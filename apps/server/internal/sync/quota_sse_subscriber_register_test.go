package sync_test

import (
	"testing"

	"privasync/server/internal/bus"
	"privasync/server/internal/bus/bustest"
	syncpkg "privasync/server/internal/sync"
)

// The SSE subscriber declares the threshold event as critical so its typed
// banner row commits atomically with the band transition that triggered it.
func TestQuotaSSESubscriberRegister_DeclaresCriticalThresholdEvent(t *testing.T) {
	spy := &bustest.Spy{}
	syncpkg.NewQuotaSSESubscriber(nil).Register(spy)

	if len(spy.Critical) != 1 || spy.Critical[0] != bus.EventQuotaThresholdCrossed {
		t.Fatalf("want single critical %q, got %v", bus.EventQuotaThresholdCrossed, spy.Critical)
	}
	if len(spy.Reactive) != 0 {
		t.Fatalf("SSE subscriber declares no reactive subscribers, got %v", spy.Reactive)
	}
}
