package handler

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
)

// mediaStreamGrant authorises repeated, Range-capable reads of one file by a
// native <audio>/<video> element (ADR-0067). Like officeDocGrant and unlike
// downloadGrant, it is NOT consumed on first use — a player fires many
// separate Range requests over the course of playback, not just one at open.
type mediaStreamGrant struct {
	OwnerID   uuid.UUID
	RootID    uuid.UUID
	Path      string
	ExpiresAt time.Time
}

const mediaStreamTokenTTL = 15 * time.Minute

var (
	mediaStreamTokens   = map[string]mediaStreamGrant{}
	mediaStreamTokensMu sync.Mutex
)

func mintMediaStreamToken(ownerID, rootID uuid.UUID, filePath string) string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	token := hex.EncodeToString(b[:])

	mediaStreamTokensMu.Lock()
	defer mediaStreamTokensMu.Unlock()

	now := time.Now()
	for t, g := range mediaStreamTokens {
		if g.ExpiresAt.Before(now) {
			delete(mediaStreamTokens, t)
		}
	}
	mediaStreamTokens[token] = mediaStreamGrant{
		OwnerID:   ownerID,
		RootID:    rootID,
		Path:      filePath,
		ExpiresAt: now.Add(mediaStreamTokenTTL),
	}
	return token
}

func lookupMediaStreamToken(token string) (mediaStreamGrant, bool) {
	mediaStreamTokensMu.Lock()
	defer mediaStreamTokensMu.Unlock()

	g, ok := mediaStreamTokens[token]
	if !ok || g.ExpiresAt.Before(time.Now()) {
		delete(mediaStreamTokens, token)
		return mediaStreamGrant{}, false
	}
	return g, true
}

type mediaStreamTokenResponse struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
}

// POST /sync-roots/{rootID}/files/media-token?path=<rel>
// Bearer-authed. Mints a media-stream grant for a file in the caller's own
// sync root — owning the root is read access here, same as every other
// own-root endpoint. See ADR-0067.
func (h *FileHandler) MintMediaStreamToken(w http.ResponseWriter, r *http.Request) {
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
	dbFile, err := h.files.FindByPath(r.Context(), rootID, cleanPath)
	if !isLiveFile(dbFile, err) {
		writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
		return
	}
	token := mintMediaStreamToken(claims.UserID, rootID, cleanPath)
	writeJSON(w, http.StatusOK, mediaStreamTokenResponse{
		Token:     token,
		ExpiresIn: int(mediaStreamTokenTTL.Seconds()),
	})
}

// POST /shared-mounts/{shareID}/files/media-token?path=<rel>
// Bearer-authed, mount-scoped counterpart of MintMediaStreamToken. Being the
// target of the share is read access (ADR-0010) — playback never checks
// can_write, exactly like MountOfficeConfig's view-only slice.
func (h *ShareHandler) MintMountMediaStreamToken(w http.ResponseWriter, r *http.Request) {
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
	token := mintMediaStreamToken(share.UserID, *share.SyncRootID, target)
	writeJSON(w, http.StatusOK, mediaStreamTokenResponse{
		Token:     token,
		ExpiresIn: int(mediaStreamTokenTTL.Seconds()),
	})
}

// GET /media/{token}
// Public route (no auth middleware) — a native <video>/<audio> src cannot
// carry our Bearer header, so access is gated by the opaque, short-lived
// grant minted alongside the mint endpoints below instead. Serves through
// serveFileContent (Range-capable, ADR-0047) so seeking/buffering works.
func (h *FileHandler) ServeMediaStream(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		writeError(w, http.StatusBadRequest, ErrTokenRequired, "token required")
		return
	}
	grant, ok := lookupMediaStreamToken(token)
	if !ok {
		writeError(w, http.StatusNotFound, ErrTokenInvalid, "token invalid or expired")
		return
	}
	dbFile, err := h.files.FindByPath(r.Context(), grant.RootID, grant.Path)
	if !isLiveFile(dbFile, err) {
		writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
		return
	}
	serveFileContent(w, r, h.storage, grant.OwnerID, grant.RootID.String(), grant.Path, dbFile.MTime)
}
