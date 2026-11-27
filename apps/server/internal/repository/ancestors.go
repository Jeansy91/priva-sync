package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// execer is the write half of pgx that a pool and a transaction both offer,
// so ancestor materialisation works inside a caller's TX as well as outside.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// dirAncestors lists the directories a row at path needs above it, outermost
// first: "/a/b/c.txt" -> ["/a", "/a/b"]. The root is not a row and is not
// returned.
func dirAncestors(path string) []string {
	var out []string
	for i := 1; i < len(path); i++ {
		if path[i] == '/' {
			out = append(out, path[:i])
		}
	}
	return out
}

// ensureAncestors materialises the directory rows above path (ADR-0041).
// Every write path that can introduce a new path calls it first, so a
// directory is never merely implied by the paths below it.
//
// Called before the row it makes room for, not after: the directory then
// carries the smaller seq, and a client walking the changes feed learns about
// the folder before the file inside it. If the caller's own write then fails,
// what stays behind is an empty directory -- visible, harmless, and not worth
// a transaction and its round trips to avoid.
//
// The owning user comes from the sync root rather than from the caller: the
// rows in a root belong to its owner even when a Shared-Mount recipient is
// the one writing, and a parameter would be one more thing to pass wrongly.
func ensureAncestors(ctx context.Context, q execer, syncRootID uuid.UUID, path string) error {
	dirs := dirAncestors(path)
	if len(dirs) == 0 {
		return nil
	}
	_, err := q.Exec(ctx, `
		INSERT INTO files (user_id, sync_root_id, path, size, mtime, is_dir, is_deleted)
		SELECT sr.user_id, sr.id, d.p, 0, NOW(), TRUE, FALSE
		FROM sync_roots sr, unnest($2::text[]) AS d(p)
		WHERE sr.id = $1
		ON CONFLICT (sync_root_id, path) WHERE is_deleted = FALSE DO NOTHING
	`, syncRootID, dirs)
	return err
}
