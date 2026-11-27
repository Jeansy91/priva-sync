package handler

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/model"
)

// GET /api/v1/shared-mounts/{shareID}/stats
//
// Lazy stats endpoint for the recipient-side detail panel. Dispatches
// on share.is_file:
//
//   - Directory mount → subtree aggregates (item count, total size,
//     latest mtime), mirroring /sync-roots/{id}/files/stats?path=…
//   - File mount → metadata of the underlying file (id, size, mtime,
//     content_hash, updated_at, last_modifier).
//
// See ADR-0012.
func (h *ShareHandler) MountStats(w http.ResponseWriter, r *http.Request) {
	share := h.resolveMount(w, r)
	if share == nil {
		return
	}
	if isFileShare(r.Context(), h.files, share) {
		dbFile, err := h.files.FindByPath(r.Context(), *share.SyncRootID, share.Path)
		if err != nil || dbFile.IsDeleted {
			writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
			return
		}
		var modifierIDs []uuid.UUID
		if dbFile.LastModifierUserID != nil {
			modifierIDs = append(modifierIDs, *dbFile.LastModifierUserID)
		}
		modifiers, err := resolveLastModifiers(r.Context(), h.users, modifierIDs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Kind         string                 `json:"kind"`
			ID           uuid.UUID              `json:"id"`
			Size         int64                  `json:"size"`
			MTime        time.Time              `json:"mtime"`
			ContentHash  string                 `json:"content_hash"`
			UpdatedAt    time.Time              `json:"updated_at"`
			LastModifier *userSummaryWithAvatar `json:"last_modifier,omitempty"`
		}{
			Kind:         "file",
			ID:           dbFile.ID,
			Size:         dbFile.Size,
			MTime:        dbFile.MTime,
			ContentHash:  hex.EncodeToString(dbFile.ContentHash),
			UpdatedAt:    dbFile.UpdatedAt,
			LastModifier: modifiers.lookupOptional(dbFile.LastModifierUserID),
		})
		return
	}
	stats, err := h.files.DirectoryStats(r.Context(), *share.SyncRootID, share.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Kind        string     `json:"kind"`
		ItemCount   int        `json:"item_count"`
		FileCount   int        `json:"file_count"`
		TotalSize   int64      `json:"total_size"`
		LatestMTime *time.Time `json:"latest_mtime,omitempty"`
	}{
		Kind:        "dir",
		ItemCount:   stats.ItemCount,
		FileCount:   stats.FileCount,
		TotalSize:   stats.TotalSize,
		LatestMTime: stats.LatestMTime,
	})
}

// POST /api/v1/shared-mounts/{shareID}/files/stat
//
// The Shared-Mount twin of FileHandler.FilesStat (ADR-0062): same batched
// fresh-state lookup for the desktop retry sweep, scoped to share.Path and
// relativized like every other mount response so the recipient never learns
// the giver's absolute layout. A path that escapes the share root rejects
// the whole request with 403 -- same all-or-nothing boundary mountSubPath
// enforces for a single path; a batch caller (the reference retry sweep)
// only ever sends paths it received from this mount's own change feed, so a
// stricter answer here can't be a legitimate request.
func (h *ShareHandler) MountFilesStat(w http.ResponseWriter, r *http.Request) {
	share := h.resolveMount(w, r)
	if share == nil {
		return
	}

	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}
	if len(body.Paths) > filesStatMaxPaths {
		writeError(w, http.StatusBadRequest, ErrTooManyPaths,
			fmt.Sprintf("at most %d paths per request", filesStatMaxPaths))
		return
	}

	absolute := make([]string, len(body.Paths))
	for i, p := range body.Paths {
		if p == "" || p == "/" {
			absolute[i] = share.Path
			continue
		}
		joined, ok := joinUnderShareRoot(share.Path, p)
		if !ok {
			writeError(w, http.StatusForbidden, ErrInvalidPath, "sub path escapes share")
			return
		}
		absolute[i] = joined
	}

	found, err := h.files.FindByPaths(r.Context(), *share.SyncRootID, absolute)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	// Relativize before diffing against the (already mount-relative) request
	// so a found row lines up with the path the caller asked about.
	relFound := make([]model.File, len(found))
	for i, f := range found {
		f.Path = relativizeToMount(f.Path, share.Path)
		relFound[i] = f
	}

	writeJSON(w, http.StatusOK, struct {
		Results []fileStatEntry `json:"results"`
	}{Results: statEntriesFor(body.Paths, relFound)})
}

// POST /api/v1/shared-mounts/{shareID}/download-token?path=<rel>
//
// Mints a short-lived token for a native browser download inside the mount.
// Reuses the downloadGrant machinery scoped to the giver's storage
// (UserID = share.UserID, RootID = *share.SyncRootID). The optional `path`
// defaults to the mount root; GET /download/{token} serves a file target as
// the file itself and a directory target as a ZIP (ADR-0012, ADR-0073).
func (h *ShareHandler) MintMountDownloadToken(w http.ResponseWriter, r *http.Request) {
	share := h.resolveMount(w, r)
	if share == nil {
		return
	}
	target, ok := mountSubPath(w, r, share, false)
	if !ok {
		return
	}
	token := mintDownloadToken(share.UserID, *share.SyncRootID, target)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"expires_in": int(downloadTokenTTL.Seconds()),
	})
}

// GET /api/v1/shared-mounts/{shareID}/download?path=<rel>
//
// Streams a single file. Mirrors the own-root DownloadFile shape.
func (h *ShareHandler) DownloadMountFile(w http.ResponseWriter, r *http.Request) {
	share := h.resolveMount(w, r)
	if share == nil {
		return
	}
	target, ok := mountSubPath(w, r, share, false)
	if !ok {
		return
	}
	dbFile, err := h.files.FindByPath(r.Context(), *share.SyncRootID, target)
	if err != nil || dbFile.IsDeleted || dbFile.IsDir {
		writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
		return
	}
	w.Header().Set("Content-Disposition", contentDispositionAttachment(path.Base(target)))
	w.Header().Set("X-Content-Hash", hex.EncodeToString(dbFile.ContentHash))
	w.Header().Set("X-Server-Version", strconv.FormatInt(dbFile.ServerVersion, 10))
	serveFileContent(w, r, h.storage, share.UserID, share.SyncRootID.String(), target, dbFile.MTime)
}
