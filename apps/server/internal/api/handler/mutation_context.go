package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/quota"
	"privasync/server/internal/storage"
	syncpkg "privasync/server/internal/sync"
)

// MutationContext is the resolved head every mutating coordinator command
// carries (ADR-0017): the sync-root owner whose quota/trash the write hits,
// the actor who actually performed it, and the target root. Own-root writes
// set Owner == Actor; shared-mount writes set Owner to the giver and Actor to
// the recipient (ADR-0013). This is a code struct, not domain vocabulary — it
// deliberately stays out of CONTEXT.md.
type MutationContext struct {
	Owner  uuid.UUID
	Actor  uuid.UUID
	RootID uuid.UUID
}

// joinUnderShareRoot joins sub against base, cleans the result and reports
// whether it stays under base. The escape guard used to be re-inlined inside
// every mount mutation handler; ADR-0017 lifts it here so the security
// boundary is unit-testable without HTTP. base == "" means "no share prefix"
// (own sync roots span the whole user namespace) and is trivially in-bounds.
func joinUnderShareRoot(base, sub string) (string, bool) {
	joined := path.Clean(base + "/" + strings.TrimLeft(sub, "/"))
	return joined, isUnderShareRoot(joined, base)
}

// resolveMutationPath reads the ?path query, joins it under basePath through
// the share-escape guard and sanitizes it. An empty path is a 400 — every
// mutation endpoint needs an explicit target. basePath "" is an own root (no
// share prefix); a non-empty basePath is a mount and an escape yields 403.
func resolveMutationPath(w http.ResponseWriter, r *http.Request, basePath string) (string, bool) {
	sub := strings.TrimSpace(r.URL.Query().Get("path"))
	if sub == "" {
		writeError(w, http.StatusBadRequest, ErrPathRequired, "path required")
		return "", false
	}
	return resolveSubPath(w, basePath, sub, ErrInvalidPath)
}

// parseIfMatch reads the If-Match header as an optimistic-concurrency
// server_version (RFC 7232). An absent header (or `*`) means "no opinion"
// and yields (nil, true) so a path collision routes through the real-conflict
// pipeline. A malformed value writes a 400 and returns (nil, false) so the
// caller bails. Shared by every mutation that honours If-Match: own-root and
// mount upload/move.
func parseIfMatch(w http.ResponseWriter, r *http.Request) (*int64, bool) {
	hdr := r.Header.Get("If-Match")
	if hdr == "" || hdr == "*" {
		return nil, true
	}
	v, err := strconv.ParseInt(hdr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid If-Match header")
		return nil, false
	}
	return &v, true
}

// resolveSubPath joins sub under basePath through the escape guard and
// sanitizes the result. An escape is a 403; a malformed path is a 400 with
// invalidCode, letting move distinguish its from/to fields. basePath "" is an
// own root (no prefix). Shared by every mutation that resolves a path from
// either the ?path query or a move body field.
func resolveSubPath(w http.ResponseWriter, basePath, sub, invalidCode string) (string, bool) {
	joined, ok := joinUnderShareRoot(basePath, sub)
	if !ok {
		writeError(w, http.StatusForbidden, ErrInvalidPath, "sub path escapes share")
		return "", false
	}
	cleanPath, err := sanitizePath(joined)
	if err != nil {
		writeError(w, http.StatusBadRequest, invalidCode, "invalid path")
		return "", false
	}
	return cleanPath, true
}

// decodeMoveRequest parses the {op,from,to} body, resolves both paths under
// base through the same escape guard as resolveSubPath, and rejects a
// same-path move — the prolog shared by the own-root and mount move handlers
// (ADR-0017 follow-up). Op-specific validation (own-root also accepts
// "copy", mount only "move") stays with the caller since the accepted set
// differs; this only decodes and resolves what both handlers need identically.
func decodeMoveRequest(w http.ResponseWriter, r *http.Request, base string) (op, fromPath, toPath string, ok bool) {
	var req struct {
		Op   string `json:"op"`
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return "", "", "", false
	}
	fromPath, ok = resolveSubPath(w, base, req.From, ErrInvalidFromPath)
	if !ok {
		return "", "", "", false
	}
	toPath, ok = resolveSubPath(w, base, req.To, ErrInvalidToPath)
	if !ok {
		return "", "", "", false
	}
	if fromPath == toPath {
		writeError(w, http.StatusBadRequest, ErrSamePath, "from and to paths are identical")
		return "", "", "", false
	}
	return req.Op, fromPath, toPath, true
}

