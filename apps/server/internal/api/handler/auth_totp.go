package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/audit"
	"privasync/server/internal/auth"
	"privasync/server/internal/httpx"
)

const backupCodeCount = 10

// POST /api/v1/auth/totp/setup
func (h *AuthHandler) SetupTOTP(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	user, err := h.users.FindByID(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrUserNotFound, "user not found")
		return
	}

	secret, otpauthURL, err := auth.GenerateTOTPSecret(h.issuer, user.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	if err := h.users.UpdateTOTP(r.Context(), user.ID, &secret, false); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"secret":      secret,
		"otpauth_url": otpauthURL,
	})
}

// POST /api/v1/auth/totp/confirm
//
// On success returns the initial set of backup codes in the response body.
// This is the only moment plaintext codes flow over the API — the DB only
// stores SHA-256 hashes.
func (h *AuthHandler) ConfirmTOTP(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}

	user, err := h.users.FindByID(r.Context(), claims.UserID)
	if err != nil || user.TOTPSecret == nil {
		writeError(w, http.StatusBadRequest, ErrTotpNotSetUp, "totp not set up")
		return
	}

	if !auth.ValidateTOTPCode(*user.TOTPSecret, req.Code) {
		writeError(w, http.StatusUnauthorized, ErrInvalidTotpCode, "invalid totp code")
		return
	}

	if err := h.users.UpdateTOTP(r.Context(), user.ID, user.TOTPSecret, true); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &user.ID, audit.Event2FAEnabled, nil))

	codes, err := generateBackupCodes(backupCodeCount)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	if err := h.users.ReplaceBackupCodes(r.Context(), user.ID, codes); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"backup_codes": codes,
	})
}

// POST /api/v1/auth/totp/backup-codes/regenerate
func (h *AuthHandler) RegenerateBackupCodes(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())

	user, err := h.users.FindByID(r.Context(), claims.UserID)
	if err != nil || !user.TOTPEnabled {
		writeError(w, http.StatusBadRequest, ErrTotpNotEnabled, "2fa not enabled")
		return
	}

	codes, err := generateBackupCodes(backupCodeCount)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	if err := h.users.ReplaceBackupCodes(r.Context(), user.ID, codes); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"backup_codes": codes,
	})
}

// GET /api/v1/auth/totp/backup-codes
//
// Listet Metadaten (used_at, created_at) ohne Klartext — zur UI-Anzeige, wie
// viele Codes noch ungenutzt sind.
func (h *AuthHandler) ListBackupCodes(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	codes, err := h.users.ListBackupCodes(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	unused := 0
	for _, c := range codes {
		if c.UsedAt == nil {
			unused++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total":  len(codes),
		"unused": unused,
		"codes":  codes,
	})
}

// DELETE /api/v1/auth/totp
func (h *AuthHandler) DisableTOTP(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if err := h.users.UpdateTOTP(r.Context(), claims.UserID, nil, false); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &claims.UserID, audit.Event2FADisabled, nil))
	sendTOTPDisabledMail(
		r.Context(), h.users, h.mailer, h.baseURL, claims.UserID,
		time.Now().UTC().Format("2006-01-02 15:04 UTC"),
		httpx.ClientIP(r),
	)
	w.WriteHeader(http.StatusNoContent)
}

// generateBackupCodes produces n 16-digit hex codes (64 bits of entropy)
// formatted as XXXX-XXXX-XXXX-XXXX for readability. Plaintext is returned
// exactly once — only the hash remains in the DB afterwards.
func generateBackupCodes(n int) ([]string, error) {
	codes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		raw := make([]byte, 8)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		h := hex.EncodeToString(raw) // 16 chars
		codes = append(codes, fmt.Sprintf("%s-%s-%s-%s", h[0:4], h[4:8], h[8:12], h[12:16]))
	}
	return codes, nil
}

// normalizeBackupCode strips whitespace + dashes and lower-cases, damit
// Copy/Paste mit/ohne Trenner, Gross-/Klein, Leerzeichen gleichwertig sind.
func normalizeBackupCode(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, "-", "")
	code = strings.ReplaceAll(code, " ", "")
	// Re-formatieren in die kanonische Form, damit der Hash zum gespeicherten passt.
	if len(code) != 16 {
		return code
	}
	return fmt.Sprintf("%s-%s-%s-%s", code[0:4], code[4:8], code[8:12], code[12:16])
}
