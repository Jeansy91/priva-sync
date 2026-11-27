package repository_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/repository"
	"privasync/server/internal/storage"
)

func newSyncRootRepo(t *testing.T) *repository.SyncRootRepository {
	t.Helper()
	return repository.NewSyncRootRepository(getPool(t), storage.NewService(t.TempDir()))
}

func TestCreateSyncRoot_DuplicateNameConflict(t *testing.T) {
	repo := newSyncRootRepo(t)
	u := createTestUser(t)
	ctx := context.Background()

	name := "dup_" + uniqueSuffix()
	if _, err := repo.CreateSyncRoot(ctx, u.ID, name, "/data/a"); err != nil {
		t.Fatalf("first CreateSyncRoot: %v", err)
	}
	if _, err := repo.CreateSyncRoot(ctx, u.ID, name, "/data/b"); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("duplicate name must yield ErrConflict, got %v", err)
	}
}

func TestGetSyncRoot_NotFound(t *testing.T) {
	repo := newSyncRootRepo(t)
	ctx := context.Background()
	if _, err := repo.GetSyncRoot(ctx, uuid.New(), uuid.New()); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("GetSyncRoot(unknown) must be ErrNotFound, got %v", err)
	}
	if _, err := repo.GetSyncRootByID(ctx, uuid.New()); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("GetSyncRootByID(unknown) must be ErrNotFound, got %v", err)
	}
}

func TestUpdateSyncRootName_NotOwned(t *testing.T) {
	repo := newSyncRootRepo(t)
	u := createTestUser(t)
	sr := createTestSyncRoot(t, u.ID)
	ctx := context.Background()
	// A different user cannot rename it -> no matching row -> ErrNotFound.
	if _, err := repo.UpdateSyncRootName(ctx, sr.ID, uuid.New(), "x"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("rename by non-owner must be ErrNotFound, got %v", err)
	}
}

func TestDeleteSyncRoot_RemovesRootAndFiles(t *testing.T) {
	pool := getPool(t)
	repo := newSyncRootRepo(t)
	files := repository.NewFileRepository(pool)
	u := createTestUser(t)
	sr := createTestSyncRoot(t, u.ID)
	ctx := context.Background()

	if _, err := files.Upsert(ctx, fileAt(u.ID, sr.ID, "/keep.txt")); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := repo.DeleteSyncRoot(ctx, sr.ID, u.ID); err != nil {
		t.Fatalf("DeleteSyncRoot: %v", err)
	}
	if _, err := repo.GetSyncRoot(ctx, sr.ID, u.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("root must be gone after delete, got %v", err)
	}
	var remaining int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM files WHERE sync_root_id = $1`, sr.ID).Scan(&remaining)
	if remaining != 0 {
		t.Fatalf("files must be cascade-deleted, %d remain", remaining)
	}
}

func TestDeleteSyncRoot_NotOwned(t *testing.T) {
	repo := newSyncRootRepo(t)
	u := createTestUser(t)
	sr := createTestSyncRoot(t, u.ID)
	ctx := context.Background()
	if err := repo.DeleteSyncRoot(ctx, sr.ID, uuid.New()); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("delete by non-owner must be ErrNotFound, got %v", err)
	}
}
