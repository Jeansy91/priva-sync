package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/audit"
	"privasync/server/internal/mail"
	"privasync/server/internal/model"
	"privasync/server/internal/officedoc"
	"privasync/server/internal/quota"
	"privasync/server/internal/repository"
	"privasync/server/internal/sse"
	sseevents "privasync/server/internal/sse/events"
	"privasync/server/internal/storage"
	syncpkg "privasync/server/internal/sync"
)

// The share* interfaces are the narrow slices of the concrete repositories that
// ShareHandler actually calls (ADR-0029, "accept interfaces"). Kept minimal so
// the concrete repos satisfy them structurally — router wiring is unchanged —
// and the 500-error branches become drivable with tiny hand-written fakes.

// shareStore is the share-aggregate surface (create, grants, public tokens,
// lookups, mount resolution) ShareHandler depends on.
type shareStore interface {
	Create(ctx context.Context, s *model.Share) (*model.Share, error)
	EnsureUserShare(ctx context.Context, ownerID, syncRootID uuid.UUID, path string) (*model.Share, error)
	EnsurePublicShare(ctx context.Context, ownerID, syncRootID uuid.UUID, path string) (*model.Share, error)
	FindPublicShare(ctx context.Context, ownerID, syncRootID uuid.UUID, path string) (*model.Share, error)
	SetPublicToken(ctx context.Context, shareID, ownerID uuid.UUID, kind, token string, expiresAt *time.Time) (*model.Share, error)
	ClearPublicToken(ctx context.Context, shareID, ownerID uuid.UUID, kind string) (*model.Share, error)
	FindMountByGrant(ctx context.Context, grantID, targetUserID uuid.UUID) (*model.Share, error)
	ListByUser(ctx context.Context, userID uuid.UUID) ([]model.Share, error)
	ListMountsForTarget(ctx context.Context, targetUserID uuid.UUID) ([]model.SharedMount, error)
	ListBySyncRoot(ctx context.Context, syncRootID uuid.UUID) ([]repository.ShareWithTarget, error)
	FindByID(ctx context.Context, id uuid.UUID) (*model.Share, error)
	FindByToken(ctx context.Context, token string) (*model.Share, error)
	Delete(ctx context.Context, shareID, userID uuid.UUID) error
	DeleteByTarget(ctx context.Context, grantID, targetUserID uuid.UUID) error
	AddGrant(ctx context.Context, shareID, targetUserID uuid.UUID, canWrite bool, expiresAt *time.Time) (*model.ShareGrant, error)
	RemoveGrant(ctx context.Context, shareID, targetUserID uuid.UUID) (*model.ShareGrant, error)
	UpdateGrant(ctx context.Context, shareID, targetUserID uuid.UUID, canWrite bool, expiresAt *time.Time) (*model.ShareGrant, error)
	EffectiveAccess(ctx context.Context, syncRootID uuid.UUID, path string) ([]repository.AccessUser, error)
}

// shareFileStore is the file-tree surface ShareHandler reads for mount listing,
// public-share browsing, ZIP streaming and empty-dir creation.
type shareFileStore interface {
	FindByPath(ctx context.Context, syncRootID uuid.UUID, path string) (*model.File, error)
	EachInSubtree(ctx context.Context, syncRootID uuid.UUID, dirPath string, fn func(model.File) error) error
	ListDirectoryLevel(ctx context.Context, p repository.ListDirectoryLevelParams) ([]model.File, error)
	CreateDirectory(ctx context.Context, userID, syncRootID uuid.UUID, dirPath string, mtime time.Time) (*model.File, error)
	ListChangedFilesSinceUnderPrefix(ctx context.Context, syncRootID uuid.UUID, dirPath string, sinceSeq int64, limit int) ([]model.File, error)
	ListMoveEventsSinceUnderPrefix(ctx context.Context, syncRootID uuid.UUID, dirPath string, sinceSeq int64, limit int) ([]model.MoveEvent, error)
	DirectoryStats(ctx context.Context, syncRootID uuid.UUID, dirPath string) (repository.DirectoryStats, error)
	FindByPaths(ctx context.Context, syncRootID uuid.UUID, paths []string) ([]model.File, error)
}

