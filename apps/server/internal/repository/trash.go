package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/model"
)

// TrashRepository owns the soft-delete lifecycle of `files` rows:
// SoftDelete → ListTrash / FindTrashedByID → RestoreFromTrash /
// PurgeTrashRecord. The active-row CRUD lives in FileRepository
// (see ADR-0001). Shares scanFile + fileColumns at package level.
type TrashRepository struct {
	db *pgxpool.Pool
}

func NewTrashRepository(db *pgxpool.Pool) *TrashRepository {
	return &TrashRepository{db: db}
}

// SoftDelete marks the file at filePath as trashed, records the trash blob
// location and bumps server_version. Returns ErrNotFound when the live row
// is missing. actor records who triggered the delete into
// last_modifier_user_id (ADR-0013).
func (r *TrashRepository) SoftDelete(ctx context.Context, syncRootID uuid.UUID, filePath, trashPath string, actor uuid.UUID) (*model.File, error) {
	var f model.File
	err := scanFile(r.db.QueryRow(ctx, `
		UPDATE files
		SET is_deleted            = TRUE,
		    trash_path            = $3,
		    deleted_at            = NOW(),
		    updated_at            = NOW(),
		    server_version        = server_version + 1,
		    last_modifier_user_id = $4
		WHERE sync_root_id = $1 AND path = $2 AND is_deleted = FALSE
		RETURNING `+fileColumns+`
	`, syncRootID, filePath, trashPath, actor), &f)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// MarkDeleted soft-deletes a file without recording a trash path (permanent
// delete). Used by the empty-trash pipeline for the second-pass row removal
// and by callers that bypass the trash feature.
func (r *TrashRepository) MarkDeleted(ctx context.Context, syncRootID uuid.UUID, path string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE files SET is_deleted = TRUE, updated_at = NOW() WHERE sync_root_id = $1 AND path = $2`,
		syncRootID, path,
	)
	return err
}

// EachTrashedBefore calls fn for every soft-deleted file across all users
// older than the cutoff that still has a recorded trash path. Used by the
// background purge sweep.
//
// Paged rather than collected: the sweep runs over every user's trash at
// once, and it does slow per-row work -- a storage delete and a transaction --
// so neither the whole result set nor an open cursor may be held for its
// duration.
//
// Oldest first, as before, but keyed on (deleted_at, id) rather than
// deleted_at alone: the callback removes rows as it goes, a purged row leaves
// the result set and one that fails to purge stays. A cursor on a value that
// can repeat would either skip its neighbours or hand the failed row back
// forever; adding the id makes the keyset total and forward-only.
func (r *TrashRepository) EachTrashedBefore(ctx context.Context, before time.Time, fn func(model.File) error) error {
	var (
		afterTime time.Time
		afterID   = uuid.Nil
	)

	for {
		rows, err := r.db.Query(ctx, `
			SELECT `+fileColumns+`
			FROM files
			WHERE is_deleted = TRUE
			  AND deleted_at IS NOT NULL
			  AND trash_path IS NOT NULL
			  AND deleted_at < $1
			  AND (deleted_at, id) > ($2, $3)
			ORDER BY deleted_at ASC, id ASC
			LIMIT $4
		`, before, afterTime, afterID, walkPageSize)
		if err != nil {
			return err
		}

		page, err := collectFiles(rows)
		if err != nil {
			return err
		}
		for _, f := range page {
			if err := fn(f); err != nil {
				return err
			}
		}
		if len(page) < walkPageSize {
			return nil
		}
		last := page[len(page)-1]
		afterTime, afterID = *last.DeletedAt, last.ID
	}
}

// ListTrashedBefore returns soft-deleted files older than the given cutoff
// that have a trash path recorded (scoped to one user).
func (r *TrashRepository) ListTrashedBefore(ctx context.Context, userID uuid.UUID, before time.Time) ([]model.File, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+fileColumns+`
		FROM files
		WHERE user_id = $1
		  AND is_deleted = TRUE
		  AND deleted_at IS NOT NULL
		  AND trash_path IS NOT NULL
		  AND deleted_at < $2
		ORDER BY deleted_at ASC
	`, userID, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []model.File
	for rows.Next() {
		var f model.File
		if err := scanFile(rows, &f); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// PurgeTrashRecord removes a single file record permanently after the
// physical file has been deleted from the trash directory.
func (r *TrashRepository) PurgeTrashRecord(ctx context.Context, fileID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM files WHERE id = $1`, fileID)
	return err
}

