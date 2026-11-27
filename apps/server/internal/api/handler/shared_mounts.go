package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

// Endpoints under /shared-mounts expose the recipient-side view of
// user-shares as Shared Mounts: the caller browses, downloads and (with
// can_write) mutates files inside share.path, but all DB and storage
// writes happen under the Sync-Root-owner's user so quota, trash and
// conflicts stay with the owner. See ADR-0010.
//
// Handlers are split across files by concern:
//   - shared_mounts.go            — resolvers and small helpers (this file)
//   - shared_mounts_listing.go    — list mounts + list mount files
//   - shared_mounts_mutate.go     — upload, move, delete
//   - shared_mounts_meta.go       — stats, download-token, single-file download
//   - shared_mounts_lifecycle.go  — recipient "leave mount"

// resolveMount loads the share by id and validates that the caller is its
// target. Returns nil + writes the appropriate response (400/404/410) on
// failure. Every mount-scoped handler goes through this so the permission
// boundary lives in one place.
func (h *ShareHandler) resolveMount(w http.ResponseWriter, r *http.Request) *model.Share {
	claims := middleware.ClaimsFromContext(r.Context())
	// The mount id in the URL is the recipient's grant id (ADR-0015). The repo
	// resolves the aggregate scoped to the caller as recipient and overlays the
	// grant's can_write/expires_at, so downstream reads effective permission
	// straight off the returned Share.
	grantID, err := uuid.Parse(chi.URLParam(r, "shareID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidShareID, "invalid share id")
		return nil
	}
	share, err := h.shares.FindMountByGrant(r.Context(), grantID, claims.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrShareNotFound, "share not found")
		return nil
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return nil
	}
	if share.ExpiresAt != nil && time.Now().After(*share.ExpiresAt) {
		writeError(w, http.StatusGone, ErrShareExpired, "share expired")
		return nil
	}
	return share
}

// mutationDeps bundles the collaborators the shared mutation bodies need.
func (h *ShareHandler) mutationDeps() mutationDeps {
	return mutationDeps{h.storage, h.coordinator, h.quota, h.maxUploadBytes}
}

// resolveMountMutation resolves a writable mount into the MutationContext for
// a recipient-side write (ADR-0017): the owner is the giver whose quota/trash
// the bytes hit, the actor is the recipient who performed it (ADR-0013). The
// returned basePath is share.Path — the giver-root prefix every mount path is
// joined and escape-checked against. Writes the failure response and returns
// ok=false when resolution, expiry or the read-only guard rejects the caller.
func (h *ShareHandler) resolveMountMutation(w http.ResponseWriter, r *http.Request) (MutationContext, string, bool) {
	share := h.resolveMount(w, r)
	if share == nil {
		return MutationContext{}, "", false
	}
	if !requireWritableMount(w, share) {
		return MutationContext{}, "", false
	}
	mc := MutationContext{
		Owner:  share.UserID,
		Actor:  middleware.ClaimsFromContext(r.Context()).UserID,
		RootID: *share.SyncRootID,
	}
	return mc, share.Path, true
}

// mountSubPath joins the `path` query against share.Path, defending against
// '..'-escape. Returns the absolute giver-root path plus ok=true on
// success; on failure it has already written the error response.
// `required=true` rejects empty sub paths with 400 (write endpoints);
// `required=false` defaults to share.Path (read endpoints).
func mountSubPath(w http.ResponseWriter, r *http.Request, share *model.Share, required bool) (string, bool) {
	sub := strings.TrimSpace(r.URL.Query().Get("path"))
	if sub == "" {
		if required {
			writeError(w, http.StatusBadRequest, ErrPathRequired, "path required")
			return "", false
		}
		return share.Path, true
	}
	if sub == "/" {
		return share.Path, true
	}
	joined, ok := joinUnderShareRoot(share.Path, sub)
	if !ok {
		writeError(w, http.StatusForbidden, ErrInvalidPath, "sub path escapes share")
		return "", false
	}
	return joined, true
}

// requireWritableMount reports whether the caller may mutate inside the
// mount. Writes a 403 + ErrMountReadOnly on failure.
func requireWritableMount(w http.ResponseWriter, share *model.Share) bool {
	if share.CanWrite {
		return true
	}
	writeError(w, http.StatusForbidden, ErrMountReadOnly, "mount is read-only")
	return false
}

// isFileShare reports whether share.Path resolves to a file row (not a
// directory) inside the giver's sync-root. Used by the mount endpoints
// that need to dispatch over kind when the Share struct itself does not
// carry an explicit is_file flag.
func isFileShare(ctx context.Context, files shareFileStore, share *model.Share) bool {
	if share.SyncRootID == nil {
		return false
	}
	f, err := files.FindByPath(ctx, *share.SyncRootID, share.Path)
	if err != nil || f.IsDeleted {
		return false
	}
	return !f.IsDir
}