// shareSyncRootStore is the owner-scoped root lookup used to authorise
// giver-side share operations.
type shareSyncRootStore interface {
	GetSyncRoot(ctx context.Context, id, userID uuid.UUID) (*model.SyncRoot, error)
}

// shareConflictStore is the conflict surface behind the mount conflict views.
type shareConflictStore interface {
	ListOpenConflictsUnderPrefix(ctx context.Context, ownerID, rootID uuid.UUID, prefix string) ([]model.Conflict, error)
	FindOpenConflictDetail(ctx context.Context, conflictID uuid.UUID) (*repository.ConflictDetail, error)
}

// shareAuditStore is the audit-write surface (fire-and-forget) ShareHandler uses.
type shareAuditStore interface {
	Log(ctx context.Context, e audit.Event) error
}

type ShareHandler struct {
	shares         shareStore
	files          shareFileStore
	syncRoots      shareSyncRootStore
	users          *repository.UserRepository
	conflicts      shareConflictStore
	audits         shareAuditStore
	storage        *storage.Service
	mailer         *mail.Mailer
	hub            *sse.Hub
	quota          *quota.Service
	coordinator    syncpkg.Coordinator
	baseURL        string
	maxUploadBytes int64
	officeCfg      officedoc.Config
}

func NewShareHandler(
	shares shareStore,
	files shareFileStore,
	syncRoots shareSyncRootStore,
	users *repository.UserRepository,
	conflicts shareConflictStore,
	audits shareAuditStore,
	stor *storage.Service,
	mailer *mail.Mailer,
	hub *sse.Hub,
	q *quota.Service,
	coord syncpkg.Coordinator,
	baseURL string,
	maxUploadMB int64,
	officeCfg officedoc.Config,
) *ShareHandler {
	return &ShareHandler{
		shares:         shares,
		files:          files,
		syncRoots:      syncRoots,
		users:          users,
		conflicts:      conflicts,
		audits:         audits,
		storage:        stor,
		mailer:         mailer,
		hub:            hub,
		quota:          q,
		coordinator:    coord,
		baseURL:        baseURL,
		maxUploadBytes: maxUploadMB * 1024 * 1024,
		officeCfg:      officeCfg,
	}
}

