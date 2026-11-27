package handler

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/audit"
	"privasync/server/internal/model"
	"privasync/server/internal/quota"
	"privasync/server/internal/repository"
	sseevents "privasync/server/internal/sse/events"
	syncpkg "privasync/server/internal/sync"
)

// File-level mutations on own sync roots: upload, download, create
// directory, soft-delete, move/rename. All routes go through the
// SyncCoordinator so quota gating, conflict detection and outbox-driven
// SSE/storage side-effects stay in one place.

// mutationDeps bundles the collaborators the shared mutation bodies need.
func (h *FileHandler) mutationDeps() mutationDeps {
	return mutationDeps{h.storage, h.coordinator, h.quota, h.maxUploadBytes}
}

// resolveOwnRoot validates the {rootID} param against the caller and returns
// the MutationContext for an own-root write, where owner and actor are the
// same user (ADR-0017). Writes the 400/404 response and returns ok=false on
// failure.
func (h *FileHandler) resolveOwnRoot(w http.ResponseWriter, r *http.Request) (MutationContext, bool) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return MutationContext{}, false
	}
	if _, err := h.syncRoots.GetSyncRoot(r.Context(), rootID, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
		return MutationContext{}, false
	}
	return MutationContext{Owner: claims.UserID, Actor: claims.UserID, RootID: rootID}, true
}

// PUT /api/v1/sync-roots/{rootID}/files
// File upload (full content, no delta). Thin adapter: resolves the own-root
// context, then hands the staging → quota → coordinator → render pipeline to
// the shared upload body.
func (h *FileHandler) UploadFile(w http.ResponseWriter, r *http.Request) {
	mc, ok := h.resolveOwnRoot(w, r)
	if !ok {
		return
	}
	uploadResolved(w, r, h.mutationDeps(), mc, "")
}

// POST /api/v1/sync-roots/{rootID}/directories
// Creates an explicit empty-directory entry so empty folders sync
// across clients. Body: {"path": "/foo/bar", "mtime": "..."}.
func (h *FileHandler) CreateDirectory(w http.ResponseWriter, r *http.Request) {
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
	cleanPath, err := sanitizePath(body.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidPath, "invalid path")
		return
	}
	if body.MTime.IsZero() {
		body.MTime = time.Now()
	}

	dir, mkdirErr, err := repository.CreateDirectoryWithStorage(
		r.Context(), h.files, h.storage, claims.UserID, rootID, cleanPath, body.MTime,
	)
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			writeError(w, http.StatusConflict, ErrDestinationOccupied, "path occupied by a file")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	// Mirror the directory on disk so recovery can detect explicit empty
	// folders.  Errors are logged but not surfaced — the DB row is the
	// source of truth and FS reflection is opportunistic.
	if mkdirErr != nil {
		slog.Warn("empty-dir mkdir failed",
			"user_id", claims.UserID, "root_id", rootID, "path", cleanPath, "error", mkdirErr)
	}

	syncpkg.PublishEphemeral(h.hub, claims.UserID, sseevents.FileAdded{
		RootID: rootID, Path: dir.Path, File: sseEventsFile(dir),
	})

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         dir.ID,
		"path":       dir.Path,
		"size":       dir.Size,
		"mtime":      dir.MTime,
		"is_deleted": dir.IsDeleted,
		"is_dir":     dir.IsDir,
		"updated_at": dir.UpdatedAt,
	})
}

// GET /api/v1/sync-roots/{rootID}/files/download?path=/foo/bar.txt
func (h *FileHandler) DownloadFile(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}

	root, err := h.syncRoots.GetSyncRoot(r.Context(), rootID, claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
		return
	}

	filePath := r.URL.Query().Get("path")
	cleanPath, err := sanitizePath(filePath)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidPath, "invalid path")
		return
	}

	dbFile, err := h.files.FindByPath(r.Context(), rootID, cleanPath)
	if !isLiveFile(dbFile, err) {
		writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
		return
	}

	w.Header().Set("Content-Disposition", contentDispositionAttachment(path.Base(cleanPath)))
	w.Header().Set("X-Content-Hash", hex.EncodeToString(dbFile.ContentHash))
	w.Header().Set("X-Server-Version", strconv.FormatInt(dbFile.ServerVersion, 10))

	serveFileContent(w, r, h.storage, claims.UserID, root.ID.String(), cleanPath, dbFile.MTime)
}

// DELETE /api/v1/sync-roots/{rootID}/files?path=/foo/bar.txt
func (h *FileHandler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	mc, ok := h.resolveOwnRoot(w, r)
	if !ok {
		return
	}

	cleanPath, err := sanitizePath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidPath, "invalid path")
		return
	}

	existing, err := h.files.FindByPath(r.Context(), mc.RootID, cleanPath)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
		return
	}

	if existing.IsDir {
		h.deleteDirectorySubtree(w, r, mc, cleanPath)
		return
	}

	h.deleteSingleFile(w, r, mc, cleanPath, existing)
}

