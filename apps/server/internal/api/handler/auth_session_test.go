package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/audit"
	"privasync/server/internal/auth"
	"privasync/server/internal/config"
	"privasync/server/internal/model"
)

func newSessionEnv(t *testing.T) (*AuthHandler, *testEnv) {
	t.Helper()
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, config.AuthConfig{
		JWTSecret:            "test-secret",
		AccessTokenTTL:       5 * time.Minute,
		RefreshTokenTTL:      time.Hour,
		RefreshRotationGrace: 30 * time.Second,
		MaxLoginAttempts:     5,
		LoginLockoutWindow:   time.Hour,
	}, "http://localhost", "PrivaSync")
	return h, env
}

// seedUserWithPassword creates a user whose stored hash actually verifies
// against the returned plaintext, so Login's password path can be exercised.
func seedUserWithPassword(t *testing.T, env *testEnv, password string) *model.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	suffix := uniqueSuffix()
	u, err := env.users.Create(context.Background(), "sess_"+suffix, "sess_"+suffix+"@t.local", hash, model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	registerUserCleanup(t, env.pool, u.ID)
	return u
}

func TestLogin_RejectsMalformedBody(t *testing.T) {
	h, _ := newSessionEnv(t)
	w := postJSON(h.Login, "/auth/login", "{not json")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidBody)
}

