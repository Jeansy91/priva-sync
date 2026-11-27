package handler

import (
	"errors"
	"net/http"
	"path"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
	sseevents "privasync/server/internal/sse/events"
	syncpkg "privasync/server/internal/sync"
)

// GET /api/v1/shared-mounts/{shareID}/conflicts
//
// Open conflicts whose underlying file lives inside the mount (ADR-0011). The
// conflict rows belong to the giver's root; we scope to share.path and
// relativize both the file and loser-copy paths so the recipient never sees
// the giver's absolute layout. Read access only needs a valid mount (any
// recipient can see conflicts); resolving is gated separately on can_write.
func (h *ShareHandler) MountConflicts(w http.ResponseWriter, r *http.Request) {
	share := h.resolveMount(w, r)
	if share == nil {
		return
	}
	conflicts, err := h.conflicts.ListOpenConflictsUnderPrefix(
		r.Context(), share.UserID, *share.SyncRootID, share.Path,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	type conflictResponse struct {
		ID           uuid.UUID `json:"id"`
		FileID       uuid.UUID `json:"file_id"`
		ConflictPath string    `json:"conflict_path"`
		CreatedAt    string    `json:"created_at"`
	}
	resp := make([]conflictResponse, 0, len(conflicts))
	for _, c := range conflicts {
		resp = append(resp, conflictResponse{
			ID:           c.ID,
			FileID:       c.FileID,
			ConflictPath: relativizeToMount(c.ConflictPath, share.Path),
			CreatedAt:    c.CreatedAt.Format("2006-01-02T15:04:05.000Z07:00"),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// findMountConflictDetail loads an open conflict and verifies it belongs to
// the given mount: same sync root + giver, file path under share.path. A
// conflict outside the mount is reported as not found so recipients cannot
// probe the giver's other directories. Writes the error response and returns
// nil on failure.
func (h *ShareHandler) findMountConflictDetail(w http.ResponseWriter, r *http.Request, share *model.Share) *repository.ConflictDetail {
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
	if d.SyncRootID != *share.SyncRootID ||
		d.OwnerID != share.UserID ||
		!isUnderShareRoot(d.FilePath, share.Path) {
		writeError(w, http.StatusNotFound, ErrConflictNotFound, "conflict not found")
		return nil
	}
	return d
}

// GET /api/v1/shared-mounts/{shareID}/conflicts/{conflictID}
//
// Both versions' metadata, with paths relativized to the mount root. Read
// access only needs a valid mount, like the conflict list.
func (h *ShareHandler) MountConflictVersions(w http.ResponseWriter, r *http.Request) {
	share := h.resolveMount(w, r)
	if share == nil {
		return
	}
	d := h.findMountConflictDetail(w, r, share)
	if d == nil {
		return
	}
	conflictSize, err := h.storage.FileSize(d.OwnerID, d.SyncRootID.String(), d.ConflictPath)
	if err != nil {
		conflictSize = 0
	}
	resp := newConflictVersionsResponse(d, conflictSize)
	resp.FilePath = relativizeToMount(d.FilePath, share.Path)
	resp.ConflictPath = relativizeToMount(d.ConflictPath, share.Path)
	writeJSON(w, http.StatusOK, resp)
}

// GET /api/v1/shared-mounts/{shareID}/conflicts/{conflictID}/file
//
// Serves the loser copy's bytes from the giver's storage, scoped by the
// conflict row (the loser copy has no files row for upload/move conflicts).
func (h *ShareHandler) MountConflictFile(w http.ResponseWriter, r *http.Request) {
	share := h.resolveMount(w, r)
	if share == nil {
		return
	}
	d := h.findMountConflictDetail(w, r, share)
	if d == nil {
		return
	}
	w.Header().Set("Content-Disposition", contentDispositionAttachment(path.Base(d.ConflictPath)))
	if err := h.storage.ServeFile(d.OwnerID, d.SyncRootID.String(), d.ConflictPath, w); err != nil {
		writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
	}
}

// POST /api/v1/shared-mounts/{shareID}/conflicts/{conflictID}/resolve
//
// A recipient with can_write resolves a conflict inside their mount. The
// conflict's file belongs to the giver, so the own-root ResolveConflict
// (gated on f.user_id = caller) cannot be used; instead we verify the
// recipient's permission here — mount valid, writable, and the conflict's
// file path under share.path — then resolve under the giver's user so quota
// and storage bookkeeping stay with the owner (ADR-0010 ownership model).
func (h *ShareHandler) ResolveMountConflict(w http.ResponseWriter, r *http.Request) {
	share := h.resolveMount(w, r)
	if share == nil {
		return
	}
	if !requireWritableMount(w, share) {
		return
	}
	strategy, ok := readResolveStrategy(w, r)
	if !ok {
		return
	}
	d := h.findMountConflictDetail(w, r, share)
	if d == nil {
		return
	}
	conflictID := d.ID

	// Resolve under the giver (storage owner), not the recipient — the loser
	// copy and quota live in the giver's root (ADR-0010).
	rc, err := h.coordinator.ResolveConflict(r.Context(), syncpkg.ResolveConflictCmd{
		UserID: share.UserID, ConflictID: conflictID, Strategy: strategy,
	})
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrConflictNotFound, "conflict not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	// Notify the giver's sessions (their root changed) and the recipient
	// (their mount view should refresh). Both reuse the existing event.
	syncpkg.PublishEphemeral(h.hub, share.UserID, sseevents.ConflictResolved{
		ConflictID: conflictID, FileID: rc.FileID, RootID: rc.SyncRootID, FilePath: rc.FilePath,
	})
	syncpkg.PublishEphemeral(h.hub, middleware.ClaimsFromContext(r.Context()).UserID, sseevents.ConflictResolved{
		ConflictID: conflictID, FileID: rc.FileID, RootID: rc.SyncRootID, FilePath: rc.FilePath,
	})

	w.WriteHeader(http.StatusNoContent)
}
