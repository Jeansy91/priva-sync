package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/model"
)

type FileRepository struct {
	db *pgxpool.Pool
}

func NewFileRepository(db *pgxpool.Pool) *FileRepository {
	return &FileRepository{db: db}
}

// SyncRoot-Methoden + SyncRootSizes leben in sync_root.go (siehe ADR-0001).
// Trash-Methoden leben in trash.go, Conflict-Methoden in conflict.go.

// rowScanner is the minimal interface satisfied by *pgxpool.Pool and pgx.Tx,
// allowing the same query helper to run inside or outside a transaction.
type rowScanner interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// scanFile scans the full file row (all columns in SELECT order).
func scanFile(row interface {
	Scan(dest ...any) error
}, f *model.File) error {
	return row.Scan(
		&f.ID, &f.UserID, &f.SyncRootID, &f.Path,
		&f.Size, &f.MTime, &f.ContentHash, &f.IsDeleted, &f.IsDir, &f.UpdatedAt,
		&f.ServerVersion, &f.TrashPath, &f.DeletedAt,
		&f.LastModifierUserID, &f.BlockHashes, &f.Seq,
	)
}

const fileColumns = `id, user_id, sync_root_id, path, size, mtime, content_hash, is_deleted, is_dir, updated_at, server_version, trash_path, deleted_at, last_modifier_user_id, block_hashes, seq`

// walkPageSize is how many rows one round trip of a paged walk pulls. Large
// enough that a big result set costs few queries, small enough that the rows
// in flight stay a fixed, small amount of memory.
const walkPageSize = 1000

// collectFiles drains one page of rows and closes them. Kept separate so a
// walk's loop cannot leak a Rows across iterations.
func collectFiles(rows pgx.Rows) ([]model.File, error) {
	defer rows.Close()

	var out []model.File
	for rows.Next() {
		var f model.File
		if err := scanFile(rows, &f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
