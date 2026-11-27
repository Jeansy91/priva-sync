package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"privasync/server/internal/bus"
	"privasync/server/internal/model"
	"privasync/server/internal/outbox"
	"privasync/server/internal/repository"
	sseevents "privasync/server/internal/sse/events"
	"privasync/server/internal/storage"
)

// DeleteCmd describes a soft-delete of a single file. Quota-neutral on the
// "headroom" axis: bytes move from Used into Trash, total stays.
type DeleteCmd struct {
	UserID      uuid.UUID
	ActorUserID uuid.UUID // ADR-0013, same semantics as on UploadCmd
	SyncRootID  uuid.UUID
	Path        string
	// IfMatch mirrors UploadCmd/MoveCmd semantics (RFC 7232): when set, the
	// delete only goes through if the row is still at that version. Without
	// it a delete is a blind write — a client that went offline, deleted
	// locally and came back would remove someone else's newer edit with no
	// conflict surfacing anywhere. nil keeps the unconditional behaviour, so
	// older clients are unaffected.
	IfMatch *int64
}

// DeleteResult tells the adapter what happened. File is the post-delete row
// (IsDeleted=true, TrashPath set) on the happy path, or the current
// server-side row when Conflict says the version moved on.
type DeleteResult struct {
	File     *model.File
	Conflict ConflictKind
}

// DeleteSubtreeCmd describes a recursive soft-delete of every descendant at
// or below Path. The coordinator publishes one FileTrashed per file row so
// the quota subscriber accumulates the byte movement.
type DeleteSubtreeCmd struct {
	UserID      uuid.UUID
	ActorUserID uuid.UUID // ADR-0013, same semantics as on UploadCmd
	SyncRootID  uuid.UUID
	Path        string
}

// DeleteSubtreeResult counts what was soft-deleted (files only, no dirs).
//
// It used to carry every trashed row, which made a cascade over a large
// subtree hold the whole subtree in memory a second time. The only
// caller sums the sizes and takes the length for an audit entry, so that is
// what it returns.
type DeleteSubtreeResult struct {
	Count int
	Bytes int64
}

// DeleteSyncRootCmd describes the destructive removal of an entire sync root.
// The coordinator owns the TX so the cascade-delete and the quota release
// commit together via the SyncRootDeleted bus event.
type DeleteSyncRootCmd struct {
	UserID     uuid.UUID
	SyncRootID uuid.UUID
}

// DeleteSyncRootResult carries the byte sizes released so callers (handlers,
// SSE) can echo the same numbers without re-querying. RevokedShares lists
// every user-share that was cascaded so the handler can publish
// ShareRevoked SSE events to the recipients. See ADR-0010.
type DeleteSyncRootResult struct {
	UsedBytes     int64
	TrashBytes    int64
	RevokedShares []repository.DeletedShare
}

// DeleteFile soft-deletes a single file through the outbox pipeline.
// Directory cascade is the adapter's job — this method handles one row.
func (c *Impl) DeleteFile(ctx context.Context, cmd DeleteCmd) (DeleteResult, error) {
	root, err := c.syncRoots.GetSyncRoot(ctx, cmd.SyncRootID, cmd.UserID)
	if err != nil {
		return DeleteResult{}, fmt.Errorf("authorize: %w", err)
	}

	existing, err := c.files.FindByPath(ctx, cmd.SyncRootID, cmd.Path)
	if err != nil {
		return DeleteResult{}, err
	}
	if existing.IsDeleted {
		return DeleteResult{}, repository.ErrNotFound
	}
	// Only a client that stated which version it means gets the guarantee;
	// one that sends no If-Match keeps the historical unconditional delete.
	if cmd.IfMatch != nil && existing.ServerVersion != *cmd.IfMatch {
		return DeleteResult{File: existing, Conflict: ConflictVersionMismatch}, nil
	}

	trashPath := storage.BuildTrashPath(cmd.Path, time.Now())
	actor := resolveActor(cmd.ActorUserID, cmd.UserID)

	var deleted *model.File
	err = c.withMutationTx(ctx, func(tx pgx.Tx, batch *outbox.Batch) error {
		var err error
		deleted, err = c.trash.SoftDelete(ctx, cmd.SyncRootID, cmd.Path, trashPath, actor)
		if err != nil {
			return err
		}
		if err := batch.Add(TrashPayload{
			UserID: cmd.UserID, RootID: root.ID, From: cmd.Path, To: trashPath,
		}); err != nil {
			return err
		}
		if err := PublishDurable(batch, cmd.UserID, sseevents.FileDeleted{
			RootID: root.ID, FileID: deleted.ID, Path: deleted.Path,
		}); err != nil {
			return err
		}
		if err := PublishDurable(batch, cmd.UserID, sseevents.TrashAdded{
			RootID: root.ID, FileID: deleted.ID, Path: deleted.Path,
			Size: deleted.Size, TrashPath: trashPath,
		}); err != nil {
			return err
		}
		if err := PublishDurable(batch, cmd.UserID, sseevents.QuotaUpdated{}); err != nil {
			return err
		}
		return c.bus.PublishCritical(ctx, tx, bus.FileTrashed{
			UserID: cmd.UserID, RootID: root.ID, FileID: deleted.ID, Bytes: deleted.Size,
		})
	})
	if err != nil {
		return DeleteResult{}, err
	}
	return DeleteResult{File: deleted}, nil
}

