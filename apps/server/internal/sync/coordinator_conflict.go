package sync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"privasync/server/internal/bus"
	"privasync/server/internal/model"
	"privasync/server/internal/outbox"
	"privasync/server/internal/repository"
	sseevents "privasync/server/internal/sse/events"
)

// ResolveStrategy selects what happens to the two competing versions when a
// conflict is settled (ADR-0003: never lose bytes silently — the strategy is
// an explicit user choice).
type ResolveStrategy string

const (
	// StrategyKeepCurrent keeps the live file and discards the loser copy.
	StrategyKeepCurrent ResolveStrategy = "keep_current"
	// StrategyKeepConflict promotes the loser copy onto the original path,
	// discarding the live file's bytes.
	StrategyKeepConflict ResolveStrategy = "keep_conflict"
	// StrategyKeepBoth keeps the live file and turns the loser copy into a
	// regular file at its conflict path. Nothing is discarded.
	StrategyKeepBoth ResolveStrategy = "keep_both"
)

// ParseResolveStrategy maps the wire value to a strategy. Empty input means
// the historical default keep_current (pre-strategy clients send no body).
func ParseResolveStrategy(s string) (ResolveStrategy, bool) {
	switch s {
	case "", string(StrategyKeepCurrent):
		return StrategyKeepCurrent, true
	case string(StrategyKeepConflict):
		return StrategyKeepConflict, true
	case string(StrategyKeepBoth):
		return StrategyKeepBoth, true
	}
	return "", false
}

// ResolveConflictCmd describes a user-initiated conflict resolution. What
// happens to the loser-path copy depends on Strategy; the conflict row is
// always marked resolved.
type ResolveConflictCmd struct {
	UserID     uuid.UUID
	ConflictID uuid.UUID
	Strategy   ResolveStrategy
}

// ResolveConflictResult carries the metadata the adapter needs for the
// "conflict_resolved" SSE notification.
type ResolveConflictResult struct {
	FileID       uuid.UUID
	SyncRootID   uuid.UUID
	ConflictPath string
	FilePath     string
	FreedBytes   int64
}

// ResolveConflict settles a conflict row according to cmd.Strategy and
// returns the metadata for the "conflict_resolved" SSE notification.
func (c *Impl) ResolveConflict(ctx context.Context, cmd ResolveConflictCmd) (ResolveConflictResult, error) {
	switch cmd.Strategy {
	case StrategyKeepConflict:
		return c.resolveKeepConflict(ctx, cmd)
	case StrategyKeepBoth:
		return c.resolveKeepBoth(ctx, cmd)
	default:
		// keep_current — also the zero value so internal callers that predate
		// strategies keep their behavior.
		return c.resolveKeepCurrent(ctx, cmd)
	}
}

// resolveKeepCurrent deletes the loser-path copy from storage and publishes
// FileConflictDiscarded so the quota subscriber releases the freed bytes.
// Storage cleanup runs best-effort: a missing or unreadable file does not
// abort the resolve, but in that case no bytes are published (the user's
// used counter stays as-is, matching the previous "only book on actual
// disk-free" semantics).
func (c *Impl) resolveKeepCurrent(ctx context.Context, cmd ResolveConflictCmd) (ResolveConflictResult, error) {
	var rc *repository.ResolvedConflict
	var freed int64
	err := c.withMutationTx(ctx, func(tx pgx.Tx, _ *outbox.Batch) error {
		var err error
		rc, err = c.conflicts.ResolveConflictTx(ctx, tx, cmd.ConflictID, cmd.UserID)
		if err != nil {
			return err
		}

		conflictSize, sizeErr := c.storage.FileSize(cmd.UserID, rc.SyncRootID.String(), rc.ConflictPath)
		if sizeErr != nil {
			conflictSize = 0
		}
		deleteErr := c.storage.DeleteFile(cmd.UserID, rc.SyncRootID.String(), rc.ConflictPath)
		if deleteErr != nil {
			slog.Warn("conflict resolve: storage cleanup failed",
				"conflict_id", cmd.ConflictID, "path", rc.ConflictPath, "error", deleteErr)
		}

		if deleteErr == nil && conflictSize > 0 {
			freed = conflictSize
			return c.bus.PublishCritical(ctx, tx, bus.FileConflictDiscarded{
				UserID: cmd.UserID, RootID: rc.SyncRootID, FileID: rc.FileID,
				ConflictID: cmd.ConflictID, Bytes: conflictSize,
			})
		}
		return nil
	})
	if err != nil {
		return ResolveConflictResult{}, err
	}
	return ResolveConflictResult{
		FileID:       rc.FileID,
		SyncRootID:   rc.SyncRootID,
		ConflictPath: rc.ConflictPath,
		FilePath:     rc.FilePath,
		FreedBytes:   freed,
	}, nil
}

