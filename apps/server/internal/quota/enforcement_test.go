package quota_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"privasync/server/internal/bus"
	"privasync/server/internal/quota"
)

// CheckHeadroom reads the counters outside the transaction that later books
// them, and the whole upload happens in between. Two uploads that each pass
// the check individually can therefore both commit and leave the account over
// its limit. The booking itself has to enforce the ceiling.
func TestOnFileUploaded_RejectsGrowthPastQuota(t *testing.T) {
	env := newQuotaEnv(t)
	u := seedQuotaUser(t, env, 900, 0, 1000)

	ctx := context.Background()
	tx, err := env.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	err = env.bus.PublishCritical(ctx, tx, bus.FileUploaded{UserID: u.ID, Bytes: 200, PrevBytes: 0})
	if !errors.Is(err, quota.ErrExceeded) {
		t.Fatalf("PublishCritical = %v, want quota.ErrExceeded — the booking accepted growth past the limit", err)
	}
}

// Everything that does not grow the total must stay unaffected: trashing,
// restoring and releasing bytes still work at or above the limit, otherwise a
// full account could never be emptied again.
func TestQuotaEnforcement_LeavesNonGrowingMovesAlone(t *testing.T) {
	env := newQuotaEnv(t)
	ctx := context.Background()

	// Already over the limit, as an admin quota cut can leave an account.
	over := seedQuotaUser(t, env, 2000, 0, 1000)
	inTx(t, env.pool, func(tx pgx.Tx) error {
		return env.bus.PublishCritical(ctx, tx, bus.FileTrashed{UserID: over.ID, Bytes: 500})
	})
	inTx(t, env.pool, func(tx pgx.Tx) error {
		return env.bus.PublishCritical(ctx, tx, bus.FileRestored{UserID: over.ID, Bytes: 500})
	})
	inTx(t, env.pool, func(tx pgx.Tx) error {
		return env.bus.PublishCritical(ctx, tx, bus.FilePurgedByCron{UserID: over.ID, Bytes: 0})
	})

	got, err := env.users.FindByID(ctx, over.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.UsedBytes != 2000 || got.TrashBytes != 0 {
		t.Fatalf("counters = used %d / trash %d, want 2000 / 0", got.UsedBytes, got.TrashBytes)
	}
}

// The invariant that matters: parallel uploads that each fit on their own
// must not add up past the limit. Postgres serialises them on the user row,
// so the second booking sees the first one's result.
func TestQuotaEnforcement_ParallelUploadsCannotOvershoot(t *testing.T) {
	env := newQuotaEnv(t)
	// Room for exactly one of the three uploads below.
	u := seedQuotaUser(t, env, 0, 0, 500)

	const uploads = 3
	var wg sync.WaitGroup
	results := make([]error, uploads)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			tx, err := env.pool.Begin(ctx)
			if err != nil {
				results[i] = err
				return
			}
			defer tx.Rollback(ctx)
			if err := env.bus.PublishCritical(ctx, tx, bus.FileUploaded{UserID: u.ID, Bytes: 400}); err != nil {
				results[i] = err
				return
			}
			results[i] = tx.Commit(ctx)
		}(i)
	}
	wg.Wait()

	accepted := 0
	for i, err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, quota.ErrExceeded):
		default:
			t.Fatalf("upload %d failed unexpectedly: %v", i, err)
		}
	}
	if accepted != 1 {
		t.Fatalf("%d of %d uploads were accepted, want exactly 1", accepted, uploads)
	}

	got, err := env.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.UsedBytes+got.TrashBytes > got.QuotaBytes {
		t.Fatalf("account is over quota: %d of %d bytes", got.UsedBytes+got.TrashBytes, got.QuotaBytes)
	}
}
