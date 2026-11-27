package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/auth"
)

func cookieByName(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// The browser's EventSource cannot set an Authorization header, so the stream
// credential travels in a cookie instead of the URL. It must be scoped
// to the stream alone and carry its own token type, so that a cookie leaking
// out never becomes a general API credential.
func TestLogin_SetsScopedSSECookie(t *testing.T) {
	h, env := newSessionEnv(t)
	u := seedUserWithPassword(t, env, "correct-password")

	w := postJSON(h.Login, "/auth/login",
		`{"username":"`+u.Username+`","password":"correct-password"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	c := cookieByName(w, middleware.SSECookieName)
	if c == nil {
		t.Fatal("login did not set the sse_token cookie")
	}
	if !c.HttpOnly {
		t.Error("sse cookie must be HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("sse cookie SameSite = %v, want Strict", c.SameSite)
	}
	if c.Path != "/api/v1/events" {
		t.Errorf("sse cookie Path = %q, want /api/v1/events (stream only)", c.Path)
	}

	claims, err := auth.ParseToken("test-secret", c.Value)
	if err != nil {
		t.Fatalf("sse cookie does not carry a valid token: %v", err)
	}
	if claims.TokenType != auth.TokenTypeSSE {
		t.Errorf("sse cookie token type = %q, want %q", claims.TokenType, auth.TokenTypeSSE)
	}
	if claims.UserID != u.ID {
		t.Errorf("sse cookie belongs to %v, want %v", claims.UserID, u.ID)
	}
}

func TestLogout_ClearsSSECookie(t *testing.T) {
	h, _ := newSessionEnv(t)

	r := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	w := httptest.NewRecorder()
	h.Logout(w, r)

	c := cookieByName(w, middleware.SSECookieName)
	if c == nil {
		t.Fatal("logout did not clear the sse_token cookie")
	}
	if c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("sse cookie not expired on logout: value=%q maxAge=%d", c.Value, c.MaxAge)
	}
}
