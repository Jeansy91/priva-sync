package storage_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/model"
	"privasync/server/internal/storage"
)

// DeleteUserMeta removes the on-disk users/{id}.json sidecar written by
// WriteUserMeta. Deleting an already-absent sidecar must be a no-op, not an
// error (callers may retry cleanup after a partial failure).
func TestDeleteUserMeta_RemovesExistingFile(t *testing.T) {
	dir := t.TempDir()
	s := storage.NewService(dir)
	u := &model.User{
		ID:        uuid.New(),
		Username:  "meta_test",
		Email:     "meta_test@t.local",
		CreatedAt: time.Now(),
	}
	if err := s.WriteUserMeta(u); err != nil {
		t.Fatalf("write user meta: %v", err)
	}
	metaPath := filepath.Join(dir, storage.DirUsers, u.ID.String()+".json")
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("meta file should exist before delete: %v", err)
	}

	if err := s.DeleteUserMeta(u.ID); err != nil {
		t.Fatalf("delete user meta: %v", err)
	}
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Errorf("meta file should be gone after delete, stat err = %v", err)
	}
}

func TestDeleteUserMeta_MissingFileIsNotAnError(t *testing.T) {
	s := storage.NewService(t.TempDir())
	if err := s.DeleteUserMeta(uuid.New()); err != nil {
		t.Errorf("deleting an absent meta file should be a no-op: %v", err)
	}
}
