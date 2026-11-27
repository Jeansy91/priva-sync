package jobs_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/jobs"
)

func TestPruneOrphanUserDirs_RemovesDirWhenNeitherDbNorSidecarHasUser(t *testing.T) {
	pool, stor, _, _ := newPruneEnv(t)
	ctx := context.Background()

	ghostUserID := uuid.New()
	ghostDir := filepath.Join(stor.DataPath(), ghostUserID.String())
	if err := os.MkdirAll(filepath.Join(ghostDir, "root-x"), 0o755); err != nil {
		t.Fatalf("mkdir ghost dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ghostDir, "root-x", "file.bin"), []byte("orphan"), 0o644); err != nil {
		t.Fatalf("write canary: %v", err)
	}

	removed, err := jobs.PruneOrphanUserDirs(ctx, pool, stor.DataPath())
	if err != nil {
		t.Fatalf("PruneOrphanUserDirs: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(ghostDir); !os.IsNotExist(err) {
		t.Errorf("orphan user-dir must be gone, stat err = %v", err)
	}
}

func TestPruneOrphanUserDirs_KeepsDirWhenSidecarExists(t *testing.T) {
	pool, stor, _, _ := newPruneEnv(t)
	ctx := context.Background()

	// User has no DB row but a users-sidecar — per ADR-0001 the sidecar
	// is the recovery floor, so the data dir must remain on disk.
	survivorID := uuid.New()
	sidecarPath := filepath.Join(stor.DataPath(), "users", survivorID.String()+".json")
	if err := os.MkdirAll(filepath.Dir(sidecarPath), 0o755); err != nil {
		t.Fatalf("mkdir users: %v", err)
	}
	if err := os.WriteFile(sidecarPath, []byte(`{"id":"`+survivorID.String()+`"}`), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(sidecarPath) })

	dataDir := filepath.Join(stor.DataPath(), survivorID.String())
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}

	removed, err := jobs.PruneOrphanUserDirs(ctx, pool, stor.DataPath())
	if err != nil {
		t.Fatalf("PruneOrphanUserDirs: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0 (sidecar keeps user alive)", removed)
	}
	if _, err := os.Stat(dataDir); err != nil {
		t.Errorf("data dir must remain because sidecar exists: %v", err)
	}
}

func TestPruneOrphanUserDirs_SkipsReservedDirs(t *testing.T) {
	pool, stor, _, _ := newPruneEnv(t)
	ctx := context.Background()

	for _, name := range []string{"users", "sync_roots", "shares", "avatars"} {
		if err := os.MkdirAll(filepath.Join(stor.DataPath(), name), 0o755); err != nil {
			t.Fatalf("mkdir reserved %s: %v", name, err)
		}
	}

	removed, err := jobs.PruneOrphanUserDirs(ctx, pool, stor.DataPath())
	if err != nil {
		t.Fatalf("PruneOrphanUserDirs: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0 (only reserved dirs present)", removed)
	}
	for _, name := range []string{"users", "sync_roots", "shares", "avatars"} {
		if _, err := os.Stat(filepath.Join(stor.DataPath(), name)); err != nil {
			t.Errorf("reserved dir %s must remain: %v", name, err)
		}
	}
}