func TestLogin_RejectsMissingCredentials(t *testing.T) {
	h, _ := newSessionEnv(t)
	w := postJSON(h.Login, "/auth/login", `{"username":"","password":""}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrMissingCredentials)
}

func TestLogin_RejectsUnknownUser(t *testing.T) {
	h, _ := newSessionEnv(t)
	w := postJSON(h.Login, "/auth/login", `{"username":"ghost_`+uniqueSuffix()+`","password":"whatever"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidCredentials)
}

func TestLogin_RejectsWrongPassword(t *testing.T) {
	h, env := newSessionEnv(t)
	u := seedUserWithPassword(t, env, "correct-password")
	w := postJSON(h.Login, "/auth/login", `{"username":"`+u.Username+`","password":"wrong-password"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidCredentials)
}

func TestLogin_RejectsDisabledAccount(t *testing.T) {
	h, env := newSessionEnv(t)
	u := seedUserWithPassword(t, env, "correct-password")
	if err := env.users.SetActive(context.Background(), u.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	w := postJSON(h.Login, "/auth/login", `{"username":"`+u.Username+`","password":"correct-password"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrAccountDisabled)
}

func TestLogin_SucceedsAndSetsRefreshCookie(t *testing.T) {
	h, env := newSessionEnv(t)
	u := seedUserWithPassword(t, env, "correct-password")
	w := postJSON(h.Login, "/auth/login", `{"username":"`+u.Username+`","password":"correct-password"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !hasCookie(w, "refresh_token") {
		t.Errorf("expected refresh_token cookie after successful login")
	}
}

func TestLogin_LocksOutAfterTooManyFailures(t *testing.T) {
	h, env := newSessionEnv(t)
	u := seedUserWithPassword(t, env, "correct-password")
	// Record MaxLoginAttempts (5) failures to trip the lockout gate.
	for i := 0; i < 5; i++ {
		if err := env.users.RecordLoginAttempt(context.Background(), u.Username, "10.0.0.1", false); err != nil {
			t.Fatalf("record attempt: %v", err)
		}
	}
	w := postJSON(h.Login, "/auth/login", `{"username":"`+u.Username+`","password":"correct-password"}`)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrTooManyAttempts)
}

func TestRefresh_RejectsMissingCookie(t *testing.T) {
	h, _ := newSessionEnv(t)
	r := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	w := httptest.NewRecorder()
	h.Refresh(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrNoRefreshToken)
}

func TestRefresh_RejectsUnknownToken(t *testing.T) {
	h, _ := newSessionEnv(t)
	r := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	r.AddCookie(&http.Cookie{Name: "refresh_token", Value: "not-a-real-token"})
	w := httptest.NewRecorder()
	h.Refresh(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRefreshToken)
}

func TestRefresh_RejectsExpiredToken(t *testing.T) {
	h, env := newSessionEnv(t)
	u := seedUserWithPassword(t, env, "pw")
	token := "refresh-" + uniqueSuffix()
	if err := env.users.CreateRefreshToken(context.Background(), u.ID, token, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("create refresh token: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	r.AddCookie(&http.Cookie{Name: "refresh_token", Value: token})
	w := httptest.NewRecorder()
	h.Refresh(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrRefreshTokenExpired)
}

func TestRefresh_RotatesTokenOnSuccess(t *testing.T) {
	h, env := newSessionEnv(t)
	u := seedUserWithPassword(t, env, "pw")
	token := "refresh-" + uniqueSuffix()
	if err := env.users.CreateRefreshToken(context.Background(), u.ID, token, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create refresh token: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	r.AddCookie(&http.Cookie{Name: "refresh_token", Value: token})
	w := httptest.NewRecorder()
	h.Refresh(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !hasCookie(w, "refresh_token") {
		t.Errorf("expected a rotated refresh_token cookie")
	}
	// The old token is retired: it survives its grace window (ADR-0046), but
	// it can never rotate again.
	old, err := env.users.FindRefreshToken(context.Background(), token)
	if err != nil {
		t.Fatalf("find rotated token: %v", err)
	}
	if old.RotatedAt == nil {
		t.Errorf("old refresh token was not marked as rotated")
	}
}

func TestLogout_ClearsCookieWithoutToken(t *testing.T) {
	h, _ := newSessionEnv(t)
	r := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	w := httptest.NewRecorder()
	h.Logout(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
}

func TestLogout_DeletesKnownToken(t *testing.T) {
	h, env := newSessionEnv(t)
	u := seedUserWithPassword(t, env, "pw")
	token := "refresh-" + uniqueSuffix()
	if err := env.users.CreateRefreshToken(context.Background(), u.ID, token, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create refresh token: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: "refresh_token", Value: token})
	w := httptest.NewRecorder()
	h.Logout(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	if _, err := env.users.FindRefreshToken(context.Background(), token); err == nil {
		t.Errorf("refresh token still present after logout")
	}
}

func TestMe_ReturnsCurrentUser(t *testing.T) {
	h, env := newSessionEnv(t)
	claims := createTestUser(t, env)
	r := reqWithClaims(http.MethodGet, "/auth/me", nil, claims)
	w := httptest.NewRecorder()
	h.Me(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestMe_RejectsUnknownUser(t *testing.T) {
	h, _ := newSessionEnv(t)
	claims := &auth.Claims{UserID: uuid.New(), Role: model.RoleUser, TokenType: auth.TokenTypeAccess}
	r := reqWithClaims(http.MethodGet, "/auth/me", nil, claims)
	w := httptest.NewRecorder()
	h.Me(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrUserNotFound)
}

// hasCookie reports whether the recorded response set a cookie of the given name.
func hasCookie(w *httptest.ResponseRecorder, name string) bool {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return true
		}
	}
	return false
}

// ─── Refresh-Token-Rotation mit Kulanzfenster (ADR-0046) ─────────────────────

// A client that fires several requests at once refreshes with the same cookie
// more than once: the second call arrives before the rotated cookie is back.
// Inside the grace window that must yield an access token instead of ending
// the session.
func TestRefresh_WithinGraceIssuesAccessTokenWithoutRotating(t *testing.T) {
	h, env := newSessionEnv(t)
	u := seedUserWithPassword(t, env, "pw")
	token := "refresh-" + uniqueSuffix()
	if err := env.users.CreateRefreshToken(context.Background(), u.ID, token, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create refresh token: %v", err)
	}

	refreshWith := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
		r.AddCookie(&http.Cookie{Name: "refresh_token", Value: token})
		w := httptest.NewRecorder()
		h.Refresh(w, r)
		return w
	}

	if w := refreshWith(); w.Code != http.StatusOK {
		t.Fatalf("first refresh: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	w := refreshWith()
	if w.Code != http.StatusOK {
		t.Fatalf("second refresh: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if hasCookie(w, "refresh_token") {
		t.Errorf("grace refresh must not rotate again")
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.AccessToken == "" {
		t.Errorf("grace refresh returned no access token")
	}
}

func TestRefresh_RejectsRotatedTokenAfterGrace(t *testing.T) {
	h, env := newSessionEnv(t)
	h.cfg.RefreshRotationGrace = 0
	u := seedUserWithPassword(t, env, "pw")
	token := "refresh-" + uniqueSuffix()
	if err := env.users.CreateRefreshToken(context.Background(), u.ID, token, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create refresh token: %v", err)
	}
	if err := env.users.MarkRefreshTokenRotated(context.Background(), token); err != nil {
		t.Fatalf("mark rotated: %v", err)
	}
	// rotated_at comes from the database's NOW(), the grace comparison from
	// the process clock. Those two are not the same clock -- a database a few
	// dozen milliseconds ahead makes a just-rotated token read as rotated in
	// the future, and a grace of 0 can then never be exceeded. Age the row so
	// the test asserts the grace rule rather than clock alignment.
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE refresh_tokens SET rotated_at = NOW() - INTERVAL '1 hour' WHERE user_id = $1`,
		u.ID); err != nil {
		t.Fatalf("age rotated_at: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	r.AddCookie(&http.Cookie{Name: "refresh_token", Value: token})
	w := httptest.NewRecorder()
	h.Refresh(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRefreshToken)
	if _, err := env.users.FindRefreshToken(context.Background(), token); err == nil {
		t.Errorf("a rotated token past its grace window must be deleted")
	}
}

// Rotated rows would otherwise pile up for the whole refresh-token TTL.
func TestRefresh_DropsRotatedTokensPastTheGraceWindow(t *testing.T) {
	h, env := newSessionEnv(t)
	u := seedUserWithPassword(t, env, "pw")
	stale := "refresh-" + uniqueSuffix()
	if err := env.users.CreateRefreshToken(context.Background(), u.ID, stale, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create stale token: %v", err)
	}
	if err := env.users.MarkRefreshTokenRotated(context.Background(), stale); err != nil {
		t.Fatalf("mark rotated: %v", err)
	}
	if err := env.users.DeleteRotatedRefreshTokens(context.Background(), u.ID, 0); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	current := "refresh-" + uniqueSuffix()
	if err := env.users.CreateRefreshToken(context.Background(), u.ID, current, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create current token: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	r.AddCookie(&http.Cookie{Name: "refresh_token", Value: current})
	w := httptest.NewRecorder()
	h.Refresh(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	if _, err := env.users.FindRefreshToken(context.Background(), stale); err == nil {
		t.Errorf("stale rotated token survived the sweep")
	}
}
