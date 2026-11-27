package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"privasync/server/internal/repository"
)

// POST /api/v1/shared-mounts/{shareID}/directories
//
// The mount counterpart to FileHandler.CreateDirectory (ADR-0011): lets a
// recipient create an explicit empty folder inside a writable mount so empty
// directories sync to the desktop symmetrically with own roots. Body:
// {"path": "/foo", "mtime": "..."} where path is mount-relative.
func (h *ShareHandler) CreateMountDirectory(w http.ResponseWriter, r *http.Request) {
	share := h.resolveMount(w, r)
	if share == nil {
		return
	}
	if !requireWritableMount(w, share) {
		return
	}

	var body struct {
		Path  string    `json:"path"`
		MTime time.Time `json:"mtime"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}
	if body.Path == "" {
		writeError(w, http.StatusBadRequest, ErrPathRequired, "path required")
		return
	}

	// Join the mount-relative path against share.Path and defend against
	// '..'-escape, same boundary as the mount move handler.
	cleanPath, ok := resolveSubPath(w, share.Path, body.Path, ErrInvalidPath)
	if !ok {
		return
	}
	if body.MTime.IsZero() {
		body.MTime = time.Now()
	}

	dir, mkdirErr, err := repository.CreateDirectoryWithStorage(
		r.Context(), h.files, h.storage, share.UserID, *share.SyncRootID, cleanPath, body.MTime,
	)
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			writeError(w, http.StatusConflict, ErrDestinationOccupied, "path occupied by a file")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	if mkdirErr != nil {
		slog.Warn("mount empty-dir mkdir failed",
			"share_id", share.ID, "path", cleanPath, "error", mkdirErr)
	}
	// Relativize the response path back to the mount so the client never
	// sees the giver's absolute layout.
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":     dir.ID,
		"path":   relativizeToMount(dir.Path, share.Path),
		"is_dir": true,
		"size":   0,
	})
}
