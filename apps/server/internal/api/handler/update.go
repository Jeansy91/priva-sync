// Package handler: public update feed. Answers the Tauri updater with the
// client build that belongs to this server version (ADR-0088). Unauthenticated,
// reachable pre-login. The artifacts themselves are not served here: the
// response points at the GitHub release assets.
package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"privasync/server/internal/clientrelease"
)

type UpdateHandler struct {
	manifest      clientrelease.Manifest
	serverVersion string
}

// NewUpdateHandler builds the feed handler. serverVersion is the running
// server's version; manifest is the build-time client manifest.
func NewUpdateHandler(manifest clientrelease.Manifest, serverVersion string) *UpdateHandler {
	return &UpdateHandler{manifest: manifest, serverVersion: serverVersion}
}

// GET /updates/{target}/{currentVersion}
//
// 200 with the Tauri v2 updater JSON when the client is older than this
// server and a build exists for its target; 204 otherwise (up to date, newer
// than the server, or no build for the target).
func (h *UpdateHandler) ServeManifest(w http.ResponseWriter, r *http.Request) {
	target := chi.URLParam(r, "target")
	current := chi.URLParam(r, "version")
	if !clientrelease.ValidTarget(target) {
		writeError(w, http.StatusBadRequest, ErrInvalidTarget, "invalid target")
		return
	}
	u := h.manifest.UpdateFor(target, current, h.serverVersion)
	if u == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, u)
}
