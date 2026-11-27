package repository_test

import (
	"context"
	"path"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

// levelEnv seeds one sync root with the given paths and returns a lister.
// A path ending in "/" is created as an explicit directory, the way an empty
// folder arrives; the directories above a file path are materialised by the
// repository itself (ADR-0041).
func levelEnv(t *testing.T, paths ...string) (*repository.FileRepository, uuid.UUID) {
	t.Helper()
	pool := getPool(t)
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	for _, p := range paths {
		f := fileAt(u.ID, root.ID, p)
		if n := len(p); n > 0 && p[n-1] == '/' {
			f.Path = p[:n-1]
			f.IsDir = true
			f.Size = 0
		}
		if _, err := files.Upsert(context.Background(), f); err != nil {
			t.Fatalf("upsert %s: %v", p, err)
		}
	}
	return files, root.ID
}

// entryShape is what a caller actually consumes from a level listing.
type entryShape struct {
	Name  string
	IsDir bool
}

func shapes(entries []model.File) []entryShape {
	out := make([]entryShape, 0, len(entries))
	for _, e := range entries {
		out = append(out, entryShape{Name: path.Base(e.Path), IsDir: e.IsDir})
	}
	return out
}

func listLevel(t *testing.T, files *repository.FileRepository, rootID uuid.UUID, prefix, cursor string, limit int) []model.File {
	t.Helper()
	entries, err := files.ListDirectoryLevel(context.Background(), repository.ListDirectoryLevelParams{
		SyncRootID: rootID,
		PathPrefix: prefix,
		Cursor:     cursor,
		Limit:      limit,
	})
	if err != nil {
		t.Fatalf("ListDirectoryLevel(%q, cursor=%q): %v", prefix, cursor, err)
	}
	return entries
}

func assertShapes(t *testing.T, got []model.File, want []entryShape) {
	t.Helper()
	g := shapes(got)
	if len(g) != len(want) {
		t.Fatalf("got %d entries %+v, want %d %+v", len(g), g, len(want), want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, g[i], want[i])
		}
	}
}

func TestListDirectoryLevel_Shapes(t *testing.T) {
	files, rootID := levelEnv(t,
		"/readme.txt",       // plain file at the root
		"/docs/guide.md",    // the directory above it is materialised
		"/docs/sub/deep.md", // two levels of it
		"/empty/",           // explicit directory, no children
		"/mixed/",           // explicit directory that also has children
		"/mixed/inside.txt",
		"/doc.txt", // name adjacent to "docs" -- must not be conflated
	)

	t.Run("root level", func(t *testing.T) {
		assertShapes(t, listLevel(t, files, rootID, "/", "", 100), []entryShape{
			{Name: "doc.txt", IsDir: false},
			{Name: "docs", IsDir: true},
			{Name: "empty", IsDir: true},
			{Name: "mixed", IsDir: true},
			{Name: "readme.txt", IsDir: false},
		})
	})

	t.Run("inside a materialised directory", func(t *testing.T) {
		assertShapes(t, listLevel(t, files, rootID, "/docs", "", 100), []entryShape{
			{Name: "guide.md", IsDir: false},
			{Name: "sub", IsDir: true},
		})
	})

	t.Run("inside an explicit directory with children", func(t *testing.T) {
		assertShapes(t, listLevel(t, files, rootID, "/mixed", "", 100), []entryShape{
			{Name: "inside.txt", IsDir: false},
		})
	})

	t.Run("inside an explicit empty directory", func(t *testing.T) {
		assertShapes(t, listLevel(t, files, rootID, "/empty", "", 100), []entryShape{})
	})
}

// The keyset cursor pages on the entry name.
func TestListDirectoryLevel_CursorPaging(t *testing.T) {
	files, rootID := levelEnv(t, "/a.txt", "/b.txt", "/c.txt", "/d.txt")

	first := listLevel(t, files, rootID, "/", "", 2)
	assertShapes(t, first, []entryShape{{Name: "a.txt"}, {Name: "b.txt"}})

	second := listLevel(t, files, rootID, "/", path.Base(first[len(first)-1].Path), 2)
	assertShapes(t, second, []entryShape{{Name: "c.txt"}, {Name: "d.txt"}})
}

// A soft-deleted row must not appear.
func TestListDirectoryLevel_IgnoresDeleted(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	for _, p := range []string{"/keep.txt", "/gone.txt"} {
		if _, err := files.Upsert(ctx, fileAt(u.ID, root.ID, p)); err != nil {
			t.Fatalf("upsert %s: %v", p, err)
		}
	}
	if _, err := pool.Exec(ctx,
		`UPDATE files SET is_deleted = TRUE WHERE sync_root_id = $1 AND path = '/gone.txt'`,
		root.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	assertShapes(t, listLevel(t, files, root.ID, "/", "", 100), []entryShape{
		{Name: "keep.txt"},
	})
}

// Losing its last child no longer removes a directory. It was never a
// decision that empty folders vanish -- it fell out of inferring the tree
// from file paths, and inferring is what ADR-0041 stopped doing.
func TestListDirectoryLevel_DirectorySurvivesItsLastChild(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	if _, err := files.Upsert(ctx, fileAt(u.ID, root.ID, "/box/child.txt")); err != nil {
		t.Fatalf("upsert child: %v", err)
	}
	assertShapes(t, listLevel(t, files, root.ID, "/", "", 100),
		[]entryShape{{Name: "box", IsDir: true}})

	if _, err := pool.Exec(ctx,
		`UPDATE files SET is_deleted = TRUE WHERE sync_root_id = $1 AND path = '/box/child.txt'`,
		root.ID); err != nil {
		t.Fatalf("soft delete child: %v", err)
	}

	if after := listLevel(t, files, root.ID, "/box", "", 100); len(after) != 0 {
		t.Fatalf("deleted child still listed: %+v", shapes(after))
	}
	assertShapes(t, listLevel(t, files, root.ID, "/", "", 100),
		[]entryShape{{Name: "box", IsDir: true}})
}
