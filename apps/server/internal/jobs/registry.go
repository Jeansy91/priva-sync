// Package jobs hosts the admin-triggered background job registry. Each job
// has a stable name, a human label, and a Run function. The registry executes
// runs, persists their outcome in job_runs, and exposes a List for the admin
// UI ("when did sweep X last run?").
package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RunFunc executes the job and returns the count of processed items. The
// registry persists this value as the "items" column so the admin UI can show
// "purged 4 files" / "removed 2 orphan sidecars".
type RunFunc func(ctx context.Context) (int, error)

// Status is the read model for one job: definition plus latest run snapshot.
type Status struct {
	Name                string
	Label               string
	Status              string // running | success | error | "" (never ran)
	Message             string
	Items               int
	StartedAt           *time.Time
	FinishedAt          *time.Time
	Running             bool
	ScheduleDescription string     // i18n code (e.g. "daily_midnight_utc"); empty when manual-only
	NextRunAt           *time.Time // nil when the job is manual-only
}

// ErrUnknownJob is returned when RunOnce is called with an unregistered name.
var ErrUnknownJob = errors.New("unknown job")

// ErrAlreadyRunning is returned when a second RunOnce is issued while the
// same job is still executing — HTTP layer maps this to 409.
var ErrAlreadyRunning = errors.New("job already running")

type job struct {
	name  string
	label string
	run   RunFunc
}

type schedule struct {
	description string
	nextRun     time.Time
}

type Registry struct {
	pool *pgxpool.Pool

	mu        sync.Mutex
	jobs      map[string]*job
	running   map[string]bool
	schedules map[string]schedule
}

func NewRegistry(pool *pgxpool.Pool) *Registry {
	return &Registry{
		pool:      pool,
		jobs:      map[string]*job{},
		running:   map[string]bool{},
		schedules: map[string]schedule{},
	}
}

// SetSchedule attaches a cadence description and next-run hint to a job. The
// scheduler loop calls this on every tick so the UI always shows the upcoming
// execution. Manual-only jobs simply skip this call.
func (r *Registry) SetSchedule(name, description string, nextRun time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.schedules[name] = schedule{description: description, nextRun: nextRun}
}

// Has reports whether a job with this name has been registered.
func (r *Registry) Has(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.jobs[name]
	return ok
}

// IsRunning reports whether a run for this name is currently in-flight.
func (r *Registry) IsRunning(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running[name]
}

// Register adds a job. Re-registering the same name overwrites the prior
// definition; intentional so test setups can replace jobs.
func (r *Registry) Register(name, label string, fn RunFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs[name] = &job{name: name, label: label, run: fn}
}

// RunOnce executes the named job synchronously and persists the outcome.
func (r *Registry) RunOnce(ctx context.Context, name string) (int, error) {
	r.mu.Lock()
	j, ok := r.jobs[name]
	if !ok {
		r.mu.Unlock()
		return 0, ErrUnknownJob
	}
	if r.running[name] {
		r.mu.Unlock()
		return 0, ErrAlreadyRunning
	}
	r.running[name] = true
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(r.running, name)
		r.mu.Unlock()
	}()

	started := time.Now().UTC()
	if err := r.writeStart(ctx, j.name, started); err != nil {
		return 0, fmt.Errorf("persist start: %w", err)
	}
	items, runErr := j.run(ctx)
	finished := time.Now().UTC()
	status := "success"
	msg := ""
	if runErr != nil {
		status = "error"
		msg = runErr.Error()
	}
	if err := r.writeFinish(ctx, j.name, started, finished, status, msg, items); err != nil {
		return items, fmt.Errorf("persist finish: %w", err)
	}
	return items, runErr
}

// List returns one Status per registered job, merged with the latest row
// from job_runs. Jobs that have never run still appear (Status == "").
func (r *Registry) List(ctx context.Context) ([]Status, error) {
	r.mu.Lock()
	defs := make([]*job, 0, len(r.jobs))
	for _, j := range r.jobs {
		defs = append(defs, j)
	}
	runningSnap := make(map[string]bool, len(r.running))
	for k, v := range r.running {
		runningSnap[k] = v
	}
	schedSnap := make(map[string]schedule, len(r.schedules))
	for k, v := range r.schedules {
		schedSnap[k] = v
	}
	r.mu.Unlock()

	rows, err := r.pool.Query(ctx,
		`SELECT name, started_at, finished_at, status, COALESCE(message, ''), items
		   FROM job_runs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	latest := map[string]Status{}
	for rows.Next() {
		var s Status
		var started time.Time
		var finished *time.Time
		if err := rows.Scan(&s.Name, &started, &finished, &s.Status, &s.Message, &s.Items); err != nil {
			return nil, err
		}
		s.StartedAt = &started
		s.FinishedAt = finished
		latest[s.Name] = s
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]Status, 0, len(defs))
	for _, j := range defs {
		s := latest[j.name]
		s.Name = j.name
		s.Label = j.label
		s.Running = runningSnap[j.name]
		if sch, ok := schedSnap[j.name]; ok {
			s.ScheduleDescription = sch.description
			next := sch.nextRun
			s.NextRunAt = &next
		}
		out = append(out, s)
	}
	return out, nil
}

func (r *Registry) writeStart(ctx context.Context, name string, started time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO job_runs (name, started_at, finished_at, status, message, items)
		VALUES ($1, $2, NULL, 'running', NULL, 0)
		ON CONFLICT (name) DO UPDATE
		   SET started_at  = EXCLUDED.started_at,
		       finished_at = NULL,
		       status      = 'running',
		       message     = NULL,
		       items       = 0
	`, name, started)
	return err
}

func (r *Registry) writeFinish(ctx context.Context, name string, started, finished time.Time, status, msg string, items int) error {
	var msgArg any = msg
	if msg == "" {
		msgArg = nil
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE job_runs
		   SET started_at  = $2,
		       finished_at = $3,
		       status      = $4,
		       message     = $5,
		       items       = $6
		 WHERE name = $1
	`, name, started, finished, status, msgArg, items)
	return err
}

