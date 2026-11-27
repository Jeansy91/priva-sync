package handler

import (
	"encoding/hex"
	"net/http"

	"github.com/google/uuid"

	"privasync/server/internal/storage"
	syncpkg "privasync/server/internal/sync"
)

// writeUploadResult renders a coordinator UploadResult as the HTTP response
// shared by every write adapter (full upload, chunked complete, delta apply).
// On a version mismatch the staging file is now orphaned and removed here; the
// real-conflict path keeps it because the coordinator already renamed it onto
// the loser path.
func writeUploadResult(w http.ResponseWriter, stor *storage.Service, userID uuid.UUID, rootID, stagingPath string, result syncpkg.UploadResult) {
	switch result.Conflict {
	case syncpkg.ConflictVersionMismatch:
		_ = stor.DeleteFile(userID, rootID, stagingPath)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":          "version conflict",
			"server_version": result.File.ServerVersion,
			"server_hash":    hex.EncodeToString(result.File.ContentHash),
		})
	case syncpkg.ConflictReal:
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":          "version conflict",
			"server_version": result.File.ServerVersion,
			"server_hash":    hex.EncodeToString(result.File.ContentHash),
			"conflict_path":  result.ConflictPath,
		})
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"id":             result.File.ID,
			"path":           result.File.Path,
			"size":           result.File.Size,
			"content_hash":   hex.EncodeToString(result.File.ContentHash),
			"server_version": result.File.ServerVersion,
			"updated_at":     result.File.UpdatedAt,
		})
	}
}
