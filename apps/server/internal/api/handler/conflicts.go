package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
	sseevents "privasync/server/internal/sse/events"
	syncpkg "privasync/server/internal/sync"
)

// Conflict endpoints. The conflict aggregate is owned by the coordinator
// (see ADR-0003); these handlers are thin REST/SSE adapters.

// GET /api/v1/sync-roots/{rootID}/conflicts
func (h *FileHandler) ListConflicts(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}
	if _, err := h.syncRoots.GetSyncRoot(r.Context(), rootID, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
		return
	}
	conflicts, err := h.conflicts.ListOpenConflicts(r.Context(), claims.UserID, rootID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	if conflicts == nil {
		conflicts = []model.Conflict{}
	}
	writeJSON(w, http.StatusOK, conflicts)
}

// conflictVersionsResponse is the wire shape of the conflict detail view:
// both competing versions side by side so a client can offer an informed
// resolve choice.
type conflictVersionsResponse struct {
	ID           uuid.UUID `json:"id"`
	FileID       uuid.UUID `json:"file_id"`
	FilePath     string    `json:"file_path"`
	ConflictPath string    `json:"conflict_path"`
	CreatedAt    time.Time `json:"created_at"`
	Current      struct {
		Size  int64     `json:"size"`
		MTime time.Time `json:"mtime"`
	} `json:"current"`
	Conflict struct {
		Size int64 `json:"size"`
		// MTime is the loser copy's own modification time; null for conflicts
		// recorded before it was stored.
		MTime     *time.Time `json:"mtime"`
		CreatedAt time.Time  `json:"created_at"`
	} `json:"conflict"`
}

func newConflictVersionsResponse(d *repository.ConflictDetail, conflictSize int64) conflictVersionsResponse {
	resp := conflictVersionsResponse{
		ID:           d.ID,
		FileID:       d.FileID,
		FilePath:     d.FilePath,
		ConflictPath: d.ConflictPath,
		CreatedAt:    d.CreatedAt,
	}
	resp.Current.Size = d.FileSize
	resp.Current.MTime = d.FileMTime
	resp.Conflict.Size = conflictSize
	resp.Conflict.MTime = d.LoserMTime
	resp.Conflict.CreatedAt = d.CreatedAt
	return resp
}

// findOwnConflictDetail loads an open conflict and enforces ownership: only
// the file's owner may see it through the own-root endpoints. Writes the
// error response and returns nil otherwise.
func (h *FileHandler) findOwnConflictDetail(w http.ResponseWriter, r *http.Request) *repository.ConflictDetail {
	claims := middleware.ClaimsFromContext(r.Context())
	conflictID, err := uuid.Parse(chi.URLParam(r, "conflictID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidConflict, "invalid conflict id")
		return nil
	}
	d, err := h.conflicts.FindOpenConflictDetail(r.Context(), conflictID)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrConflictNotFound, "conflict not found")
		return nil
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return nil
	}
	if d.OwnerID != claims.UserID {
		// Do not disclose other users' conflicts — treat as not found.
		writeError(w, http.StatusNotFound, ErrConflictNotFound, "conflict not found")
		return nil
	}
	return d
}

// GET /api/v1/conflicts/{conflictID}
func (h *FileHandler) GetConflict(w http.ResponseWriter, r *http.Request) {
	d := h.findOwnConflictDetail(w, r)
	if d == nil {
		return
	}
	conflictSize, err := h.storage.FileSize(d.OwnerID, d.SyncRootID.String(), d.ConflictPath)
	if err != nil {
		conflictSize = 0
	}
	writeJSON(w, http.StatusOK, newConflictVersionsResponse(d, conflictSize))
}

// GET /api/v1/conflicts/{conflictID}/file
//
// Serves the loser copy's bytes. Upload and move conflicts have no files row
// at the conflict path, so the path-based download endpoint cannot serve
// them — this one reads storage directly, scoped by the conflict row.
func (h *FileHandler) GetConflictFile(w http.ResponseWriter, r *http.Request) {
	d := h.findOwnConflictDetail(w, r)
	if d == nil {
		return
	}
	w.Header().Set("Content-Disposition", contentDispositionAttachment(path.Base(d.ConflictPath)))
	if err := h.storage.ServeFile(d.OwnerID, d.SyncRootID.String(), d.ConflictPath, w); err != nil {
		writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
	}
}

// readResolveStrategy parses the optional request body of a resolve call.
// An empty body keeps backward compatibility with pre-strategy clients
// (keep_current). Returns false after writing the error response.
func readResolveStrategy(w http.ResponseWriter, r *http.Request) (syncpkg.ResolveStrategy, bool) {
	var req struct {
		Strategy string `json:"strategy"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid body")
		return "", false
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid body")
			return "", false
		}
	}
	strategy, ok := syncpkg.ParseResolveStrategy(req.Strategy)
	if !ok {
		writeError(w, http.StatusBadRequest, ErrInvalidStrategy, "invalid resolve strategy")
		return "", false
	}
	return strategy, true
}

// POST /api/v1/conflicts/{conflictID}/resolve
func (h *FileHandler) ResolveConflict(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	conflictID, err := uuid.Parse(chi.URLParam(r, "conflictID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidConflict, "invalid conflict id")
		return
	}
	strategy, ok := readResolveStrategy(w, r)
	if !ok {
		return
	}

	rc, err := h.coordinator.ResolveConflict(r.Context(), syncpkg.ResolveConflictCmd{
		UserID: claims.UserID, ConflictID: conflictID, Strategy: strategy,
	})
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrConflictNotFound, "conflict not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	// Push to every session of the user: web reloads the list, desktop
	// triggers a sync cycle so the canonical state lands on disk
	// immediately.
	syncpkg.PublishEphemeral(h.hub, claims.UserID, sseevents.ConflictResolved{
		ConflictID: conflictID,
		FileID:     rc.FileID,
		RootID:     rc.SyncRootID,
		FilePath:   rc.FilePath,
	})

	w.WriteHeader(http.StatusNoContent)
}
