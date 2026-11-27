package webdav

import (
	"context"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	gwebdav "golang.org/x/net/webdav"

	"privasync/server/internal/model"
	"privasync/server/internal/repository"
	syncpkg "privasync/server/internal/sync"
)

// errNotImplemented guards operations that are invalid for a given node type
// (e.g. Read on a directory handle, Readdir on a file handle).
var errNotImplemented = errors.New("webdav: operation not valid for this node")

// davFS is a per-request webdav.FileSystem bound to one authenticated user.
// Reads project from the `files` repository; mutations flow through the Sync
// Coordinator (ADR-0018). The top level of the namespace maps to the user's
// Sync Roots addressed by name.
type davFS struct {
	h      *Handler
	userID uuid.UUID
}

func (h *Handler) newFS(userIDStr string) *davFS {
	uid, _ := uuid.Parse(userIDStr)
	return &davFS{h: h, userID: uid}
}

// currentETag returns the strong validator for the resource at name and
// whether it exists, for RFC 7232 precondition evaluation. Directories and
// the namespace root exist but carry no content hash.
func (d *davFS) currentETag(ctx context.Context, name string) (etag string, exists bool, err error) {
	res, err := d.resolve(ctx, name)
	if err != nil {
		return "", false, err
	}
	switch res.kind {
	case kindFile:
		return `"` + hex.EncodeToString(res.file.ContentHash) + `"`, true, nil
	case kindDir, kindRootList:
		return "", true, nil
	default:
		return "", false, nil
	}
}

// cleanName normalises a WebDAV path to a slash-rooted, slash-trimmed form.
// "/" stays "/"; "/Photos/" becomes "/Photos".
func cleanName(name string) string {
	c := path.Clean("/" + strings.TrimPrefix(name, "/"))
	return c
}

// nodeKind classifies what a cleaned WebDAV path resolves to.
type nodeKind int

const (
	kindNotExist nodeKind = iota
	kindRootList          // the namespace root "/", listing the user's sync roots
	kindDir               // a sync root, or a directory (explicit or implicit) within one
	kindFile              // an existing file row
)

// resolved is the outcome of mapping a WebDAV path onto the file model.
type resolved struct {
	root *model.SyncRoot
	sub  string      // path within the root; "" for the root directory itself
	file *model.File // non-nil only for kindFile
	kind nodeKind
}

// displayName is the base name used for the resolved node's FileInfo.
func (r resolved) displayName() string {
	switch r.kind {
	case kindRootList:
		return "/"
	case kindDir:
		if r.sub == "" {
			return r.root.Name
		}
		return path.Base(r.sub)
	case kindFile:
		return path.Base(r.file.Path)
	default:
		return ""
	}
}

// resolve maps a WebDAV path onto a sync root + sub-path, distinguishing the
// namespace root, a directory (explicit row or implicit from descendants), and
// a file. Root names are the user-facing namespace; the internal UUID never
// appears in WebDAV paths (ADR-0018).
func (d *davFS) resolve(ctx context.Context, name string) (resolved, error) {
	name = cleanName(name)
	if name == "/" {
		return resolved{kind: kindRootList}, nil
	}
	rootName, sub := splitRoot(name)
	root, ok, err := d.findRootByName(ctx, rootName)
	if err != nil {
		return resolved{}, err
	}
	if !ok {
		return resolved{}, nil
	}
	if sub == "" {
		return resolved{root: root, kind: kindDir}, nil
	}
	f, err := d.h.files.FindByPath(ctx, root.ID, sub)
	if err == nil {
		if f.IsDir {
			return resolved{root: root, sub: sub, kind: kindDir}, nil
		}
		return resolved{root: root, sub: sub, file: f, kind: kindFile}, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return resolved{}, err
	}
	// No explicit row: it may still be an implicit directory derived from
	// deeper file paths.
	has, err := d.h.files.HasChildren(ctx, root.ID, sub)
	if err != nil {
		return resolved{}, err
	}
	if has {
		return resolved{root: root, sub: sub, kind: kindDir}, nil
	}
	return resolved{}, nil
}

func (d *davFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	r, err := d.resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	switch r.kind {
	case kindRootList, kindDir:
		return dirInfo(r.displayName()), nil
	case kindFile:
		return fileInfoFromRow(r.file), nil
	default:
		return nil, os.ErrNotExist
	}
}

// splitRoot separates a cleaned "/RootName/sub/path" into ("RootName",
// "/sub/path"). For "/RootName" the sub-path is "".
func splitRoot(name string) (rootName, sub string) {
	trimmed := strings.TrimPrefix(name, "/")
	if i := strings.IndexByte(trimmed, '/'); i >= 0 {
		return trimmed[:i], trimmed[i:]
	}
	return trimmed, ""
}

// findRootByName resolves a top-level path segment to one of the user's Sync
// Roots.
func (d *davFS) findRootByName(ctx context.Context, name string) (*model.SyncRoot, bool, error) {
	roots, err := d.h.syncRoots.ListSyncRoots(ctx, d.userID)
	if err != nil {
		return nil, false, err
	}
	for i := range roots {
		if roots[i].Name == name {
			return &roots[i], true, nil
		}
	}
	return nil, false, nil
}

