package webdav

import (
	"context"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path"
	"time"

	gwebdav "golang.org/x/net/webdav"

	"privasync/server/internal/model"
)

// ─── FileInfo ────────────────────────────────────────────────────────────────

type fileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	isDir   bool
	etag    string // hex BLAKE3 content hash; empty for directories
}

func dirInfo(name string) fileInfo {
	return fileInfo{name: path.Base(name), mode: os.ModeDir | 0o755, modTime: time.Now(), isDir: true}
}

// fileInfoFromRow lifts a file row into an os.FileInfo.
func fileInfoFromRow(f *model.File) fileInfo {
	return fileInfo{
		name:    path.Base(f.Path),
		size:    f.Size,
		mode:    0o644,
		modTime: f.MTime,
		etag:    hex.EncodeToString(f.ContentHash),
	}
}

func (fi fileInfo) Name() string       { return fi.name }
func (fi fileInfo) Size() int64        { return fi.size }
func (fi fileInfo) Mode() os.FileMode  { return fi.mode }
func (fi fileInfo) ModTime() time.Time { return fi.modTime }
func (fi fileInfo) IsDir() bool        { return fi.isDir }
func (fi fileInfo) Sys() any           { return nil }

// ETag exposes the BLAKE3 content hash as a strong validator so GET responses
// are cacheable and If-Match/If-None-Match reflect real content changes
// (RFC 7232). Directories have no content hash and fall back to the library
// default.
func (fi fileInfo) ETag(ctx context.Context) (string, error) {
	if fi.etag == "" {
		return "", gwebdav.ErrNotImplemented
	}
	return `"` + fi.etag + `"`, nil
}

// ─── Directory handle ─────────────────────────────────────────────────────────

// dirHandle is a read-only webdav.File over a fixed slice of child infos,
// produced once at OpenFile time (which has a context). Readdir pages through
// it the way os.File does.
type dirHandle struct {
	name  string
	infos []fs.FileInfo
	pos   int
}

func (h *dirHandle) Readdir(count int) ([]fs.FileInfo, error) {
	if h.pos >= len(h.infos) {
		if count <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	end := len(h.infos)
	if count > 0 && h.pos+count < end {
		end = h.pos + count
	}
	out := make([]fs.FileInfo, end-h.pos)
	copy(out, h.infos[h.pos:end])
	h.pos = end
	return out, nil
}

func (h *dirHandle) Stat() (fs.FileInfo, error)     { return dirInfo(h.name), nil }
func (h *dirHandle) Close() error                   { return nil }
func (h *dirHandle) Read([]byte) (int, error)       { return 0, errNotImplemented }
func (h *dirHandle) Write([]byte) (int, error)      { return 0, errNotImplemented }
func (h *dirHandle) Seek(int64, int) (int64, error) { return 0, errNotImplemented }

// ─── File handle ──────────────────────────────────────────────────────────────

// fileHandle is a read-only, seekable webdav.File backed by the on-disk bytes
// of a file row. Writes use writeHandle instead (see write.go).
type fileHandle struct {
	f    *os.File
	info fileInfo
}

func (h *fileHandle) Read(p []byte) (int, error)           { return h.f.Read(p) }
func (h *fileHandle) Seek(off int64, w int) (int64, error) { return h.f.Seek(off, w) }
func (h *fileHandle) Close() error                         { return h.f.Close() }
func (h *fileHandle) Stat() (fs.FileInfo, error)           { return h.info, nil }
func (h *fileHandle) Write([]byte) (int, error)            { return 0, errNotImplemented }
func (h *fileHandle) Readdir(int) ([]fs.FileInfo, error)   { return nil, errNotImplemented }
