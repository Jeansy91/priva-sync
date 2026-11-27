package handler

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/audit"
	"privasync/server/internal/repository"
)

// emailChangeTokenTTL bounds the validity of a confirm-link.
const emailChangeTokenTTL = 24 * time.Hour

// looksLikeEmail ist die minimale Plausibilitaetspruefung: lokaler Teil +
// '@' + Domain mit Punkt. Strenge Validierung waere RFC-5322-Theater —
// die echte Pruefung ist der Confirm-Link an die Adresse.
func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at < 1 || at == len(s)-1 {
		return false
	}
	local := s[:at]
	domain := s[at+1:]
	if strings.ContainsAny(local, " \t\r\n") || strings.ContainsAny(domain, " \t\r\n") {
		return false
	}
	return strings.Contains(domain, ".")
}

// POST /api/v1/auth/profile/email-change — Stoesst einen Email-Wechsel an,
// indem ein Bestaetigungslink an die *neue* Adresse versendet wird. Die
// users.email-Spalte bleibt unveraendert, bis der Nutzer den Link aufruft.
func (h *AuthHandler) RequestEmailChange(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	var req struct {
		NewEmail string `json:"new_email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}
	newEmail := strings.TrimSpace(req.NewEmail)
	if !looksLikeEmail(newEmail) {
		writeError(w, http.StatusBadRequest, ErrEmailInvalid, "invalid email address")
		return
	}

	// Konflikt-Check: existierender Nutzer mit dieser Mail (case-insensitive).
	if existing, err := h.users.FindByEmail(r.Context(), newEmail); err == nil && existing != nil {
		writeError(w, http.StatusConflict, ErrEmailTaken, "email already in use")
		return
	} else if err != nil && !errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	user, err := h.users.FindByID(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrUserNotFound, "user not found")
		return
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	token := base64.URLEncoding.EncodeToString(raw)
	if err := h.users.CreateEmailChangeToken(r.Context(), claims.UserID, newEmail, token, time.Now().Add(emailChangeTokenTTL)); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &claims.UserID, audit.EventEmailChangeRequested, map[string]any{
		"new_email": newEmail,
	}))
	sendEmailChangeRequestMail(h.mailer, h.baseURL, user, newEmail, token)

	w.WriteHeader(http.StatusAccepted)
}

// POST /api/v1/auth/profile/email-change/confirm — verifies the token and
// adopts the new address. The token is consumed atomically; replay fails
// with 400.
func (h *AuthHandler) ConfirmEmailChange(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, ErrTokenRequired, "token required")
		return
	}

	tok, err := h.users.ConsumeEmailChangeToken(r.Context(), req.Token)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusBadRequest, ErrTokenInvalid, "invalid or expired token")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	// Zwischen Token-Erstellung und Confirm haette jemand anderes die Adresse
	// belegen koennen — Unique-Constraint klaert das endgueltig.
	user, _ := h.users.FindByID(r.Context(), claims.UserID)
	oldEmail := ""
	if user != nil {
		oldEmail = user.Email
	}
	if err := h.users.UpdateEmail(r.Context(), tok.UserID, tok.NewEmail); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			writeError(w, http.StatusConflict, ErrEmailTaken, "email already in use")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &tok.UserID, audit.EventEmailChanged, map[string]any{
		"old_email": oldEmail,
		"new_email": tok.NewEmail,
	}))
	if user != nil {
		sendEmailChangedMail(h.mailer, h.baseURL, user, oldEmail, tok.NewEmail)
	}

	w.WriteHeader(http.StatusNoContent)
}
