package handler

import (
	"archive/zip"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

// Anonymous endpoints under /api/v1/public/shares/{token}. They serve
// share_type='public' links without any auth — only the unguessable
// token is the credential. Mount-shares (share_type='user') are served
// by shared_mounts.go through the authenticated API instead.

type publicEntry struct {
	Name  string    `json:"name"`
	Path  string    `json:"path"`
	IsDir bool      `json:"is_dir"`
	Size  int64     `json:"size"`
	MTime time.Time `json:"mtime"`
}

type publicShareView struct {
	ShareType string        `json:"share_type"`
	Path      string        `json:"path"`
	IsFile    bool          `json:"is_file"`
	CanWrite  bool          `json:"can_write"`
	Entries   []publicEntry `json:"entries"`
}

// GET /api/v1/public/shares/{token}
func (h *ShareHandler) PublicShare(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")

	share, err := h.shares.FindByToken(r.Context(), token)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrShareNotFound, "share not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	if publicLinkExpired(share, token) {
		writeError(w, http.StatusGone, ErrShareExpired, "share expired")
		return
	}

	resp := toShareResponse(share)
	resp.CanWrite = publicTokenCanWrite(share, token)
	// Anonymous endpoint: echoing the tokens would hand a read-link holder
	// the write token. The caller already knows the token they used.
	resp.Token, resp.ReadToken, resp.WriteToken = nil, nil, nil
	writeJSON(w, http.StatusOK, resp)
}

// publicTokenCanWrite reports whether the token the caller presented is the
// share's write token. Only directory public-shares have a write token
// (ADR-0015); for read tokens and file shares this is always false.
func publicTokenCanWrite(share *model.Share, token string) bool {
	return share.WriteToken != nil && *share.WriteToken == token
}

// publicLinkExpired reports whether the presented link is past its limit.
// Each link carries its own expiry, chosen at link creation; the
// aggregate-level expires_at (legacy share creation) still covers both.
func publicLinkExpired(share *model.Share, token string) bool {
	now := time.Now()
	if share.ExpiresAt != nil && now.After(*share.ExpiresAt) {
		return true
	}
	exp := share.ReadExpiresAt
	if publicTokenCanWrite(share, token) {
		exp = share.WriteExpiresAt
	}
	return exp != nil && now.After(*exp)
}

