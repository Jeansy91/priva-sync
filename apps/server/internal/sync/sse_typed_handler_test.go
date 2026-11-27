package sync_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/sse"
	"privasync/server/internal/sse/events"
	syncpkg "privasync/server/internal/sync"
)

// capturingPublisher records every Publish call so the typed-SSE handler
// can be exercised without spinning up an HTTP subscriber.
type capturingPublisher struct {
	calls []capturedPublish
}

type capturedPublish struct {
	userID uuid.UUID
	event  sse.Event
}

func (p *capturingPublisher) Publish(userID uuid.UUID, ev sse.Event) {
	p.calls = append(p.calls, capturedPublish{userID, ev})
}

// envelopeBytes asks the hub how it would serialise ev so the test sees
// exactly the bytes that would land on the wire.
func envelopeBytes(t *testing.T, ev sse.Event) []byte {
	t.Helper()
	b, err := sse.MarshalEnvelopeForTest(ev)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return b
}

func TestTypedSSEHandler_DispatchesEnvelope(t *testing.T) {
	userID := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	rootID := uuid.New()
	fileID := uuid.New()
	updatedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)

	payload := syncpkg.NewTypedSSEPayload(userID, events.FileUploaded{
		RootID: rootID,
		Path:   "/x.txt",
		File: events.File{
			ID:          fileID,
			Path:        "/x.txt",
			Size:        42,
			ContentHash: "ff",
			UpdatedAt:   updatedAt,
		},
	})
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	pub := &capturingPublisher{}
	handler := syncpkg.TypedSSEHandler(pub)

	if err := handler(context.Background(), raw); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.userID != userID {
		t.Errorf("user_id = %s, want %s", call.userID, userID)
	}
	if call.event.EventName() != "file_uploaded" {
		t.Errorf("event.name = %q, want file_uploaded", call.event.EventName())
	}

	// The hub serialises the carried event into the full envelope so consumers
	// receive {type,version,data}.
	envBytes := envelopeBytes(t, call.event)
	var env struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
		Data    struct {
			Path string `json:"path"`
			File struct {
				Size int64 `json:"size"`
			} `json:"file"`
		} `json:"data"`
	}
	if err := json.Unmarshal(envBytes, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.Type != "file_uploaded" || env.Version != 2 {
		t.Errorf("envelope header = %+v", env)
	}
	if env.Data.Path != "/x.txt" || env.Data.File.Size != 42 {
		t.Errorf("envelope data = %+v", env.Data)
	}
}

func TestTypedSSEHandler_NilPublisherIsNoop(t *testing.T) {
	handler := syncpkg.TypedSSEHandler(nil)
	if err := handler(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Errorf("nil publisher should be tolerated, got %v", err)
	}
}
