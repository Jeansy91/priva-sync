package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/bootstrap"
)

// tokenCaptureHandler is a slog.Handler that plucks the "setup_token"
// attribute out of the bootstrap banner so a test can drive Complete()'s
// success path with the real, otherwise-unexported token.
type tokenCaptureHandler struct{ token *string }

func (h tokenCaptureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h tokenCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "setup_token" {
			*h.token = a.Value.String()
			return false
		}
		return true
	})
	return nil
}
func (h tokenCaptureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h tokenCaptureHandler) WithGroup(string) slog.Handler      { return h }

func TestSetupHandler_Status_NoSetupNeeded(t *testing.T) {
	h := NewSetupHandler(&bootstrap.State{}, nil)

	r := httptest.NewRequest("GET", "/setup/status", nil)
	w := httptest.NewRecorder()
	h.Status(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]bool
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["needs_setup"] {
		t.Error("needs_setup = true for a zero-value State, want false")
	}
}

func TestSetupHandler_Complete_AlreadyDone(t *testing.T) {
	h := NewSetupHandler(&bootstrap.State{}, nil)

	body := bytes.NewBufferString(`{"token":"x","username":"a","email":"a@t.local","password":"password123"}`)
	r := httptest.NewRequest("POST", "/setup", body)
	w := httptest.NewRecorder()
	h.Complete(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != ErrSetupAlreadyComplete {
		t.Errorf("code = %v, want %q", resp["code"], ErrSetupAlreadyComplete)
	}
}

// setupHandlerNeedingSetup returns a SetupHandler backed by a live State that
// still needs setup (real token, only known to the process via bootstrap's
// stdout banner). Used to exercise Complete()'s validation branches that
// don't require knowing the token value itself.
func setupHandlerNeedingSetup(t *testing.T) (*SetupHandler, *testEnv) {
	t.Helper()
	env := newTestEnv(t)
	state, err := bootstrap.Init(context.Background(), env.users)
	if err != nil {
		t.Fatalf("bootstrap.Init: %v", err)
	}
	if !state.NeedsSetup() {
		t.Skip("an admin already exists in this test DB -- cannot exercise the needs-setup path")
	}
	return NewSetupHandler(state, env.users), env
}

func TestSetupHandler_Complete_MissingFields(t *testing.T) {
	h, _ := setupHandlerNeedingSetup(t)

	body := bytes.NewBufferString(`{"token":"x","username":"","email":"","password":""}`)
	r := httptest.NewRequest("POST", "/setup", body)
	w := httptest.NewRecorder()
	h.Complete(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestSetupHandler_Complete_ShortPassword(t *testing.T) {
	h, _ := setupHandlerNeedingSetup(t)

	body := bytes.NewBufferString(`{"token":"x","username":"admin","email":"admin@t.local","password":"short"}`)
	r := httptest.NewRequest("POST", "/setup", body)
	w := httptest.NewRecorder()
	h.Complete(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != ErrPasswordTooShort {
		t.Errorf("code = %v, want %q", resp["code"], ErrPasswordTooShort)
	}
}

func TestSetupHandler_Complete_InvalidToken(t *testing.T) {
	h, _ := setupHandlerNeedingSetup(t)

	body := bytes.NewBufferString(`{"token":"definitely-wrong","username":"admin","email":"admin@t.local","password":"password123"}`)
	r := httptest.NewRequest("POST", "/setup", body)
	w := httptest.NewRecorder()
	h.Complete(w, r)

	if w.Code != 401 {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != ErrInvalidSetupToken {
		t.Errorf("code = %v, want %q", resp["code"], ErrInvalidSetupToken)
	}
}

func TestSetupHandler_Complete_CreatesAdminWithValidToken(t *testing.T) {
	env := newTestEnv(t)

	// Capture the setup token from the bootstrap banner. bootstrap.Init logs
	// it via slog; swap the default logger just for that call.
	var token string
	prev := slog.Default()
	slog.SetDefault(slog.New(tokenCaptureHandler{token: &token}))
	state, err := bootstrap.Init(context.Background(), env.users)
	slog.SetDefault(prev)
	if err != nil {
		t.Fatalf("bootstrap.Init: %v", err)
	}
	if !state.NeedsSetup() {
		t.Skip("an admin already exists in this test DB -- cannot exercise the success path")
	}
	if token == "" {
		t.Fatal("setup token was not captured from the bootstrap banner")
	}

	h := NewSetupHandler(state, env.users)
	suffix := uuid.NewString()[:8]
	body := bytes.NewBufferString(`{"token":"` + token + `","username":"admin_` + suffix +
		`","email":"admin_` + suffix + `@t.local","password":"password123"}`)
	r := httptest.NewRequest("POST", "/setup", body)
	w := httptest.NewRecorder()
	h.Complete(w, r)

	if w.Code != 201 {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Delete the created admin at test end, otherwise HasAdmin() stays true and
	// every future setup test skips against this shared DB.
	if id, perr := uuid.Parse(resp["id"].(string)); perr == nil {
		registerUserCleanup(t, env.pool, id)
	} else {
		t.Fatalf("response id not a uuid: %v", resp["id"])
	}

	if state.NeedsSetup() {
		t.Error("state still needs setup after a successful Complete")
	}
	// The one-shot token is consumed: a second attempt is now 404.
	body2 := bytes.NewBufferString(`{"token":"` + token + `","username":"x","email":"x@t.local","password":"password123"}`)
	r2 := httptest.NewRequest("POST", "/setup", body2)
	w2 := httptest.NewRecorder()
	h.Complete(w2, r2)
	if w2.Code != 404 {
		t.Errorf("second Complete status = %d, want 404", w2.Code)
	}
}
