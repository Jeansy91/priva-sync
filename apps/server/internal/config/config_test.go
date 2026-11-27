package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfigFile(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

const validMinimalYAML = `
auth:
  jwt_secret: "01234567890123456789012345678901"
database:
  url: "postgres://user:pass@localhost:5432/db"
`

func TestLoad_ValidMinimalConfig_AppliesDefaults(t *testing.T) {
	path := writeConfigFile(t, validMinimalYAML)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.APIPort != 8080 {
		t.Errorf("Server.APIPort = %d, want 8080 (default)", cfg.Server.APIPort)
	}
	if cfg.Database.MaxConns != 20 {
		t.Errorf("Database.MaxConns = %d, want 20 (default)", cfg.Database.MaxConns)
	}
	if cfg.Auth.AccessTokenTTL.String() != "15m0s" {
		t.Errorf("Auth.AccessTokenTTL = %v, want 15m", cfg.Auth.AccessTokenTTL)
	}
	if cfg.SMTP.Enabled {
		t.Error("SMTP.Enabled should default to false")
	}
}

func TestLoad_MissingJWTSecretRejected(t *testing.T) {
	path := writeConfigFile(t, `
database:
  url: "postgres://user:pass@localhost:5432/db"
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load: want error for missing auth.jwt_secret")
	}
}

func TestLoad_ShortJWTSecretRejected(t *testing.T) {
	path := writeConfigFile(t, `
auth:
  jwt_secret: "too-short"
database:
  url: "postgres://user:pass@localhost:5432/db"
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load: want error for jwt_secret < 32 chars")
	}
}

func TestLoad_MissingDatabaseURLRejected(t *testing.T) {
	path := writeConfigFile(t, `
auth:
  jwt_secret: "01234567890123456789012345678901"
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load: want error for missing database.url")
	}
}

func TestLoad_SMTPEnabledRequiresHostAndFromEmail(t *testing.T) {
	path := writeConfigFile(t, `
auth:
  jwt_secret: "01234567890123456789012345678901"
database:
  url: "postgres://user:pass@localhost:5432/db"
smtp:
  enabled: true
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load: want error when smtp.enabled=true but host/from_email missing")
	}
}

func TestLoad_SMTPEnabledWithValidFieldsSucceeds(t *testing.T) {
	path := writeConfigFile(t, `
auth:
  jwt_secret: "01234567890123456789012345678901"
database:
  url: "postgres://user:pass@localhost:5432/db"
smtp:
  enabled: true
  host: "smtp.example.com"
  from_email: "noreply@example.com"
  tls_mode: "starttls"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.SMTP.Enabled {
		t.Error("SMTP.Enabled should be true")
	}
}

func TestLoad_SMTPInvalidTLSModeRejected(t *testing.T) {
	path := writeConfigFile(t, `
auth:
  jwt_secret: "01234567890123456789012345678901"
database:
  url: "postgres://user:pass@localhost:5432/db"
smtp:
  enabled: true
  host: "smtp.example.com"
  from_email: "noreply@example.com"
  tls_mode: "totally-invalid"
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load: want error for invalid smtp.tls_mode")
	}
}

func TestLoad_DocumentServerBothEmptySucceeds(t *testing.T) {
	path := writeConfigFile(t, validMinimalYAML)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DocumentServer.URL != "" || cfg.DocumentServer.JWTSecret != "" {
		t.Fatal("DocumentServer should stay empty when unset")
	}
}

func TestLoad_DocumentServerHalfSetRejected(t *testing.T) {
	path := writeConfigFile(t, `
auth:
  jwt_secret: "01234567890123456789012345678901"
database:
  url: "postgres://user:pass@localhost:5432/db"
document_server:
  url: "https://docserver.example"
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load: want error when only document_server.url is set")
	}
}

func TestLoad_DocumentServerBothSetSucceeds(t *testing.T) {
	path := writeConfigFile(t, `
auth:
  jwt_secret: "01234567890123456789012345678901"
database:
  url: "postgres://user:pass@localhost:5432/db"
document_server:
  url: "https://docserver.example"
  jwt_secret: "shared-secret"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DocumentServer.URL != "https://docserver.example" || cfg.DocumentServer.JWTSecret != "shared-secret" {
		t.Fatalf("DocumentServer = %+v, want both fields set", cfg.DocumentServer)
	}
}

func TestLoad_EnvVarsOverrideConfigFile(t *testing.T) {
	path := writeConfigFile(t, validMinimalYAML)
	t.Setenv("PRIVASYNC_DATABASE__URL", "postgres://from-env:pass@localhost:5432/db")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Database.URL != "postgres://from-env:pass@localhost:5432/db" {
		t.Errorf("Database.URL = %q, want env-var value", cfg.Database.URL)
	}
}

// Regression: keys without a default and without an explicit bind
// (server.static_path) were silently dropped by
// Unmarshal even with a correctly-named env var set. Load must now surface
// them via the loop-bind over all known keys.
func TestLoad_EnvVarsOverrideUnboundKeys(t *testing.T) {
	path := writeConfigFile(t, validMinimalYAML)
	t.Setenv("PRIVASYNC_SERVER__STATIC_PATH", "/srv/web")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.StaticPath != "/srv/web" {
		t.Errorf("Server.StaticPath = %q, want /srv/web (from env)", cfg.Server.StaticPath)
	}
}

// Regression: the env prefix takes exactly one underscore after
// PRIVASYNC (PRIVASYNC_SERVER__BASE_URL), not two. The double-underscore form
// docker-compose used historically must NOT resolve.
func TestLoad_DoubleUnderscoreEnvDoesNotResolve(t *testing.T) {
	path := writeConfigFile(t, validMinimalYAML)
	t.Setenv("PRIVASYNC__SERVER__BASE_URL", "http://wrong-double-underscore")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.BaseURL == "http://wrong-double-underscore" {
		t.Error("double-underscore env var must not resolve; single underscore after prefix is the contract")
	}
}

func TestLoad_ExplicitMissingConfigFileRejected(t *testing.T) {
	// Unlike the empty-path case (which searches known locations and
	// tolerates absence), an explicitly passed path is expected to exist.
	nonexistent := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	if _, err := Load(nonexistent); err == nil {
		t.Fatal("Load: want error for explicit nonexistent config path")
	}
}

func TestLoad_TrustedProxiesDefaultsToEmpty(t *testing.T) {
	path := writeConfigFile(t, validMinimalYAML)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Server.TrustedProxies) != 0 {
		t.Errorf("Server.TrustedProxies = %v, want empty (trust no forwarding header by default)",
			cfg.Server.TrustedProxies)
	}
}

func TestLoad_TrustedProxiesParsed(t *testing.T) {
	path := writeConfigFile(t, validMinimalYAML+`
server:
  trusted_proxies:
    - "10.0.0.0/8"
    - "192.0.2.50"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"10.0.0.0/8", "192.0.2.50"}
	if len(cfg.Server.TrustedProxies) != len(want) {
		t.Fatalf("Server.TrustedProxies = %v, want %v", cfg.Server.TrustedProxies, want)
	}
	for i, w := range want {
		if cfg.Server.TrustedProxies[i] != w {
			t.Errorf("Server.TrustedProxies[%d] = %q, want %q", i, cfg.Server.TrustedProxies[i], w)
		}
	}
}

func TestLoad_MalformedTrustedProxyRejected(t *testing.T) {
	path := writeConfigFile(t, validMinimalYAML+`
server:
  trusted_proxies:
    - "not-a-cidr"
`)

	if _, err := Load(path); err == nil {
		t.Fatal("Load should reject a malformed trusted_proxies entry at startup")
	}
}

// The deployment sets this via env, not via config file (docker-compose), so
// the comma-separated form has to survive viper's Unmarshal.
func TestLoad_TrustedProxiesFromCommaSeparatedEnv(t *testing.T) {
	path := writeConfigFile(t, validMinimalYAML)
	t.Setenv("PRIVASYNC_SERVER__TRUSTED_PROXIES", "10.0.0.0/8,172.16.0.0/12")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"10.0.0.0/8", "172.16.0.0/12"}
	if len(cfg.Server.TrustedProxies) != len(want) {
		t.Fatalf("Server.TrustedProxies = %#v, want %#v", cfg.Server.TrustedProxies, want)
	}
	for i, w := range want {
		if cfg.Server.TrustedProxies[i] != w {
			t.Errorf("Server.TrustedProxies[%d] = %q, want %q", i, cfg.Server.TrustedProxies[i], w)
		}
	}
}
