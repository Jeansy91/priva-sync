// Subscriber enqueues mails into the durable outbox in response to reactive
// domain events. Each handler opens its own short TX so the bus thread
// stays free of long work; retry on SMTP failure lives in the outbox worker.
package mail

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"privasync/server/internal/bus"
	"privasync/server/internal/outbox"
	"privasync/server/internal/repository"
)

// SyncConflictPayload is the outbox body for a sync-conflict mail. It implements
// outbox.Payload so the producer here and the handler in sync.BuildHandlers bind
// to the same type — the JSON shape cannot drift between them.
type SyncConflictPayload struct {
	UserID   uuid.UUID `json:"user_id"`
	FilePath string    `json:"file_path"`
}

func (SyncConflictPayload) Kind() outbox.Kind { return outbox.KindMailSyncConflict }

// QuotaWarningPayload is the outbox body for a quota-warning mail.
type QuotaWarningPayload struct {
	UserID   uuid.UUID `json:"user_id"`
	NewState string    `json:"new_state"`
	UsedPct  int       `json:"used_pct"`
}

func (QuotaWarningPayload) Kind() outbox.Kind { return outbox.KindMailQuotaWarning }

// Subscriber wires critical bus events to outbox-mediated mail delivery.
// Critical (not reactive) so the mail row lands in the publisher's TX —
// guarantees mail-vs-domain consistency across a crash: either both commit
// or neither does.
type Subscriber struct {
	outbox *outbox.Repository
	users  *repository.UserRepository
}

func NewSubscriber(ob *outbox.Repository, users *repository.UserRepository) *Subscriber {
	return &Subscriber{outbox: ob, users: users}
}

// Register declares the subscriber's own bus contract: both mail-worthy events
// are critical so the outbox mail row commits in the publisher's TX.
func (s *Subscriber) Register(b bus.Bus) {
	b.SubscribeCritical(bus.EventQuotaThresholdCrossed, s.OnQuotaThresholdCrossed)
	b.SubscribeCritical(bus.EventConflictCreated, s.OnConflictCreated)
}

// OnQuotaThresholdCrossed enqueues a quota-warning mail row in the publisher's
// TX when the user crossed into warning80 / critical95 and opted in to mail
// notifications. Returning an error aborts the quota mutation — the band
// transition and the mail are committed atomically.
func (s *Subscriber) OnQuotaThresholdCrossed(ctx context.Context, tx pgx.Tx, evt bus.Event) error {
	e, ok := evt.(bus.QuotaThresholdCrossed)
	if !ok {
		return nil
	}
	if thresholdFor(e.NewState) == 0 {
		return nil
	}
	u, err := s.users.FindByID(ctx, e.UserID)
	if err != nil {
		return fmt.Errorf("mail subscriber: user lookup: %w", err)
	}
	if !u.EmailNotify {
		return nil
	}
	return s.enqueue(ctx, tx, QuotaWarningPayload{
		UserID:   e.UserID,
		NewState: e.NewState,
		UsedPct:  e.UsedPct,
	})
}

// OnConflictCreated enqueues a sync_conflict mail in the publisher's TX. Run
// inside the conflict-row TX so a rolled-back conflict cannot leak a mail.
func (s *Subscriber) OnConflictCreated(ctx context.Context, tx pgx.Tx, evt bus.Event) error {
	e, ok := evt.(bus.ConflictCreated)
	if !ok {
		return nil
	}
	u, err := s.users.FindByID(ctx, e.UserID)
	if err != nil {
		return fmt.Errorf("mail subscriber: user lookup: %w", err)
	}
	if !u.EmailNotify {
		return nil
	}
	// Held back until the next daily digest. The handler folds every pending
	// conflict row of the user into that one mail, so a day of conflicts yields
	// a single message however many files it covers.
	return s.enqueueAt(ctx, tx, NextConflictDigest(time.Now()), SyncConflictPayload{
		UserID:   e.UserID,
		FilePath: e.FilePath,
	})
}

// enqueue inserts a mail payload under the kind it reports for itself, so the
// row's kind column can never disagree with the payload it carries.
func (s *Subscriber) enqueue(ctx context.Context, tx pgx.Tx, p outbox.Payload) error {
	return s.enqueueAt(ctx, tx, time.Now(), p)
}

// enqueueAt is enqueue with an earliest delivery time.
func (s *Subscriber) enqueueAt(ctx context.Context, tx pgx.Tx, runAt time.Time, p outbox.Payload) error {
	if err := s.outbox.InsertAt(ctx, tx, uuid.New(), 1, p, runAt); err != nil {
		return fmt.Errorf("insert outbox: %w", err)
	}
	return nil
}

// ConflictDigestHour is the local hour at which the daily conflict digest is
// sent.
const ConflictDigestHour = 8

// NextConflictDigest returns the first digest time strictly after now: the next
// ConflictDigestHour:00 in now's location.
func NextConflictDigest(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), ConflictDigestHour, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = time.Date(now.Year(), now.Month(), now.Day()+1, ConflictDigestHour, 0, 0, 0, now.Location())
	}
	return next
}

func thresholdFor(newState string) int {
	switch newState {
	case "critical95":
		return 95
	case "warning80":
		return 80
	}
	return 0
}
