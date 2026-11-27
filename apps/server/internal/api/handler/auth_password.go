package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/audit"
	"privasync/server/internal/auth"
	"privasync/server/internal/mail"
	"privasync/server/internal/repository"
)

// passwordResetTokenTTL bounds how long a reset link remains valid. Kept
// short so a leaked mail is not a long-lived credential.
const passwordResetTokenTTL = 1 * time.Hour

// PUT /api/v1/auth/password
func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, ErrPasswordResetMissing, "current_password and new_password required")
		return
	}
	if len(req.NewPassword) < 12 {
		writeError(w, http.StatusBadRequest, ErrPasswordTooShortNew, "new password must be at least 12 characters")
		return
	}

	user, err := h.users.FindByID(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrUserNotFound, "user not found")
		return
	}

	if err := auth.VerifyPassword(req.CurrentPassword, user.PasswordHash); err != nil {
		writeError(w, http.StatusUnauthorized, ErrCurrentPasswordWrong, "current password is incorrect")
		return
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	if err := h.users.UpdatePassword(r.Context(), claims.UserID, newHash); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &claims.UserID, audit.EventPasswordChanged, nil))

	// Invalidate all existing refresh tokens so other sessions are logged out
	_ = h.users.DeleteAllRefreshTokens(r.Context(), claims.UserID)

	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/auth/password/reset-request
//
// Rate-limited upstream via loginRL. Always returns 200 so a caller cannot
// probe email existence.
func (h *AuthHandler) RequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		// Silent success to avoid leaking validation state via error codes.
		w.WriteHeader(http.StatusOK)
		return
	}

	user, err := h.users.FindByEmail(r.Context(), req.Email)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			slog.Warn("password reset: user lookup failed", "error", err)
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	if !user.IsActive {
		// Same response as happy path — the account is just inert.
		w.WriteHeader(http.StatusOK)
		return
	}

	token, err := newSecureToken()
	if err != nil {
		slog.Error("password reset: token generation failed", "error", err)
		w.WriteHeader(http.StatusOK)
		return
	}
	expiresAt := time.Now().Add(passwordResetTokenTTL)

	if err := h.users.CreatePasswordResetToken(r.Context(), user.ID, token, expiresAt); err != nil {
		slog.Error("password reset: token insert failed", "user_id", user.ID, "error", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	resetURL := h.baseURL + "/password-reset?token=" + token
	expiresIn := "1 hour"
	if user.PreferredLanguage == mail.LangDE {
		expiresIn = "1 Stunde"
	}
	if err := h.mailer.SendTemplate(user.Email, user.PreferredLanguage, mail.TplPasswordReset, mail.PasswordResetData{
		Common: mail.Common{
			BaseURL:  h.baseURL,
			Username: user.Username,
			OptIn:    false, // transactional — always send
		},
		ResetURL:  resetURL,
		ExpiresIn: expiresIn,
	}); err != nil {
		slog.Warn("password reset: mail enqueue failed", "user_id", user.ID, "error", err)
	}

	w.WriteHeader(http.StatusOK)
}

// POST /api/v1/auth/password/reset-confirm
func (h *AuthHandler) ConfirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}
	if req.Token == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, ErrPasswordResetMissing, "token and new_password required")
		return
	}
	if len(req.NewPassword) < 12 {
		writeError(w, http.StatusBadRequest, ErrPasswordTooShortNew, "new password must be at least 12 characters")
		return
	}

	// Lookup via sha256-hash of the submitted token. The DB b-tree hit is
	// effectively constant-time because the hash has a fixed length; any
	// plaintext-token inequality is hidden before the DB ever sees it.
	tok, err := h.users.FindPasswordResetToken(r.Context(), req.Token)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusBadRequest, ErrTokenInvalid, "invalid or expired token")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	if tok.UsedAt != nil || time.Now().After(tok.ExpiresAt) {
		writeError(w, http.StatusBadRequest, ErrTokenInvalid, "invalid or expired token")
		return
	}

	user, err := h.users.FindByID(r.Context(), tok.UserID)
	if err != nil || !user.IsActive {
		writeError(w, http.StatusBadRequest, ErrTokenInvalid, "invalid or expired token")
		return
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	if err := h.users.UpdatePassword(r.Context(), user.ID, newHash); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &user.ID, audit.EventPasswordReset, nil))
	if err := h.users.MarkPasswordResetTokenUsed(r.Context(), tok.ID); err != nil {
		slog.Warn("password reset: mark used failed", "token_id", tok.ID, "error", err)
	}
	// Invalidate every existing session — forces other devices to re-login.
	_ = h.users.DeleteAllRefreshTokens(r.Context(), user.ID)

	w.WriteHeader(http.StatusNoContent)
}
