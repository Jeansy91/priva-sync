package handler

import (
	"encoding/hex"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

const (
	searchLimitDefault = 200
	searchLimitMax     = 500
)

// translateSearchPattern converts a user query into a SQL LIKE pattern
// body: SQL metacharacters `%`, `_`, `\` are escaped, the user-facing
// wildcard `*` becomes `%`. Input is lowercased to match the
// `lower(path) gin_trgm_ops` index.
func translateSearchPattern(q string) string {
	lower := strings.ToLower(q)
	escaped := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	).Replace(lower)
	return strings.ReplaceAll(escaped, "*", "%")
}

// GET /api/v1/files/search?q=...&limit=...
// Global search across every sync-root the caller owns plus every active
// Shared Mount where the caller is the target. Returns one group per
// root/mount with at least one hit. Each group carries its own keyset
// cursor; the load-more flow reuses the per-root SearchFiles endpoint.
// Mount groups carry mount_id + a giver-relative `sync_root_name`; file
// paths in mount groups are stripped to be mount-relative.
// See ADR-0008 and ADR-0010.
func (h *FileHandler) SearchFilesGlobal(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, ErrPathRequired, "query required")
		return
	}

	limit := searchLimitDefault
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > searchLimitMax {
		limit = searchLimitMax
	}

	roots, err := h.syncRoots.ListSyncRoots(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	pattern := translateSearchPattern(q)

	type fileResponse struct {
		ID           uuid.UUID              `json:"id"`
		Path         string                 `json:"path"`
		Size         int64                  `json:"size"`
		MTime        time.Time              `json:"mtime"`
		ContentHash  string                 `json:"content_hash,omitempty"`
		IsDeleted    bool                   `json:"is_deleted"`
		IsDir        bool                   `json:"is_dir,omitempty"`
		UpdatedAt    time.Time              `json:"updated_at"`
		LastModifier *userSummaryWithAvatar `json:"last_modifier,omitempty"`
	}
	type groupResponse struct {
		SyncRootID   uuid.UUID      `json:"sync_root_id"`
		SyncRootName string         `json:"sync_root_name"`
		MountID      *uuid.UUID     `json:"mount_id,omitempty"`
		Files        []fileResponse `json:"files"`
		NextCursor   string         `json:"next_cursor"`
	}

	// Two-pass: collect all hits per group first, then a single batch
	// users lookup feeds the embedded last_modifier on the second pass.
	// stripPrefix is set on mount groups so file paths become mount-relative.
	type pendingGroup struct {
		group       groupResponse
		files       []model.File
		stripPrefix string
	}
	var pending []pendingGroup
	var modifierIDs []uuid.UUID

	for _, root := range roots {
		files, err := h.files.SearchFiles(r.Context(), repository.SearchFilesParams{
			SyncRootID:  root.ID,
			PathPrefix:  "/",
			LikePattern: pattern,
			Limit:       limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
			return
		}
		if len(files) == 0 {
			continue
		}
		group := groupResponse{
			SyncRootID:   root.ID,
			SyncRootName: root.Name,
			Files:        make([]fileResponse, 0, len(files)),
		}
		for _, f := range files {
			if f.LastModifierUserID != nil {
				modifierIDs = append(modifierIDs, *f.LastModifierUserID)
			}
		}
		if len(files) == limit {
			group.NextCursor = files[len(files)-1].Path
		}
		pending = append(pending, pendingGroup{group: group, files: files})
	}

	// Shared Mounts: search inside every active user-share where the
	// caller is the target. Hits are scoped to share.path and the giver-
	// root prefix is stripped so the recipient sees mount-relative paths.
	if h.shares != nil {
		mounts, err := h.shares.ListMountsForTarget(r.Context(), claims.UserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
			return
		}
		for _, m := range mounts {
			files, err := h.files.SearchFiles(r.Context(), repository.SearchFilesParams{
				SyncRootID:  m.SyncRootID,
				PathPrefix:  m.Path,
				LikePattern: pattern,
				Limit:       limit,
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
				return
			}
			if len(files) == 0 {
				continue
			}
			mountID := m.ShareID
			group := groupResponse{
				SyncRootID:   m.SyncRootID,
				SyncRootName: m.OwnerUsername + ": " + path.Base(m.Path),
				MountID:      &mountID,
				Files:        make([]fileResponse, 0, len(files)),
			}
			for _, f := range files {
				if f.LastModifierUserID != nil {
					modifierIDs = append(modifierIDs, *f.LastModifierUserID)
				}
			}
			if len(files) == limit {
				group.NextCursor = files[len(files)-1].Path
			}
			pending = append(pending, pendingGroup{group: group, files: files, stripPrefix: m.Path})
		}
	}

	modifiers, err := resolveLastModifiers(r.Context(), h.users, modifierIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	// Second pass: materialise file rows, applying mount-relative stripping
	// when stripPrefix is set (mount groups).
	resp := struct {
		Groups []groupResponse `json:"groups"`
	}{Groups: make([]groupResponse, 0, len(pending))}
	for _, pg := range pending {
		group := pg.group
		for _, f := range pg.files {
			fp := f.Path
			if pg.stripPrefix != "" {
				rel := strings.TrimPrefix(fp, pg.stripPrefix)
				if rel == "" || rel[0] != '/' {
					rel = "/" + rel
				}
				fp = rel
			}
			group.Files = append(group.Files, fileResponse{
				ID:           f.ID,
				Path:         fp,
				Size:         f.Size,
				MTime:        f.MTime,
				ContentHash:  hex.EncodeToString(f.ContentHash),
				IsDeleted:    f.IsDeleted,
				IsDir:        f.IsDir,
				UpdatedAt:    f.UpdatedAt,
				LastModifier: modifiers.lookupOptional(f.LastModifierUserID),
			})
		}
		resp.Groups = append(resp.Groups, group)
	}

	writeJSON(w, http.StatusOK, resp)
}

// GET /api/v1/sync-roots/{rootID}/files/search?q=...&path=...&limit=...&cursor=...
// Server-side trigram search under one sync root. See ADR-0008.
func (h *FileHandler) SearchFiles(w http.ResponseWriter, r *http.Request) {
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

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, ErrPathRequired, "query required")
		return
	}

	prefix := r.URL.Query().Get("path")
	if prefix == "" {
		prefix = "/"
	}
	prefix = path.Clean("/" + strings.TrimLeft(prefix, "/"))

	limit := searchLimitDefault
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > searchLimitMax {
		limit = searchLimitMax
	}

	cursor := r.URL.Query().Get("cursor")

	files, err := h.files.SearchFiles(r.Context(), repository.SearchFilesParams{
		SyncRootID:  rootID,
		PathPrefix:  prefix,
		LikePattern: translateSearchPattern(q),
		Limit:       limit,
		Cursor:      cursor,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	type fileResponse struct {
		ID           uuid.UUID              `json:"id"`
		Path         string                 `json:"path"`
		Size         int64                  `json:"size"`
		MTime        time.Time              `json:"mtime"`
		ContentHash  string                 `json:"content_hash,omitempty"`
		IsDeleted    bool                   `json:"is_deleted"`
		IsDir        bool                   `json:"is_dir,omitempty"`
		UpdatedAt    time.Time              `json:"updated_at"`
		LastModifier *userSummaryWithAvatar `json:"last_modifier,omitempty"`
	}

	modifierIDs := make([]uuid.UUID, 0, len(files))
	for _, f := range files {
		if f.LastModifierUserID != nil {
			modifierIDs = append(modifierIDs, *f.LastModifierUserID)
		}
	}
	modifiers, err := resolveLastModifiers(r.Context(), h.users, modifierIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	resp := struct {
		Files      []fileResponse `json:"files"`
		NextCursor string         `json:"next_cursor"`
	}{
		Files: make([]fileResponse, 0, len(files)),
	}
	for _, f := range files {
		resp.Files = append(resp.Files, fileResponse{
			ID:           f.ID,
			Path:         f.Path,
			Size:         f.Size,
			MTime:        f.MTime,
			ContentHash:  hex.EncodeToString(f.ContentHash),
			IsDeleted:    f.IsDeleted,
			IsDir:        f.IsDir,
			UpdatedAt:    f.UpdatedAt,
			LastModifier: modifiers.lookupOptional(f.LastModifierUserID),
		})
	}
	if len(files) == limit {
		resp.NextCursor = files[len(files)-1].Path
	}
	writeJSON(w, http.StatusOK, resp)
}
