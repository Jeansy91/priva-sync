package repository_test

import (
	"context"
	"path"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

// A stored path is data, not a pattern. Fed into `LIKE` unescaped, "_" matches
// any single character and "%" matches anything at all, so a directory named
// "a_b" reaches into its neighbour "axb". Underscores in folder names
// are ordinary, so this is not an exotic case.
func likePrefixEnv(t *testing.T) (*repository.FileRepository, uuid.UUID, uuid.UUID) {
	t.Helper()
	pool := getPool(t)
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	for _, p := range []string{
		"/a_b/own.txt",   // the directory we ask about
		"/axb/other.txt", // differs exactly where "_" is a wildcard
		"/pct%/own.txt",  // "%" would swallow every sibling
		"/pctZZ/other.txt",
	} {
		if _, err := files.Upsert(context.Background(), fileAt(u.ID, root.ID, p)); err != nil {
			t.Fatalf("upsert %s: %v", p, err)
		}
	}
	return files, root.ID, u.ID
}

func TestLikePrefix_ListDirectoryLevelStaysInsideItsDirectory(t *testing.T) {
	files, rootID, _ := likePrefixEnv(t)

	for _, tt := range []struct{ prefix, want string }{
		{prefix: "/a_b", want: "own.txt"},
		{prefix: "/pct%", want: "own.txt"},
	} {
		t.Run(tt.prefix, func(t *testing.T) {
			got := listLevel(t, files, rootID, tt.prefix, "", 100)
			if len(got) != 1 || path.Base(got[0].Path) != tt.want {
				t.Fatalf("ListDirectoryLevel(%q) = %+v, want exactly [%s]",
					tt.prefix, shapes(got), tt.want)
			}
		})
	}
}

// An empty directory must not borrow its neighbour's contents.
func TestLikePrefix_EmptyDirectoryDoesNotBorrowFromNeighbours(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	empty := fileAt(u.ID, root.ID, "/a_b")
	empty.IsDir = true
	empty.Size = 0
	if _, err := files.Upsert(ctx, empty); err != nil {
		t.Fatalf("upsert dir: %v", err)
	}
	if _, err := files.Upsert(ctx, fileAt(u.ID, root.ID, "/axb/child.txt")); err != nil {
		t.Fatalf("upsert neighbour child: %v", err)
	}

	if got := listLevel(t, files, root.ID, "/a_b", "", 100); len(got) != 0 {
		t.Fatalf("/a_b should be empty, got %+v — it borrowed a neighbour's child", shapes(got))
	}
	assertShapes(t, listLevel(t, files, root.ID, "/axb", "", 100),
		[]entryShape{{Name: "child.txt"}})
}

// collectSubtree drains the paged walk into a slice. Production code streams
// it; a test that asserts on the whole subtree wants it in one piece.
func collectSubtree(t *testing.T, files *repository.FileRepository, rootID uuid.UUID, dirPath string) []model.File {
	t.Helper()
	var out []model.File
	err := files.EachInSubtree(context.Background(), rootID, dirPath, func(f model.File) error {
		out = append(out, f)
		return nil
	})
	if err != nil {
		t.Fatalf("EachInSubtree(%q): %v", dirPath, err)
	}
	return out
}

// pathsOf is what a subtree query actually handed back, so a scope failure
// names the intruder instead of a row count.
func pathsOf(files []model.File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

func wantPaths(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

// The subtree walk backs cascade-delete: an over-matching prefix there
// deletes rows out of an unrelated directory. The directory's own row is part
// of the subtree (ADR-0041); the neighbour's is not.
func TestLikePrefix_SubtreeWalkStaysInsideItsDirectory(t *testing.T) {
	files, rootID, _ := likePrefixEnv(t)

	for _, prefix := range []string{"/a_b", "/pct%"} {
		wantPaths(t, "EachInSubtree("+prefix+")", pathsOf(collectSubtree(t, files, rootID, prefix)),
			[]string{prefix, prefix + "/own.txt"})
	}
}

func TestLikePrefix_ListChangedFilesSinceUnderPrefixStaysInsideItsDirectory(t *testing.T) {
	files, rootID, _ := likePrefixEnv(t)
	ctx := context.Background()

	for _, prefix := range []string{"/a_b", "/pct%"} {
		got, err := files.ListChangedFilesSinceUnderPrefix(ctx, rootID, prefix, 0, 100)
		if err != nil {
			t.Fatalf("ListChangedFilesSinceUnderPrefix(%q): %v", prefix, err)
		}
		wantPaths(t, "ListChangedFilesSinceUnderPrefix("+prefix+")", pathsOf(got),
			[]string{prefix, prefix + "/own.txt"})
	}
}

// The escape character needs escaping too. A path ending in one would
// otherwise leave a dangling escape and make Postgres reject the pattern
// outright -- a 500 instead of a listing.
func TestLikePrefix_BackslashInPathIsEscaped(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	bs := string(rune(92)) // a literal backslash, as a file name may contain
	for _, p := range []string{"/back" + bs + "slash/own.txt", "/backXslash/other.txt"} {
		if _, err := files.Upsert(ctx, fileAt(u.ID, root.ID, p)); err != nil {
			t.Fatalf("upsert %s: %v", p, err)
		}
	}

	got := listLevel(t, files, root.ID, "/back"+bs+"slash", "", 100)
	if len(got) != 1 || path.Base(got[0].Path) != "own.txt" {
		t.Fatalf("listing a directory with a backslash = %+v, want exactly [own.txt]", shapes(got))
	}

	wantPaths(t, "EachInSubtree", pathsOf(collectSubtree(t, files, root.ID, "/back"+bs+"slash")),
		[]string{"/back" + bs + "slash", "/back" + bs + "slash/own.txt"})
}
