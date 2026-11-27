package repository_test

import (
	"context"
	"testing"

	"privasync/server/internal/repository"
	"privasync/server/internal/storage"
)

func TestSetShowFileCheckboxes_PersistsToggle(t *testing.T) {
	pool := getPool(t)
	stor := storage.NewService(t.TempDir())
	users := repository.NewUserRepository(pool, stor)
	u := createTestUser(t)

	if err := users.SetShowFileCheckboxes(context.Background(), u.ID, true); err != nil {
		t.Fatalf("SetShowFileCheckboxes(true): %v", err)
	}
	got, err := users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if !got.ShowFileCheckboxes {
		t.Fatal("ShowFileCheckboxes = false after SetShowFileCheckboxes(true)")
	}

	if err := users.SetShowFileCheckboxes(context.Background(), u.ID, false); err != nil {
		t.Fatalf("SetShowFileCheckboxes(false): %v", err)
	}
	got, err = users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.ShowFileCheckboxes {
		t.Fatal("ShowFileCheckboxes = true after SetShowFileCheckboxes(false)")
	}
}