func (d *davFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (gwebdav.File, error) {
	if isWriteFlag(flag) {
		return d.openForWrite(ctx, name)
	}
	r, err := d.resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	switch r.kind {
	case kindRootList:
		infos, err := d.rootInfos(ctx)
		if err != nil {
			return nil, err
		}
		return &dirHandle{name: "/", infos: infos}, nil
	case kindDir:
		infos, err := d.dirInfos(ctx, r.root.ID, r.sub)
		if err != nil {
			return nil, err
		}
		return &dirHandle{name: r.displayName(), infos: infos}, nil
	case kindFile:
		osf, err := d.h.stor.Open(d.userID, r.root.ID.String(), r.sub)
		if err != nil {
			return nil, err
		}
		return &fileHandle{f: osf, info: fileInfoFromRow(r.file)}, nil
	default:
		return nil, os.ErrNotExist
	}
}

const dirPageSize = 1000

// dirInfos lists the immediate children under sub within a sync root, paging
// through the keyset listing. sub is "" for the root itself.
func (d *davFS) dirInfos(ctx context.Context, rootID uuid.UUID, sub string) ([]fs.FileInfo, error) {
	prefix := sub
	if prefix == "" {
		prefix = "/"
	}
	var (
		infos  []fs.FileInfo
		cursor string
	)
	for {
		entries, err := d.h.files.ListDirectoryLevel(ctx, repository.ListDirectoryLevelParams{
			SyncRootID: rootID,
			PathPrefix: prefix,
			Cursor:     cursor,
			Limit:      dirPageSize,
		})
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir {
				infos = append(infos, dirInfo(e.Path))
			} else {
				infos = append(infos, fileInfoFromRow(&e))
			}
		}
		if len(entries) < dirPageSize {
			break
		}
		cursor = path.Base(entries[len(entries)-1].Path)
	}
	return infos, nil
}

func (d *davFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	name = cleanName(name)
	rootName, sub := splitRoot(name)
	if sub == "" {
		// Cannot MKCOL a sync root through WebDAV.
		return os.ErrExist
	}
	root, ok, err := d.findRootByName(ctx, rootName)
	if err != nil {
		return err
	}
	if !ok {
		return os.ErrNotExist
	}
	// Shared with the REST handlers — DB row plus on-disk dir — and does not
	// route through the coordinator (ADR-0018).
	_, mkdirErr, err := repository.CreateDirectoryWithStorage(
		ctx, d.h.files, d.h.stor, d.userID, root.ID, sub, time.Now(),
	)
	if err != nil {
		return err
	}
	return mkdirErr
}

func (d *davFS) RemoveAll(ctx context.Context, name string) error {
	r, err := d.resolve(ctx, name)
	if err != nil {
		return err
	}
	switch r.kind {
	case kindFile:
		_, err := d.h.coord.DeleteFile(ctx, syncpkg.DeleteCmd{
			UserID: d.userID, ActorUserID: d.userID, SyncRootID: r.root.ID, Path: r.sub,
		})
		return err
	case kindDir:
		if r.sub == "" {
			// Refuse to delete a whole sync root through WebDAV.
			return os.ErrPermission
		}
		_, err := d.h.coord.DeleteSubtree(ctx, syncpkg.DeleteSubtreeCmd{
			UserID: d.userID, ActorUserID: d.userID, SyncRootID: r.root.ID, Path: r.sub,
		})
		return err
	case kindRootList:
		return os.ErrPermission
	default:
		// Already gone — RemoveAll is idempotent.
		return nil
	}
}

func (d *davFS) Rename(ctx context.Context, oldName, newName string) error {
	srcRoot, srcSub, ok, err := d.locate(ctx, oldName)
	if err != nil {
		return err
	}
	if !ok || srcSub == "" {
		return os.ErrNotExist
	}
	dstRoot, dstSub, ok, err := d.locate(ctx, newName)
	if err != nil {
		return err
	}
	if !ok || dstSub == "" {
		return os.ErrInvalid
	}
	if srcRoot.ID != dstRoot.ID {
		// The coordinator renames within one root only. A cross-root move is
		// honest copy-then-delete (no atomic cross-root rename exists).
		return d.moveAcrossRoots(ctx, srcRoot, srcSub, dstRoot, dstSub)
	}
	_, err = d.h.coord.MoveFile(ctx, syncpkg.MoveCmd{
		UserID: d.userID, ActorUserID: d.userID, SyncRootID: srcRoot.ID,
		From: srcSub, To: dstSub,
	})
	return err
}

// locate splits a WebDAV path into its sync root and sub-path without
// requiring the target to exist (used by Rename for the destination).
func (d *davFS) locate(ctx context.Context, name string) (*model.SyncRoot, string, bool, error) {
	rootName, sub := splitRoot(cleanName(name))
	root, ok, err := d.findRootByName(ctx, rootName)
	return root, sub, ok, err
}

// rootInfos lists the user's Sync Roots as directory entries named after the
// root, hiding the internal sync-root UUID from the namespace.
func (d *davFS) rootInfos(ctx context.Context) ([]fs.FileInfo, error) {
	roots, err := d.h.syncRoots.ListSyncRoots(ctx, d.userID)
	if err != nil {
		return nil, err
	}
	infos := make([]fs.FileInfo, 0, len(roots))
	for _, r := range roots {
		infos = append(infos, dirInfo(r.Name))
	}
	return infos, nil
}
