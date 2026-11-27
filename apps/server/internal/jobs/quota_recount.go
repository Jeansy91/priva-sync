package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"privasync/server/internal/repository"
)

// quotaRecomputer is the one thing this sweep needs from the quota service:
// heal a single user's counters from the `files` table.
type quotaRecomputer interface {
	RecomputeViaBus(ctx context.Context, userID uuid.UUID) error
}

// RecountQuotas rebuilds used_bytes/trash_bytes for every account and returns
// how many of them were actually wrong.
//
// The counters are carried forward, never counted: every byte movement books a
// delta in the same transaction as the mutation, so nothing recounts them in
// normal operation. That holds as long as every path books correctly -- a purge whose
// recount failed, a crash between two writes, or a bug in the booking itself
// leaves a number that is never revisited, and the drift is silent: uploads
// get rejected against a limit the account is not really at.
//
// A user whose recount fails is logged and skipped; the sweep still visits
// everyone else and reports the failure count in its error.
func RecountQuotas(ctx context.Context, users *repository.UserRepository, quota quotaRecomputer) (int, error) {
	all, err := users.List(ctx)
	if err != nil {
		return 0, fmt.Errorf("quota recount: list users: %w", err)
	}

	healed, failed := 0, 0
	for _, u := range all {
		if err := quota.RecomputeViaBus(ctx, u.ID); err != nil {
			slog.Error("quota recount: recompute failed", "user_id", u.ID, "error", err)
			failed++
			continue
		}
		after, err := users.FindByID(ctx, u.ID)
		if err != nil {
			slog.Error("quota recount: reload failed", "user_id", u.ID, "error", err)
			failed++
			continue
		}
		if after.UsedBytes == u.UsedBytes && after.TrashBytes == u.TrashBytes {
			continue
		}
		// Named per account: drift means some booking path lost bytes, and the
		// numbers are the only trace of which account and how much.
		slog.Warn("quota recount: healed drift",
			"user_id", u.ID,
			"used_before", u.UsedBytes, "used_after", after.UsedBytes,
			"trash_before", u.TrashBytes, "trash_after", after.TrashBytes)
		healed++
	}

	if failed > 0 {
		return healed, fmt.Errorf("quota recount: %d of %d accounts failed", failed, len(all))
	}
	return healed, nil
}
