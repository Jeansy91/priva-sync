package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/audit"
	"privasync/server/internal/repository"
)

// PUT /api/v1/admin/users/{userID}/quota
func (h *AdminHandler) SetQuota(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidUserID, "invalid user id")
		return
	}

	var req struct {
		QuotaBytes int64 `json:"quota_bytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}

	if err := h.users.SetQuota(r.Context(), userID, req.QuotaBytes); err != nil {
		slog.Error("admin: set quota failed", "target_user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	adminID := claims.UserID
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &adminID, audit.EventAdminQuotaSet, map[string]any{
		"target_user_id": userID,
		"quota_bytes":    req.QuotaBytes,
	}))
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/admin/users/{userID}/recompute-quota
// Rebuilds used_bytes/trash_bytes from the `files` table via the bus, so the
// quota subscriber re-evaluates the threshold band as part of the heal.
func (h *AdminHandler) RecomputeQuota(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidUserID, "invalid user id")
		return
	}
	if err := h.quota.RecomputeViaBus(r.Context(), userID); errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrUserNotFound, "user not found")
		return
	} else if err != nil {
		slog.Error("admin: recompute quota failed", "target_user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	u, err := h.users.FindByID(r.Context(), userID)
	if err != nil {
		slog.Error("admin: recompute quota reload failed", "target_user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	adminID := claims.UserID
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &adminID, audit.EventAdminQuotaRecomputed, map[string]any{
		"target_user_id": u.ID,
		"used_bytes":     u.UsedBytes,
		"trash_bytes":    u.TrashBytes,
	}))
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          u.ID,
		"used_bytes":  u.UsedBytes,
		"trash_bytes": u.TrashBytes,
		"quota_bytes": u.QuotaBytes,
	})
}
