package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"privasync/server/internal/bus"
	"privasync/server/internal/delta"
	"privasync/server/internal/model"
	"privasync/server/internal/outbox"
	sseevents "privasync/server/internal/sse/events"
)

// UploadCmd describes one file upload after the bytes have been assembled
// and live at StagingPath. The coordinator owns the rest: version check,
// quota, persistence, side-effects.
//
// ActorUserID is the user who actually triggered the write. For own-root
// uploads it equals UserID; for mount uploads it is the recipient, while
// UserID stays the giver (storage owner). The coordinator records it as
// `files.last_modifier_user_id`. See ADR-0013.
type UploadCmd struct {
	UserID      uuid.UUID
	ActorUserID uuid.UUID
	SyncRootID  uuid.UUID
	Path        string    // target path in the sync root
	StagingPath string    // where assembled bytes currently live (inside the same root)
	Size        int64     // written bytes
	ContentHash []byte    // BLAKE3 of the assembled bytes
	MTime       time.Time // client-provided mtime, or NOW if zero

	// BlockSignatures are the delta-sync block signatures of the very bytes
	// the caller just wrote, when it could produce them for free while
	// streaming (the chunked-upload path). Left nil, the coordinator
	// reads the staging file to derive them.
	BlockSignatures []delta.Signature

	// IfMatch is the value of the HTTP If-Match request header parsed as the
	// caller's known server_version. nil means the client sent no header —
	// the coordinator treats that as "no opinion" and routes any version
	// mismatch through the real-Conflict path. With a value, a mismatch
	// becomes a version-Conflict (HTTP 409 + staging cleanup, no
	// Conflict-Row, no Loser-Path).
	IfMatch *int64
}

// ConflictKind discriminates the two failure modes a coordinator can produce.
type ConflictKind int

const (
	ConflictNone            ConflictKind = iota
	ConflictVersionMismatch              // client sent If-Match and lost — HTTP 409
	ConflictReal                         // no If-Match → Loser-Path materialised
)

// UploadResult tells the adapter what happened so it can render the HTTP
// response. File is always populated (latest authoritative row); ConflictPath
// is set only for ConflictReal.
type UploadResult struct {
	File         *model.File
	Conflict     ConflictKind
	ConflictPath string
}

// UploadFile drives the upload pipeline. The bytes already exist at
// cmd.StagingPath inside the user's sync-root directory (the chunk-assembly
// adapter placed them there).
func (c *Impl) UploadFile(ctx context.Context, cmd UploadCmd) (UploadResult, error) {
	// Phase 1 — Authorize.
	root, err := c.syncRoots.GetSyncRoot(ctx, cmd.SyncRootID, cmd.UserID)
	if err != nil {
		return UploadResult{}, fmt.Errorf("authorize: %w", err)
	}

	// Phase 2 — Quota-Gate. We know the actual Size at this point (bytes
	// already assembled). Pre-staging Content-Length gating is the
	// adapter's job and lives one level up.
	var oldSize int64
	if existing, err := c.files.FindByPath(ctx, cmd.SyncRootID, cmd.Path); err == nil {
		oldSize = existing.Size
	}
	if err := c.quota.CheckHeadroom(ctx, cmd.UserID, cmd.Size-oldSize); err != nil {
		return UploadResult{}, err
	}

	// Phase 3 — TX: upsert + outbox enqueue, single commit.
	//
	// Conflict-Diskrimination (RFC 7232 semantics):
	//   • If-Match present, value matches existing server_version → upsert
	//   • If-Match present, value mismatches                       → ConflictVersionMismatch (HTTP 409)
	//   • If-Match absent (or `*`), file already exists at path    → ConflictReal (Loser-Path)
	//   • If-Match absent, file does not yet exist                 → upsert
	mtime := cmd.MTime
	if mtime.IsZero() {
		mtime = time.Now()
	}

	// "Real conflict" is only reachable when the client did not opt into the
	// optimistic check: a live file at the same path means the client wrote
	// blind and must lose. UpsertWithVersionCheck cannot express this on its
	// own (it short-circuits when clientVersion < 0), so we pre-check here.
	if cmd.IfMatch == nil {
		if existing, err := c.files.FindByPath(ctx, cmd.SyncRootID, cmd.Path); err == nil && !existing.IsDeleted {
			// Identical bytes are not a conflict: there is nothing to lose and
			// nothing to choose between. A client lands here whenever it does
			// not know the file's server_version — a re-added sync root, a lost
			// upload response, a retried request — and must simply learn the
			// current version. The hash is the server's own (computed while the
			// bytes were written), never the client's claim.
			if sameContent(existing, cmd) {
				if err := c.storage.DeleteFile(cmd.UserID, root.ID.String(), cmd.StagingPath); err != nil {
					slog.Warn("identical upload: staging cleanup failed", "path", cmd.Path, "error", err)
				}
				return UploadResult{File: existing, Conflict: ConflictNone}, nil
			}
			return c.realConflict(ctx, cmd, root.ID, existing)
		}
	}

	clientVersion := int64(-1)
	if cmd.IfMatch != nil {
		clientVersion = *cmd.IfMatch
	}

	actor := resolveActor(cmd.ActorUserID, cmd.UserID)
	file, conflict, err := c.files.UpsertWithVersionCheck(ctx, &model.File{
		UserID:             cmd.UserID,
		SyncRootID:         cmd.SyncRootID,
		Path:               cmd.Path,
		Size:               cmd.Size,
		MTime:              mtime,
		ContentHash:        cmd.ContentHash,
		IsDeleted:          false,
		LastModifierUserID: &actor,
		BlockHashes:        c.computeBlockHashes(cmd, root.ID),
	}, clientVersion)
	if err != nil {
		return UploadResult{}, err
	}

	switch {
	case conflict && cmd.IfMatch != nil:
		// Version-Conflict (RFC 7232): the client explicitly opted into the
		// optimistic check and lost. No Conflict-Row, no Loser-Path; the
		// adapter deletes the staging file and responds 409.
		return UploadResult{File: file, Conflict: ConflictVersionMismatch}, nil

	case conflict:
		// With If-Match present + mismatched we already returned above;
		// reaching here with clientVersion >= 0 means a race we treat as
		// version mismatch as well.
		return UploadResult{File: file, Conflict: ConflictVersionMismatch}, nil

	default:
		// Happy path: outbox rename staging → target + SSE.
		err := c.withMutationTx(ctx, func(tx pgx.Tx, batch *outbox.Batch) error {
			if err := batch.Add(RenamePayload{
				UserID: cmd.UserID, RootID: root.ID, From: cmd.StagingPath, To: cmd.Path,
			}); err != nil {
				return err
			}
			if err := PublishDurable(batch, cmd.UserID, sseevents.FileUploaded{
				RootID: root.ID, Path: file.Path, File: newSSEFile(file),
			}); err != nil {
				return err
			}
			if err := PublishDurable(batch, cmd.UserID, sseevents.QuotaUpdated{}); err != nil {
				return err
			}
			return c.bus.PublishCritical(ctx, tx, bus.FileUploaded{
				UserID: cmd.UserID, RootID: root.ID, FileID: file.ID,
				Bytes: cmd.Size, PrevBytes: oldSize,
			})
		})
		if err != nil {
			return UploadResult{}, err
		}
		return UploadResult{File: file, Conflict: ConflictNone}, nil
	}
}

