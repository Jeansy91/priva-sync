package outbox_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/db"
	"privasync/server/internal/outbox"
)

var (
	poolOnce sync.Once
	pool     *pgxpool.Pool
	poolErr  error
)

func loadDotenv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
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
		pool = p
	})
	if poolErr != nil {
		t.Skipf("test DB unavailable: %v", poolErr)
	}
	// Each test gets a clean slate. Wiped before as well as after: the whole
	// suite shares one database, and a test binary that died mid-run (or a
	// package whose own cleanup does not cover this table) otherwise leaves
	// rows that decide what the global ClaimNext returns here.
	wipeOutbox(t)
	t.Cleanup(func() { wipeOutbox(t) })
	return pool
}

func wipeOutbox(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = pool.Exec(ctx, `DELETE FROM outbox`)
}

// dumpOutbox renders the table for a failure message.
//
// TestRepository_DifferentGroupsRunIndependently failed once in CI with
// "entry not found" on its second claim and has resisted every attempt to
// reproduce it — 25 runs of this package alone, and a run with the packages
// that write outbox rows going first, all green. The symptom narrows the
// cause sharply: foreign rows would make ClaimNext return the wrong group,
// not nothing, so at that moment both inserted rows had to be gone, already
// done, or scheduled into the future. Which of the three it is decides where
// to look, and guessing between them is what this avoids. Cheap, and only
// ever runs on the way to a failure.
func dumpOutbox(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := pool.Query(ctx, `
		SELECT group_id, seq, kind, attempts, next_attempt_at, done_at, NOW()
		FROM outbox ORDER BY created_at
	`)
	if err != nil {
		return "dump failed: " + err.Error()
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var (
			group         uuid.UUID
			seq, attempts int
			kind          string
			next, now     time.Time
			done          *time.Time
		)
		if err := rows.Scan(&group, &seq, &kind, &attempts, &next, &done, &now); err != nil {
			return "dump scan failed: " + err.Error()
		}
		fmt.Fprintf(&b, "  group=%s seq=%d kind=%s attempts=%d next_attempt_at=%s done_at=%v now=%s\n",
			group, seq, kind, attempts, next.Format(time.RFC3339Nano), done, now.Format(time.RFC3339Nano))
	}
	if b.Len() == 0 {
		return "  (table empty)"
	}
	return b.String()
}

