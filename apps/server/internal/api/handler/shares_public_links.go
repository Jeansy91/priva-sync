package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/audit"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
	sseevents "privasync/server/internal/sse/events"
	syncpkg "privasync/server/internal/sync"
)

// Owner-side lifecycle of public links on a path's public share aggregate
// (ADR-0015 dual-link): generate, time-limit, remove. The anonymous
// recipient-side endpoints live in shares_public.go.

// POST /api/v1/sync-roots/{rootID}/public-links  body {path, kind}
//
// Generates one public link (read or write) for a path, get-or-creating the
// public share aggregate. read and write links have independent lifecycles.
// A write link is rejected for file shares (files carry no write access).
// See ADR-0015.
func (h *ShareHandler) CreatePublicLink(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		Path      string  `json:"path"`
		Kind      string  `json:"kind"`
		ExpiresAt *string `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" ||
		(req.Kind != "read" && req.Kind != "write") {
		writeError(w, http.StatusBadRequest, ErrShareFieldsMiss, "path and kind (read|write) required")
		return
	}
	if req.Kind == "write" && isFileShare(r.Context(), h.files, &model.Share{SyncRootID: &rootID, Path: req.Path}) {
		writeError(w, http.StatusBadRequest, ErrShareFieldsMiss, "write link not allowed for files")
		return
	}
	expiresAt, ok := parseOptionalExpiry(req.ExpiresAt)
	if !ok {
		writeError(w, http.StatusBadRequest, ErrInvalidExpiresAt, "invalid expires_at format (RFC3339 required)")
		return
	}

	agg, err := h.shares.EnsurePublicShare(r.Context(), claims.UserID, rootID, req.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	token, err := generateToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	updated, err := h.shares.SetPublicToken(r.Context(), agg.ID, claims.UserID, req.Kind, token, expiresAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &claims.UserID, audit.EventShareCreated, map[string]any{
		"share_id": updated.ID, "share_type": "public", "kind": req.Kind, "path": req.Path,
		"expires_at": expiresAt,
	}))
	resp := toShareResponse(updated)
	syncpkg.PublishEphemeral(h.hub, claims.UserID, sseevents.ShareAdded{Share: shareResponseToRef(resp)})
	writeJSON(w, http.StatusCreated, resp)
}

// DELETE /api/v1/sync-roots/{rootID}/public-links?path=<p>&kind=<read|write>
//
// Removes one public link. When both links are gone the public share is
// deleted. Idempotent — a missing share is a no-op. See ADR-0015.
func (h *ShareHandler) DeletePublicLink(w http.ResponseWriter, r *http.Request) {
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
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	kind := r.URL.Query().Get("kind")
	if path == "" || (kind != "read" && kind != "write") {
		writeError(w, http.StatusBadRequest, ErrShareFieldsMiss, "path and kind (read|write) required")
		return
	}

	share, err := h.shares.FindPublicShare(r.Context(), claims.UserID, rootID, path)
	if errors.Is(err, repository.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	if _, err := h.shares.ClearPublicToken(r.Context(), share.ID, claims.UserID, kind); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &claims.UserID, audit.EventShareDeleted, map[string]any{
		"share_id": share.ID, "share_type": "public", "kind": kind,
	}))
	syncpkg.PublishEphemeral(h.hub, claims.UserID, sseevents.ShareRevoked{ShareID: share.ID})
	w.WriteHeader(http.StatusNoContent)
}
