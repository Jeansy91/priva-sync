package assembler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/delta"
	"privasync/server/internal/errcode"
	"privasync/server/internal/model"
	"privasync/server/internal/quota"
	"privasync/server/internal/repository"
	syncpkg "privasync/server/internal/sync"
)

// SessionStore is the slice of the upload repository the worker needs: the
// state transitions of one session, nothing else (ADR-0029).
type SessionStore interface {
	ClaimSession(ctx context.Context, sessionID uuid.UUID) (*model.UploadSession, error)
	MarkCompleted(ctx context.Context, sessionID uuid.UUID, res model.UploadSessionResult, retain time.Duration) error
	MarkFailed(ctx context.Context, sessionID uuid.UUID, code string, retain time.Duration) error
	MarkRequeued(ctx context.Context, sessionID uuid.UUID, code string) error
}

// Storage is the slice of the storage service the worker needs.
type Storage interface {
	FinalizeChunks(sessionID fmt.Stringer, userID uuid.UUID, rootID, finalPath string, tee io.Writer) (int64, []byte, error)
	DeleteFile(userID uuid.UUID, rootID, filePath string) error
	DeleteChunkDir(sessionID fmt.Stringer) error
}

// Committer is the Sync Coordinator seen from here. It is transport-free —
// identity travels in the command — which is what lets the commit happen
// outside a request at all.
type Committer interface {
	UploadFile(ctx context.Context, cmd syncpkg.UploadCmd) (syncpkg.UploadResult, error)
}

// Options are the knobs that are policy rather than wiring.
type Options struct {
	// MaxAttempts caps how often one session may be claimed. Strandings count:
	// a session that takes the server down with it must not be able to do so
	// forever.
	MaxAttempts int
	// Retain is how long a finished session row stays answerable.
	Retain time.Duration
	// Timeout bounds a single attempt.
	Timeout time.Duration
}

type Service struct {
	sessions SessionStore
	storage  Storage
	commit   Committer
	opts     Options
}

func NewService(sessions SessionStore, stor Storage, commit Committer, opts Options) *Service {
	return &Service{sessions: sessions, storage: stor, commit: commit, opts: opts}
}

// failure is one attempt's verdict. Permanent means the bytes on the server
// are what they are, so another attempt can only reach the same conclusion at
// the same cost; anything else deserves a retry.
type failure struct {
	code      string
	permanent bool
}

// Assemble is the Pool's runner: it takes ownership of one session, assembles
// it, commits it, and writes the verdict to the row. It reports whether the
// job deserves another attempt.
func (s *Service) Assemble(job Job) bool {
	// The worker runs on its own context, never a request's. The complete was
	// answered with 202 and its request is long gone — and before this moved
	// out of the handler, a proxy giving up after 100 seconds cancelled the
	// commit of a file that had taken eight minutes to assemble.
	ctx, cancel := context.WithTimeout(context.Background(), s.opts.Timeout)
	defer cancel()

	sess, err := s.sessions.ClaimSession(ctx, job.SessionID)
	if errors.Is(err, repository.ErrConflict) {
		// Not ours: another worker holds it, or it already has an answer.
		return false
	}
	if err != nil {
		slog.Error("assembler: claiming session failed", "session_id", job.SessionID, "error", err)
		return false
	}

	if sess.Attempts > s.opts.MaxAttempts {
		code := errcode.AssemblyAbandoned
		if sess.LastErrorCode != nil {
			code = *sess.LastErrorCode
		}
		s.fail(ctx, sess, code)
		return false
	}

	fail := s.attempt(ctx, sess)
	switch {
	case fail == nil:
		return false
	case fail.permanent, sess.Attempts >= s.opts.MaxAttempts:
		s.fail(ctx, sess, fail.code)
		return false
	default:
		if err := s.sessions.MarkRequeued(ctx, sess.ID, fail.code); err != nil {
			slog.Error("assembler: requeue failed", "session_id", sess.ID, "error", err)
			return false
		}
		slog.Warn("assembler: attempt failed, retrying",
			"session_id", sess.ID, "attempt", sess.Attempts, "code", fail.code)
		return true
	}
}

