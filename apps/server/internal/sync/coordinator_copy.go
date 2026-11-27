package sync

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"privasync/server/internal/bus"
	"privasync/server/internal/model"
	"privasync/server/internal/outbox"
	"privasync/server/internal/repository"
	sseevents "privasync/server/internal/sse/events"
)

// CopyCmd describes the duplication of an existing file or folder inside one
// sync root. Unlike a move, a copy adds bytes — the coordinator gates it on
// quota and books the copied size against the user. ActorUserID mirrors the
// other commands (recipient for mount writes, owner otherwise; see ADR-0013).
type CopyCmd struct {
	UserID      uuid.UUID
	ActorUserID uuid.UUID
	SyncRootID  uuid.UUID
	From        string
	To          string
}

// CopyResult carries the freshly created row (file copy) or the destination
// directory row (folder copy).
type CopyResult struct {
	File *model.File
}

// CopyFile drives the copy pipeline: authorize → resolve file-vs-directory →
// quota-gate → DB rows + storage copy + SSE, single commit. A live entry
// already at cmd.To yields repository.ErrConflict so the adapter can answer
// 409. quota.ErrExceeded surfaces verbatim for a 507. repository.ErrNotFound
// is returned when neither a row nor any descendant exists at cmd.From.
func (c *Impl) CopyFile(ctx context.Context, cmd CopyCmd) (CopyResult, error) {
	// Phase 1 — Authorize.
	root, err := c.syncRoots.GetSyncRoot(ctx, cmd.SyncRootID, cmd.UserID)
	if err != nil {
		return CopyResult{}, fmt.Errorf("authorize: %w", err)
	}

	// Phase 2 — Resolve source. A missing own row may still be an implicit
	// directory (children below it, no own row) — treat that as a folder copy.
	src, err := c.files.FindByPath(ctx, cmd.SyncRootID, cmd.From)
	if errors.Is(err, repository.ErrNotFound) {
		hasChildren, cerr := c.files.HasChildren(ctx, cmd.SyncRootID, cmd.From)
		if cerr != nil {
			return CopyResult{}, cerr
		}
		if hasChildren {
			return c.copyDirectory(ctx, cmd, root.ID)
		}
		return CopyResult{}, err
	}
	if err != nil {
		return CopyResult{}, err
	}
	if src.IsDir {
		return c.copyDirectory(ctx, cmd, root.ID)
	}

	// Phase 3 — Destination pre-flight + quota-gate.
	if dst, derr := c.files.FindByPath(ctx, cmd.SyncRootID, cmd.To); derr == nil && !dst.IsDeleted {
		return CopyResult{}, repository.ErrConflict
	}
	if err := c.quota.CheckHeadroom(ctx, cmd.UserID, src.Size); err != nil {
		return CopyResult{}, err
	}

	// Phase 4 — forge the new row, then TX: outbox copy + announce + book quota.
	actor := resolveActor(cmd.ActorUserID, cmd.UserID)
	copied, err := c.files.CopyFile(ctx, cmd.SyncRootID, cmd.From, cmd.To, actor)
	if errors.Is(err, repository.ErrConflict) {
		// Race: another client occupied cmd.To between pre-flight and INSERT.
		return CopyResult{}, repository.ErrConflict
	}
	if err != nil {
		return CopyResult{}, err
	}

	err = c.withMutationTx(ctx, func(tx pgx.Tx, batch *outbox.Batch) error {
		if err := batch.Add(CopyPayload{
			UserID: cmd.UserID, RootID: root.ID, From: cmd.From, To: cmd.To,
		}); err != nil {
			return err
		}
		if err := PublishDurable(batch, cmd.UserID, sseevents.FileAdded{
			RootID: root.ID, Path: copied.Path, File: newSSEFile(copied),
		}); err != nil {
			return err
		}
		if err := PublishDurable(batch, cmd.UserID, sseevents.QuotaUpdated{}); err != nil {
			return err
		}
		return c.bus.PublishCritical(ctx, tx, bus.FileUploaded{
			UserID: cmd.UserID, RootID: root.ID, FileID: copied.ID,
			Bytes: copied.Size, PrevBytes: 0,
		})
	})
	if err != nil {
		return CopyResult{}, err
	}
	return CopyResult{File: copied}, nil
}

// copyDirectory duplicates a folder subtree: it gates the whole subtree's byte
// sum against quota, forges the descendant rows (repository.CopyDirectory),
// enqueues one recursive storage copy, and announces the new folder via
// FileAdded. The byte sum is booked once as a FileUploaded so the user is
// charged for the full duplicate; over-quota fails closed before any write.
func (c *Impl) copyDirectory(ctx context.Context, cmd CopyCmd, rootID uuid.UUID) (CopyResult, error) {
	if dst, derr := c.files.FindByPath(ctx, cmd.SyncRootID, cmd.To); derr == nil && !dst.IsDeleted {
		return CopyResult{}, repository.ErrConflict
	}

	stats, err := c.files.DirectoryStats(ctx, cmd.SyncRootID, cmd.From)
	if err != nil {
		return CopyResult{}, err
	}
	if err := c.quota.CheckHeadroom(ctx, cmd.UserID, stats.TotalSize); err != nil {
		return CopyResult{}, err
	}

	actor := resolveActor(cmd.ActorUserID, cmd.UserID)
	dir, err := c.files.CopyDirectory(ctx, cmd.SyncRootID, cmd.From, cmd.To, actor)
	if errors.Is(err, repository.ErrConflict) {
		return CopyResult{}, repository.ErrConflict
	}
	if err != nil {
		return CopyResult{}, err
	}

	err = c.withMutationTx(ctx, func(tx pgx.Tx, batch *outbox.Batch) error {
		if err := batch.Add(CopyPayload{
			UserID: cmd.UserID, RootID: rootID, From: cmd.From, To: cmd.To,
		}); err != nil {
			return err
		}
		if err := PublishDurable(batch, cmd.UserID, sseevents.FileAdded{
			RootID: rootID, Path: dir.Path, File: newSSEFile(dir),
		}); err != nil {
			return err
		}
		if err := PublishDurable(batch, cmd.UserID, sseevents.QuotaUpdated{}); err != nil {
			return err
		}
		// Folder rows have no FileID worth booking individually; charge the
		// whole subtree to the user in one shot. UserID-only FileUploaded
		// with a zero FileID is fine — the quota subscriber keys off
		// Bytes/PrevBytes.
		if stats.TotalSize > 0 {
			return c.bus.PublishCritical(ctx, tx, bus.FileUploaded{
				UserID: cmd.UserID, RootID: rootID, FileID: dir.ID,
				Bytes: stats.TotalSize, PrevBytes: 0,
			})
		}
		return nil
	})
	if err != nil {
		return CopyResult{}, err
	}
	return CopyResult{File: dir}, nil
}
