package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"privasync/server/internal/audit"
	"privasync/server/internal/auth"
	"privasync/server/internal/config"
	"privasync/server/internal/mail"
)

// newPasswordResetEnv builds an AuthHandler wired with a real but disabled
// Mailer, so RequestPasswordReset runs the full token+mail path without an
// SMTP server (SendTemplate renders and returns nil).
func newPasswordResetEnv(t *testing.T) (*AuthHandler, *testEnv) {
	t.Helper()
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	mailer, err := mail.New(config.SMTPConfig{Enabled: false})
	if err != nil {
		t.Fatalf("mail.New: %v", err)
	}
	h := NewAuthHandler(env.users, audits, env.storage, mailer, config.AuthConfig{}, "http://localhost", "PrivaSync")
	return h, env
}

func postJSON(h func(http.ResponseWriter, *http.Request), target, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, target, bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestRequestPasswordReset_ActiveUserGetsToken(t *testing.T) {
	h, env := newPasswordResetEnv(t)
	u := seedUser(t, env, "reset_"+uniqueSuffix(), "reset_"+uniqueSuffix()+"@t.local")

	w := postJSON(h.RequestPasswordReset, "/auth/password/reset-request", `{"email":"`+u.Email+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var count int
	if err := env.pool.QueryRow(t.Context(),
		`SELECT count(*) FROM password_reset_tokens WHERE user_id = $1`, u.ID).Scan(&count); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if count != 1 {
		t.Errorf("reset token count = %d, want 1", count)
	}
}

func TestRequestPasswordReset_UnknownEmailStillReturns200(t *testing.T) {
	h, _ := newPasswordResetEnv(t)
	w := postJSON(h.RequestPasswordReset, "/auth/password/reset-request", `{"email":"nobody_`+uniqueSuffix()+`@t.local"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestRequestPasswordReset_InactiveUserStillReturns200(t *testing.T) {
	h, env := newPasswordResetEnv(t)
	u := seedUser(t, env, "inactive_"+uniqueSuffix(), "inactive_"+uniqueSuffix()+"@t.local")
	if err := env.users.SetActive(t.Context(), u.ID, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	w := postJSON(h.RequestPasswordReset, "/auth/password/reset-request", `{"email":"`+u.Email+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestRequestPasswordReset_EmptyBodyReturns200(t *testing.T) {
	h, _ := newPasswordResetEnv(t)
	w := postJSON(h.RequestPasswordReset, "/auth/password/reset-request", `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestConfirmPasswordReset_ChangesPasswordWithValidToken(t *testing.T) {
	h, env := newPasswordResetEnv(t)
	u := seedUser(t, env, "confirm_"+uniqueSuffix(), "confirm_"+uniqueSuffix()+"@t.local")
	token := "reset-token-" + uniqueSuffix()
	if err := env.users.CreatePasswordResetToken(t.Context(), u.ID, token, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create token: %v", err)
	}

	w := postJSON(h.ConfirmPasswordReset, "/auth/password/reset-confirm",
		`{"token":"`+token+`","new_password":"brand-new-secret-1"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}

	updated, err := env.users.FindByID(t.Context(), u.ID)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	if err := auth.VerifyPassword("brand-new-secret-1", updated.PasswordHash); err != nil {
		t.Errorf("new password does not verify: %v", err)
	}
}

func TestConfirmPasswordReset_RejectsUnknownToken(t *testing.T) {
	h, _ := newPasswordResetEnv(t)
	w := postJSON(h.ConfirmPasswordReset, "/auth/password/reset-confirm",
		`{"token":"does-not-exist","new_password":"brand-new-secret-1"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrTokenInvalid)
}

func TestConfirmPasswordReset_RejectsExpiredToken(t *testing.T) {
	h, env := newPasswordResetEnv(t)
	u := seedUser(t, env, "expired_"+uniqueSuffix(), "expired_"+uniqueSuffix()+"@t.local")
	token := "expired-token-" + uniqueSuffix()
	if err := env.users.CreatePasswordResetToken(t.Context(), u.ID, token, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("create token: %v", err)
	}

	w := postJSON(h.ConfirmPasswordReset, "/auth/password/reset-confirm",
		`{"token":"`+token+`","new_password":"brand-new-secret-1"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrTokenInvalid)
}

func TestConfirmPasswordReset_RejectsUsedToken(t *testing.T) {
	h, env := newPasswordResetEnv(t)
	u := seedUser(t, env, "used_"+uniqueSuffix(), "used_"+uniqueSuffix()+"@t.local")
	token := "used-token-" + uniqueSuffix()
	if err := env.users.CreatePasswordResetToken(t.Context(), u.ID, token, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create token: %v", err)
	}
	// First confirm consumes the token.
	if w := postJSON(h.ConfirmPasswordReset, "/auth/password/reset-confirm",
		`{"token":"`+token+`","new_password":"brand-new-secret-1"}`); w.Code != http.StatusNoContent {
		t.Fatalf("first confirm status = %d; body=%s", w.Code, w.Body.String())
	}
	// Second confirm must be rejected — the token is now marked used.
	w := postJSON(h.ConfirmPasswordReset, "/auth/password/reset-confirm",
		`{"token":"`+token+`","new_password":"another-new-secret-2"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrTokenInvalid)
}

func TestConfirmPasswordReset_RejectsShortPassword(t *testing.T) {
	h, _ := newPasswordResetEnv(t)
	w := postJSON(h.ConfirmPasswordReset, "/auth/password/reset-confirm",
		`{"token":"whatever","new_password":"short"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrPasswordTooShortNew)
}

func TestConfirmPasswordReset_RejectsMissingFields(t *testing.T) {
	h, _ := newPasswordResetEnv(t)
	w := postJSON(h.ConfirmPasswordReset, "/auth/password/reset-confirm", `{"token":"","new_password":""}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrPasswordResetMissing)
}
