package handler

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/assembler"
	"privasync/server/internal/errcode"
	"privasync/server/internal/model"
	"privasync/server/internal/quota"
	"privasync/server/internal/repository"
	"privasync/server/internal/storage"
)

// maxChunkLength is a flat, server-side ceiling per chunk, independent of
// whatever size the client's own adaptive heuristic picked (ADR-0066) — the
// server has no opinion on the client's target chunk size, only a bound
// against a runaway or malicious declared length.
const maxChunkLength int64 = 256 * 1024 * 1024 // 256 MiB

// uploadStore is the slice of UploadRepository that UploadHandler depends on.
// A narrow role interface (ADR-0029) — accepting it instead of the concrete
// repo lets tests inject session-store failures to exercise the 500 paths.
type uploadStore interface {
	CreateSession(ctx context.Context, s *model.UploadSession) (*model.UploadSession, error)
	GetSession(ctx context.Context, sessionID, userID uuid.UUID) (*model.UploadSession, error)
	AddRange(ctx context.Context, sessionID uuid.UUID, offset, length int64) (*model.UploadSession, error)
	MarkQueued(ctx context.Context, sessionID uuid.UUID, ifMatch *int64) (*model.UploadSession, error)
}

// uploadQueue is the assembly worker pool seen from the request side: the
// handler hands work over and stops caring. Everything the worker needs is on
// the session row, which is also where it writes its verdict.
type uploadQueue interface {
	Submit(job assembler.Job) bool
}

// How long a status long-poll may hold a request, and how often it looks. The
// ceiling stays well under what a reverse proxy tolerates (~100 s), so the
// answer is ours rather than a 504; the interval is a compromise between
// answering promptly and querying a row nobody else is touching.
const (
	maxStatusWait    = 60 * time.Second
	statusPollPeriod = 500 * time.Millisecond
)

type UploadHandler struct {
	syncRoots *repository.SyncRootRepository
	uploads   uploadStore
	storage   *storage.Service
	quota     *quota.Service
	queue     uploadQueue

	// DiskUsage reports free and total bytes for the data volume. Injectable
	// so tests can describe a full disk without filling one.
	DiskUsage DiskUsageFunc
}

func NewUploadHandler(
	syncRoots *repository.SyncRootRepository,
	uploads uploadStore,
	stor *storage.Service,
	q *quota.Service,
	queue uploadQueue,
) *UploadHandler {
	return &UploadHandler{
		syncRoots: syncRoots, uploads: uploads, storage: stor,
		quota: q, queue: queue, DiskUsage: RealDiskUsage,
	}
}

// POST /api/v1/sync-roots/{rootID}/uploads
// Initiates a new multi-part upload session. The optimistic version check is
// stated here, in the init body: the complete call carries no body, and its
// If-Match header cannot reach a caller that finishes the session outside a
// request. A header on complete still wins when one is present (RFC 7232).
// Body: { "path": "/...", "total_size": 123456, "content_hash": "hex",
//
//	"mtime": "RFC3339", "if_match": 7 }
func (h *UploadHandler) InitUpload(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}
	if _, err := h.syncRoots.GetSyncRoot(r.Context(), rootID, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
		return
	}

	var req struct {
		Path        string `json:"path"`
		TotalSize   int64  `json:"total_size"`
		ContentHash string `json:"content_hash"` // hex BLAKE3, optional
		MTime       string `json:"mtime"`        // RFC3339, optional
		IfMatch     *int64 `json:"if_match"`     // server_version being overwritten, optional
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" || req.TotalSize <= 0 {
		writeError(w, http.StatusBadRequest, ErrUploadFields, "path and total_size required")
		return
	}

	cleanPath, err := sanitizePath(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidPath, "invalid path")
		return
	}

	// Both limits are checked before a single byte moves. The quota is
	// re-checked at commit, which is where correctness lives — this is the
	// early, honest "no" that saves the client from pushing a whole file
	// across the wire and the server from parking it on disk until the
	// session expires. Deliberately a check, not a reservation: overlapping
	// sessions may still overbook, and the commit-time check catches that.
	if err := h.quota.CheckHeadroom(r.Context(), claims.UserID, req.TotalSize); err != nil {
		writeError(w, http.StatusInsufficientStorage, ErrQuotaExceeded, "storage quota exceeded")
		return
	}
	// A quota is an accounting limit; the volume is a physical one. An
	// unreadable disk is not a reason to refuse an upload, so only a definite
	// answer counts against the request.
	if free, _, derr := h.DiskUsage(h.storage.DataPath()); derr == nil && free < req.TotalSize {
		writeError(w, http.StatusInsufficientStorage, ErrDiskFull, "not enough free space on the server")
		return
	}

	var contentHash []byte
	if req.ContentHash != "" {
		contentHash, _ = hex.DecodeString(req.ContentHash)
	}

	var mtime *time.Time
	if req.MTime != "" {
		if t, err := time.Parse(time.RFC3339, req.MTime); err == nil {
			mtime = &t
		}
	}

	sess, err := h.uploads.CreateSession(r.Context(), &model.UploadSession{
		UserID:      claims.UserID,
		SyncRootID:  rootID,
		Path:        cleanPath,
		TotalSize:   req.TotalSize,
		ContentHash: contentHash,
		MTime:       mtime,
		IfMatch:     req.IfMatch,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"upload_id":  sess.ID,
		"expires_at": sess.ExpiresAt,
	})
}

