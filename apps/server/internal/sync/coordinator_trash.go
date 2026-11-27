package sync

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"privasync/server/internal/bus"
	"privasync/server/internal/model"
	"privasync/server/internal/outbox"
	"privasync/server/internal/repository"
	sseevents "privasync/server/internal/sse/events"
)

// RestoreCmd describes a restore of a single trashed file by ID. The
// coordinator handles authorize → conflict pre-flight → DB un-trash → outbox.
type RestoreCmd struct {
	UserID      uuid.UUID
	ActorUserID uuid.UUID // ADR-0013, same semantics as on UploadCmd
	SyncRootID  uuid.UUID
	FileID      uuid.UUID
}

// RestoreResult mirrors MoveResult. ConflictReal materialises when the
// original path is already occupied: the trashed bytes are routed to a
// loser-path and the original row stays in the trash.
type RestoreResult struct {
	File         *model.File
	Conflict     ConflictKind
	ConflictPath string
}

// EmptyTrashCmd targets every trashed file across all of the user's sync
// roots. Scope is intentionally user-wide so a single client action can
// purge a fragmented trash without N round-trips.
type EmptyTrashCmd struct {
	UserID uuid.UUID
}

// RestoreFromTrash drives the restore pipeline. ErrNotFound bubbles when the
// trashed row is gone (already restored, purged, or never existed).
func (c *Impl) RestoreFromTrash(ctx context.Context, cmd RestoreCmd) (RestoreResult, error) {
	root, err := c.syncRoots.GetSyncRoot(ctx, cmd.SyncRootID, cmd.UserID)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("authorize: %w", err)
	}

	trashed, err := c.trash.FindTrashedByID(ctx, cmd.FileID, cmd.UserID)
	if err != nil {
		return RestoreResult{}, err
	}
	if trashed.TrashPath == nil || *trashed.TrashPath == "" {
		return RestoreResult{}, repository.ErrNotFound
	}

	// Pre-flight: original path occupied by a live row → real conflict.
	if existing, derr := c.files.FindByPath(ctx, cmd.SyncRootID, trashed.Path); derr == nil && !existing.IsDeleted {
		return c.realRestoreConflict(ctx, cmd, root.ID, trashed)
	}

	actor := resolveActor(cmd.ActorUserID, cmd.UserID)

	var restored *model.File
	err = c.withMutationTx(ctx, func(tx pgx.Tx, batch *outbox.Batch) error {
		var err error
		restored, err = c.trash.RestoreFromTrash(ctx, cmd.FileID, actor)
		if err != nil {
			return err
		}
		if err := batch.Add(UntrashPayload{
			UserID: cmd.UserID, RootID: root.ID, From: *trashed.TrashPath, To: restored.Path,
		}); err != nil {
			return err
		}
		if err := PublishDurable(batch, cmd.UserID, sseevents.FileRestored{
			RootID: root.ID, FileID: restored.ID, Path: restored.Path,
		}); err != nil {
			return err
		}
		if err := PublishDurable(batch, cmd.UserID, sseevents.FileAdded{
			RootID: root.ID, Path: restored.Path, File: newSSEFile(restored),
		}); err != nil {
			return err
		}
		if err := PublishDurable(batch, cmd.UserID, sseevents.QuotaUpdated{}); err != nil {
			return err
		}
		return c.bus.PublishCritical(ctx, tx, bus.FileRestored{
			UserID: cmd.UserID, RootID: root.ID, FileID: restored.ID, Bytes: restored.Size,
		})
	})
	if err != nil {
		return RestoreResult{}, err
	}
	return RestoreResult{File: restored}, nil
}

// realRestoreConflict materialises a conflict copy when the original path is
// occupied. The trashed bytes move to the loser-path; the row leaves the
// trash and becomes a live file at the conflict path. The occupying row is
// untouched.
func (c *Impl) realRestoreConflict(ctx context.Context, cmd RestoreCmd, rootID uuid.UUID, trashed *model.File) (RestoreResult, error) {
	conflictPath := buildConflictPath(trashed.Path)
	actor := resolveActor(cmd.ActorUserID, cmd.UserID)

	file, err := c.materialiseConflictCopy(ctx, conflictMaterialization{
		UserID:       cmd.UserID,
		RootID:       rootID,
		FileID:       trashed.ID,
		ConflictPath: conflictPath,
		OriginalPath: trashed.Path,
		LoserMTime:   trashed.MTime,
		StorageMove: UntrashPayload{
			UserID: cmd.UserID, RootID: rootID, From: *trashed.TrashPath, To: conflictPath,
		},
		KeptEvent: bus.FileRestored{
			UserID: cmd.UserID, RootID: rootID, FileID: trashed.ID, Bytes: trashed.Size,
		},
		// Restore the row, but onto the loser-path so the occupant stays.
		ResolveFile: func(ctx context.Context) (*model.File, error) {
			return c.trash.RestoreFromTrashAtPath(ctx, trashed.ID, conflictPath, actor)
		},
	})
	if err != nil {
		return RestoreResult{}, err
	}
	return RestoreResult{File: file, Conflict: ConflictReal, ConflictPath: conflictPath}, nil
}

// EmptyTrash purges every trashed file of the user: DB rows are removed
// inside one TX, physical blobs leave via outbox so a mid-flight crash
// stays retryable. Quota is rebuilt authoritatively from `files` after
// commit. Idempotent: an empty trash is a no-op.
func (c *Impl) EmptyTrash(ctx context.Context, cmd EmptyTrashCmd) error {
	trashed, err := c.trash.ListAllTrashedByUser(ctx, cmd.UserID)
	if err != nil {
		return err
	}
	if len(trashed) == 0 {
		return nil
	}

	err = c.withMutationTx(ctx, func(tx pgx.Tx, batch *outbox.Batch) error {
		for i := range trashed {
			f := &trashed[i]
			if err := c.trash.PurgeTrashRecordTx(ctx, tx, f.ID); err != nil {
				return err
			}
			if err := batch.Add(DeletePayload{
				UserID: cmd.UserID, RootID: f.SyncRootID, TrashPath: *f.TrashPath,
			}); err != nil {
				return err
			}
		}
		if err := PublishDurable(batch, cmd.UserID, sseevents.TrashEmptied{}); err != nil {
			return err
		}
		return PublishDurable(batch, cmd.UserID, sseevents.QuotaUpdated{})
	})
	if err != nil {
		return err
	}
	if err := c.quota.RecomputeViaBus(ctx, cmd.UserID); err != nil {
		// Drift heals on the next quota touch; the purge already committed.
		slog.Warn("empty_trash: quota recompute failed", "user_id", cmd.UserID, "error", err)
	}
	return nil
}
