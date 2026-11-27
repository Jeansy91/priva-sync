package handler

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/delta"
	"privasync/server/internal/quota"
	"privasync/server/internal/repository"
	"privasync/server/internal/storage"
	syncpkg "privasync/server/internal/sync"
)

// DeltaHandler serves the rsync-style delta-sync endpoints (ADR-0002):
// block signatures of an existing server file and reconstruction from a
// client-supplied plan. Reconstruction reuses the Sync Coordinator so version
// checks, conflicts, quota and SSE behave exactly like a full upload.
type DeltaHandler struct {
	files       *repository.FileRepository
	syncRoots   *repository.SyncRootRepository
	storage     *storage.Service
	coordinator syncpkg.Coordinator
}

func NewDeltaHandler(
	files *repository.FileRepository,
	syncRoots *repository.SyncRootRepository,
	stor *storage.Service,
	coord syncpkg.Coordinator,
) *DeltaHandler {
	return &DeltaHandler{files: files, syncRoots: syncRoots, storage: stor, coordinator: coord}
}

// GET /api/v1/sync-roots/{rootID}/files/delta?path=/foo.bin
// Returns per-block signatures of the current server version so the client can
// diff locally and upload only changed blocks. When no server version exists or
// it is at/below the delta threshold, responds delta_available=false so the
// client falls back to a full upload.
func (h *DeltaHandler) DeltaRequest(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}
	root, err := h.syncRoots.GetSyncRoot(r.Context(), rootID, claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
		return
	}

	cleanPath, err := sanitizePath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidPath, "invalid path")
		return
	}

	dbFile, err := h.files.FindByPath(r.Context(), rootID, cleanPath)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && (dbFile.IsDeleted || dbFile.IsDir)) {
		// No usable server version — client must upload in full.
		writeJSON(w, http.StatusOK, map[string]any{"delta_available": false})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	if dbFile.Size <= delta.MinDeltaSize {
		writeJSON(w, http.StatusOK, map[string]any{"delta_available": false})
		return
	}

	// Prefer the persisted block-signature cache (files.block_hashes):
	// UploadFile fills it on every write, so the hot read path serves it
	// directly without re-hashing the file. A NULL/unparsable cache (pre-migration
	// rows, sub-threshold history) falls back to on-the-fly hashing below.
	sigs, ok := decodeBlockHashes(dbFile.BlockHashes)
	if !ok {
		f, err := h.storage.Open(claims.UserID, root.ID.String(), cleanPath)
		if err != nil {
			writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
			return
		}
		defer f.Close()

		sigs, err = delta.BlockSignatures(f, delta.BlockSize)
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"delta_available": true,
		"block_size":      delta.BlockSize,
		"server_version":  dbFile.ServerVersion,
		"signatures":      sigs,
	})
}

// decodeBlockHashes parses a file row's cached block_hashes JSONB into
// signatures. ok is false when the cache is absent (NULL) or unparsable, so the
// caller falls back to hashing the file on the fly.
func decodeBlockHashes(raw []byte) (sigs []delta.Signature, ok bool) {
	if len(raw) == 0 {
		return nil, false
	}
	if err := json.Unmarshal(raw, &sigs); err != nil {
		return nil, false
	}
	return sigs, true
}

// deltaApplyRequest is the wire body of a delta-apply call. Each op carries
// exactly one of copy or data; literal data is base64 so the plan stays JSON.
type deltaApplyRequest struct {
	Path         string `json:"path"`
	ExpectedHash string `json:"expected_hash"` // hex BLAKE3 of the reconstructed file
	MTime        string `json:"mtime"`         // RFC3339, optional
	Ops          []struct {
		Copy *struct {
			Offset int64 `json:"offset"`
			Length int64 `json:"length"`
		} `json:"copy"`
		Data string `json:"data"` // base64
	} `json:"ops"`
}

