package mail_test

import (
	"bufio"
	"context"
	"encoding/json"
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
	"privasync/server/internal/mail"
	"privasync/server/internal/model"
	"privasync/server/internal/outbox"
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

type subEnv struct {
	pool   *pgxpool.Pool
	users  *repository.UserRepository
	outbox *outbox.Repository
	sub    *mail.Subscriber
}

func newSubEnv(t *testing.T) *subEnv {
	t.Helper()
	pool := getPool(t)
	stor := storage.NewService(t.TempDir())
	users := repository.NewUserRepository(pool, stor)
	ob := outbox.NewRepository(pool)
	return &subEnv{
		pool:   pool,
		users:  users,
		outbox: ob,
		sub:    mail.NewSubscriber(ob, users),
	}
}

// runInTx wraps the subscriber call in a real TX so we exercise the critical
// path: the insert lands in the TX, commit makes it visible, rollback would
// erase it. Tests pass committed=true to keep the row for assertions.
func runInTx(t *testing.T, pool *pgxpool.Pool, fn func(tx pgx.Tx) error, commit bool) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		t.Fatalf("subscriber: %v", err)
	}
	if commit {
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
}

func seedSubUser(t *testing.T, env *subEnv, state string, emailNotify bool) *model.User {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	u, err := env.users.Create(context.Background(),
		"ms_"+suffix, "ms_"+suffix+"@test.local", "x", model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Outbox rows reference the user only inside the JSON payload (no FK),
		// so they must be swept explicitly or they poison later outbox tests.
		_, _ = env.pool.Exec(ctx,
			`DELETE FROM outbox
			 WHERE kind IN ('mail_sync_conflict', 'mail_quota_warning')
			   AND payload->>'user_id' = $1`, u.ID.String())
		_, _ = env.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID)
	})
	if _, err := env.pool.Exec(context.Background(), `
		UPDATE users SET quota_state=$1, email_notify=$2,
		                 quota_bytes=10000, used_bytes=8200, trash_bytes=0
		WHERE id=$3`, state, emailNotify, u.ID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}

// mailRow pairs the outbox row's kind column with the mail payload fields. The
// kind now lives in the column (not a payload discriminator) since the outbox split
// split mail_send into typed kinds.
type mailRow struct {
	RowKind  string
	NewState string `json:"new_state"`
	UsedPct  int    `json:"used_pct"`
}

