package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/storage"
)

// PruneSidecars removes JSON sidecars whose backing DB row is gone. Scope:
// sync_roots/* and shares/*. users/* are intentionally NOT pruned — they are
// the recovery floor (see ADR-0001); deleting them on a transient DB blip
// would destroy the only persistent user record.
//
// A sync_root or share sidecar is removed when EITHER the entity ID is
// missing from the DB OR the referenced user_id is missing (orphan ref).
//
// Returns the count of sidecar files removed.
func PruneSidecars(ctx context.Context, pool *pgxpool.Pool, dataPath string) (int, error) {
	userIDs, err := loadIDSet(ctx, pool, `SELECT id FROM users`)
	if err != nil {
		return 0, fmt.Errorf("load users: %w", err)
	}
	rootIDs, err := loadIDSet(ctx, pool, `SELECT id FROM sync_roots`)
	if err != nil {
		return 0, fmt.Errorf("load sync_roots: %w", err)
	}
	shareIDs, err := loadIDSet(ctx, pool, `SELECT id FROM shares`)
	if err != nil {
		return 0, fmt.Errorf("load shares: %w", err)
	}

	removed := 0
	removed += pruneDir(filepath.Join(dataPath, storage.DirSyncRoots), rootIDs, userIDs)
	removed += pruneDir(filepath.Join(dataPath, storage.DirShares), shareIDs, userIDs)
	return removed, nil
}

func loadIDSet(ctx context.Context, pool *pgxpool.Pool, query string) (map[uuid.UUID]struct{}, error) {
	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]struct{}{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// pruneDir scans dir for *.json files. A file is removed when either:
//   - the UUID parsed from its basename is not in liveIDs, or
//   - the parsed JSON has a user_id field that is not in liveUserIDs.
//
// Missing dir is a no-op (fresh install).
func pruneDir(dir string, liveIDs, liveUserIDs map[uuid.UUID]struct{}) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("sidecar prune: read dir failed", "dir", dir, "error", err)
		}
		return 0
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		idStr := strings.TrimSuffix(e.Name(), ".json")
		id, err := uuid.Parse(idStr)
		if err != nil {
			continue // unknown file shape — leave it alone
		}
		shouldRemove := false
		if _, ok := liveIDs[id]; !ok {
			shouldRemove = true
		} else if uref, ok := readUserRef(path); ok {
			if _, alive := liveUserIDs[uref]; !alive {
				shouldRemove = true
			}
		}
		if !shouldRemove {
			continue
		}
		if err := os.Remove(path); err != nil {
			slog.Warn("sidecar prune: remove failed", "path", path, "error", err)
			continue
		}
		removed++
	}
	return removed
}

func readUserRef(path string) (uuid.UUID, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return uuid.Nil, false
	}
	var meta struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(meta.UserID)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}
