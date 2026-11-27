package jobs_test

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/jobs"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

// writeThumbFile creates a fake cache entry at <dir>/<hexHash>_256_cover.jpg.
func writeThumbFile(t *testing.T, dir, hexHash string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, hexHash+"_256_cover.jpg")
	if err := os.WriteFile(path, []byte("fake-thumb"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// PruneThumbs walks <data>/thumbs/v2/<userID>/<rootID>/*, deleting entries
// whose owning user/root no longer exists in the DB, plus entries whose
// content-hash prefix no longer matches a live (non-deleted) file in that
// root. This exercises the DB-backed orchestration (loadActiveHashesByRoot +
// the three deletion branches); pruneThumbFiles/parseThumbHash themselves
// are covered by the pure-filesystem tests in thumb_prune_test.go.
func TestPruneThumbs_RemovesOrphansAndStaleKeepsLive(t *testing.T) {
	pool, stor, usersRepo, syncRootsRepo := newPruneEnv(t)
	ctx := context.Background()
	filesRepo := repository.NewFileRepository(pool)

	suffix := uniqueJobsSuffix()
	u, err := usersRepo.Create(ctx, "thumb_"+suffix, "thumb_"+suffix+"@t.local", "x", model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, u.ID)
	})
	root, err := syncRootsRepo.CreateSyncRoot(ctx, u.ID, "thumb_root_"+suffix, "/thumb_"+suffix)
	if err != nil {
		t.Fatalf("create root: %v", err)
	}

	liveHash := []byte("live-content-hash-32-bytes-long")
	if _, err := filesRepo.Upsert(ctx, &model.File{
		UserID: u.ID, SyncRootID: root.ID, Path: "/a.txt", Size: 10, MTime: time.Now(),
		ContentHash: liveHash,
	}); err != nil {
		t.Fatalf("upsert live file: %v", err)
	}

	thumbsRoot := filepath.Join(stor.DataPath(), "thumbs", "v2")
	liveDir := filepath.Join(thumbsRoot, u.ID.String(), root.ID.String())
	liveHex := hex.EncodeToString(liveHash)
	liveThumb := writeThumbFile(t, liveDir, liveHex)
	staleThumb := writeThumbFile(t, liveDir, "deadbeefdeadbeefdeadbeefdeadbeef")

	// Orphan root: real user, but a root id with no DB row.
	orphanRootID := uuid.New()
	orphanRootDir := filepath.Join(thumbsRoot, u.ID.String(), orphanRootID.String())
	writeThumbFile(t, orphanRootDir, "ff00ff00ff00ff00ff00ff00ff00ff00")

	// Orphan user: a user id with no DB row at all.
	orphanUserID := uuid.New()
	orphanUserDir := filepath.Join(thumbsRoot, orphanUserID.String(), uuid.New().String())
	writeThumbFile(t, orphanUserDir, "ee00ee00ee00ee00ee00ee00ee00ee00")

	removed, err := jobs.PruneThumbs(ctx, pool, stor.DataPath())
	if err != nil {
		t.Fatalf("PruneThumbs: %v", err)
	}
	if removed != 3 {
		t.Errorf("removed = %d, want 3 (stale hash + orphan root + orphan user)", removed)
	}

	if _, err := os.Stat(liveThumb); err != nil {
		t.Errorf("live thumb should survive: %v", err)
	}
	if _, err := os.Stat(staleThumb); !os.IsNotExist(err) {
		t.Errorf("stale thumb should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(thumbsRoot, u.ID.String(), orphanRootID.String())); !os.IsNotExist(err) {
		t.Errorf("orphan root dir should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(thumbsRoot, orphanUserID.String())); !os.IsNotExist(err) {
		t.Errorf("orphan user dir should be removed, stat err = %v", err)
	}
}

// No thumbs/v2 directory at all is the common case (fresh install) and must
// short-circuit to (0, nil) rather than erroring on the missing root.
func TestPruneThumbs_MissingDirIsNoop(t *testing.T) {
	pool, stor, _, _ := newPruneEnv(t)
	removed, err := jobs.PruneThumbs(context.Background(), pool, stor.DataPath())
	if err != nil {
		t.Fatalf("PruneThumbs: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

func uniqueJobsSuffix() string {
	return uuid.NewString()[:10]
}
