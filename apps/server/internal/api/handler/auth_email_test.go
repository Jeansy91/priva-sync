package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"privasync/server/internal/auth"
)

func postEmail(h func(http.ResponseWriter, *http.Request), claims *auth.Claims, body string) *httptest.ResponseRecorder {
	r := reqWithClaims(http.MethodPost, "/auth/profile/email-change", bytes.NewBufferString(body), claims)
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestRequestEmailChange_RejectsMalformedBody(t *testing.T) {
	h, env := newPasswordResetEnv(t)
	claims := createTestUser(t, env)
	w := postEmail(h.RequestEmailChange, claims, "{bad")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidBody)
}

func TestRequestEmailChange_RejectsInvalidEmail(t *testing.T) {
	h, env := newPasswordResetEnv(t)
	claims := createTestUser(t, env)
	w := postEmail(h.RequestEmailChange, claims, `{"new_email":"not-an-email"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrEmailInvalid)
}

func TestRequestEmailChange_RejectsTakenEmail(t *testing.T) {
	h, env := newPasswordResetEnv(t)
	claims := createTestUser(t, env)
	taken := "taken_" + uniqueSuffix() + "@t.local"
	seedUser(t, env, "other_"+uniqueSuffix(), taken)

	w := postEmail(h.RequestEmailChange, claims, `{"new_email":"`+taken+`"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrEmailTaken)
}

func TestRequestEmailChange_Succeeds(t *testing.T) {
	h, env := newPasswordResetEnv(t)
	claims := createTestUser(t, env)
	w := postEmail(h.RequestEmailChange, claims, `{"new_email":"fresh_`+uniqueSuffix()+`@t.local"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", w.Code, w.Body.String())
	}
}

func TestConfirmEmailChange_RejectsMalformedBody(t *testing.T) {
	h, env := newPasswordResetEnv(t)
	claims := createTestUser(t, env)
	r := reqWithClaims(http.MethodPost, "/auth/profile/email-change/confirm", bytes.NewBufferString("{bad"), claims)
	w := httptest.NewRecorder()
	h.ConfirmEmailChange(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidBody)
}

func TestConfirmEmailChange_RejectsMissingToken(t *testing.T) {
	h, env := newPasswordResetEnv(t)
	claims := createTestUser(t, env)
	r := reqWithClaims(http.MethodPost, "/auth/profile/email-change/confirm", bytes.NewBufferString(`{"token":""}`), claims)
	w := httptest.NewRecorder()
	h.ConfirmEmailChange(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrTokenRequired)
}

func TestConfirmEmailChange_RejectsUnknownToken(t *testing.T) {
	h, env := newPasswordResetEnv(t)
	claims := createTestUser(t, env)
	r := reqWithClaims(http.MethodPost, "/auth/profile/email-change/confirm", bytes.NewBufferString(`{"token":"nope"}`), claims)
	w := httptest.NewRecorder()
	h.ConfirmEmailChange(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrTokenInvalid)
}

func TestConfirmEmailChange_AdoptsNewEmail(t *testing.T) {
	h, env := newPasswordResetEnv(t)
	u := seedUser(t, env, "chg_"+uniqueSuffix(), "chg_"+uniqueSuffix()+"@t.local")
	claims := &auth.Claims{UserID: u.ID, Role: u.Role, TokenType: auth.TokenTypeAccess}
	newEmail := "adopted_" + uniqueSuffix() + "@t.local"
	token := "email-token-" + uniqueSuffix()
	if err := env.users.CreateEmailChangeToken(context.Background(), u.ID, newEmail, token, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create token: %v", err)
	}

	r := reqWithClaims(http.MethodPost, "/auth/profile/email-change/confirm", bytes.NewBufferString(`{"token":"`+token+`"}`), claims)
	w := httptest.NewRecorder()
	h.ConfirmEmailChange(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	updated, err := env.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	if updated.Email != newEmail {
		t.Errorf("email = %q, want %q", updated.Email, newEmail)
	}
}
