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
	sseevents "privasync/server/internal/sse/events"
)

// conflictMaterialization carries what varies between the three real-
// conflict call sites (Upload's realConflict, Move's realMoveConflict,
// Restore's realRestoreConflict); materialiseConflictCopy owns the
// choreography they share (ADR-0022 part 3): Conflict-Row → resolved file →
// outbox storage-move → SSE ConflictAdded → the "kept" bus event →
// bus.ConflictCreated.
type conflictMaterialization struct {
	UserID       uuid.UUID
	RootID       uuid.UUID
	FileID       uuid.UUID // the file the conflict row references
	ConflictPath string
	OriginalPath string // ConflictAdded.OriginalPath and ConflictCreated.FilePath
	CurrentPath  string // ConflictAdded.CurrentPath — only Move sets this (source stays live)
	LoserMTime   time.Time // the loser copy's own mtime; zero when unknown
	StorageMove  outbox.Payload
	KeptEvent    bus.Event // bus.FileConflictKept (Upload/Move) or bus.FileRestored (Restore)
	// ResolveFile runs after the conflict row is created and returns the file
	// the caller should report. Upload/Move return their already-known file
	// unchanged; Restore relocates the trashed row onto ConflictPath here.
	ResolveFile func(ctx context.Context) (*model.File, error)
}

func (c *Impl) materialiseConflictCopy(ctx context.Context, m conflictMaterialization) (*model.File, error) {
	var file *model.File
	err := c.withMutationTx(ctx, func(tx pgx.Tx, batch *outbox.Batch) error {
		var loserMTime *time.Time
		if !m.LoserMTime.IsZero() {
			loserMTime = &m.LoserMTime
		}
		confRow, err := c.conflicts.CreateConflict(ctx, m.FileID, m.ConflictPath, nil, loserMTime)
		if err != nil {
			return fmt.Errorf("create conflict row: %w", err)
		}
		file, err = m.ResolveFile(ctx)
		if err != nil {
			return err
		}
		if err := batch.Add(m.StorageMove); err != nil {
			return err
		}
		if err := PublishDurable(batch, m.UserID, sseevents.ConflictAdded{
			ConflictID:   confRow.ID,
			FileID:       m.FileID,
			RootID:       m.RootID,
			ConflictPath: m.ConflictPath,
			OriginalPath: m.OriginalPath,
			CurrentPath:  m.CurrentPath,
			CreatedAt:    confRow.CreatedAt,
		}); err != nil {
			return err
		}
		if err := c.bus.PublishCritical(ctx, tx, m.KeptEvent); err != nil {
			return err
		}
		return c.bus.PublishCritical(ctx, tx, bus.ConflictCreated{
			UserID: m.UserID, ConflictID: confRow.ID, FilePath: m.OriginalPath,
		})
	})
	if err != nil {
		return nil, err
	}
	return file, nil
}
