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

func (r *FileRepository) Upsert(ctx context.Context, f *model.File) (*model.File, error) {
	if err := ensureAncestors(ctx, r.db, f.SyncRootID, f.Path); err != nil {
		return nil, fmt.Errorf("materialising directories above %s: %w", f.Path, err)
	}

	var out model.File
	err := scanFile(r.db.QueryRow(ctx, `
		INSERT INTO files (user_id, sync_root_id, path, size, mtime, content_hash, is_deleted, is_dir, last_modifier_user_id, block_hashes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (sync_root_id, path) WHERE is_deleted = FALSE DO UPDATE SET
			size                  = EXCLUDED.size,
			mtime                 = EXCLUDED.mtime,
			content_hash          = EXCLUDED.content_hash,
			is_deleted            = FALSE,
			is_dir                = EXCLUDED.is_dir,
			trash_path            = NULL,
			deleted_at            = NULL,
			updated_at            = NOW(),
			last_modifier_user_id = EXCLUDED.last_modifier_user_id,
			block_hashes          = EXCLUDED.block_hashes
		RETURNING `+fileColumns,
		f.UserID, f.SyncRootID, f.Path, f.Size, f.MTime, f.ContentHash, f.IsDeleted, f.IsDir, f.LastModifierUserID, f.BlockHashes,
	), &out)
	if err != nil {
		return nil, fmt.Errorf("upserting file: %w", err)
	}
	return &out, nil
}

// CreateDirectory inserts (or revives) a directory entry — a zero-byte
// is_dir=TRUE row that represents an explicit folder so empty directories
// can be synced between clients. Returns ErrConflict if the same path
// is already occupied by a file.
func (r *FileRepository) CreateDirectory(ctx context.Context, userID, syncRootID uuid.UUID, dirPath string, mtime time.Time) (*model.File, error) {
	if err := ensureAncestors(ctx, r.db, syncRootID, dirPath); err != nil {
		return nil, fmt.Errorf("materialising directories above %s: %w", dirPath, err)
	}

	var out model.File
	err := scanFile(r.db.QueryRow(ctx, `
		INSERT INTO files (user_id, sync_root_id, path, size, mtime, content_hash, is_deleted, is_dir)
		VALUES ($1, $2, $3, 0, $4, NULL, FALSE, TRUE)
		ON CONFLICT (sync_root_id, path) WHERE is_deleted = FALSE DO UPDATE SET
			is_deleted = FALSE,
			trash_path = NULL,
			deleted_at = NULL,
			updated_at = NOW()
		WHERE files.is_dir = TRUE
		RETURNING `+fileColumns,
		userID, syncRootID, dirPath, mtime,
	), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		// ON CONFLICT fired but WHERE is_dir = TRUE did not match: a file
		// already occupies the path.
		return nil, ErrConflict
	}
	if err != nil {
		return nil, fmt.Errorf("creating directory: %w", err)
	}
	return &out, nil
}

// DirectoryCreator is the minimal interface CreateDirectoryWithStorage needs
// for the DB step — satisfied by *FileRepository directly (webdav) and by
// any narrower per-handler seam interface (ADR-0029) that carries the same
// CreateDirectory method (REST own-root and mount handlers).
type DirectoryCreator interface {
	CreateDirectory(ctx context.Context, userID, syncRootID uuid.UUID, dirPath string, mtime time.Time) (*model.File, error)
}

// DirectoryDisk is the minimal interface for the on-disk mirror step —
// satisfied by *storage.Service without importing that package here.
type DirectoryDisk interface {
	MakeDir(userID uuid.UUID, syncRootID, dirPath string) error
}

