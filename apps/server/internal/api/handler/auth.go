package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/audit"
	"privasync/server/internal/auth"
	"privasync/server/internal/config"
	"privasync/server/internal/mail"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

// authUserStore is the AuthHandler's view of the user repository — only the
// UserRepository methods the auth endpoints call. Narrow per-handler role
// interfaces (ADR-0029) make the repo-failure (500) branches testable with
// hand-written fakes; the concrete *repository.UserRepository satisfies this
// structurally, so router wiring is unchanged.
type authUserStore interface {
	FindByID(ctx context.Context, id uuid.UUID) (*model.User, error)
	FindByUsername(ctx context.Context, username string) (*model.User, error)
	FindByEmail(ctx context.Context, email string) (*model.User, error)
	RecordLoginAttempt(ctx context.Context, username, ip string, succeeded bool) error
	CountFailedAttempts(ctx context.Context, username string, window time.Duration) (int, error)
	UpdateTOTP(ctx context.Context, userID uuid.UUID, secret *string, enabled bool) error
	UpdateUsername(ctx context.Context, userID uuid.UUID, username string) error
	UpdateEmail(ctx context.Context, userID uuid.UUID, email string) error
	UpdatePassword(ctx context.Context, userID uuid.UUID, hash string) error
	SetAvatarPath(ctx context.Context, userID uuid.UUID, path string) error
	ClearAvatarPath(ctx context.Context, userID uuid.UUID) error
	SetPreferredLanguage(ctx context.Context, userID uuid.UUID, lang string) error
	SetShowFileTree(ctx context.Context, userID uuid.UUID, show bool) error
	SetShowFileCheckboxes(ctx context.Context, userID uuid.UUID, show bool) error
	SetTrashView(ctx context.Context, userID uuid.UUID, view string) error
	ReplaceBackupCodes(ctx context.Context, userID uuid.UUID, plaintextCodes []string) error
	ListBackupCodes(ctx context.Context, userID uuid.UUID) ([]model.BackupCode, error)
	ConsumeBackupCode(ctx context.Context, userID uuid.UUID, plaintext string) error
	CreateRefreshToken(ctx context.Context, userID uuid.UUID, token string, expiresAt time.Time) error
	FindRefreshToken(ctx context.Context, token string) (*model.RefreshToken, error)
	MarkRefreshTokenRotated(ctx context.Context, token string) error
	DeleteRotatedRefreshTokens(ctx context.Context, userID uuid.UUID, grace time.Duration) error
	DeleteRefreshToken(ctx context.Context, token string) error
	DeleteAllRefreshTokens(ctx context.Context, userID uuid.UUID) error
	CreatePasswordResetToken(ctx context.Context, userID uuid.UUID, token string, expiresAt time.Time) error
	FindPasswordResetToken(ctx context.Context, token string) (*model.PasswordResetToken, error)
	MarkPasswordResetTokenUsed(ctx context.Context, id uuid.UUID) error
	CreateEmailChangeToken(ctx context.Context, userID uuid.UUID, newEmail, token string, expiresAt time.Time) error
	ConsumeEmailChangeToken(ctx context.Context, token string) (*model.EmailChangeToken, error)
}

// authAuditStore is the audit-log slice AuthHandler needs.
type authAuditStore interface {
	Log(ctx context.Context, e audit.Event) error
}

// authClientStore records client identifications on login (optional dep).
type authClientStore interface {
	Upsert(ctx context.Context, in repository.UpsertClientInput) (*model.Client, error)
	TouchLastSeen(ctx context.Context, userID uuid.UUID, deviceID string, ip *string) error
}

// authPasskeyStore is the WebAuthn credential slice AuthHandler needs.
type authPasskeyStore interface {
	Create(ctx context.Context, userID uuid.UUID, credentialID, credential []byte, name string) (*model.PasskeyCredential, error)
	ListByUser(ctx context.Context, userID uuid.UUID) ([]model.PasskeyCredential, error)
	FindByCredentialID(ctx context.Context, credentialID []byte) (uuid.UUID, error)
	UpdateSignCount(ctx context.Context, credentialID, credential []byte) error
	Delete(ctx context.Context, id, userID uuid.UUID) error
}

