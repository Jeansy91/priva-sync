package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"

	"privasync/server/internal/httpx"
)

type Config struct {
	Server         ServerConfig
	Database       DatabaseConfig
	Auth           AuthConfig
	Storage        StorageConfig
	Sync           SyncConfig
	SMTP           SMTPConfig
	DocumentServer DocumentServerConfig `mapstructure:"document_server"`
}

type ServerConfig struct {
	Host        string   `mapstructure:"host"`
	APIPort     int      `mapstructure:"api_port"`
	WebDAVPort  int      `mapstructure:"webdav_port"`
	BaseURL     string   `mapstructure:"base_url"`
	CORSOrigins []string `mapstructure:"cors_origins"`
	StaticPath  string   `mapstructure:"static_path"`
	// TrustedProxies lists the CIDRs (or bare addresses) whose
	// X-Forwarded-For / X-Real-IP headers are believed. Empty — the default —
	// means no forwarding header is trusted and the TCP peer address is used
	// everywhere. Set this to the reverse proxy's network when running behind
	// one; leaving it empty there makes every caller look like the proxy.
	TrustedProxies []string `mapstructure:"trusted_proxies"`
}

type DatabaseConfig struct {
	URL             string        `mapstructure:"url"`
	MaxConns        int32         `mapstructure:"max_conns"`
	MinConns        int32         `mapstructure:"min_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
}

type AuthConfig struct {
	JWTSecret       string        `mapstructure:"jwt_secret"`
	AccessTokenTTL  time.Duration `mapstructure:"access_token_ttl"`
	RefreshTokenTTL time.Duration `mapstructure:"refresh_token_ttl"`
	// How long a just-rotated refresh token still authenticates a refresh.
	// Covers the in-flight requests of a client that refreshes in parallel
	// (ADR-0046); short enough that a stolen token is worth little.
	RefreshRotationGrace time.Duration `mapstructure:"refresh_rotation_grace"`
	TwoFASessionTTL      time.Duration `mapstructure:"two_fa_session_ttl"`
	PasskeyChallengeTTL  time.Duration `mapstructure:"passkey_challenge_ttl"`
	MaxLoginAttempts     int           `mapstructure:"max_login_attempts"`
	LoginLockoutWindow   time.Duration `mapstructure:"login_lockout_window"`
}

type StorageConfig struct {
	DataPath        string `mapstructure:"data_path"`
	MaxUploadSizeMB int64  `mapstructure:"max_upload_size_mb"`
}

type SyncConfig struct {
	DeltaThresholdMB int `mapstructure:"delta_threshold_mb"`
	BlockSizeMB      int `mapstructure:"block_size_mb"`
}

// DocumentServerConfig points at an externally operated, OnlyOffice-API-
// compatible document server ("Euro-Office") used for read-only Office-
// document preview (ADR-0027). Deliberately has no built-in default — an
// empty URL means the feature stays fully inactive (Enabled() == false),
// never a request against a wrong/local fallback.
type DocumentServerConfig struct {
	URL       string `mapstructure:"url"`
	JWTSecret string `mapstructure:"jwt_secret"`
}

type SMTPConfig struct {
	Host      string `mapstructure:"host"`
	Port      int    `mapstructure:"port"`
	Username  string `mapstructure:"username"`
	Password  string `mapstructure:"password"`
	FromName  string `mapstructure:"from_name"`
	FromEmail string `mapstructure:"from_email"`
	// TLSMode: "none" (plain), "starttls" (STARTTLS upgrade, Port 587),
	// "tls" (implicit TLS / SMTPS, Port 465).
	TLSMode string `mapstructure:"tls_mode"`
	Enabled bool   `mapstructure:"enabled"`
}

func Load(path string) (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.api_port", 8080)
	v.SetDefault("server.webdav_port", 8081)
	v.SetDefault("server.base_url", "http://localhost:8080")
	v.SetDefault("server.cors_origins", []string{"http://localhost:8080"})
	v.SetDefault("server.trusted_proxies", []string{})

	v.SetDefault("database.max_conns", 20)
	v.SetDefault("database.min_conns", 2)
	v.SetDefault("database.conn_max_lifetime", "1h")

	v.SetDefault("auth.access_token_ttl", "15m")
	v.SetDefault("auth.refresh_token_ttl", "720h")
	v.SetDefault("auth.refresh_rotation_grace", "30s")
	v.SetDefault("auth.two_fa_session_ttl", "5m")
	v.SetDefault("auth.passkey_challenge_ttl", "2m")
	v.SetDefault("auth.max_login_attempts", 20)
	v.SetDefault("auth.login_lockout_window", "1m")

	v.SetDefault("storage.data_path", "/data")
	v.SetDefault("storage.max_upload_size_mb", 10240)

	v.SetDefault("sync.delta_threshold_mb", 1)
	v.SetDefault("sync.block_size_mb", 1)

	v.SetDefault("smtp.port", 587)
	v.SetDefault("smtp.tls_mode", "starttls")
	v.SetDefault("smtp.enabled", false)

	// Config file
	if path != "" {
		v.SetConfigFile(path)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath("/config")
		v.AddConfigPath(".")
	}

	// Env-var overrides. Format: PRIVASYNC_<SECTION>__<FIELD>, e.g.
	// PRIVASYNC_DATABASE__URL, PRIVASYNC_SERVER__BASE_URL (single underscore
	// after the prefix, "." -> "__" between segments).
	v.SetEnvPrefix("PRIVASYNC")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "__"))
	v.AutomaticEnv()

	// AutomaticEnv only reaches Unmarshal for keys viper already knows (via a
	// default or an explicit bind). Register every field that has no
	// default so every key is known, then bind them all — relying on
	// AutomaticEnv alone leaves unregistered keys empty after Unmarshal.
	v.SetDefault("server.static_path", "")
	v.SetDefault("database.url", "")
	v.SetDefault("auth.jwt_secret", "")
	v.SetDefault("smtp.host", "")
	v.SetDefault("smtp.username", "")
	v.SetDefault("smtp.password", "")
	v.SetDefault("smtp.from_name", "")
	v.SetDefault("smtp.from_email", "")
	v.SetDefault("document_server.url", "")
	v.SetDefault("document_server.jwt_secret", "")
	for _, key := range v.AllKeys() {
		_ = v.BindEnv(key)
	}

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

func validate(cfg *Config) error {
	if cfg.Auth.JWTSecret == "" {
		return fmt.Errorf("auth.jwt_secret is required")
	}
	if len(cfg.Auth.JWTSecret) < 32 {
		return fmt.Errorf("auth.jwt_secret must be at least 32 characters")
	}
	if cfg.Database.URL == "" {
		return fmt.Errorf("database.url is required")
	}
	// A malformed entry here would silently degrade to "trust nothing", which
	// looks like a working config but breaks rate limiting and the audit log
	// behind a proxy. Fail at startup instead.
	if _, err := httpx.ParseTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		return fmt.Errorf("server.trusted_proxies: %w", err)
	}
	// Document-server: both-or-neither. A lone URL or secret is almost
	// certainly a typo'd env var, not an intentional half-config — fail
	// fast at startup instead of silently disabling the feature.
	if (cfg.DocumentServer.URL == "") != (cfg.DocumentServer.JWTSecret == "") {
		return fmt.Errorf("document_server.url and document_server.jwt_secret must both be set or both be empty")
	}
	if cfg.SMTP.Enabled {
		switch cfg.SMTP.TLSMode {
		case "none", "starttls", "tls":
		default:
			return fmt.Errorf("smtp.tls_mode must be one of: none, starttls, tls")
		}
		if cfg.SMTP.Host == "" {
			return fmt.Errorf("smtp.host is required when smtp.enabled=true")
		}
		if cfg.SMTP.FromEmail == "" {
			return fmt.Errorf("smtp.from_email is required when smtp.enabled=true")
		}
	}
	return nil
}