type shareResponse struct {
	ID           uuid.UUID  `json:"id"`
	GrantID      *uuid.UUID `json:"grant_id,omitempty"`
	SyncRootID   *uuid.UUID `json:"sync_root_id,omitempty"`
	TargetUserID *uuid.UUID `json:"target_user_id,omitempty"`
	Path         string     `json:"path"`
	ShareType    string     `json:"share_type"`
	Token        *string    `json:"token,omitempty"`
	ReadToken    *string    `json:"read_token,omitempty"`
	WriteToken   *string    `json:"write_token,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	// Per-link expiry for public dual-links (ADR-0015).
	ReadExpiresAt  *time.Time `json:"read_expires_at,omitempty"`
	WriteExpiresAt *time.Time `json:"write_expires_at,omitempty"`
	CanWrite       bool       `json:"can_write"`
	CreatedAt      time.Time  `json:"created_at"`
}

func toShareResponse(s *model.Share) shareResponse {
	return shareResponse{
		ID:             s.ID,
		SyncRootID:     s.SyncRootID,
		TargetUserID:   s.TargetUserID,
		Path:           s.Path,
		ShareType:      string(s.ShareType),
		Token:          s.Token,
		ReadToken:      s.ReadToken,
		WriteToken:     s.WriteToken,
		ExpiresAt:      s.ExpiresAt,
		ReadExpiresAt:  s.ReadExpiresAt,
		WriteExpiresAt: s.WriteExpiresAt,
		CanWrite:       s.CanWrite,
		CreatedAt:      s.CreatedAt,
	}
}

// shareResponseToRef lifts the REST response shape into the SSE payload struct.
// Field-for-field copy so the SSE wire matches the REST wire exactly.
func shareResponseToRef(r shareResponse) sseevents.ShareRef {
	return sseevents.ShareRef{
		ID:           r.ID,
		SyncRootID:   r.SyncRootID,
		TargetUserID: r.TargetUserID,
		Path:         r.Path,
		ShareType:    r.ShareType,
		Token:        r.Token,
		ExpiresAt:    r.ExpiresAt,
		CanWrite:     r.CanWrite,
		CreatedAt:    r.CreatedAt,
	}
}

// GET /api/v1/sync-roots/{rootID}/shares
//
// Giver-side share-map for one of the caller's own sync roots. Returns
// shares grouped by path so the Files-Browser can render badges per
// row without a per-entry lookup. Includes both user-shares (with
// target username) and public-token shares. See ADR-0010.
func (h *ShareHandler) ListSharesByRoot(w http.ResponseWriter, r *http.Request) {
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
	shares, err := h.shares.ListBySyncRoot(r.Context(), rootID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	type shareInfo struct {
		ID             uuid.UUID  `json:"id"`
		ShareID        uuid.UUID  `json:"share_id"`
		ShareType      string     `json:"share_type"`
		TargetUserID   *uuid.UUID `json:"target_user_id,omitempty"`
		TargetUsername *string    `json:"target_username,omitempty"`
		CanWrite       bool       `json:"can_write"`
		ExpiresAt      *time.Time `json:"expires_at"`
		// Public dual-link tokens — owner-only endpoint, so safe to surface so
		// the share dialog can show the links without a re-fetch.
		ReadToken  *string `json:"read_token,omitempty"`
		WriteToken *string `json:"write_token,omitempty"`
		// Per-link expiry, chosen at link creation (ADR-0015).
		ReadExpiresAt  *time.Time `json:"read_expires_at,omitempty"`
		WriteExpiresAt *time.Time `json:"write_expires_at,omitempty"`
	}
	resp := make(map[string][]shareInfo, len(shares))
	for _, s := range shares {
		resp[s.Path] = append(resp[s.Path], shareInfo{
			ID:             s.ID,
			ShareID:        s.AggregateID,
			ShareType:      string(s.ShareType),
			TargetUserID:   s.TargetUserID,
			TargetUsername: s.TargetUsername,
			CanWrite:       s.CanWrite,
			ReadToken:      s.ReadToken,
			WriteToken:     s.WriteToken,
			ExpiresAt:      s.ExpiresAt,
			ReadExpiresAt:  s.ReadExpiresAt,
			WriteExpiresAt: s.WriteExpiresAt,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// GET /api/v1/sync-roots/{rootID}/access?path=<p>
//
// Effective access map for a path inside one of the caller's own roots:
// recipients split into writers/readers, resolved from every ancestor share so
// items inside a shared directory transparently show who can reach them.
// Owner-only — a non-owner root yields 404. See ADR-0015.
func (h *ShareHandler) ShareAccess(w http.ResponseWriter, r *http.Request) {
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
	if path == "" {
		path = "/"
	}

	users, err := h.shares.EffectiveAccess(r.Context(), rootID, path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	type accessUser struct {
		ID        uuid.UUID `json:"id"`
		Username  string    `json:"username"`
		AvatarURL *string   `json:"avatar_url"`
	}
	resp := struct {
		Writers []accessUser `json:"writers"`
		Readers []accessUser `json:"readers"`
	}{Writers: []accessUser{}, Readers: []accessUser{}}
	for _, u := range users {
		var avatarURL *string
		if u.AvatarPath != nil {
			s := "/api/v1/auth/avatar/" + u.ID.String()
			avatarURL = &s
		}
		au := accessUser{ID: u.ID, Username: u.Username, AvatarURL: avatarURL}
		if u.CanWrite {
			resp.Writers = append(resp.Writers, au)
		} else {
			resp.Readers = append(resp.Readers, au)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// GET /api/v1/shares
func (h *ShareHandler) ListShares(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	shares, err := h.shares.ListByUser(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	resp := make([]shareResponse, 0, len(shares))
	for i := range shares {
		resp = append(resp, toShareResponse(&shares[i]))
	}
	writeJSON(w, http.StatusOK, resp)
}

// POST /api/v1/sync-roots/{rootID}/shares
func (h *ShareHandler) CreateShare(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())

	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}

	root, err := h.syncRoots.GetSyncRoot(r.Context(), rootID, claims.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	var req struct {
		Path           string  `json:"path"`
		ShareType      string  `json:"share_type"`
		TargetUserID   *string `json:"target_user_id"`
		TargetUsername *string `json:"target_username"`
		ExpiresAt      *string `json:"expires_at"`
		CanWrite       bool    `json:"can_write"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" || req.ShareType == "" {
		writeError(w, http.StatusBadRequest, ErrShareFieldsMiss, "path and share_type required")
		return
	}
	if req.ShareType != string(model.ShareTypePublic) && req.ShareType != string(model.ShareTypeUser) {
		writeError(w, http.StatusBadRequest, ErrShareTypeInvalid, "share_type must be 'public' or 'user'")
		return
	}

	expiresAt, ok := parseOptionalExpiry(req.ExpiresAt)
	if !ok {
		writeError(w, http.StatusBadRequest, ErrInvalidExpiresAt, "invalid expires_at format (RFC3339 required)")
		return
	}

	// Public shares keep the single-row-with-token shape (dual-link is an
	// ADR-0015 follow-up). User shares become an aggregate — one editable share
	// per path — carrying recipients as grants.
	if req.ShareType == string(model.ShareTypePublic) {
		// Two links: a read token (also kept in `token` for backward compat) and
		// — for directories only — a write token. Files carry no write access.
		readTok, err := generateToken()
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
			return
		}
		pub := &model.Share{
			UserID:     claims.UserID,
			SyncRootID: &root.ID,
			Path:       req.Path,
			ShareType:  model.ShareTypePublic,
			CanWrite:   req.CanWrite,
			Token:      &readTok,
			ReadToken:  &readTok,
			ExpiresAt:  expiresAt,
		}
		if !isFileShare(r.Context(), h.files, &model.Share{SyncRootID: &root.ID, Path: req.Path}) {
			writeTok, err := generateToken()
			if err != nil {
				writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
				return
			}
			pub.WriteToken = &writeTok
		}
		created, err := h.shares.Create(r.Context(), pub)
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
			return
		}
		_ = h.audits.Log(r.Context(), audit.FromRequest(r, &claims.UserID, audit.EventShareCreated, map[string]any{
			"share_id": created.ID, "share_type": created.ShareType,
			"sync_root_id": created.SyncRootID, "path": created.Path, "can_write": created.CanWrite,
		}))
		resp := toShareResponse(created)
		syncpkg.PublishEphemeral(h.hub, claims.UserID, sseevents.ShareAdded{Share: shareResponseToRef(resp)})
		writeJSON(w, http.StatusCreated, resp)
		return
	}

	// --- user share: resolve recipient, then upsert a grant on the aggregate.
	var targetID uuid.UUID
	switch {
	case req.TargetUserID != nil && *req.TargetUserID != "":
		id, err := uuid.Parse(*req.TargetUserID)
		if err != nil {
			writeError(w, http.StatusBadRequest, ErrInvalidTargetUser, "invalid target_user_id")
			return
		}
		if _, err := h.users.FindByID(r.Context(), id); err != nil {
			writeError(w, http.StatusNotFound, ErrTargetUserNotFound, "target user not found")
			return
		}
		targetID = id
	case req.TargetUsername != nil && *req.TargetUsername != "":
		target, err := h.users.FindByUsername(r.Context(), *req.TargetUsername)
		if err != nil {
			writeError(w, http.StatusNotFound, ErrTargetUserNotFound, "target user not found")
			return
		}
		targetID = target.ID
	default:
		writeError(w, http.StatusBadRequest, ErrShareUserMissing, "target_user_id or target_username required for user share")
		return
	}
	if targetID == claims.UserID {
		writeError(w, http.StatusBadRequest, ErrShareSelf, "cannot share with yourself")
		return
	}

	// File shares carry no write access — only directories (ADR-0015).
	canWrite := req.CanWrite
	if isFileShare(r.Context(), h.files, &model.Share{SyncRootID: &root.ID, Path: req.Path}) {
		canWrite = false
	}

	agg, err := h.shares.EnsureUserShare(r.Context(), claims.UserID, root.ID, req.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	grant, err := h.shares.AddGrant(r.Context(), agg.ID, targetID, canWrite, expiresAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	sendShareReceivedMail(
		r.Context(), h.users, h.mailer, h.baseURL,
		targetID, claims.UserID, path.Base(agg.Path), agg.ID,
	)

	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &claims.UserID, audit.EventShareCreated, map[string]any{
		"share_id":       agg.ID,
		"grant_id":       grant.ID,
		"share_type":     agg.ShareType,
		"sync_root_id":   agg.SyncRootID,
		"target_user_id": targetID,
		"path":           agg.Path,
		"can_write":      grant.CanWrite,
	}))

	// Response describes the aggregate but surfaces the just-affected grant so
	// the recipient's mount anchor (grant id) and effective permission are
	// available without a re-fetch.
	resp := toShareResponse(agg)
	resp.GrantID = &grant.ID
	resp.TargetUserID = &targetID
	resp.CanWrite = grant.CanWrite
	resp.ExpiresAt = grant.ExpiresAt

	// Owner sees the aggregate; the recipient sees the grant as a mount (id =
	// grant id, can_write/expiry from the grant).
	syncpkg.PublishEphemeral(h.hub, claims.UserID, sseevents.ShareAdded{Share: shareResponseToRef(resp)})
	syncpkg.PublishEphemeral(h.hub, targetID, sseevents.ShareAdded{Share: sseevents.ShareRef{
		ID:           grant.ID,
		SyncRootID:   agg.SyncRootID,
		TargetUserID: &targetID,
		Path:         agg.Path,
		ShareType:    string(model.ShareTypeUser),
		ExpiresAt:    grant.ExpiresAt,
		CanWrite:     grant.CanWrite,
		CreatedAt:    grant.CreatedAt,
	}})

	writeJSON(w, http.StatusCreated, resp)
}

