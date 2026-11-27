package outbox

import (
	"context"
	"encoding/json"
	"fmt"
)

// Payload is a self-describing outbox row body: Kind() ties the Go type to the
// wire Kind it serializes under. Because the type carries its own Kind, the
// registry binds Kind ↔ type ↔ handler from the type alone — a producer that
// drifts the payload shape breaks at compile time, not at retry time.
//
// Implement Kind() on a *value* receiver: Register reads the kind from a zero
// value of the type (Insert reads it from the passed instance), so a value
// receiver keeps Kind() valid for a bare struct. A pointer-receiver Kind()
// would nil-panic in Register — the design is value-type payloads throughout.
type Payload interface {
	Kind() Kind
}

// Registry maps each Kind to its type-erased Handler. Build one with Register
// so every entry decodes into the exact payload type its handler expects.
type Registry map[Kind]Handler

// Register binds a typed handler under the Kind reported by its payload type.
// The generated Handler unmarshals the raw row into T once, centrally, so no
// handler hand-rolls its own decode. The Kind is read from the zero value of
// T, keeping the wire tag and the Go type in lockstep — this requires T to
// carry Kind() on a value receiver (see Payload).
func Register[T Payload](reg Registry, fn func(context.Context, T) error) {
	var zero T
	reg[zero.Kind()] = func(ctx context.Context, raw json.RawMessage) error {
		var p T
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		return fn(ctx, p)
	}
}

// Require asserts that every kind has a registered handler, returning an error
// naming the missing ones. Wire it at startup over AllKinds so an unregistered
// kind is a boot failure rather than a row that backs off forever.
func (reg Registry) Require(kinds ...Kind) error {
	var missing []Kind
	for _, k := range kinds {
		if _, ok := reg[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("outbox: no handler registered for kinds %v", missing)
	}
	return nil
}
