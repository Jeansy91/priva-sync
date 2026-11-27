package quota_test

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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/bus"
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

type qEnv struct {
	pool  *pgxpool.Pool
	users *repository.UserRepository
	svc   *quota.Service
	bus   bus.Bus
}

func newQuotaEnv(t *testing.T) *qEnv {
	t.Helper()
	pool := getPool(t)
	stor := storage.NewService(t.TempDir())
	users := repository.NewUserRepository(pool, stor)
	svc := quota.New(users, pool, nil, "http://test")
	b := bus.New(bus.Config{ReactiveWorkers: 1})
	svc.Register(b)
	t.Cleanup(b.Close)
	return &qEnv{pool: pool, users: users, svc: svc, bus: b}
}

func seedQuotaUser(t *testing.T, env *qEnv, used, trash, quotaBytes int64) *model.User {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	u, err := env.users.Create(context.Background(), "qt_"+suffix, "qt_"+suffix+"@test.local", "x", model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = env.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID)
	})
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE users SET quota_bytes=$1, used_bytes=$2, trash_bytes=$3 WHERE id=$4`,
		quotaBytes, used, trash, u.ID); err != nil {
		t.Fatalf("seed counters: %v", err)
	}
	u.QuotaBytes = quotaBytes
	u.UsedBytes = used
	u.TrashBytes = trash
	return u
}

func inTx(t *testing.T, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		t.Fatalf("tx fn: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestSetUsedAndTrashBytesTx_OverwritesAbsolute(t *testing.T) {
	env := newQuotaEnv(t)
	u := seedQuotaUser(t, env, 500, 1000, 10000)

	inTx(t, env.pool, func(tx pgx.Tx) error {
		_, err := env.users.SetUsedAndTrashBytesTx(context.Background(), tx, u.ID, 200, 50)
		return err
	})

	got, err := env.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.UsedBytes != 200 || got.TrashBytes != 50 {
		t.Errorf("got used=%d trash=%d, want 200/50", got.UsedBytes, got.TrashBytes)
	}
}

func TestOnFilePurgedByCron_ReleasesTrashBytes(t *testing.T) {
	env := newQuotaEnv(t)
	u := seedQuotaUser(t, env, 500, 1000, 10000)

	inTx(t, env.pool, func(tx pgx.Tx) error {
		return env.bus.PublishCritical(context.Background(), tx, bus.FilePurgedByCron{
			UserID: u.ID, RootID: uuid.New(), FileID: uuid.New(), Bytes: 300,
		})
	})

	got, err := env.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.UsedBytes != 500 {
		t.Errorf("used_bytes = %d, want 500 (unchanged)", got.UsedBytes)
	}
	if got.TrashBytes != 700 {
		t.Errorf("trash_bytes = %d, want 700", got.TrashBytes)
	}
}

func TestOnAdminQuotaAdjusted_OverwritesAbsolute(t *testing.T) {
	env := newQuotaEnv(t)
	// Seed with drift: stored counters claim 9000 used, but truth is 100.
	u := seedQuotaUser(t, env, 9000, 0, 10000)

	inTx(t, env.pool, func(tx pgx.Tx) error {
		return env.bus.PublishCritical(context.Background(), tx, bus.AdminQuotaAdjusted{
			UserID: u.ID, NewUsed: 100, NewTrash: 50,
		})
	})

	got, err := env.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.UsedBytes != 100 || got.TrashBytes != 50 {
		t.Errorf("got used=%d trash=%d, want 100/50", got.UsedBytes, got.TrashBytes)
	}
}

// Counters past int4 must survive the absolute write: the recount feeds real
// byte totals, and an untyped 0 in the UPDATE's GREATEST used to make Postgres
// infer int4 for the parameter, failing every account over 2 GB.
func TestOnAdminQuotaAdjusted_AcceptsCountersBeyondInt4(t *testing.T) {
	env := newQuotaEnv(t)
	u := seedQuotaUser(t, env, 0, 0, 0)

	const (
		wantUsed  int64 = 58263084747 // ~54 GiB, well past math.MaxInt32
		wantTrash int64 = 4294967296  // exactly 4 GiB
	)
	inTx(t, env.pool, func(tx pgx.Tx) error {
		return env.bus.PublishCritical(context.Background(), tx, bus.AdminQuotaAdjusted{
			UserID: u.ID, NewUsed: wantUsed, NewTrash: wantTrash,
		})
	})

	got, err := env.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.UsedBytes != wantUsed || got.TrashBytes != wantTrash {
		t.Errorf("counters = used=%d trash=%d, want %d/%d",
			got.UsedBytes, got.TrashBytes, wantUsed, wantTrash)
	}
}

func TestRecomputeViaBus_HealsDriftFromFilesTable(t *testing.T) {
	env := newQuotaEnv(t)
	u := seedQuotaUser(t, env, 9000, 9000, 10000) // drift: counters lie
	syncRoots := repository.NewSyncRootRepository(env.pool, storage.NewService(t.TempDir()))
	sr, err := syncRoots.CreateSyncRoot(context.Background(), u.ID, "r", "/r")
	if err != nil {
		t.Fatalf("create sync root: %v", err)
	}
	// Truth: one live file (100) + one trashed file (50)
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO files (user_id, sync_root_id, path, size, mtime, content_hash,
		                   is_deleted, is_dir, updated_at, server_version)
		VALUES ($1, $2, '/live.txt', 100, NOW(), 'h', FALSE, FALSE, NOW(), 1)
	`, u.ID, sr.ID); err != nil {
		t.Fatalf("insert live: %v", err)
	}
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO files (user_id, sync_root_id, path, size, mtime, content_hash,
		                   is_deleted, is_dir, updated_at, server_version,
		                   deleted_at, trash_path)
		VALUES ($1, $2, '/old.txt', 50, NOW(), 'h', TRUE, FALSE, NOW(), 1,
		        NOW(), '.trash/old.txt')
	`, u.ID, sr.ID); err != nil {
		t.Fatalf("insert trash: %v", err)
	}

	if err := env.svc.RecomputeViaBus(context.Background(), u.ID); err != nil {
		t.Fatalf("RecomputeViaBus: %v", err)
	}

	got, err := env.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.UsedBytes != 100 || got.TrashBytes != 50 {
		t.Errorf("counters = used=%d trash=%d, want 100/50", got.UsedBytes, got.TrashBytes)
	}
}

func TestOnAdminQuotaAdjusted_DeEscalatesBandToOK(t *testing.T) {
	env := newQuotaEnv(t)
	// User sits in critical95 with drifted counters; recount drops them well
	// below the warning band — state must follow.
	u := seedQuotaUser(t, env, 9700, 0, 10000)
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE users SET quota_state='critical95' WHERE id=$1`, u.ID); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	inTx(t, env.pool, func(tx pgx.Tx) error {
		return env.bus.PublishCritical(context.Background(), tx, bus.AdminQuotaAdjusted{
			UserID: u.ID, NewUsed: 100, NewTrash: 0,
		})
	})

	got, err := env.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.QuotaState != "ok" {
		t.Errorf("quota_state = %q, want %q", got.QuotaState, "ok")
	}
}
