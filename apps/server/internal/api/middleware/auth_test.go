package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/auth"
	"privasync/server/internal/model"
)

const testSecret = "test-jwt-secret"

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestAuthenticate_ValidTokenPassesClaims(t *testing.T) {
	userID := uuid.New()
	tok, err := auth.IssueAccessToken(testSecret, time.Hour, userID, model.RoleAdmin)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	var gotClaims *auth.Claims
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClaims = ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()

	Authenticate(testSecret)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotClaims == nil || gotClaims.UserID != userID {
		t.Fatalf("claims not propagated correctly: %+v", gotClaims)
	}
}

func TestAuthenticate_MissingTokenRejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	Authenticate(testSecret)(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAuthenticate_InvalidTokenRejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer garbage")
	rec := httptest.NewRecorder()

	Authenticate(testSecret)(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAuthenticate_WrongTokenTypeRejected(t *testing.T) {
	// A 2FA-session token must not be accepted as a regular access token --
	// otherwise a half-authenticated client could skip the second factor.
	tok, err := auth.Issue2FASessionToken(testSecret, time.Hour, uuid.New())
	if err != nil {
		t.Fatalf("Issue2FASessionToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()

	Authenticate(testSecret)(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// A credential in the query string ends up in the request log, the browser
// history and any Referer sent onwards. The SSE stream now authenticates via
// the sse_token cookie instead, so nothing needs this path.
func TestAuthenticate_QueryParamRejected(t *testing.T) {
	tok, err := auth.IssueAccessToken(testSecret, time.Hour, uuid.New(), model.RoleUser)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/sse?token="+tok, nil)
	rec := httptest.NewRecorder()

	Authenticate(testSecret)(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — access token accepted from the URL", rec.Code)
	}
}

// AuthenticateSSE serves two callers: the desktop client, which sends a normal
// Bearer header, and the browser's EventSource, which cannot set headers at
// all and therefore relies on the cookie.
func TestAuthenticateSSE(t *testing.T) {
	userID := uuid.New()
	accessToken, err := auth.IssueAccessToken(testSecret, time.Hour, userID, model.RoleUser)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	sseToken, err := auth.IssueSSEToken(testSecret, time.Hour, userID, model.RoleUser)
	if err != nil {
		t.Fatalf("IssueSSEToken: %v", err)
	}
	twoFAToken, err := auth.Issue2FASessionToken(testSecret, time.Hour, userID)
	if err != nil {
		t.Fatalf("Issue2FASessionToken: %v", err)
	}

	tests := []struct {
		name   string
		bearer string
		cookie string
		query  string
		want   int
	}{
		{name: "bearer access token from the desktop client", bearer: accessToken, want: http.StatusOK},
		{name: "sse cookie from the browser", cookie: sseToken, want: http.StatusOK},
		{name: "no credential at all", want: http.StatusUnauthorized},
		{name: "query parameter is not a credential", query: accessToken, want: http.StatusUnauthorized},
		{name: "sse token is not accepted as a bearer", bearer: sseToken, want: http.StatusUnauthorized},
		{name: "access token is not accepted as the cookie", cookie: accessToken, want: http.StatusUnauthorized},
		{name: "2fa session token is not accepted", cookie: twoFAToken, want: http.StatusUnauthorized},
		{name: "garbage cookie", cookie: "garbage", want: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/api/v1/events"
			if tt.query != "" {
				url += "?token=" + tt.query
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			if tt.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tt.bearer)
			}
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: SSECookieName, Value: tt.cookie})
			}

			var gotClaims *auth.Claims
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotClaims = ClaimsFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			})
			rec := httptest.NewRecorder()
			AuthenticateSSE(testSecret)(next).ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
			if tt.want == http.StatusOK && (gotClaims == nil || gotClaims.UserID != userID) {
				t.Fatalf("claims not propagated: %+v", gotClaims)
			}
		})
	}
}

// The narrow SSE credential must not open the rest of the API.
func TestAuthenticate_SSETokenRejected(t *testing.T) {
	tok, err := auth.IssueSSEToken(testSecret, time.Hour, uuid.New(), model.RoleUser)
	if err != nil {
		t.Fatalf("IssueSSEToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.AddCookie(&http.Cookie{Name: SSECookieName, Value: tok})
	rec := httptest.NewRecorder()

	Authenticate(testSecret)(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — sse token reached a normal API route", rec.Code)
	}
}

func TestRequireAdmin_NonAdminForbidden(t *testing.T) {
	tok, err := auth.IssueAccessToken(testSecret, time.Hour, uuid.New(), model.RoleUser)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()

	chain := Authenticate(testSecret)(RequireAdmin(okHandler()))
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRequireAdmin_AdminPasses(t *testing.T) {
	tok, err := auth.IssueAccessToken(testSecret, time.Hour, uuid.New(), model.RoleAdmin)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()

	chain := Authenticate(testSecret)(RequireAdmin(okHandler()))
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestRequireAdmin_NoClaimsInContextForbidden(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	RequireAdmin(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestClaimsFromContext_NilWhenAbsent(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := ClaimsFromContext(req.Context()); got != nil {
		t.Fatalf("ClaimsFromContext = %+v, want nil", got)
	}
}
