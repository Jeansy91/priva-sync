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

// MoveCmd describes a rename of an existing file inside one sync root.
// Bytes are byte-neutral (no quota delta), so the coordinator skips the
// quota gate. If-Match mirrors UploadCmd semantics (RFC 7232).
type MoveCmd struct {
	UserID uuid.UUID
	// ActorUserID — actual writer for last_modifier_user_id (ADR-0013).
	// Equals UserID for own-root moves; differs for mount moves where
	// the recipient triggers the move while UserID stays the giver.
	ActorUserID uuid.UUID
	SyncRootID  uuid.UUID
	From        string
	To          string
	IfMatch     *int64
}

// MoveResult mirrors UploadResult. File is the authoritative row: post-move
// on the happy path, pre-move (current server state) on either conflict.
type MoveResult struct {
	File         *model.File
	Conflict     ConflictKind
	ConflictPath string
}

// MoveFile drives the move pipeline. Source must already live at cmd.From
// in the DB; the storage rename is deferred to the outbox like Upload.
//
// Conflict-Diskrimination matches UploadFile:
//   - If-Match present + source version mismatches            → ConflictVersionMismatch (HTTP 409)
//   - If-Match present + destination already occupied         → ConflictVersionMismatch (HTTP 409)
//   - If-Match absent  + destination already occupied         → ConflictReal (copy → loser-path)
//   - If-Match absent  + happy path                           → DB rename + storage rename
//
// repository.ErrNotFound is returned verbatim when the source row is gone.
func (c *Impl) MoveFile(ctx context.Context, cmd MoveCmd) (MoveResult, error) {
	// Phase 1 — Authorize.
	root, err := c.syncRoots.GetSyncRoot(ctx, cmd.SyncRootID, cmd.UserID)
	if err != nil {
		return MoveResult{}, fmt.Errorf("authorize: %w", err)
	}

	// Phase 2 — Load source for version check. A missing row may still be an
	// implicit directory (no own row but children below it), so before giving
	// up we check for descendants and, if any, treat it as a folder rename.
	src, err := c.files.FindByPath(ctx, cmd.SyncRootID, cmd.From)
	if errors.Is(err, repository.ErrNotFound) {
		hasChildren, cerr := c.files.HasChildren(ctx, cmd.SyncRootID, cmd.From)
		if cerr != nil {
			return MoveResult{}, cerr
		}
		if hasChildren {
			return c.moveDirectory(ctx, cmd, root.ID)
		}
		return MoveResult{}, err
	}
	if err != nil {
		return MoveResult{}, err
	}

	// Directories take a different path: the rename rewrites the whole subtree
	// (dir row + descendants) and the storage rename moves the folder in one
	// os.Rename. Folder-move-move conflict-copies are not worth the complexity
	// (rare), so a busy destination just 409s — see moveDirectory.
	if src.IsDir {
		return c.moveDirectory(ctx, cmd, root.ID)
	}

	if cmd.IfMatch != nil && src.ServerVersion != *cmd.IfMatch {
		return MoveResult{File: src, Conflict: ConflictVersionMismatch}, nil
	}

	// Phase 3 — Destination check (pre-flight). A live row at cmd.To means
	// either the user already has something there or a parallel client beat
	// us to it. Without If-Match we lose into a conflict-copy; with If-Match
	// we 409 so the client can rebase.
	if dst, derr := c.files.FindByPath(ctx, cmd.SyncRootID, cmd.To); derr == nil && !dst.IsDeleted {
		if cmd.IfMatch != nil {
			return MoveResult{File: src, Conflict: ConflictVersionMismatch}, nil
		}
		return c.realMoveConflict(ctx, cmd, root.ID, src)
	}

	// Phase 4 — DB rename, then TX: outbox enqueue + bus, single commit.
	actor := resolveActor(cmd.ActorUserID, cmd.UserID)
	moved, err := c.files.MoveFile(ctx, cmd.SyncRootID, cmd.From, cmd.To, actor)
	if errors.Is(err, repository.ErrConflict) {
		// Race: another client occupied cmd.To between pre-flight and UPDATE.
		// Mirror the pre-flight branch.
		if cmd.IfMatch != nil {
			return MoveResult{File: src, Conflict: ConflictVersionMismatch}, nil
		}
		return c.realMoveConflict(ctx, cmd, root.ID, src)
	}
	if err != nil {
		return MoveResult{}, err
	}

	err = c.withMutationTx(ctx, func(tx pgx.Tx, batch *outbox.Batch) error {
		if err := batch.Add(RenamePayload{
			UserID: cmd.UserID, RootID: root.ID, From: cmd.From, To: cmd.To,
		}); err != nil {
			return err
		}
		if err := PublishDurable(batch, cmd.UserID, sseevents.FileMoved{
			RootID: root.ID,
			FileID: moved.ID,
			From:   cmd.From,
			To:     cmd.To,
			File:   newSSEFile(moved),
		}); err != nil {
			return err
		}
		return c.bus.PublishCritical(ctx, tx, bus.FileMoved{
			UserID: cmd.UserID, RootID: root.ID, FileID: moved.ID,
			From: cmd.From, To: cmd.To,
		})
	})
	if err != nil {
		return MoveResult{}, err
	}
	return MoveResult{File: moved}, nil
}

