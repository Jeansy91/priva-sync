package mail_test

import (
	"testing"

	"privasync/server/internal/bus"
	"privasync/server/internal/bus/bustest"
	"privasync/server/internal/mail"
)

// The mail subscriber declares both notification-worthy events as critical, so
// the mail row lands in the publisher's TX — mail and domain commit atomically.
func TestSubscriberRegister_DeclaresCriticalMailEvents(t *testing.T) {
	spy := &bustest.Spy{}
	mail.NewSubscriber(nil, nil).Register(spy)

	want := map[string]bool{
		bus.EventQuotaThresholdCrossed: true,
		bus.EventConflictCreated:       true,
	}
	if len(spy.Critical) != len(want) {
		t.Fatalf("want %d critical events, got %v", len(want), spy.Critical)
	}
	for _, n := range spy.Critical {
		if !want[n] {
			t.Fatalf("unexpected critical event %q; got %v", n, spy.Critical)
		}
	}
	if len(spy.Reactive) != 0 {
		t.Fatalf("mail declares no reactive subscribers, got %v", spy.Reactive)
	}
}
