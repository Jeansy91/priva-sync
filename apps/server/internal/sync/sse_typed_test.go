package sync_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"privasync/server/internal/outbox"
	"privasync/server/internal/sse/events"
	syncpkg "privasync/server/internal/sync"
)

// fakeInserter satisfies outbox.Inserter without a database — only used to
// verify PublishDurable routes through the batch (ordering itself is
// outbox.Batch's own responsibility, covered in outbox/batch_test.go).
type fakeInserter struct {
	calls []outbox.Kind
	err   error
}

func (f *fakeInserter) Insert(_ context.Context, _ pgx.Tx, _ uuid.UUID, _ int, p outbox.Payload) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, p.Kind())
	return nil
}

// PublishDurable must insert exactly one KindSSETyped row into the batch —
// the seam every coordinator choreography's SSE event now goes through
// (ADR-0023).
func TestPublishDurable_InsertsOneTypedSSERowIntoBatch(t *testing.T) {
	fake := &fakeInserter{}
	batch := outbox.NewBatch(context.Background(), nil, fake, uuid.New())

	if err := syncpkg.PublishDurable(batch, uuid.New(), events.QuotaUpdated{}); err != nil {
		t.Fatalf("PublishDurable: %v", err)
	}

	if len(fake.calls) != 1 || fake.calls[0] != outbox.KindSSETyped {
		t.Fatalf("calls = %+v, want exactly one KindSSETyped insert", fake.calls)
	}
}

// A failing Inserter must propagate through PublishDurable unchanged — the
// caller's `if err := PublishDurable(...); err != nil { return err }` chain
// depends on this.
func TestPublishDurable_PropagatesInsertError(t *testing.T) {
	boom := errors.New("boom")
	fake := &fakeInserter{err: boom}
	batch := outbox.NewBatch(context.Background(), nil, fake, uuid.New())

	if err := syncpkg.PublishDurable(batch, uuid.New(), events.QuotaUpdated{}); !errors.Is(err, boom) {
		t.Fatalf("got %v, want %v", err, boom)
	}
}

// A nil hub (tests/reduced configs without SSE) must be a silent no-op —
// callers don't guard every handler-side publish with a nil check.
func TestPublishEphemeral_NilHubIsNoop(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PublishEphemeral panicked on nil hub: %v", r)
		}
	}()
	syncpkg.PublishEphemeral(nil, uuid.New(), events.QuotaUpdated{})
}

func TestNewTypedSSEPayload_FileUploaded(t *testing.T) {
	userID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	rootID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	fileID := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	updatedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)

	p := syncpkg.NewTypedSSEPayload(userID, events.FileUploaded{
		RootID: rootID,
		Path:   "/notes.md",
		File: events.File{
			ID:          fileID,
			Path:        "/notes.md",
			Size:        128,
			ContentHash: "abcd",
			UpdatedAt:   updatedAt,
		},
	})

	if p.UserID != userID {
		t.Errorf("user_id mismatch")
	}
	if p.Type != "file_uploaded" {
		t.Errorf("type = %q, want file_uploaded", p.Type)
	}

	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	var got struct {
		UserID   string          `json:"user_id"`
		Type     string          `json:"type"`
		Envelope json.RawMessage `json:"envelope"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got.UserID != userID.String() {
		t.Errorf("wire user_id = %q", got.UserID)
	}
	if got.Type != "file_uploaded" {
		t.Errorf("wire type = %q", got.Type)
	}

	var env struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
		Data    struct {
			RootID string `json:"root_id"`
			Path   string `json:"path"`
			File   struct {
				ID   string `json:"id"`
				Size int64  `json:"size"`
			} `json:"file"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got.Envelope, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.Type != "file_uploaded" || env.Version != 2 {
		t.Errorf("envelope = %+v", env)
	}
	if env.Data.Path != "/notes.md" || env.Data.File.Size != 128 {
		t.Errorf("envelope data = %+v", env.Data)
	}
	if env.Data.RootID != rootID.String() || env.Data.File.ID != fileID.String() {
		t.Errorf("envelope ids = %+v", env.Data)
	}
}
