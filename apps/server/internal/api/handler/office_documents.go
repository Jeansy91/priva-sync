package handler

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"path"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/model"
	"privasync/server/internal/officedoc"
)

// officeDocGrant authorises a single document fetch by the external
// document server for read-only Office preview (ADR-0027). Unlike
// downloadGrant it is NOT consumed on first use: the document server may
// re-fetch the same document several times while one preview session is
// open, so the grant lives until its TTL expires.
type officeDocGrant struct {
	// OwnerID is whose storage tree the file lives under — the caller for
	// an own sync root, the giver for a Shared Mount (ADR-0010).
	OwnerID   uuid.UUID
	RootID    uuid.UUID
	Path      string
	ExpiresAt time.Time
}

const officeDocTokenTTL = 5 * time.Minute

var (
	officeDocTokens   = map[string]officeDocGrant{}
	officeDocTokensMu sync.Mutex
)

func mintOfficeDocToken(ownerID, rootID uuid.UUID, docPath string) string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	token := hex.EncodeToString(b[:])

	officeDocTokensMu.Lock()
	defer officeDocTokensMu.Unlock()

	now := time.Now()
	for t, g := range officeDocTokens {
		if g.ExpiresAt.Before(now) {
			delete(officeDocTokens, t)
		}
	}
	officeDocTokens[token] = officeDocGrant{
		OwnerID:   ownerID,
		RootID:    rootID,
		Path:      docPath,
		ExpiresAt: now.Add(officeDocTokenTTL),
	}
	return token
}

func lookupOfficeDocToken(token string) (officeDocGrant, bool) {
	officeDocTokensMu.Lock()
	defer officeDocTokensMu.Unlock()

	g, ok := officeDocTokens[token]
	if !ok || g.ExpiresAt.Before(time.Now()) {
		delete(officeDocTokens, token)
		return officeDocGrant{}, false
	}
	return g, true
}

// GET /office/documents/{token}
// Public route (no auth middleware) — the document server cannot send our
// Bearer header, so access is gated by the opaque, short-lived token minted
// alongside the signed editor config instead. See ADR-0027.
func (h *FileHandler) ServeOfficeDocument(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		writeError(w, http.StatusBadRequest, ErrTokenRequired, "token required")
		return
	}
	grant, ok := lookupOfficeDocToken(token)
	if !ok {
		writeError(w, http.StatusNotFound, ErrTokenInvalid, "token invalid or expired")
		return
	}
	if err := h.storage.ServeFile(grant.OwnerID, grant.RootID.String(), grant.Path, w); err != nil {
		writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
	}
}

type officeConfigResponse struct {
	DocumentServerURL string         `json:"document_server_url"`
	Config            map[string]any `json:"config"`
}

// buildOfficeConfig mints the document-fetch token and delegates to
// officedoc.BuildAndSign. Shared by the own-root and mount-scoped handlers
// so the two never drift on document key / URL shape. When editable is
// true it additionally mints a callback token (ADR-0033) carrying the
// file's current server_version as BaseVersion, so a later save-back
// callback can route through the coordinator's normal If-Match/conflict
// machinery unchanged.
// grantID is the recipient's mount anchor for a Shared Mount and empty for an
// own-root document; the save-back path uses it to re-check the permission
// against the live grant.
func buildOfficeConfig(cfg officedoc.Config, baseURL string, ownerID, rootID uuid.UUID, dbFile *model.File, docPath string, requesterID uuid.UUID, editable bool, grantID string) (map[string]any, error) {
	format, _ := officedoc.TypeForPath(docPath)
	token := mintOfficeDocToken(ownerID, rootID, docPath)
	docURL := fmt.Sprintf("%s/office/documents/%s", baseURL, token)
	// Key changes with server_version so the document server's own cache
	// is invalidated whenever the underlying content changes.
	key := fmt.Sprintf("%s-%d", dbFile.ID, dbFile.ServerVersion)

	opts := officedoc.BuildOpts{
		Key:         key,
		Title:       path.Base(docPath),
		FileType:    format.FileType,
		DocType:     format.DocumentType,
		DocumentURL: docURL,
		UserID:      requesterID.String(),
		Username:    requesterID.String(),
	}
	// A format the document server only converts on open is never saved
	// back, however much write access the requester has (see officedoc.Type).
	if editable && format.Editable {
		callbackToken, err := officedoc.SignCallbackToken(cfg.JWTSecret, officedoc.CallbackClaims{
			OwnerID:     ownerID.String(),
			RootID:      rootID.String(),
			Path:        docPath,
			ActorUserID: requesterID.String(),
			GrantID:     grantID,
			BaseVersion: dbFile.ServerVersion,
		})
		if err != nil {
			return nil, err
		}
		opts.Editable = true
		opts.CallbackURL = fmt.Sprintf("%s/office/documents/callback?token=%s", baseURL, callbackToken)
	}

	config, _, err := officedoc.BuildAndSign(cfg, opts)
	return config, err
}

// GET /sync-roots/{rootID}/files/office-config
// Signed, short-lived editor config for an Office file in an own sync root
// (supported formats per ADR-0049). Owning the root is both read *and*
// write access here — there is no separate permission system for own roots
// — so the requester gets an editable config with save-back wired up
// whenever the format itself round-trips (ADR-0033).
func (h *FileHandler) OfficeConfig(w http.ResponseWriter, r *http.Request) {
	if !h.officeCfg.Enabled() {
		writeError(w, http.StatusNotFound, ErrDocumentServerDisabled, "document server not configured")
		return
	}
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
	cleanPath, err := sanitizePath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidPath, "invalid path")
		return
	}
	if _, ok := officedoc.TypeForPath(cleanPath); !ok {
		writeError(w, http.StatusBadRequest, ErrUnsupportedFileType, "unsupported file type")
		return
	}
	dbFile, err := h.files.FindByPath(r.Context(), rootID, cleanPath)
	if err != nil || dbFile.IsDeleted || dbFile.IsDir {
		writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
		return
	}
	config, err := buildOfficeConfig(h.officeCfg, h.baseURL, claims.UserID, rootID, dbFile, cleanPath, claims.UserID, true, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, officeConfigResponse{DocumentServerURL: h.officeCfg.URL, Config: config})
}

// GET /shared-mounts/{shareID}/files/office-config
// Mount-scoped counterpart of FileHandler.OfficeConfig. Being the target of
// the share is read access (ADR-0010); editability follows the mount's own
// can_write flag (ADR-0033) — a read-only recipient gets a view-only config,
// a writable one gets save-back wired up exactly like an own-root file.
func (h *ShareHandler) MountOfficeConfig(w http.ResponseWriter, r *http.Request) {
	if !h.officeCfg.Enabled() {
		writeError(w, http.StatusNotFound, ErrDocumentServerDisabled, "document server not configured")
		return
	}
	share := h.resolveMount(w, r)
	if share == nil {
		return
	}
	target, ok := mountSubPath(w, r, share, false)
	if !ok {
		return
	}
	if _, ok := officedoc.TypeForPath(target); !ok {
		writeError(w, http.StatusBadRequest, ErrUnsupportedFileType, "unsupported file type")
		return
	}
	dbFile, err := h.files.FindByPath(r.Context(), *share.SyncRootID, target)
	if err != nil || dbFile.IsDeleted || dbFile.IsDir {
		writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	config, err := buildOfficeConfig(h.officeCfg, h.baseURL, share.UserID, *share.SyncRootID, dbFile, target, claims.UserID, share.CanWrite, chi.URLParam(r, "shareID"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, officeConfigResponse{DocumentServerURL: h.officeCfg.URL, Config: config})
}