// sameContent reports whether the upload carries exactly the bytes the live
// file already has. A missing hash on either side never counts as equal.
func sameContent(existing *model.File, cmd UploadCmd) bool {
	return len(cmd.ContentHash) > 0 &&
		existing.Size == cmd.Size &&
		bytes.Equal(existing.ContentHash, cmd.ContentHash)
}

// computeBlockHashes fills the delta-sync block-signature cache
// (files.block_hashes, ADR-0002) for the bytes this mutation writes.
// This is the single chokepoint for every byte-changing mutation (full upload
// and delta-apply both land here), so the cache can never drift from the file.
//
// A caller that streamed the bytes itself hands the signatures over ready-made;
// everyone else pays one read of the staging file. Either way it is purely an
// optimisation for the read path: signatures are only worth caching above the
// delta threshold (smaller files never get a delta offer), and any failure
// degrades to NULL so delta-request simply re-hashes on the fly — a cache miss
// must never fail an upload.
func (c *Impl) computeBlockHashes(cmd UploadCmd, rootID uuid.UUID) []byte {
	if cmd.Size <= delta.MinDeltaSize {
		return nil
	}
	sigs := cmd.BlockSignatures
	if sigs == nil {
		var err error
		if sigs, err = c.readBlockSignatures(cmd, rootID); err != nil {
			return nil
		}
	}
	raw, err := json.Marshal(sigs)
	if err != nil {
		slog.Warn("delta cache: marshal failed", "err", err, "path", cmd.Path)
		return nil
	}
	return raw
}

// readBlockSignatures derives the signatures from the staging bytes on disk —
// the fallback for callers that cannot produce them while writing.
func (c *Impl) readBlockSignatures(cmd UploadCmd, rootID uuid.UUID) ([]delta.Signature, error) {
	f, err := c.storage.Open(cmd.UserID, rootID.String(), cmd.StagingPath)
	if err != nil {
		slog.Warn("delta cache: open staging failed", "err", err, "path", cmd.Path)
		return nil, err
	}
	defer f.Close()

	sigs, err := delta.BlockSignatures(f, delta.BlockSize)
	if err != nil {
		slog.Warn("delta cache: block signatures failed", "err", err, "path", cmd.Path)
		return nil, err
	}
	return sigs, nil
}

// realConflict materialises a loser bytes path + Conflict-Row + Outbox
// (rename → sse conflict_added). Conflict mail is published reactively on
// the bus after commit so a rolled-back TX does not trigger SMTP.
func (c *Impl) realConflict(ctx context.Context, cmd UploadCmd, rootID uuid.UUID, existing *model.File) (UploadResult, error) {
	conflictPath := buildConflictPath(cmd.Path)

	file, err := c.materialiseConflictCopy(ctx, conflictMaterialization{
		UserID:       cmd.UserID,
		RootID:       rootID,
		FileID:       existing.ID,
		ConflictPath: conflictPath,
		OriginalPath: cmd.Path,
		LoserMTime:   cmd.MTime,
		StorageMove: RenamePayload{
			UserID: cmd.UserID, RootID: rootID, From: cmd.StagingPath, To: conflictPath,
		},
		KeptEvent: bus.FileConflictKept{
			UserID: cmd.UserID, RootID: rootID, FileID: existing.ID, Bytes: cmd.Size,
		},
		ResolveFile: func(context.Context) (*model.File, error) { return existing, nil },
	})
	if err != nil {
		return UploadResult{}, err
	}
	return UploadResult{File: file, Conflict: ConflictReal, ConflictPath: conflictPath}, nil
}
