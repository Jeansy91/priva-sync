package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// beginPasskeyResponse mirrors the shape written by RegisterPasskeyBegin and
// PasskeyLoginBegin — both return {"options": ..., "challenge_token": ...}.
type beginPasskeyResponse struct {
	Options        json.RawMessage `json:"options"`
	ChallengeToken string          `json:"challenge_token"`
}

func TestRegisterPasskeyFinish_SucceedsWithValidCeremony(t *testing.T) {
	authH, passkeys, env := newPasskeyTestEnv(t)
	claims := createTestUser(t, env)
	va := newVirtualAuthenticator(t)

	beginReq := reqWithClaims(http.MethodPost, "/api/v1/auth/passkey/register/begin", nil, claims)
	beginW := httptest.NewRecorder()
	authH.RegisterPasskeyBegin(beginW, beginReq)
	if beginW.Code != http.StatusOK {
		t.Fatalf("begin status=%d body=%s", beginW.Code, beginW.Body.String())
	}
	var begun beginPasskeyResponse
	decodeBody(t, beginW, &begun)
	challenge := challengeFromOptions(t, begun.Options)

	credentialJSON := va.registrationResponseJSON(t, challenge, "localhost", "http://localhost")
	finishBody, _ := json.Marshal(map[string]any{
		"challenge_token": begun.ChallengeToken,
		"credential":      json.RawMessage(credentialJSON),
		"name":            "My Test Key",
	})
	finishReq := reqWithClaims(http.MethodPost, "/api/v1/auth/passkey/register/finish", bytes.NewReader(finishBody), claims)
	finishW := httptest.NewRecorder()
	authH.RegisterPasskeyFinish(finishW, finishReq)

	if finishW.Code != http.StatusCreated {
		t.Fatalf("finish status=%d body=%s", finishW.Code, finishW.Body.String())
	}
	var stored struct {
		Name string `json:"name"`
	}
	decodeBody(t, finishW, &stored)
	if stored.Name != "My Test Key" {
		t.Errorf("stored name = %q, want %q", stored.Name, "My Test Key")
	}

	rows, err := passkeys.ListByUser(t.Context(), claims.UserID)
	if err != nil {
		t.Fatalf("list passkeys: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 stored passkey, got %d", len(rows))
	}
}

func TestRegisterPasskeyFinish_DefaultsNameWhenBlank(t *testing.T) {
	authH, _, env := newPasskeyTestEnv(t)
	claims := createTestUser(t, env)
	va := newVirtualAuthenticator(t)

	beginReq := reqWithClaims(http.MethodPost, "/api/v1/auth/passkey/register/begin", nil, claims)
	beginW := httptest.NewRecorder()
	authH.RegisterPasskeyBegin(beginW, beginReq)
	var begun beginPasskeyResponse
	decodeBody(t, beginW, &begun)
	challenge := challengeFromOptions(t, begun.Options)

	credentialJSON := va.registrationResponseJSON(t, challenge, "localhost", "http://localhost")
	finishBody, _ := json.Marshal(map[string]any{
		"challenge_token": begun.ChallengeToken,
		"credential":      json.RawMessage(credentialJSON),
	})
	finishReq := reqWithClaims(http.MethodPost, "/api/v1/auth/passkey/register/finish", bytes.NewReader(finishBody), claims)
	finishW := httptest.NewRecorder()
	authH.RegisterPasskeyFinish(finishW, finishReq)

	if finishW.Code != http.StatusCreated {
		t.Fatalf("finish status=%d body=%s", finishW.Code, finishW.Body.String())
	}
	var stored struct {
		Name string `json:"name"`
	}
	decodeBody(t, finishW, &stored)
	if stored.Name != "Passkey" {
		t.Errorf("stored name = %q, want default %q", stored.Name, "Passkey")
	}
}

func TestPasskeyLoginFinish_SucceedsWithValidCeremonyAndIssuesTokens(t *testing.T) {
	authH, _, env := newPasskeyTestEnv(t)
	claims := createTestUser(t, env)
	va := newVirtualAuthenticator(t)

	// Register the passkey first so login has something to validate against.
	regBeginReq := reqWithClaims(http.MethodPost, "/api/v1/auth/passkey/register/begin", nil, claims)
	regBeginW := httptest.NewRecorder()
	authH.RegisterPasskeyBegin(regBeginW, regBeginReq)
	var regBegun beginPasskeyResponse
	decodeBody(t, regBeginW, &regBegun)
	regChallenge := challengeFromOptions(t, regBegun.Options)

	regFinishBody, _ := json.Marshal(map[string]any{
		"challenge_token": regBegun.ChallengeToken,
		"credential":      json.RawMessage(va.registrationResponseJSON(t, regChallenge, "localhost", "http://localhost")),
	})
	regFinishReq := reqWithClaims(http.MethodPost, "/api/v1/auth/passkey/register/finish", bytes.NewReader(regFinishBody), claims)
	regFinishW := httptest.NewRecorder()
	authH.RegisterPasskeyFinish(regFinishW, regFinishReq)
	if regFinishW.Code != http.StatusCreated {
		t.Fatalf("registration finish status=%d body=%s", regFinishW.Code, regFinishW.Body.String())
	}

	// Now perform a discoverable login with the same authenticator.
	loginBeginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/login/begin", nil)
	loginBeginW := httptest.NewRecorder()
	authH.PasskeyLoginBegin(loginBeginW, loginBeginReq)
	var loginBegun beginPasskeyResponse
	decodeBody(t, loginBeginW, &loginBegun)
	loginChallenge := challengeFromOptions(t, loginBegun.Options)

	userHandle, err := claims.UserID.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal user id: %v", err)
	}
	assertionJSON := va.assertionResponseJSON(t, loginChallenge, "localhost", "http://localhost", userHandle)
	loginFinishBody, _ := json.Marshal(map[string]any{
		"challenge_token": loginBegun.ChallengeToken,
		"credential":      json.RawMessage(assertionJSON),
	})
	loginFinishReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/login/finish", bytes.NewReader(loginFinishBody))
	loginFinishW := httptest.NewRecorder()
	authH.PasskeyLoginFinish(loginFinishW, loginFinishReq)

	if loginFinishW.Code != http.StatusOK {
		t.Fatalf("login finish status=%d body=%s", loginFinishW.Code, loginFinishW.Body.String())
	}
	var tokens struct {
		AccessToken string `json:"access_token"`
	}
	decodeBody(t, loginFinishW, &tokens)
	if tokens.AccessToken == "" {
		t.Errorf("expected non-empty access token, got %+v", tokens)
	}
	// The refresh token is delivered as an HttpOnly cookie, not in the body.
	var refreshCookie *http.Cookie
	for _, c := range loginFinishW.Result().Cookies() {
		if c.Name == "refresh_token" {
			refreshCookie = c
			break
		}
	}
	if refreshCookie == nil || refreshCookie.Value == "" {
		t.Errorf("expected refresh_token cookie to be set")
	}
}

