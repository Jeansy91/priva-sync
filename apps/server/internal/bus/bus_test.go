package bus_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"privasync/server/internal/bus"
)

type fakeEvent struct{ name string }

func (f fakeEvent) EventName() string { return f.name }

type otherFakeEvent struct{}

func (otherFakeEvent) EventName() string { return "other" }

// Typed must unwrap the concrete event and hand it to the typed handler —
// no manual assertion left at the call site.
func TestTyped_CallsHandlerWithConcreteEvent(t *testing.T) {
	var got fakeEvent
	h := bus.Typed(func(_ context.Context, _ pgx.Tx, e fakeEvent) error {
		got = e
		return nil
	})
	if err := h(context.Background(), nil, fakeEvent{"file.uploaded"}); err != nil {
		t.Fatalf("Typed handler: %v", err)
	}
	if got.name != "file.uploaded" {
		t.Errorf("handler saw %+v, want fakeEvent{file.uploaded}", got)
	}
}

// A mismatched concrete type must fail closed with a descriptive error
// instead of panicking or silently no-op'ing — same contract every hand-
// written `e, ok := evt.(T)` assertion had before Typed replaced them.
func TestTyped_RejectsMismatchedEventType(t *testing.T) {
	called := false
	h := bus.Typed(func(_ context.Context, _ pgx.Tx, _ fakeEvent) error {
		called = true
		return nil
	})
	err := h(context.Background(), nil, otherFakeEvent{})
	if err == nil {
		t.Fatal("expected an error for a mismatched event type")
	}
	if called {
		t.Error("handler must not run when the event type doesn't match")
	}
}

func TestPublishCritical_CallsSubscribersInOrder(t *testing.T) {
	b := bus.New(bus.Config{ReactiveWorkers: 1})
	defer b.Close()

	var got []string
	b.SubscribeCritical("file.uploaded", func(_ context.Context, _ pgx.Tx, e bus.Event) error {
		got = append(got, "a:"+e.EventName())
		return nil
	})
	b.SubscribeCritical("file.uploaded", func(_ context.Context, _ pgx.Tx, e bus.Event) error {
		got = append(got, "b:"+e.EventName())
		return nil
	})

	if err := b.PublishCritical(context.Background(), nil, fakeEvent{"file.uploaded"}); err != nil {
		t.Fatalf("PublishCritical: %v", err)
	}
	if len(got) != 2 || got[0] != "a:file.uploaded" || got[1] != "b:file.uploaded" {
		t.Fatalf("unexpected order: %v", got)
	}
}

func TestPublishCritical_PropagatesError(t *testing.T) {
	b := bus.New(bus.Config{ReactiveWorkers: 1})
	defer b.Close()

	boom := errors.New("boom")
	called := 0
	b.SubscribeCritical("evt", func(_ context.Context, _ pgx.Tx, _ bus.Event) error {
		called++
		return boom
	})
	b.SubscribeCritical("evt", func(_ context.Context, _ pgx.Tx, _ bus.Event) error {
		called++
		return nil
	})

	err := b.PublishCritical(context.Background(), nil, fakeEvent{"evt"})
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom, got %v", err)
	}
	if called != 1 {
		t.Fatalf("expected short-circuit after first failure, called=%d", called)
	}
}

func TestPublishReactive_RunsAsync_SwallowsErrors(t *testing.T) {
	b := bus.New(bus.Config{ReactiveWorkers: 2})
	defer b.Close()

	var ran atomic.Int32
	var wg sync.WaitGroup
	wg.Add(2)

	b.SubscribeReactive("r", func(_ context.Context, _ bus.Event) {
		defer wg.Done()
		ran.Add(1)
	})
	b.SubscribeReactive("r", func(_ context.Context, _ bus.Event) {
		defer wg.Done()
		panic("subscriber panic must not crash bus")
	})

	b.PublishReactive(context.Background(), fakeEvent{"r"})

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reactive subscribers did not run")
	}
	if ran.Load() != 1 {
		t.Fatalf("expected 1 successful reactive, got %d", ran.Load())
	}
}

func TestPublishCritical_NoSubscribers_NoError(t *testing.T) {
	b := bus.New(bus.Config{ReactiveWorkers: 1})
	defer b.Close()
	if err := b.PublishCritical(context.Background(), nil, fakeEvent{"none"}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}