// GET /api/v1/public/shares/{token}/entries[?sub=<rel>]
//
// Anonymous directory/file lookup for a public share token. Directory shares
// return the direct children of the current sub-path (relative to share.path);
// file shares return a single entry. `sub` is sanitised; any path escape
// outside share.path produces 403.
func (h *ShareHandler) PublicShareEntries(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	share, err := h.shares.FindByToken(r.Context(), token)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrShareNotFound, "share not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	if publicLinkExpired(share, token) {
		writeError(w, http.StatusGone, ErrShareExpired, "share expired")
		return
	}
	if share.SyncRootID == nil {
		writeError(w, http.StatusNotFound, ErrShareNotFound, "share not found")
		return
	}

	// File-share? FindByPath on share.Path; if the file exists at the
	// exact path, treat the share as a file-share.
	dbFile, fileErr := h.files.FindByPath(r.Context(), *share.SyncRootID, share.Path)
	isFile := isLiveFile(dbFile, fileErr)

	view := publicShareView{
		ShareType: string(share.ShareType),
		Path:      share.Path,
		CanWrite:  publicTokenCanWrite(share, token),
		IsFile:    isFile,
		Entries:   []publicEntry{},
	}

	if isFile {
		view.Entries = append(view.Entries, publicEntry{
			Name:  path.Base(dbFile.Path),
			Path:  dbFile.Path,
			IsDir: false,
			Size:  dbFile.Size,
			MTime: dbFile.MTime,
		})
		writeJSON(w, http.StatusOK, view)
		return
	}

	// Directory share — optional sub-path must stay under share.Path.
	sub := strings.TrimSpace(r.URL.Query().Get("sub"))
	currentPath := share.Path
	if sub != "" && sub != "/" {
		joined, ok := joinUnderShareRoot(share.Path, sub)
		if !ok {
			writeError(w, http.StatusForbidden, ErrInvalidPath, "sub path escapes share")
			return
		}
		currentPath = joined
	}

	prefix := currentPath
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	entries, err := h.files.ListDirectoryLevel(r.Context(), repository.ListDirectoryLevelParams{
		SyncRootID: *share.SyncRootID,
		PathPrefix: prefix,
		Limit:      1000,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	for _, e := range entries {
		view.Entries = append(view.Entries, publicEntry{
			Name:  strings.TrimPrefix(e.Path, prefix),
			Path:  e.Path,
			IsDir: e.IsDir,
			Size:  e.Size,
			MTime: e.MTime,
		})
	}
	view.Path = currentPath
	writeJSON(w, http.StatusOK, view)
}

// GET /api/v1/public/shares/{token}/download[?sub=<rel>]
//
// Streams a single file. File shares ignore `sub`; directory shares require
// `sub` to point at a path inside the share root that resolves to a file
// (not a sub-directory).
func (h *ShareHandler) PublicShareDownload(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	share, err := h.shares.FindByToken(r.Context(), token)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, ErrShareNotFound, "share not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	if publicLinkExpired(share, token) {
		writeError(w, http.StatusGone, ErrShareExpired, "share expired")
		return
	}
	if share.SyncRootID == nil {
		writeError(w, http.StatusNotFound, ErrShareNotFound, "share not found")
		return
	}

	// Resolve target: share.Path itself (file or dir), or a sub-path below it.
	target := share.Path
	if sub := strings.TrimSpace(r.URL.Query().Get("sub")); sub != "" && sub != "/" {
		joined, ok := joinUnderShareRoot(share.Path, sub)
		if !ok {
			writeError(w, http.StatusForbidden, ErrInvalidPath, "sub path escapes share")
			return
		}
		target = joined
	}

	// Single file? FindByPath at the exact path — otherwise directory ZIP.
	if dbFile, err := h.files.FindByPath(r.Context(), *share.SyncRootID, target); isLiveFile(dbFile, err) {
		w.Header().Set("Content-Disposition", contentDispositionAttachment(path.Base(target)))
		w.Header().Set("X-Content-Hash", hex.EncodeToString(dbFile.ContentHash))
		if err := h.storage.ServeFile(share.UserID, share.SyncRootID.String(), target, w); err != nil {
			writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
		}
		return
	}

	h.streamShareDirZip(w, r, share, target)
}

// streamShareDirZip packs all non-deleted files under `dir` (path within
// share.SyncRoot) into a ZIP stream. Archive paths are relative to `dir` so
// the recipient cannot see the share-internal layout.
func (h *ShareHandler) streamShareDirZip(
	w http.ResponseWriter,
	r *http.Request,
	share *model.Share,
	dir string,
) {
	prefix := dir
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	// The archive is written as the subtree is walked, so its memory cost is
	// one page of rows rather than the whole tree.
	//
	// Headers go out with the first entry, not before the walk: until one
	// file has been found there is still a 404 to send, and a directory
	// holding nothing must not deliver an empty ZIP. Once bytes are on the
	// wire a failure can only be logged and the stream cut -- the status line
	// is long gone.
	var (
		zw      *zip.Writer
		flusher http.Flusher
	)
	defer func() {
		if zw != nil {
			_ = zw.Close()
		}
	}()

	err := h.files.EachInSubtree(r.Context(), *share.SyncRootID, dir, func(f model.File) error {
		if f.IsDir || !strings.HasPrefix(f.Path, prefix) {
			return nil
		}
		entryName := strings.TrimPrefix(f.Path, prefix)
		if entryName == "" {
			return nil
		}

		if zw == nil {
			zw, flusher = startZipResponse(w, path.Base(dir))
		}

		entry, err := zw.CreateHeader(&zip.FileHeader{
			Name:     entryName,
			Method:   zip.Deflate,
			Modified: f.MTime,
		})
		if err != nil {
			return fmt.Errorf("zip header for %s: %w", f.Path, err)
		}
		if err := h.storage.ServeFile(share.UserID, share.SyncRootID.String(), f.Path, entry); err != nil {
			return fmt.Errorf("serving %s: %w", f.Path, err)
		}
		if flusher != nil {
			_ = zw.Flush()
			flusher.Flush()
		}
		return nil
	})

	switch {
	case err != nil && zw != nil:
		slog.Warn("share-zip: stream aborted mid-archive", "dir", dir, "error", err)
	case err != nil:
		slog.Error("share-zip: walk failed", "dir", dir, "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
	case zw == nil:
		writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
	}
}
