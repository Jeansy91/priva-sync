package httpx

import (
	"net"
	"net/url"
	"strings"
)

// Origin reduces a URL to a comparable scheme://host:port triple with the
// scheme's default port made explicit, so that "https://h" and "https://h:443"
// compare equal. Anything that is not absolute http(s) fails.
func Origin(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", false
	}
	port := u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return scheme + "://" + net.JoinHostPort(host, port), true
}

// SameOrigin reports whether a and b denote the same origin (see Origin).
// Either URL failing to parse as absolute http(s) makes them not the same
// origin, even if both fail identically.
func SameOrigin(a, b string) bool {
	oa, ok := Origin(a)
	if !ok {
		return false
	}
	ob, ok := Origin(b)
	return ok && oa == ob
}