// mustInsert fabricates a raw outbox row for tests that need synthetic kinds or
// payloads (arbitrary maps, nil). It goes straight to SQL rather than through
// repo.Insert, which now requires a typed outbox.Payload and derives the kind
// from it — the wrong tool for planting hand-crafted rows.
func mustInsert(t *testing.T, repo *outbox.Repository, group uuid.UUID, seq int, kind outbox.Kind, payload any) {
	t.Helper()
	_ = repo
	ctx := context.Background()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO outbox (group_id, seq, kind, payload) VALUES ($1, $2, $3, $4)`,
		group, seq, kind, raw); err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func conflictMail(user uuid.UUID, path string) map[string]any {
	return map[string]any{"user_id": user.String(), "file_path": path}
}

// A burst of conflict mails for one user must be foldable into a single digest:
// PendingConflictMails returns exactly that user's not-yet-done sync_conflict
// rows (no other users, no other kinds), and MarkManyDone clears them so the
// digest is sent once.
func TestRepository_PendingConflictMails_FoldsBurstPerUser(t *testing.T) {
	p := getPool(t)
	repo := outbox.NewRepository(p)
	user := uuid.New()
	other := uuid.New()

	mustInsert(t, repo, uuid.New(), 1, outbox.KindMailSyncConflict, conflictMail(user, "/a.txt"))
	mustInsert(t, repo, uuid.New(), 1, outbox.KindMailSyncConflict, conflictMail(user, "/b.txt"))
	mustInsert(t, repo, uuid.New(), 1, outbox.KindMailSyncConflict, conflictMail(user, "/c.txt"))
	mustInsert(t, repo, uuid.New(), 1, outbox.KindMailSyncConflict, conflictMail(other, "/x.txt"))
	mustInsert(t, repo, uuid.New(), 1, outbox.KindMailQuotaWarning, map[string]any{"user_id": user.String()})

	ctx := context.Background()
	pending, err := repo.PendingConflictMails(ctx, user)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 3 {
		t.Fatalf("want 3 sync_conflict mails for user, got %d", len(pending))
	}
	got := map[string]bool{}
	var ids []uuid.UUID
	for _, m := range pending {
		got[m.FilePath] = true
		ids = append(ids, m.ID)
	}
	for _, want := range []string{"/a.txt", "/b.txt", "/c.txt"} {
		if !got[want] {
			t.Fatalf("missing %s in %v", want, got)
		}
	}

	if err := repo.MarkManyDone(ctx, ids); err != nil {
		t.Fatalf("markmany: %v", err)
	}
	after, err := repo.PendingConflictMails(ctx, user)
	if err != nil {
		t.Fatalf("pending2: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("want 0 pending after MarkManyDone, got %d", len(after))
	}
}

func TestRepository_ClaimRespectsGroupOrdering(t *testing.T) {
	p := getPool(t)
	repo := outbox.NewRepository(p)
	group := uuid.New()

	mustInsert(t, repo, group, 1, outbox.KindStorageRename, map[string]string{"a": "1"})
	mustInsert(t, repo, group, 2, outbox.KindSSETyped, map[string]string{"a": "2"})
	mustInsert(t, repo, group, 3, outbox.KindMailQuotaWarning, map[string]string{"a": "3"})

	ctx := context.Background()
	// Drain serially and record the order — ordering inside a group_id
	// must be strict seq=1, 2, 3.
	var order []int
	for i := 0; i < 3; i++ {
		e, err := repo.ClaimNext(ctx)
		if err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		order = append(order, e.Seq)
		if err := repo.MarkDone(ctx, e.ID); err != nil {
			t.Fatalf("mark done: %v", err)
		}
	}
	want := []int{1, 2, 3}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("ordering: got %v, want %v", order, want)
		}
	}
	if _, err := repo.ClaimNext(ctx); !errors.Is(err, outbox.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after drain, got %v", err)
	}
}

func TestRepository_DifferentGroupsRunIndependently(t *testing.T) {
	p := getPool(t)
	repo := outbox.NewRepository(p)
	g1, g2 := uuid.New(), uuid.New()

	mustInsert(t, repo, g1, 1, outbox.KindStorageRename, nil)
	mustInsert(t, repo, g2, 1, outbox.KindStorageRename, nil)

	ctx := context.Background()
	seen := map[uuid.UUID]bool{}
	for i := 0; i < 2; i++ {
		e, err := repo.ClaimNext(ctx)
		if err != nil {
			t.Fatalf("claim %d: %v\noutbox:\n%s", i, err, dumpOutbox(t))
		}
		seen[e.GroupID] = true
		// Checked, not discarded: a MarkDone that quietly failed leaves the
		// same entry claimable, and the next claim then returns it again —
		// the assertion below would report the wrong groups without ever
		// naming the write that actually went missing.
		if err := repo.MarkDone(ctx, e.ID); err != nil {
			t.Fatalf("mark done %d (id=%s): %v", i, e.ID, err)
		}
	}
	if !seen[g1] || !seen[g2] {
		t.Fatalf("expected both groups served, got %v\noutbox:\n%s", seen, dumpOutbox(t))
	}
}

func TestWorker_WakeDrainsImmediately(t *testing.T) {
	p := getPool(t)
	repo := outbox.NewRepository(p)
	group := uuid.New()

	var ran int32
	w := outbox.NewWorker(repo, map[outbox.Kind]outbox.Handler{
		outbox.KindStorageRename: func(ctx context.Context, _ json.RawMessage) error {
			atomic.AddInt32(&ran, 1)
			return nil
		},
	})
	w.PollInterval = time.Hour // make sure only Wake can advance us
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	mustInsert(t, repo, group, 1, outbox.KindStorageRename, nil)
	w.Wake()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&ran) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if atomic.LoadInt32(&ran) != 1 {
		t.Fatalf("handler never ran")
	}

	cancel()
	<-done
}

func TestWorker_FailureSchedulesRetry(t *testing.T) {
	p := getPool(t)
	repo := outbox.NewRepository(p)
	group := uuid.New()

	w := outbox.NewWorker(repo, map[outbox.Kind]outbox.Handler{
		outbox.KindStorageRename: func(ctx context.Context, _ json.RawMessage) error {
			return errors.New("boom")
		},
	})
	w.PollInterval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	mustInsert(t, repo, group, 1, outbox.KindStorageRename, nil)
	w.Wake()

	deadline := time.Now().Add(2 * time.Second)
	var e outbox.Entry
	for time.Now().Before(deadline) {
		var dbE outbox.Entry
		var donePtr *time.Time
		err := p.QueryRow(context.Background(), `
			SELECT id, group_id, seq, kind, attempts, done_at
			FROM outbox WHERE group_id = $1
		`, group).Scan(&dbE.ID, &dbE.GroupID, &dbE.Seq, &dbE.Kind, &dbE.Attempts, &donePtr)
		if errors.Is(err, pgx.ErrNoRows) {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if dbE.Attempts >= 1 {
			e = dbE
			e.DoneAt = donePtr
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if e.Attempts < 1 {
		t.Fatalf("expected attempts >= 1, got %d", e.Attempts)
	}
	if e.DoneAt != nil {
		t.Fatalf("expected not done after failure, got done_at=%v", e.DoneAt)
	}

	cancel()
	<-done
}