// decodeOps translates the wire ops into delta.Op values, rejecting any op that
// is neither a clean copy nor data.
func decodeOps(req *deltaApplyRequest) ([]delta.Op, error) {
	ops := make([]delta.Op, 0, len(req.Ops))
	for i, o := range req.Ops {
		switch {
		case o.Copy != nil && o.Data == "":
			ops = append(ops, delta.Copy{SrcOffset: o.Copy.Offset, Length: o.Copy.Length})
		case o.Copy == nil && o.Data != "":
			raw, err := base64.StdEncoding.DecodeString(o.Data)
			if err != nil {
				return nil, errors.New("op " + strconv.Itoa(i) + ": invalid base64 data")
			}
			ops = append(ops, delta.Data{Bytes: raw})
		default:
			return nil, errors.New("op " + strconv.Itoa(i) + ": must carry exactly one of copy/data")
		}
	}
	return ops, nil
}

// POST /api/v1/sync-roots/{rootID}/files/delta/apply
// Reconstructs the full file from the client's copy/data plan against the
// current server version, verifies the declared content hash, then commits
// through the Sync Coordinator (version check, conflict, quota, SSE). If-Match
// carries the client's known server_version (RFC 7232).
func (h *DeltaHandler) DeltaApply(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}
	root, err := h.syncRoots.GetSyncRoot(r.Context(), rootID, claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
		return
	}

	var req deltaApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}
	cleanPath, err := sanitizePath(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidPath, "invalid path")
		return
	}
	expectedHash, err := hex.DecodeString(req.ExpectedHash)
	if err != nil || len(expectedHash) == 0 {
		writeError(w, http.StatusBadRequest, ErrUploadFields, "expected_hash required")
		return
	}
	ops, err := decodeOps(&req)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrDeltaInvalidOp, err.Error())
		return
	}

	// Delta only applies against an existing, non-deleted server version — the
	// copy ops reference its bytes. A vanished base means the client must fall
	// back to a full upload.
	base, err := h.files.FindByPath(r.Context(), rootID, cleanPath)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && (base.IsDeleted || base.IsDir)) {
		writeError(w, http.StatusUnprocessableEntity, ErrDeltaNoBase, "no server base for delta")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	oldFile, err := h.storage.Open(claims.UserID, root.ID.String(), cleanPath)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, ErrDeltaNoBase, "no server base for delta")
		return
	}
	defer oldFile.Close()

	stagingPath, staging, err := h.storage.CreateStaging(claims.UserID, root.ID.String(), cleanPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	written, actualHash, err := delta.Reconstruct(oldFile, ops, staging)
	staging.Close()
	if err != nil {
		_ = h.storage.DeleteFile(claims.UserID, root.ID.String(), stagingPath)
		writeError(w, http.StatusUnprocessableEntity, ErrDeltaFailed, "reconstruction failed")
		return
	}

	// Verify the rebuilt file hashes to what the client promised before we let
	// the coordinator commit it — a bad plan never replaces the server file.
	if hex.EncodeToString(actualHash) != hex.EncodeToString(expectedHash) {
		_ = h.storage.DeleteFile(claims.UserID, root.ID.String(), stagingPath)
		writeError(w, http.StatusUnprocessableEntity, ErrContentHashMismatch, "content hash mismatch")
		return
	}

	mtime := time.Now()
	if req.MTime != "" {
		if t, perr := time.Parse(time.RFC3339, req.MTime); perr == nil {
			mtime = t
		}
	}

	var ifMatch *int64
	if hdr := r.Header.Get("If-Match"); hdr != "" && hdr != "*" {
		v, perr := strconv.ParseInt(hdr, 10, 64)
		if perr != nil {
			_ = h.storage.DeleteFile(claims.UserID, root.ID.String(), stagingPath)
			writeError(w, http.StatusBadRequest, ErrUploadFields, "invalid If-Match header")
			return
		}
		ifMatch = &v
	}

	result, err := h.coordinator.UploadFile(r.Context(), syncpkg.UploadCmd{
		UserID:      claims.UserID,
		ActorUserID: claims.UserID,
		SyncRootID:  rootID,
		Path:        cleanPath,
		StagingPath: stagingPath,
		Size:        written,
		ContentHash: actualHash,
		MTime:       mtime,
		IfMatch:     ifMatch,
	})
	if err != nil {
		_ = h.storage.DeleteFile(claims.UserID, root.ID.String(), stagingPath)
		if errors.Is(err, quota.ErrExceeded) {
			writeError(w, http.StatusInsufficientStorage, ErrQuotaExceeded, "storage quota exceeded")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	writeUploadResult(w, h.storage, claims.UserID, root.ID.String(), stagingPath, result)
}
