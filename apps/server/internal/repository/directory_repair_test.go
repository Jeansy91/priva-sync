package repository_test

import (
	"context"
	"testing"

	"privasync/server/internal/repository"
)

// The sweep is the net under ADR-0041, so what matters is that it notices a
// directory row that is gone and puts it back -- and that it leaves the one
// case it must not touch alone.
func TestMaterialiseMissingDirectories_RestoresAPurgedDirectory(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	if _, err := files.Upsert(ctx, fileAt(u.ID, root.ID, "/a/b/file.txt")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Both directory rows vanish, as a purge that outran its children would
	// leave them.
	if _, err := pool.Exec(ctx,
		`DELETE FROM files WHERE sync_root_id = $1 AND is_dir = TRUE`, root.ID); err != nil {
		t.Fatalf("purge directories: %v", err)
	}
	if got := liveDirs(t, root.ID); len(got) != 0 {
		t.Fatalf("setup left directories behind: %v", got)
	}

	created, blocked, err := files.MaterialiseMissingDirectories(ctx)
	if err != nil {
		t.Fatalf("MaterialiseMissingDirectories: %v", err)
	}
	if created < 2 {
		t.Errorf("created %d rows, want at least the two that were missing", created)
	}
	if len(blocked) != 0 {
		t.Errorf("reported %v as blocked, want none", blocked)
	}
	wantPaths(t, "directories after repair", liveDirs(t, root.ID), []string{"/a", "/a/b"})

	// Running it again changes nothing. The counts the sweep returns are
	// repository-wide, so the assertion is scoped to this root instead.
	if _, _, err := files.MaterialiseMissingDirectories(ctx); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	wantPaths(t, "directories after a second repair", liveDirs(t, root.ID), []string{"/a", "/a/b"})
}

// A live file where a directory would have to go is reported, never
// overwritten -- the sweep would otherwise destroy a file to satisfy an
// invariant.
func TestMaterialiseMissingDirectories_ReportsAFileInTheWay(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	blocker, err := files.Upsert(ctx, fileAt(u.ID, root.ID, "/thing"))
	if err != nil {
		t.Fatalf("upsert blocker: %v", err)
	}
	if _, err := files.Upsert(ctx, fileAt(u.ID, root.ID, "/thing/inside.txt")); err != nil {
		t.Fatalf("upsert under blocker: %v", err)
	}

	_, blocked, err := files.MaterialiseMissingDirectories(ctx)
	if err != nil {
		t.Fatalf("MaterialiseMissingDirectories: %v", err)
	}
	var named bool
	for _, p := range blocked {
		if p == "/thing" {
			named = true
		}
	}
	if !named {
		t.Errorf("blocked = %v, want /thing named", blocked)
	}

	still, err := files.FindByPath(ctx, root.ID, "/thing")
	if err != nil {
		t.Fatalf("find blocker: %v", err)
	}
	if still.ID != blocker.ID || still.IsDir {
		t.Fatalf("/thing = %+v, want the original file row untouched", still)
	}
}
