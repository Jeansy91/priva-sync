// Package quota owns per-user storage accounting (domain term: "Kontingent").
// It centralises the invariant that any mutation of used_bytes/trash_bytes
// also re-evaluates the 80%/95% warning bands.
//
// Every mutation enters through the DomainEventBus: the SyncCoordinator
// publishes File* events, the cron purger publishes FilePurgedByCron, and
// the admin recount publishes AdminQuotaAdjusted via RecomputeViaBus. The
// service's critical subscribers do the in-TX bookkeeping; the warning mail
// is fanned out as a reactive QuotaThresholdCrossed event.
package quota

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/bus"
	"privasync/server/internal/mail"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

// ErrExceeded reports that growth would blow past the quota. It comes from
// two places: CheckHeadroom, as an early answer before any bytes move, and
// the in-TX booking itself, which is the one that actually enforces the
// limit. The service wraps detail values (used/limit) via %w so handlers can
// forward a meaningful error string as a 507 body.
var ErrExceeded = errors.New("quota exceeded")

// Service bundles UserRepository, Mailer, and BaseURL once so handlers can
// reduce their calls to a single argument.
type Service struct {
	users   *repository.UserRepository
	pool    *pgxpool.Pool
	mailer  *mail.Mailer
	baseURL string
	bus     bus.Bus
}

// New creates a Service. `mailer` may be nil (e.g. in tests or when SMTP is
// unconfigured) — mail delivery is skipped in that case, accounting continues
// normally. The bus reference is wired post-construction via Attach so
// main.go can keep the wiring order simple. The pool is used by RecomputeViaBus
// to own the recount TX.
func New(users *repository.UserRepository, pool *pgxpool.Pool, mailer *mail.Mailer, baseURL string) *Service {
	return &Service{users: users, pool: pool, mailer: mailer, baseURL: baseURL}
}

// Register hooks the service into the DomainEventBus: it declares every
// counter-moving File*/quota event as a critical (in-TX) subscriber so quota
// bookkeeping commits atomically with the mutation. No reactive subscribers —
// the threshold mail is fanned out by the mail package's own Register.
// Not idempotent: calling twice double-registers; main.go owns lifecycle.
func (s *Service) Register(b bus.Bus) {
	s.bus = b
	b.SubscribeCritical(bus.EventFileUploaded, bus.Typed(s.OnFileUploaded))
	b.SubscribeCritical(bus.EventFileConflictKept, bus.Typed(s.OnFileConflictKept))
	b.SubscribeCritical(bus.EventFileConflictDiscarded, bus.Typed(s.OnFileConflictDiscarded))
	b.SubscribeCritical(bus.EventFileTrashed, bus.Typed(s.OnFileTrashed))
	b.SubscribeCritical(bus.EventFileRestored, bus.Typed(s.OnFileRestored))
	b.SubscribeCritical(bus.EventSyncRootDeleted, bus.Typed(s.OnSyncRootDeleted))
	b.SubscribeCritical(bus.EventFilePurgedByCron, bus.Typed(s.OnFilePurgedByCron))
	b.SubscribeCritical(bus.EventAdminQuotaAdjusted, bus.Typed(s.OnAdminQuotaAdjusted))
}

// CheckHeadroom reports whether the user can still absorb `addBytes` more
// (used + trash + addBytes <= quota). With addBytes=0 it is a pure gate check.
// Returns ErrExceeded (wrapped with detail) when headroom is missing.
//
// This is an early answer, not the decision: it reads the counters before the
// upload streams and they can move before the booking commits. The limit is
// enforced in applyDeltaTx, inside the transaction that writes them. Keeping
// this check is still worth it -- it rejects a hopeless upload before the
// bytes travel rather than after.
func (s *Service) CheckHeadroom(ctx context.Context, userID uuid.UUID, addBytes int64) error {
	u, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.QuotaBytes <= 0 {
		return nil
	}
	if u.UsedBytes+u.TrashBytes+addBytes > u.QuotaBytes {
		return fmt.Errorf("%w: %d/%d bytes used, %d additional requested",
			ErrExceeded, u.UsedBytes+u.TrashBytes, u.QuotaBytes, addBytes)
	}
	return nil
}

