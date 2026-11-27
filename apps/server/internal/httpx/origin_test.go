package httpx

import "testing"

func TestSameOrigin(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "identical", a: "https://docs.example.test:8443/x", b: "https://docs.example.test:8443/y", want: true},
		{name: "host casing is irrelevant", a: "https://DOCS.example.test:8443/x", b: "https://docs.example.test:8443/y", want: true},
		{name: "different host", a: "https://docs.example.test:8443/x", b: "https://evil.example.test:8443/x", want: false},
		{name: "different port", a: "https://docs.example.test:8443/x", b: "https://docs.example.test:9999/x", want: false},
		{name: "downgraded scheme", a: "https://docs.example.test:8443/x", b: "http://docs.example.test:8443/x", want: false},
		{name: "non-http scheme", a: "https://docs.example.test/x", b: "file:///etc/passwd", want: false},
		{name: "empty a", a: "", b: "https://docs.example.test/x", want: false},
		{name: "unparsable b", a: "https://docs.example.test/x", b: "://:::", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SameOrigin(tt.a, tt.b); got != tt.want {
				t.Errorf("SameOrigin(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestSameOrigin_DefaultPorts(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "https bare vs explicit", a: "https://docs.test/x", b: "https://docs.test:443/y", want: true},
		{name: "http bare vs explicit", a: "http://docs.test/x", b: "http://docs.test:80/y", want: true},
		{name: "http default port is not the https one", a: "http://docs.test/x", b: "http://docs.test:443/y", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SameOrigin(tt.a, tt.b); got != tt.want {
				t.Errorf("SameOrigin(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
