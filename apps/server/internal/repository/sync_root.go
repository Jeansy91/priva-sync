package repository

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/model"
	"privasync/server/internal/storage"
)

// SyncRootRepository owns the sync-root aggregate: CRUD on `sync_roots`,
// the on-disk sidecar mirror, byte-size aggregates and the cross-aggregate
// cascade delete (files / conflicts / shares / upload_sessions). The
// cascade lives here because the sync-root is the aggregate root that
// owns those tables; the TX is opened by the caller (coordinator) so the
// delete commits alongside the SyncRootDeleted bus event. See ADR-0001.
type SyncRootRepository struct {
	db   *pgxpool.Pool
	stor *storage.Service
}

func NewSyncRootRepository(db *pgxpool.Pool, stor *storage.Service) *SyncRootRepository {
	return &SyncRootRepository{db: db, stor: stor}
}

func (r *SyncRootRepository) writeRootMeta(sr *model.SyncRoot) {
	if err := r.stor.WriteRootMeta(sr); err != nil {
		slog.Warn("sync root meta write failed", "root_id", sr.ID, "error", err)
	}
}

func (r *SyncRootRepository) CreateSyncRoot(ctx context.Context, userID uuid.UUID, name, path string) (*model.SyncRoot, error) {
	var sr model.SyncRoot
	err := r.db.QueryRow(ctx, `
		INSERT INTO sync_roots (user_id, name, path)
		VALUES ($1, $2, $3)
		RETURNING id, user_id, name, path, created_at
	`, userID, name, path).Scan(&sr.ID, &sr.UserID, &sr.Name, &sr.Path, &sr.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("creating sync root: %w", err)
	}
	r.writeRootMeta(&sr)
	return &sr, nil
}

// UpdateSyncRootName renames a sync root's display name in place. The storage
// directory is keyed by root id, not name, so no bytes move — only the label
// and its on-disk meta sidecar change. ErrConflict on a duplicate name for the
// same user (partial unique), ErrNotFound when the root is not owned by userID.
func (r *SyncRootRepository) UpdateSyncRootName(ctx context.Context, rootID, userID uuid.UUID, name string) (*model.SyncRoot, error) {
	var sr model.SyncRoot
	err := r.db.QueryRow(ctx, `
		UPDATE sync_roots SET name = $3
		WHERE id = $1 AND user_id = $2
		RETURNING id, user_id, name, path, created_at
	`, rootID, userID, name).Scan(&sr.ID, &sr.UserID, &sr.Name, &sr.Path, &sr.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrConflict
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("renaming sync root: %w", err)
	}
	r.writeRootMeta(&sr)
	return &sr, nil
}

