package handler

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/repository"
)

type UserHandler struct {
	users *repository.UserRepository
}

func NewUserHandler(users *repository.UserRepository) *UserHandler {
	return &UserHandler{users: users}
}

type userSummary struct {
	ID       uuid.UUID `json:"id"`
	Username string    `json:"username"`
}

const userSearchLimit = 10

// GET /api/v1/users/search?q=<prefix>
//
// Returns active users with a username prefix match, capped. Self excluded.
// An empty query yields the first N users alphabetically so the picker UI
// shows suggestions immediately on open (rather than depending on typing).
func (h *UserHandler) Search(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	users, err := h.users.SearchByUsernamePrefix(r.Context(), q, claims.UserID, userSearchLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	resp := make([]userSummary, 0, len(users))
	for _, u := range users {
		resp = append(resp, userSummary{ID: u.ID, Username: u.Username})
	}
	writeJSON(w, http.StatusOK, resp)
}
