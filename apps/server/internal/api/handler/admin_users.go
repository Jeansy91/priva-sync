package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/audit"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

// GET /api/v1/admin/users
func (h *AdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.users.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	failedByUser, err := h.users.FailedAttemptsByUser(r.Context(), h.cfg.LoginLockoutWindow)
	if err != nil {
		failedByUser = map[string]int{}
	}

	resp := make([]userResp, 0, len(users))
	for _, u := range users {
		resp = append(resp, newUserResp(u, failedByUser[u.Username] >= h.cfg.MaxLoginAttempts))
	}
	writeJSON(w, http.StatusOK, resp)
}

// userResp is the single admin-facing shape of a user. Both the listing and
// the create endpoint return it, so a freshly created row can be rendered by
// the client without a refetch.
type userResp struct {
	ID          uuid.UUID  `json:"id"`
	Username    string     `json:"username"`
	Email       string     `json:"email"`
	Role        model.Role `json:"role"`
	QuotaBytes  int64      `json:"quota_bytes"`
	UsedBytes   int64      `json:"used_bytes"`
	TrashBytes  int64      `json:"trash_bytes"`
	TOTPEnabled bool       `json:"totp_enabled"`
	IsActive    bool       `json:"is_active"`
	IsLocked    bool       `json:"is_locked"`
	AvatarURL   *string    `json:"avatar_url"`
}

func newUserResp(u model.User, isLocked bool) userResp {
	var avatarURL *string
	if u.AvatarPath != nil {
		s := "/api/v1/auth/avatar/" + u.ID.String()
		avatarURL = &s
	}
	return userResp{
		ID: u.ID, Username: u.Username, Email: u.Email,
		Role: u.Role, QuotaBytes: u.QuotaBytes, UsedBytes: u.UsedBytes,
		TrashBytes:  u.TrashBytes,
		TOTPEnabled: u.TOTPEnabled, IsActive: u.IsActive,
		IsLocked:  isLocked,
		AvatarURL: avatarURL,
	}
}

// inviteTokenTTL bounds how long the mailed invite link stays valid. Longer
// than the reset TTL because the recipient may not check mail immediately;
// after expiry the user self-services a fresh link via "forgot password".
const inviteTokenTTL = 72 * time.Hour

// POST /api/v1/admin/users
//
// Creates the account without a usable password: the user receives an invite
// mail with a one-time token and sets their own password via the reset-confirm
// endpoint. The admin never knows the credential.
func (h *AdminHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string     `json:"username"`
		Email    string     `json:"email"`
		Role     model.Role `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}
	if req.Username == "" || req.Email == "" {
		writeError(w, http.StatusBadRequest, ErrRegistrationFields, "username and email required")
		return
	}
	if req.Role == "" {
		req.Role = model.RoleUser
	}

	// Empty hash = no password can ever verify against it, so the account is
	// inert until the invite token is redeemed.
	user, err := h.users.Create(r.Context(), req.Username, req.Email, "", req.Role)
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, ErrUsernameTaken, "username or email already taken")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	token, err := h.newInviteToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	if err := h.users.CreatePasswordResetToken(r.Context(), user.ID, token, time.Now().Add(inviteTokenTTL)); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	sendInviteMail(h.mailer, h.baseURL, user, token)

	claims := middleware.ClaimsFromContext(r.Context())
	adminID := claims.UserID
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &adminID, audit.EventAdminUserCreated, map[string]any{
		"target_user_id":  user.ID,
		"target_username": user.Username,
		"role":            user.Role,
	}))

	// Full shape, not just the identity fields: the admin UI prepends this
	// row into the live table, so any missing counter would render as NaN.
	writeJSON(w, http.StatusCreated, newUserResp(*user, false))
}

// POST /api/v1/admin/users/{userID}/unlock
func (h *AdminHandler) UnlockUser(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidUserID, "invalid user id")
		return
	}

	target, err := h.users.FindByID(r.Context(), userID)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrUserNotFound, "user not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	if err := h.users.ClearFailedAttempts(r.Context(), target.Username); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	claims := middleware.ClaimsFromContext(r.Context())
	adminID := claims.UserID
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &adminID, audit.EventAdminUserUnlocked, map[string]any{
		"target_user_id":  target.ID,
		"target_username": target.Username,
	}))

	w.WriteHeader(http.StatusNoContent)
}

// PUT /api/v1/admin/users/{userID}/active
func (h *AdminHandler) SetActive(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidUserID, "invalid user id")
		return
	}

	var req struct {
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}

	claims := middleware.ClaimsFromContext(r.Context())
	if vErr := validateActiveChange(req.Active, userID, claims.UserID); vErr != nil {
		writeError(w, vErr.Status, vErr.Code, vErr.Msg)
		return
	}
	if err := h.users.SetActive(r.Context(), userID, req.Active); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	adminID := claims.UserID
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &adminID, audit.EventAdminUserUpdated, map[string]any{
		"target_user_id": userID,
		"active":         req.Active,
	}))
	w.WriteHeader(http.StatusNoContent)
}
