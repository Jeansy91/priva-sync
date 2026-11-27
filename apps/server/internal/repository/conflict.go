package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/model"
)

// ConflictRepository owns the lifecycle of `conflicts` rows: create, list
// open, resolve. JOINs `files` for ownership / scoping but does not write
// to it. See ADR-0001 (Repository-Split nach Aggregaten).
type ConflictRepository struct {
	db *pgxpool.Pool
}

func NewConflictRepository(db *pgxpool.Pool) *ConflictRepository {
	return &ConflictRepository{db: db}
}

// CreateConflict records a conflict. loserMTime is the loser copy's own
// modification time; nil when unknown.
func (r *ConflictRepository) CreateConflict(ctx context.Context, fileID uuid.UUID, conflictPath string, clientID *uuid.UUID, loserMTime *time.Time) (*model.Conflict, error) {
	var c model.Conflict
	err := r.db.QueryRow(ctx, `
		INSERT INTO conflicts (file_id, conflict_path, client_id, loser_mtime)
		VALUES ($1, $2, $3, $4)
		RETURNING id, file_id, conflict_path, client_id, resolved, created_at, resolved_at
	`, fileID, conflictPath, clientID, loserMTime).Scan(
		&c.ID, &c.FileID, &c.ConflictPath, &c.ClientID,
		&c.Resolved, &c.CreatedAt, &c.ResolvedAt,
	)
	return &c, err
}

func (r *ConflictRepository) ListOpenConflicts(ctx context.Context, userID, rootID uuid.UUID) ([]model.Conflict, error) {
	rows, err := r.db.Query(ctx, `
		SELECT c.id, c.file_id, c.conflict_path, c.client_id, c.resolved, c.created_at, c.resolved_at
		FROM conflicts c
		JOIN files f ON f.id = c.file_id
		WHERE f.user_id = $1 AND f.sync_root_id = $2 AND c.resolved = FALSE
		ORDER BY c.created_at DESC
	`, userID, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var conflicts []model.Conflict
	for rows.Next() {
		var c model.Conflict
		if err := rows.Scan(
			&c.ID, &c.FileID, &c.ConflictPath, &c.ClientID,
			&c.Resolved, &c.CreatedAt, &c.ResolvedAt,
		); err != nil {
			return nil, err
		}
		conflicts = append(conflicts, c)
	}
	return conflicts, rows.Err()
}

// ListOpenConflictsUnderPrefix is ListOpenConflicts scoped to a path subtree
// of one owner's root — the conflicts a Shared-Mount recipient may see inside
// share.path (ADR-0011). `ownerID` is the giver (the files' user_id), not the
// recipient. Conflict paths are giver-absolute; the caller relativizes them
// for the mount response.
func (r *ConflictRepository) ListOpenConflictsUnderPrefix(ctx context.Context, ownerID, rootID uuid.UUID, prefix string) ([]model.Conflict, error) {
	self, lo, hi := subtreeRange(prefix)
	rows, err := r.db.Query(ctx, `
		SELECT c.id, c.file_id, c.conflict_path, c.client_id, c.resolved, c.created_at, c.resolved_at
		FROM conflicts c
		JOIN files f ON f.id = c.file_id
		WHERE f.user_id = $1 AND f.sync_root_id = $2 AND c.resolved = FALSE
		  AND (f.path = $3 OR (f.path ~>=~ $4 AND f.path ~<~ $5))
		ORDER BY c.created_at DESC
	`, ownerID, rootID, self, lo, hi)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var conflicts []model.Conflict
	for rows.Next() {
		var c model.Conflict
		if err := rows.Scan(
			&c.ID, &c.FileID, &c.ConflictPath, &c.ClientID,
			&c.Resolved, &c.CreatedAt, &c.ResolvedAt,
		); err != nil {
			return nil, err
		}
		conflicts = append(conflicts, c)
	}
	return conflicts, rows.Err()
}

// ConflictDetail joins an open conflict with its live file — everything a
// client needs to offer an informed resolve choice. Owner/root/paths also
// drive the permission checks of the detail endpoints.
type ConflictDetail struct {
	ID                 uuid.UUID
	FileID             uuid.UUID
	OwnerID            uuid.UUID
	SyncRootID         uuid.UUID
	FilePath           string
	ConflictPath       string
	CreatedAt          time.Time
	FileSize           int64
	FileMTime          time.Time
	LoserMTime         *time.Time // nil for conflicts created before migration 041
	LastModifierUserID *uuid.UUID
}

// FindOpenConflictDetail returns the detail of an open conflict, or
// ErrNotFound when the conflict is missing or already resolved.
func (r *ConflictRepository) FindOpenConflictDetail(ctx context.Context, conflictID uuid.UUID) (*ConflictDetail, error) {
	var d ConflictDetail
	err := r.db.QueryRow(ctx, `
		SELECT c.id, c.file_id, f.user_id, f.sync_root_id, f.path, c.conflict_path,
		       c.created_at, f.size, f.mtime, c.loser_mtime, f.last_modifier_user_id
		FROM conflicts c
		JOIN files f ON f.id = c.file_id
		WHERE c.id = $1 AND c.resolved = FALSE
	`, conflictID).Scan(
		&d.ID, &d.FileID, &d.OwnerID, &d.SyncRootID, &d.FilePath, &d.ConflictPath,
		&d.CreatedAt, &d.FileSize, &d.FileMTime, &d.LoserMTime, &d.LastModifierUserID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ResolvedConflict describes the outcome of a successful resolve: the
// storage coordinates of the orphan conflict copy (for deletion) plus
// file_id and original path for push notifications to other sessions.
type ResolvedConflict struct {
	FileID       uuid.UUID
	SyncRootID   uuid.UUID
	ConflictPath string
	FilePath     string
}

// ResolveConflict marks the conflict resolved and returns the storage
// coordinates (sync_root_id + conflict_path) of the orphan conflict copy
// so the caller can delete the file from storage. Returns ErrNotFound if
// the conflict is missing, already resolved, or owned by a different user.
func (r *ConflictRepository) ResolveConflict(ctx context.Context, conflictID, userID uuid.UUID) (*ResolvedConflict, error) {
	return resolveConflict(ctx, r.db, conflictID, userID)
}

func (r *ConflictRepository) ResolveConflictTx(ctx context.Context, tx pgx.Tx, conflictID, userID uuid.UUID) (*ResolvedConflict, error) {
	return resolveConflict(ctx, tx, conflictID, userID)
}

func resolveConflict(ctx context.Context, q rowScanner, conflictID, userID uuid.UUID) (*ResolvedConflict, error) {
	var rc ResolvedConflict
	err := q.QueryRow(ctx, `
		UPDATE conflicts c SET resolved = TRUE, resolved_at = NOW()
		FROM files f
		WHERE c.file_id = f.id AND c.id = $1 AND f.user_id = $2 AND c.resolved = FALSE
		RETURNING f.id, f.sync_root_id, c.conflict_path, f.path
	`, conflictID, userID).Scan(&rc.FileID, &rc.SyncRootID, &rc.ConflictPath, &rc.FilePath)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rc, nil
}
