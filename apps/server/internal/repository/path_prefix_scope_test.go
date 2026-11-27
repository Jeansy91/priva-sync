package repository_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

// It was established that a stored path fed into LIKE is read as a pattern, so
// a directory named "a_b" reaches into its sibling "axb". The fix covered the
// listing queries only. Every other query that scopes by path prefix has the
// same shape, and the ones below are worse than a wrong listing: two of them
// rewrite rows, and two of them cross a share boundary.
//
// "a_b" is not an exotic name. Underscores in folder names are ordinary.
func neighbourEnv(t *testing.T) (*repository.FileRepository, *model.User, *model.SyncRoot) {
	t.Helper()
	pool := getPool(t)
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	for _, p := range []string{"/a_b/own.txt", "/axb/other.txt"} {
		if _, err := files.Upsert(context.Background(), fileAt(u.ID, root.ID, p)); err != nil {
			t.Fatalf("upsert %s: %v", p, err)
		}
	}
	return files, u, root
}

// livePaths is the ground truth the mutation tests compare against: what the
// root actually holds afterwards, not what the mutation claims it did.
func livePaths(t *testing.T, rootID uuid.UUID) []string {
	t.Helper()
	rows, err := getPool(t).Query(context.Background(),
		`SELECT path FROM files WHERE sync_root_id = $1 AND is_deleted = FALSE ORDER BY path`, rootID)
	if err != nil {
		t.Fatalf("read live paths: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatalf("scan path: %v", err)
		}
		out = append(out, p)
	}
	return out
}

// Moving "/a_b" must not move "/axb" with it. This one rewrites paths and
// writes tombstones, so an over-matching prefix is data loss from the
// neighbour's point of view: their file is gone from where it was and now
// lives under a directory they never touched.
func TestPathPrefixScope_MoveDirectoryLeavesNeighboursAlone(t *testing.T) {
	files, u, root := neighbourEnv(t)

	if _, err := files.MoveDirectory(context.Background(), root.ID, "/a_b", "/moved", u.ID); err != nil {
		t.Fatalf("MoveDirectory: %v", err)
	}

	wantPaths(t, "after moving /a_b the root", livePaths(t, root.ID),
		[]string{"/axb", "/axb/other.txt", "/moved", "/moved/own.txt"})
}

// Copying "/a_b" must not duplicate "/axb" underneath the destination.
func TestPathPrefixScope_CopyDirectoryLeavesNeighboursAlone(t *testing.T) {
	files, u, root := neighbourEnv(t)

	if _, err := files.CopyDirectory(context.Background(), root.ID, "/a_b", "/copy", u.ID); err != nil {
		t.Fatalf("CopyDirectory: %v", err)
	}

	wantPaths(t, "after copying /a_b the root", livePaths(t, root.ID),
		[]string{"/a_b", "/a_b/own.txt", "/axb", "/axb/other.txt", "/copy", "/copy/own.txt"})
}

// A Shared-Mount recipient sees the conflicts inside share.path and nothing
// else (ADR-0011). An over-matching prefix hands them the giver's conflict
// paths from a directory that was never shared -- the path itself is the
// disclosure.
func TestPathPrefixScope_ListOpenConflictsUnderPrefixStaysInsideTheShare(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	files, u, root := neighbourEnv(t)
	conflicts := repository.NewConflictRepository(pool)

	for _, p := range []string{"/a_b/own.txt", "/axb/other.txt"} {
		f, err := files.FindByPath(ctx, root.ID, p)
		if err != nil {
			t.Fatalf("find %s: %v", p, err)
		}
		if _, err := conflicts.CreateConflict(ctx, f.ID, p+".conflict", nil, nil); err != nil {
			t.Fatalf("create conflict for %s: %v", p, err)
		}
	}

	got, err := conflicts.ListOpenConflictsUnderPrefix(ctx, u.ID, root.ID, "/a_b")
	if err != nil {
		t.Fatalf("ListOpenConflictsUnderPrefix: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("a share on /a_b exposes %d conflicts, want 1 -- it reached into /axb", len(got))
	}
	if got[0].ConflictPath != "/a_b/own.txt.conflict" {
		t.Fatalf("exposed conflict path %q, want the one inside the share", got[0].ConflictPath)
	}
}

// Search is scoped to a prefix for the same reason: inside a Shared Mount the
// prefix is the share boundary.
func TestPathPrefixScope_SearchFilesStaysInsideItsPrefix(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	for _, p := range []string{"/a_b/hit.txt", "/axb/hit.txt"} {
		if _, err := files.Upsert(ctx, fileAt(u.ID, root.ID, p)); err != nil {
			t.Fatalf("upsert %s: %v", p, err)
		}
	}

	got, err := files.SearchFiles(ctx, repository.SearchFilesParams{
		SyncRootID:  root.ID,
		PathPrefix:  "/a_b",
		LikePattern: "hit",
		Limit:       100,
	})
	if err != nil {
		t.Fatalf("SearchFiles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("search under /a_b returned %d hits, want 1 -- it reached into /axb", len(got))
	}
	if got[0].Path != "/a_b/hit.txt" {
		t.Fatalf("search under /a_b returned %q", got[0].Path)
	}
}

// The stats a directory shows are cosmetic next to the two above, but they
// come from the same predicate and would be silently wrong.
func TestPathPrefixScope_DirectoryStatsCountsOnlyItsOwnSubtree(t *testing.T) {
	files, _, root := neighbourEnv(t)

	stats, err := files.DirectoryStats(context.Background(), root.ID, "/a_b")
	if err != nil {
		t.Fatalf("DirectoryStats: %v", err)
	}
	if stats.FileCount != 1 || stats.ItemCount != 1 {
		t.Fatalf("DirectoryStats(/a_b) = %d files / %d items, want 1/1", stats.FileCount, stats.ItemCount)
	}
}
