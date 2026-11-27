package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/audit"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

type roleError struct {
	Status int
	Code   string
	Msg    string
}

func (e *roleError) Error() string { return e.Msg }

// validateActiveChange blockiert Self-Lock: ein Admin darf den eigenen
// Account nicht deaktivieren — sonst kann er sich selbst aussperren.
func validateActiveChange(active bool, targetID, callerID uuid.UUID) *roleError {
	if targetID == callerID && !active {
		return &roleError{http.StatusBadRequest, ErrInvalidTargetUser, "cannot lock yourself"}
	}
	return nil
}

// validateRoleChange reports whether a role change is allowed.
// Self-demotion (an admin removing their own admin rights) is forbidden —
// otherwise a single-admin system could lock itself out of admin access.
func validateRoleChange(newRole model.Role, targetID, callerID uuid.UUID) *roleError {
	if newRole != model.RoleAdmin && newRole != model.RoleUser {
		return &roleError{http.StatusBadRequest, ErrInvalidBody, "invalid role"}
	}
	if targetID == callerID && newRole != model.RoleAdmin {
		return &roleError{http.StatusBadRequest, ErrInvalidTargetUser, "cannot demote yourself"}
	}
	return nil
}

// PUT /api/v1/admin/users/{userID}/role
func (h *AdminHandler) SetRole(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidUserID, "invalid user id")
		return
	}

	var req struct {
		Role model.Role `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}

	claims := middleware.ClaimsFromContext(r.Context())
	if vErr := validateRoleChange(req.Role, userID, claims.UserID); vErr != nil {
		writeError(w, vErr.Status, vErr.Code, vErr.Msg)
		return
	}

	if err := h.users.SetRole(r.Context(), userID, req.Role); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrUserNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	adminID := claims.UserID
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &adminID, audit.EventAdminUserUpdated, map[string]any{
		"target_user_id": userID,
		"role":           req.Role,
	}))
	w.WriteHeader(http.StatusNoContent)
}
