package repository

import (
	"context"

	"github.com/google/uuid"

	"privasync/server/internal/model"
)

// EachInSubtree calls fn for every live row at or below dirPath, ordered by
// path so a parent always arrives before its children.
//
// Paged rather than streamed from one open cursor: a caller that walks a
// subtree is usually doing slow work per row -- reading bytes off disk,
// compressing them into a ZIP, deleting a file and committing a transaction --
// and an open cursor would pin a pool connection for the whole of it. Paging
// gives the same bounded memory and hands the connection back between pages.
//
// The keyset is the path. Rows the callback removes from the result set (a
// cascade delete soft-deleting as it goes) simply stop appearing, and the
// cursor only ever moves forward, so nothing is visited twice.
func (r *FileRepository) EachInSubtree(
	ctx context.Context,
	syncRootID uuid.UUID,
	dirPath string,
	fn func(model.File) error,
) error {
	self, lo, hi := subtreeRange(dirPath)
	after := ""

	for {
		rows, err := r.db.Query(ctx, `
			SELECT `+fileColumns+`
			FROM files
			WHERE sync_root_id = $1
			  AND is_deleted = FALSE
			  AND (path = $2 OR (path ~>=~ $3 AND path ~<~ $4))
			  AND path > $5
			ORDER BY path ASC
			LIMIT $6
		`, syncRootID, self, lo, hi, after, walkPageSize)
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
		after = page[len(page)-1].Path
	}
}
