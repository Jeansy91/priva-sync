package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// MissingDirectory is a directory some live path needs above it that has no
// row. Blocked marks the ones where a live non-directory row sits at that
// path, which the sweep reports rather than repairs.
type MissingDirectory struct {
	SyncRootID uuid.UUID
	Path       string
	Blocked    bool
}

// MaterialiseMissingDirectories inserts the directory rows that some live path
// needs above it but that are not there, and reports the paths where a live
// non-directory row stands where a directory would have to go.
//
// ADR-0041 makes a directory a row, and every write path materialises the
// directories above what it writes. This is the net under that: a directory
// row lost some other way -- a purge that outran its children, a write path
// added later without the call -- leaves the rows beneath it present, syncing
// and unreachable through navigation, which is the kind of fault that stays
// invisible until someone goes looking for a file.
//
// A blocked path is not repaired. It is a state from before ADR-0041 and
// contradictory either way; overwriting a file with a directory would be a
// worse answer than naming it.
func (r *FileRepository) MaterialiseMissingDirectories(ctx context.Context) (created int, blocked []string, err error) {
	missing, err := r.findMissingDirectories(ctx)
	if err != nil {
		return 0, nil, err
	}

	for _, m := range missing {
		if m.Blocked {
			blocked = append(blocked, m.Path)
			continue
		}
		tag, err := r.db.Exec(ctx, `
			INSERT INTO files (user_id, sync_root_id, path, size, mtime, is_dir, is_deleted)
			SELECT sr.user_id, sr.id, $2, 0, NOW(), TRUE, FALSE
			FROM sync_roots sr
			WHERE sr.id = $1
			ON CONFLICT (sync_root_id, path) WHERE is_deleted = FALSE DO NOTHING
		`, m.SyncRootID, m.Path)
		if err != nil {
			return created, blocked, fmt.Errorf("materialising %s: %w", m.Path, err)
		}
		created += int(tag.RowsAffected())
	}
	return created, blocked, nil
}

// findMissingDirectories walks every live path up to the root and returns the
// ancestors that have no directory row.
func (r *FileRepository) findMissingDirectories(ctx context.Context) ([]MissingDirectory, error) {
	rows, err := r.db.Query(ctx, `
		WITH RECURSIVE ancestors AS (
		    SELECT sync_root_id,
		           left(path, length(path) - length(split_part(path, '/', -1)) - 1) AS path
		    FROM files
		    WHERE is_deleted = FALSE
		    UNION
		    SELECT sync_root_id,
		           left(path, length(path) - length(split_part(path, '/', -1)) - 1)
		    FROM ancestors
		    WHERE path <> ''
		)
		SELECT a.sync_root_id, a.path,
		       EXISTS (
		           SELECT 1 FROM files f
		           WHERE f.sync_root_id = a.sync_root_id AND f.path = a.path
		             AND f.is_deleted = FALSE
		       ) AS blocked
		FROM ancestors a
		WHERE a.path <> ''
		  AND NOT EXISTS (
		      SELECT 1 FROM files f
		      WHERE f.sync_root_id = a.sync_root_id AND f.path = a.path
		        AND f.is_deleted = FALSE AND f.is_dir = TRUE
		  )
	`)
	if err != nil {
		return nil, fmt.Errorf("finding missing directories: %w", err)
	}
	defer rows.Close()

	var out []MissingDirectory
	for rows.Next() {
		var m MissingDirectory
		if err := rows.Scan(&m.SyncRootID, &m.Path, &m.Blocked); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
