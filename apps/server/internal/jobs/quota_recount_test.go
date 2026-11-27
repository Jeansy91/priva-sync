package jobs_test

import (
	"context"
	"testing"

	"privasync/server/internal/bus"
	"privasync/server/internal/jobs"
	"privasync/server/internal/model"
	"privasync/server/internal/quota"
)

// RecountQuotas is the scheduled counterpart to the admin recount button: it
// walks every account, rebuilds used_bytes/trash_bytes from the `files` table
// and reports how many were wrong. Seeded here with an account whose counters
// claim bytes that no file backs, which is exactly the drift a failed purge
// leaves behind.
func TestRecountQuotas_HealsDriftAndCountsIt(t *testing.T) {
	pool, _, users, _ := newPruneEnv(t)
	ctx := context.Background()

	suffix := uniqueJobsSuffix()
	u, err := users.Create(ctx, "recount_"+suffix, "recount_"+suffix+"@t.local", "x", model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, u.ID)
	})
	// Drift: the counters claim bytes, the files table knows nothing of them.
	if _, err := pool.Exec(ctx, `
		UPDATE users SET used_bytes = 9000, trash_bytes = 500 WHERE id = $1
	`, u.ID); err != nil {
		t.Fatalf("seed drift: %v", err)
	}

	svc := quota.New(users, pool, nil, "")
	svc.Register(bus.New(bus.Config{}))

	healed, err := jobs.RecountQuotas(ctx, users, svc)
	if err != nil {
		t.Fatalf("RecountQuotas: %v", err)
	}
	if healed < 1 {
		t.Errorf("healed = %d, want at least the seeded account", healed)
	}

	got, err := users.FindByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.UsedBytes != 0 || got.TrashBytes != 0 {
		t.Errorf("counters = used=%d trash=%d, want 0/0", got.UsedBytes, got.TrashBytes)
	}

	// Second pass finds nothing left to heal for this account.
	if _, err := jobs.RecountQuotas(ctx, users, svc); err != nil {
		t.Fatalf("second RecountQuotas: %v", err)
	}
	again, err := users.FindByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("reload after second pass: %v", err)
	}
	if again.UsedBytes != 0 || again.TrashBytes != 0 {
		t.Errorf("counters moved on a clean recount: used=%d trash=%d", again.UsedBytes, again.TrashBytes)
	}
}
