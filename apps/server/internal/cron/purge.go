// Package cron hosts the background sweeps that mutate quota via the
// DomainEventBus rather than calling the quota service directly. Today only
// the 30-day trash retention purge lives here; the upload-session cleanup
// stays in main.go because it owns no quota-relevant counters.
package cron

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/bus"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
	"privasync/server/internal/storage"
)

// Purger sweeps soft-deleted files older than Retention and hard-deletes
// them. Per file: storage delete first, then a TX that publishes
// FilePurgedByCron (Critical → quota releases trash bytes in-TX) and removes
// the row. The bus is the single mutation path; the purger does not touch
// quota counters directly.
type Purger struct {
	pool      *pgxpool.Pool
	syncRoots *repository.SyncRootRepository
	trash     *repository.TrashRepository
	stor      *storage.Service
	bus       bus.Bus
	retention time.Duration
}

// NewPurger wires the purger. Retention is typically 30 days; tests pass
// shorter windows.
func NewPurger(pool *pgxpool.Pool, syncRoots *repository.SyncRootRepository, trash *repository.TrashRepository, stor *storage.Service, b bus.Bus, retention time.Duration) *Purger {
	return &Purger{pool: pool, syncRoots: syncRoots, trash: trash, stor: stor, bus: b, retention: retention}
}

// Run executes PurgeOnce immediately and then every interval until ctx is
// cancelled. Errors from a single sweep are logged and do not abort the loop.
func (p *Purger) Run(ctx context.Context, interval time.Duration) {
	if _, err := p.PurgeOnce(ctx); err != nil {
		slog.Error("trash purge: sweep failed", "error", err)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := p.PurgeOnce(ctx); err != nil {
				slog.Error("trash purge: sweep failed", "error", err)
			}
		}
	}
}

// PurgeOnce runs one sweep and returns the number of files successfully
// purged. Per-file failures are logged and skipped — a single bad file must
// not stall the whole queue.
func (p *Purger) PurgeOnce(ctx context.Context) (int, error) {
	cutoff := time.Now().Add(-p.retention)
	purged := 0
	err := p.trash.EachTrashedBefore(ctx, cutoff, func(f model.File) error {
		if f.TrashPath == nil {
			return nil
		}
		if err := p.purgeOne(ctx, f); err != nil {
			slog.Warn("trash purge: file failed", "file_id", f.ID, "error", err)
			return nil
		}
		purged++
		return nil
	})
	if err != nil {
		return purged, fmt.Errorf("list trashed: %w", err)
	}
	if purged > 0 {
		slog.Info("trash purge complete", "purged", purged)
	}
	return purged, nil
}

func (p *Purger) purgeOne(ctx context.Context, f model.File) error {
	root, err := p.syncRoots.GetSyncRootByID(ctx, f.SyncRootID)
	if err != nil {
		return fmt.Errorf("sync root lookup: %w", err)
	}
	if err := p.stor.DeleteTrash(f.UserID, root.Path, *f.TrashPath); err != nil {
		return fmt.Errorf("storage delete: %w", err)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	evt := bus.FilePurgedByCron{
		UserID: f.UserID, RootID: f.SyncRootID, FileID: f.ID, Bytes: f.Size,
	}
	if err := p.bus.PublishCritical(ctx, tx, evt); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	if err := p.trash.PurgeTrashRecordTx(ctx, tx, f.ID); err != nil {
		return fmt.Errorf("delete record: %w", err)
	}
	return tx.Commit(ctx)
}
