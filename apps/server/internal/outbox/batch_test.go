package outbox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"privasync/server/internal/outbox"
)

// renamePayload/ssePayload/mailPayload are minimal Payload implementations so
// this test can exercise Batch ordering without a real outbox row shape —
// only Kind() matters here.
type renamePayload struct{}

func (renamePayload) Kind() outbox.Kind { return outbox.KindStorageRename }

type ssePayload struct{}

func (ssePayload) Kind() outbox.Kind { return outbox.KindSSETyped }

type mailPayload struct{}

func (mailPayload) Kind() outbox.Kind { return outbox.KindMailQuotaWarning }

type recordedInsert struct {
	groupID uuid.UUID
	seq     int
	kind    outbox.Kind
}

// fakeInserter satisfies outbox.Inserter without touching a database — Batch
// only needs to know it was called with the right (group, seq) in order.
type fakeInserter struct {
	calls []recordedInsert
	err   error
}

func (f *fakeInserter) Insert(_ context.Context, _ pgx.Tx, groupID uuid.UUID, seq int, p outbox.Payload) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, recordedInsert{groupID: groupID, seq: seq, kind: p.Kind()})
	return nil
}

func TestBatch_AddNumbersSeqMonotonicallyInCallOrder(t *testing.T) {
	fake := &fakeInserter{}
	group := uuid.New()
	batch := outbox.NewBatch(context.Background(), nil, fake, group)

	if err := batch.Add(renamePayload{}); err != nil {
		t.Fatalf("add rename: %v", err)
	}
	if err := batch.Add(ssePayload{}); err != nil {
		t.Fatalf("add sse: %v", err)
	}
	if err := batch.Add(mailPayload{}); err != nil {
		t.Fatalf("add mail: %v", err)
	}

	want := []recordedInsert{
		{groupID: group, seq: 1, kind: outbox.KindStorageRename},
		{groupID: group, seq: 2, kind: outbox.KindSSETyped},
		{groupID: group, seq: 3, kind: outbox.KindMailQuotaWarning},
	}
	if len(fake.calls) != len(want) {
		t.Fatalf("got %d calls, want %d: %v", len(fake.calls), len(want), fake.calls)
	}
	for i, w := range want {
		if fake.calls[i] != w {
			t.Fatalf("call %d: got %+v, want %+v", i, fake.calls[i], w)
		}
	}
}

func TestBatch_SeparateBatchesRestartSeqPerGroup(t *testing.T) {
	fake := &fakeInserter{}
	g1, g2 := uuid.New(), uuid.New()
	b1 := outbox.NewBatch(context.Background(), nil, fake, g1)
	b2 := outbox.NewBatch(context.Background(), nil, fake, g2)

	if err := b1.Add(renamePayload{}); err != nil {
		t.Fatalf("b1 add: %v", err)
	}
	if err := b2.Add(renamePayload{}); err != nil {
		t.Fatalf("b2 add: %v", err)
	}
	if err := b1.Add(ssePayload{}); err != nil {
		t.Fatalf("b1 add 2: %v", err)
	}

	want := []recordedInsert{
		{groupID: g1, seq: 1, kind: outbox.KindStorageRename},
		{groupID: g2, seq: 1, kind: outbox.KindStorageRename},
		{groupID: g1, seq: 2, kind: outbox.KindSSETyped},
	}
	if len(fake.calls) != len(want) {
		t.Fatalf("got %d calls, want %d: %v", len(fake.calls), len(want), fake.calls)
	}
	for i, w := range want {
		if fake.calls[i] != w {
			t.Fatalf("call %d: got %+v, want %+v", i, fake.calls[i], w)
		}
	}
}

func TestBatch_AddPropagatesInsertError(t *testing.T) {
	boom := errors.New("boom")
	fake := &fakeInserter{err: boom}
	batch := outbox.NewBatch(context.Background(), nil, fake, uuid.New())

	if err := batch.Add(renamePayload{}); !errors.Is(err, boom) {
		t.Fatalf("got %v, want %v", err, boom)
	}
}