// moveDirectory renames a folder: it rewrites the dir row plus every
// descendant path (repository.MoveDirectory, one UPDATE), enqueues a single
// storage rename (os.Rename moves the whole subtree), and announces the move
// once via FileMoved. Clients reload the listing on that event; the desktop
// picks up the rewritten descendants through GetChanges (each row's
// updated_at bumped). A live entry already at cmd.To yields a 409 — no
// conflict-copy is materialised for directories.
func (c *Impl) moveDirectory(ctx context.Context, cmd MoveCmd, rootID uuid.UUID) (MoveResult, error) {
	if dst, derr := c.files.FindByPath(ctx, cmd.SyncRootID, cmd.To); derr == nil && !dst.IsDeleted {
		return MoveResult{Conflict: ConflictVersionMismatch}, nil
	}

	actor := resolveActor(cmd.ActorUserID, cmd.UserID)
	moved, err := c.files.MoveDirectory(ctx, cmd.SyncRootID, cmd.From, cmd.To, actor)
	if errors.Is(err, repository.ErrConflict) {
		// Race: another client occupied cmd.To between pre-flight and UPDATE.
		return MoveResult{Conflict: ConflictVersionMismatch}, nil
	}
	if err != nil {
		return MoveResult{}, err
	}

	err = c.withMutationTx(ctx, func(tx pgx.Tx, batch *outbox.Batch) error {
		if err := batch.Add(RenamePayload{
			UserID: cmd.UserID, RootID: rootID, From: cmd.From, To: cmd.To,
		}); err != nil {
			return err
		}
		if err := PublishDurable(batch, cmd.UserID, sseevents.FileMoved{
			RootID: rootID,
			FileID: moved.ID,
			From:   cmd.From,
			To:     cmd.To,
			File:   newSSEFile(moved),
		}); err != nil {
			return err
		}
		return c.bus.PublishCritical(ctx, tx, bus.FileMoved{
			UserID: cmd.UserID, RootID: rootID, FileID: moved.ID,
			From: cmd.From, To: cmd.To,
		})
	})
	if err != nil {
		return MoveResult{}, err
	}
	return MoveResult{File: moved}, nil
}

// realMoveConflict materialises a conflict copy at cmd.To when a parallel
// client already occupies the destination. The source is left in place; only
// its bytes are *copied* to the loser-path. Mirrors the upload realConflict
// shape (Conflict-Row + outbox: storage copy → sse → mail).
func (c *Impl) realMoveConflict(ctx context.Context, cmd MoveCmd, rootID uuid.UUID, src *model.File) (MoveResult, error) {
	conflictPath := buildConflictPath(cmd.To)

	file, err := c.materialiseConflictCopy(ctx, conflictMaterialization{
		UserID:       cmd.UserID,
		RootID:       rootID,
		FileID:       src.ID,
		ConflictPath: conflictPath,
		OriginalPath: cmd.To,
		CurrentPath:  src.Path,
		LoserMTime:   src.MTime,
		StorageMove: CopyPayload{
			UserID: cmd.UserID, RootID: rootID, From: src.Path, To: conflictPath,
		},
		KeptEvent: bus.FileConflictKept{
			UserID: cmd.UserID, RootID: rootID, FileID: src.ID, Bytes: src.Size,
		},
		ResolveFile: func(context.Context) (*model.File, error) { return src, nil },
	})
	if err != nil {
		return MoveResult{}, err
	}
	return MoveResult{File: file, Conflict: ConflictReal, ConflictPath: conflictPath}, nil
}
