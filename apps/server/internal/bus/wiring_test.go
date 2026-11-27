package bus_test

import (
	"testing"

	"privasync/server/internal/bus"
)

// recordingSubscriber notes each Register call and the bus it was handed, so
// the test can assert WireSubscribers fans out to every subscriber exactly once.
type recordingSubscriber struct {
	calls int
	got   bus.Bus
}

func (r *recordingSubscriber) Register(b bus.Bus) {
	r.calls++
	r.got = b
}

func TestWireSubscribers_RegistersEverySubscriberOnce(t *testing.T) {
	b := bus.New(bus.Config{ReactiveWorkers: 1})
	defer b.Close()

	a, c := &recordingSubscriber{}, &recordingSubscriber{}
	bus.WireSubscribers(b, a, c)

	if a.calls != 1 || c.calls != 1 {
		t.Fatalf("want each subscriber registered once, got a=%d c=%d", a.calls, c.calls)
	}
	if a.got != b || c.got != b {
		t.Fatal("subscribers were handed a bus other than the one passed to WireSubscribers")
	}
}
