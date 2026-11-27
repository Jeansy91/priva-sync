package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/repository"
)

// appPasswordNameMaxLen bounds the label the owner picks. It only ever has to
// distinguish one device from another in a list.
const appPasswordNameMaxLen = 64

// POST /api/v1/auth/app-passwords
//
// Mints a WebDAV credential and returns its plaintext exactly once. The
// account password is not a WebDAV credential (ADR-0040), so this is how a
// file manager or rclone gets access.
func (h *AuthHandler) CreateAppPassword(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > appPasswordNameMaxLen {
		writeError(w, http.StatusBadRequest, ErrInvalidAppPasswordName, "name required")
		return
	}

	ap, secret, err := h.appPasswords.Create(r.Context(), claims.UserID, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         ap.ID,
		"name":       ap.Name,
		"created_at": ap.CreatedAt,
		// Shown once — the server keeps only a hash and cannot repeat it.
		"secret": secret,
	})
}

// GET /api/v1/auth/app-passwords
func (h *AuthHandler) ListAppPasswords(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	list, err := h.appPasswords.ListByUser(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, ap := range list {
		out = append(out, map[string]any{
			"id":           ap.ID,
			"name":         ap.Name,
			"created_at":   ap.CreatedAt,
			"last_used_at": ap.LastUsedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"app_passwords": out})
}

// DELETE /api/v1/auth/app-passwords/{appPasswordID}
func (h *AuthHandler) DeleteAppPassword(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "appPasswordID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidAppPasswordID, "invalid app password id")
		return
	}
	// Delete is scoped to the caller, so a stranger holding the id gets the
	// same answer as someone naming one that never existed.
	if err := h.appPasswords.Delete(r.Context(), id, claims.UserID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrAppPasswordNotFound, "app password not found")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
