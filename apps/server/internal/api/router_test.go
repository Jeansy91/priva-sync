package api_test

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/api"
	"privasync/server/internal/assembler"
	"privasync/server/internal/audit"
	"privasync/server/internal/bootstrap"
	"privasync/server/internal/bus"
	"privasync/server/internal/config"
	"privasync/server/internal/db"
	"privasync/server/internal/jobs"
	"privasync/server/internal/mail"
	"privasync/server/internal/outbox"
	"privasync/server/internal/quota"
	"privasync/server/internal/repository"
	"privasync/server/internal/sse"
	"privasync/server/internal/storage"
	syncpkg "privasync/server/internal/sync"
)

var (
	poolOnce sync.Once
	testPool *pgxpool.Pool
	poolErr  error
)

func loadDotenv() {
	dir, _ := os.Getwd()
	for i := 0; i < 6; i++ {
		path := filepath.Join(dir, ".env")
		if f, err := os.Open(path); err == nil {
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				eq := strings.IndexByte(line, '=')
				if eq < 1 {
					continue
				}
				k := strings.TrimSpace(line[:eq])
				v := strings.Trim(strings.TrimSpace(line[eq+1:]), `"'`)
				if os.Getenv(k) == "" {
					os.Setenv(k, os.ExpandEnv(v))
				}
			}
			f.Close()
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

func getPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	poolOnce.Do(func() {
		loadDotenv()
		url := os.Getenv("TEST_DATABASE_URL")
		if url == "" {
			poolErr = errors.New("TEST_DATABASE_URL not set")
			return
		}
		cfg, err := pgxpool.ParseConfig(url)
		if err != nil {
			poolErr = err
			return
		}
		cfg.MaxConns = 4
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		p, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			poolErr = err
			return
		}
		if err := p.Ping(ctx); err != nil {
			poolErr = err
			return
		}
		if err := db.Migrate(p); err != nil {
			poolErr = err
			return
		}
		testPool = p
	})
	if poolErr != nil {
		t.Skipf("test DB unavailable: %v", poolErr)
	}
	return testPool
}

// newTestRouter wires the whole application router exactly like
// cmd/server/main.go, minus the SPA static handler (StaticPath left empty)
// so unmatched routes fall through to chi's default 404.
func newTestRouter(t *testing.T) http.Handler {
	t.Helper()
	pool := getPool(t)
	dir := t.TempDir()
	stor := storage.NewService(dir)

	users := repository.NewUserRepository(pool, stor)
	files := repository.NewFileRepository(pool)
	syncRoots := repository.NewSyncRootRepository(pool, stor)
	conflicts := repository.NewConflictRepository(pool)
	trash := repository.NewTrashRepository(pool)
	uploads := repository.NewUploadRepository(pool)
	shares := repository.NewShareRepository(pool, stor)
	audits := audit.NewRepository(pool)

	hub := sse.NewHub()
	domainBus := bus.New(bus.Config{ReactiveWorkers: 1})
	q := quota.New(users, pool, nil, "http://test")
	q.Register(domainBus)
	t.Cleanup(domainBus.Close)

	outRep := outbox.NewRepository(pool)
	handlers := syncpkg.BuildHandlers(stor, hub, nil, users, outRep, "http://test")
	worker := outbox.NewWorker(outRep, handlers)
	worker.PollInterval = time.Hour
	coord := syncpkg.NewCoordinator(pool, files, syncRoots, conflicts, trash, outRep, worker, q, domainBus, stor)

	jobReg := jobs.NewRegistry(pool)
	setupState := &bootstrap.State{}
	var mailer *mail.Mailer

	cfg := &config.Config{
		Server: config.ServerConfig{
			// BaseURL feeds NewAuthHandler's webauthn RPID -- must be a real
			// domain string ("http://test" panics the WebAuthn config).
			BaseURL:     "http://localhost",
			CORSOrigins: []string{"http://test"},
		},
		Auth: config.AuthConfig{
			JWTSecret: "router-test-secret-at-least-32-bytes-long",
		},
		Storage: config.StorageConfig{
			DataPath:        dir,
			MaxUploadSizeMB: 100,
		},
	}

	rt := api.NewRouter(cfg, pool, users, files, syncRoots, conflicts, trash, uploads, shares,
		audits, stor, hub, setupState, mailer, coord, q, jobReg,
		assembler.New(assembler.Config{Run: func(assembler.Job) bool { return false }}))
	return rt.Handler()
}

func TestRouter_Healthz(t *testing.T) {
	h := newTestRouter(t)
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}
	if w.Body.String() != "ok" {
		t.Errorf("body = %q, want ok", w.Body.String())
	}
}

// A protected route reached without a bearer token must be rejected by the
// Authenticate middleware wired for the whole /api/v1 protected group.
func TestRouter_ProtectedRouteRequiresAuth(t *testing.T) {
	h := newTestRouter(t)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sync-roots", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401, body=%s", w.Code, w.Body.String())
	}
}

// The public setup-status route must be reachable without authentication.
func TestRouter_SetupStatusIsPublic(t *testing.T) {
	h := newTestRouter(t)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200, body=%s", w.Code, w.Body.String())
	}
}

// Unmatched paths 404 when no SPA static handler is configured.
func TestRouter_UnknownPathNotFound(t *testing.T) {
	h := newTestRouter(t)
	r := httptest.NewRequest(http.MethodGet, "/nope-does-not-exist", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", w.Code)
	}
}

// Every credential-guessing endpoint carries the login rate limit. /auth/2fa
// was the one exception, which let a caller holding the password grind TOTP
// codes as fast as the network allowed.
func TestRouter_TwoFactorEndpointIsRateLimited(t *testing.T) {
	h := newTestRouter(t)

	var lastCode int
	for i := 0; i < 12; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa",
			strings.NewReader(`{"session_token":"nope","totp_code":"000000"}`))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "203.0.113.99:40000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		lastCode = w.Code
		if lastCode == http.StatusTooManyRequests {
			return
		}
	}
	t.Fatalf("12 requests all got through (last status %d), want 429 — /auth/2fa is unmetered", lastCode)
}