// authAppPasswordStore is the WebDAV-credential slice AuthHandler needs.
type authAppPasswordStore interface {
	Create(ctx context.Context, userID uuid.UUID, name string) (*model.AppPassword, string, error)
	ListByUser(ctx context.Context, userID uuid.UUID) ([]model.AppPassword, error)
	Delete(ctx context.Context, id, userID uuid.UUID) error
}

// authStorage is the avatar-file slice AuthHandler needs from storage.Service.
type authStorage interface {
	WriteAvatar(userID uuid.UUID, png []byte) error
	DeleteAvatar(userID uuid.UUID) error
	AvatarPath(userID uuid.UUID) string
}

type AuthHandler struct {
	users        authUserStore
	audits       authAuditStore
	stor         authStorage
	mailer       *mail.Mailer
	clients      authClientStore
	passkeys     authPasskeyStore
	appPasswords authAppPasswordStore
	webAuthn *webauthn.WebAuthn
	cfg      config.AuthConfig
	baseURL  string
	issuer   string
}

func NewAuthHandler(users authUserStore, audits authAuditStore, stor authStorage, mailer *mail.Mailer, cfg config.AuthConfig, baseURL, issuer string) *AuthHandler {
	w, err := auth.NewWebAuthn(baseURL, issuer)
	if err != nil {
		// baseURL is a startup config value (config.ServerConfig.BaseURL) —
		// an invalid value here is a deploy-time misconfiguration, not a
		// runtime condition, so fail fast like config.Load's own validation.
		panic(fmt.Errorf("webauthn config: %w", err))
	}
	return &AuthHandler{users: users, audits: audits, stor: stor, mailer: mailer, webAuthn: w, cfg: cfg, baseURL: baseURL, issuer: issuer}
}

// WithClients wires the client repository so successful logins record an
// identification row for the admin overview. Optional — when nil, login skips
// the upsert (e.g. older clients that send no identification).
func (h *AuthHandler) WithClients(c authClientStore) *AuthHandler {
	h.clients = c
	return h
}

// WithPasskeys wires the passkey credential repository. Required for the
// passkey endpoints to function — NewAuthHandler leaves it nil so router
// wiring stays explicit, same as WithClients.
func (h *AuthHandler) WithPasskeys(p authPasskeyStore) *AuthHandler {
	h.passkeys = p
	return h
}

// WithAppPasswords wires the WebDAV-credential repository. Required for the
// app-password endpoints, same wiring convention as WithPasskeys.
func (h *AuthHandler) WithAppPasswords(a authAppPasswordStore) *AuthHandler {
	h.appPasswords = a
	return h
}

// clientPayload mirrors the JSON the desktop/mobile client sends on login and
// 2FA verify. All fields except device_id are optional but the admin overview
// is only useful when populated.
type clientPayload struct {
	DeviceID      string `json:"device_id"`
	DeviceName    string `json:"device_name"`
	Platform      string `json:"platform"`
	OSVersion     string `json:"os_version"`
	ClientVersion string `json:"client_version"`
}

func (p *clientPayload) valid() bool {
	if p == nil {
		return false
	}
	switch p.Platform {
	case "windows", "macos", "linux", "mobile":
	default:
		return false
	}
	return p.DeviceID != ""
}

// upsertClient persists the caller's identification. Errors are logged but
// never block the auth flow — the admin overview is a side-channel, not a
// gate on login.
func (h *AuthHandler) upsertClient(r *http.Request, userID uuid.UUID, p *clientPayload, ip string) {
	if h.clients == nil || !p.valid() {
		return
	}
	in := repository.UpsertClientInput{
		UserID:   userID,
		DeviceID: p.DeviceID,
		Name:     p.DeviceName,
		Platform: p.Platform,
	}
	if p.OSVersion != "" {
		in.OSVersion = &p.OSVersion
	}
	if p.ClientVersion != "" {
		in.ClientVersion = &p.ClientVersion
	}
	if ip != "" {
		in.LastIP = &ip
	}
	if _, err := h.clients.Upsert(r.Context(), in); err != nil {
		slog.Warn("client upsert failed", "user_id", userID, "device_id", p.DeviceID, "error", err)
	}
}