// RecomputeViaBus heals quota drift by summing the truth from `files` and
// publishing AdminQuotaAdjusted on the bus — the critical subscriber writes
// the absolute counters, recomputes the band, and emits a threshold event
// when the band moves. Routing the recount through the bus keeps it the
// single mutation channel for the quota counters.
func (s *Service) RecomputeViaBus(ctx context.Context, userID uuid.UUID) error {
	if _, err := s.users.FindByID(ctx, userID); err != nil {
		return err
	}
	used, trash, err := s.users.SumUserSizes(ctx, userID)
	if err != nil {
		return fmt.Errorf("quota: sum user sizes: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("quota: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := s.bus.PublishCritical(ctx, tx, bus.AdminQuotaAdjusted{
		UserID: userID, NewUsed: used, NewTrash: trash,
	}); err != nil {
		return fmt.Errorf("quota: publish: %w", err)
	}
	return tx.Commit(ctx)
}

// ─── Critical subscribers ──────────────────────────────────────────────────

// OnFileUploaded books the delta of an upload in-TX. Returning an error
// causes the publisher to roll back, leaving the file row and the quota in
// sync.
func (s *Service) OnFileUploaded(ctx context.Context, tx pgx.Tx, e bus.FileUploaded) error {
	return s.applyDeltaTx(ctx, tx, e.UserID, e.Bytes-e.PrevBytes, 0)
}

// OnFileConflictKept books the bytes of a conflict copy that lives alongside
// an existing file.
func (s *Service) OnFileConflictKept(ctx context.Context, tx pgx.Tx, e bus.FileConflictKept) error {
	return s.applyDeltaTx(ctx, tx, e.UserID, e.Bytes, 0)
}

// OnFileConflictDiscarded releases the bytes of a conflict copy after the
// user resolves the conflict and the loser-path file is deleted from storage.
func (s *Service) OnFileConflictDiscarded(ctx context.Context, tx pgx.Tx, e bus.FileConflictDiscarded) error {
	return s.applyDeltaTx(ctx, tx, e.UserID, -e.Bytes, 0)
}

// OnFileTrashed shifts bytes from used into trash. Total stays.
func (s *Service) OnFileTrashed(ctx context.Context, tx pgx.Tx, e bus.FileTrashed) error {
	return s.applyDeltaTx(ctx, tx, e.UserID, -e.Bytes, e.Bytes)
}

// OnFileRestored shifts bytes from trash back into used.
func (s *Service) OnFileRestored(ctx context.Context, tx pgx.Tx, e bus.FileRestored) error {
	return s.applyDeltaTx(ctx, tx, e.UserID, e.Bytes, -e.Bytes)
}

// OnAdminQuotaAdjusted overwrites used_bytes/trash_bytes with the absolute
// counters the admin recount derived from the `files` table. Recomputes the
// quota band and publishes QuotaThresholdCrossed if it changed — same
// reactive-mail path the File* subscribers use.
func (s *Service) OnAdminQuotaAdjusted(ctx context.Context, tx pgx.Tx, e bus.AdminQuotaAdjusted) error {
	u, err := s.users.SetUsedAndTrashBytesTx(ctx, tx, e.UserID, e.NewUsed, e.NewTrash)
	if err != nil {
		return fmt.Errorf("quota: set absolute counters: %w", err)
	}
	oldState := QuotaState(u.QuotaState)
	newState := s.computeState(u)
	if newState == oldState {
		return nil
	}
	if err := s.users.SetQuotaStateTx(ctx, tx, e.UserID, string(newState)); err != nil {
		return fmt.Errorf("quota: persist state: %w", err)
	}
	return s.publishEscalation(ctx, tx, e.UserID, oldState, newState, u.UsedBytes+u.TrashBytes, u.QuotaBytes)
}

// OnFilePurgedByCron releases the bytes of a trash entry that the 30-day
// retention sweep hard-deletes. Must run in the same TX as the row delete so
// quota and `files` cannot drift on a partial failure.
func (s *Service) OnFilePurgedByCron(ctx context.Context, tx pgx.Tx, e bus.FilePurgedByCron) error {
	return s.applyDeltaTx(ctx, tx, e.UserID, 0, -e.Bytes)
}

// OnSyncRootDeleted releases both counters in one atomic write so the band
// transition fires once instead of twice.
func (s *Service) OnSyncRootDeleted(ctx context.Context, tx pgx.Tx, e bus.SyncRootDeleted) error {
	return s.applyDeltaTx(ctx, tx, e.UserID, -e.UsedBytes, -e.TrashBytes)
}

// ─── Internal helpers ──────────────────────────────────────────────────────

// applyDeltaTx books the byte movement in-TX, recomputes the band, persists
// the new band, and publishes QuotaThresholdCrossed reactively when the band
// changed. The publish is best-effort: a later tx.Commit failure can leave a
// stale reactive event in flight, which the mail subscriber treats as a no-op
// (it re-reads user state from the DB).
func (s *Service) applyDeltaTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, deltaUsed, deltaTrash int64) error {
	if deltaUsed == 0 && deltaTrash == 0 {
		return nil
	}
	var (
		u   *model.User
		err error
	)
	if deltaTrash == 0 {
		u, err = s.users.AdjustUsedBytesTx(ctx, tx, userID, deltaUsed)
	} else {
		u, err = s.users.AdjustUsedAndTrashBytesTx(ctx, tx, userID, deltaUsed, deltaTrash)
	}
	if err != nil {
		return fmt.Errorf("quota: adjust counters: %w", err)
	}

	// Enforce the ceiling here, not only in CheckHeadroom. That check reads
	// the counters before the upload and books them after it, so two uploads
	// that each fit on their own can both pass it and still overshoot
	// together. This runs inside the caller's transaction on a row the
	// UPDATE already locked, so concurrent bookings see each other.
	//
	// Only growth is rejected: trashing, restoring and releasing bytes must
	// keep working at or above the limit, or an account that an admin quota
	// cut pushed over could never be emptied again.
	if deltaUsed+deltaTrash > 0 && u.QuotaBytes > 0 && u.UsedBytes+u.TrashBytes > u.QuotaBytes {
		return fmt.Errorf("%w: %d/%d bytes after adding %d",
			ErrExceeded, u.UsedBytes+u.TrashBytes, u.QuotaBytes, deltaUsed+deltaTrash)
	}

	oldState := QuotaState(u.QuotaState)
	newState := s.computeState(u)
	if newState != oldState {
		if err := s.users.SetQuotaStateTx(ctx, tx, userID, string(newState)); err != nil {
			return fmt.Errorf("quota: persist state: %w", err)
		}
		return s.publishEscalation(ctx, tx, userID, oldState, newState, u.UsedBytes+u.TrashBytes, u.QuotaBytes)
	}
	return nil
}

// publishEscalation emits QuotaThresholdCrossed only when the band climbed.
// Critical publish so the mail subscriber's outbox insert commits in the
// same TX as the band transition (no crash gap). Bus stays nil-safe for
// unit tests that skip Attach.
func (s *Service) publishEscalation(ctx context.Context, tx pgx.Tx, userID uuid.UUID, old, next QuotaState, usedBytes, quotaBytes int64) error {
	if s.bus == nil || !isEscalation(old, next) {
		return nil
	}
	pct := 0
	if quotaBytes > 0 {
		pct = int(usedBytes * 100 / quotaBytes)
	}
	return s.bus.PublishCritical(ctx, tx, bus.QuotaThresholdCrossed{
		UserID: userID, NewState: string(next), UsedPct: pct,
	})
}

func (s *Service) computeState(u *model.User) QuotaState {
	if u.QuotaBytes <= 0 {
		return QuotaStateOK
	}
	usedPct := float64(u.UsedBytes+u.TrashBytes) * 100 / float64(u.QuotaBytes)
	return transitionQuotaState(QuotaState(u.QuotaState), usedPct)
}
