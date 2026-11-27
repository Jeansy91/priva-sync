package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"privasync/server/internal/repository"
	syncpkg "privasync/server/internal/sync"
)

// POST /api/v1/shared-mounts/{shareID}/files?path=<rel>
//
// Streams the body to staging, then commits via the coordinator under
// the Sync-Root-owner's user so quota and trash stay with the owner.
func (h *ShareHandler) UploadMountFile(w http.ResponseWriter, r *http.Request) {
	mc, basePath, ok := h.resolveMountMutation(w, r)
	if !ok {
		return
	}
	uploadResolved(w, r, h.mutationDeps(), mc, basePath)
}

// PATCH /api/v1/shared-mounts/{shareID}/files
//
// Move/rename inside the mount. Both `from` and `to` must resolve under
// share.path — otherwise an attacker could rename a mount file into
// adjacent giver directories.
func (h *ShareHandler) MoveMountFile(w http.ResponseWriter, r *http.Request) {
	mc, basePath, ok := h.resolveMountMutation(w, r)
	if !ok {
		return
	}
	// Both from and to are joined and escape-checked against share.Path so a
	// rename can't reach adjacent giver directories.
	op, fromPath, toPath, ok := decodeMoveRequest(w, r, basePath)
	if !ok {
		return
	}
	if op != "move" {
		writeError(w, http.StatusBadRequest, ErrUnsupportedOp, "unsupported op (only 'move' is supported)")
		return
	}
	// If-Match (RFC 7232) — same shape as the own-root move handler so
	// move-move conflicts surface as 409 instead of a silent loser copy.
	ifMatch, ok := parseIfMatch(w, r)
	if !ok {
		return
	}
	// mc.Actor is the recipient, not the giver — last_modifier_user_id records
	// who actually wrote even though the file lives in the giver's root (ADR-0013).
	result, err := h.coordinator.MoveFile(r.Context(), syncpkg.MoveCmd{
		UserID:      mc.Owner,
		ActorUserID: mc.Actor,
		SyncRootID:  mc.RootID,
		From:        fromPath,
		To:          toPath,
		IfMatch:     ifMatch,
	})
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrSourceNotInDB, "source file not found in database")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	writeMoveResult(w, result, map[string]any{
		"id":   result.File.ID,
		"path": result.File.Path,
	})
}

// DELETE /api/v1/shared-mounts/{shareID}/files?path=<rel>
//
// Routes the soft-delete through the coordinator under the owner's user
// so the file lands in the owner's trash.
func (h *ShareHandler) DeleteMountFile(w http.ResponseWriter, r *http.Request) {
	mc, basePath, ok := h.resolveMountMutation(w, r)
	if !ok {
		return
	}
	cleanPath, ok := resolveMutationPath(w, r, basePath)
	if !ok {
		return
	}
	// mc.Actor is the recipient — last_modifier_user_id records the actual
	// writer even though the file lives in the giver's root. See ADR-0013.
	if _, err := h.coordinator.DeleteFile(r.Context(), syncpkg.DeleteCmd{
		UserID:      mc.Owner,
		ActorUserID: mc.Actor,
		SyncRootID:  mc.RootID,
		Path:        cleanPath,
	}); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
			return
		}
		slog.Error("mount delete: coordinator failed", "root_id", mc.RootID, "path", cleanPath, "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
