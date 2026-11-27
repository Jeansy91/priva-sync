package handler

import (
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/avatar"
)

// avatarMaxBytes is the upper bound for avatar uploads before scaling.
const avatarMaxBytes = 1 << 20 // 1 MiB

// PUT /api/v1/auth/avatar — Multipart-Form mit Feld "avatar".
func (h *AuthHandler) PutAvatar(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())

	// MaxBytesReader liefert ein 413, wenn der Body groesser als das Limit
	// ist — wir setzen ein bisschen Slack ein, damit Multipart-Overhead nicht
	// auf den Nutzdaten-Limit angerechnet wird.
	r.Body = http.MaxBytesReader(w, r.Body, avatarMaxBytes+8192)

	//#nosec G120 -- body is already capped by the MaxBytesReader above; ParseMultipartForm cannot exhaust memory beyond avatarMaxBytes+8192
	if err := r.ParseMultipartForm(avatarMaxBytes + 8192); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, ErrAvatarTooLarge, "avatar too large")
		return
	}

	file, _, err := r.FormFile("avatar")
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrAvatarMissing, "missing avatar field")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, avatarMaxBytes+1))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, ErrAvatarTooLarge, "avatar too large")
		return
	}

	png, err := avatar.Process(data, avatarMaxBytes)
	switch {
	case errors.Is(err, avatar.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, ErrAvatarTooLarge, "avatar too large")
		return
	case errors.Is(err, avatar.ErrInvalidImage):
		writeError(w, http.StatusBadRequest, ErrAvatarInvalid, "invalid image — PNG/JPG required")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	if err := h.stor.WriteAvatar(claims.UserID, png); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	relPath := claims.UserID.String() + ".png"
	if err := h.users.SetAvatarPath(r.Context(), claims.UserID, relPath); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"avatar_url": "/api/v1/auth/avatar/" + claims.UserID.String(),
	})
}

// DELETE /api/v1/auth/avatar — entfernt Datei + setzt avatar_path = NULL.
func (h *AuthHandler) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if err := h.stor.DeleteAvatar(claims.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	if err := h.users.ClearAvatarPath(r.Context(), claims.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/auth/avatar/{userID} — liefert das PNG. Authentifiziert, weil
// Profilbilder ohne Login nicht einsehbar sein sollen.
func (h *AuthHandler) GetAvatar(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "userID")
	userID, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidUserID, "invalid user id")
		return
	}
	user, err := h.users.FindByID(r.Context(), userID)
	if err != nil || user.AvatarPath == nil {
		writeError(w, http.StatusNotFound, ErrAvatarNotPresent, "no avatar")
		return
	}
	//#nosec G703 -- userID is a parsed uuid.UUID (line 92); AvatarPath joins it as `<uuid>.png`, no user-controlled path segment
	f, err := os.Open(h.stor.AvatarPath(userID))
	if err != nil {
		writeError(w, http.StatusNotFound, ErrAvatarNotPresent, "no avatar")
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "avatar.png", stat.ModTime(), f)
}
