package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
)

// downloadGrant berechtigt zum einmaligen Streamen eines Sync-Root-ZIPs ohne
// Auth-Header. Die Tokens haben kurze TTL (5 min), werden In-Memory gehalten
// und bei der ersten Nutzung verbraucht. Path "" or "/" means the whole root;
// a directory path scopes the ZIP to that subtree, and a file path serves the
// file itself — so the browser's download manager streams it to disk instead
// of the page buffering it as a Blob first (ADR-0073).
type downloadGrant struct {
	UserID    uuid.UUID
	RootID    uuid.UUID
	Path      string
	ExpiresAt time.Time
}

const downloadTokenTTL = 5 * time.Minute

// fileDownloadResumeWindow is how long a file grant stays usable after its
// most recent request. A browser resumes a paused or dropped download with a
// Range request against the same URL, so a file grant cannot be single-use;
// it stays scoped to that one file and read-only (ADR-0073). ZIP grants stay
// single-use — a ZIP stream has no length and cannot be resumed anyway.
const fileDownloadResumeWindow = 24 * time.Hour

var (
	downloadTokens   = map[string]downloadGrant{}
	downloadTokensMu sync.Mutex
)

func mintDownloadToken(userID, rootID uuid.UUID, subtreePath string) string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	token := hex.EncodeToString(b[:])

	downloadTokensMu.Lock()
	defer downloadTokensMu.Unlock()

	// Opportunistic cleanup — selten genug aufgerufen, dass eine lineare
	// Iteration vertretbar ist.
	now := time.Now()
	for t, g := range downloadTokens {
		if g.ExpiresAt.Before(now) {
			delete(downloadTokens, t)
		}
	}
	downloadTokens[token] = downloadGrant{
		UserID:    userID,
		RootID:    rootID,
		Path:      subtreePath,
		ExpiresAt: now.Add(downloadTokenTTL),
	}
	return token
}

func lookupDownloadToken(token string) (downloadGrant, bool) {
	downloadTokensMu.Lock()
	defer downloadTokensMu.Unlock()

	g, ok := downloadTokens[token]
	if !ok || g.ExpiresAt.Before(time.Now()) {
		delete(downloadTokens, token)
		return downloadGrant{}, false
	}
	return g, true
}

// consumeDownloadToken removes the grant and reports whether it was still
// there — of two concurrent requests for a single-use grant, one wins.
func consumeDownloadToken(token string) bool {
	downloadTokensMu.Lock()
	defer downloadTokensMu.Unlock()

	_, ok := downloadTokens[token]
	delete(downloadTokens, token)
	return ok
}

func extendDownloadToken(token string, window time.Duration) {
	downloadTokensMu.Lock()
	defer downloadTokensMu.Unlock()

	if g, ok := downloadTokens[token]; ok {
		g.ExpiresAt = time.Now().Add(window)
		downloadTokens[token] = g
	}
}

// POST /api/v1/sync-roots/{rootID}/download-token
// Issues a short-lived token that lets the browser pull the ZIP stream
// natively (without an auth header) via GET /download/{token}.
func (h *FileHandler) MintDownloadToken(w http.ResponseWriter, r *http.Request) {
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

	// Optional JSON body {path}. Missing/empty body means "whole root".
	subtreePath := ""
	if r.ContentLength != 0 && r.Body != nil {
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			writeError(w, http.StatusBadRequest, ErrInvalidPath, "invalid request body")
			return
		}
		if req.Path != "" && req.Path != "/" {
			clean, err := sanitizePath(req.Path)
			if err != nil {
				writeError(w, http.StatusBadRequest, ErrInvalidPath, "invalid path")
				return
			}
			subtreePath = clean
		}
	}

	token := mintDownloadToken(claims.UserID, rootID, subtreePath)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"expires_in": int(downloadTokenTTL.Seconds()),
	})
}

// GET /download/{token}
// Public-Route — keine Auth-Middleware. Validiert den Token; a grant for a
// file is served as that file (Range-capable, ADR-0047), anything else goes
// through the ZIP builder with the same streaming headers as DownloadSyncRoot.
func (h *FileHandler) ServeTokenDownload(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		writeError(w, http.StatusBadRequest, ErrTokenRequired, "token required")
		return
	}
	grant, ok := lookupDownloadToken(token)
	if !ok {
		writeError(w, http.StatusNotFound, ErrTokenInvalid, "token invalid or expired")
		return
	}
	if grant.Path != "" {
		dbFile, err := h.files.FindByPath(r.Context(), grant.RootID, grant.Path)
		if isLiveFile(dbFile, err) {
			// Kept alive so pause/resume works; ServeContent answers the
			// browser's Range + If-Range, and a changed file (new mtime)
			// restarts the transfer instead of splicing two versions.
			extendDownloadToken(token, fileDownloadResumeWindow)
			w.Header().Set("Content-Disposition", contentDispositionAttachment(path.Base(grant.Path)))
			serveFileContent(w, r, h.storage, grant.UserID, grant.RootID.String(), grant.Path, dbFile.MTime)
			return
		}
	}
	if !consumeDownloadToken(token) {
		writeError(w, http.StatusNotFound, ErrTokenInvalid, "token invalid or expired")
		return
	}
	h.streamSyncRootZip(w, r, grant.UserID, grant.RootID, grant.Path)
}
