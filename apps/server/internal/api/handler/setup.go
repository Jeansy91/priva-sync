package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"privasync/server/internal/auth"
	"privasync/server/internal/bootstrap"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

type SetupHandler struct {
	state *bootstrap.State
	users *repository.UserRepository
}

func NewSetupHandler(state *bootstrap.State, users *repository.UserRepository) *SetupHandler {
	return &SetupHandler{state: state, users: users}
}

// GET /api/v1/setup/status
// Public. Frontend calls this before login to decide whether to redirect to /setup.
func (h *SetupHandler) Status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"needs_setup": h.state.NeedsSetup(),
	})
}

// POST /api/v1/setup
// Public. Consumes the one-shot bootstrap token to create the initial admin.
func (h *SetupHandler) Complete(w http.ResponseWriter, r *http.Request) {
	if !h.state.NeedsSetup() {
		writeError(w, http.StatusNotFound, ErrSetupAlreadyComplete, "setup already complete")
		return
	}

	var req struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}
	if req.Username == "" || req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, ErrRegistrationFields, "username, email and password required")
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, ErrPasswordTooShort, "password must be at least 8 characters")
		return
	}
	if !h.state.Verify(req.Token) {
		slog.Warn("setup: invalid token attempt")
		writeError(w, http.StatusUnauthorized, ErrInvalidSetupToken, "invalid setup token")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	user, err := h.users.Create(r.Context(), req.Username, req.Email, hash, model.RoleAdmin)
	if errors.Is(err, repository.ErrConflict) {
		writeError(w, http.StatusConflict, ErrUsernameTaken, "username or email already taken")
		return
	}
	if err != nil {
		slog.Error("setup: admin create failed", "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	h.state.Complete()
	slog.Info("setup complete — admin created", "username", user.Username, "user_id", user.ID)

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":       user.ID,
		"username": user.Username,
		"email":    user.Email,
	})
}
