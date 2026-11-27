// Package bustest provides a recording bus.Bus for asserting the subscription
// contract a package declares in its Register method — which events, and at
// what criticality — without running any handler bodies or touching a DB.
package bustest

import (
	"context"

	"github.com/jackc/pgx/v5"

	"privasync/server/internal/bus"
)

// Spy implements bus.Bus by recording the event names passed to
// SubscribeCritical / SubscribeReactive. Publish is a no-op — the point is the
// declared wiring, not dispatch.
type Spy struct {
	Critical []string
	Reactive []string
}

var _ bus.Bus = (*Spy)(nil)

func (s *Spy) SubscribeCritical(name string, _ bus.CriticalHandler) {
	s.Critical = append(s.Critical, name)
}

func (s *Spy) SubscribeReactive(name string, _ bus.ReactiveHandler) {
	s.Reactive = append(s.Reactive, name)
}

func (s *Spy) PublishCritical(context.Context, pgx.Tx, bus.Event) error { return nil }

func (s *Spy) PublishReactive(context.Context, bus.Event) {}

func (s *Spy) Close() {}
