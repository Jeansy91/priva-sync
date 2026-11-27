package jobs_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/jobs"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
	"privasync/server/internal/storage"
)

func newPruneEnv(t *testing.T) (*pgxpool.Pool, *storage.Service, *repository.UserRepository, *repository.SyncRootRepository) {
	t.Helper()
	pool := getPool(t)
	stor := storage.NewService(t.TempDir())
	users := repository.NewUserRepository(pool, stor)
	syncRoots := repository.NewSyncRootRepository(pool, stor)
	return pool, stor, users, syncRoots
}

func writeOrphanRootMeta(t *testing.T, stor *storage.Service, id, userID uuid.UUID) string {
	t.Helper()
	path := filepath.Join(stor.DataPath(), "sync_roots", id.String()+".json")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	body, _ := json.Marshal(map[string]any{
		"id":         id.String(),
		"user_id":    userID.String(),
		"name":       "ghost",
		"path":       "/ghost",
		"created_at": "2024-01-01T00:00:00Z",
	})
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	return path
}

func writeOrphanShareMeta(t *testing.T, stor *storage.Service, id, userID uuid.UUID) string {
	t.Helper()
	path := filepath.Join(stor.DataPath(), "shares", id.String()+".json")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	body, _ := json.Marshal(map[string]any{
		"id":         id.String(),
		"user_id":    userID.String(),
		"path":       "/",
		"share_type": "public",
		"can_write":  false,
		"created_at": "2024-01-01T00:00:00Z",
	})
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	return path
}

func TestPruneSidecars_RemovesOrphanSyncRootAndKeepsLive(t *testing.T) {
	pool, stor, usersRepo, filesRepo := newPruneEnv(t)
	ctx := context.Background()

	// Live: real user + real sync root, both written to sidecars.
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	live, err := usersRepo.Create(ctx, "live_"+suffix, "live_"+suffix+"@t.local", "x", model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM users WHERE id=$1`, live.ID)
	})
	liveRoot, err := filesRepo.CreateSyncRoot(ctx, live.ID, "live_"+suffix, "/live_"+suffix)
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	liveRootPath := filepath.Join(stor.DataPath(), "sync_roots", liveRoot.ID.String()+".json")
	// CreateSyncRoot does not write the sidecar by itself in this minimal env,
	// so we materialize it explicitly to mirror the production write.
	if err := stor.WriteRootMeta(liveRoot); err != nil {
		t.Fatalf("write live meta: %v", err)
	}

	// Orphan A: sync_root id has no DB row.
	orphanRootID := uuid.New()
	orphanRootPath := writeOrphanRootMeta(t, stor, orphanRootID, live.ID)

	// Orphan B: sync_root id missing AND user_id missing — same prune reason,
	// belt-and-suspenders.
	orphanBothID := uuid.New()
	orphanBothPath := writeOrphanRootMeta(t, stor, orphanBothID, uuid.New())

	removed, err := jobs.PruneSidecars(ctx, pool, stor.DataPath())
	if err != nil {
		t.Fatalf("PruneSidecars: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2 (both orphans)", removed)
	}
	if _, err := os.Stat(liveRootPath); err != nil {
		t.Errorf("live root sidecar must remain: %v", err)
	}
	if _, err := os.Stat(orphanRootPath); !os.IsNotExist(err) {
		t.Errorf("orphan root sidecar must be gone, stat err = %v", err)
	}
	if _, err := os.Stat(orphanBothPath); !os.IsNotExist(err) {
		t.Errorf("orphan both sidecar must be gone, stat err = %v", err)
	}
}

func TestPruneSidecars_RemovesOrphanShareAndLeavesUsersAlone(t *testing.T) {
	pool, stor, usersRepo, _ := newPruneEnv(t)
	ctx := context.Background()

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	live, err := usersRepo.Create(ctx, "u_"+suffix, "u_"+suffix+"@t.local", "x", model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM users WHERE id=$1`, live.ID)
	})

	// Write a sidecar for the live user — must survive prune (users untouched
	// by design; protects recovery material).
	if err := stor.WriteUserMeta(live); err != nil {
		t.Fatalf("write user meta: %v", err)
	}
	liveUserPath := filepath.Join(stor.DataPath(), "users", live.ID.String()+".json")

	// Write a totally orphan users.json (no DB row). Must also survive.
	ghostUserID := uuid.New()
	ghostUserPath := filepath.Join(stor.DataPath(), "users", ghostUserID.String()+".json")
	_ = os.MkdirAll(filepath.Dir(ghostUserPath), 0o755)
	_ = os.WriteFile(ghostUserPath, []byte(`{"id":"`+ghostUserID.String()+`"}`), 0o644)

	// Orphan share — references a non-existent user, share itself not in DB.
	orphanShareID := uuid.New()
	orphanSharePath := writeOrphanShareMeta(t, stor, orphanShareID, uuid.New())

	removed, err := jobs.PruneSidecars(ctx, pool, stor.DataPath())
	if err != nil {
		t.Fatalf("PruneSidecars: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (only the share)", removed)
	}
	if _, err := os.Stat(orphanSharePath); !os.IsNotExist(err) {
		t.Errorf("orphan share must be gone, stat err = %v", err)
	}
	if _, err := os.Stat(liveUserPath); err != nil {
		t.Errorf("live user sidecar must remain: %v", err)
	}
	if _, err := os.Stat(ghostUserPath); err != nil {
		t.Errorf("orphan user sidecar must remain (users untouched by policy): %v", err)
	}
	_ = os.Remove(ghostUserPath)
}