func selectMailRows(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) []mailRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT kind, payload FROM outbox
		 WHERE kind IN ('mail_sync_conflict', 'mail_quota_warning')
		 ORDER BY created_at ASC`)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()
	var out []mailRow
	for rows.Next() {
		var kind string
		var raw []byte
		if err := rows.Scan(&kind, &raw); err != nil {
			t.Fatalf("scan: %v", err)
		}
		var p mailRow
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		p.RowKind = kind
		// The tests scope by deleting the user in cleanup, so rows for other
		// tests can coexist. Filter by NewState/UsedPct against per-test seeded
		// values when asserting.
		out = append(out, p)
		_ = userID
	}
	return out
}

func TestSubscriber_OnQuotaThresholdCrossed_EnqueuesOutboxRow(t *testing.T) {
	env := newSubEnv(t)
	u := seedSubUser(t, env, "warning80", true)

	runInTx(t, env.pool, func(tx pgx.Tx) error {
		return env.sub.OnQuotaThresholdCrossed(context.Background(), tx, bus.QuotaThresholdCrossed{
			UserID: u.ID, NewState: "warning80", UsedPct: 82,
		})
	}, true)

	var hit *mailRow
	for _, p := range selectMailRows(t, env.pool, u.ID) {
		if p.NewState == "warning80" && p.UsedPct == 82 {
			hit = &p
			break
		}
	}
	if hit == nil {
		t.Fatal("expected outbox mail row with warning80/82")
	}
	if hit.RowKind != "mail_quota_warning" {
		t.Errorf("kind = %q, want mail_quota_warning", hit.RowKind)
	}
}

func TestSubscriber_OnQuotaThresholdCrossed_SkipsWhenEmailNotifyOff(t *testing.T) {
	env := newSubEnv(t)
	u := seedSubUser(t, env, "warning80", false)

	runInTx(t, env.pool, func(tx pgx.Tx) error {
		return env.sub.OnQuotaThresholdCrossed(context.Background(), tx, bus.QuotaThresholdCrossed{
			UserID: u.ID, NewState: "warning80", UsedPct: 83,
		})
	}, true)

	for _, p := range selectMailRows(t, env.pool, u.ID) {
		if p.UsedPct == 83 {
			t.Fatalf("did not expect outbox row for opted-out user, got %+v", p)
		}
	}
}

// TestSubscriber_OnQuotaThresholdCrossed_RollbackRemovesOutboxRow proves the
// crash-safety contract: when the publisher's TX rolls back, the mail row
// vanishes with it. No half-state where a mail goes out for a band
// transition that never committed.
func TestSubscriber_OnQuotaThresholdCrossed_RollbackRemovesOutboxRow(t *testing.T) {
	env := newSubEnv(t)
	u := seedSubUser(t, env, "warning80", true)

	runInTx(t, env.pool, func(tx pgx.Tx) error {
		return env.sub.OnQuotaThresholdCrossed(context.Background(), tx, bus.QuotaThresholdCrossed{
			UserID: u.ID, NewState: "warning80", UsedPct: 85,
		})
	}, false /* rollback */)

	for _, p := range selectMailRows(t, env.pool, u.ID) {
		if p.UsedPct == 85 {
			t.Fatalf("rolled-back TX must not leak an outbox row, got %+v", p)
		}
	}
}

func TestSubscriber_OnConflictCreated_EnqueuesSyncConflictMail(t *testing.T) {
	env := newSubEnv(t)
	u := seedSubUser(t, env, "ok", true)
	path := "/conflict-" + uuid.NewString() + ".txt"

	runInTx(t, env.pool, func(tx pgx.Tx) error {
		return env.sub.OnConflictCreated(context.Background(), tx, bus.ConflictCreated{
			UserID: u.ID, ConflictID: uuid.New(), FilePath: path,
		})
	}, true)

	rows, err := env.pool.Query(context.Background(),
		`SELECT payload FROM outbox WHERE kind='mail_sync_conflict' AND payload->>'file_path' = $1`, path)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected outbox mail_sync_conflict row for the conflict file path")
	}
}

// A conflict mail is held back for the daily digest, so the worker must not
// pick it up before the next digest time.
func TestSubscriber_OnConflictCreated_DefersMailToDailyDigest(t *testing.T) {
	env := newSubEnv(t)
	u := seedSubUser(t, env, "ok", true)
	path := "/conflict-" + uuid.NewString() + ".txt"

	runInTx(t, env.pool, func(tx pgx.Tx) error {
		return env.sub.OnConflictCreated(context.Background(), tx, bus.ConflictCreated{
			UserID: u.ID, ConflictID: uuid.New(), FilePath: path,
		})
	}, true)

	var wait time.Duration
	err := env.pool.QueryRow(context.Background(),
		`SELECT next_attempt_at - NOW() FROM outbox
		 WHERE kind='mail_sync_conflict' AND payload->>'file_path' = $1`, path).Scan(&wait)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	if wait <= 0 || wait > 24*time.Hour+time.Minute {
		t.Fatalf("conflict mail must wait for the next daily digest, waits %v", wait)
	}
}

func TestNextConflictDigest(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	at := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, loc) }
	for _, tc := range []struct {
		name      string
		now, want time.Time
	}{
		{"before digest hour", at(11, 3, 24), at(11, 8, 0)},
		{"exactly at digest hour", at(11, 8, 0), at(12, 8, 0)},
		{"after digest hour", at(11, 14, 30), at(12, 8, 0)},
		{"month end", time.Date(2026, 11, 30, 23, 0, 0, 0, loc), time.Date(2026, 12, 1, 8, 0, 0, 0, loc)},
	} {
		if got := mail.NextConflictDigest(tc.now); !got.Equal(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSubscriber_OnConflictCreated_SkipsWhenEmailNotifyOff(t *testing.T) {
	env := newSubEnv(t)
	u := seedSubUser(t, env, "ok", false)
	path := "/conflict-" + uuid.NewString() + ".txt"

	runInTx(t, env.pool, func(tx pgx.Tx) error {
		return env.sub.OnConflictCreated(context.Background(), tx, bus.ConflictCreated{
			UserID: u.ID, ConflictID: uuid.New(), FilePath: path,
		})
	}, true)

	var n int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outbox WHERE kind='mail_sync_conflict' AND payload->>'file_path' = $1`, path).Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected no outbox row for opted-out user, got %d", n)
	}
}

// TestSubscriber_OnConflictCreated_RollbackRemovesOutboxRow mirrors the
// quota crash-safety test: a conflict that the coordinator rolls back must
// not produce a mail.
func TestSubscriber_OnConflictCreated_RollbackRemovesOutboxRow(t *testing.T) {
	env := newSubEnv(t)
	u := seedSubUser(t, env, "ok", true)
	path := "/conflict-rollback-" + uuid.NewString() + ".txt"

	runInTx(t, env.pool, func(tx pgx.Tx) error {
		return env.sub.OnConflictCreated(context.Background(), tx, bus.ConflictCreated{
			UserID: u.ID, ConflictID: uuid.New(), FilePath: path,
		})
	}, false /* rollback */)

	var n int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outbox WHERE kind='mail_sync_conflict' AND payload->>'file_path' = $1`, path).Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if n != 0 {
		t.Fatalf("rolled-back TX must not leak an outbox row, got %d", n)
	}
}
