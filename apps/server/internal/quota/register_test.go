package quota_test

import (
	"testing"

	"privasync/server/internal/bus"
	"privasync/server/internal/bus/bustest"
	"privasync/server/internal/quota"
)

// The quota service declares its own bookkeeping contract: every counter-moving
// domain event is a critical subscriber (in-TX), none reactive. This pins the
// self-registration so a new File* event can't silently skip quota accounting.
func TestServiceRegister_DeclaresCriticalBookkeepingEvents(t *testing.T) {
	spy := &bustest.Spy{}
	quota.New(nil, nil, nil, "").Register(spy)

	want := []string{
		bus.EventFileUploaded,
		bus.EventFileConflictKept,
		bus.EventFileConflictDiscarded,
		bus.EventFileTrashed,
		bus.EventFileRestored,
		bus.EventSyncRootDeleted,
		bus.EventFilePurgedByCron,
		bus.EventAdminQuotaAdjusted,
	}
	assertSameSet(t, "critical", spy.Critical, want)
	if len(spy.Reactive) != 0 {
		t.Fatalf("quota declares no reactive subscribers, got %v", spy.Reactive)
	}
}

func assertSameSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: want %d events %v, got %d %v", label, len(want), want, len(got), got)
	}
	seen := map[string]int{}
	for _, n := range got {
		seen[n]++
	}
	for _, n := range want {
		if seen[n] == 0 {
			t.Fatalf("%s: missing %q; got %v", label, n, got)
		}
		seen[n]--
	}
}
