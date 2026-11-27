// Package httpx holds small, dependency-free HTTP helpers shared by the API
// handlers, the WebDAV listener and the audit log. It imports nothing but the
// standard library, so any package may depend on it without creating a cycle.
package httpx

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
)

type contextKey string

const contextKeyClientIP contextKey = "client_ip"

// TrustedProxies is the set of network ranges whose forwarding headers are
// believed. Anything outside it is treated as a direct — and therefore
// unverified — caller.
type TrustedProxies []*net.IPNet

// ParseTrustedProxies converts config entries into matchable ranges. Each
// entry is either a CIDR ("10.0.0.0/8") or a bare address ("192.0.2.50",
// "::1"), which is taken as a single-host range.
func ParseTrustedProxies(entries []string) (TrustedProxies, error) {
	out := make(TrustedProxies, 0, len(entries))
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(entry); err == nil {
			out = append(out, network)
			continue
		}
		ip := net.ParseIP(entry)
		if ip == nil {
			return nil, fmt.Errorf("trusted proxy %q is neither a CIDR nor an IP address", raw)
		}
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	return out, nil
}

func (tp TrustedProxies) contains(ip net.IP) bool {
	for _, network := range tp {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// Middleware resolves the caller's address once and stores it on the request
// context, where ClientIP picks it up. It must run before anything that rate-
// limits, logs or persists the client address.
//
// Deliberately replaces chi's RealIP middleware: that one validates the header
// *format* but trusts its origin unconditionally, so any caller can choose the
// value that ends up in RemoteAddr. The resolved address keys the rate limiter
// and lands in INET columns, where a caller-chosen value disables the control.
func (tp TrustedProxies) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := tp.resolve(r)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKeyClientIP, ip)))
	})
}

// resolve determines the caller's address for one request.
func (tp TrustedProxies) resolve(r *http.Request) string {
	peer := peerIP(r)
	if peer == "" {
		return ""
	}
	if !tp.contains(net.ParseIP(peer)) {
		// Direct caller: the headers are whatever it chose to send.
		return peer
	}
	if forwarded := tp.rightmostUntrusted(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		return forwarded
	}
	if realIP := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); realIP != nil {
		return realIP.String()
	}
	return peer
}

// rightmostUntrusted walks an X-Forwarded-For chain from right to left and
// returns the first address outside the trusted set.
//
// Direction matters: each proxy appends the address it saw, so the rightmost
// entries are the ones our own infrastructure wrote, while the leftmost can be
// anything the original caller put there. Reading the chain left-to-right —
// as the previous implementation did — hands the caller full control.
func (tp TrustedProxies) rightmostUntrusted(header string) string {
	if header == "" {
		return ""
	}
	hops := strings.Split(header, ",")
	for i := len(hops) - 1; i >= 0; i-- {
		ip := net.ParseIP(strings.TrimSpace(hops[i]))
		if ip == nil {
			continue
		}
		if !tp.contains(ip) {
			return ip.String()
		}
	}
	return ""
}

// ClientIP returns the caller's address as a validated, port-less string, or
// "" when none could be determined.
//
// It reads the value Middleware resolved. Requests that never passed the
// middleware fall back to the connection's peer address, so callers outside
// the API router (WebDAV, unit tests) still get a correct answer rather than
// an empty one.
func ClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(contextKeyClientIP).(string); ok {
		return ip
	}
	return peerIP(r)
}

// peerIP returns the validated address of the TCP peer, ignoring every header.
func peerIP(r *http.Request) string {
	addr := r.RemoteAddr
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	ip := net.ParseIP(strings.Trim(addr, "[]"))
	if ip == nil {
		return ""
	}
	return ip.String()
}
