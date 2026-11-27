package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/audit"
	"privasync/server/internal/repository"
)

// PUT /api/v1/auth/language — setzt die bevorzugte UI- und Mail-Sprache.
func (h *AuthHandler) SetLanguage(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	var req struct {
		Language string `json:"language"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}
	if req.Language != "de" && req.Language != "en" {
		writeError(w, http.StatusBadRequest, ErrUnsupportedOp, "unsupported language")
		return
	}
	if err := h.users.SetPreferredLanguage(r.Context(), claims.UserID, req.Language); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PUT /api/v1/auth/preferences/file-tree — toggles the file-tree sidebar
// in the web frontend. Persisted per user so the setting holds
// across devices and sessions.
func (h *AuthHandler) SetShowFileTree(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	var req struct {
		Show bool `json:"show"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}
	if err := h.users.SetShowFileTree(r.Context(), claims.UserID, req.Show); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PUT /api/v1/auth/preferences/file-checkboxes — toggles the selection
// checkbox column / overlay in the web file browser. Persisted per user.
func (h *AuthHandler) SetShowFileCheckboxes(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	var req struct {
		Show bool `json:"show"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}
	if err := h.users.SetShowFileCheckboxes(r.Context(), claims.UserID, req.Show); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PUT /api/v1/auth/preferences/trash-view — persists the preferred trash
// view ('list' | 'tree'). Per user so the mode applies across sessions and
// devices.
func (h *AuthHandler) SetTrashView(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	var req struct {
		View string `json:"view"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}
	if req.View != "list" && req.View != "tree" {
		writeError(w, http.StatusBadRequest, ErrUnsupportedOp, "unsupported trash view")
		return
	}
	if err := h.users.SetTrashView(r.Context(), claims.UserID, req.View); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PUT /api/v1/auth/profile/username — der eingeloggte Nutzer setzt seinen
// Benutzernamen neu. Bei Konflikt mit einem bestehenden Konto liefert die
// Funktion 409 — Sessions bleiben aktiv.
func (h *AuthHandler) SetUsername(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		writeError(w, http.StatusBadRequest, ErrRegistrationFields, "username required")
		return
	}
	err := h.users.UpdateUsername(r.Context(), claims.UserID, username)
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, ErrUsernameTaken, "username already taken")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &claims.UserID, audit.EventUsernameChanged, map[string]any{
		"username": username,
	}))
	w.WriteHeader(http.StatusNoContent)
}
