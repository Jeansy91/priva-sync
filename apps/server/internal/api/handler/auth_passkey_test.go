package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/audit"
	"privasync/server/internal/config"
	"privasync/server/internal/repository"
)

func newPasskeyTestEnv(t *testing.T) (*AuthHandler, *repository.PasskeyRepository, *testEnv) {
	t.Helper()
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	cfg := config.AuthConfig{
		JWTSecret:           "test-secret",
		AccessTokenTTL:      5 * time.Minute,
		RefreshTokenTTL:     time.Hour,
		PasskeyChallengeTTL: 2 * time.Minute,
		MaxLoginAttempts:    10,
		LoginLockoutWindow:  time.Hour,
	}
	passkeys := repository.NewPasskeyRepository(env.pool)
	authH := NewAuthHandler(env.users, audits, env.storage, nil, cfg, "http://localhost", "PrivaSync").WithPasskeys(passkeys)
	return authH, passkeys, env
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("decode body: %v (body=%s)", err, w.Body.String())
	}
}

func TestRegisterPasskeyBegin_ReturnsChallengeAndOptions(t *testing.T) {
	authH, _, env := newPasskeyTestEnv(t)
	claims := createTestUser(t, env)

	r := reqWithClaims(http.MethodPost, "/api/v1/auth/passkey/register/begin", nil, claims)
	w := httptest.NewRecorder()
	authH.RegisterPasskeyBegin(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Options        json.RawMessage `json:"options"`
		ChallengeToken string          `json:"challenge_token"`
	}
	decodeBody(t, w, &resp)
	if resp.ChallengeToken == "" {
		t.Fatal("expected non-empty challenge_token")
	}
	if len(resp.Options) == 0 {
		t.Fatal("expected non-empty options")
	}
}

func TestRegisterPasskeyFinish_InvalidChallengeToken(t *testing.T) {
	authH, _, env := newPasskeyTestEnv(t)
	claims := createTestUser(t, env)

	body, _ := json.Marshal(map[string]any{
		"challenge_token": "not-a-real-token",
		"credential":      json.RawMessage(`{}`),
	})
	r := reqWithClaims(http.MethodPost, "/api/v1/auth/passkey/register/finish", bytes.NewReader(body), claims)
	w := httptest.NewRecorder()
	authH.RegisterPasskeyFinish(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRegisterPasskeyFinish_ChallengeForDifferentUser(t *testing.T) {
	authH, _, env := newPasskeyTestEnv(t)
	claimsA := createTestUser(t, env)
	claimsB := createTestUser(t, env)

	beginReq := reqWithClaims(http.MethodPost, "/api/v1/auth/passkey/register/begin", nil, claimsA)
	beginW := httptest.NewRecorder()
	authH.RegisterPasskeyBegin(beginW, beginReq)
	var begun struct {
		ChallengeToken string `json:"challenge_token"`
	}
	decodeBody(t, beginW, &begun)

	body, _ := json.Marshal(map[string]any{
		"challenge_token": begun.ChallengeToken,
		"credential":      json.RawMessage(`{}`),
	})
	finishReq := reqWithClaims(http.MethodPost, "/api/v1/auth/passkey/register/finish", bytes.NewReader(body), claimsB)
	finishW := httptest.NewRecorder()
	authH.RegisterPasskeyFinish(finishW, finishReq)

	if finishW.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", finishW.Code, finishW.Body.String())
	}
}

func TestRegisterPasskeyFinish_MalformedCredential(t *testing.T) {
	authH, _, env := newPasskeyTestEnv(t)
	claims := createTestUser(t, env)

	beginReq := reqWithClaims(http.MethodPost, "/api/v1/auth/passkey/register/begin", nil, claims)
	beginW := httptest.NewRecorder()
	authH.RegisterPasskeyBegin(beginW, beginReq)
	var begun struct {
		ChallengeToken string `json:"challenge_token"`
	}
	decodeBody(t, beginW, &begun)

	body, _ := json.Marshal(map[string]any{
		"challenge_token": begun.ChallengeToken,
		"credential":      json.RawMessage(`{"garbage":true}`),
	})
	finishReq := reqWithClaims(http.MethodPost, "/api/v1/auth/passkey/register/finish", bytes.NewReader(body), claims)
	finishW := httptest.NewRecorder()
	authH.RegisterPasskeyFinish(finishW, finishReq)

	if finishW.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", finishW.Code, finishW.Body.String())
	}
}