// CreateDirectoryWithStorage is the shared DB-row + on-disk-dir choreography
// behind an explicit empty-directory creation (ADR-0018): the Sync
// Coordinator is deliberately bypassed here — an empty directory needs none
// of its delta-sync/conflict machinery — so every entry point that creates
// one used to reimplement the same two-step call by hand (REST own-root,
// REST mount, WebDAV MKCOL).
//
// What happens to a MakeDir failure differs per caller — REST logs it and
// keeps the DB row authoritative (FS reflection is opportunistic), WebDAV
// propagates it as the MKCOL result — so that decision stays with the
// caller: this returns the mkdir error alongside the created row instead of
// handling it. mkdirErr is only meaningful when err is nil.
func CreateDirectoryWithStorage(
	ctx context.Context,
	files DirectoryCreator,
	disk DirectoryDisk,
	userID, syncRootID uuid.UUID,
	dirPath string,
	mtime time.Time,
) (dir *model.File, mkdirErr error, err error) {
	dir, err = files.CreateDirectory(ctx, userID, syncRootID, dirPath, mtime)
	if err != nil {
		return nil, nil, err
	}
	mkdirErr = disk.MakeDir(userID, syncRootID.String(), dirPath)
	return dir, mkdirErr, nil
}

// UpsertWithVersionCheck upserts a file record, but first checks that the
// caller's known server_version matches the current value (optimistic lock).
// If there is a version mismatch, it returns (existingFile, true, nil) so
// the handler can return a 409 with the current server state.
// Pass clientVersion = -1 to skip the version check (e.g. brand-new file).
func (r *FileRepository) UpsertWithVersionCheck(
	ctx context.Context,
	f *model.File,
	clientVersion int64,
) (*model.File, bool, error) {
	if clientVersion >= 0 {
		existing, err := r.FindByPath(ctx, f.SyncRootID, f.Path)
		if err == nil && !existing.IsDeleted {
			if existing.ServerVersion != clientVersion {
				return existing, true, nil // conflict
			}
		}
	}
	result, err := r.Upsert(ctx, f)
	return result, false, err
}

// MoveFileWithVersionCheck moves a file with an optimistic version check
// (move-move conflict detection). Pass clientVersion = -1 to skip the check.
//
// When the current file at fromPath no longer exists (e.g. another client
// already moved it elsewhere) OR its server_version differs from
// clientVersion, returns (existing, true, nil) so the caller can create a
// move-conflict copy. `existing` then points at the current server state of
// the file (when findable via file_id).
func (r *FileRepository) MoveFileWithVersionCheck(
	ctx context.Context,
	syncRootID uuid.UUID,
	fromPath string,
	toPath string,
	clientVersion int64,
	actor uuid.UUID,
) (*model.File, bool, error) {
	if clientVersion >= 0 {
		existing, err := r.FindByPath(ctx, syncRootID, fromPath)
		if errors.Is(err, ErrNotFound) {
			// Source gone -- most likely already moved. No file id at hand.
			return nil, true, nil
		}
		if err != nil {
			return nil, false, err
		}
		if existing.ServerVersion != clientVersion {
			return existing, true, nil
		}
	}
	out, err := r.MoveFile(ctx, syncRootID, fromPath, toPath, actor)
	return out, false, err
}