// DeleteSubtree cascade-trashes every descendant at or below cmd.Path. Each
// file row goes through the same TX shape as DeleteFile (SoftDelete + outbox
// trash + SSE + bus.FileTrashed). Directory rows are MarkDeleted with an SSE
// notification but no quota event (no bytes). Per-row atomicity matches the
// previous handler behaviour: a mid-loop failure may leave a partial subtree
// soft-deleted, which the caller can re-issue idempotently.
func (c *Impl) DeleteSubtree(ctx context.Context, cmd DeleteSubtreeCmd) (DeleteSubtreeResult, error) {
	root, err := c.syncRoots.GetSyncRoot(ctx, cmd.SyncRootID, cmd.UserID)
	if err != nil {
		return DeleteSubtreeResult{}, fmt.Errorf("authorize: %w", err)
	}
	// Walked page by page rather than loaded whole: the cascade soft-deletes
	// as it goes, so rows leave the result set behind the cursor and the
	// memory cost stays one page however large the subtree is.
	actor := resolveActor(cmd.ActorUserID, cmd.UserID)
	var res DeleteSubtreeResult
	err = c.files.EachInSubtree(ctx, cmd.SyncRootID, cmd.Path, func(row model.File) error {
		if row.IsDir {
			return c.deleteSubtreeDir(ctx, root.ID, cmd.UserID, row)
		}
		deleted, err := c.deleteSubtreeFile(ctx, root.ID, cmd.UserID, actor, row)
		if err != nil {
			return err
		}
		res.Count++
		res.Bytes += deleted.Size
		return nil
	})
	if err != nil {
		return DeleteSubtreeResult{}, err
	}
	c.worker.Wake()
	return res, nil
}

func (c *Impl) deleteSubtreeFile(ctx context.Context, rootID, userID, actor uuid.UUID, row model.File) (*model.File, error) {
	trashPath := storage.BuildTrashPath(row.Path, time.Now())

	var deleted *model.File
	err := c.withMutationTx(ctx, func(tx pgx.Tx, batch *outbox.Batch) error {
		var err error
		deleted, err = c.trash.SoftDelete(ctx, rootID, row.Path, trashPath, actor)
		if err != nil {
			return err
		}
		if err := batch.Add(TrashPayload{
			UserID: userID, RootID: rootID, From: row.Path, To: trashPath,
		}); err != nil {
			return err
		}
		if err := PublishDurable(batch, userID, sseevents.FileDeleted{
			RootID: rootID, FileID: deleted.ID, Path: deleted.Path,
		}); err != nil {
			return err
		}
		if err := PublishDurable(batch, userID, sseevents.TrashAdded{
			RootID: rootID, FileID: deleted.ID, Path: deleted.Path,
			Size: deleted.Size, TrashPath: trashPath,
		}); err != nil {
			return err
		}
		if err := PublishDurable(batch, userID, sseevents.QuotaUpdated{}); err != nil {
			return err
		}
		return c.bus.PublishCritical(ctx, tx, bus.FileTrashed{
			UserID: userID, RootID: rootID, FileID: deleted.ID, Bytes: deleted.Size,
		})
	})
	if err != nil {
		return nil, err
	}
	return deleted, nil
}

func (c *Impl) deleteSubtreeDir(ctx context.Context, rootID, userID uuid.UUID, row model.File) error {
	return c.withMutationTx(ctx, func(tx pgx.Tx, batch *outbox.Batch) error {
		if err := c.trash.MarkDeleted(ctx, rootID, row.Path); err != nil {
			return err
		}
		return PublishDurable(batch, userID, sseevents.FileDeleted{
			RootID: rootID, FileID: row.ID, Path: row.Path,
		})
	})
}

// DeleteSyncRoot drops the whole sync root in one TX and releases its
// used/trash bytes via the SyncRootDeleted bus event. Storage cleanup is the
// caller's responsibility (the coordinator has no storage handle).
func (c *Impl) DeleteSyncRoot(ctx context.Context, cmd DeleteSyncRootCmd) (DeleteSyncRootResult, error) {
	if _, err := c.syncRoots.GetSyncRoot(ctx, cmd.SyncRootID, cmd.UserID); err != nil {
		return DeleteSyncRootResult{}, err
	}

	var sizes *repository.SyncRootSizes
	var grants []repository.DeletedShare
	var shareIDs []uuid.UUID
	err := c.withMutationTx(ctx, func(tx pgx.Tx, _ *outbox.Batch) error {
		var err error
		sizes, err = c.syncRoots.SumSyncRootSizesTx(ctx, tx, cmd.SyncRootID)
		if err != nil {
			return err
		}
		grants, shareIDs, err = c.syncRoots.DeleteSyncRootTx(ctx, tx, cmd.SyncRootID, cmd.UserID)
		if err != nil {
			return err
		}
		return c.bus.PublishCritical(ctx, tx, bus.SyncRootDeleted{
			UserID: cmd.UserID, RootID: cmd.SyncRootID,
			UsedBytes: sizes.UsedBytes, TrashBytes: sizes.TrashBytes,
		})
	})
	if err != nil {
		return DeleteSyncRootResult{}, err
	}
	c.syncRoots.DeleteSyncRootMetaCleanup(cmd.SyncRootID, shareIDs)
	return DeleteSyncRootResult{
		UsedBytes:     sizes.UsedBytes,
		TrashBytes:    sizes.TrashBytes,
		RevokedShares: grants,
	}, nil
}