func TestListPasskeys_EmptyInitially(t *testing.T) {
	authH, _, env := newPasskeyTestEnv(t)
	claims := createTestUser(t, env)

	r := reqWithClaims(http.MethodGet, "/api/v1/auth/passkey", nil, claims)
	w := httptest.NewRecorder()
	authH.ListPasskeys(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var rows []map[string]any
	decodeBody(t, w, &rows)
	if len(rows) != 0 {
		t.Fatalf("expected no passkeys, got %d", len(rows))
	}
}

func TestDeletePasskey_NotFound(t *testing.T) {
	authH, _, env := newPasskeyTestEnv(t)
	claims := createTestUser(t, env)

	id := uuid.NewString()
	r := reqWithClaims(http.MethodDelete, "/api/v1/auth/passkey/"+id, nil, claims)
	r = withURLParams(r, "passkeyID", id)
	w := httptest.NewRecorder()
	authH.DeletePasskey(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDeletePasskey_ScopedToOwner(t *testing.T) {
	authH, passkeys, env := newPasskeyTestEnv(t)
	owner := createTestUser(t, env)
	other := createTestUser(t, env)

	stored, err := passkeys.Create(t.Context(), owner.UserID, []byte(uuid.NewString()), []byte(`{}`), "Test Passkey")
	if err != nil {
		t.Fatalf("seed passkey: %v", err)
	}

	// Another user must not be able to delete it.
	r := reqWithClaims(http.MethodDelete, "/api/v1/auth/passkey/"+stored.ID.String(), nil, other)
	r = withURLParams(r, "passkeyID", stored.ID.String())
	w := httptest.NewRecorder()
	authH.DeletePasskey(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-owner delete: status=%d body=%s", w.Code, w.Body.String())
	}

	// The owner can.
	r2 := reqWithClaims(http.MethodDelete, "/api/v1/auth/passkey/"+stored.ID.String(), nil, owner)
	r2 = withURLParams(r2, "passkeyID", stored.ID.String())
	w2 := httptest.NewRecorder()
	authH.DeletePasskey(w2, r2)
	if w2.Code != http.StatusNoContent {
		t.Fatalf("owner delete: status=%d body=%s", w2.Code, w2.Body.String())
	}
}

func TestPasskeyLoginBegin_ReturnsChallengeAndOptions(t *testing.T) {
	authH, _, _ := newPasskeyTestEnv(t)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/login/begin", nil)
	w := httptest.NewRecorder()
	authH.PasskeyLoginBegin(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Options        json.RawMessage `json:"options"`
		ChallengeToken string          `json:"challenge_token"`
	}
	decodeBody(t, w, &resp)
	if resp.ChallengeToken == "" {
		t.Fatal("expected non-empty challenge_token")
	}
	if len(resp.Options) == 0 {
		t.Fatal("expected non-empty options")
	}
}

func TestPasskeyLoginFinish_InvalidChallengeToken(t *testing.T) {
	authH, _, _ := newPasskeyTestEnv(t)

	body, _ := json.Marshal(map[string]any{
		"challenge_token": "not-a-real-token",
		"credential":      json.RawMessage(`{}`),
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/login/finish", bytes.NewReader(body))
	w := httptest.NewRecorder()
	authH.PasskeyLoginFinish(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestPasskeyLoginFinish_MalformedCredential(t *testing.T) {
	authH, _, _ := newPasskeyTestEnv(t)

	beginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/login/begin", nil)
	beginW := httptest.NewRecorder()
	authH.PasskeyLoginBegin(beginW, beginReq)
	var begun struct {
		ChallengeToken string `json:"challenge_token"`
	}
	decodeBody(t, beginW, &begun)

	body, _ := json.Marshal(map[string]any{
		"challenge_token": begun.ChallengeToken,
		"credential":      json.RawMessage(`{"garbage":true}`),
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/login/finish", bytes.NewReader(body))
	w := httptest.NewRecorder()
	authH.PasskeyLoginFinish(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
