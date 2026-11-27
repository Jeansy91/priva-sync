package handler

import (
	"net/http"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/sse"
)

type SSEHandler struct {
	hub *sse.Hub
}

func NewSSEHandler(hub *sse.Hub) *SSEHandler {
	return &SSEHandler{hub: hub}
}

// GET /api/v1/events
// Long-lived SSE stream; authentication is enforced by AuthenticateSSE, which
// accepts either a Bearer access token (desktop) or the scoped SSE cookie
// (browser EventSource).
func (h *SSEHandler) Stream(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	h.hub.Subscribe(w, r, claims.UserID)
}