// PurgeTrashRecordTx is the TX-bound variant of PurgeTrashRecord. The
// coordinator's empty-trash pipeline batches multiple deletes inside a
// single domain TX so the outbox inserts that follow are atomic with the
// row removals.
func (r *TrashRepository) PurgeTrashRecordTx(ctx context.Context, tx pgx.Tx, fileID uuid.UUID) error {
	_, err := tx.Exec(ctx, `DELETE FROM files WHERE id = $1`, fileID)
	return err
}

// ListAllTrashedByUser returns every soft-deleted file across the user's
// sync roots that has a physical trash blob recorded. Used by the
// empty-trash pipeline.
func (r *TrashRepository) ListAllTrashedByUser(ctx context.Context, userID uuid.UUID) ([]model.File, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+fileColumns+`
		FROM files
		WHERE user_id = $1
		  AND is_deleted = TRUE
		  AND trash_path IS NOT NULL
		ORDER BY deleted_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []model.File
	for rows.Next() {
		var f model.File
		if err := scanFile(rows, &f); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// ListTrash lists all trashed (soft-deleted) files for a sync root, newest
// first.
func (r *TrashRepository) ListTrash(ctx context.Context, syncRootID uuid.UUID) ([]model.File, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+fileColumns+`
		FROM files
		WHERE sync_root_id = $1
		  AND is_deleted = TRUE
		  AND trash_path IS NOT NULL
		ORDER BY deleted_at DESC NULLS LAST
	`, syncRootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []model.File
	for rows.Next() {
		var f model.File
		if err := scanFile(rows, &f); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// FindTrashedByID looks up a single trashed file by its UUID, scoped to a
// user.
func (r *TrashRepository) FindTrashedByID(ctx context.Context, fileID, userID uuid.UUID) (*model.File, error) {
	var f model.File
	err := scanFile(r.db.QueryRow(ctx, `
		SELECT `+fileColumns+`
		FROM files
		WHERE id = $1 AND user_id = $2 AND is_deleted = TRUE AND trash_path IS NOT NULL
	`, fileID, userID), &f)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &f, err
}

// RestoreFromTrash clears the soft-delete markers, restoring the file to
// active state. actor records who triggered the restore into
// last_modifier_user_id (ADR-0013).
func (r *TrashRepository) RestoreFromTrash(ctx context.Context, fileID uuid.UUID, actor uuid.UUID) (*model.File, error) {
	var out model.File
	err := scanFile(r.db.QueryRow(ctx, `
		UPDATE files
		SET is_deleted            = FALSE,
		    trash_path            = NULL,
		    deleted_at            = NULL,
		    updated_at            = NOW(),
		    last_modifier_user_id = $2
		WHERE id = $1
		RETURNING `+fileColumns,
		fileID, actor,
	), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &out, restoreAncestors(ctx, r.db, &out)
}

// restoreAncestors materialises the directories a restored row needs above
// it -- they may have been purged while it sat in the trash (ADR-0041).
//
// Unlike the create paths this runs after the row is back, because a restore
// is addressed by file id and the path is only known once the row is read.
// The directory therefore carries the larger seq. A client that meets the
// file first creates the folder on the way to writing it, so the order costs
// nothing.
func restoreAncestors(ctx context.Context, q execer, f *model.File) error {
	if err := ensureAncestors(ctx, q, f.SyncRootID, f.Path); err != nil {
		return fmt.Errorf("materialising directories above %s: %w", f.Path, err)
	}
	return nil
}

// RestoreFromTrashAtPath restores a trashed file but onto a different path
// (loser-path on restore-on-conflict). Returns the live row at newPath.
// actor records who triggered the restore into last_modifier_user_id.
func (r *TrashRepository) RestoreFromTrashAtPath(ctx context.Context, fileID uuid.UUID, newPath string, actor uuid.UUID) (*model.File, error) {
	var out model.File
	err := scanFile(r.db.QueryRow(ctx, `
		UPDATE files
		SET is_deleted            = FALSE,
		    trash_path            = NULL,
		    deleted_at            = NULL,
		    path                  = $2,
		    updated_at            = NOW(),
		    server_version        = server_version + 1,
		    last_modifier_user_id = $3
		WHERE id = $1
		RETURNING `+fileColumns,
		fileID, newPath, actor,
	), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &out, restoreAncestors(ctx, r.db, &out)
}
