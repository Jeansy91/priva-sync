package sync_test

import (
	"testing"

	"privasync/server/internal/outbox"
	syncpkg "privasync/server/internal/sync"
)

// BuildHandlers must register a handler for every kind the coordinators emit.
// Require over AllKinds turns an omission into a test failure (and a boot
// failure) instead of a row that backs off forever at retry time. Deps are nil
// because BuildHandlers only builds closures — it does not touch them until a
// row is drained.
func TestBuildHandlers_CoversAllKinds(t *testing.T) {
	reg := syncpkg.BuildHandlers(nil, nil, nil, nil, nil, "")
	if err := reg.Require(outbox.AllKinds...); err != nil {
		t.Fatalf("BuildHandlers registry incomplete: %v", err)
	}
}