// deleteSingleFile soft-deletes a single file by delegating to the
// SyncCoordinator. The audit log entry stays here since it's request-shaped
// (IP, user-agent) rather than coordinator concern.
func (h *FileHandler) deleteSingleFile(
	w http.ResponseWriter,
	r *http.Request,
	mc MutationContext,
	filePath string,
	existing *model.File,
) {
	ifMatch, ok := parseIfMatch(w, r)
	if !ok {
		return
	}

	res, err := h.coordinator.DeleteFile(r.Context(), syncpkg.DeleteCmd{
		UserID: mc.Owner, ActorUserID: mc.Actor, SyncRootID: mc.RootID, Path: filePath,
		IfMatch: ifMatch,
	})
	if res.Conflict == syncpkg.ConflictVersionMismatch {
		// Someone wrote a newer version than the one the client meant to
		// delete. Same shape as the upload conflict so the client can rebase
		// on it instead of guessing.
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":          "version conflict",
			"server_version": res.File.ServerVersion,
		})
		return
	}
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
			return
		}
		slog.Error("delete: coordinator failed", "path", filePath, "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &mc.Actor, audit.EventFileDeleted, map[string]any{
		"sync_root_id": mc.RootID,
		"path":         filePath,
		"size":         existing.Size,
	}))

	w.WriteHeader(http.StatusNoContent)
}

// deleteDirectorySubtree cascade-trashes every descendant of dirPath plus
// the dir row itself. Files have their bytes moved into trash storage; dir
// rows are soft-deleted (no physical bytes). Subtree-listings happen once
// up-front so an SSE storm reflects the same set the user sees in the UI.
func (h *FileHandler) deleteDirectorySubtree(
	w http.ResponseWriter,
	r *http.Request,
	mc MutationContext,
	dirPath string,
) {
	res, err := h.coordinator.DeleteSubtree(r.Context(), syncpkg.DeleteSubtreeCmd{
		UserID: mc.Owner, ActorUserID: mc.Actor, SyncRootID: mc.RootID, Path: dirPath,
	})
	if err != nil {
		slog.Error("delete dir: coordinator failed", "path", dirPath, "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &mc.Actor, audit.EventFileDeleted, map[string]any{
		"sync_root_id": mc.RootID,
		"path":         dirPath,
		"is_dir":       true,
		"descendants":  res.Count,
		"size":         res.Bytes,
	}))

	w.WriteHeader(http.StatusNoContent)
}

// PATCH /api/v1/sync-roots/{rootID}/files
// Body: {"op":"move","from":"/old/path.txt","to":"/new/path.txt"}
func (h *FileHandler) MoveFile(w http.ResponseWriter, r *http.Request) {
	mc, ok := h.resolveOwnRoot(w, r)
	if !ok {
		return
	}

	op, fromPath, toPath, ok := decodeMoveRequest(w, r, "")
	if !ok {
		return
	}
	if op != "move" && op != "copy" {
		writeError(w, http.StatusBadRequest, ErrUnsupportedOp, "unsupported op (only 'move' and 'copy' are supported)")
		return
	}

	if op == "copy" {
		h.copyFile(w, r, mc, fromPath, toPath)
		return
	}

	// If-Match per HTTP header (RFC 7232) — same shape as upload.
	ifMatch, ok := parseIfMatch(w, r)
	if !ok {
		return
	}

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
		"id":             result.File.ID,
		"path":           result.File.Path,
		"size":           result.File.Size,
		"content_hash":   hex.EncodeToString(result.File.ContentHash),
		"server_version": result.File.ServerVersion,
		"updated_at":     result.File.UpdatedAt,
	})
}

// copyFile handles the op=copy branch of PATCH /sync-roots/{rootID}/files.
// Files and folders share one path: the coordinator resolves which. A busy
// destination is a 409 (no conflict-copy is materialised for copies); going
// over quota is a 507 with no partial copy left behind.
func (h *FileHandler) copyFile(w http.ResponseWriter, r *http.Request, mc MutationContext, fromPath, toPath string) {
	result, err := h.coordinator.CopyFile(r.Context(), syncpkg.CopyCmd{
		UserID:      mc.Owner,
		ActorUserID: mc.Actor,
		SyncRootID:  mc.RootID,
		From:        fromPath,
		To:          toPath,
	})
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrSourceNotInDB, "source file not found in database")
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, ErrDestinationOccupied, "destination path already exists")
		return
	}
	if errors.Is(err, quota.ErrExceeded) {
		writeError(w, http.StatusInsufficientStorage, ErrQuotaExceeded, "storage quota exceeded")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":             result.File.ID,
		"path":           result.File.Path,
		"size":           result.File.Size,
		"content_hash":   hex.EncodeToString(result.File.ContentHash),
		"server_version": result.File.ServerVersion,
		"updated_at":     result.File.UpdatedAt,
	})
}
