package handler

import (
	"encoding/hex"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/repository"
)

// GET /api/v1/shared-mounts
//
// Every active user-share where the caller is the target, with the giver
// profile embedded so the sidebar can render an avatar without a second
// round-trip.
func (h *ShareHandler) ListSharedMounts(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	mounts, err := h.shares.ListMountsForTarget(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	type ownerResponse struct {
		ID        uuid.UUID `json:"id"`
		Username  string    `json:"username"`
		AvatarURL *string   `json:"avatar_url"`
	}
	type mountResponse struct {
		ID        uuid.UUID     `json:"id"`
		Name      string        `json:"name"`
		Path      string        `json:"path"`
		IsFile    bool          `json:"is_file"`
		CanWrite  bool          `json:"can_write"`
		ExpiresAt *time.Time    `json:"expires_at"`
		Owner     ownerResponse `json:"owner"`
	}
	resp := make([]mountResponse, 0, len(mounts))
	for _, m := range mounts {
		var avatarURL *string
		if m.OwnerAvatarPath != nil {
			s := "/api/v1/auth/avatar/" + m.OwnerID.String()
			avatarURL = &s
		}
		// A root-share (path "/") has no basename — path.Base("/") is "/",
		// which is meaningless as a sidebar label. Fall back to the giver's
		// sync-folder name so it reads like a real shared folder.
		name := path.Base(m.Path)
		if m.Path == "/" {
			name = m.SyncRootName
		}
		resp = append(resp, mountResponse{
			ID:        m.ShareID,
			Name:      name,
			Path:      m.Path,
			IsFile:    m.IsFile,
			CanWrite:  m.CanWrite,
			ExpiresAt: m.ExpiresAt,
			Owner: ownerResponse{
				ID: m.OwnerID, Username: m.OwnerUsername, AvatarURL: avatarURL,
			},
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// GET /api/v1/shared-mounts/{shareID}/files?path=<rel>
//
// Direct children under <rel> within the mount. Mirrors the own-root
// ListFiles shape so the client can reuse rendering.
func (h *ShareHandler) ListMountFiles(w http.ResponseWriter, r *http.Request) {
	share := h.resolveMount(w, r)
	if share == nil {
		return
	}
	target, ok := mountSubPath(w, r, share, false)
	if !ok {
		return
	}
	prefix := target
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	entries, err := h.files.ListDirectoryLevel(r.Context(), repository.ListDirectoryLevelParams{
		SyncRootID: *share.SyncRootID,
		PathPrefix: prefix,
		Limit:      1000,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	// Same shape as the own-root listing, and for the same reason every field
	// is now always set: an entry is a row (ADR-0041).
	type entryResponse struct {
		Name         string                 `json:"name"`
		Path         string                 `json:"path"`
		IsDir        bool                   `json:"is_dir"`
		ID           uuid.UUID              `json:"id"`
		Size         int64                  `json:"size"`
		MTime        time.Time              `json:"mtime"`
		ContentHash  string                 `json:"content_hash,omitempty"`
		UpdatedAt    time.Time              `json:"updated_at"`
		LastModifier *userSummaryWithAvatar `json:"last_modifier,omitempty"`
	}
	modifierIDs := make([]uuid.UUID, 0, len(entries))
	for _, e := range entries {
		if e.LastModifierUserID != nil {
			modifierIDs = append(modifierIDs, *e.LastModifierUserID)
		}
	}
	modifiers, err := resolveLastModifiers(r.Context(), h.users, modifierIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	resp := struct {
		Entries []entryResponse `json:"entries"`
	}{Entries: make([]entryResponse, 0, len(entries))}
	for _, e := range entries {
		resp.Entries = append(resp.Entries, entryResponse{
			Name:         strings.TrimPrefix(e.Path, prefix),
			Path:         e.Path,
			IsDir:        e.IsDir,
			ID:           e.ID,
			Size:         e.Size,
			MTime:        e.MTime,
			ContentHash:  hex.EncodeToString(e.ContentHash),
			UpdatedAt:    e.UpdatedAt,
			LastModifier: modifiers.lookupOptional(e.LastModifierUserID),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}
