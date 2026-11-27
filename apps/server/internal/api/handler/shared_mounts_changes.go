package handler

import (
	"net/http"
	"strconv"
	"strings"
)

// GET /api/v1/shared-mounts/{shareID}/changes?since=<seq>&limit=<n>&feed_version=2
//
// The Shared-Mount counterpart to FileHandler.GetChanges (ADR-0011): the
// desktop sync engine pulls deltas for an activated mount exactly the way it
// does for an own sync root, but scoped to share.path and with every path
// relativized against it so the recipient never learns the giver's absolute
// layout.
//
// Same cursor and same envelope as the own-root feed: `since` is the seq of
// the last row the caller already has (0 or absent for a first sync), and the
// response carries next_cursor until the caller has drained the feed. It used
// to take an RFC3339 timestamp and return every matching row at once, which
// cost it both the clock-jump guarantee of ADR-0036 and any bound on
// the response size. Tombstones are included so deletions mirror
// locally; a first sync sees them too and ignores them, as the own-root path
// already does. feed_version=2 is required (ADR-0065), same as the own-root
// feed -- both are consumed by the same Rust sync engine.
func (h *ShareHandler) MountChanges(w http.ResponseWriter, r *http.Request) {
	share := h.resolveMount(w, r)
	if share == nil {
		return
	}

	if !requireFeedVersion(w, r) {
		return
	}

	var sinceSeq int64
	if s := r.URL.Query().Get("since"); s != "" {
		parsed, err := strconv.ParseInt(s, 10, 64)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, ErrInvalidSince, "invalid since parameter (expects a seq cursor)")
			return
		}
		sinceSeq = parsed
	}

	limit := changesLimitDefault
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > changesLimitMax {
		limit = changesLimitMax
	}

	// Overfetch by one on each source (ADR-0065) -- see FileHandler.GetChanges.
	files, err := h.files.ListChangedFilesSinceUnderPrefix(
		r.Context(), *share.SyncRootID, share.Path, sinceSeq, limit+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	moves, err := h.files.ListMoveEventsSinceUnderPrefix(
		r.Context(), *share.SyncRootID, share.Path, sinceSeq, limit+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	// Same wire shape as FileHandler.GetChanges so the desktop reuses its
	// decoder unchanged — only the paths are mount-relative.
	relativize := func(p string) string { return relativizeToMount(p, share.Path) }
	writeJSON(w, http.StatusOK, buildChangesResponse(files, moves, limit, relativize))
}
// relativizeToMount strips the mount's giver-side base path so the client
// only ever sees mount-relative paths. The share-root row itself maps to
// "/"; everything below keeps a leading slash. Mirrors the relativization
// in ListMountFiles.
func relativizeToMount(abs, base string) string {
	rel := strings.TrimPrefix(abs, base)
	if rel == "" {
		return "/"
	}
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	return rel
}
