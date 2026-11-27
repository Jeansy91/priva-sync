package repository_test

import (
	"context"
	"testing"

	"privasync/server/internal/repository"
)

// FindByPaths backs the ADR-0062 stat endpoint: a retry must be able to tell
// "server never had this path" apart from "server had it and deleted it" --
// unlike FindByPath, which folds both cases into ErrNotFound.
func TestFindByPaths_DistinguishesFoundDeletedAndUnknown(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	if _, err := files.Upsert(ctx, fileAt(u.ID, root.ID, "/keep.txt")); err != nil {
		t.Fatalf("upsert keep.txt: %v", err)
	}
	if _, err := files.Upsert(ctx, fileAt(u.ID, root.ID, "/gone.txt")); err != nil {
		t.Fatalf("upsert gone.txt: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE files SET is_deleted = TRUE WHERE sync_root_id = $1 AND path = '/gone.txt'`,
		root.ID); err != nil {
		t.Fatalf("soft delete gone.txt: %v", err)
	}

	got, err := files.FindByPaths(ctx, root.ID, []string{"/keep.txt", "/gone.txt", "/never.txt"})
	if err != nil {
		t.Fatalf("FindByPaths: %v", err)
	}

	byPath := make(map[string]bool, len(got)) // path -> is_deleted
	for _, f := range got {
		byPath[f.Path] = f.IsDeleted
	}

	if len(got) != 2 {
		t.Fatalf("FindByPaths returned %d row(s) = %+v, want 2 (keep.txt + gone.txt, never.txt absent)", len(got), got)
	}
	if deleted, ok := byPath["/keep.txt"]; !ok || deleted {
		t.Fatalf("keep.txt: present=%v is_deleted=%v, want present=true is_deleted=false", ok, deleted)
	}
	if deleted, ok := byPath["/gone.txt"]; !ok || !deleted {
		t.Fatalf("gone.txt: present=%v is_deleted=%v, want present=true is_deleted=true", ok, deleted)
	}
	if _, ok := byPath["/never.txt"]; ok {
		t.Fatalf("never.txt must be absent from the result, not a row -- the caller distinguishes it by its absence")
	}
}

func TestFindByPaths_EmptyPathListReturnsEmptyResult(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	got, err := files.FindByPaths(ctx, root.ID, []string{})
	if err != nil {
		t.Fatalf("FindByPaths with empty list: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("FindByPaths with empty list = %+v, want empty", got)
	}
}

// A path list must only ever surface rows for the requested sync root, even
// if another root happens to have a file at the same path.
func TestFindByPaths_ScopedToSyncRoot(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	rootA := createTestSyncRoot(t, u.ID)
	rootB := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	if _, err := files.Upsert(ctx, fileAt(u.ID, rootA.ID, "/shared-name.txt")); err != nil {
		t.Fatalf("upsert into rootA: %v", err)
	}
	if _, err := files.Upsert(ctx, fileAt(u.ID, rootB.ID, "/shared-name.txt")); err != nil {
		t.Fatalf("upsert into rootB: %v", err)
	}

	got, err := files.FindByPaths(ctx, rootA.ID, []string{"/shared-name.txt"})
	if err != nil {
		t.Fatalf("FindByPaths: %v", err)
	}
	if len(got) != 1 || got[0].SyncRootID != rootA.ID {
		t.Fatalf("FindByPaths(rootA) = %+v, want exactly one row scoped to rootA", got)
	}
}
