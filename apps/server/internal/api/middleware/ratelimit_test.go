package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

// A caller must not be able to pick its own limiter bucket by sending a
// forwarding header — that would make every limit in the router decorative.
func TestRateLimiter_Middleware_IgnoresForwardingHeadersFromUntrustedPeer(t *testing.T) {
	rl := NewRateLimiter(rate.Limit(0.001), 1)
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	send := func(realIPHeader string) int {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "203.0.113.7:51234"
		r.Header.Set("X-Real-IP", realIPHeader)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}

	if code := send("1.0.0.1"); code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200 (within burst)", code)
	}
	// Same peer, a fresh forged header each time: the burst must stay spent.
	for i, forged := range []string{"1.0.0.2", "1.0.0.3", "1.0.0.4"} {
		if code := send(forged); code != http.StatusTooManyRequests {
			t.Fatalf("request %d with X-Real-IP %s status = %d, want 429 — header-spoofed bypass",
				i+2, forged, code)
		}
	}
}

func TestRateLimiter_GetLimiter_ReusesSameLimiterPerIP(t *testing.T) {
	rl := NewRateLimiter(rate.Limit(1), 1)

	a := rl.getLimiter("1.2.3.4")
	b := rl.getLimiter("1.2.3.4")
	if a != b {
		t.Error("getLimiter returned different instances for the same IP")
	}

	c := rl.getLimiter("5.6.7.8")
	if a == c {
		t.Error("getLimiter returned the same instance for different IPs")
	}
}

func TestRateLimiter_Middleware_BlocksAfterBurstExhausted(t *testing.T) {
	rl := NewRateLimiter(rate.Limit(0.001), 1)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := rl.Middleware(next)

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "9.9.9.9:1"

	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, r)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200 (within burst)", w1.Code)
	}

	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429 (burst exhausted)", w2.Code)
	}
}

func TestRateLimiter_Middleware_TracksIPsIndependently(t *testing.T) {
	rl := NewRateLimiter(rate.Limit(0.001), 1)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := rl.Middleware(next)

	r1 := httptest.NewRequest("GET", "/", nil)
	r1.RemoteAddr = "1.1.1.1:1"
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "2.2.2.2:1"

	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, r1)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)

	if w1.Code != http.StatusOK || w2.Code != http.StatusOK {
		t.Fatalf("first request per IP should pass: ip1=%d ip2=%d", w1.Code, w2.Code)
	}
}

// The limiter map is keyed by caller address. Even with the forwarding-header
// bypass closed, a caller on a directly exposed port can still walk a large
// address range (an IPv6 /64 costs nothing), so the bookkeeping needs its own
// ceiling -- otherwise the rate limiter is itself the memory-exhaustion
// vector it exists to prevent.
func TestRateLimiter_EvictsLeastRecentlyUsedBeyondCap(t *testing.T) {
	rl := NewRateLimiter(rate.Limit(1), 1)
	rl.MaxEntries = 3

	rl.getLimiter("10.0.0.1")
	rl.getLimiter("10.0.0.2")
	rl.getLimiter("10.0.0.3")
	// Touch .1 so .2 becomes the least recently used.
	first := rl.getLimiter("10.0.0.1")
	rl.getLimiter("10.0.0.4")

	if got := rl.size(); got != 3 {
		t.Fatalf("limiter count = %d, want at most 3", got)
	}
	if rl.getLimiter("10.0.0.1") != first {
		t.Error("recently used entry was evicted instead of the oldest")
	}
}

func TestRateLimiter_StaysBoundedUnderAddressFlood(t *testing.T) {
	rl := NewRateLimiter(rate.Limit(1), 1)
	rl.MaxEntries = 64

	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 5000; i++ {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = fmt.Sprintf("2001:db8::%x", i)
		h.ServeHTTP(httptest.NewRecorder(), r)
	}

	if got := rl.size(); got > 64 {
		t.Fatalf("limiter count = %d after 5000 distinct addresses, want at most 64", got)
	}
}