// PATCH /api/v1/shares/{shareID}/grants/{userID}
//
// Owner-only in-place change of one recipient's grant (can_write, expiry).
// Avoids the revoke+recreate dance that would tear down the recipient's
// activated mount. Emits ShareUpdated to owner and target so both lists patch
// in lock-step; the desktop treats it as latency-only and re-pulls. File
// shares force can_write=false. See ADR-0015 (supersedes ADR-0014).
func (h *ShareHandler) UpdateShareGrant(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())

	shareID, err := uuid.Parse(chi.URLParam(r, "shareID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidShareID, "invalid share id")
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidTargetUser, "invalid user id")
		return
	}

	var req struct {
		CanWrite  bool    `json:"can_write"`
		ExpiresAt *string `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrShareFieldsMiss, "can_write required")
		return
	}
	expiresAt, ok := parseOptionalExpiry(req.ExpiresAt)
	if !ok {
		writeError(w, http.StatusBadRequest, ErrInvalidExpiresAt, "invalid expires_at format (RFC3339 required)")
		return
	}

	// Owner-only: a non-owner (or unknown) aggregate is indistinguishable from
	// missing — 404, no existence leak.
	agg, err := h.shares.FindByID(r.Context(), shareID)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && agg.UserID != claims.UserID) {
		writeError(w, http.StatusNotFound, ErrShareNotFound, "share not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	canWrite := req.CanWrite
	if isFileShare(r.Context(), h.files, agg) {
		canWrite = false
	}

	grant, err := h.shares.UpdateGrant(r.Context(), shareID, targetID, canWrite, expiresAt)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrShareNotFound, "share not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &claims.UserID, audit.EventShareUpdated, map[string]any{
		"share_id":  shareID,
		"grant_id":  grant.ID,
		"can_write": grant.CanWrite,
	}))

	resp := toShareResponse(agg)
	resp.GrantID = &grant.ID
	resp.TargetUserID = &targetID
	resp.CanWrite = grant.CanWrite
	resp.ExpiresAt = grant.ExpiresAt

	syncpkg.PublishEphemeral(h.hub, claims.UserID, sseevents.ShareUpdated{Share: shareResponseToRef(resp)})
	syncpkg.PublishEphemeral(h.hub, targetID, sseevents.ShareUpdated{Share: sseevents.ShareRef{
		ID:           grant.ID,
		SyncRootID:   agg.SyncRootID,
		TargetUserID: &targetID,
		Path:         agg.Path,
		ShareType:    string(model.ShareTypeUser),
		ExpiresAt:    grant.ExpiresAt,
		CanWrite:     grant.CanWrite,
		CreatedAt:    grant.CreatedAt,
	}})

	writeJSON(w, http.StatusOK, resp)
}

// DELETE /api/v1/shares/{shareID}/grants/{userID}
//
// Owner removes one recipient from a share aggregate. Emits ShareRevoked to
// owner and recipient (keyed on the grant id = the recipient's mount anchor)
// so open sessions drop the mount. The aggregate and the other recipients'
// grants stay intact. See ADR-0015.
func (h *ShareHandler) DeleteShareGrant(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())

	shareID, err := uuid.Parse(chi.URLParam(r, "shareID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidShareID, "invalid share id")
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidTargetUser, "invalid user id")
		return
	}

	agg, err := h.shares.FindByID(r.Context(), shareID)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && agg.UserID != claims.UserID) {
		writeError(w, http.StatusNotFound, ErrShareNotFound, "share not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	grant, err := h.shares.RemoveGrant(r.Context(), shareID, targetID)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrShareNotFound, "share not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &claims.UserID, audit.EventShareDeleted, map[string]any{
		"share_id": shareID,
		"grant_id": grant.ID,
		"reason":   "grant_removed",
	}))
	syncpkg.PublishEphemeral(h.hub, claims.UserID, sseevents.ShareRevoked{ShareID: grant.ID})
	syncpkg.PublishEphemeral(h.hub, targetID, sseevents.ShareRevoked{ShareID: grant.ID})
	sendShareRevokedMail(
		r.Context(), h.users, h.mailer, h.baseURL,
		targetID, claims.UserID, path.Base(agg.Path),
	)
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/v1/shares/{shareID}
func (h *ShareHandler) DeleteShare(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())

	shareID, err := uuid.Parse(chi.URLParam(r, "shareID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidShareID, "invalid share id")
		return
	}

	// Load before delete so we can notify the target user by mail (ADR-0010)
	// and route the SSE event to them. Non-fatal if missing — Delete below
	// will short-circuit with the same 404.
	existing, _ := h.shares.FindByID(r.Context(), shareID)

	err = h.shares.Delete(r.Context(), shareID, claims.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrShareNotFound, "share not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &claims.UserID, audit.EventShareDeleted, map[string]any{
		"share_id": shareID,
	}))
	syncpkg.PublishEphemeral(h.hub, claims.UserID, sseevents.ShareRevoked{ShareID: shareID})
	if existing != nil && existing.TargetUserID != nil {
		syncpkg.PublishEphemeral(h.hub, *existing.TargetUserID, sseevents.ShareRevoked{ShareID: shareID})
		sendShareRevokedMail(
			r.Context(), h.users, h.mailer, h.baseURL,
			*existing.TargetUserID, claims.UserID,
			path.Base(existing.Path),
		)
	}
	w.WriteHeader(http.StatusNoContent)
}

// isUnderShareRoot reports whether `candidate` (already path.Clean'd) equals
// `root` or sits beneath it — prevents `..`-escapes.
func isUnderShareRoot(candidate, root string) bool {
	if candidate == root {
		return true
	}
	r := root
	if !strings.HasSuffix(r, "/") {
		r += "/"
	}
	return strings.HasPrefix(candidate, r)
}

// parseOptionalExpiry parses an optional RFC3339 expiry. nil or "" mean
// "no limit"; the bool reports whether the value parsed cleanly.
func parseOptionalExpiry(s *string) (*time.Time, bool) {
	if s == nil || *s == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil, false
	}
	return &t, true
}

func generateToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