func (s *Service) attempt(ctx context.Context, sess *model.UploadSession) *failure {
	rootID := sess.SyncRootID.String()
	// The assembled bytes land beside the target and are renamed onto it only
	// once the version check has had its say, so a losing upload cannot
	// destroy the server's copy.
	stagingPath := sess.Path + ".staging-" + uuid.NewString()

	// The finalise pass reads the staged bytes exactly once. Teeing them
	// through a signature writer buys the delta cache for free; rereading a
	// 20 GB file for it is what this replaces. Below the delta
	// threshold no client is ever offered a delta, so there is nothing to fill.
	var sigw *delta.SignatureWriter
	var tee io.Writer
	if sess.TotalSize > delta.MinDeltaSize {
		sigw = delta.NewSignatureWriter(delta.BlockSize)
		tee = sigw
	}

	written, hash, err := s.storage.FinalizeChunks(sess.ID, sess.UserID, rootID, stagingPath, tee)
	if err != nil {
		slog.Error("assembler: assembly failed", "session_id", sess.ID, "error", err)
		if errors.Is(err, fs.ErrNotExist) {
			// A chunk that is gone does not come back; another attempt would
			// only reread whatever is left of it.
			return &failure{errcode.ChunksMissing, true}
		}
		return &failure{errcode.AssemblyFailed, false}
	}

	// The assembly must be exactly the size the session was opened for.
	// Anything else means chunks went missing or arrived truncated, and
	// committing it would replace a good server-side file with a broken one.
	if written != sess.TotalSize {
		s.discard(sess, rootID, stagingPath)
		slog.Error("assembler: assembled size mismatch",
			"session_id", sess.ID, "written", written, "want", sess.TotalSize)
		return &failure{errcode.AssembledSizeWrong, true}
	}

	// Without a client-stated hash there is nothing to check against — the
	// server would only be comparing its own arithmetic with itself.
	if len(sess.ContentHash) > 0 && !bytes.Equal(hash, sess.ContentHash) {
		s.discard(sess, rootID, stagingPath)
		return &failure{errcode.ContentHashMismatch, true}
	}

	mtime := time.Now()
	if sess.MTime != nil {
		mtime = *sess.MTime
	}

	var sigs []delta.Signature
	if sigw != nil {
		sigs = sigw.Signatures()
	}

	result, err := s.commit.UploadFile(ctx, syncpkg.UploadCmd{
		UserID:          sess.UserID,
		ActorUserID:     sess.UserID,
		SyncRootID:      sess.SyncRootID,
		Path:            sess.Path,
		StagingPath:     stagingPath,
		Size:            written,
		ContentHash:     hash,
		MTime:           mtime,
		IfMatch:         sess.IfMatch,
		BlockSignatures: sigs,
	})
	if err != nil {
		s.discard(sess, rootID, stagingPath)
		if errors.Is(err, quota.ErrExceeded) {
			return &failure{errcode.QuotaExceeded, true}
		}
		slog.Error("assembler: commit failed", "session_id", sess.ID, "error", err)
		return &failure{errcode.UploadFailed, false}
	}

	// A version conflict leaves the staging copy unused; a real conflict does
	// not — the coordinator turns it into the loser copy (ADR-0003).
	if result.Conflict == syncpkg.ConflictVersionMismatch {
		s.discard(sess, rootID, stagingPath)
	}

	if err := s.sessions.MarkCompleted(ctx, sess.ID, sessionResultOf(result), s.opts.Retain); err != nil {
		// The file is committed. Losing the record only means the client is
		// told less than it could be, so nothing here gets undone over it —
		// but it must be visible.
		slog.Error("assembler: recording the result failed", "session_id", sess.ID, "error", err)
	}
	if err := s.storage.DeleteChunkDir(sess.ID); err != nil {
		slog.Warn("assembler: chunk cleanup failed", "session_id", sess.ID, "error", err)
	}
	return nil
}

func (s *Service) discard(sess *model.UploadSession, rootID, stagingPath string) {
	if err := s.storage.DeleteFile(sess.UserID, rootID, stagingPath); err != nil {
		slog.Warn("assembler: staging cleanup failed", "session_id", sess.ID, "error", err)
	}
}

func (s *Service) fail(ctx context.Context, sess *model.UploadSession, code string) {
	if err := s.sessions.MarkFailed(ctx, sess.ID, code, s.opts.Retain); err != nil {
		slog.Error("assembler: recording the failure failed", "session_id", sess.ID, "error", err)
	}
	slog.Warn("assembler: upload failed for good", "session_id", sess.ID, "code", code)
}

// sessionResultOf reduces a coordinator result to what the session row keeps:
// which of the three outcomes it was, and the fields a replay must reproduce.
func sessionResultOf(result syncpkg.UploadResult) model.UploadSessionResult {
	res := model.UploadSessionResult{
		Kind:          model.UploadResultOK,
		FileID:        &result.File.ID,
		ServerVersion: &result.File.ServerVersion,
		ContentHash:   result.File.ContentHash,
	}
	switch result.Conflict {
	case syncpkg.ConflictVersionMismatch:
		res.Kind = model.UploadResultVersionMismatch
	case syncpkg.ConflictReal:
		res.Kind = model.UploadResultConflict
		path := result.ConflictPath
		res.ConflictPath = &path
	}
	return res
}
