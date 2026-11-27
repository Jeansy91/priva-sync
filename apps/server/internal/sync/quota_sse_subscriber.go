package sync

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"privasync/server/internal/bus"
	"privasync/server/internal/outbox"
	sseevents "privasync/server/internal/sse/events"
)

// QuotaSSESubscriber projects critical bus.QuotaThresholdCrossed events into a
// typed SSE outbox row. Critical (not reactive) so the SSE delivery commits
// atomically with the quota mutation that triggered it — a rolled-back band
// transition cannot leak a banner.
type QuotaSSESubscriber struct {
	outbox *outbox.Repository
}

func NewQuotaSSESubscriber(ob *outbox.Repository) *QuotaSSESubscriber {
	return &QuotaSSESubscriber{outbox: ob}
}

// Register declares the subscriber's own bus contract: the threshold event is
// critical so the typed banner SSE row commits in the publisher's TX.
func (s *QuotaSSESubscriber) Register(b bus.Bus) {
	b.SubscribeCritical(bus.EventQuotaThresholdCrossed, s.OnQuotaThresholdCrossed)
}

// OnQuotaThresholdCrossed enqueues a KindSSETyped row carrying the band
// transition. The mail subscriber for the same event keeps mail delivery
// independent — both run in the publisher's TX so consistency is preserved.
func (s *QuotaSSESubscriber) OnQuotaThresholdCrossed(ctx context.Context, tx pgx.Tx, evt bus.Event) error {
	e, ok := evt.(bus.QuotaThresholdCrossed)
	if !ok {
		return nil
	}
	batch := outbox.NewBatch(ctx, tx, s.outbox, uuid.New())
	if err := PublishDurable(batch, e.UserID, sseevents.QuotaThresholdCrossed{
		NewState: e.NewState,
		UsedPct:  e.UsedPct,
	}); err != nil {
		return fmt.Errorf("sse subscriber: insert outbox: %w", err)
	}
	return nil
}