// PUT /api/v1/sync-roots/{rootID}/uploads/{uploadID}/chunks/{offset}
// Uploads a single chunk. Body is raw bytes; its length is the chunk's length
// (ADR-0066) — the client picks it per chunk, so there is no session-wide
// chunk_size to derive it from any more.
func (h *UploadHandler) UploadChunk(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	uploadID, err := uuid.Parse(chi.URLParam(r, "uploadID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidUploadID, "invalid upload id")
		return
	}
	offset, err := strconv.ParseInt(chi.URLParam(r, "offset"), 10, 64)
	if err != nil || offset < 0 {
		writeError(w, http.StatusBadRequest, ErrInvalidChunkOffset, "invalid chunk offset")
		return
	}
	// Content-Length is the chunk's declared length. Without it there is
	// nothing to bound the read by or to check the write against.
	if r.ContentLength <= 0 {
		writeError(w, http.StatusBadRequest, ErrChunkLengthRequired, "Content-Length required")
		return
	}
	if r.ContentLength > maxChunkLength {
		writeError(w, http.StatusBadRequest, ErrChunkTooLarge, "chunk exceeds the maximum chunk length")
		return
	}

	sess, err := h.uploads.GetSession(r.Context(), uploadID, claims.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrUploadSessionMixed, "upload session not found or expired")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	if offset+r.ContentLength > sess.TotalSize {
		writeError(w, http.StatusBadRequest, ErrChunkOutOfRange, "chunk range exceeds the upload's total size")
		return
	}
	if time.Now().After(sess.ExpiresAt) {
		writeError(w, http.StatusGone, ErrUploadSessionGone, "upload session expired")
		return
	}
	// Once a complete has been accepted the chunks belong to the assembly
	// worker: writing one now either changes bytes it is reading, or bytes it
	// has already committed.
	if sess.Status != model.UploadSessionOpen {
		writeError(w, http.StatusConflict, ErrUploadSessionClosed, "upload session no longer accepts chunks")
		return
	}

	declaredLength := r.ContentLength
	r.Body = http.MaxBytesReader(w, r.Body, declaredLength+1)
	written, err := h.storage.WriteChunkAt(uploadID, offset, r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrChunkStoreFailed, "failed to store chunk")
		return
	}

	// A chunk shorter than declared means the transfer was cut short.
	// Accepting it stores a hole that only surfaces as a silently truncated
	// file — one the server then hashes itself, so the stored hash agrees
	// with the corruption and nothing downstream notices. The range stays
	// unrecorded, so a retry can overwrite it.
	if written != declaredLength {
		writeError(w, http.StatusBadRequest, ErrChunkLengthMismatch,
			"chunk at offset "+strconv.FormatInt(offset, 10)+" has "+strconv.FormatInt(written, 10)+
				" bytes, expected "+strconv.FormatInt(declaredLength, 10))
		return
	}

	if _, err := h.uploads.AddRange(r.Context(), uploadID, offset, written); err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	w.WriteHeader(http.StatusOK)
}

