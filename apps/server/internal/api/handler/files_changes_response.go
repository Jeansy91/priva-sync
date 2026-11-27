package handler

import (
	"encoding/hex"
	"net/http"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/model"
)

// changeEntry / moveEntry / changesResponse are the wire shape shared by
// FileHandler.GetChanges (own sync root) and ShareHandler.MountChanges
// (ADR-0011) -- the desktop's decoder does not distinguish the two feeds.
type changeEntry struct {
	ID            uuid.UUID `json:"id"`
	Path          string    `json:"path"`
	Size          int64     `json:"size"`
	MTime         time.Time `json:"mtime"`
	ContentHash   string    `json:"content_hash"`
	IsDeleted     bool      `json:"is_deleted"`
	IsDir         bool      `json:"is_dir,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
	ServerVersion int64     `json:"server_version"`
	Seq           int64     `json:"seq"`
}

// moveEntry is a single ADR-0065 prefix-move event standing in for an
// entire directory-move subtree: the client recomputes the prefix change on
// its own rows instead of seeing one entry per descendant.
type moveEntry struct {
	OldPrefix string `json:"old_prefix"`
	NewPrefix string `json:"new_prefix"`
	Seq       int64  `json:"seq"`
}

type changesResponse struct {
	Changes    []changeEntry `json:"changes"`
	Moves      []moveEntry   `json:"moves,omitempty"`
	NextCursor int64         `json:"next_cursor,omitempty"`
}

// buildChangesResponse merges a files page with a move_events page
// (ADR-0065), both already fetched with LIMIT limit+1 and ordered by seq
// ascending, into the wire response. relativize is nil for the own-root feed
// and share.Path-stripping for the Shared-Mount feed (ADR-0011) -- applied
// to every path-shaped field, moves included, so a recipient never learns
// the giver's absolute layout.
func buildChangesResponse(files []model.File, moves []model.MoveEvent, limit int, relativize func(string) string) changesResponse {
	if relativize == nil {
		relativize = func(p string) string { return p }
	}

	outFiles, outMoves, cursor, hasMore := mergeChangeFeed(files, moves, limit)

	resp := changesResponse{
		Changes: make([]changeEntry, 0, len(outFiles)),
		Moves:   make([]moveEntry, 0, len(outMoves)),
	}
	for _, f := range outFiles {
		resp.Changes = append(resp.Changes, changeEntry{
			ID:            f.ID,
			Path:          relativize(f.Path),
			Size:          f.Size,
			MTime:         f.MTime,
			ContentHash:   hex.EncodeToString(f.ContentHash),
			IsDeleted:     f.IsDeleted,
			IsDir:         f.IsDir,
			UpdatedAt:     f.UpdatedAt,
			ServerVersion: f.ServerVersion,
			Seq:           f.Seq,
		})
	}
	for _, m := range outMoves {
		resp.Moves = append(resp.Moves, moveEntry{
			OldPrefix: relativize(m.OldPrefix),
			NewPrefix: relativize(m.NewPrefix),
			Seq:       m.Seq,
		})
	}
	if hasMore {
		resp.NextCursor = cursor
	}
	return resp
}

// requireFeedVersion enforces the ADR-0065 protocol bump on both changes
// endpoints: a client that doesn't declare feed_version=2 would otherwise see
// a directory move as silently missing data (no seq bump on the affected
// rows any more, and the move_events row is invisible to it) instead of a
// loud, immediate error. Unconditional -- not just when a move is actually
// pending -- because the auto-update window (ADR-0060) in which any client
// could hit this is minutes, not worth a second query path to narrow it.
// Writes the error response itself; returns false when the caller must stop.
func requireFeedVersion(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Query().Get("feed_version") != "2" {
		writeError(w, http.StatusBadRequest, ErrFeedVersionRequired,
			"feed_version=2 is required (ADR-0065: the changes feed now merges in move_events)")
		return false
	}
	return true
}
