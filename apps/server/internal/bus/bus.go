// Package bus implements the in-process Domain Event Bus.
//
// Two delivery modes:
//   - Critical subscribers run synchronously in the publisher's transaction.
//     A handler error short-circuits the dispatch and rolls back the TX.
//   - Reactive subscribers run asynchronously on a worker pool after commit.
//     Handler errors / panics are logged and never affect the publisher.
//
// The bus is in-process only; durable side-effects belong in the Outbox.
package bus

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/jackc/pgx/v5"
)

// Event is the contract for everything published on the bus. EventName is the
// dispatch key — keep it stable, dotted, lowercase (e.g. "file.uploaded").
type Event interface {
	EventName() string
}

// CriticalHandler runs inside the publisher's TX. Returning an error aborts
// dispatch and is bubbled to the publisher so it can roll back.
type CriticalHandler func(ctx context.Context, tx pgx.Tx, evt Event) error

// ReactiveHandler runs post-commit, off the request goroutine. Errors and
// panics are swallowed (logged) — reactive subscribers must not break the
// publisher's operation.
type ReactiveHandler func(ctx context.Context, evt Event)

// Typed adapts a type-safe handler over a concrete event T into a
// CriticalHandler. Every subscriber used to repeat the same
// `e, ok := evt.(bus.X); if !ok { return fmt.Errorf(...) }` boilerplate at
// the top of each On* method (quota.Service alone had it eight times); this
// does the assertion once so handlers stay typed all the way through. Go
// doesn't allow a method to carry its own type parameter (only the receiver
// type can be generic), so this is a free function rather than a method.
func Typed[T Event](handler func(ctx context.Context, tx pgx.Tx, e T) error) CriticalHandler {
	return func(ctx context.Context, tx pgx.Tx, evt Event) error {
		e, ok := evt.(T)
		if !ok {
			return fmt.Errorf("bus: unexpected event %T, want %T", evt, e)
		}
		return handler(ctx, tx, e)
	}
}

// Bus is the public surface. Implementations must be safe for concurrent use.
type Bus interface {
	SubscribeCritical(name string, h CriticalHandler)
	SubscribeReactive(name string, h ReactiveHandler)
	PublishCritical(ctx context.Context, tx pgx.Tx, evt Event) error
	PublishReactive(ctx context.Context, evt Event)
	Close()
}

// Config tunes the reactive worker pool.
type Config struct {
	// ReactiveWorkers is the goroutine count draining reactive events.
	// Defaults to 4 when <= 0.
	ReactiveWorkers int
	// ReactiveQueue is the buffered channel size. Defaults to 256 when <= 0.
	ReactiveQueue int
}

type reactiveJob struct {
	ctx context.Context
	h   ReactiveHandler
	evt Event
}

type inProcessBus struct {
	mu       sync.RWMutex
	critical map[string][]CriticalHandler
	reactive map[string][]ReactiveHandler

	jobs   chan reactiveJob
	wg     sync.WaitGroup
	closed chan struct{}
}

// New returns an in-process Bus ready for SubscribeXxx calls. Close it on
// shutdown to drain in-flight reactive jobs.
func New(cfg Config) Bus {
	if cfg.ReactiveWorkers <= 0 {
		cfg.ReactiveWorkers = 4
	}
	if cfg.ReactiveQueue <= 0 {
		cfg.ReactiveQueue = 256
	}
	b := &inProcessBus{
		critical: map[string][]CriticalHandler{},
		reactive: map[string][]ReactiveHandler{},
		jobs:     make(chan reactiveJob, cfg.ReactiveQueue),
		closed:   make(chan struct{}),
	}
	for i := 0; i < cfg.ReactiveWorkers; i++ {
		b.wg.Add(1)
		go b.worker()
	}
	return b
}

func (b *inProcessBus) SubscribeCritical(name string, h CriticalHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.critical[name] = append(b.critical[name], h)
}

func (b *inProcessBus) SubscribeReactive(name string, h ReactiveHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reactive[name] = append(b.reactive[name], h)
}

func (b *inProcessBus) PublishCritical(ctx context.Context, tx pgx.Tx, evt Event) error {
	b.mu.RLock()
	handlers := append([]CriticalHandler(nil), b.critical[evt.EventName()]...)
	b.mu.RUnlock()
	for i, h := range handlers {
		if err := h(ctx, tx, evt); err != nil {
			return fmt.Errorf("critical subscriber %d for %q: %w", i, evt.EventName(), err)
		}
	}
	return nil
}

func (b *inProcessBus) PublishReactive(ctx context.Context, evt Event) {
	b.mu.RLock()
	handlers := append([]ReactiveHandler(nil), b.reactive[evt.EventName()]...)
	b.mu.RUnlock()
	for _, h := range handlers {
		select {
		case <-b.closed:
			return
		case b.jobs <- reactiveJob{ctx: ctx, h: h, evt: evt}:
		}
	}
}

func (b *inProcessBus) Close() {
	select {
	case <-b.closed:
		return
	default:
	}
	close(b.closed)
	close(b.jobs)
	b.wg.Wait()
}

func (b *inProcessBus) worker() {
	defer b.wg.Done()
	for job := range b.jobs {
		runReactive(job)
	}
}

func runReactive(job reactiveJob) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("bus: reactive subscriber panicked",
				"event", job.evt.EventName(), "panic", r)
		}
	}()
	job.h(job.ctx, job.evt)
}
