package outbox

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Inserter is the narrow seam Batch depends on — just Repository.Insert.
// Mirrors the ADR-0029 handler-seam pattern: Batch's ordering logic becomes
// unit-testable without a database.
type Inserter interface {
	Insert(ctx context.Context, tx pgx.Tx, groupID uuid.UUID, seq int, p Payload) error
}

var _ Inserter = (*Repository)(nil)

// Batch numbers outbox rows within one group_id monotonically in call order
// (ADR-0022). One Batch belongs to one TX; the caller's Add order is the
// seq order, so the handwritten seq literals that used to drift out of sync
// with the choreography disappear.
type Batch struct {
	ins     Inserter
	ctx     context.Context
	tx      pgx.Tx
	groupID uuid.UUID
	seq     int
}

// NewBatch starts a fresh batch for one group_id.
func NewBatch(ctx context.Context, tx pgx.Tx, ins Inserter, groupID uuid.UUID) *Batch {
	return &Batch{ins: ins, ctx: ctx, tx: tx, groupID: groupID}
}

// Add enqueues p as the next seq in this batch.
func (b *Batch) Add(p Payload) error {
	b.seq++
	return b.ins.Insert(b.ctx, b.tx, b.groupID, b.seq, p)
}
