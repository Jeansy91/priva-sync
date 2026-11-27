package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"privasync/server/internal/model"
)

func (r *FileRepository) FindByPath(ctx context.Context, syncRootID uuid.UUID, path string) (*model.File, error) {
	var f model.File
	err := scanFile(r.db.QueryRow(ctx, `
		SELECT `+fileColumns+`
		FROM files WHERE sync_root_id = $1 AND path = $2 AND is_deleted = FALSE
	`, syncRootID, path), &f)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &f, err
}

// FindByPaths looks up a batch of paths in one round trip and returns
// whatever rows exist -- live or soft-deleted alike. Unlike FindByPath it
// does not filter is_deleted=FALSE and does not report ErrNotFound: a
// missing path is simply absent from the result, so the caller can tell
// "never existed" (absent) apart from "existed and was deleted"
// (IsDeleted=true), which FindByPath's single ErrNotFound cannot express.
// Backs the ADR-0062 stat endpoint: a retry that only re-fetches state
// before applying it must never mistake "never existed" for "still there".
func (r *FileRepository) FindByPaths(ctx context.Context, syncRootID uuid.UUID, paths []string) ([]model.File, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT `+fileColumns+`
		FROM files WHERE sync_root_id = $1 AND path = ANY($2)
	`, syncRootID, paths)
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

// HasChildren reports whether any live row sits below dirPath (i.e. under
// `dirPath/`). Lets the move pipeline treat an *implicit* directory — one the
// UI infers from child paths without an own row — as a renameable folder.
func (r *FileRepository) HasChildren(ctx context.Context, syncRootID uuid.UUID, dirPath string) (bool, error) {
	_, lo, hi := subtreeRange(dirPath)
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM files
			WHERE sync_root_id = $1 AND is_deleted = FALSE
			  AND path ~>=~ $2 AND path ~<~ $3
		)`, syncRootID, lo, hi).Scan(&exists)
	return exists, err
}

// GetSyncRootByID looks up a sync root by its UUID (without user scoping).
// Used internally by background jobs that already trust the file records.
func (r *FileRepository) GetSyncRootByID(ctx context.Context, id uuid.UUID) (*model.SyncRoot, error) {
	var sr model.SyncRoot
	err := r.db.QueryRow(ctx,
		`SELECT id, user_id, name, path, created_at FROM sync_roots WHERE id = $1`, id,
	).Scan(&sr.ID, &sr.UserID, &sr.Name, &sr.Path, &sr.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &sr, err
}

// GetChangedSince returns up to limit files with seq > sinceSeq, ordered by
// seq ascending -- the paginated changes-feed cursor for desktop sync
// (ADR-0036). seq is a monotonic per-row sequence, not a wall-clock
// timestamp: a backwards clock correction cannot hide a change from a
// client cursor that has already advanced past it. Callers page through by
// re-calling with the seq of the last row received.
func (r *FileRepository) GetChangedSince(ctx context.Context, syncRootID uuid.UUID, sinceSeq int64, limit int) ([]model.File, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+fileColumns+`
		FROM files
		WHERE sync_root_id = $1 AND seq > $2
		ORDER BY seq ASC
		LIMIT $3
	`, syncRootID, sinceSeq, limit)
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

// ListMoveEventsSince returns up to limit move_events rows with seq >
// sinceSeq, ordered by seq ascending -- the second half of the ADR-0065
// changes-feed merge alongside GetChangedSince. A directory move's affected
// files rows never bump seq (see MoveDirectory), so this is the only place
// that move surfaces in the feed.
func (r *FileRepository) ListMoveEventsSince(ctx context.Context, syncRootID uuid.UUID, sinceSeq int64, limit int) ([]model.MoveEvent, error) {
	rows, err := r.db.Query(ctx, `
		SELECT sync_root_id, old_prefix, new_prefix, seq
		FROM move_events
		WHERE sync_root_id = $1 AND seq > $2
		ORDER BY seq ASC
		LIMIT $3
	`, syncRootID, sinceSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []model.MoveEvent
	for rows.Next() {
		var e model.MoveEvent
		if err := rows.Scan(&e.SyncRootID, &e.OldPrefix, &e.NewPrefix, &e.Seq); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// ListChangedFilesSinceUnderPrefix is GetChangedSince scoped to a subtree.
// Powers the Shared-Mount delta feed (ADR-0011): a recipient sees only
// changes inside share.path, never the giver's siblings. Tombstones are
// included so the desktop mirrors deletions inside the mount.
//
// Keyset-paginated on the same monotonic seq the own-root feed uses
// (ADR-0036). It ran on updated_at until the seq cursor replaced it, which meant a backwards clock
// correction could give a new change a smaller timestamp than one already
// delivered and hide it from a cursor that had moved past -- silent
// divergence rather than a visible error.
func (r *FileRepository) ListChangedFilesSinceUnderPrefix(
	ctx context.Context,
	syncRootID uuid.UUID,
	dirPath string,
	sinceSeq int64,
	limit int,
) ([]model.File, error) {
	self, lo, hi := subtreeRange(dirPath)
	rows, err := r.db.Query(ctx, `
		SELECT `+fileColumns+`
		FROM files
		WHERE sync_root_id = $1
		  AND seq > $2
		  AND (path = $3 OR (path ~>=~ $4 AND path ~<~ $5))
		ORDER BY seq ASC
		LIMIT $6
	`, syncRootID, sinceSeq, self, lo, hi, limit)
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

// ListMoveEventsSinceUnderPrefix is ListMoveEventsSince scoped to a subtree,
// the Shared-Mount counterpart for the ADR-0065 move_events feed. Scoped on
// old_prefix: a move whose source lived inside the mount is the recipient's
// to know about, mirroring how the giver's own client would see it. A move
// crossing the mount boundary (source outside/destination inside, or the
// reverse) is not covered -- narrower than the surrounding files feed, which
// already tracks entries appearing/disappearing at the boundary through the
// ordinary insert/delete path. Out of scope for ADR-0065.
func (r *FileRepository) ListMoveEventsSinceUnderPrefix(
	ctx context.Context,
	syncRootID uuid.UUID,
	dirPath string,
	sinceSeq int64,
	limit int,
) ([]model.MoveEvent, error) {
	self, lo, hi := subtreeRange(dirPath)
	rows, err := r.db.Query(ctx, `
		SELECT sync_root_id, old_prefix, new_prefix, seq
		FROM move_events
		WHERE sync_root_id = $1
		  AND seq > $2
		  AND (old_prefix = $3 OR (old_prefix ~>=~ $4 AND old_prefix ~<~ $5))
		ORDER BY seq ASC
		LIMIT $6
	`, syncRootID, sinceSeq, self, lo, hi, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []model.MoveEvent
	for rows.Next() {
		var e model.MoveEvent
		if err := rows.Scan(&e.SyncRootID, &e.OldPrefix, &e.NewPrefix, &e.Seq); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

