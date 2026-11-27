package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/audit"
	"privasync/server/internal/officedoc"
	syncpkg "privasync/server/internal/sync"
)

// officeCallbackHTTPClient fetches the edited bytes the document server
// hands back in the callback body's url field. A generous but bounded
// timeout — this runs on a public, unauthenticated route, so it must never
// hang a worker goroutine indefinitely on a slow/stuck peer.
//
// Redirects are not followed: the URL's origin is checked before the request
// (AllowsDocumentURL), and a redirect would let the allowed host hand the
// connection on to one that never passed that check.
var officeCallbackHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// Status values from the document server's callback contract. Only
// MustSave/MustForceSave carry new bytes to persist; every other value is
// a lifecycle ping (editing started, closed without changes, save error,
// ...) that only needs an ack.
const (
	officeStatusMustSave      = 2
	officeStatusMustForceSave = 6
)

type officeCallbackBody struct {
	Status int    `json:"status"`
	URL    string `json:"url"`
}

// officeCallbackResponse is the document server's own success/failure
// envelope — always HTTP 200; error is carried inside the body. A
// non-conforming response makes the document server treat the save as
// failed and keep retrying / warn the user, so this shape is load-bearing.
type officeCallbackResponse struct {
	Error int `json:"error"`
}

// POST /office/documents/callback?token=<callback-jwt>
// Public route (no auth middleware, same reasoning as ServeOfficeDocument):
// the document server cannot send our Bearer header. Authorization is the
// opaque, server-signed callback token itself — never the document
// server's own JWT-signing of the request body (ADR-0033). Always answers
// 200 with {"error":0|1}; only a structurally invalid request (missing/bad
// token, unparsable JSON) gets a real 4xx.
func (h *FileHandler) OfficeCallback(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		writeError(w, http.StatusBadRequest, ErrTokenRequired, "token required")
		return
	}
	claims, err := officedoc.ParseCallbackToken(h.officeCfg.JWTSecret, token)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrTokenInvalid, "token invalid or expired")
		return
	}

	var body officeCallbackBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid callback body")
		return
	}

	switch body.Status {
	case officeStatusMustSave, officeStatusMustForceSave:
		if h.saveOfficeCallback(r, claims, body) {
			writeJSON(w, http.StatusOK, officeCallbackResponse{Error: 0})
		} else {
			writeJSON(w, http.StatusOK, officeCallbackResponse{Error: 1})
		}
	default:
		// Editing/closed-without-changes/save-error lifecycle pings — just
		// ack, nothing to persist.
		writeJSON(w, http.StatusOK, officeCallbackResponse{Error: 0})
	}
}

