package repository_test

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/auth"
	"privasync/server/internal/db"
	"privasync/server/internal/model"
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
			poolErr = context.DeadlineExceeded
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

func uniqueSuffix() string {
	return uuid.NewString()[:8]
}

// createTestUser inserts a throwaway user and registers cleanup.
func createTestUser(t *testing.T) *model.User {
	t.Helper()
	pool := getPool(t)
	stor := storage.NewService(t.TempDir())
	users := repository.NewUserRepository(pool, stor)

	hash, err := auth.HashPassword("irrelevant-password-123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	u, err := users.Create(context.Background(),
		"repo_test_"+uniqueSuffix(), "repo_test_"+uniqueSuffix()+"@t.local", hash, model.RoleUser)
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", u.ID)
	})
	return u
}

// fileAt builds an in-memory file row ready for Upsert -- a plain regular
// file with a fixed size/mtime, no content hash needed by the query tests.
func fileAt(userID, syncRootID uuid.UUID, path string) *model.File {
	return &model.File{
		UserID:     userID,
		SyncRootID: syncRootID,
		Path:       path,
		Size:       1,
		MTime:      time.Now(),
	}
}

// createTestSyncRoot inserts a sync root owned by userID and registers cleanup.
func createTestSyncRoot(t *testing.T, userID uuid.UUID) *model.SyncRoot {
	t.Helper()
	pool := getPool(t)
	stor := storage.NewService(t.TempDir())
	roots := repository.NewSyncRootRepository(pool, stor)

	sr, err := roots.CreateSyncRoot(context.Background(), userID, "root_"+uniqueSuffix(), "/data/"+uniqueSuffix())
	if err != nil {
		t.Fatalf("create test sync root: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM sync_roots WHERE id = $1", sr.ID)
	})
	return sr
}
