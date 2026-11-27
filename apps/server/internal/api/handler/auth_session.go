package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/audit"
	"privasync/server/internal/auth"
	"privasync/server/internal/httpx"
	"privasync/server/internal/repository"
)

// POST /api/v1/auth/login
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string         `json:"username"`
		Password string         `json:"password"`
		Client   *clientPayload `json:"client"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, ErrMissingCredentials, "username and password required")
		return
	}

	ip := httpx.ClientIP(r)

	if !h.allowAttempt(w, r, req.Username) {
		return
	}

	user, err := h.users.FindByUsername(r.Context(), req.Username)
	if errors.Is(err, repository.ErrNotFound) {
		// Constant-cost dummy verify so a missing user cannot be told apart from
		// a wrong password by response timing.
		_ = auth.VerifyPassword(req.Password, "$argon2id$v=19$m=65536,t=2,p=4$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
		if !h.recordFailedAttempt(w, r, req.Username, ip, nil, "unknown_user") {
			return
		}
		writeError(w, http.StatusUnauthorized, ErrInvalidCredentials, "invalid credentials")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	if !user.IsActive {
		writeError(w, http.StatusForbidden, ErrAccountDisabled, "account disabled")
		return
	}

	if err := auth.VerifyPassword(req.Password, user.PasswordHash); err != nil {
		if !h.recordFailedAttempt(w, r, req.Username, ip, &user.ID, "wrong_password") {
			return
		}
		writeError(w, http.StatusUnauthorized, ErrInvalidCredentials, "invalid credentials")
		return
	}

	_ = h.users.RecordLoginAttempt(r.Context(), req.Username, ip, true)
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &user.ID, audit.EventLoginSuccess, map[string]any{
		"username":     req.Username,
		"requires_2fa": user.TOTPEnabled,
	}))

	// 2FA erforderlich?
	if user.TOTPEnabled {
		sessionToken, err := auth.Issue2FASessionToken(h.cfg.JWTSecret, h.cfg.TwoFASessionTTL, user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"requires_2fa":  true,
			"session_token": sessionToken,
		})
		return
	}

	h.upsertClient(r, user.ID, req.Client, ip)
	h.issueTokenPair(w, r, user.ID, user.Role)
}

// allowAttempt reports whether username may make another credential attempt.
// On false it has already written the response: 429 once the failed-attempt
// budget is spent, 500 when the count could not be read at all. The latter is
// fatal on purpose -- login_attempts is the only brake on guessing, so
// continuing without a usable count would serve every further attempt
// unmetered.
func (h *AuthHandler) allowAttempt(w http.ResponseWriter, r *http.Request, username string) bool {
	failedCount, err := h.users.CountFailedAttempts(r.Context(), username, h.cfg.LoginLockoutWindow)
	if err != nil {
		slog.Error("reading failed-attempt count", "username", username, "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return false
	}
	if failedCount < h.cfg.MaxLoginAttempts {
		return true
	}
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, nil, audit.EventLoginLockout, map[string]any{
		"username":     username,
		"failed_count": failedCount,
	}))
	writeError(w, http.StatusTooManyRequests, ErrTooManyAttempts, "too many failed login attempts")
	return false
}

// recordFailedAttempt books one failed login against the lockout counter and
// writes the matching audit event. It reports whether the caller may continue;
// on a bookkeeping failure it has already written a 500, because a guess that
// the counter never saw is a guess the lockout will never stop.
func (h *AuthHandler) recordFailedAttempt(
	w http.ResponseWriter,
	r *http.Request,
	username, ip string,
	userID *uuid.UUID,
	reason string,
) bool {
	if err := h.users.RecordLoginAttempt(r.Context(), username, ip, false); err != nil {
		slog.Error("recording failed login attempt", "username", username, "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return false
	}
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, userID, audit.EventLoginFail, map[string]any{
		"username": username,
		"reason":   reason,
	}))
	return true
}

// POST /api/v1/auth/2fa
func (h *AuthHandler) Verify2FA(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionToken string         `json:"session_token"`
		TOTPCode     string         `json:"totp_code"`
		BackupCode   string         `json:"backup_code"`
		Client       *clientPayload `json:"client"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}

	claims, err := auth.ParseToken(h.cfg.JWTSecret, req.SessionToken)
	if err != nil || claims.TokenType != auth.TokenType2FASession {
		writeError(w, http.StatusUnauthorized, ErrInvalidSessionToken, "invalid session token")
		return
	}

	user, err := h.users.FindByID(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, ErrInvalidSessionToken, "invalid session token")
		return
	}

	if !user.TOTPEnabled || user.TOTPSecret == nil {
		writeError(w, http.StatusBadRequest, ErrTotpNotEnabled, "2fa not enabled")
		return
	}

	// Second factor, same brake as the first. A caller who already has the
	// password can otherwise grind six-digit codes for the whole life of the
	// session token, and with skew 1 three of them are valid at any moment
	//. Failures share the password path's counter, so guessing here
	// locks the account exactly as guessing there does.
	if !h.allowAttempt(w, r, user.Username) {
		return
	}

	ip := httpx.ClientIP(r)
	switch {
	case req.TOTPCode != "":
		if !auth.ValidateTOTPCode(*user.TOTPSecret, req.TOTPCode) {
			if !h.recordFailedAttempt(w, r, user.Username, ip, &user.ID, "wrong_totp") {
				return
			}
			writeError(w, http.StatusUnauthorized, ErrInvalidTotpCode, "invalid totp code")
			return
		}
	case req.BackupCode != "":
		if err := h.users.ConsumeBackupCode(r.Context(), user.ID, normalizeBackupCode(req.BackupCode)); err != nil {
			if !h.recordFailedAttempt(w, r, user.Username, ip, &user.ID, "wrong_backup_code") {
				return
			}
			writeError(w, http.StatusUnauthorized, ErrInvalidBackupCode, "invalid backup code")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, ErrTotpCodeRequired, "totp_code or backup_code required")
		return
	}

	_ = h.users.RecordLoginAttempt(r.Context(), user.Username, ip, true)
	h.upsertClient(r, user.ID, req.Client, ip)
	h.issueTokenPair(w, r, user.ID, user.Role)
}