func (h *AuthHandler) issueTokenPair(w http.ResponseWriter, r *http.Request, userID uuid.UUID, role model.Role) {
	rawRefresh := make([]byte, 32)
	if _, err := rand.Read(rawRefresh); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	refreshToken := base64.URLEncoding.EncodeToString(rawRefresh)

	expiresAt := time.Now().Add(h.cfg.RefreshTokenTTL)
	if err := h.users.CreateRefreshToken(r.Context(), userID, refreshToken, expiresAt); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure -- Secure is proxy-aware via secureCookie() (https baseURL forces it on behind a TLS-terminating proxy); intentionally not a literal true so plain-HTTP dev still receives the cookie
	http.SetCookie(w, &http.Cookie{ //#nosec G124 -- Secure is set by secureCookie (https-baseURL / proxy-aware); deliberately dynamic so dev over plain HTTP still works
		Name:     "refresh_token",
		Value:    refreshToken,
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   h.secureCookie(r),
		SameSite: http.SameSiteStrictMode,
		Path:     "/api/v1/auth",
	})

	h.issueAccessToken(w, r, userID, role)
}

// issueAccessToken writes the access token and the stream cookie, leaving the
// refresh token untouched. Used on its own by a refresh inside the rotation
// grace window, where rotating a second time is exactly what must not happen.
func (h *AuthHandler) issueAccessToken(w http.ResponseWriter, r *http.Request, userID uuid.UUID, role model.Role) {
	accessToken, err := auth.IssueAccessToken(h.cfg.JWTSecret, h.cfg.AccessTokenTTL, userID, role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	// Stream credential for the browser: EventSource cannot send an
	// Authorization header, and putting the access token in the URL would
	// leak it into request logs and history. Scoped to the stream
	// path and to its own token type, and re-issued on every refresh so it
	// never outlives the session.
	sseToken, err := auth.IssueSSEToken(h.cfg.JWTSecret, h.cfg.AccessTokenTTL, userID, role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	http.SetCookie(w, h.sseCookie(r, sseToken))

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": accessToken,
		"expires_in":   int(h.cfg.AccessTokenTTL.Seconds()),
		"requires_2fa": false,
	})
}

// sseCookie builds the event-stream cookie. An empty value yields an expired
// cookie, which is how Logout clears it.
func (h *AuthHandler) sseCookie(r *http.Request, value string) *http.Cookie {
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure -- Secure is proxy-aware via secureCookie() (https baseURL forces it on behind a TLS-terminating proxy); intentionally not a literal true so plain-HTTP dev still receives the cookie
	c := &http.Cookie{ //#nosec G124 -- Secure is set by secureCookie (https-baseURL / proxy-aware); deliberately dynamic so dev over plain HTTP still works
		Name:     middleware.SSECookieName,
		Value:    value,
		HttpOnly: true,
		Secure:   h.secureCookie(r),
		SameSite: http.SameSiteStrictMode,
		// Scoped to the stream: the cookie is sent automatically, so it must
		// not ride along on every other API call.
		Path: "/api/v1/events",
	}
	if value == "" {
		c.MaxAge = -1
	} else {
		c.Expires = time.Now().Add(h.cfg.AccessTokenTTL)
	}
	return c
}

// secureCookie decides the cookie Secure flag. Behind a TLS-terminating
// reverse proxy r.TLS is nil even though the browser connection is HTTPS, so
// an https baseURL forces Secure on; a direct-TLS listener still works via
// r.TLS. Plain-HTTP dev (http baseURL, no TLS) stays false so the browser
// keeps sending the cookie.
func (h *AuthHandler) secureCookie(r *http.Request) bool {
	return r.TLS != nil || strings.HasPrefix(h.baseURL, "https://")
}
