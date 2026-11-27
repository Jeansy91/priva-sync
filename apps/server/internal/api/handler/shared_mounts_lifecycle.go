package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/audit"
	"privasync/server/internal/repository"
	sseevents "privasync/server/internal/sse/events"
	syncpkg "privasync/server/internal/sync"
)

// DELETE /api/v1/shared-mounts/{shareID}
//
// Recipient-side "leave the mount" action. Deletes the user-share row
// the recipient is bound to, which tears down the mount from his view
// and emits share_revoked to both the giver and the recipient so any
// open sessions drop the mount immediately. The giver retains all
// other shares; only this one anchor is removed. See ADR-0010.
func (h *ShareHandler) LeaveSharedMount(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	grantID, err := uuid.Parse(chi.URLParam(r, "shareID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidShareID, "invalid share id")
		return
	}
	// Resolve before delete so we can notify the giver. The mount id is the
	// recipient's grant id (ADR-0015); the aggregate yields the giver's user id.
	existing, _ := h.shares.FindMountByGrant(r.Context(), grantID, claims.UserID)
	if err := h.shares.DeleteByTarget(r.Context(), grantID, claims.UserID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrShareNotFound, "share not found")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &claims.UserID, audit.EventShareDeleted, map[string]any{
		"share_id": grantID,
		"reason":   "recipient_leave",
	}))
	syncpkg.PublishEphemeral(h.hub, claims.UserID, sseevents.ShareRevoked{ShareID: grantID})
	if existing != nil {
		syncpkg.PublishEphemeral(h.hub, existing.UserID, sseevents.ShareRevoked{ShareID: grantID})
	}
	w.WriteHeader(http.StatusNoContent)
}
