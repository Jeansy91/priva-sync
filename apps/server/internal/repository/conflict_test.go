package repository_test

import (
	"context"
	"testing"
	"time"

	"privasync/server/internal/repository"
)

func TestResolveConflict_MarksResolvedAndScopesToOwner(t *testing.T) {
	pool := getPool(t)
	files := repository.NewFileRepository(pool)
	conflicts := repository.NewConflictRepository(pool)
	u := createTestUser(t)
	other := createTestUser(t)
	sr := createTestSyncRoot(t, u.ID)

	f, err := files.Upsert(context.Background(), fileAt(u.ID, sr.ID, "/conflicted.txt"))
	if err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	c, err := conflicts.CreateConflict(context.Background(), f.ID, "/conflicted.txt.conflict-1", nil, nil)
	if err != nil {
		t.Fatalf("CreateConflict: %v", err)
	}

	if _, err := conflicts.ResolveConflict(context.Background(), c.ID, other.ID); err != repository.ErrNotFound {
		t.Fatalf("ResolveConflict by non-owner: err = %v, want ErrNotFound", err)
	}

	rc, err := conflicts.ResolveConflict(context.Background(), c.ID, u.ID)
	if err != nil {
		t.Fatalf("ResolveConflict: %v", err)
	}
	if rc.FileID != f.ID || rc.ConflictPath != "/conflicted.txt.conflict-1" {
		t.Fatalf("ResolveConflict result = %+v, want file %v path /conflicted.txt.conflict-1", rc, f.ID)
	}

	if _, err := conflicts.ResolveConflict(context.Background(), c.ID, u.ID); err != repository.ErrNotFound {
		t.Fatalf("ResolveConflict twice: err = %v, want ErrNotFound (already resolved)", err)
	}
}

// The loser copy's own mtime travels with the conflict so a resolve dialog can
// show it next to the current version's; conflicts without one read back nil.
func TestFindOpenConflictDetail_ReturnsLoserMTime(t *testing.T) {
	pool := getPool(t)
	files := repository.NewFileRepository(pool)
	conflicts := repository.NewConflictRepository(pool)
	u := createTestUser(t)
	sr := createTestSyncRoot(t, u.ID)

	f, err := files.Upsert(context.Background(), fileAt(u.ID, sr.ID, "/mtime.txt"))
	if err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	loser := time.Date(2026, 11, 3, 14, 5, 9, 0, time.UTC)
	withMTime, err := conflicts.CreateConflict(context.Background(), f.ID, "/mtime.txt.conflict-1", nil, &loser)
	if err != nil {
		t.Fatalf("CreateConflict: %v", err)
	}
	without, err := conflicts.CreateConflict(context.Background(), f.ID, "/mtime.txt.conflict-2", nil, nil)
	if err != nil {
		t.Fatalf("CreateConflict: %v", err)
	}

	d, err := conflicts.FindOpenConflictDetail(context.Background(), withMTime.ID)
	if err != nil {
		t.Fatalf("FindOpenConflictDetail: %v", err)
	}
	if d.LoserMTime == nil || !d.LoserMTime.Equal(loser) {
		t.Fatalf("LoserMTime = %v, want %v", d.LoserMTime, loser)
	}
	d, err = conflicts.FindOpenConflictDetail(context.Background(), without.ID)
	if err != nil {
		t.Fatalf("FindOpenConflictDetail: %v", err)
	}
	if d.LoserMTime != nil {
		t.Fatalf("LoserMTime = %v, want nil", d.LoserMTime)
	}
}
