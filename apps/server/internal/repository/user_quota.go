package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"privasync/server/internal/model"
)

// AdjustUsedBytesTx mirrors AdjustUsedBytes but runs inside the caller's TX,
// so the bookkeeping commits or rolls back atomically with the publisher.
func (r *UserRepository) AdjustUsedBytesTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, delta int64) (*model.User, error) {
	var u model.User
	err := scanUser(tx.QueryRow(ctx, `
		UPDATE users SET used_bytes = GREATEST(0, used_bytes + $1)
		WHERE id = $2
		RETURNING `+userColumns,
		delta, userID,
	), &u)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// AdjustUsedAndTrashBytesTx mirrors AdjustUsedAndTrashBytes in-TX.
func (r *UserRepository) AdjustUsedAndTrashBytesTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, deltaUsed, deltaTrash int64) (*model.User, error) {
	var u model.User
	err := scanUser(tx.QueryRow(ctx, `
		UPDATE users SET
			used_bytes  = GREATEST(0, used_bytes  + $1),
			trash_bytes = GREATEST(0, trash_bytes + $2)
		WHERE id = $3
		RETURNING `+userColumns,
		deltaUsed, deltaTrash, userID,
	), &u)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// SumUserSizes returns the authoritative used/trash byte sums derived from
// the `files` table. Pure read — no mutation, no TX needed. The bus-driven
// admin recount path feeds these numbers into AdminQuotaAdjusted.
func (r *UserRepository) SumUserSizes(ctx context.Context, userID uuid.UUID) (used, trash int64, err error) {
	err = r.db.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(size) FILTER (WHERE is_deleted = FALSE), 0),
			COALESCE(SUM(size) FILTER (WHERE is_deleted = TRUE AND trash_path IS NOT NULL), 0)
		FROM files WHERE user_id = $1
	`, userID).Scan(&used, &trash)
	return used, trash, err
}

// SetUsedAndTrashBytesTx overwrites both counters with absolute values in-TX.
// Used by the AdminQuotaAdjusted subscriber to heal drift between the stored
// counters and the truth derived from the `files` table.
//
// The 0 in GREATEST carries an explicit bigint cast: without it Postgres
// infers int4 from the untyped literal and the whole expression with it, so
// any account past 2 GB failed the recount with an encoding error.
func (r *UserRepository) SetUsedAndTrashBytesTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, used, trash int64) (*model.User, error) {
	var u model.User
	err := scanUser(tx.QueryRow(ctx, `
		UPDATE users SET
			used_bytes  = GREATEST(0::bigint, $1),
			trash_bytes = GREATEST(0::bigint, $2)
		WHERE id = $3
		RETURNING `+userColumns,
		used, trash, userID,
	), &u)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// SetQuotaStateTx mirrors SetQuotaState in-TX.
func (r *UserRepository) SetQuotaStateTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, state string) error {
	_, err := tx.Exec(ctx,
		`UPDATE users SET quota_state = $1 WHERE id = $2`,
		state, userID,
	)
	return err
}
