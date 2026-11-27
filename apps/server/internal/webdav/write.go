package webdav

import (
	"context"
	"encoding/hex"
	"errors"
	"hash"
	"io/fs"
	"os"
	"path"
	"time"

	"github.com/google/uuid"
	gwebdav "golang.org/x/net/webdav"
	"lukechampine.com/blake3"

	"privasync/server/internal/repository"
	syncpkg "privasync/server/internal/sync"
)

// errVersionRace is returned when a WebDAV PUT keeps losing the optimistic
// version check across the bounded retry window.
var errVersionRace = errors.New("webdav: version race on overwrite")

// isWriteFlag reports whether an OpenFile flag set indicates a write/create
// (WebDAV PUT), as opposed to a read (GET/PROPFIND).
func isWriteFlag(flag int) bool {
	return flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC) != 0
}

// openForWrite stages a streaming PUT inside the target's sync root. The
// returned handle buffers bytes to the staging file while hashing, then on
// Close drives the bytes through the Sync Coordinator so the write is a
// first-class mutation (quota, files-row, SSE) — never a raw FS write
// (ADR-0018).
func (d *davFS) openForWrite(ctx context.Context, name string) (gwebdav.File, error) {
	name = cleanName(name)
	rootName, sub := splitRoot(name)
	if sub == "" {
		// Refusing to PUT onto a sync root itself.
		return nil, os.ErrInvalid
	}
	root, ok, err := d.findRootByName(ctx, rootName)
	if err != nil {
		return nil, err
	}
	if !ok {
		// Parent collection (the sync root) does not exist → 409 in handlePut.
		return nil, os.ErrNotExist
	}

	staging, tmp, err := d.h.stor.CreateStaging(d.userID, root.ID.String(), sub)
	if err != nil {
		return nil, err
	}
	return &writeHandle{
		ctx:     ctx,
		d:       d,
		rootID:  root.ID,
		target:  sub,
		staging: staging,
		tmp:     tmp,
		hasher:  blake3.New(32, nil),
	}, nil
}

// writeHandle is a write-only webdav.File that stages bytes and finalises them
// through the Sync Coordinator on Close.
type writeHandle struct {
	ctx     context.Context
	d       *davFS
	rootID  uuid.UUID
	target  string // slash path within the root, e.g. "/report.txt"
	staging string // slash path of the staging file within the root
	tmp     *os.File
	hasher  hash.Hash
	size    int64
	closed  bool
}

func (h *writeHandle) Write(p []byte) (int, error) {
	n, err := h.tmp.Write(p)
	if n > 0 {
		_, _ = h.hasher.Write(p[:n])
		h.size += int64(n)
	}
	return n, err
}

func (h *writeHandle) Close() error {
	if h.closed {
		return nil
	}
	h.closed = true

	if err := h.tmp.Sync(); err != nil {
		h.discardStaging()
		return err
	}
	if err := h.tmp.Close(); err != nil {
		h.discardStaging()
		return err
	}

	if err := h.commit(); err != nil {
		h.discardStaging()
		return err
	}
	return nil
}

// maxOverwriteRetries bounds the read-version → write loop for the rare case
// where a concurrent writer bumps the version between our read and write
// (ADR-0018: WebDAV PUT is last-writer-wins).
const maxOverwriteRetries = 3

// commit drives the staged bytes through the Sync Coordinator. WebDAV PUT has
// no optimistic-concurrency header, so an overwrite must not spawn a loser
// copy: we read the current server_version and pass it as If-Match, taking the
// coordinator's clean-overwrite path. A version race retries with the fresh
// version; the staging file survives a mismatch, so the retry reuses it.
func (h *writeHandle) commit() error {
	hash := h.hasher.Sum(nil)
	for attempt := 0; attempt < maxOverwriteRetries; attempt++ {
		ifMatch, err := h.currentVersion()
		if err != nil {
			return err
		}
		res, err := h.d.h.coord.UploadFile(h.ctx, syncpkg.UploadCmd{
			UserID:      h.d.userID,
			ActorUserID: h.d.userID,
			SyncRootID:  h.rootID,
			Path:        h.target,
			StagingPath: h.staging,
			Size:        h.size,
			ContentHash: hash,
			MTime:       time.Now(),
			IfMatch:     ifMatch,
		})
		if err != nil {
			return err
		}
		if res.Conflict == syncpkg.ConflictVersionMismatch {
			continue // lost a race; re-read version and retry (staging survives)
		}
		return nil
	}
	return errVersionRace
}

// currentVersion returns the live file's server_version as an If-Match value,
// or nil when no live file exists at the target (a genuine create).
func (h *writeHandle) currentVersion() (*int64, error) {
	f, err := h.d.h.files.FindByPath(h.ctx, h.rootID, h.target)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	v := f.ServerVersion
	return &v, nil
}

// discardStaging removes a staging file the coordinator never consumed.
func (h *writeHandle) discardStaging() {
	_ = h.d.h.stor.DeleteFile(h.d.userID, h.rootID.String(), h.staging)
}

func (h *writeHandle) Stat() (fs.FileInfo, error) {
	// Called after io.Copy, before Close: the hasher already holds every byte,
	// so the content-hash ETag is available for the PUT response.
	return fileInfo{
		name:    path.Base(h.target),
		size:    h.size,
		mode:    0o644,
		modTime: time.Now(),
		etag:    hex.EncodeToString(h.hasher.Sum(nil)),
	}, nil
}

func (h *writeHandle) Read([]byte) (int, error)           { return 0, errNotImplemented }
func (h *writeHandle) Seek(int64, int) (int64, error)     { return 0, errNotImplemented }
func (h *writeHandle) Readdir(int) ([]fs.FileInfo, error) { return nil, errNotImplemented }
