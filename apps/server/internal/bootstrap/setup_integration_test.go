package bootstrap_test

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
	"privasync/server/internal/bootstrap"
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

func newUserRepo(t *testing.T) *repository.UserRepository {
	t.Helper()
	pool := getPool(t)
	stor := storage.NewService(t.TempDir())
	return repository.NewUserRepository(pool, stor)
}

func uniqueSuffix() string {
	return uuid.NewString()[:8]
}

func TestInit_NoAdminYields_NeedsSetupTrue(t *testing.T) {
	users := newUserRepo(t)
	ctx := context.Background()

	hasAdmin, err := users.HasAdmin(ctx)
	if err != nil {
		t.Fatalf("HasAdmin: %v", err)
	}
	if hasAdmin {
		t.Skip("an admin already exists in this test DB -- cannot exercise the no-admin path")
	}

	state, err := bootstrap.Init(ctx, users)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !state.NeedsSetup() {
		t.Fatal("NeedsSetup() = false, want true when no admin exists")
	}
}

func TestInit_ExistingAdminYields_NeedsSetupFalse(t *testing.T) {
	users := newUserRepo(t)
	ctx := context.Background()

	hash, err := auth.HashPassword("irrelevant-password-123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	admin, err := users.Create(ctx, "bootstrap_admin_"+uniqueSuffix(), "bootstrap_"+uniqueSuffix()+"@t.local", hash, model.RoleAdmin)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	t.Cleanup(func() {
		_, _ = getPool(t).Exec(context.Background(), "DELETE FROM users WHERE id = $1", admin.ID)
	})

	state, err := bootstrap.Init(ctx, users)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if state.NeedsSetup() {
		t.Fatal("NeedsSetup() = true, want false when an admin already exists")
	}
	if state.Verify("anything") {
		t.Fatal("Verify() should always be false once setup is not needed")
	}
}
