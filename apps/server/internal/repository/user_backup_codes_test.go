package repository_test

import (
	"context"
	"testing"

	"privasync/server/internal/repository"
)

func TestBackupCodes_ReplaceListConsume(t *testing.T) {
	pool := getPool(t)
	users := repository.NewUserRepository(pool, nil)
	user := createTestUser(t)
	ctx := context.Background()

	if err := users.ReplaceBackupCodes(ctx, user.ID, []string{"code-a", "code-b", "code-c"}); err != nil {
		t.Fatalf("ReplaceBackupCodes: %v", err)
	}

	codes, err := users.ListBackupCodes(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListBackupCodes: %v", err)
	}
	if len(codes) != 3 {
		t.Fatalf("ListBackupCodes returned %d codes, want 3", len(codes))
	}
	for _, c := range codes {
		if c.UsedAt != nil {
			t.Errorf("fresh code %s has UsedAt set", c.ID)
		}
	}

	if err := users.ConsumeBackupCode(ctx, user.ID, "code-b"); err != nil {
		t.Fatalf("ConsumeBackupCode: %v", err)
	}

	after, err := users.ListBackupCodes(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListBackupCodes (after consume): %v", err)
	}
	usedCount := 0
	for _, c := range after {
		if c.UsedAt != nil {
			usedCount++
		}
	}
	if usedCount != 1 {
		t.Fatalf("used code count = %d, want 1", usedCount)
	}
}

func TestBackupCodes_ConsumeUnknownCodeNotFound(t *testing.T) {
	users := repository.NewUserRepository(getPool(t), nil)
	user := createTestUser(t)

	if err := users.ReplaceBackupCodes(context.Background(), user.ID, []string{"only-code"}); err != nil {
		t.Fatalf("ReplaceBackupCodes: %v", err)
	}

	err := users.ConsumeBackupCode(context.Background(), user.ID, "not-a-real-code")
	if err != repository.ErrNotFound {
		t.Fatalf("ConsumeBackupCode(unknown) = %v, want ErrNotFound", err)
	}
}

func TestBackupCodes_ConsumeAlreadyUsedCodeNotFound(t *testing.T) {
	users := repository.NewUserRepository(getPool(t), nil)
	user := createTestUser(t)
	ctx := context.Background()

	if err := users.ReplaceBackupCodes(ctx, user.ID, []string{"single-use"}); err != nil {
		t.Fatalf("ReplaceBackupCodes: %v", err)
	}
	if err := users.ConsumeBackupCode(ctx, user.ID, "single-use"); err != nil {
		t.Fatalf("ConsumeBackupCode (first): %v", err)
	}
	if err := users.ConsumeBackupCode(ctx, user.ID, "single-use"); err != repository.ErrNotFound {
		t.Fatalf("ConsumeBackupCode (replay) = %v, want ErrNotFound", err)
	}
}

func TestBackupCodes_ReplaceClearsPrevious(t *testing.T) {
	users := repository.NewUserRepository(getPool(t), nil)
	user := createTestUser(t)
	ctx := context.Background()

	if err := users.ReplaceBackupCodes(ctx, user.ID, []string{"gen1-a", "gen1-b"}); err != nil {
		t.Fatalf("ReplaceBackupCodes (gen1): %v", err)
	}
	if err := users.ReplaceBackupCodes(ctx, user.ID, []string{"gen2-a"}); err != nil {
		t.Fatalf("ReplaceBackupCodes (gen2): %v", err)
	}

	codes, err := users.ListBackupCodes(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListBackupCodes: %v", err)
	}
	if len(codes) != 1 {
		t.Fatalf("ListBackupCodes after replace = %d codes, want 1 (old generation cleared)", len(codes))
	}
	if err := users.ConsumeBackupCode(ctx, user.ID, "gen1-a"); err != repository.ErrNotFound {
		t.Fatalf("ConsumeBackupCode(old generation) = %v, want ErrNotFound", err)
	}
}
