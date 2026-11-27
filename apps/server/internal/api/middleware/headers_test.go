package middleware

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeaders_SetsExpectedHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := SecurityHeaders(next)

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
		"Permissions-Policy":     "camera=(), microphone=(), geolocation=()",
	}
	for header, expected := range want {
		if got := w.Header().Get(header); got != expected {
			t.Errorf("header %s = %q, want %q", header, got, expected)
		}
	}
	if csp := w.Header().Get("Content-Security-Policy"); csp == "" {
		t.Error("Content-Security-Policy header not set")
	}
	if hsts := w.Header().Get("Strict-Transport-Security"); hsts != "" {
		t.Errorf("Strict-Transport-Security = %q, want empty for a non-TLS request", hsts)
	}
}

func TestSecurityHeaders_SetsHSTSOnlyOverTLS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	h := SecurityHeaders(next)

	r := httptest.NewRequest("GET", "/", nil)
	r.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if hsts := w.Header().Get("Strict-Transport-Security"); hsts == "" {
		t.Error("Strict-Transport-Security header not set for a TLS request")
	}
}
