package webdav

import (
	"context"
	"io"
	"os"
	"strings"
	"time"

	"lukechampine.com/blake3"

	"privasync/server/internal/model"
	syncpkg "privasync/server/internal/sync"
)

// moveAcrossRoots implements a WebDAV MOVE whose source and destination live in
// different sync roots. The coordinator only renames within a single root, so
// this is copy-then-delete: every source file is re-uploaded into the
// destination root through the coordinator (quota, files-row, SSE) and the
// source subtree is then soft-deleted. The operation is not atomic — a failure
// mid-way can leave a partially-copied destination — which is inherent to a
// cross-root move (no filesystem offers an atomic cross-volume rename).
func (d *davFS) moveAcrossRoots(ctx context.Context, srcRoot *model.SyncRoot, srcSub string, dstRoot *model.SyncRoot, dstSub string) error {
	// The walk is ordered parent-first, so a directory is created before the
	// children copied into it, and it pages rather than loading the whole
	// subtree.
	seen := 0
	err := d.h.files.EachInSubtree(ctx, srcRoot.ID, srcSub, func(row model.File) error {
		seen++
		dstPath := dstSub + strings.TrimPrefix(row.Path, srcSub)
		if row.IsDir {
			if _, err := d.h.files.CreateDirectory(ctx, d.userID, dstRoot.ID, dstPath, time.Now()); err != nil {
				return err
			}
			return d.h.stor.MakeDir(d.userID, dstRoot.ID.String(), dstPath)
		}
		return d.copyFileAcross(ctx, srcRoot, row.Path, dstRoot, dstPath)
	})
	if err != nil {
		return err
	}
	if seen == 0 {
		// Every file and every directory has a row (ADR-0041); nothing here
		// means the source does not exist.
		return os.ErrNotExist
	}

	_, err = d.h.coord.DeleteSubtree(ctx, syncpkg.DeleteSubtreeCmd{
		UserID: d.userID, ActorUserID: d.userID, SyncRootID: srcRoot.ID, Path: srcSub,
	})
	return err
}

// copyFileAcross streams one file's bytes from the source root into a staging
// file in the destination root and finalises it through the coordinator.
func (d *davFS) copyFileAcross(ctx context.Context, srcRoot *model.SyncRoot, srcPath string, dstRoot *model.SyncRoot, dstPath string) error {
	in, err := d.h.stor.Open(d.userID, srcRoot.ID.String(), srcPath)
	if err != nil {
		return err
	}
	defer in.Close()

	staging, tmp, err := d.h.stor.CreateStaging(d.userID, dstRoot.ID.String(), dstPath)
	if err != nil {
		return err
	}
	hasher := blake3.New(32, nil)
	size, err := io.Copy(io.MultiWriter(tmp, hasher), in)
	if err != nil {
		tmp.Close()
		_ = d.h.stor.DeleteFile(d.userID, dstRoot.ID.String(), staging)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		_ = d.h.stor.DeleteFile(d.userID, dstRoot.ID.String(), staging)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = d.h.stor.DeleteFile(d.userID, dstRoot.ID.String(), staging)
		return err
	}

	if _, err := d.h.coord.UploadFile(ctx, syncpkg.UploadCmd{
		UserID:      d.userID,
		ActorUserID: d.userID,
		SyncRootID:  dstRoot.ID,
		Path:        dstPath,
		StagingPath: staging,
		Size:        size,
		ContentHash: hasher.Sum(nil),
		MTime:       time.Now(),
	}); err != nil {
		_ = d.h.stor.DeleteFile(d.userID, dstRoot.ID.String(), staging)
		return err
	}
	return nil
}
