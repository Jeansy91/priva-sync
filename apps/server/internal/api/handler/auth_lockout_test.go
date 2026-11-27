package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"privasync/server/internal/auth"
)

// The login lockout is the only brake on password guessing, and it is driven
// entirely by the login_attempts table. Whenever the server cannot read or
// write that table it must refuse the request -- carrying on means every
// further guess is unmetered and invisible (#179).
func TestLogin_LockoutBookkeepingFailureIs500(t *testing.T) {
	hash, err := auth.HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	tests := []struct {
		name     string
		errOn    string
		password string
	}{
		{
			name:     "cannot read the failed-attempt count",
			errOn:    "CountFailedAttempts",
			password: "correct-horse-battery-staple",
		},
		{
			name:     "cannot record a failed attempt for a known user",
			errOn:    "RecordLoginAttempt",
			password: "wrong-password",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			users := fakeAuthUsers{errOn: map[string]error{tt.errOn: errBoom}}
			users.user.PasswordHash = hash
			users.user.IsActive = true
			h := fakeAuthHandler(users, fakeAuthPasskeys{}, fakeAuthStorage{})

			w := httptest.NewRecorder()
			h.Login(w, authErrReq(http.MethodPost, "/auth/login",
				`{"username":"u","password":"`+tt.password+`"}`))

			assert500(t, w)
		})
	}
}

// End-to-end proof that the lockout survives a caller rotating forged
// forwarding headers. Before #177/#179 each request carried a fresh, invalid
// X-Real-IP straight into the INET column, the INSERT failed, the discarded
// error left login_attempts empty, and the counter never moved.
func TestLogin_LockoutSurvivesForgedForwardingHeaders(t *testing.T) {
	h, env := newSessionEnv(t)
	u := seedUserWithPassword(t, env, "correct-password")

	forged := []string{
		"not-an-ip", "'; DROP TABLE login_attempts; --", "", "999.999.999.999",
		"1.2.3.4, junk", "<script>", "::gg", "0", "localhost", "10.0.0.1:1234",
	}
	body := `{"username":"` + u.Username + `","password":"wrong-password"}`

	for i := 0; i < 5; i++ {
		r := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Real-IP", forged[i%len(forged)])
		r.Header.Set("X-Forwarded-For", forged[(i+1)%len(forged)])
		w := httptest.NewRecorder()
		h.Login(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401; body=%s", i+1, w.Code, w.Body.String())
		}
	}

	// Correct password now, but the account must be locked out anyway.
	w := postJSON(h.Login, "/auth/login",
		`{"username":"`+u.Username+`","password":"correct-password"}`)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 — forged headers defeated the lockout; body=%s",
			w.Code, w.Body.String())
	}
	assertCode(t, w, ErrTooManyAttempts)
}
