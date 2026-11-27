package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"privasync/server/internal/model"
)

// Grant CRUD for the user-share aggregate (ADR-0015). A share row of
// share_type='user' owns N share_grants — one per recipient, each with its own
// can_write and expiry. Add/remove/update map the editable share dialog's
// per-recipient operations.

const shareGrantColumns = `id, share_id, target_user_id, can_write, expires_at, created_at`

func scanShareGrant(row interface{ Scan(...any) error }, g *model.ShareGrant) error {
	return row.Scan(&g.ID, &g.ShareID, &g.TargetUserID, &g.CanWrite, &g.ExpiresAt, &g.CreatedAt)
}

// AddGrant attaches a recipient to a share aggregate. Re-adding the same
// recipient is an upsert of can_write/expires_at, so the dialog can call it
// idempotently. Returns the persisted grant.
func (r *ShareRepository) AddGrant(ctx context.Context, shareID, targetUserID uuid.UUID, canWrite bool, expiresAt *time.Time) (*model.ShareGrant, error) {
	var g model.ShareGrant
	err := scanShareGrant(r.db.QueryRow(ctx, `
		INSERT INTO share_grants (share_id, target_user_id, can_write, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (share_id, target_user_id)
		DO UPDATE SET can_write = EXCLUDED.can_write, expires_at = EXCLUDED.expires_at
		RETURNING `+shareGrantColumns,
		shareID, targetUserID, canWrite, expiresAt,
	), &g)
	if err != nil {
		return nil, fmt.Errorf("adding share grant: %w", err)
	}
	return &g, nil
}

// ListGrants returns every recipient grant of a share aggregate, newest first.
func (r *ShareRepository) ListGrants(ctx context.Context, shareID uuid.UUID) ([]model.ShareGrant, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+shareGrantColumns+` FROM share_grants WHERE share_id = $1 ORDER BY created_at DESC`,
		shareID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.ShareGrant
	for rows.Next() {
		var g model.ShareGrant
		if err := scanShareGrant(rows, &g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// RemoveGrant detaches a recipient from a share aggregate, returning the
// removed grant so the caller can route a revoke event to the recipient by its
// (mount-anchor) id. A missing grant yields ErrNotFound.
func (r *ShareRepository) RemoveGrant(ctx context.Context, shareID, targetUserID uuid.UUID) (*model.ShareGrant, error) {
	var g model.ShareGrant
	err := scanShareGrant(r.db.QueryRow(ctx,
		`DELETE FROM share_grants WHERE share_id = $1 AND target_user_id = $2
		 RETURNING `+shareGrantColumns,
		shareID, targetUserID,
	), &g)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("removing share grant: %w", err)
	}
	return &g, nil
}

// AccessUser is one recipient who has effective access to a path, with their
// profile for avatar rendering and whether they may write. See ADR-0015.
type AccessUser struct {
	ID         uuid.UUID
	Username   string
	AvatarPath *string
	CanWrite   bool
}

// EffectiveAccess returns the recipients who can reach `path` in the given
// root, resolved from every user-share whose path is an ancestor of (or equal
// to) `path` — so a file inherits the grants of the directories above it.
// Expired grants are excluded. A user granted write anywhere up the chain wins
// write (DISTINCT ON keeps the can_write=true row). See ADR-0015.
func (r *ShareRepository) EffectiveAccess(ctx context.Context, syncRootID uuid.UUID, path string) ([]AccessUser, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT ON (u.id) u.id, u.username, u.avatar_path, g.can_write
		FROM shares s
		JOIN share_grants g ON g.share_id = s.id
		JOIN users u ON u.id = g.target_user_id
		WHERE s.sync_root_id = $1
		  AND s.share_type = 'user'
		  AND (g.expires_at IS NULL OR g.expires_at > NOW())
		  AND (s.path = $2 OR starts_with($2, rtrim(s.path, '/') || '/'))
		ORDER BY u.id, g.can_write DESC
	`, syncRootID, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AccessUser
	for rows.Next() {
		var a AccessUser
		if err := rows.Scan(&a.ID, &a.Username, &a.AvatarPath, &a.CanWrite); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateGrant changes a recipient's can_write/expiry in place. A missing grant
// yields ErrNotFound.
func (r *ShareRepository) UpdateGrant(ctx context.Context, shareID, targetUserID uuid.UUID, canWrite bool, expiresAt *time.Time) (*model.ShareGrant, error) {
	var g model.ShareGrant
	err := scanShareGrant(r.db.QueryRow(ctx, `
		UPDATE share_grants SET can_write = $1, expires_at = $2
		WHERE share_id = $3 AND target_user_id = $4
		RETURNING `+shareGrantColumns,
		canWrite, expiresAt, shareID, targetUserID,
	), &g)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("updating share grant: %w", err)
	}
	return &g, nil
}
