package repository_test

import (
	"context"
	"testing"

	"privasync/server/internal/repository"
	"privasync/server/internal/storage"
)

func TestSumSyncRootSizes_CountsActiveAndTrashSeparately(t *testing.T) {
	pool := getPool(t)
	stor := storage.NewService(t.TempDir())
	roots := repository.NewSyncRootRepository(pool, stor)
	trash := repository.NewTrashRepository(pool)
	files := repository.NewFileRepository(pool)
	u := createTestUser(t)
	sr := createTestSyncRoot(t, u.ID)

	active := fileAt(u.ID, sr.ID, "/active.bin")
	active.Size = 100
	if _, err := files.Upsert(context.Background(), active); err != nil {
		t.Fatalf("upsert active file: %v", err)
	}

	trashed := fileAt(u.ID, sr.ID, "/trashed.bin")
	trashed.Size = 50
	if _, err := files.Upsert(context.Background(), trashed); err != nil {
		t.Fatalf("upsert file to trash: %v", err)
	}
	if _, err := trash.SoftDelete(context.Background(), sr.ID, "/trashed.bin", "trash/trashed.bin", u.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	sizes, err := roots.SumSyncRootSizes(context.Background(), sr.ID)
	if err != nil {
		t.Fatalf("SumSyncRootSizes: %v", err)
	}
	if sizes.UsedBytes != 100 {
		t.Errorf("UsedBytes = %d, want 100", sizes.UsedBytes)
	}
	if sizes.TrashBytes != 50 {
		t.Errorf("TrashBytes = %d, want 50", sizes.TrashBytes)
	}
}

func TestGetSyncRootByID_SyncRootRepository(t *testing.T) {
	pool := getPool(t)
	stor := storage.NewService(t.TempDir())
	roots := repository.NewSyncRootRepository(pool, stor)
	u := createTestUser(t)
	sr := createTestSyncRoot(t, u.ID)

	got, err := roots.GetSyncRootByID(context.Background(), sr.ID)
	if err != nil {
		t.Fatalf("GetSyncRootByID: %v", err)
	}
	if got.ID != sr.ID || got.UserID != u.ID {
		t.Fatalf("GetSyncRootByID = %+v, want id %v owned by %v", got, sr.ID, u.ID)
	}

	if _, err := roots.GetSyncRootByID(context.Background(), u.ID); err != repository.ErrNotFound {
		t.Fatalf("GetSyncRootByID unknown id: err = %v, want ErrNotFound", err)
	}
}
