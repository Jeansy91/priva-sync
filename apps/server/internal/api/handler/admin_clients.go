package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

// adminClientStore is the client-registry slice AdminClientsHandler needs
// (ADR-0029).
type adminClientStore interface {
	ListAll(ctx context.Context) ([]model.ClientWithUser, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// AdminClientsHandler serves the admin overview of registered desktop/mobile
// clients (which user, OS, version, IP, last seen).
type AdminClientsHandler struct {
	clients adminClientStore
}

func NewAdminClientsHandler(clients adminClientStore) *AdminClientsHandler {
	return &AdminClientsHandler{clients: clients}
}

// GET /api/v1/admin/clients
func (h *AdminClientsHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.clients.ListAll(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		out = append(out, map[string]any{
			"id":             c.ID,
			"user_id":        c.UserID,
			"username":       c.Username,
			"device_id":      c.DeviceID,
			"name":           c.Name,
			"platform":       c.Platform,
			"os_version":     c.OSVersion,
			"client_version": c.ClientVersion,
			"last_ip":        c.LastIP,
			"last_seen":      c.LastSeen,
			"created_at":     c.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": out})
}

// DELETE /api/v1/admin/clients/{clientID}
//
// Removes a stale entry from the overview. This is bookkeeping only — no
// session is revoked, so a device that is still signed in reappears on its
// next login or token refresh, which is exactly what makes the list
// self-cleaning for devices that are truly gone.
func (h *AdminClientsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "clientID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidClientID, "invalid client id")
		return
	}
	if err := h.clients.Delete(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrClientNotFound, "client not found")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
