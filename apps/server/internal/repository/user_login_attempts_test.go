package repository_test

import (
	"context"
	"testing"
	"time"

	"privasync/server/internal/repository"
	"privasync/server/internal/storage"
)

// login_attempts.ip_address is INET, so the column itself rejects anything
// that is not an address. That is why the caller must hand in a validated
// value: an unvalidated one fails the INSERT, and a discarded error then
// leaves the lockout counter standing still.
func TestRecordLoginAttempt_RejectsMalformedIP(t *testing.T) {
	pool := getPool(t)
	users := repository.NewUserRepository(pool, storage.NewService(t.TempDir()))
	u := createTestUser(t)
	ctx := context.Background()

	if err := users.RecordLoginAttempt(ctx, u.Username, "not-an-ip", false); err == nil {
		t.Fatal("RecordLoginAttempt accepted a malformed ip_address; the INET guard is gone")
	}

	count, err := users.CountFailedAttempts(ctx, u.Username, time.Minute)
	if err != nil {
		t.Fatalf("CountFailedAttempts: %v", err)
	}
	if count != 0 {
		t.Fatalf("failed attempts = %d, want 0 (the rejected insert must not have landed)", count)
	}

	if err := users.RecordLoginAttempt(ctx, u.Username, "203.0.113.7", false); err != nil {
		t.Fatalf("RecordLoginAttempt with a valid address: %v", err)
	}
	count, err = users.CountFailedAttempts(ctx, u.Username, time.Minute)
	if err != nil {
		t.Fatalf("CountFailedAttempts: %v", err)
	}
	if count != 1 {
		t.Fatalf("failed attempts = %d, want 1", count)
	}
}