// ListSyncRootsWithOwner is the variant used by the public list endpoint:
// joins `users` so the response carries the owner's username and avatar
// state. Internal callers that don't need the owner block keep using
// ListSyncRoots. See ADR-0009.
func (r *SyncRootRepository) ListSyncRootsWithOwner(ctx context.Context, userID uuid.UUID) ([]model.SyncRootWithOwner, error) {
	rows, err := r.db.Query(ctx, `
		SELECT sr.id, sr.user_id, sr.name, sr.path, sr.created_at,
		       u.username, u.avatar_path
		FROM sync_roots sr
		JOIN users u ON u.id = sr.user_id
		WHERE sr.user_id = $1
		ORDER BY sr.name
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roots []model.SyncRootWithOwner
	for rows.Next() {
		var sr model.SyncRootWithOwner
		if err := rows.Scan(&sr.ID, &sr.UserID, &sr.Name, &sr.Path, &sr.CreatedAt,
			&sr.OwnerUsername, &sr.OwnerAvatarPath); err != nil {
			return nil, err
		}
		roots = append(roots, sr)
	}
	return roots, rows.Err()
}

func (r *SyncRootRepository) ListSyncRoots(ctx context.Context, userID uuid.UUID) ([]model.SyncRoot, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, user_id, name, path, created_at FROM sync_roots WHERE user_id = $1 ORDER BY name`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roots []model.SyncRoot
	for rows.Next() {
		var sr model.SyncRoot
		if err := rows.Scan(&sr.ID, &sr.UserID, &sr.Name, &sr.Path, &sr.CreatedAt); err != nil {
			return nil, err
		}
		roots = append(roots, sr)
	}
	return roots, rows.Err()
}

func (r *SyncRootRepository) GetSyncRoot(ctx context.Context, id, userID uuid.UUID) (*model.SyncRoot, error) {
	var sr model.SyncRoot
	err := r.db.QueryRow(ctx,
		`SELECT id, user_id, name, path, created_at FROM sync_roots WHERE id = $1 AND user_id = $2`,
		id, userID,
	).Scan(&sr.ID, &sr.UserID, &sr.Name, &sr.Path, &sr.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &sr, err
}

// GetSyncRootByID looks up a sync root by its UUID (without user scoping).
// Used internally by background jobs that already trust the file records.
func (r *SyncRootRepository) GetSyncRootByID(ctx context.Context, id uuid.UUID) (*model.SyncRoot, error) {
	var sr model.SyncRoot
	err := r.db.QueryRow(ctx,
		`SELECT id, user_id, name, path, created_at FROM sync_roots WHERE id = $1`, id,
	).Scan(&sr.ID, &sr.UserID, &sr.Name, &sr.Path, &sr.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &sr, err
}

// SyncRootSizes sums the used bytes of a sync root, split between active
// files and trash. Used on delete to compute quota deltas without per-file
// queries.
type SyncRootSizes struct {
	UsedBytes  int64
	TrashBytes int64
}

func (r *SyncRootRepository) SumSyncRootSizes(ctx context.Context, rootID uuid.UUID) (*SyncRootSizes, error) {
	return sumSyncRootSizes(ctx, r.db, rootID)
}

func (r *SyncRootRepository) SumSyncRootSizesTx(ctx context.Context, tx pgx.Tx, rootID uuid.UUID) (*SyncRootSizes, error) {
	return sumSyncRootSizes(ctx, tx, rootID)
}

func sumSyncRootSizes(ctx context.Context, q rowScanner, rootID uuid.UUID) (*SyncRootSizes, error) {
	var s SyncRootSizes
	err := q.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(size) FILTER (WHERE is_deleted = FALSE), 0) AS used,
			COALESCE(SUM(size) FILTER (WHERE is_deleted = TRUE AND trash_path IS NOT NULL), 0) AS trash
		FROM files WHERE sync_root_id = $1
	`, rootID).Scan(&s.UsedBytes, &s.TrashBytes)
	return &s, err
}

// DeletedShare captures the minimal info the cascade needs to report
// back so the coordinator can publish ShareRevoked SSE events to every
// recipient of a user-share before the row vanishes. TargetUserID is
// nil for public-token shares. See ADR-0010.
type DeletedShare struct {
	ID           uuid.UUID
	TargetUserID *uuid.UUID
}

// DeleteSyncRootTx performs the cascade-delete inside the caller's TX and
// returns (a) the recipient grants that were torn down — one per recipient,
// keyed on the grant id so the coordinator can publish ShareRevoked to each —
// and (b) the share aggregate ids for off-DB meta cleanup after commit.
// Coordinator owns the TX so it can publish SyncRootDeleted critically
// alongside the row drop. See ADR-0010/0015.
func (r *SyncRootRepository) DeleteSyncRootTx(ctx context.Context, tx pgx.Tx, rootID, userID uuid.UUID) ([]DeletedShare, []uuid.UUID, error) {
	var owned bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM sync_roots WHERE id = $1 AND user_id = $2)`,
		rootID, userID,
	).Scan(&owned); err != nil {
		return nil, nil, err
	}
	if !owned {
		return nil, nil, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM conflicts WHERE file_id IN (
			SELECT id FROM files WHERE sync_root_id = $1
		)`, rootID); err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM upload_sessions WHERE sync_root_id = $1`, rootID); err != nil {
		return nil, nil, err
	}
	// Capture recipient grants before the cascade drops them — each grant id is
	// a recipient's mount anchor, so it carries the ShareRevoked target.
	grantRows, err := tx.Query(ctx, `
		SELECT g.id, g.target_user_id
		FROM share_grants g JOIN shares s ON s.id = g.share_id
		WHERE s.sync_root_id = $1`, rootID)
	if err != nil {
		return nil, nil, err
	}
	var grants []DeletedShare
	for grantRows.Next() {
		var g DeletedShare
		if err := grantRows.Scan(&g.ID, &g.TargetUserID); err != nil {
			grantRows.Close()
			return nil, nil, err
		}
		grants = append(grants, g)
	}
	grantRows.Close()
	if err := grantRows.Err(); err != nil {
		return nil, nil, err
	}
	// Delete shares (cascades grants) and collect aggregate ids for meta cleanup.
	shareRows, err := tx.Query(ctx,
		`DELETE FROM shares WHERE sync_root_id = $1 RETURNING id`, rootID)
	if err != nil {
		return nil, nil, err
	}
	var shareIDs []uuid.UUID
	for shareRows.Next() {
		var id uuid.UUID
		if err := shareRows.Scan(&id); err != nil {
			shareRows.Close()
			return nil, nil, err
		}
		shareIDs = append(shareIDs, id)
	}
	shareRows.Close()
	if err := shareRows.Err(); err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM files WHERE sync_root_id = $1`, rootID); err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM sync_roots WHERE id = $1`, rootID); err != nil {
		return nil, nil, err
	}
	return grants, shareIDs, nil
}

// DeleteSyncRootMetaCleanup removes off-DB meta snapshots for the root and its
// dropped shares. Best-effort — failures are logged, not returned.
func (r *SyncRootRepository) DeleteSyncRootMetaCleanup(rootID uuid.UUID, shareIDs []uuid.UUID) {
	if err := r.stor.DeleteRootMeta(rootID); err != nil {
		slog.Warn("sync root meta delete failed", "root_id", rootID, "error", err)
	}
	if err := r.stor.DeleteShareMetas(shareIDs); err != nil {
		slog.Warn("share meta bulk delete failed", "root_id", rootID, "error", err)
	}
}

// DeleteSyncRoot deletes the sync root including all files rows, conflicts,
// shares, and upload sessions in one transaction. Storage cleanup is the
// caller's responsibility (orphan bytes are logged on failure).
func (r *SyncRootRepository) DeleteSyncRoot(ctx context.Context, rootID, userID uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, shareIDs, err := r.DeleteSyncRootTx(ctx, tx, rootID, userID)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	r.DeleteSyncRootMetaCleanup(rootID, shareIDs)
	return nil
}