// GET /api/v1/sync-roots/{rootID}/uploads/{uploadID}/status
// Reports which chunks have been received so a client can resume, and — once
// a complete has been accepted — how the assembly is going and how it ended.
// With ?wait=30s the request holds until the state changes, which keeps the
// normal case quasi-synchronous without any request outliving a proxy.
func (h *UploadHandler) UploadStatus(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	uploadID, err := uuid.Parse(chi.URLParam(r, "uploadID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidUploadID, "invalid upload id")
		return
	}

	sess, err := h.uploads.GetSession(r.Context(), uploadID, claims.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrUploadSessionNF, "upload session not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	// Only a session in the worker's hands has a state change worth waiting
	// for. One still collecting chunks answers at once — that is what a
	// resuming client is asking about — and a finished one has its answer.
	if wait := statusWait(r); wait > 0 && model.UploadSessionInFlight(sess.Status) {
		sess = h.awaitStateChange(r.Context(), sess, claims.UserID, wait)
	}

	writeJSON(w, http.StatusOK, uploadStatusBody(sess))
}

// statusWait reads the requested long-poll duration. A malformed value is no
// reason to refuse the request: the endpoint's other job — telling a resuming
// client which chunks are missing — works perfectly well without waiting.
func statusWait(r *http.Request) time.Duration {
	raw := r.URL.Query().Get("wait")
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0
	}
	if d > maxStatusWait {
		return maxStatusWait
	}
	return d
}

// awaitStateChange polls the session row until its state moves or the wait is
// spent, and returns whichever version it last saw. Polling rather than a
// notification channel: after a restart the session may be owned by a worker
// this request knows nothing about, and the row is the only shared truth.
func (h *UploadHandler) awaitStateChange(
	ctx context.Context, sess *model.UploadSession, userID uuid.UUID, wait time.Duration,
) *model.UploadSession {
	deadline := time.Now().Add(wait)
	ticker := time.NewTicker(statusPollPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return sess
		case <-ticker.C:
			cur, err := h.uploads.GetSession(ctx, sess.ID, userID)
			if err != nil {
				return sess
			}
			if cur.Status != sess.Status || time.Now().After(deadline) {
				return cur
			}
		}
	}
}

func uploadStatusBody(sess *model.UploadSession) map[string]any {
	missing := model.MissingRanges(sess.TotalSize, sess.ReceivedRanges)
	missingPairs := make([][2]int64, len(missing))
	for i, r := range missing {
		missingPairs[i] = [2]int64{r.Start, r.End}
	}

	body := map[string]any{
		"upload_id":      sess.ID,
		"status":         sess.Status,
		"missing_ranges": missingPairs,
		"expires_at":     sess.ExpiresAt,
	}

	switch sess.Status {
	case model.UploadSessionCompleted:
		body["result"] = uploadResultBody(sess)
	case model.UploadSessionFailed:
		code := ErrUploadFailed
		if sess.ErrorCode != nil {
			code = *sess.ErrorCode
		}
		body["error"] = map[string]any{"code": code}
	}
	return body
}

// uploadResultBody is the terminal answer as the status endpoint reports it.
// Same fields as the replayed complete response, nested one level down: here
// the outcome is data about the session, not the status of the request.
func uploadResultBody(sess *model.UploadSession) map[string]any {
	kind := model.UploadResultOK
	if sess.ResultKind != nil {
		kind = *sess.ResultKind
	}
	var serverVersion int64
	if sess.ResultServerVersion != nil {
		serverVersion = *sess.ResultServerVersion
	}

	res := map[string]any{
		"kind":           kind,
		"path":           sess.Path,
		"size":           sess.TotalSize,
		"content_hash":   hex.EncodeToString(sess.ResultContentHash),
		"server_version": serverVersion,
	}
	if sess.ResultFileID != nil {
		res["id"] = *sess.ResultFileID
	}
	if sess.ResultConflictPath != nil {
		res["conflict_path"] = *sess.ResultConflictPath
	}
	if sess.CompletedAt != nil {
		res["updated_at"] = *sess.CompletedAt
	}
	return res
}

