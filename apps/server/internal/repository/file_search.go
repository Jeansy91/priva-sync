package repository

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"privasync/server/internal/model"
)

// SearchFilesParams parametrises the server-side file/directory search.
// LikePattern is the already-translated LIKE body the handler produced
// (user wildcard `*` → `%`, SQL metacharacters escaped).
type SearchFilesParams struct {
	SyncRootID  uuid.UUID
	PathPrefix  string // "/" or "/sub" — wrapped with "%" internally
	LikePattern string // escaped, without surrounding `%`
	Limit       int    // Anzahl Treffer; muss > 0 sein
	Cursor      string // path > cursor; empty = first page
}

// SearchFiles returns non-deleted files/directories whose path matches
// LikePattern case-insensitively and lives under PathPrefix. Paginated
// by keyset on path. See ADR-0008.
func (r *FileRepository) SearchFiles(ctx context.Context, p SearchFilesParams) ([]model.File, error) {
	prefix := p.PathPrefix
	if prefix == "" {
		prefix = "/"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	// Wrap with `%...%` for substring semantics; backslash is the escape char.
	// This one is a genuine pattern -- the user's own search term, already
	// translated and escaped by the handler. The prefix beside it is not, and
	// uses range bounds so a directory named "a_b" cannot widen the scope.
	wrapped := "%" + p.LikePattern + "%"
	lo, hi, _ := pathPrefixRange(prefix)

	rows, err := r.db.Query(ctx, `
		SELECT `+fileColumns+`
		FROM files
		WHERE sync_root_id = $1
		  AND is_deleted   = FALSE
		  AND path ~>=~ $2 AND path ~<~ $3
		  AND lower(path) LIKE $4 ESCAPE '\'
		  AND path > $5
		ORDER BY path ASC
		LIMIT $6
	`, p.SyncRootID, lo, hi, wrapped, p.Cursor, p.Limit)
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
