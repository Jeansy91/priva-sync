package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/storage"
)

// PruneOrphanUserDirs removes top-level `/data/{user_id}/` directories
// whose user is unreachable via BOTH the DB and the users/ sidecar floor.
// This job is destructive — it walks the user-data tree and deletes the
// entire subtree per orphan, including any sync_root data inside.
//
// Registered as a manual-only job: with neither a DB row nor a sidecar
// there is no recovery path for the user, but operators still confirm
// the deletion explicitly via the admin UI before it runs.
//
// Returns the count of user-data-dirs removed.
func PruneOrphanUserDirs(ctx context.Context, pool *pgxpool.Pool, dataPath string) (int, error) {
	dbUserIDs, err := loadIDSet(ctx, pool, `SELECT id FROM users`)
	if err != nil {
		return 0, fmt.Errorf("load users: %w", err)
	}
	liveUserIDs := unionWithSidecarUsers(dbUserIDs, filepath.Join(dataPath, storage.DirUsers))
	return pruneOrphanUserDirs(dataPath, liveUserIDs), nil
}

// reservedTopLevelDirs lists `/data` entries that are NOT user-data-dirs.
// Hidden directories (starting with '.') are also skipped.
var reservedTopLevelDirs = map[string]struct{}{
	storage.DirUsers:     {},
	storage.DirSyncRoots: {},
	storage.DirShares:    {},
	storage.DirAvatars:   {},
}

// unionWithSidecarUsers returns the union of `dbUserIDs` and every UUID
// parsed from `{usersDir}/*.json`. Sidecars are the recovery floor (see
// ADR-0001) — as long as one exists, the user is recoverable and must
// not be treated as gone. Missing usersDir (fresh install) is ignored.
func unionWithSidecarUsers(dbUserIDs map[uuid.UUID]struct{}, usersDir string) map[uuid.UUID]struct{} {
	out := make(map[uuid.UUID]struct{}, len(dbUserIDs))
	for id := range dbUserIDs {
		out[id] = struct{}{}
	}
	entries, err := os.ReadDir(usersDir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("user-sidecar union: read dir failed", "dir", usersDir, "error", err)
		}
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		idStr := strings.TrimSuffix(e.Name(), ".json")
		id, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}
		out[id] = struct{}{}
	}
	return out
}

// pruneOrphanUserDirs walks the top level of `dataPath`, treats each
// UUID-named subdirectory as a user-data-dir, and removes any whose ID is
// not in `liveUserIDs`. liveUserIDs must be the union of DB users and
// users/ sidecars — only with both missing is the directory truly
// unreachable orphan storage. Returns the count of dirs removed.
//
// Non-UUID top-level entries (and reserved/hidden ones) are skipped with a
// warning so operators notice unexpected layout.
func pruneOrphanUserDirs(dataPath string, liveUserIDs map[uuid.UUID]struct{}) int {
	entries, err := os.ReadDir(dataPath)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("orphan user-dir prune: read dataPath failed", "path", dataPath, "error", err)
		}
		return 0
	}
	removed := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if _, reserved := reservedTopLevelDirs[name]; reserved {
			continue
		}
		id, err := uuid.Parse(name)
		if err != nil {
			slog.Warn("orphan user-dir prune: unexpected top-level dir", "name", name)
			continue
		}
		if _, alive := liveUserIDs[id]; alive {
			continue
		}
		path := filepath.Join(dataPath, name)
		if err := os.RemoveAll(path); err != nil {
			slog.Warn("orphan user-dir prune: remove failed",
				"user_id", id, "path", path, "error", err)
			continue
		}
		removed++
		slog.Info("orphan user-dir prune: removed unreachable data dir",
			"user_id", id, "path", path)
	}
	return removed
}
