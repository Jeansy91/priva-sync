// Package sync owns the SyncCoordinator: the single entry point that
// orchestrates mutating file operations across all layers (Authorize →
// Validate → Quota-Gate → Storage-Staging → DB-TX → Outbox → Commit →
// Same-TX-Hook). HTTP handlers degrade to thin adapters that translate
// requests/responses; everything that matters happens here.
//
// See apps/server/CONTEXT.md → "Sync Coordinator".
package sync

import (
	"context"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/bus"
	"privasync/server/internal/model"
	"privasync/server/internal/outbox"
	"privasync/server/internal/quota"
	"privasync/server/internal/repository"
	sseevents "privasync/server/internal/sse/events"
	"privasync/server/internal/storage"
)

// Coordinator is the public surface. Verticals share one pipeline:
// Upload, Move. Further verticals (Delete, Restore) will land alongside.
type Coordinator interface {
	UploadFile(ctx context.Context, cmd UploadCmd) (UploadResult, error)
	MoveFile(ctx context.Context, cmd MoveCmd) (MoveResult, error)
	CopyFile(ctx context.Context, cmd CopyCmd) (CopyResult, error)
	DeleteFile(ctx context.Context, cmd DeleteCmd) (DeleteResult, error)
	DeleteSubtree(ctx context.Context, cmd DeleteSubtreeCmd) (DeleteSubtreeResult, error)
	RestoreFromTrash(ctx context.Context, cmd RestoreCmd) (RestoreResult, error)
	EmptyTrash(ctx context.Context, cmd EmptyTrashCmd) error
	DeleteSyncRoot(ctx context.Context, cmd DeleteSyncRootCmd) (DeleteSyncRootResult, error)
	ResolveConflict(ctx context.Context, cmd ResolveConflictCmd) (ResolveConflictResult, error)
}

// Impl is the concrete coordinator. Built once in main.go.
type Impl struct {
	pool      *pgxpool.Pool
	files     *repository.FileRepository
	syncRoots *repository.SyncRootRepository
	conflicts *repository.ConflictRepository
	trash     *repository.TrashRepository
	outbox    *outbox.Repository
	worker    *outbox.Worker
	quota     *quota.Service
	bus       bus.Bus
	storage   *storage.Service
}

// NewCoordinator wires the dependencies.
func NewCoordinator(
	pool *pgxpool.Pool,
	files *repository.FileRepository,
	syncRoots *repository.SyncRootRepository,
	conflicts *repository.ConflictRepository,
	trash *repository.TrashRepository,
	outboxRepo *outbox.Repository,
	worker *outbox.Worker,
	q *quota.Service,
	b bus.Bus,
	stor *storage.Service,
) *Impl {
	return &Impl{pool: pool, files: files, syncRoots: syncRoots, conflicts: conflicts, trash: trash, outbox: outboxRepo, worker: worker, quota: q, bus: b, storage: stor}
}

// withMutationTx owns the TX lifecycle every coordinator mutation shares:
// Begin, deferred Rollback, Commit, and the post-commit worker.Wake()
// (Same-TX-Hook). fn receives the open tx and a fresh per-TX outbox.Batch and
// writes whatever Outbox-/Bus-choreography the mutation needs — the runner
// carries no behavior template of its own (ADR-0017 boundary, kept by
// ADR-0022).
func (c *Impl) withMutationTx(ctx context.Context, fn func(tx pgx.Tx, batch *outbox.Batch) error) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	batch := outbox.NewBatch(ctx, tx, c.outbox, uuid.New())
	if err := fn(tx, batch); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	c.worker.Wake()
	return nil
}

// resolveActor picks the effective last-modifier id: an explicit actor when
// the caller provided one (mount writes), the storage owner otherwise
// (own-root writes). Centralises the ADR-0013 fallback so every pipeline
// step uses the same rule.
func resolveActor(actor, owner uuid.UUID) uuid.UUID {
	if actor == uuid.Nil {
		return owner
	}
	return actor
}

// buildConflictPath constructs a conflict-marker path that preserves the
// original extension so the OS/shell still opens it correctly.
// e.g. "/docs/report.pdf" -> "/docs/report.conflict-20260419T153000.pdf".
func buildConflictPath(orig string) string {
	ext := path.Ext(orig)
	base := strings.TrimSuffix(orig, ext)
	ts := time.Now().UTC().Format("20060102T150405")
	return fmt.Sprintf("%s.conflict-%s%s", base, ts, ext)
}

// newSSEFile lifts a model.File row into the wire-format sseevents.File so
// SSE payloads stay decoupled from the DB representation (ContentHash is hex
// on the wire, raw bytes in the model).
func newSSEFile(f *model.File) sseevents.File {
	return sseevents.File{
		ID:            f.ID,
		Path:          f.Path,
		Size:          f.Size,
		MTime:         f.MTime,
		ContentHash:   hex.EncodeToString(f.ContentHash),
		IsDeleted:     f.IsDeleted,
		IsDir:         f.IsDir,
		ServerVersion: f.ServerVersion,
		UpdatedAt:     f.UpdatedAt,
	}
}
