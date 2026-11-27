package jobs_test

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

	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/db"
	"privasync/server/internal/jobs"
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

func cleanupJob(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(c, `DELETE FROM job_runs WHERE name = $1`, name)
	})
}

// Tracer bullet: a registered job runs through RunOnce and the registry
// persists started_at, finished_at, status=success, and the reported item
// count so the admin UI can surface "last run" information.
func TestRegistry_RunOnce_PersistsSuccess(t *testing.T) {
	pool := getPool(t)
	name := "jobs_test_tracer"
	cleanupJob(t, pool, name)

	reg := jobs.NewRegistry(pool)
	reg.Register(name, "Tracer Job", func(ctx context.Context) (int, error) {
		return 7, nil
	})

	before := time.Now()
	items, err := reg.RunOnce(context.Background(), name)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if items != 7 {
		t.Fatalf("items = %d, want 7", items)
	}

	statuses, err := reg.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var got *jobs.Status
	for i := range statuses {
		if statuses[i].Name == name {
			got = &statuses[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("job %q missing from List", name)
	}
	if got.Status != "success" {
		t.Errorf("status = %q, want success", got.Status)
	}
	if got.Items != 7 {
		t.Errorf("items = %d, want 7", got.Items)
	}
	if got.StartedAt == nil || got.StartedAt.Before(before.Add(-time.Second)) {
		t.Errorf("started_at = %v, want >= %v", got.StartedAt, before)
	}
	if got.FinishedAt == nil || got.FinishedAt.Before(*got.StartedAt) {
		t.Errorf("finished_at = %v, want >= started_at %v", got.FinishedAt, got.StartedAt)
	}
}

// Jobs may carry a schedule description and a next-run hint so the admin UI
// can show "next run at 2026-05-19 00:00 UTC" without re-deriving the cadence.
func TestRegistry_ScheduleExposedThroughList(t *testing.T) {
	pool := getPool(t)
	name := "jobs_test_sched"
	cleanupJob(t, pool, name)

	reg := jobs.NewRegistry(pool)
	reg.Register(name, "Scheduled", func(ctx context.Context) (int, error) { return 0, nil })

	next := time.Date(2099, 1, 2, 0, 0, 0, 0, time.UTC)
	reg.SetSchedule(name, "Täglich um 00:00 UTC", next)

	statuses, err := reg.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var got *jobs.Status
	for i := range statuses {
		if statuses[i].Name == name {
			got = &statuses[i]
		}
	}
	if got == nil {
		t.Fatalf("missing")
	}
	if got.ScheduleDescription != "Täglich um 00:00 UTC" {
		t.Errorf("schedule desc = %q", got.ScheduleDescription)
	}
	if got.NextRunAt == nil || !got.NextRunAt.Equal(next) {
		t.Errorf("next_run_at = %v, want %v", got.NextRunAt, next)
	}

	// Jobs without a schedule (manual-only) expose neither.
	manual := "jobs_test_manual"
	cleanupJob(t, pool, manual)
	reg.Register(manual, "Manual", func(ctx context.Context) (int, error) { return 0, nil })
	statuses, _ = reg.List(context.Background())
	for _, s := range statuses {
		if s.Name == manual {
			if s.ScheduleDescription != "" || s.NextRunAt != nil {
				t.Errorf("manual job leaks schedule: %+v", s)
			}
		}
	}
}

// When a job returns an error, RunOnce surfaces it AND the persisted status
// flips to "error" with the message captured for the admin UI.
func TestRegistry_RunOnce_PersistsErrorWithMessage(t *testing.T) {
	pool := getPool(t)
	name := "jobs_test_err"
	cleanupJob(t, pool, name)

	reg := jobs.NewRegistry(pool)
	boom := errors.New("boom: storage missing")
	reg.Register(name, "Failing Job", func(ctx context.Context) (int, error) {
		return 3, boom
	})

	items, err := reg.RunOnce(context.Background(), name)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if items != 3 {
		t.Fatalf("items = %d, want 3 (partial progress is reported)", items)
	}

	statuses, _ := reg.List(context.Background())
	var got *jobs.Status
	for i := range statuses {
		if statuses[i].Name == name {
			got = &statuses[i]
		}
	}
	if got == nil {
		t.Fatalf("job missing from List")
	}
	if got.Status != "error" {
		t.Errorf("status = %q, want error", got.Status)
	}
	if !strings.Contains(got.Message, "boom") {
		t.Errorf("message = %q, want to contain %q", got.Message, "boom")
	}
}

// RunOnce on an unregistered name fails fast with ErrUnknownJob — the HTTP
// layer maps this to 404 so admins do not silently start nothing.
func TestRegistry_RunOnce_UnknownJobReturnsSentinel(t *testing.T) {
	pool := getPool(t)
	reg := jobs.NewRegistry(pool)
	_, err := reg.RunOnce(context.Background(), "does_not_exist")
	if !errors.Is(err, jobs.ErrUnknownJob) {
		t.Fatalf("err = %v, want ErrUnknownJob", err)
	}
}

// Starting a job that is already running returns ErrAlreadyRunning so the
// HTTP layer can map it to 409 — protects long sweeps from concurrent runs.
func TestRegistry_RunOnce_RejectsConcurrent(t *testing.T) {
	pool := getPool(t)
	name := "jobs_test_concurrent"
	cleanupJob(t, pool, name)

	reg := jobs.NewRegistry(pool)
	release := make(chan struct{})
	entered := make(chan struct{})
	reg.Register(name, "Slow Job", func(ctx context.Context) (int, error) {
		close(entered)
		<-release
		return 0, nil
	})

	done := make(chan error, 1)
	go func() {
		_, err := reg.RunOnce(context.Background(), name)
		done <- err
	}()
	<-entered

	if _, err := reg.RunOnce(context.Background(), name); !errors.Is(err, jobs.ErrAlreadyRunning) {
		t.Fatalf("second RunOnce err = %v, want ErrAlreadyRunning", err)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
}