// upsertLoserBytesAt records the loser copy's bytes as the live file at
// `path`: size + BLAKE3 from storage, version bump via the DB trigger. Both
// promote strategies require the copy to exist — promoting nothing would
// silently lose the user's choice.
func (c *Impl) upsertLoserBytesAt(ctx context.Context, cmd ResolveConflictCmd, rc *repository.ResolvedConflict, path string) (*model.File, error) {
	loserHash, err := c.storage.HashFile(cmd.UserID, rc.SyncRootID.String(), rc.ConflictPath)
	if err != nil {
		return nil, fmt.Errorf("conflict copy unreadable: %w", err)
	}
	loserSize, err := c.storage.FileSize(cmd.UserID, rc.SyncRootID.String(), rc.ConflictPath)
	if err != nil {
		return nil, fmt.Errorf("conflict copy unreadable: %w", err)
	}
	return c.files.Upsert(ctx, &model.File{
		UserID:             cmd.UserID,
		SyncRootID:         rc.SyncRootID,
		Path:               path,
		Size:               loserSize,
		MTime:              time.Now(),
		ContentHash:        loserHash,
		IsDeleted:          false,
		LastModifierUserID: &cmd.UserID,
	})
}

// resolveKeepConflict promotes the loser copy: its bytes replace the live
// file (storage rename via outbox, overwrite), the files row takes the loser
// copy's size/hash, and the winner's bytes are released from quota.
func (c *Impl) resolveKeepConflict(ctx context.Context, cmd ResolveConflictCmd) (ResolveConflictResult, error) {
	var rc *repository.ResolvedConflict
	var winnerSize int64
	err := c.withMutationTx(ctx, func(tx pgx.Tx, batch *outbox.Batch) error {
		var err error
		rc, err = c.conflicts.ResolveConflictTx(ctx, tx, cmd.ConflictID, cmd.UserID)
		if err != nil {
			return err
		}

		var sizeErr error
		winnerSize, sizeErr = c.storage.FileSize(cmd.UserID, rc.SyncRootID.String(), rc.FilePath)
		if sizeErr != nil {
			winnerSize = 0
		}

		file, err := c.upsertLoserBytesAt(ctx, cmd, rc, rc.FilePath)
		if err != nil {
			return err
		}

		if err := batch.Add(RenamePayload{
			UserID: cmd.UserID, RootID: rc.SyncRootID, From: rc.ConflictPath, To: rc.FilePath,
		}); err != nil {
			return err
		}
		if err := PublishDurable(batch, cmd.UserID, sseevents.FileUploaded{
			RootID: rc.SyncRootID, Path: file.Path, File: newSSEFile(file),
		}); err != nil {
			return err
		}
		if err := PublishDurable(batch, cmd.UserID, sseevents.QuotaUpdated{}); err != nil {
			return err
		}

		// The discarded bytes are the winner's: after the rename only the
		// loser copy's bytes remain on disk.
		if winnerSize > 0 {
			return c.bus.PublishCritical(ctx, tx, bus.FileConflictDiscarded{
				UserID: cmd.UserID, RootID: rc.SyncRootID, FileID: rc.FileID,
				ConflictID: cmd.ConflictID, Bytes: winnerSize,
			})
		}
		return nil
	})
	if err != nil {
		return ResolveConflictResult{}, err
	}
	return ResolveConflictResult{
		FileID:       rc.FileID,
		SyncRootID:   rc.SyncRootID,
		ConflictPath: rc.ConflictPath,
		FilePath:     rc.FilePath,
		FreedBytes:   winnerSize,
	}, nil
}

// resolveKeepBoth keeps both versions: the live file is untouched and the
// loser copy gets a files row at its conflict path, turning it into a
// regular synced file. Its bytes were already booked into quota when the
// conflict was created (FileConflictKept), so no quota event fires.
func (c *Impl) resolveKeepBoth(ctx context.Context, cmd ResolveConflictCmd) (ResolveConflictResult, error) {
	var rc *repository.ResolvedConflict
	err := c.withMutationTx(ctx, func(tx pgx.Tx, batch *outbox.Batch) error {
		var err error
		rc, err = c.conflicts.ResolveConflictTx(ctx, tx, cmd.ConflictID, cmd.UserID)
		if err != nil {
			return err
		}

		// Restore conflicts already own a row at the conflict path (the
		// trashed row was revived there); the upsert is idempotent for that
		// case.
		file, err := c.upsertLoserBytesAt(ctx, cmd, rc, rc.ConflictPath)
		if err != nil {
			return err
		}

		return PublishDurable(batch, cmd.UserID, sseevents.FileUploaded{
			RootID: rc.SyncRootID, Path: file.Path, File: newSSEFile(file),
		})
	})
	if err != nil {
		return ResolveConflictResult{}, err
	}
	return ResolveConflictResult{
		FileID:       rc.FileID,
		SyncRootID:   rc.SyncRootID,
		ConflictPath: rc.ConflictPath,
		FilePath:     rc.FilePath,
	}, nil
}
