package sse_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/httpx/httpxtest"
	"privasync/server/internal/sse"
	"privasync/server/internal/sse/events"
)

// flushRecorder implements http.ResponseWriter + http.Flusher for SSE tests.
type flushRecorder struct {
	mu     sync.Mutex
	header http.Header
	buf    bytes.Buffer
	chunks chan []byte
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{header: http.Header{}, chunks: make(chan []byte, 64)}
}

func (f *flushRecorder) Header() http.Header { return f.header }
func (f *flushRecorder) WriteHeader(int)     {}
func (f *flushRecorder) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buf.Write(p)
	return len(p), nil
}
func (f *flushRecorder) Flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.buf.Len() == 0 {
		return
	}
	b := make([]byte, f.buf.Len())
	copy(b, f.buf.Bytes())
	f.buf.Reset()
	select {
	case f.chunks <- b:
	default:
	}
}

// subscribe spawns Subscribe in a goroutine and returns a chunks channel + cleanup.
func subscribe(t *testing.T, hub *sse.Hub, userID uuid.UUID) (<-chan []byte, func()) {
	t.Helper()
	fr := newFlushRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/events", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		hub.Subscribe(fr, req, userID)
		close(done)
	}()
	select {
	case <-fr.chunks: // initial ": connected"
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("no initial keep-alive")
	}
	return fr.chunks, func() {
		cancel()
		<-done
	}
}

// waitChunk waits for the next non-keepalive chunk.
func waitChunk(t *testing.T, ch <-chan []byte, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case b := <-ch:
			s := string(b)
			if strings.HasPrefix(s, ": ") {
				continue
			}
			return b
		case <-deadline:
			t.Fatal("no event chunk")
			return nil
		}
	}
}

// TestPublishTypedFileUploadedEmitsEnvelope verifies that hub.Publish accepts
// a typed events.Payload directly and emits the SSE wire format with the
// versioned envelope (no untyped sse.Event literal).
func TestPublishTypedFileUploadedEmitsEnvelope(t *testing.T) {
	hub := sse.NewHub()
	uid := uuid.New()
	chunks, cleanup := subscribe(t, hub, uid)
	defer cleanup()

	rootID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	fileID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	payload := events.FileUploaded{
		RootID: rootID,
		Path:   "/x.md",
		File:   events.File{ID: fileID, Path: "/x.md", Size: 1, ServerVersion: 1},
	}

	// New API: Publish accepts events.Payload, hub builds envelope internally.
	hub.Publish(uid, payload)

	chunk := waitChunk(t, chunks, 2*time.Second)
	gotType, gotData := parseSSE(t, chunk)
	if gotType != "file_uploaded" {
		t.Fatalf("event type = %q, want file_uploaded", gotType)
	}
	var env struct {
		Type    string          `json:"type"`
		Version int             `json:"version"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(gotData, &env); err != nil {
		t.Fatalf("envelope unmarshal: %v", err)
	}
	if env.Type != "file_uploaded" {
		t.Errorf("envelope.type = %q", env.Type)
	}
	if env.Version != events.EnvelopeVersion {
		t.Errorf("envelope.version = %d, want %d", env.Version, events.EnvelopeVersion)
	}
	if !bytes.Contains(env.Data, []byte(`"root_id":"`+rootID.String()+`"`)) {
		t.Errorf("envelope.data missing root_id: %s", env.Data)
	}
}

// TestRawEventBytesIdenticalToTypedPublish verifies that a NewRawEvent
// pass-through (used by the outbox typed-SSE worker) produces byte-identical
// wire output to a direct typed Publish. This guards the no-re-marshal
// invariant that lets outbox-replayed events look exactly like direct ones.
func TestRawEventBytesIdenticalToTypedPublish(t *testing.T) {
	hub := sse.NewHub()
	uid := uuid.New()
	chunks, cleanup := subscribe(t, hub, uid)
	defer cleanup()

	payload := events.FileUploaded{
		RootID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Path:   "/x.md",
		File:   events.File{ID: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Path: "/x.md"},
	}

	hub.Publish(uid, payload)
	directChunk := waitChunk(t, chunks, 2*time.Second)

	envBytes, err := json.Marshal(events.NewEnvelope(payload))
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	hub.Publish(uid, sse.NewRawEvent(payload.EventName(), envBytes))
	rawChunk := waitChunk(t, chunks, 2*time.Second)

	if !bytes.Equal(directChunk, rawChunk) {
		t.Errorf("wire drift between direct typed and raw-envelope publish:\ndirect:\n%s\nraw:\n%s", directChunk, rawChunk)
	}
}

func parseSSE(t *testing.T, chunk []byte) (string, []byte) {
	t.Helper()
	var typ, data string
	for _, line := range strings.Split(string(chunk), "\n") {
		switch {
		case strings.HasPrefix(line, "event: "):
			typ = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
	return typ, []byte(data)
}

// readLive reads whatever the stream has sent next, with a bound so a dead
// connection fails the test instead of hanging it.
func readLive(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	res := make(chan result, 1)
	go func() {
		// SSE frames end in a blank line — skip those, the payload line is
		// what the caller asserts on.
		for {
			line, err := r.ReadString('\n')
			if err != nil || strings.TrimSpace(line) != "" {
				res <- result{line, err}
				return
			}
		}
	}()
	select {
	case got := <-res:
		if got.err != nil {
			t.Fatalf("stream read: %v", got.err)
		}
		return got.line
	case <-time.After(3 * time.Second):
		t.Fatal("stream produced nothing — connection dead")
		return ""
	}
}

// A stream is one request that deliberately never ends, so it must outlive
// the server's timeouts the way production wires them (ADR-0072).
func TestStreamOutlivesServerWriteTimeout(t *testing.T) {
	const writeTimeout = 300 * time.Millisecond

	hub := sse.NewHub()
	uid := uuid.New()

	srv := httpxtest.Server(t, writeTimeout, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.Subscribe(w, r, uid)
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer res.Body.Close()

	reader := bufio.NewReader(res.Body)
	if got := readLive(t, reader); !strings.HasPrefix(got, ": connected") {
		t.Fatalf("first chunk = %q, want the connect keep-alive", got)
	}

	// Past the deadline the connection would otherwise have been torn down.
	time.Sleep(2 * writeTimeout)

	hub.Publish(uid, events.FileUploaded{
		RootID: uuid.New(),
		Path:   "/late.md",
		File:   events.File{ID: uuid.New(), Path: "/late.md", Size: 1, ServerVersion: 1},
	})

	if got := readLive(t, reader); !strings.HasPrefix(got, "event: file_uploaded") {
		t.Fatalf("chunk after the write timeout = %q, want the published event", got)
	}
}
