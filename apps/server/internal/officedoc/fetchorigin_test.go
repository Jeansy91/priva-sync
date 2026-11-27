package officedoc

import "testing"

// The save-back callback hands us a URL to fetch. The callback token is not a
// secret from the user -- it sits in the /office-config response -- so any
// authenticated caller can POST an arbitrary url and make the server fetch it
//. Only the configured document server's own origin may be dialled.
func TestConfig_AllowsDocumentURL(t *testing.T) {
	cfg := Config{URL: "https://docs.example.test:8443", JWTSecret: "s"}

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "the document server itself", url: "https://docs.example.test:8443/cache/files/x/output.docx", want: true},
		{name: "same origin, root path", url: "https://docs.example.test:8443/", want: true},
		{name: "host casing is irrelevant", url: "https://DOCS.EXAMPLE.TEST:8443/x", want: true},
		{name: "different host", url: "https://evil.example.test:8443/x", want: false},
		{name: "different port", url: "https://docs.example.test:9999/x", want: false},
		{name: "no port where one is configured", url: "https://docs.example.test/x", want: false},
		{name: "downgraded scheme", url: "http://docs.example.test:8443/x", want: false},
		{name: "cloud metadata", url: "http://169.254.169.254/latest/meta-data/", want: false},
		{name: "loopback", url: "http://127.0.0.1:8080/api/v1/admin/users", want: false},
		{name: "userinfo pointing elsewhere", url: "https://docs.example.test:8443@evil.example.test/x", want: false},
		{name: "non-http scheme", url: "file:///etc/passwd", want: false},
		{name: "empty", url: "", want: false},
		{name: "unparsable", url: "://:::", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cfg.AllowsDocumentURL(tt.url); got != tt.want {
				t.Errorf("AllowsDocumentURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

// A document server reached on its scheme's default port may be configured
// with or without the port; both spellings denote the same origin.
func TestConfig_AllowsDocumentURL_DefaultPorts(t *testing.T) {
	tests := []struct {
		name      string
		configURL string
		url       string
		want      bool
	}{
		{name: "https configured bare, url explicit", configURL: "https://docs.test", url: "https://docs.test:443/x", want: true},
		{name: "https configured explicit, url bare", configURL: "https://docs.test:443", url: "https://docs.test/x", want: true},
		{name: "http configured bare, url explicit", configURL: "http://docs.test", url: "http://docs.test:80/x", want: true},
		{name: "http default port is not the https one", configURL: "http://docs.test", url: "http://docs.test:443/x", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{URL: tt.configURL, JWTSecret: "s"}
			if got := cfg.AllowsDocumentURL(tt.url); got != tt.want {
				t.Errorf("AllowsDocumentURL(%q) with config %q = %v, want %v",
					tt.url, tt.configURL, got, tt.want)
			}
		})
	}
}

// With the feature switched off nothing may be fetched at all.
func TestConfig_AllowsDocumentURL_DisabledConfigAllowsNothing(t *testing.T) {
	var cfg Config
	if cfg.AllowsDocumentURL("http://anything.test/x") {
		t.Error("a disabled document server must not authorise any fetch")
	}
}
