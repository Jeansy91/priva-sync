package sync

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"privasync/server/internal/outbox"
	"privasync/server/internal/sse"
	"privasync/server/internal/sse/events"
)

// ─── EventPublisher seam (ADR-0023) ──────────────────────────────────────────
//
// Two ways an SSE event reaches a client: durable (outbox row, commits with
// the caller's TX, crash-recoverable — coordinator choreographies) or
// ephemeral (straight to the hub, best-effort, never persisted — handler-side
// notifications that don't need replay). PublishDurable and PublishEphemeral
// are the only places either path should be touched: no handler calls
// hub.Publish directly, no coordinator/subscriber inserts a typed-SSE outbox
// row by hand. Go methods can't carry their own type parameters, so this is
// two package functions rather than a literal `interface { ... }` — the
// semantics the issue asked for (two named, narrow entry points) are the
// same either way.

// PublishDurable enqueues ev as the next row in batch — durable, commit-
// bound, crash-recoverable. The only way a coordinator/subscriber choreography
// should reach the SSE outbox.
func PublishDurable[T events.Payload](batch *outbox.Batch, userID uuid.UUID, ev T) error {
	return batch.Add(NewTypedSSEPayload(userID, ev))
}

// PublishEphemeral pushes ev straight to the hub — best-effort, delivered
// only to clients connected right now, never persisted. The only way a
// handler should reach the SSE hub. hub may be nil (tests/reduced configs
// without SSE) — the call is then a no-op.
func PublishEphemeral(hub *sse.Hub, userID uuid.UUID, ev sse.Event) {
	if hub == nil {
		return
	}
	hub.Publish(userID, ev)
}

// SSEPublisher is the minimal slice of sse.Hub needed by the typed SSE
// handler. Kept as an interface so tests can capture publishes without
// spinning up an HTTP subscriber.
type SSEPublisher interface {
	Publish(userID uuid.UUID, ev sse.Event)
}

// TypedSSEHandler returns an outbox.Handler that decodes a TypedSSEPayload
// and pushes the envelope verbatim to the SSE hub. The envelope JSON is
// stored as-is (json.RawMessage) and wrapped in sse.NewRawEvent so the
// consumer-facing wire bytes match the original publish — no re-marshal drift.
//
// A nil publisher is tolerated so tests and reduced configurations can drop
// SSE without crashing the worker.
func TypedSSEHandler(pub SSEPublisher) outbox.Handler {
	return func(_ context.Context, raw json.RawMessage) error {
		if pub == nil {
			return nil
		}
		var p TypedSSEPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		pub.Publish(p.UserID, sse.NewRawEvent(p.Type, p.Envelope))
		return nil
	}
}

// TypedSSEPayload is the outbox row body for typed SSE events. Type is
// duplicated at the row level so the worker can route without parsing the
// envelope, while Envelope carries the complete wire-format JSON consumed by
// web and desktop. New typed events join via NewTypedSSEPayload — the untyped
// SSEPayload path stays for events not yet migrated (file_uploaded
// only).
type TypedSSEPayload struct {
	UserID   uuid.UUID       `json:"user_id"`
	Type     string          `json:"type"`
	Envelope json.RawMessage `json:"envelope"`
}

func (TypedSSEPayload) Kind() outbox.Kind { return outbox.KindSSETyped }

// NewTypedSSEPayload builds the outbox payload from a typed event. The
// envelope is marshalled once here so a malformed payload fails fast at the
// publish site, not in the worker.
func NewTypedSSEPayload[T events.Payload](userID uuid.UUID, p T) TypedSSEPayload {
	env := events.NewEnvelope(p)
	raw, err := json.Marshal(env)
	if err != nil {
		// Marshalling a struct with JSON-tagged primitives cannot fail; panic
		// rather than hide a type-level bug behind a returned error.
		panic("sync: marshal SSE envelope: " + err.Error())
	}
	return TypedSSEPayload{
		UserID:   userID,
		Type:     env.Type,
		Envelope: raw,
	}
}
