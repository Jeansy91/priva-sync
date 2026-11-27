package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Handler executes one Kind of side-effect. Returning nil marks the entry
// done; returning an error schedules a retry with backoff. Handlers must be
// idempotent — the worker may re-run an entry after crash recovery.
type Handler func(ctx context.Context, payload json.RawMessage) error

// Worker drains the outbox: a fallback poll runs every PollInterval and the
// Same-TX-Hook (call Wake from inside a coordinator commit) bumps it
// immediately so happy-path latency is bounded by Run, not by Poll.
type Worker struct {
	repo         *Repository
	handlers     map[Kind]Handler
	PollInterval time.Duration
	Backoff      func(attempts int) time.Duration

	wake chan struct{}
	mu   sync.Mutex
}

// NewWorker builds a worker. handlers maps every Kind that may appear in the
// outbox to its executor; missing kinds are logged and back off forever.
func NewWorker(repo *Repository, handlers map[Kind]Handler) *Worker {
	return &Worker{
		repo:         repo,
		handlers:     handlers,
		PollInterval: 5 * time.Second,
		Backoff:      defaultBackoff,
		wake:         make(chan struct{}, 1),
	}
}

// Wake nudges the worker to drain immediately (Same-TX-Hook). Non-blocking:
// if a wake is already queued, the new signal is dropped. The pending wake
// will pick up everything that has accumulated.
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Run blocks until ctx is cancelled, draining the outbox on every wake or
// poll tick.
func (w *Worker) Run(ctx context.Context) {
	tick := time.NewTicker(w.PollInterval)
	defer tick.Stop()
	for {
		w.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		case <-tick.C:
		}
	}
}

// drain runs entries until ClaimNext reports nothing pending.
func (w *Worker) drain(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		e, err := w.repo.ClaimNext(ctx)
		if err == ErrNotFound {
			return
		}
		if err != nil {
			slog.Warn("outbox: claim failed", "error", err)
			return
		}
		w.execute(ctx, e)
	}
}

func (w *Worker) execute(ctx context.Context, e *Entry) {
	h, ok := w.handlers[e.Kind]
	if !ok {
		err := fmt.Errorf("no handler for kind %q", e.Kind)
		_ = w.repo.MarkRetry(ctx, e.ID, err.Error(), w.Backoff(e.Attempts))
		slog.Warn("outbox: missing handler", "kind", e.Kind, "id", e.ID)
		return
	}
	if err := h(ctx, e.Payload); err != nil {
		_ = w.repo.MarkRetry(ctx, e.ID, err.Error(), w.Backoff(e.Attempts))
		slog.Warn("outbox: handler failed", "kind", e.Kind, "id", e.ID, "attempts", e.Attempts+1, "error", err)
		return
	}
	if err := w.repo.MarkDone(ctx, e.ID); err != nil {
		slog.Warn("outbox: mark done failed", "id", e.ID, "error", err)
	}
}

// defaultBackoff: exponential with cap, jitter-free for predictability in
// tests. 1s, 2s, 4s, 8s, ... up to 5min.
func defaultBackoff(attempts int) time.Duration {
	d := time.Second << attempts
	if d > 5*time.Minute || d <= 0 {
		return 5 * time.Minute
	}
	return d
}
