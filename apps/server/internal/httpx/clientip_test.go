package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"privasync/server/internal/httpx"
)

func mustTrusted(t *testing.T, cidrs ...string) httpx.TrustedProxies {
	t.Helper()
	tp, err := httpx.ParseTrustedProxies(cidrs)
	if err != nil {
		t.Fatalf("ParseTrustedProxies(%v): %v", cidrs, err)
	}
	return tp
}

// resolve runs the middleware and reports what ClientIP saw downstream.
func resolve(t *testing.T, tp httpx.TrustedProxies, remoteAddr string, headers map[string]string) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remoteAddr
	for k, v := range headers {
		r.Header.Set(k, v)
	}

	var got string
	tp.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = httpx.ClientIP(r)
	})).ServeHTTP(httptest.NewRecorder(), r)
	return got
}

func TestParseTrustedProxies(t *testing.T) {
	t.Run("accepts CIDR and bare address", func(t *testing.T) {
		tp := mustTrusted(t, "10.0.0.0/8", "192.0.2.50", "::1")
		if len(tp) != 3 {
			t.Fatalf("want 3 ranges, got %d", len(tp))
		}
	})

	t.Run("empty input yields empty set", func(t *testing.T) {
		tp := mustTrusted(t)
		if len(tp) != 0 {
			t.Fatalf("want empty, got %d", len(tp))
		}
	})

	t.Run("rejects garbage", func(t *testing.T) {
		if _, err := httpx.ParseTrustedProxies([]string{"not-a-cidr"}); err == nil {
			t.Fatal("want error for malformed entry, got nil")
		}
	})
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		trusted    []string
		remoteAddr string
		headers    map[string]string
		want       string
	}{
		{
			name:       "strips the port from the peer address",
			remoteAddr: "203.0.113.7:51234",
			want:       "203.0.113.7",
		},
		{
			name:       "ignores X-Real-IP from an untrusted peer",
			remoteAddr: "203.0.113.7:51234",
			headers:    map[string]string{"X-Real-IP": "1.2.3.4"},
			want:       "203.0.113.7",
		},
		{
			name:       "ignores X-Forwarded-For from an untrusted peer",
			remoteAddr: "203.0.113.7:51234",
			headers:    map[string]string{"X-Forwarded-For": "1.2.3.4"},
			want:       "203.0.113.7",
		},
		{
			name:       "handles an IPv6 peer",
			remoteAddr: "[2001:db8::1]:443",
			want:       "2001:db8::1",
		},
		{
			name:       "returns empty for an unparsable peer",
			remoteAddr: "garbage",
			want:       "",
		},
		{
			name:       "believes X-Forwarded-For from a trusted proxy",
			trusted:    []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.1:40000",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9"},
			want:       "203.0.113.9",
		},
		{
			// The caller pre-set XFF to a forged value; the proxy appended the
			// address it actually saw. Only the appended one is trustworthy.
			name:       "takes the rightmost untrusted entry, not the caller-supplied one",
			trusted:    []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.1:40000",
			headers:    map[string]string{"X-Forwarded-For": "1.2.3.4, 203.0.113.9"},
			want:       "203.0.113.9",
		},
		{
			name:       "skips further trusted hops in the chain",
			trusted:    []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.1:40000",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9, 10.0.0.2"},
			want:       "203.0.113.9",
		},
		{
			name:       "falls back to X-Real-IP when X-Forwarded-For is absent",
			trusted:    []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.1:40000",
			headers:    map[string]string{"X-Real-IP": "203.0.113.9"},
			want:       "203.0.113.9",
		},
		{
			name:       "falls back to the peer when the header is unparsable",
			trusted:    []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.1:40000",
			headers:    map[string]string{"X-Forwarded-For": "not-an-ip"},
			want:       "10.0.0.1",
		},
		{
			name:       "falls back to the peer when the whole chain is trusted",
			trusted:    []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.1:40000",
			headers:    map[string]string{"X-Forwarded-For": "10.0.0.2, 10.0.0.3"},
			want:       "10.0.0.1",
		},
		{
			name:       "accepts a bare address as a trusted proxy",
			trusted:    []string{"192.0.2.50"},
			remoteAddr: "192.0.2.50:40000",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9"},
			want:       "203.0.113.9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolve(t, mustTrusted(t, tt.trusted...), tt.remoteAddr, tt.headers)
			if got != tt.want {
				t.Errorf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

// ClientIP must stay useful for requests that never passed the middleware —
// unit tests and the WebDAV listener build requests directly.
func TestClientIPWithoutMiddleware(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.7:51234"
	r.Header.Set("X-Real-IP", "1.2.3.4")

	if got := httpx.ClientIP(r); got != "203.0.113.7" {
		t.Fatalf("want the peer address 203.0.113.7, got %q", got)
	}
}
