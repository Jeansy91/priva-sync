package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"privasync/server/internal/audit"
	"privasync/server/internal/auth"
	"privasync/server/internal/config"
	"privasync/server/internal/model"
)

// newTOTPUser returns a handler plus a user that has completed TOTP setup,
// ready to be driven through Verify2FA.
func newTOTPUser(t *testing.T) (*AuthHandler, config.AuthConfig, *model.User) {
	t.Helper()
	env := newTestEnv(t)
	cfg := authCfg()
	h := NewAuthHandler(env.users, audit.NewRepository(env.pool), env.storage, nil, cfg,
		"http://localhost", "PrivaSync")
	claims := createTestUser(t, env)
	confirmTOTPFor(t, h, env, claims)

	u, err := env.users.FindByID(context.Background(), claims.UserID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	return h, cfg, u
}

// verify2FA posts one code against a fresh session token for the given user.
func verify2FA(t *testing.T, h *AuthHandler, cfg config.AuthConfig, u *model.User, field, code string) *httptest.ResponseRecorder {
	t.Helper()
	sessionToken, err := auth.Issue2FASessionToken(cfg.JWTSecret, cfg.TwoFASessionTTL, u.ID)
	if err != nil {
		t.Fatalf("Issue2FASessionToken: %v", err)
	}
	body := bytes.NewBufferString(`{"session_token":"` + sessionToken + `","` + field + `":"` + code + `"}`)
	r := httptest.NewRequest(http.MethodPost, "/auth/2fa", body)
	w := httptest.NewRecorder()
	h.Verify2FA(w, r)
	return w
}

func currentTOTP(t *testing.T, u *model.User) string {
	t.Helper()
	code, err := totp.GenerateCode(*u.TOTPSecret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	return code
}

// A caller holding the password gets a 2FA session token and could otherwise
// grind six-digit codes at will. With skew 1 three codes are valid at any
// moment, so unmetered guessing turns the second factor into a formality
// (#181). Failed codes must therefore feed the same lockout counter the
// password path uses.
func TestVerify2FA_WrongCodesTripTheLockout(t *testing.T) {
	h, cfg, u := newTOTPUser(t)

	for i := 0; i < cfg.MaxLoginAttempts; i++ {
		w := verify2FA(t, h, cfg, u, "totp_code", "000000")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401; body=%s", i+1, w.Code, w.Body.String())
		}
	}

	// Counter is full: even the correct code must now be refused.
	w := verify2FA(t, h, cfg, u, "totp_code", currentTOTP(t, u))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 — 2FA guessing is unmetered; body=%s", w.Code, w.Body.String())
	}
}

// Wrong backup codes are guessing too and belong in the same counter.
func TestVerify2FA_WrongBackupCodeIsCounted(t *testing.T) {
	h, cfg, u := newTOTPUser(t)

	if w := verify2FA(t, h, cfg, u, "backup_code", "dead-beef-dead-beef"); w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}

	count, err := h.users.CountFailedAttempts(context.Background(), u.Username, cfg.LoginLockoutWindow)
	if err != nil {
		t.Fatalf("CountFailedAttempts: %v", err)
	}
	if count != 1 {
		t.Fatalf("failed attempts = %d, want 1 — wrong backup code was not counted", count)
	}
}

// A few typos must not lock anyone out: the correct code still works while
// the counter is below the limit.
func TestVerify2FA_CorrectCodeStillWorksAfterAFewMisses(t *testing.T) {
	h, cfg, u := newTOTPUser(t)

	for i := 0; i < 2; i++ {
		verify2FA(t, h, cfg, u, "totp_code", "000000")
	}

	if w := verify2FA(t, h, cfg, u, "totp_code", currentTOTP(t, u)); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a couple of typos must not lock the user out; body=%s",
			w.Code, w.Body.String())
	}
}
