package outbox_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/outbox"
)

// fakePayload is a test-only Payload that proves the type-routing mechanism
// without depending on any real Kind.
type fakePayload struct {
	Msg string `json:"msg"`
}

func (fakePayload) Kind() outbox.Kind { return outbox.Kind("test_fake") }

// Register binds a typed handler under the payload's own Kind, and the built
// Handler unmarshals the raw row into that type before dispatch.
func TestRegistry_Register_RoutesRawToTypedHandler(t *testing.T) {
	reg := outbox.Registry{}
	var got fakePayload
	outbox.Register(reg, func(_ context.Context, p fakePayload) error {
		got = p
		return nil
	})

	h, ok := reg[outbox.Kind("test_fake")]
	if !ok {
		t.Fatal("Register did not bind a handler under the payload Kind")
	}
	if err := h(context.Background(), json.RawMessage(`{"msg":"hello"}`)); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if got.Msg != "hello" {
		t.Fatalf("typed handler got Msg=%q, want %q", got.Msg, "hello")
	}
}

// Require turns a missing handler into a boot-time error instead of a
// retry-time surprise: it reports exactly the kinds that lack a registration.
func TestRegistry_Require_ReportsMissingKinds(t *testing.T) {
	reg := outbox.Registry{}
	outbox.Register(reg, func(context.Context, fakePayload) error { return nil })

	if err := reg.Require(outbox.Kind("test_fake")); err != nil {
		t.Fatalf("Require rejected a fully-covered kind: %v", err)
	}

	err := reg.Require(outbox.Kind("test_fake"), outbox.Kind("test_missing"))
	if err == nil {
		t.Fatal("Require accepted a registry missing a handler")
	}
	if !strings.Contains(err.Error(), "test_missing") {
		t.Fatalf("Require error %q does not name the missing kind", err)
	}
	if strings.Contains(err.Error(), "test_fake") {
		t.Fatalf("Require error %q names a covered kind", err)
	}
}

// Insert stores the kind the payload reports for itself, so a producer can
// never file a row under a kind that disagrees with its payload.
func TestRepository_Insert_DerivesKindFromPayload(t *testing.T) {
	p := getPool(t)
	repo := outbox.NewRepository(p)
	group := uuid.New()
	ctx := context.Background()

	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if err := repo.Insert(ctx, tx, group, 1, fakePayload{Msg: "x"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var kind string
	if err := p.QueryRow(ctx, `SELECT kind FROM outbox WHERE group_id = $1`, group).Scan(&kind); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if kind != "test_fake" {
		t.Fatalf("stored kind = %q, want the payload's own kind %q", kind, "test_fake")
	}
}

// A row whose payload no longer decodes into the handler's type must surface
// as an error (which the worker turns into a retry), never run the handler on
// a half-populated value. This is the drift the type contract exists to catch.
func TestRegistry_Register_SurfacesDecodeErrorWithoutRunningHandler(t *testing.T) {
	reg := outbox.Registry{}
	ran := false
	outbox.Register(reg, func(context.Context, fakePayload) error {
		ran = true
		return nil
	})

	err := reg[outbox.Kind("test_fake")](context.Background(), json.RawMessage(`{"msg":`))
	if err == nil {
		t.Fatal("malformed payload did not surface an error")
	}
	if ran {
		t.Fatal("handler ran on an undecodable payload")
	}
}
