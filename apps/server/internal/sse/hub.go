// Package sse provides a lightweight Server-Sent Events hub for per-user push notifications.
package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/sse/events"
)

// keepAliveInterval bestimmt wie oft ein SSE-Kommentar als Keep-Alive
// rausgeht. Zweck: NAT/Proxy/Browser-Idle-Timeouts triggern keinen
// stillen Connection-Drop, der die Client-Resync-Logik verzoegert.
// 25s liegt unter den ueblichen 30-60s-Timeouts (nginx default 60s,
// Cloudflare ~100s, OS-NAT-Idle ~30s).
const keepAliveInterval = 25 * time.Second

// Event is the typed sum-type of every payload the hub can emit. Every
// concrete events.* struct satisfies it via EventName(); untyped publishes
// are a compile-time error.
type Event = events.Payload

type client struct {
	ch     chan Event
	userID uuid.UUID
}

// Hub manages SSE client subscriptions.
type Hub struct {
	mu      sync.RWMutex
	clients map[uuid.UUID]map[*client]struct{}
}

func NewHub() *Hub {
	return &Hub{clients: make(map[uuid.UUID]map[*client]struct{})}
}

// Subscribe registers the response writer as an SSE stream for userID.
// It blocks until the client disconnects or r.Context() is cancelled.
func (h *Hub) Subscribe(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx passthrough

	c := &client{ch: make(chan Event, 16), userID: userID}
	h.add(c)
	defer h.remove(c)

	// Send a keep-alive comment immediately so the browser knows the stream is alive.
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-c.ch:
			data, err := marshalEnvelope(ev)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.EventName(), data)
			flusher.Flush()
		case <-ticker.C:
			// SSE-Kommentar (Zeile mit ':' Prefix) — wird vom EventSource
			// ignoriert, haelt aber TCP-Verbindung + Proxies wach.
			_, _ = fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// Publish sends a typed event to all active connections for userID.
func (h *Hub) Publish(userID uuid.UUID, ev Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients[userID] {
		select {
		case c.ch <- ev:
		default: // slow client — drop rather than block
		}
	}
}

func (h *Hub) add(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[c.userID] == nil {
		h.clients[c.userID] = make(map[*client]struct{})
	}
	h.clients[c.userID][c] = struct{}{}
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients[c.userID], c)
	if len(h.clients[c.userID]) == 0 {
		delete(h.clients, c.userID)
	}
}

// marshalEnvelope produces the versioned wire-format bytes for ev. Kept as a
// free function so callers outside the hub (e.g. outbox-replay) can produce
// byte-identical output via the same path. rawEnvelope short-circuits to its
// pre-marshalled bytes so field order cannot drift between direct-hub and
// relayed paths.
func marshalEnvelope(ev Event) ([]byte, error) {
	if r, ok := ev.(rawEnvelope); ok {
		return r.bytes, nil
	}
	return json.Marshal(events.NewEnvelope[events.Payload](ev))
}

// rawEnvelope wraps already-marshalled envelope bytes so the outbox-replay path
// can publish without re-marshaling. Internal — constructed via NewRawEvent.
type rawEnvelope struct {
	name  string
	bytes []byte
}

// EventName implements events.Payload.
func (r rawEnvelope) EventName() string { return r.name }

// NewRawEvent wraps the given envelope bytes (full {type,version,data} JSON)
// for direct emission through Hub.Publish. The outbox typed-SSE worker uses
// this to keep wire bytes identical to the original publish.
func NewRawEvent(eventName string, envelopeBytes []byte) Event {
	return rawEnvelope{name: eventName, bytes: envelopeBytes}
}

// MarshalEnvelopeForTest exposes the internal envelope marshaller for
// cross-package tests that want to assert on the exact wire bytes. Not part
// of the runtime API — tests only.
func MarshalEnvelopeForTest(ev Event) ([]byte, error) { return marshalEnvelope(ev) }
