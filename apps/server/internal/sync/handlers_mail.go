package sync

import (
	"context"

	"github.com/google/uuid"

	"privasync/server/internal/mail"
	"privasync/server/internal/outbox"
	"privasync/server/internal/repository"
)

// registerMailHandlers binds the two mail kinds to their typed payloads. They
// were once a single kind that second-dispatched on a payload discriminator;
// splitting them lets each bind to its own payload type so the
// worker routes without re-parsing and a shape drift fails at compile time.
func registerMailHandlers(reg outbox.Registry, mailer *mail.Mailer, users *repository.UserRepository, ob *outbox.Repository, baseURL string) {
	outbox.Register(reg, func(ctx context.Context, p mail.SyncConflictPayload) error {
		if mailer == nil {
			return nil
		}
		u, err := users.FindByID(ctx, p.UserID)
		if err != nil || !u.EmailNotify {
			return nil
		}
		// Fold the whole burst into one digest: a sync that conflicts N files
		// enqueues N mails. `pending` includes the row being processed (not yet
		// marked done), so it is the complete set; if a concurrent drain already
		// cleared it, fall back to this row's own path so the mail is never empty.
		pending, err := ob.PendingConflictMails(ctx, p.UserID)
		if err != nil {
			return err
		}
		paths := make([]string, 0, len(pending))
		ids := make([]uuid.UUID, 0, len(pending))
		for _, m := range pending {
			if m.FilePath != "" {
				paths = append(paths, m.FilePath)
			}
			ids = append(ids, m.ID)
		}
		if len(paths) == 0 {
			paths = []string{p.FilePath}
		}
		if err := mailer.SendTemplate(u.Email, u.PreferredLanguage, mail.TplSyncConflict, mail.SyncConflictData{
			Common:       mail.Common{BaseURL: baseURL, Username: u.Username, OptIn: true},
			FilePath:     paths[0],
			Count:        len(paths),
			FilePaths:    paths,
			ConflictsURL: baseURL + "/conflicts",
		}); err != nil {
			return err
		}
		// Retire the folded siblings so they don't each send their own mail. The
		// current row is in this set too; the worker's own MarkDone afterwards is
		// then an idempotent no-op. Only reached after a successful send, so an
		// SMTP failure still retries.
		return ob.MarkManyDone(ctx, ids)
	})

	outbox.Register(reg, func(ctx context.Context, p mail.QuotaWarningPayload) error {
		if mailer == nil {
			return nil
		}
		u, err := users.FindByID(ctx, p.UserID)
		if err != nil || !u.EmailNotify {
			return nil
		}
		// Re-read of quota_state guards against stale events from a rolled-back
		// TX: if the user is no longer in the escalated band, swallow silently.
		if u.QuotaState != p.NewState {
			return nil
		}
		return mailer.SendTemplate(u.Email, u.PreferredLanguage, mail.TplQuotaWarning, mail.QuotaWarningData{
			Common:     mail.Common{BaseURL: baseURL, Username: u.Username, OptIn: true},
			UsedBytes:  u.UsedBytes + u.TrashBytes,
			QuotaBytes: u.QuotaBytes,
			Percentage: p.UsedPct,
		})
	})
}
