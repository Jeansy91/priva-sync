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
)

func newAdminTestEnv(t *testing.T) (*AdminHandler, *AuthHandler, *testEnv) {
	t.Helper()
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	cfg := config.AuthConfig{
		JWTSecret:          "test-secret",
		AccessTokenTTL:     5 * time.Minute,
		RefreshTokenTTL:    1 * time.Hour,
		MaxLoginAttempts:   10,
		LoginLockoutWindow: time.Hour,
	}
	adminH := NewAdminHandler(env.users, audits, nil, nil, cfg, "http://test")
	authH := NewAuthHandler(env.users, audits, env.storage, nil, cfg, "http://localhost", "PrivaSync")
	return adminH, authH, env
}

// Admin creates a user without choosing a password. The server stores an
// invite token instead; the user redeems it to set their own password and
// can then log in. The full loop: create -> redeem -> login.
func TestCreateUser_InviteFlow_SetOwnPasswordAndLogin(t *testing.T) {
	adminH, authH, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)

	// Pin the otherwise random invite token so the test can redeem it.
	inviteToken := "invite_" + uuid.NewString()
	adminH.newInviteToken = func() (string, error) { return inviteToken, nil }

	username := "invitee_" + uniqueSuffix()
	buf, _ := json.Marshal(map[string]any{
		"username": username,
		"email":    username + "@test.local",
		"role":     "user",
	})
	r := reqWithClaims(http.MethodPost, "/api/v1/admin/users", bytes.NewReader(buf), admin)
	w := httptest.NewRecorder()
	adminH.CreateUser(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	registerUserCleanup(t, env.pool, created.ID)

	// Redeem the invite token to set an own password.
	buf, _ = json.Marshal(map[string]any{
		"token":        inviteToken,
		"new_password": "my-own-password-123",
	})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/auth/password/reset-confirm", bytes.NewReader(buf))
	w = httptest.NewRecorder()
	authH.ConfirmPasswordReset(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("redeem invite: status=%d body=%s", w.Code, w.Body.String())
	}

	// The chosen password now works for login.
	buf, _ = json.Marshal(map[string]any{
		"username": username,
		"password": "my-own-password-123",
	})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(buf))
	w = httptest.NewRecorder()
	authH.Login(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("login: status=%d body=%s", w.Code, w.Body.String())
	}
	var loginResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginResp.AccessToken == "" {
		t.Errorf("login response has no access_token")
	}
}

// Until the invite is redeemed the account has no usable password — any
// login attempt is rejected.
func TestCreateUser_InviteFlow_LoginBeforeRedeemFails(t *testing.T) {
	adminH, authH, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)

	username := "invitee_" + uniqueSuffix()
	buf, _ := json.Marshal(map[string]any{
		"username": username,
		"email":    username + "@test.local",
	})
	r := reqWithClaims(http.MethodPost, "/api/v1/admin/users", bytes.NewReader(buf), admin)
	w := httptest.NewRecorder()
	adminH.CreateUser(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	registerUserCleanup(t, env.pool, created.ID)

	buf, _ = json.Marshal(map[string]any{
		"username": username,
		"password": "any-guess-whatsoever",
	})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(buf))
	w = httptest.NewRecorder()
	authH.Login(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("login before redeem: status=%d, want 401; body=%s", w.Code, w.Body.String())
	}
}

// username and email stay mandatory; a password is no longer accepted as
// input (the field is simply ignored if sent by an old client).
func TestCreateUser_RequiresUsernameAndEmail(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)

	for _, body := range []string{
		`{"email":"x@test.local"}`,
		`{"username":"x_user"}`,
		`{}`,
	} {
		r := reqWithClaims(http.MethodPost, "/api/v1/admin/users", bytes.NewReader([]byte(body)), admin)
		w := httptest.NewRecorder()
		adminH.CreateUser(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %s: status=%d, want 400", body, w.Code)
		}
	}
}