// POST /api/v1/auth/refresh
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("refresh_token")
	if err != nil {
		writeError(w, http.StatusUnauthorized, ErrNoRefreshToken, "no refresh token")
		return
	}

	tokenRecord, err := h.users.FindRefreshToken(r.Context(), cookie.Value)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, ErrInvalidRefreshToken, "invalid refresh token")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	if time.Now().After(tokenRecord.ExpiresAt) {
		_ = h.users.DeleteRefreshToken(r.Context(), cookie.Value)
		writeError(w, http.StatusUnauthorized, ErrRefreshTokenExpired, "refresh token expired")
		return
	}
	if tokenRecord.RotatedAt != nil && time.Since(*tokenRecord.RotatedAt) > h.cfg.RefreshRotationGrace {
		_ = h.users.DeleteRefreshToken(r.Context(), cookie.Value)
		writeError(w, http.StatusUnauthorized, ErrInvalidRefreshToken, "invalid refresh token")
		return
	}

	user, err := h.users.FindByID(r.Context(), tokenRecord.UserID)
	if err != nil || !user.IsActive {
		writeError(w, http.StatusUnauthorized, ErrInvalidRefreshToken, "invalid refresh token")
		return
	}

	// Still inside the grace window: this is a second refresh from a client
	// whose requests overlapped, not a new session. Hand out an access token
	// and leave the rotation the first call already performed alone --
	// rotating again would invalidate the cookie that client just received
	// (ADR-0046).
	if tokenRecord.RotatedAt != nil {
		h.issueAccessToken(w, r, user.ID, user.Role)
		return
	}

	// Token rotation: retire the old refresh token, issue a fresh one. The
	// retired row survives its grace window, then the sweep collects it.
	if err := h.users.MarkRefreshTokenRotated(r.Context(), cookie.Value); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	if err := h.users.DeleteRotatedRefreshTokens(r.Context(), user.ID, h.cfg.RefreshRotationGrace); err != nil {
		slog.Warn("sweeping rotated refresh tokens failed", "user_id", user.ID, "error", err)
	}
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &user.ID, audit.EventTokenRefresh, nil))

	// Refresh doubles as the client registry's heartbeat: it keeps "Letzter
	// Zugriff" tracking ongoing sync activity rather than the moment of login.
	// A client that sends its full identification is upserted -- that is what
	// makes an in-place app update's new version land without a re-login, and
	// what lets a session older than the registry get a row at all; one that
	// only sends the device header gets the cheaper last_seen touch. The body
	// is read here, after the token proved good, so an unauthenticated caller
	// never gets us to parse one.
	if h.clients != nil {
		var body struct {
			Client *clientPayload `json:"client"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		ip := httpx.ClientIP(r)
		switch deviceID := r.Header.Get("X-Client-Device"); {
		case body.Client.valid():
			h.upsertClient(r, user.ID, body.Client, ip)
		case deviceID != "":
			var ipPtr *string
			if ip != "" {
				ipPtr = &ip
			}
			if err := h.clients.TouchLastSeen(r.Context(), user.ID, deviceID, ipPtr); err != nil && !errors.Is(err, repository.ErrNotFound) {
				slog.Warn("client touch failed", "user_id", user.ID, "device_id", deviceID, "error", err)
			}
		}
	}

	h.issueTokenPair(w, r, user.ID, user.Role)
}

// POST /api/v1/auth/logout
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("refresh_token"); err == nil {
		if tok, err := h.users.FindRefreshToken(r.Context(), cookie.Value); err == nil {
			_ = h.audits.Log(r.Context(), audit.FromRequest(r, &tok.UserID, audit.EventLogout, nil))
		}
		_ = h.users.DeleteRefreshToken(r.Context(), cookie.Value)
	}
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure -- Secure is proxy-aware via secureCookie() (https baseURL forces it on behind a TLS-terminating proxy); intentionally not a literal true so plain-HTTP dev still receives the cookie
	http.SetCookie(w, &http.Cookie{ //#nosec G124 -- Secure is set by secureCookie (https-baseURL / proxy-aware); deliberately dynamic so dev over plain HTTP still works
		Name:     "refresh_token",
		Value:    "",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secureCookie(r),
		SameSite: http.SameSiteStrictMode,
		Path:     "/api/v1/auth",
	})
	http.SetCookie(w, h.sseCookie(r, ""))
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/auth/me
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	user, err := h.users.FindByID(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrUserNotFound, "user not found")
		return
	}
	var avatarURL *string
	if user.AvatarPath != nil {
		u := "/api/v1/auth/avatar/" + user.ID.String()
		avatarURL = &u
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":                   user.ID,
		"username":             user.Username,
		"email":                user.Email,
		"role":                 user.Role,
		"totp_enabled":         user.TOTPEnabled,
		"quota_bytes":          user.QuotaBytes,
		"used_bytes":           user.UsedBytes,
		"trash_bytes":          user.TrashBytes,
		"avatar_url":           avatarURL,
		"preferred_language":   user.PreferredLanguage,
		"show_file_tree":       user.ShowFileTree,
		"show_file_checkboxes": user.ShowFileCheckboxes,
		"trash_view":           user.TrashView,
	})
}