// MoveDirectory renames a directory and rewrites every descendant path: the
// dir row itself (path = fromPath) plus all children under `fromPath/`. The
// substring splice keeps each child's suffix and swaps only the prefix, so
// /old → /new turns /old/sub/a.txt into /new/sub/a.txt.
//
// The affected rows deliberately get NO fresh seq (ADR-0065): a single
// move_events row, inserted in the same transaction, is the one changes-feed
// signal for the whole subtree — a per-row bump would flood the feed with
// one entry per descendant again, which is exactly the problem ADR-0065
// solves. The files_assign_seq trigger is told to keep NEW.seq := OLD.seq
// for exactly this statement via a transaction-local GUC, set immediately
// before the bulk UPDATE and reset immediately after so no other write in
// this transaction — ensureAncestors creates real new rows that must keep
// getting a real seq — is affected by the suppression.
//
// No tombstone is left at the vacated paths any more — the move_events row
// replaces that role entirely; the desktop applies the prefix change itself
// instead of reconciling the feed path-by-path.
//
// Runs in one transaction so a crash can never leave ancestors materialised
// without the move applied, or the move applied without its move_events
// signal.
//
// ErrConflict is returned when toPath already holds a live entry; ErrNotFound
// when neither the directory row nor any descendant exists.
func (r *FileRepository) MoveDirectory(ctx context.Context, syncRootID uuid.UUID, fromPath, toPath string, actor uuid.UUID) (*model.File, error) {
	// Destination must be free — a live row at toPath (file or dir) blocks.
	existing, err := r.FindByPath(ctx, syncRootID, toPath)
	if err == nil && !existing.IsDeleted {
		return nil, ErrConflict
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if err := ensureAncestors(ctx, tx, syncRootID, toPath); err != nil {
		return nil, fmt.Errorf("materialising directories above %s: %w", toPath, err)
	}

	_, lo, hi := subtreeRange(fromPath)
	if _, err := tx.Exec(ctx, `SET LOCAL app.suppress_seq_bump = 'on'`); err != nil {
		return nil, fmt.Errorf("suppressing seq bump: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE files f
		SET path                  = $3 || substring(f.path FROM length($2) + 1),
		    updated_at            = NOW(),
		    last_modifier_user_id = $4
		WHERE f.sync_root_id = $1
		  AND f.is_deleted = FALSE
		  AND (f.path = $2 OR (f.path ~>=~ $5 AND f.path ~<~ $6))`,
		syncRootID, fromPath, toPath, actor, lo, hi,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("moving directory: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `RESET app.suppress_seq_bump`); err != nil {
		return nil, fmt.Errorf("resetting seq bump suppression: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO move_events (sync_root_id, old_prefix, new_prefix, seq)
		VALUES ($1, $2, $3, nextval('files_seq_seq'))`,
		syncRootID, fromPath, toPath,
	); err != nil {
		return nil, fmt.Errorf("recording move event: %w", err)
	}

	// The renamed dir usually has its own row at toPath. Implicit directories
	// (inferred from child paths, no own row) leave nothing at toPath after
	// the rewrite — synthesise a directory row so callers have a path to
	// announce. ID stays zero; no consumer keys off it for a folder move.
	var out model.File
	err = scanFile(tx.QueryRow(ctx, `
		SELECT `+fileColumns+`
		FROM files WHERE sync_root_id = $1 AND path = $2 AND is_deleted = FALSE
	`, syncRootID, toPath), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		out = model.File{SyncRootID: syncRootID, Path: toPath, IsDir: true}
	} else if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

// CopyFile duplicates a single file row at toPath: a new id, a fresh
// server_version (0 via the column default), but the same content_hash, size,
// mtime and is_dir as the source. The on-disk bytes are copied separately by
// the storage outbox; this only forges the DB row. actor records who triggered
// the copy into last_modifier_user_id (ADR-0013).
//
// ErrNotFound is returned when no live row sits at fromPath; ErrConflict when
// toPath is already occupied by a live row.
func (r *FileRepository) CopyFile(ctx context.Context, syncRootID uuid.UUID, fromPath, toPath string, actor uuid.UUID) (*model.File, error) {
	if existing, err := r.FindByPath(ctx, syncRootID, toPath); err == nil && !existing.IsDeleted {
		return nil, ErrConflict
	}
	if err := ensureAncestors(ctx, r.db, syncRootID, toPath); err != nil {
		return nil, fmt.Errorf("materialising directories above %s: %w", toPath, err)
	}

	var out model.File
	err := scanFile(r.db.QueryRow(ctx, `
		INSERT INTO files (user_id, sync_root_id, path, size, mtime, content_hash,
		                   is_deleted, is_dir, last_modifier_user_id, block_hashes)
		SELECT user_id, sync_root_id, $3, size, mtime, content_hash,
		       FALSE, is_dir, $4, block_hashes
		FROM files
		WHERE sync_root_id = $1 AND path = $2 AND is_deleted = FALSE
		RETURNING `+fileColumns,
		syncRootID, fromPath, toPath, actor,
	), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("copying file: %w", err)
	}
	return &out, nil
}

// CopyDirectory recursively duplicates a folder subtree under toPath: the dir
// row itself (path = fromPath) plus every descendant under `fromPath/`. Each
// new row gets a new id and a fresh server_version (column default) while
// keeping content_hash, size, mtime and is_dir; the substring splice swaps only
// the path prefix so /old/sub/a.txt becomes /new/sub/a.txt. The on-disk bytes
// are copied separately by the storage outbox.
//
// ErrConflict is returned when toPath already holds a live entry; ErrNotFound
// when neither the directory row nor any descendant exists.
func (r *FileRepository) CopyDirectory(ctx context.Context, syncRootID uuid.UUID, fromPath, toPath string, actor uuid.UUID) (*model.File, error) {
	if existing, err := r.FindByPath(ctx, syncRootID, toPath); err == nil && !existing.IsDeleted {
		return nil, ErrConflict
	}

	if err := ensureAncestors(ctx, r.db, syncRootID, toPath); err != nil {
		return nil, fmt.Errorf("materialising directories above %s: %w", toPath, err)
	}

	_, lo, hi := subtreeRange(fromPath)
	tag, err := r.db.Exec(ctx, `
		INSERT INTO files (user_id, sync_root_id, path, size, mtime, content_hash,
		                   is_deleted, is_dir, last_modifier_user_id, block_hashes)
		SELECT user_id, sync_root_id,
		       $3 || substring(path FROM length($2) + 1),
		       size, mtime, content_hash, FALSE, is_dir, $4, block_hashes
		FROM files
		WHERE sync_root_id = $1
		  AND is_deleted = FALSE
		  AND (path = $2 OR (path ~>=~ $5 AND path ~<~ $6))`,
		syncRootID, fromPath, toPath, actor, lo, hi,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("copying directory: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}

	// Like MoveDirectory: an implicit source directory (no own row) leaves
	// nothing at toPath after the copy — synthesise a dir row to announce.
	out, err := r.FindByPath(ctx, syncRootID, toPath)
	if errors.Is(err, ErrNotFound) {
		return &model.File{SyncRootID: syncRootID, Path: toPath, IsDir: true}, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MoveFile relocates a file entry from fromPath to toPath within the same sync root.
// Returns the updated entry. ErrConflict is returned when toPath already exists.
// actor records who triggered the move into last_modifier_user_id (ADR-0013).
//
// Like MoveDirectory it drops an is_deleted tombstone at fromPath so the
// path-keyed desktop pull removes the old copy instead of keeping it next to
// the renamed file. Tombstone + rewrite are one statement.
func (r *FileRepository) MoveFile(ctx context.Context, syncRootID uuid.UUID, fromPath, toPath string, actor uuid.UUID) (*model.File, error) {
	// Check whether the destination path already exists (and is not deleted).
	existing, err := r.FindByPath(ctx, syncRootID, toPath)
	if err == nil && !existing.IsDeleted {
		return nil, ErrConflict
	}
	if err := ensureAncestors(ctx, r.db, syncRootID, toPath); err != nil {
		return nil, fmt.Errorf("materialising directories above %s: %w", toPath, err)
	}

	var out model.File
	err = scanFile(r.db.QueryRow(ctx, `
		WITH tombstone AS (
			INSERT INTO files (user_id, sync_root_id, path, size, mtime, content_hash,
			                   is_deleted, is_dir, updated_at, deleted_at, last_modifier_user_id)
			SELECT user_id, sync_root_id, path, 0, mtime, NULL,
			       TRUE, is_dir, NOW(), NOW(), $4
			FROM files
			WHERE sync_root_id = $1 AND path = $2 AND is_deleted = FALSE
		)
		UPDATE files
		SET path                  = $3,
		    updated_at            = NOW(),
		    last_modifier_user_id = $4
		WHERE sync_root_id = $1 AND path = $2 AND is_deleted = FALSE
		RETURNING `+fileColumns,
		syncRootID, fromPath, toPath, actor,
	), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("moving file: %w", err)
	}
	return &out, nil
}