func TestPasskeyLoginFinish_RejectsUnknownCredential(t *testing.T) {
	authH, _, _ := newPasskeyTestEnv(t)
	va := newVirtualAuthenticator(t) // never registered

	loginBeginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/login/begin", nil)
	loginBeginW := httptest.NewRecorder()
	authH.PasskeyLoginBegin(loginBeginW, loginBeginReq)
	var loginBegun beginPasskeyResponse
	decodeBody(t, loginBeginW, &loginBegun)
	loginChallenge := challengeFromOptions(t, loginBegun.Options)

	randomUserHandle := uuid.New()
	userHandleBytes, _ := randomUserHandle.MarshalBinary()
	assertionJSON := va.assertionResponseJSON(t, loginChallenge, "localhost", "http://localhost", userHandleBytes)
	loginFinishBody, _ := json.Marshal(map[string]any{
		"challenge_token": loginBegun.ChallengeToken,
		"credential":      json.RawMessage(assertionJSON),
	})
	loginFinishReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/login/finish", bytes.NewReader(loginFinishBody))
	loginFinishW := httptest.NewRecorder()
	authH.PasskeyLoginFinish(loginFinishW, loginFinishReq)

	if loginFinishW.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", loginFinishW.Code, loginFinishW.Body.String())
	}
}

func TestPasskeyLoginFinish_RejectsDisabledAccount(t *testing.T) {
	authH, _, env := newPasskeyTestEnv(t)
	claims := createTestUser(t, env)
	va := newVirtualAuthenticator(t)

	regBeginReq := reqWithClaims(http.MethodPost, "/api/v1/auth/passkey/register/begin", nil, claims)
	regBeginW := httptest.NewRecorder()
	authH.RegisterPasskeyBegin(regBeginW, regBeginReq)
	var regBegun beginPasskeyResponse
	decodeBody(t, regBeginW, &regBegun)
	regChallenge := challengeFromOptions(t, regBegun.Options)

	regFinishBody, _ := json.Marshal(map[string]any{
		"challenge_token": regBegun.ChallengeToken,
		"credential":      json.RawMessage(va.registrationResponseJSON(t, regChallenge, "localhost", "http://localhost")),
	})
	regFinishReq := reqWithClaims(http.MethodPost, "/api/v1/auth/passkey/register/finish", bytes.NewReader(regFinishBody), claims)
	regFinishW := httptest.NewRecorder()
	authH.RegisterPasskeyFinish(regFinishW, regFinishReq)
	if regFinishW.Code != http.StatusCreated {
		t.Fatalf("registration finish status=%d body=%s", regFinishW.Code, regFinishW.Body.String())
	}

	if err := env.users.SetActive(t.Context(), claims.UserID, false); err != nil {
		t.Fatalf("disable user: %v", err)
	}

	loginBeginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/login/begin", nil)
	loginBeginW := httptest.NewRecorder()
	authH.PasskeyLoginBegin(loginBeginW, loginBeginReq)
	var loginBegun beginPasskeyResponse
	decodeBody(t, loginBeginW, &loginBegun)
	loginChallenge := challengeFromOptions(t, loginBegun.Options)

	userHandle, _ := claims.UserID.MarshalBinary()
	assertionJSON := va.assertionResponseJSON(t, loginChallenge, "localhost", "http://localhost", userHandle)
	loginFinishBody, _ := json.Marshal(map[string]any{
		"challenge_token": loginBegun.ChallengeToken,
		"credential":      json.RawMessage(assertionJSON),
	})
	loginFinishReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/login/finish", bytes.NewReader(loginFinishBody))
	loginFinishW := httptest.NewRecorder()
	authH.PasskeyLoginFinish(loginFinishW, loginFinishReq)

	if loginFinishW.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", loginFinishW.Code, loginFinishW.Body.String())
	}
}
