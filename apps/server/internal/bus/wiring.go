package bus

// Subscriber is a package that owns bus contracts and knows how to declare
// them. Each implementer subscribes its own handlers — critical vs reactive —
// so the wiring intent lives with the domain logic instead of being
// reconstructed in main.go.
type Subscriber interface {
	Register(b Bus)
}

// WireSubscribers hands the bus to every subscriber so each can declare its
// own contracts. This is the single wiring call main.go makes in place of the
// scattered SubscribeCritical lines.
func WireSubscribers(b Bus, subs ...Subscriber) {
	for _, s := range subs {
		s.Register(b)
	}
}
