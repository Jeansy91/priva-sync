package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/audit"
	"privasync/server/internal/config"
)

func newAuthTestEnv(t *testing.T) (*AuthHandler, *testEnv) {
	t.Helper()
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, config.AuthConfig{}, "http://localhost", "PrivaSync")
	return h, env
}

func TestSetUsername_Success(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	body := bytes.NewBufferString(`{"username":"new_name_abc"}`)
	r := reqWithClaims("PUT", "/auth/profile/username", body, claims)
	w := httptest.NewRecorder()
	h.SetUsername(w, r)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	u, err := env.users.FindByID(context.Background(), claims.UserID)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	if u.Username != "new_name_abc" {
		t.Errorf("username = %q, want %q", u.Username, "new_name_abc")
	}
}

func TestSetUsername_Conflict(t *testing.T) {
	h, env := newAuthTestEnv(t)
	c1 := createTestUser(t, env)
	c2 := createTestUser(t, env)
	other, err := env.users.FindByID(context.Background(), c2.UserID)
	if err != nil {
		t.Fatalf("find other: %v", err)
	}

	body := bytes.NewBufferString(`{"username":"` + other.Username + `"}`)
	r := reqWithClaims("PUT", "/auth/profile/username", body, c1)
	w := httptest.NewRecorder()
	h.SetUsername(w, r)

	if w.Code != 409 {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != ErrUsernameTaken {
		t.Errorf("code = %v, want %q", resp["code"], ErrUsernameTaken)
	}
}

// ─── Email-Change Request ────────────────────────────────────────────────────

func TestRequestEmailChange_Success(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	body := bytes.NewBufferString(`{"new_email":"new+addr@test.local"}`)
	r := reqWithClaims("POST", "/auth/profile/email-change", body, claims)
	w := httptest.NewRecorder()
	h.RequestEmailChange(w, r)

	if w.Code != 202 {
		t.Fatalf("status = %d, want 202; body=%s", w.Code, w.Body.String())
	}
	// The user's email must NOT have changed yet.
	u, _ := env.users.FindByID(context.Background(), claims.UserID)
	if strings.Contains(u.Email, "new+addr") {
		t.Errorf("email already changed before confirm: %s", u.Email)
	}
	// Pending Token muss persistiert sein.
	n, err := env.users.CountEmailChangeTokens(context.Background(), claims.UserID)
	if err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if n != 1 {
		t.Errorf("token count = %d, want 1", n)
	}
}

func TestRequestEmailChange_Conflict(t *testing.T) {
	h, env := newAuthTestEnv(t)
	c1 := createTestUser(t, env)
	c2 := createTestUser(t, env)
	other, _ := env.users.FindByID(context.Background(), c2.UserID)

	body := bytes.NewBufferString(`{"new_email":"` + other.Email + `"}`)
	r := reqWithClaims("POST", "/auth/profile/email-change", body, c1)
	w := httptest.NewRecorder()
	h.RequestEmailChange(w, r)

	if w.Code != 409 {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
}

func TestRequestEmailChange_InvalidFormat(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	body := bytes.NewBufferString(`{"new_email":"not-an-email"}`)
	r := reqWithClaims("POST", "/auth/profile/email-change", body, claims)
	w := httptest.NewRecorder()
	h.RequestEmailChange(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

// ─── Email-Change Confirm ────────────────────────────────────────────────────

func TestConfirmEmailChange_Success(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	// Token direkt im Repo erzeugen — testet den Confirm-Pfad unabhaengig
	// vom Request-Endpoint.
	newAddr := "fresh+confirm@test.local"
	rawToken := "raw-token-" + uuid.NewString()
	if err := env.users.CreateEmailChangeToken(
		context.Background(), claims.UserID, newAddr, rawToken, time.Now().Add(time.Hour),
	); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	body := bytes.NewBufferString(`{"token":"` + rawToken + `"}`)
	r := reqWithClaims("POST", "/auth/profile/email-change/confirm", body, claims)
	w := httptest.NewRecorder()
	h.ConfirmEmailChange(w, r)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	u, _ := env.users.FindByID(context.Background(), claims.UserID)
	if u.Email != newAddr {
		t.Errorf("email = %q, want %q", u.Email, newAddr)
	}
	// Token muss verbraucht sein.
	n, _ := env.users.CountEmailChangeTokens(context.Background(), claims.UserID)
	if n != 0 {
		t.Errorf("token count after confirm = %d, want 0", n)
	}
}

func TestConfirmEmailChange_ReplayFails(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)
	rawToken := "raw-token-" + uuid.NewString()
	_ = env.users.CreateEmailChangeToken(
		context.Background(), claims.UserID, "replay@test.local", rawToken, time.Now().Add(time.Hour),
	)

	// First call — success.
	body := bytes.NewBufferString(`{"token":"` + rawToken + `"}`)
	r := reqWithClaims("POST", "/auth/profile/email-change/confirm", body, claims)
	w := httptest.NewRecorder()
	h.ConfirmEmailChange(w, r)
	if w.Code != 204 {
		t.Fatalf("first call status = %d, want 204", w.Code)
	}

	// Replay — must fail with 400.
	body2 := bytes.NewBufferString(`{"token":"` + rawToken + `"}`)
	r2 := reqWithClaims("POST", "/auth/profile/email-change/confirm", body2, claims)
	w2 := httptest.NewRecorder()
	h.ConfirmEmailChange(w2, r2)
	if w2.Code != 400 {
		t.Fatalf("replay status = %d, want 400; body=%s", w2.Code, w2.Body.String())
	}
}

func TestConfirmEmailChange_UnknownToken(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	body := bytes.NewBufferString(`{"token":"does-not-exist"}`)
	r := reqWithClaims("POST", "/auth/profile/email-change/confirm", body, claims)
	w := httptest.NewRecorder()
	h.ConfirmEmailChange(w, r)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestSetUsername_Empty(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	body := bytes.NewBufferString(`{"username":""}`)
	r := reqWithClaims("PUT", "/auth/profile/username", body, claims)
	w := httptest.NewRecorder()
	h.SetUsername(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}
