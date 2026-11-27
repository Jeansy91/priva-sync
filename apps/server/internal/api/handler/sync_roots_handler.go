package handler

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
	sseevents "privasync/server/internal/sse/events"
	syncpkg "privasync/server/internal/sync"
)

// HTTP handlers for the sync-root aggregate: listing, creation, deletion,
// and full-root ZIP download. File-level operations live elsewhere (see
// files.go and friends).

// GET /api/v1/sync-roots
func (h *FileHandler) ListSyncRoots(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	roots, err := h.syncRoots.ListSyncRootsWithOwner(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	type ownerResponse struct {
		ID        uuid.UUID `json:"id"`
		Username  string    `json:"username"`
		AvatarURL *string   `json:"avatar_url"`
	}
	type syncRootResponse struct {
		ID    uuid.UUID     `json:"id"`
		Name  string        `json:"name"`
		Path  string        `json:"path"`
		Owner ownerResponse `json:"owner"`
	}
	resp := make([]syncRootResponse, 0, len(roots))
	for _, r := range roots {
		var avatarURL *string
		if r.OwnerAvatarPath != nil {
			s := "/api/v1/auth/avatar/" + r.UserID.String()
			avatarURL = &s
		}
		resp = append(resp, syncRootResponse{
			ID: r.ID, Name: r.Name, Path: r.Path,
			Owner: ownerResponse{
				ID: r.UserID, Username: r.OwnerUsername, AvatarURL: avatarURL,
			},
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// POST /api/v1/sync-roots
func (h *FileHandler) CreateSyncRoot(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, ErrNameRequired, "name required")
		return
	}

	safePath := sanitizeName(req.Name)
	root, err := h.syncRoots.CreateSyncRoot(r.Context(), claims.UserID, req.Name, safePath)
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, ErrSyncRootExists, "sync root already exists")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	if err := h.storage.CreateRootDir(claims.UserID, root.ID.String()); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	type syncRootResponse struct {
		ID        uuid.UUID `json:"id"`
		Name      string    `json:"name"`
		Path      string    `json:"path"`
		CreatedAt time.Time `json:"created_at"`
	}
	syncpkg.PublishEphemeral(h.hub, claims.UserID, sseevents.SyncRootAdded{
		Root: sseevents.SyncRootRef{ID: root.ID, Name: root.Name, Path: root.Path, CreatedAt: root.CreatedAt},
	})
	writeJSON(w, http.StatusCreated, syncRootResponse{
		ID: root.ID, Name: root.Name, Path: root.Path, CreatedAt: root.CreatedAt,
	})
}

// PATCH /api/v1/sync-roots/{rootID}
// Body: {"name":"New name"}. Renames the sync root's display name. No bytes
// move — the storage dir is keyed by id, not name (see UpdateSyncRootName).
func (h *FileHandler) RenameSyncRoot(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, ErrNameRequired, "name required")
		return
	}

	root, err := h.syncRoots.UpdateSyncRootName(r.Context(), rootID, claims.UserID, strings.TrimSpace(req.Name))
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, ErrSyncRootExists, "sync root already exists")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	syncpkg.PublishEphemeral(h.hub, claims.UserID, sseevents.SyncRootRenamed{RootID: root.ID, Name: root.Name})

	type syncRootResponse struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
		Path string    `json:"path"`
	}
	writeJSON(w, http.StatusOK, syncRootResponse{ID: root.ID, Name: root.Name, Path: root.Path})
}

