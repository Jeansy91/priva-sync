package handler

import (
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/repository"
	sseevents "privasync/server/internal/sse/events"
	syncpkg "privasync/server/internal/sync"
)

// Trash-bucket endpoints. Soft-deletes live with the rest of the
// mutation code (files_mutate.go) — this file owns the post-delete
// lifecycle: list, restore, single-purge, empty-all.

// GET /api/v1/sync-roots/{rootID}/trash
func (h *FileHandler) ListTrash(w http.ResponseWriter, r *http.Request) {
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

	files, err := h.trash.ListTrash(r.Context(), rootID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	type trashEntry struct {
		ID          uuid.UUID  `json:"id"`
		Path        string     `json:"path"`
		Size        int64      `json:"size"`
		ContentHash string     `json:"content_hash,omitempty"`
		DeletedAt   *time.Time `json:"deleted_at"`
	}
	resp := make([]trashEntry, 0, len(files))
	for _, f := range files {
		resp = append(resp, trashEntry{
			ID:          f.ID,
			Path:        f.Path,
			Size:        f.Size,
			ContentHash: hex.EncodeToString(f.ContentHash),
			DeletedAt:   f.DeletedAt,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// POST /api/v1/sync-roots/{rootID}/trash/{fileID}/restore
func (h *FileHandler) RestoreTrash(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}
	fileID, err := uuid.Parse(chi.URLParam(r, "fileID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidFileID, "invalid file id")
		return
	}

	res, err := h.coordinator.RestoreFromTrash(r.Context(), syncpkg.RestoreCmd{
		UserID: claims.UserID, ActorUserID: claims.UserID, SyncRootID: rootID, FileID: fileID,
	})
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrTrashedNotFound, "trashed file not found")
		return
	}
	if err != nil {
		slog.Error("restore: coordinator failed", "file_id", fileID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	restored := res.File
	resp := map[string]any{
		"id":           restored.ID,
		"path":         restored.Path,
		"size":         restored.Size,
		"content_hash": hex.EncodeToString(restored.ContentHash),
		"updated_at":   restored.UpdatedAt,
	}
	if res.Conflict == syncpkg.ConflictReal {
		resp["conflict_path"] = res.ConflictPath
	}
	writeJSON(w, http.StatusOK, resp)
}

// DELETE /api/v1/sync-roots/{rootID}/trash/{fileID}
//
// Permanently purges a single trashed file — the user-driven counterpart to
// the 30-day retention sweep in cmd/server. Frees the trashed bytes from the
// user's quota immediately so they can upload again without waiting.
func (h *FileHandler) PurgeTrash(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}
	fileID, err := uuid.Parse(chi.URLParam(r, "fileID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidFileID, "invalid file id")
		return
	}

	root, err := h.syncRoots.GetSyncRoot(r.Context(), rootID, claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
		return
	}

	trashed, err := h.trash.FindTrashedByID(r.Context(), fileID, claims.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrTrashedNotFound, "trashed file not found")
		return
	}
	if err != nil {
		slog.Error("purge: find trashed failed", "file_id", fileID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	// Best-effort disk delete — a missing physical file (legacy row, manual
	// cleanup, FS race with the retention sweep) must not block the DB purge,
	// otherwise the trash list would carry zombie rows the user cannot remove.
	if trashed.TrashPath != nil && *trashed.TrashPath != "" {
		if err := h.storage.DeleteTrash(claims.UserID, root.Path, *trashed.TrashPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("purge: storage delete failed", "file_id", fileID, "trash_path", *trashed.TrashPath, "error", err)
		}
	}

	if err := h.trash.PurgeTrashRecord(r.Context(), fileID); err != nil {
		slog.Error("purge: record delete failed", "file_id", fileID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	// Recount instead of delta-shift: legacy trash rows from before the
	// trash_bytes counter existed never incremented it, so a delta path
	// cannot heal that drift. Recount derived from the `files` table is
	// authoritative and the bus subscriber re-evaluates the threshold band.
	if err := h.quota.RecomputeViaBus(r.Context(), claims.UserID); err != nil {
		slog.Warn("purge: recompute quota failed", "user_id", claims.UserID, "error", err)
	}
	syncpkg.PublishEphemeral(h.hub, claims.UserID, sseevents.QuotaUpdated{})
	syncpkg.PublishEphemeral(h.hub, claims.UserID, sseevents.TrashPurged{RootID: rootID, FileID: fileID})

	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/v1/trash
//
// Permanently empties the user's entire trash: every is_deleted row is
// purged in one TX, the physical blobs leave storage crash-safe through
// the outbox pipeline.
func (h *FileHandler) EmptyTrash(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if err := h.coordinator.EmptyTrash(r.Context(), syncpkg.EmptyTrashCmd{
		UserID: claims.UserID,
	}); err != nil {
		slog.Error("empty trash failed", "user_id", claims.UserID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