// POST /api/v1/sync-roots/{rootID}/uploads/{uploadID}/complete
// Accepts the assembly and answers 202. The work — reading every chunk,
// hashing gigabytes, committing — runs in the assembly worker (ADR-0043); the
// client follows it through the status endpoint.
func (h *UploadHandler) CompleteUpload(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}
	uploadID, err := uuid.Parse(chi.URLParam(r, "uploadID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidUploadID, "invalid upload id")
		return
	}

	if _, err := h.syncRoots.GetSyncRoot(r.Context(), rootID, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
		return
	}

	sess, err := h.uploads.GetSession(r.Context(), uploadID, claims.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrUploadSessionMixed, "upload session not found or expired")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	if sess.SyncRootID != rootID {
		writeError(w, http.StatusBadRequest, ErrSessionWrongRoot, "session belongs to a different root")
		return
	}

	// A session that is no longer open answers from its own state: the verdict
	// if it has one, otherwise another 202. The client retries for hours, and
	// assembling a multi-gigabyte file again to reach a conclusion we already
	// hold is the expensive way to say it — after a successful complete the
	// chunks are gone anyway.
	if sess.Status != model.UploadSessionOpen {
		writeSessionState(w, sess)
		return
	}

	// The whole file must be covered.
	if missing := model.MissingRanges(sess.TotalSize, sess.ReceivedRanges); len(missing) > 0 {
		writeError(w, http.StatusConflict, ErrChunkMissing, "missing "+strconv.Itoa(len(missing))+" byte range(s)")
		return
	}

	// If-Match per HTTP header (RFC 7232). No header means the client states
	// no expectation, and a version mismatch then goes through the real
	// conflict path rather than a 409. What init recorded stands otherwise:
	// without it a chunked update of an existing file would read as a blind
	// write and materialise a full loser copy of a multi-gigabyte file.
	ifMatch, ok := ifMatchOverride(w, r)
	if !ok {
		return
	}

	queued, err := h.uploads.MarkQueued(r.Context(), uploadID, ifMatch)
	if errors.Is(err, repository.ErrConflict) {
		// Another complete won the race in the meantime. Answer from wherever
		// the session got to rather than from what we read a moment ago.
		if cur, gerr := h.uploads.GetSession(r.Context(), uploadID, claims.UserID); gerr == nil {
			writeSessionState(w, cur)
			return
		}
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	h.queue.Submit(assembler.Job{SessionID: queued.ID, UserID: queued.UserID})
	writeSessionState(w, queued)
}

// ifMatchOverride parses the If-Match header, if any. It reports false once it
// has answered the request itself.
func ifMatchOverride(w http.ResponseWriter, r *http.Request) (*int64, bool) {
	hdr := r.Header.Get("If-Match")
	if hdr == "" || hdr == "*" {
		return nil, true
	}
	v, err := strconv.ParseInt(hdr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrUploadFields, "invalid If-Match header")
		return nil, false
	}
	return &v, true
}

// writeSessionState answers a complete from the session row: 202 while the
// work is still ahead, the stored verdict once there is one.
func writeSessionState(w http.ResponseWriter, sess *model.UploadSession) {
	if model.UploadSessionInFlight(sess.Status) {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"upload_id": sess.ID,
			"status":    sess.Status,
		})
		return
	}
	writeStoredResult(w, sess)
}

// storedResultStatus maps a recorded failure back to the status it is answered
// with. Only terminal failures are recorded; anything unmapped is a bug rather
// than a case to guess at, and gets a 500.
var storedResultStatus = map[string]int{
	ErrContentHashMismatch:    http.StatusUnprocessableEntity,
	ErrAssembledSizeWrong:     http.StatusUnprocessableEntity,
	ErrChunkMissing:           http.StatusConflict,
	ErrQuotaExceeded:          http.StatusInsufficientStorage,
	errcode.AssemblyAbandoned: http.StatusInternalServerError,
}

// writeStoredResult answers from the session row. The body is rebuilt rather
// than stored verbatim — the client reads server_version and content_hash, and
// keeping HTTP shapes out of the database is worth the reconstruction.
func writeStoredResult(w http.ResponseWriter, sess *model.UploadSession) {
	if sess.Status == model.UploadSessionFailed {
		code := ErrUploadFailed
		if sess.ErrorCode != nil {
			code = *sess.ErrorCode
		}
		status, ok := storedResultStatus[code]
		if !ok {
			status = http.StatusInternalServerError
		}
		writeError(w, status, code, "upload session already failed")
		return
	}

	var serverVersion int64
	if sess.ResultServerVersion != nil {
		serverVersion = *sess.ResultServerVersion
	}
	kind := model.UploadResultOK
	if sess.ResultKind != nil {
		kind = *sess.ResultKind
	}

	switch kind {
	case model.UploadResultVersionMismatch:
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":          "version conflict",
			"server_version": serverVersion,
			"server_hash":    hex.EncodeToString(sess.ResultContentHash),
		})
	case model.UploadResultConflict:
		body := map[string]any{
			"error":          "version conflict",
			"server_version": serverVersion,
			"server_hash":    hex.EncodeToString(sess.ResultContentHash),
		}
		if sess.ResultConflictPath != nil {
			body["conflict_path"] = *sess.ResultConflictPath
		}
		writeJSON(w, http.StatusConflict, body)
	default:
		body := map[string]any{
			"path":           sess.Path,
			"size":           sess.TotalSize,
			"content_hash":   hex.EncodeToString(sess.ResultContentHash),
			"server_version": serverVersion,
		}
		if sess.ResultFileID != nil {
			body["id"] = *sess.ResultFileID
		}
		if sess.CompletedAt != nil {
			body["updated_at"] = *sess.CompletedAt
		}
		writeJSON(w, http.StatusOK, body)
	}
}
