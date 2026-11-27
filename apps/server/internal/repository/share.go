package repository

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/model"
	"privasync/server/internal/storage"
)

type ShareRepository struct {
	db   *pgxpool.Pool
	stor *storage.Service
}

func NewShareRepository(db *pgxpool.Pool, stor *storage.Service) *ShareRepository {
	return &ShareRepository{db: db, stor: stor}
}

func (r *ShareRepository) writeMeta(sh *model.Share) {
	if err := r.stor.WriteShareMeta(sh); err != nil {
		slog.Warn("share meta write failed", "share_id", sh.ID, "error", err)
	}
}

const shareColumns = `id, user_id, sync_root_id, target_user_id, path, share_type, token, read_token, write_token, password_hash, expires_at, read_expires_at, write_expires_at, can_write, created_at`

func scanShare(row interface{ Scan(...any) error }, s *model.Share) error {
	return row.Scan(
		&s.ID, &s.UserID, &s.SyncRootID, &s.TargetUserID, &s.Path, &s.ShareType,
		&s.Token, &s.ReadToken, &s.WriteToken, &s.PasswordHash,
		&s.ExpiresAt, &s.ReadExpiresAt, &s.WriteExpiresAt, &s.CanWrite, &s.CreatedAt,
	)
}

func (r *ShareRepository) Create(ctx context.Context, s *model.Share) (*model.Share, error) {
	var out model.Share
	err := scanShare(r.db.QueryRow(ctx, `
		INSERT INTO shares (user_id, sync_root_id, target_user_id, path, share_type, token, read_token, write_token, expires_at, can_write)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+shareColumns,
		s.UserID, s.SyncRootID, s.TargetUserID, s.Path, s.ShareType, s.Token, s.ReadToken, s.WriteToken, s.ExpiresAt, s.CanWrite,
	), &out)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("creating share: %w", err)
	}
	r.writeMeta(&out)
	return &out, nil
}

// EnsureUserShare get-or-creates the single user-share aggregate for
// (owner, sync_root, path). The aggregate carries no target itself —
// recipients live in share_grants — so one editable share holds many users.
// See ADR-0015.
func (r *ShareRepository) EnsureUserShare(ctx context.Context, ownerID, syncRootID uuid.UUID, path string) (*model.Share, error) {
	var s model.Share
	err := scanShare(r.db.QueryRow(ctx,
		`SELECT `+shareColumns+` FROM shares
		 WHERE user_id = $1 AND sync_root_id = $2 AND path = $3 AND share_type = 'user'
		 ORDER BY created_at LIMIT 1`,
		ownerID, syncRootID, path,
	), &s)
	if err == nil {
		return &s, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("finding user share aggregate: %w", err)
	}
	err = scanShare(r.db.QueryRow(ctx,
		`INSERT INTO shares (user_id, sync_root_id, path, share_type)
		 VALUES ($1, $2, $3, 'user')
		 RETURNING `+shareColumns,
		ownerID, syncRootID, path,
	), &s)
	if err != nil {
		return nil, fmt.Errorf("creating user share aggregate: %w", err)
	}
	r.writeMeta(&s)
	return &s, nil
}

// EnsurePublicShare get-or-creates the single public share aggregate for
// (owner, sync_root, path). Tokens are added later per kind, so a read-only
// and a write link have independent lifecycles. See ADR-0015.
func (r *ShareRepository) EnsurePublicShare(ctx context.Context, ownerID, syncRootID uuid.UUID, path string) (*model.Share, error) {
	var s model.Share
	err := scanShare(r.db.QueryRow(ctx,
		`SELECT `+shareColumns+` FROM shares
		 WHERE user_id = $1 AND sync_root_id = $2 AND path = $3 AND share_type = 'public'
		 ORDER BY created_at LIMIT 1`,
		ownerID, syncRootID, path,
	), &s)
	if err == nil {
		return &s, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("finding public share: %w", err)
	}
	err = scanShare(r.db.QueryRow(ctx,
		`INSERT INTO shares (user_id, sync_root_id, path, share_type)
		 VALUES ($1, $2, $3, 'public')
		 RETURNING `+shareColumns,
		ownerID, syncRootID, path,
	), &s)
	if err != nil {
		return nil, fmt.Errorf("creating public share: %w", err)
	}
	r.writeMeta(&s)
	return &s, nil
}

// FindPublicShare returns the public share aggregate for a path, or
// ErrNotFound. Read-only — used by the delete-link path.
func (r *ShareRepository) FindPublicShare(ctx context.Context, ownerID, syncRootID uuid.UUID, path string) (*model.Share, error) {
	var s model.Share
	err := scanShare(r.db.QueryRow(ctx,
		`SELECT `+shareColumns+` FROM shares
		 WHERE user_id = $1 AND sync_root_id = $2 AND path = $3 AND share_type = 'public'
		 ORDER BY created_at LIMIT 1`,
		ownerID, syncRootID, path,
	), &s)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &s, err
}

// publicTokenColumn maps the link kind to its column. Only these two literals
// ever reach the SQL, so the dynamic column name is safe.
func publicTokenColumn(kind string) (string, bool) {
	switch kind {
	case "read":
		return "read_token", true
	case "write":
		return "write_token", true
	default:
		return "", false
	}
}

// SetPublicToken sets the read or write token on a public share (owner-scoped),
// returning the updated row so the handler can echo both links. The expiry is
// part of the link's identity: it is set together with the token and a
// regenerated link without a limit drops the old one.
func (r *ShareRepository) SetPublicToken(ctx context.Context, shareID, ownerID uuid.UUID, kind, token string, expiresAt *time.Time) (*model.Share, error) {
	col, ok := publicTokenColumn(kind)
	if !ok {
		return nil, ErrNotFound
	}
	var s model.Share
	err := scanShare(r.db.QueryRow(ctx, fmt.Sprintf(
		`UPDATE shares SET %s = $1, %s_expires_at = $2
		 WHERE id = $3 AND user_id = $4 AND share_type = 'public'
		 RETURNING %s`, col, kind, shareColumns),
		token, expiresAt, shareID, ownerID,
	), &s)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("setting public token: %w", err)
	}
	r.writeMeta(&s)
	return &s, nil
}

// ClearPublicToken nulls one token on a public share. When both tokens are gone
// the share row is deleted (returns nil, nil) so an empty public share never
// lingers. See ADR-0015.
func (r *ShareRepository) ClearPublicToken(ctx context.Context, shareID, ownerID uuid.UUID, kind string) (*model.Share, error) {
	col, ok := publicTokenColumn(kind)
	if !ok {
		return nil, ErrNotFound
	}
	var s model.Share
	err := scanShare(r.db.QueryRow(ctx, fmt.Sprintf(
		`UPDATE shares SET %s = NULL, %s_expires_at = NULL
		 WHERE id = $1 AND user_id = $2 AND share_type = 'public'
		 RETURNING %s`, col, kind, shareColumns),
		shareID, ownerID,
	), &s)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("clearing public token: %w", err)
	}
	if s.ReadToken == nil && s.WriteToken == nil {
		if _, derr := r.db.Exec(ctx, `DELETE FROM shares WHERE id = $1`, shareID); derr != nil {
			return nil, fmt.Errorf("deleting empty public share: %w", derr)
		}
		_ = r.stor.DeleteShareMeta(shareID)
		return nil, nil
	}
	r.writeMeta(&s)
	return &s, nil
}

// FindMountByGrant resolves a recipient's mount by grant id, scoped to the
// recipient (target_user_id) so a non-recipient or missing grant yields
// ErrNotFound. The returned Share carries the grant's can_write/expires_at
// overlaid onto the aggregate, so mount handlers read effective permission
// straight off the Share. See ADR-0015.
func (r *ShareRepository) FindMountByGrant(ctx context.Context, grantID, targetUserID uuid.UUID) (*model.Share, error) {
	var s model.Share
	err := r.db.QueryRow(ctx, `
		SELECT s.id, s.user_id, s.sync_root_id, s.target_user_id, s.path, s.share_type,
		       s.token, s.password_hash, g.expires_at, g.can_write, s.created_at
		FROM share_grants g
		JOIN shares s ON s.id = g.share_id
		WHERE g.id = $1 AND g.target_user_id = $2
		  AND s.share_type = 'user' AND s.sync_root_id IS NOT NULL
	`, grantID, targetUserID).Scan(
		&s.ID, &s.UserID, &s.SyncRootID, &s.TargetUserID, &s.Path, &s.ShareType,
		&s.Token, &s.PasswordHash, &s.ExpiresAt, &s.CanWrite, &s.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding mount by grant: %w", err)
	}
	return &s, nil
}

func (r *ShareRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]model.Share, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+shareColumns+` FROM shares WHERE user_id = $1 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var shares []model.Share
	for rows.Next() {
		var s model.Share
		if err := scanShare(rows, &s); err != nil {
			return nil, err
		}
		shares = append(shares, s)
	}
	return shares, rows.Err()
}

// ListMountsForTarget returns the active grants where `targetUserID` is the
// recipient — one mount per grant. The mount id is the grant id (the
// recipient's stable anchor, ADR-0015), and can_write/expires_at come from the
// grant, not the aggregate. The giver's profile is joined so callers can
// render the sidebar avatar without a second round-trip. `is_file` is derived
// from the matching files row (same sync_root_id + path). See ADR-0010/0015.
func (r *ShareRepository) ListMountsForTarget(ctx context.Context, targetUserID uuid.UUID) ([]model.SharedMount, error) {
	rows, err := r.db.Query(ctx, `
		SELECT g.id, s.sync_root_id, sr.name, s.path, g.can_write, g.expires_at,
		       u.id, u.username, u.avatar_path,
		       COALESCE(f.is_dir = FALSE AND f.is_deleted = FALSE, FALSE) AS is_file
		FROM share_grants g
		JOIN shares s ON s.id = g.share_id
		JOIN users u ON u.id = s.user_id
		JOIN sync_roots sr ON sr.id = s.sync_root_id
		LEFT JOIN files f
		       ON f.sync_root_id = s.sync_root_id
		      AND f.path = s.path
		      AND f.is_deleted = FALSE
		WHERE s.share_type = 'user'
		  AND g.target_user_id = $1
		  AND s.sync_root_id IS NOT NULL
		  AND (g.expires_at IS NULL OR g.expires_at > NOW())
		ORDER BY g.created_at DESC
	`, targetUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.SharedMount
	for rows.Next() {
		var m model.SharedMount
		if err := rows.Scan(
			&m.ShareID, &m.SyncRootID, &m.SyncRootName, &m.Path, &m.CanWrite, &m.ExpiresAt,
			&m.OwnerID, &m.OwnerUsername, &m.OwnerAvatarPath,
			&m.IsFile,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ShareWithTarget enriches a Share with the target user's username when
// share_type='user'. Powers the Geber-side badges so the badge tooltip
// can show "geteilt mit <username>" without a per-row lookup. Public
// shares carry a nil TargetUsername. See ADR-0010.
type ShareWithTarget struct {
	model.Share
	TargetUsername *string
	// AggregateID is the share aggregate id (shares.id). For user-shares ID is
	// the grant id; the aggregate id is what the grant edit endpoints address.
	AggregateID uuid.UUID
}

// ListBySyncRoot returns every share entry attached to the given sync root:
// one row per recipient grant for user-shares (ID = grant id, so the
// Geber-side badge can address the individual recipient) and one row per
// public-share aggregate (ID = share id). Enriched with the recipient
// username. Used by the Geber-side `GET /sync-roots/{id}/shares` endpoint.
// See ADR-0010/0015.
func (r *ShareRepository) ListBySyncRoot(ctx context.Context, syncRootID uuid.UUID) ([]ShareWithTarget, error) {
	rows, err := r.db.Query(ctx, `
		SELECT g.id, s.user_id, s.sync_root_id, g.target_user_id, s.path,
		       s.share_type, s.token, NULL::text, NULL::text, s.password_hash, g.expires_at,
		       NULL::timestamptz, NULL::timestamptz,
		       g.can_write, g.created_at, u.username, s.id AS aggregate_id
		FROM shares s
		JOIN share_grants g ON g.share_id = s.id
		LEFT JOIN users u ON u.id = g.target_user_id
		WHERE s.sync_root_id = $1 AND s.share_type = 'user'
		UNION ALL
		SELECT s.id, s.user_id, s.sync_root_id, s.target_user_id, s.path,
		       s.share_type, s.token, s.read_token, s.write_token, s.password_hash, s.expires_at,
		       s.read_expires_at, s.write_expires_at,
		       s.can_write, s.created_at, NULL, s.id AS aggregate_id
		FROM shares s
		WHERE s.sync_root_id = $1 AND s.share_type = 'public'
		ORDER BY path, created_at
	`, syncRootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ShareWithTarget
	for rows.Next() {
		var s ShareWithTarget
		if err := rows.Scan(
			&s.ID, &s.UserID, &s.SyncRootID, &s.TargetUserID, &s.Path, &s.ShareType,
			&s.Token, &s.ReadToken, &s.WriteToken, &s.PasswordHash,
			&s.ExpiresAt, &s.ReadExpiresAt, &s.WriteExpiresAt, &s.CanWrite, &s.CreatedAt,
			&s.TargetUsername, &s.AggregateID,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *ShareRepository) FindByID(ctx context.Context, id uuid.UUID) (*model.Share, error) {
	var s model.Share
	err := scanShare(r.db.QueryRow(ctx,
		`SELECT `+shareColumns+` FROM shares WHERE id = $1`, id), &s)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &s, err
}

// FindByToken resolves a public share by any of its tokens — the legacy/read
// token or the write token (ADR-0015 dual-link). Callers compare the matched
// token against share.WriteToken to decide whether the link grants write
// access.
func (r *ShareRepository) FindByToken(ctx context.Context, token string) (*model.Share, error) {
	var s model.Share
	err := scanShare(r.db.QueryRow(ctx,
		`SELECT `+shareColumns+` FROM shares
		 WHERE token = $1 OR read_token = $1 OR write_token = $1`,
		token,
	), &s)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &s, err
}

func (r *ShareRepository) Delete(ctx context.Context, shareID, userID uuid.UUID) error {
	result, err := r.db.Exec(ctx,
		`DELETE FROM shares WHERE id = $1 AND user_id = $2`,
		shareID, userID,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	_ = r.stor.DeleteShareMeta(shareID)
	return nil
}

// DeleteByTarget removes the recipient's grant on a mount. The grant is the
// recipient's own anchor (its id is the mount id), so deleting it tears down
// the mount from his view without touching the giver's aggregate or the other
// recipients' grants. See ADR-0010/0015.
func (r *ShareRepository) DeleteByTarget(ctx context.Context, grantID, targetUserID uuid.UUID) error {
	result, err := r.db.Exec(ctx,
		`DELETE FROM share_grants WHERE id = $1 AND target_user_id = $2`,
		grantID, targetUserID,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
