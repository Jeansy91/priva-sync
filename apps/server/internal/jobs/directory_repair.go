package jobs

import (
	"context"
	"log/slog"

	"privasync/server/internal/repository"
)

// RepairDirectories re-materialises directory rows that went missing above a
// live path (ADR-0041) and returns how many it created.
//
// Every write path already materialises the directories above what it writes,
// so a healthy installation finds nothing. This exists because the failure it
// guards against is silent: the rows under a missing directory keep syncing
// and simply stop being reachable through navigation.
//
// A path where a live file sits where a directory would have to go is logged,
// not repaired -- see MaterialiseMissingDirectories.
func RepairDirectories(ctx context.Context, files *repository.FileRepository) (int, error) {
	created, blocked, err := files.MaterialiseMissingDirectories(ctx)
	if err != nil {
		return created, err
	}
	if created > 0 {
		slog.Warn("directory repair: materialised missing directories", "count", created)
	}
	// Not transient, and not something the sweep can resolve on its own, so it
	// is named on every run until someone acts on it.
	for _, p := range blocked {
		slog.Warn("directory repair: a file occupies a path that must be a directory", "path", p)
	}
	return created, nil
}