// saveOfficeCallback fetches the edited bytes from body.URL and persists
// them through the coordinator's normal upload pipeline, reusing the
// If-Match/conflict machinery unchanged (ADR-0033): a clean save (nobody
// wrote meanwhile) updates in place via claims.BaseVersion as If-Match. A
// version race — someone else wrote between config-mint and save — makes
// the first attempt come back ConflictVersionMismatch; since that attempt
// never commits (coordinator.UploadFile rolls back without touching the
// staging bytes on that path), the exact same command is safely retried
// once with IfMatch cleared, which routes it through the existing
// real-conflict/loser-copy path (ADR-0003) instead of silently overwriting
// or discarding the edit. Returns false whenever the document server
// should see {"error":1} (fetch failure, oversized body, quota, or any
// coordinator error) — never panics or leaves orphan staging bytes behind.
// On success it also writes an audit-log entry (files.office_saved),
// consistent with the existing file-event naming scheme.
func (h *FileHandler) saveOfficeCallback(r *http.Request, claims officedoc.CallbackClaims, body officeCallbackBody) bool {
	ctx := r.Context()
	// Only the configured document server may be dialled. Without this the
	// caller picks the target, and since the fetched bytes are written into a
	// file they own and can download again, that is a read primitive against
	// everything the server can reach.
	if !h.officeCfg.AllowsDocumentURL(body.URL) {
		slog.Warn("office callback: refused fetch outside the document server's origin",
			"url", body.URL)
		return false
	}
	ownerID, err := uuid.Parse(claims.OwnerID)
	if err != nil {
		return false
	}
	rootID, err := uuid.Parse(claims.RootID)
	if err != nil {
		return false
	}
	actorID, err := uuid.Parse(claims.ActorUserID)
	if err != nil {
		return false
	}
	if !h.callbackStillAuthorised(ctx, claims, ownerID, rootID, actorID) {
		return false
	}

	resp, err := officeCallbackHTTPClient.Get(body.URL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}

	rootStr := rootID.String()
	stagingPath := claims.Path + ".staging-" + uuid.NewString()
	// +1 so a body exactly at the limit is distinguishable from one that
	// exceeds it, mirroring http.MaxBytesReader's own convention.
	limited := io.LimitReader(resp.Body, h.maxUploadBytes+1)
	written, contentHash, err := h.storage.WriteFile(ownerID, rootStr, stagingPath, limited)
	if err != nil {
		return false
	}
	if written > h.maxUploadBytes {
		_ = h.storage.DeleteFile(ownerID, rootStr, stagingPath)
		return false
	}

	baseVersion := claims.BaseVersion
	cmd := syncpkg.UploadCmd{
		UserID:      ownerID,
		ActorUserID: actorID,
		SyncRootID:  rootID,
		Path:        claims.Path,
		StagingPath: stagingPath,
		Size:        written,
		ContentHash: contentHash,
		MTime:       time.Now(),
		IfMatch:     &baseVersion,
	}
	result, err := h.coordinator.UploadFile(ctx, cmd)
	if err != nil {
		_ = h.storage.DeleteFile(ownerID, rootStr, stagingPath)
		return false
	}
	if result.Conflict == syncpkg.ConflictVersionMismatch {
		cmd.IfMatch = nil
		if _, err := h.coordinator.UploadFile(ctx, cmd); err != nil {
			_ = h.storage.DeleteFile(ownerID, rootStr, stagingPath)
			return false
		}
	}

	_ = h.audits.Log(ctx, audit.FromRequest(r, &actorID, audit.EventFileOfficeSaved, map[string]any{
		"sync_root_id": rootID,
		"path":         claims.Path,
		"size":         written,
	}))
	return true
}

// callbackStillAuthorised re-checks, at save time, that the actor may still
// write the document the token names.
//
// The token itself is not enough: it lives for 24 hours, and every other
// mount operation resolves the recipient's grant fresh on each request
// (resolveMount). Without this the save-back path stayed authorised for the
// token's whole lifetime, so withdrawing a share -- or just its write flag --
// did not stop writes it had once permitted.
func (h *FileHandler) callbackStillAuthorised(
	ctx context.Context,
	claims officedoc.CallbackClaims,
	ownerID, rootID, actorID uuid.UUID,
) bool {
	// No grant: the document lives in the actor's own root, where owning it
	// is the permission. Anything else would be a token minted for a mount
	// and then replayed without one.
	if claims.GrantID == "" {
		if actorID != ownerID {
			slog.Warn("office callback: no grant and actor is not the owner",
				"actor_id", actorID, "owner_id", ownerID)
			return false
		}
		return true
	}

	grantID, err := uuid.Parse(claims.GrantID)
	if err != nil {
		return false
	}
	share, err := h.shares.FindMountByGrant(ctx, grantID, actorID)
	if err != nil {
		slog.Warn("office callback: grant no longer resolves",
			"grant_id", grantID, "actor_id", actorID, "error", err)
		return false
	}
	if share.ExpiresAt != nil && time.Now().After(*share.ExpiresAt) {
		return false
	}
	if !share.CanWrite {
		slog.Warn("office callback: write access withdrawn", "grant_id", grantID, "actor_id", actorID)
		return false
	}
	if share.SyncRootID == nil || *share.SyncRootID != rootID {
		return false
	}
	// The token names a concrete path; it must still sit inside the share.
	// The token names a concrete path; the share may since have been narrowed
	// to a different subtree, so the path has to still sit inside it.
	root := strings.TrimSuffix(share.Path, "/")
	if claims.Path != root && !strings.HasPrefix(claims.Path, root+"/") {
		slog.Warn("office callback: path outside the share",
			"path", claims.Path, "share_path", share.Path)
		return false
	}
	return true
}
