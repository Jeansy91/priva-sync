package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository persists Entry rows. Insert takes a pgx.Tx so a SyncCoordinator
// can enqueue inside its domain TX — guaranteeing the side-effect is recorded
// iff the domain row commits. Worker methods use the pool.
type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Insert writes one entry inside the caller's transaction. The kind is taken
// from the payload itself (p.Kind()) so a producer can never file a row under a
// kind that disagrees with the payload it carries — the binding is single-
// sourced on both the producing and the consuming side.
func (r *Repository) Insert(ctx context.Context, tx pgx.Tx, groupID uuid.UUID, seq int, p Payload) error {
	return r.InsertAt(ctx, tx, groupID, seq, p, time.Now())
}

// InsertAt is Insert with an explicit earliest run time: ClaimNext skips the
// row until runAt has passed. Used for entries that are deliberately held back,
// such as the daily conflict digest.
func (r *Repository) InsertAt(ctx context.Context, tx pgx.Tx, groupID uuid.UUID, seq int, p Payload, runAt time.Time) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO outbox (group_id, seq, kind, payload, next_attempt_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		groupID, seq, p.Kind(), raw, runAt,
	)
	return err
}

// ClaimNext returns the next runnable entry: lowest seq per group, only when
// every prior seq in that group is done. Skips groups whose previous seq is
// still pending.
//
// The FOR UPDATE SKIP LOCKED does *not* claim the row for the duration of the
// handler: this runs on the pool, outside an explicit transaction, so the
// implicit commit releases the lock long before Worker.execute finishes. What
// keeps an entry from running twice is that exactly one Worker goroutine
// drains sequentially, in exactly one server process (ADR-0039). Running a
// second instance would double-execute entries; making that safe means
// holding a real transaction from here until MarkDone.
func (r *Repository) ClaimNext(ctx context.Context) (*Entry, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, group_id, seq, kind, payload, attempts, next_attempt_at, last_error, done_at, created_at
		FROM outbox o
		WHERE done_at IS NULL
		  AND next_attempt_at <= NOW()
		  AND NOT EXISTS (
		      SELECT 1 FROM outbox p
		      WHERE p.group_id = o.group_id
		        AND p.seq < o.seq
		        AND p.done_at IS NULL
		  )
		ORDER BY next_attempt_at ASC, seq ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`)
	var e Entry
	if err := row.Scan(&e.ID, &e.GroupID, &e.Seq, &e.Kind, &e.Payload,
		&e.Attempts, &e.NextAttemptAt, &e.LastError, &e.DoneAt, &e.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

// ConflictMail is one not-yet-delivered sync_conflict mail row, reduced to the
// fields the digest needs.
type ConflictMail struct {
	ID       uuid.UUID
	FilePath string
}

// PendingConflictMails returns every not-yet-done mail_sync_conflict row for
// the given user, oldest first. The mail handler folds them into one digest so
// a burst of conflicts (e.g. a multi-file sync) yields a single mail instead of
// one per file. Matches on the row kind + payload `user_id` so it never touches
// another user's or another kind's rows.
func (r *Repository) PendingConflictMails(ctx context.Context, userID uuid.UUID) ([]ConflictMail, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, COALESCE(payload->>'file_path', '')
		FROM outbox
		WHERE kind = 'mail_sync_conflict'
		  AND done_at IS NULL
		  AND payload->>'user_id' = $1
		ORDER BY created_at ASC, id ASC
	`, userID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ConflictMail
	for rows.Next() {
		var m ConflictMail
		if err := rows.Scan(&m.ID, &m.FilePath); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkManyDone marks a set of outbox rows done in one statement. Used by the
// conflict-mail handler to retire the whole folded burst once the single digest
// mail has been sent.
func (r *Repository) MarkManyDone(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx, `UPDATE outbox SET done_at = NOW() WHERE id = ANY($1)`, ids)
	return err
}

// MarkDone sets done_at = NOW().
func (r *Repository) MarkDone(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE outbox SET done_at = NOW() WHERE id = $1`, id)
	return err
}

// MarkRetry records the failure, bumps attempts, and pushes the next attempt
// out by backoff.
func (r *Repository) MarkRetry(ctx context.Context, id uuid.UUID, errMsg string, backoff time.Duration) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE outbox SET
			attempts        = attempts + 1,
			last_error      = $2,
			next_attempt_at = NOW() + $3::interval
		WHERE id = $1
	`, id, errMsg, backoff.String())
	return err
}
