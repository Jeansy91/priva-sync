package webdav

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	gwebdav "golang.org/x/net/webdav"

	"privasync/server/internal/auth"
	"privasync/server/internal/config"
	"privasync/server/internal/repository"
	"privasync/server/internal/storage"
	syncpkg "privasync/server/internal/sync"
)

var errUnauthorized = errors.New("unauthorized")

// Handler serves a user's files over WebDAV (RFC 4918) on the dedicated
// WebDAV port. Unlike a naive filesystem mount, every read is projected from
// the `files` repository and every mutation flows through the Sync
// Coordinator, so WebDAV writes are first-class mutations: quota-gated,
// visible in the web UI, and accompanied by SSE/conflict/last-modifier
// side-effects (see ADR-0018).
type Handler struct {
	users        *repository.UserRepository
	appPasswords *repository.AppPasswordRepository
	syncRoots    *repository.SyncRootRepository
	files        *repository.FileRepository
	stor         *storage.Service
	coord        syncpkg.Coordinator
	authCfg      config.AuthConfig
	locks        gwebdav.LockSystem
}

func NewHandler(
	users *repository.UserRepository,
	appPasswords *repository.AppPasswordRepository,
	syncRoots *repository.SyncRootRepository,
	files *repository.FileRepository,
	stor *storage.Service,
	coord syncpkg.Coordinator,
	authCfg config.AuthConfig,
) *Handler {
	return &Handler{
		users:        users,
		appPasswords: appPasswords,
		syncRoots:    syncRoots,
		files:        files,
		stor:         stor,
		coord:        coord,
		authCfg:      authCfg,
		locks:        gwebdav.NewMemLS(),
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	userID, err := h.authenticate(r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="PrivaSync WebDAV"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	fsys := h.newFS(userID)
	if !fsys.checkPrecondition(w, r) {
		return
	}

	dav := &gwebdav.Handler{
		Prefix:     "/",
		FileSystem: fsys,
		LockSystem: h.locks,
	}
	dav.ServeHTTP(w, r)
}

// authenticate checks Basic Auth or Bearer token and returns the user ID.
func (h *Handler) authenticate(r *http.Request) (string, error) {
	// Bearer token (desktop client).
	if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
		token := authHeader[7:]
		claims, err := auth.ParseToken(h.authCfg.JWTSecret, token)
		if err != nil || claims.TokenType != auth.TokenTypeAccess {
			return "", errUnauthorized
		}
		user, err := h.users.FindByID(r.Context(), claims.UserID)
		if err != nil || !user.IsActive {
			return "", errUnauthorized
		}
		return user.ID.String(), nil
	}

	// Basic Auth (rclone, file managers). The credential is an app password,
	// never the account password: Basic Auth cannot ask for a second factor,
	// so accepting the account password here would make TOTP decoration --
	// the account would be fully reachable on this port with the password
	// alone (ADR-0040).
	username, secret, ok := r.BasicAuth()
	if !ok {
		return "", errUnauthorized
	}

	// The secret identifies its own credential, so this is one indexed lookup
	// rather than a hash comparison per stored credential.
	ap, err := h.appPasswords.FindBySecret(r.Context(), secret)
	if err != nil {
		return "", errUnauthorized
	}
	user, err := h.users.FindByID(r.Context(), ap.UserID)
	if err != nil || !user.IsActive {
		return "", errUnauthorized
	}
	// The presented name has to be the owner's. Not a security boundary --
	// the secret already is one -- but it stops a mistyped account from
	// silently working and writing into someone else's tree.
	if !strings.EqualFold(username, user.Username) {
		return "", errUnauthorized
	}

	if err := h.appPasswords.TouchLastUsed(r.Context(), ap.ID); err != nil {
		slog.Warn("webdav: recording app-password use failed", "app_password_id", ap.ID, "error", err)
	}
	return user.ID.String(), nil
}