// writeMoveResult renders the coordinator's MoveResult conflict cases — the
// part that is byte-identical between the own-root and mount move handlers.
// The success body isn't: own-root exposes size/content_hash/updated_at,
// mount only id/path, so the caller supplies it (ADR-0017 follow-up).
func writeMoveResult(w http.ResponseWriter, result syncpkg.MoveResult, successBody map[string]any) {
	switch result.Conflict {
	case syncpkg.ConflictVersionMismatch:
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":          "move conflict",
			"current_path":   result.File.Path,
			"server_version": result.File.ServerVersion,
		})
	case syncpkg.ConflictReal:
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":          "move conflict",
			"current_path":   result.File.Path,
			"server_version": result.File.ServerVersion,
			"conflict_path":  result.ConflictPath,
		})
	default:
		writeJSON(w, http.StatusOK, successBody)
	}
}

// mutationDeps is the slice of collaborators the shared mutation bodies need.
// FileHandler and ShareHandler are distinct structs that both carry these
// four fields; grouping them keeps the free helpers' signatures readable.
type mutationDeps struct {
	storage     *storage.Service
	coordinator syncpkg.Coordinator
	quota       *quota.Service
	maxUpload   int64
}

// uploadResolved is the shared upload body behind both the own-root PUT and
// the shared-mount POST (ADR-0017). Once the caller has resolved who owns the
// write (mc) and the mount prefix (basePath, "" for own roots), the staging →
// quota → coordinator → render pipeline is identical, so it lives here once.
func uploadResolved(w http.ResponseWriter, r *http.Request, d mutationDeps, mc MutationContext, basePath string) {
	cleanPath, ok := resolveMutationPath(w, r, basePath)
	if !ok {
		return
	}

	// Pre-stream quota gate. addBytes=0 because the real size is only known
	// after the body is read; the coordinator re-gates against the true delta.
	if err := d.quota.CheckHeadroom(r.Context(), mc.Owner, 0); err != nil {
		if errors.Is(err, quota.ErrExceeded) {
			writeError(w, http.StatusInsufficientStorage, ErrQuotaExceeded, "storage quota exceeded")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	// If-Match (RFC 7232) is parsed before staging so a malformed header bails
	// without leaving orphan bytes. Absent/`*` means "no opinion" → a path
	// collision routes through the real-conflict pipeline.
	ifMatch, ok := parseIfMatch(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, d.maxUpload)
	rootStr := mc.RootID.String()
	stagingPath := cleanPath + ".staging-" + uuid.NewString()
	written, contentHash, err := d.storage.WriteFile(mc.Owner, rootStr, stagingPath, r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrUploadFailed, "upload failed")
		return
	}

	mtime := time.Now()
	if mtimeStr := r.Header.Get("X-File-Mtime"); mtimeStr != "" {
		if t, parseErr := time.Parse(time.RFC3339, mtimeStr); parseErr == nil {
			mtime = t
		}
	}

	result, err := d.coordinator.UploadFile(r.Context(), syncpkg.UploadCmd{
		UserID:      mc.Owner,
		ActorUserID: mc.Actor,
		SyncRootID:  mc.RootID,
		Path:        cleanPath,
		StagingPath: stagingPath,
		Size:        written,
		ContentHash: contentHash,
		MTime:       mtime,
		IfMatch:     ifMatch,
	})
	if err != nil {
		_ = d.storage.DeleteFile(mc.Owner, rootStr, stagingPath)
		if errors.Is(err, quota.ErrExceeded) {
			writeError(w, http.StatusInsufficientStorage, ErrQuotaExceeded, "storage quota exceeded")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	writeUploadResult(w, d.storage, mc.Owner, rootStr, stagingPath, result)
}
