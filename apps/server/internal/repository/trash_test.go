package repository_test

import (
	"context"
	"testing"
	"time"

	"privasync/server/internal/repository"
)

func TestListTrashedBefore_ScopesToUserAndCutoff(t *testing.T) {
	pool := getPool(t)
	files := repository.NewFileRepository(pool)
	trashRepo := repository.NewTrashRepository(pool)
	u := createTestUser(t)
	other := createTestUser(t)
	sr := createTestSyncRoot(t, u.ID)

	f, err := files.Upsert(context.Background(), fileAt(u.ID, sr.ID, "/old.bin"))
	if err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	if _, err := trashRepo.SoftDelete(context.Background(), sr.ID, "/old.bin", "trash/old.bin", u.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	_ = f

	future := time.Now().Add(time.Hour)
	got, err := trashRepo.ListTrashedBefore(context.Background(), u.ID, future)
	if err != nil {
		t.Fatalf("ListTrashedBefore: %v", err)
	}
	if len(got) != 1 || got[0].Path != "/old.bin" {
		t.Fatalf("ListTrashedBefore = %+v, want exactly /old.bin", got)
	}

	past := time.Now().Add(-time.Hour)
	got, err = trashRepo.ListTrashedBefore(context.Background(), u.ID, past)
	if err != nil {
		t.Fatalf("ListTrashedBefore (cutoff before delete): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListTrashedBefore with cutoff before delete = %+v, want none", got)
	}

	got, err = trashRepo.ListTrashedBefore(context.Background(), other.ID, future)
	if err != nil {
		t.Fatalf("ListTrashedBefore (other user): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListTrashedBefore for unrelated user = %+v, want none", got)
	}
}
