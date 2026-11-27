package repository

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"privasync/server/internal/model"
)

type ListDirectoryLevelParams struct {
	SyncRootID uuid.UUID
	PathPrefix string // "/" or "/sub"
	Cursor     string // keyset on entry name; empty = first page
	Limit      int
}

// ListDirectoryLevel returns the direct children under PathPrefix, paginated
// by keyset on the entry name. See ADR-0008 and ADR-0041.
//
// Every child is a row, directories included, so a level is one index range
// over (sync_root_id, parent_path, path) and costs what the page costs. It
// used to be derived instead: reduce every path in the subtree to its first
// segment, group, take the page. That cost tracked the size of the subtree,
// which made the top level of a million-file root take ~1.9s however few
// entries were asked for.
func (r *FileRepository) ListDirectoryLevel(ctx context.Context, p ListDirectoryLevelParams) ([]model.File, error) {
	prefix := p.PathPrefix
	if prefix == "" {
		prefix = "/"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	// The cursor is an entry name; the keyset compares full paths. Every path
	// under the directory sorts after the prefix itself, so an empty cursor
	// starts at the prefix.
	after := prefix + p.Cursor

	rows, err := r.db.Query(ctx, `
		SELECT `+fileColumns+`
		FROM files
		WHERE sync_root_id = $1
		  AND parent_path  = $2
		  AND is_deleted   = FALSE
		  AND path > $3
		ORDER BY path ASC
		LIMIT $4
	`, p.SyncRootID, prefix, after, p.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []model.File
	for rows.Next() {
		var f model.File
		if err := scanFile(rows, &f); err != nil {
			return nil, err
		}
		entries = append(entries, f)
	}
	return entries, rows.Err()
}