// DELETE /api/v1/sync-roots/{rootID}
// Deletes the sync root with all its file rows, conflicts, shares and stored
// bytes. Destructive -- the client must confirm beforehand.
func (h *FileHandler) DeleteSyncRoot(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}

	result, err := h.coordinator.DeleteSyncRoot(r.Context(), syncpkg.DeleteSyncRootCmd{
		UserID:     claims.UserID,
		SyncRootID: rootID,
	})
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	rootDir := h.storage.RootDir(claims.UserID, rootID.String())
	//#nosec G703 -- rootDir is composed from the caller's uuid and a parsed uuid.UUID (rootID); neither can carry path separators
	if err := os.RemoveAll(rootDir); err != nil {
		slog.Warn("delete sync root: storage cleanup failed",
			"user_id", claims.UserID, "root_id", rootID, "error", err)
	}

	syncpkg.PublishEphemeral(h.hub, claims.UserID, sseevents.QuotaUpdated{})
	syncpkg.PublishEphemeral(h.hub, claims.UserID, sseevents.SyncRootDeleted{RootID: rootID})

	// Notify mount recipients so their sidebar doesn't point at a dead
	// share row. See ADR-0010.
	for _, s := range result.RevokedShares {
		if s.TargetUserID == nil {
			continue
		}
		syncpkg.PublishEphemeral(h.hub, *s.TargetUserID, sseevents.ShareRevoked{ShareID: s.ID})
	}

	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/sync-roots/{rootID}/download
// Streams all non-deleted files of the sync root as a ZIP. Mostly used by
// programmatic clients and small roots — browsers use the token path
// (/download/{token}) so the native save dialog starts without buffering.
func (h *FileHandler) DownloadSyncRoot(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}
	h.streamSyncRootZip(w, r, claims.UserID, rootID, "")
}

// streamSyncRootZip serialises a sync root (or a subtree thereof) as a ZIP
// stream into the response. subtreePath "" or "/" means the whole root; any
// other value scopes the ZIP to that directory and rewrites entry names so
// they are relative to the subtree root. The caller has already performed
// authorisation.
func (h *FileHandler) streamSyncRootZip(
	w http.ResponseWriter,
	r *http.Request,
	userID uuid.UUID,
	rootID uuid.UUID,
	subtreePath string,
) {
	root, err := h.syncRoots.GetSyncRoot(r.Context(), rootID, userID)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
		return
	}
	// Archive-relative naming needs one spelling of the root; the repository
	// accepts both.
	queryPath := strings.TrimSuffix(subtreePath, "/")

	// Unlike the share ZIP this always answers 200: the caller is the owner
	// and an empty root is a legitimate empty archive, so the headers go out
	// before the walk.
	name := root.Name
	if queryPath != "" {
		name = path.Base(queryPath)
	}
	zw, flusher := startZipResponse(w, name)
	defer zw.Close()

	// Written as the subtree is walked, so the archive costs one page of rows
	// rather than the whole tree. The headers are already out, so a
	// mid-stream failure can only be logged and the stream cut.
	err = h.files.EachInSubtree(r.Context(), rootID, subtreePath, func(f model.File) error {
		var entryName string
		if queryPath == "" {
			entryName = strings.TrimPrefix(f.Path, "/")
		} else {
			// Skip the subtree's own dir row — it has no payload and would
			// produce an empty entry name.
			if f.Path == queryPath {
				return nil
			}
			entryName = strings.TrimPrefix(f.Path, queryPath+"/")
		}
		if entryName == "" || f.IsDir {
			return nil
		}
		entry, err := zw.CreateHeader(&zip.FileHeader{
			Name:     entryName,
			Method:   zip.Deflate,
			Modified: f.MTime,
		})
		if err != nil {
			return fmt.Errorf("zip header for %s: %w", f.Path, err)
		}
		if err := h.storage.ServeFile(userID, rootID.String(), f.Path, entry); err != nil {
			return fmt.Errorf("serving %s: %w", f.Path, err)
		}
		// Push the in-flight buffer after each file so the browser can
		// stream into the save dialog rather than wait for the whole ZIP.
		if flusher != nil {
			_ = zw.Flush()
			flusher.Flush()
		}
		return nil
	})
	if err != nil {
		slog.Warn("zip: stream aborted mid-archive", "root_id", rootID, "error", err)
	}
}
