package cron_test

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/bus"
	"privasync/server/internal/cron"
	"privasync/server/internal/db"
	"privasync/server/internal/model"
	"privasync/server/internal/quota"
	"privasync/server/internal/repository"
	"privasync/server/internal/storage"
)

var (
	poolOnce sync.Once
	testPool *pgxpool.Pool
	poolErr  error
)

func loadDotenv() {
	dir, _ := os.Getwd()
	for i := 0; i < 6; i++ {
		path := filepath.Join(dir, ".env")
		if f, err := os.Open(path); err == nil {
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				eq := strings.IndexByte(line, '=')
				if eq < 1 {
					continue
				}
				k := strings.TrimSpace(line[:eq])
				v := strings.Trim(strings.TrimSpace(line[eq+1:]), `"'`)
				if os.Getenv(k) == "" {
					os.Setenv(k, os.ExpandEnv(v))
				}
			}
			f.Close()
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

func getPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	poolOnce.Do(func() {
		loadDotenv()
		url := os.Getenv("TEST_DATABASE_URL")
		if url == "" {
			poolErr = errors.New("TEST_DATABASE_URL not set")
			return
		}
		cfg, err := pgxpool.ParseConfig(url)
		if err != nil {
			poolErr = err
			return
		}
		cfg.MaxConns = 4
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		p, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			poolErr = err
			return
		}
		if err := p.Ping(ctx); err != nil {
			poolErr = err
			return
		}
		if err := db.Migrate(p); err != nil {
			poolErr = err
			return
		}
		testPool = p
	})
	if poolErr != nil {
		t.Skipf("test DB unavailable: %v", poolErr)
	}
	return testPool
}

type purgeEnv struct {
	pool      *pgxpool.Pool
	users     *repository.UserRepository
	syncRoots *repository.SyncRootRepository
	stor      *storage.Service
	bus       bus.Bus
	purger    *cron.Purger
}

func newPurgeEnv(t *testing.T) *purgeEnv {
	t.Helper()
	pool := getPool(t)
	stor := storage.NewService(t.TempDir())
	users := repository.NewUserRepository(pool, stor)
	syncRoots := repository.NewSyncRootRepository(pool, stor)
	trash := repository.NewTrashRepository(pool)
	svc := quota.New(users, pool, nil, "http://test")
	b := bus.New(bus.Config{ReactiveWorkers: 1})
	svc.Register(b)
	t.Cleanup(b.Close)
	p := cron.NewPurger(pool, syncRoots, trash, stor, b, 30*24*time.Hour)
	return &purgeEnv{pool: pool, users: users, syncRoots: syncRoots, stor: stor, bus: b, purger: p}
}

func seedUserWithTrash(t *testing.T, env *purgeEnv, sizeBytes int64, deletedAt time.Time) (*model.User, *model.SyncRoot, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	u, err := env.users.Create(ctx, "cr_"+suffix, "cr_"+suffix+"@test.local", "x", model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = env.pool.Exec(c, `DELETE FROM users WHERE id = $1`, u.ID)
	})
	if _, err := env.pool.Exec(ctx,
		`UPDATE users SET quota_bytes=10000, used_bytes=0, trash_bytes=$1 WHERE id=$2`,
		sizeBytes, u.ID); err != nil {
		t.Fatalf("seed trash counter: %v", err)
	}
	sr, err := env.syncRoots.CreateSyncRoot(ctx, u.ID, "root_"+suffix, "/root_"+suffix)
	if err != nil {
		t.Fatalf("create sync root: %v", err)
	}
	var fileID uuid.UUID
	if err := env.pool.QueryRow(ctx, `
		INSERT INTO files
		  (user_id, sync_root_id, path, size, mtime, content_hash, is_deleted,
		   is_dir, updated_at, server_version, deleted_at, trash_path)
		VALUES ($1, $2, $3, $4, NOW(), 'h', TRUE, FALSE, NOW(), 1, $5, $6)
		RETURNING id
	`, u.ID, sr.ID, "/old.txt", sizeBytes, deletedAt, ".trash/old.txt").Scan(&fileID); err != nil {
		t.Fatalf("insert trashed file: %v", err)
	}
	return u, sr, fileID
}

func TestPurger_RemovesOldTrashAndReleasesQuota(t *testing.T) {
	env := newPurgeEnv(t)
	old := time.Now().Add(-31 * 24 * time.Hour)
	u, _, fileID := seedUserWithTrash(t, env, 250, old)

	n, err := env.purger.PurgeOnce(context.Background())
	if err != nil {
		t.Fatalf("PurgeOnce: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged = %d, want 1", n)
	}

	var rows int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM files WHERE id = $1`, fileID).Scan(&rows); err != nil {
		t.Fatalf("count file: %v", err)
	}
	if rows != 0 {
		t.Errorf("file row not deleted (rows=%d)", rows)
	}

	got, err := env.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.TrashBytes != 0 {
		t.Errorf("trash_bytes = %d, want 0 (released by bus subscriber)", got.TrashBytes)
	}
}

func TestPurger_SkipsRecentTrash(t *testing.T) {
	env := newPurgeEnv(t)
	recent := time.Now().Add(-1 * 24 * time.Hour)
	u, _, fileID := seedUserWithTrash(t, env, 250, recent)

	n, err := env.purger.PurgeOnce(context.Background())
	if err != nil {
		t.Fatalf("PurgeOnce: %v", err)
	}
	if n != 0 {
		t.Errorf("purged = %d, want 0 (file is recent)", n)
	}

	var rows int
	_ = env.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM files WHERE id = $1`, fileID).Scan(&rows)
	if rows != 1 {
		t.Errorf("file row missing (rows=%d)", rows)
	}

	got, _ := env.users.FindByID(context.Background(), u.ID)
	if got.TrashBytes != 250 {
		t.Errorf("trash_bytes = %d, want 250 (untouched)", got.TrashBytes)
	}
}

// Run sweeps immediately, then loops on the ticker until ctx is cancelled.
// Bound the wait with an outer timeout instead of a raw sleep so the test
// fails fast (rather than hanging) if the ctx.Done() branch is ever broken.
func TestPurger_Run_StopsOnContextCancel(t *testing.T) {
	env := newPurgeEnv(t)
	old := time.Now().Add(-31 * 24 * time.Hour)
	_, _, fileID := seedUserWithTrash(t, env, 250, old)

	// Two separate claims, and neither is about how fast the DB answers: the
	// sweep on entry purges the seeded file, and a cancelled context makes Run
	// return. Waiting for the row to disappear rather than for a fixed
	// duration keeps a slow shared DB from deciding the outcome.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		env.purger.Run(ctx, 10*time.Millisecond)
		close(done)
	}()

	deadline := time.Now().Add(30 * time.Second)
	for {
		var rows int
		_ = env.pool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM files WHERE id = $1`, fileID).Scan(&rows)
		if rows == 0 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("the sweep never purged the seeded file")
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
