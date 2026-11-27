package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/assembler"
	"privasync/server/internal/audit"
	"privasync/server/internal/auth"
	"privasync/server/internal/bus"
	"privasync/server/internal/db"
	"privasync/server/internal/model"
	"privasync/server/internal/officedoc"
	"privasync/server/internal/outbox"
	"privasync/server/internal/quota"
	"privasync/server/internal/repository"
	"privasync/server/internal/sse"
	"privasync/server/internal/storage"
	syncpkg "privasync/server/internal/sync"
)

var (
	testPoolOnce sync.Once
	testPool     *pgxpool.Pool
	testPoolErr  error
)

// loadDotenv liest TEST_DATABASE_URL aus .env in Repo-Root (max. 5 Ebenen hoch),
// damit `go test` ohne externes dotenv-Tool funktioniert.
func loadDotenv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
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
				v := strings.TrimSpace(line[eq+1:])
				v = strings.Trim(v, `"'`)
				v = os.ExpandEnv(v)
				if os.Getenv(k) == "" {
					os.Setenv(k, v)
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

func getTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	testPoolOnce.Do(func() {
		loadDotenv()
		url := os.Getenv("TEST_DATABASE_URL")
		if url == "" {
			testPoolErr = errors.New("TEST_DATABASE_URL not set")
			return
		}
		cfg, err := pgxpool.ParseConfig(url)
		if err != nil {
			testPoolErr = err
			return
		}
		cfg.MaxConns = 4
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			testPoolErr = err
			return
		}
		if err := pool.Ping(ctx); err != nil {
			testPoolErr = err
			return
		}
		if err := db.Migrate(pool); err != nil {
			testPoolErr = err
			return
		}
		testPool = pool
	})
	if testPoolErr != nil {
		t.Skipf("test DB unavailable: %v", testPoolErr)
	}
	return testPool
}

// inlineQueue assembles on the spot, in the caller's goroutine. Handler tests
// are then free of any waiting: by the time complete has answered 202, the
// worker's verdict is already on the session row.
type inlineQueue struct{ svc *assembler.Service }

func (q inlineQueue) Submit(job assembler.Job) bool {
	for attempt := 0; attempt < 3 && q.svc.Assemble(job); attempt++ {
	}
	return true
}

type testEnv struct {
	handler   *FileHandler
	shareH    *ShareHandler
	deltaH    *DeltaHandler
	uploadH   *UploadHandler
	pool      *pgxpool.Pool
	hub       *sse.Hub
	storDir   string
	files     *repository.FileRepository
	syncRoots *repository.SyncRootRepository
	shares    *repository.ShareRepository
	conflicts *repository.ConflictRepository
	trash     *repository.TrashRepository
	users     *repository.UserRepository
	uploads   *repository.UploadRepository
	storage   *storage.Service
	outRep    *outbox.Repository
	handlers  map[outbox.Kind]outbox.Handler
	audits    *audit.Repository
}

// newTestEnv wires a fresh, isolated handler set backed by the shared test
// pool. officeCfg is optional and defaults to officedoc.Config{} (feature
// disabled) — pass one to exercise the ADR-0027 office-preview endpoints
// against an "enabled" document-server config.
func newTestEnv(t *testing.T, officeCfg ...officedoc.Config) *testEnv {
	t.Helper()
	cfg := officedoc.Config{}
	if len(officeCfg) > 0 {
		cfg = officeCfg[0]
	}
	pool := getTestPool(t)
	dir := t.TempDir()
	stor := storage.NewService(dir)
	files := repository.NewFileRepository(pool)
	syncRoots := repository.NewSyncRootRepository(pool, stor)
	conflicts := repository.NewConflictRepository(pool)
	trash := repository.NewTrashRepository(pool)
	users := repository.NewUserRepository(pool, stor)
	shares := repository.NewShareRepository(pool, stor)
	audits := audit.NewRepository(pool)
	q := quota.New(users, pool, nil, "http://test")
	domainBus := bus.New(bus.Config{ReactiveWorkers: 1})
	q.Register(domainBus)
	t.Cleanup(domainBus.Close)
	hub := sse.NewHub()
	outRep := outbox.NewRepository(pool)
	handlers := syncpkg.BuildHandlers(stor, hub, nil, users, outRep, "http://test")
	worker := outbox.NewWorker(outRep, handlers)
	worker.PollInterval = time.Hour // tests drain by hand
	coord := syncpkg.NewCoordinator(pool, files, syncRoots, conflicts, trash, outRep, worker, q, domainBus, stor)
	h := NewFileHandler(files, syncRoots, conflicts, trash, users, shares, audits, stor, nil, hub, q, coord, "http://test", 100, cfg)
	sh := NewShareHandler(shares, files, syncRoots, users, conflicts, audits, stor, nil, hub, q, coord, "http://test", 100, cfg)
	dh := NewDeltaHandler(files, syncRoots, stor, coord)
	uploads := repository.NewUploadRepository(pool)
	uh := NewUploadHandler(syncRoots, uploads, stor, q, inlineQueue{
		svc: assembler.NewService(uploads, stor, coord, assembler.Options{
			MaxAttempts: 3, Retain: time.Hour, Timeout: time.Minute,
		}),
	})
	// Wipe any leftover outbox entries from prior tests so manual drainOutbox
	// only processes work produced by the current test.
	if _, err := pool.Exec(context.Background(), `DELETE FROM outbox`); err != nil {
		t.Fatalf("wipe outbox: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM outbox`)
	})
	return &testEnv{
		handler: h, shareH: sh, deltaH: dh, uploadH: uh, pool: pool, hub: hub, storDir: dir,
		files: files, syncRoots: syncRoots, shares: shares, conflicts: conflicts, trash: trash, users: users, uploads: uploads, storage: stor,
		outRep: outRep, handlers: handlers, audits: audits,
	}
}

// drainOutbox claims every runnable entry and runs its handler. Called by
// tests that exercise coordinator-routed flows (e.g. MoveFile) so the SSE
// publish actually fires inside the test deadline.
func (env *testEnv) drainOutbox(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		e, err := env.outRep.ClaimNext(ctx)
		if errors.Is(err, outbox.ErrNotFound) {
			return
		}
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		h, ok := env.handlers[e.Kind]
		if !ok {
			t.Fatalf("no handler for kind %q", e.Kind)
		}
		if err := h(ctx, e.Payload); err != nil {
			t.Fatalf("handler %q: %v", e.Kind, err)
		}
		if err := env.outRep.MarkDone(ctx, e.ID); err != nil {
			t.Fatalf("mark done: %v", err)
		}
	}
}

func createTestUser(t *testing.T, env *testEnv) *auth.Claims {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	u := seedUser(t, env, "test_"+suffix, "test_"+suffix+"@test.local")
	return &auth.Claims{UserID: u.ID, Role: u.Role, TokenType: auth.TokenTypeAccess}
}

// seedUser creates a user and registers a cleanup that deletes it at test end.
// ON DELETE CASCADE on sync_roots/files/shares/audit_logs sweeps the rest.
// All tests that insert into `users` must go through this helper so the
// table stays clean across test runs.
func seedUser(t *testing.T, env *testEnv, username, email string) *model.User {
	t.Helper()
	u, err := env.users.Create(context.Background(), username, email, "x", model.RoleUser)
	if err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	registerUserCleanup(t, env.pool, u.ID)
	return u
}

func registerUserCleanup(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
			t.Logf("cleanup user %s: %v", id, err)
		}
	})
}

func createTestSyncRoot(t *testing.T, env *testEnv, userID uuid.UUID, name string) *model.SyncRoot {
	t.Helper()
	sr, err := env.syncRoots.CreateSyncRoot(context.Background(), userID, name, "/"+name)
	if err != nil {
		t.Fatalf("create sync root: %v", err)
	}
	return sr
}

func reqWithClaims(method, target string, body io.Reader, claims *auth.Claims) *http.Request {
	r := httptest.NewRequest(method, target, body)
	ctx := context.WithValue(r.Context(), middleware.ContextKeyClaims, claims)
	return r.WithContext(ctx)
}

func withURLParams(r *http.Request, kv ...string) *http.Request {
	rctx := chi.NewRouteContext()
	for i := 0; i+1 < len(kv); i += 2 {
		rctx.URLParams.Add(kv[i], kv[i+1])
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// flushRecorder is an http.ResponseWriter + Flusher for SSE tests.
// Each Flush ships the buffered bytes as a chunk into the chunks channel.
type flushRecorder struct {
	mu     sync.Mutex
	header http.Header
	buf    bytes.Buffer
	chunks chan []byte
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{header: http.Header{}, chunks: make(chan []byte, 64)}
}

func (f *flushRecorder) Header() http.Header { return f.header }
func (f *flushRecorder) WriteHeader(int)     {}
func (f *flushRecorder) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buf.Write(p)
	return len(p), nil
}
func (f *flushRecorder) Flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.buf.Len() == 0 {
		return
	}
	b := make([]byte, f.buf.Len())
	copy(b, f.buf.Bytes())
	f.buf.Reset()
	select {
	case f.chunks <- b:
	default:
	}
}

// captureEvents abonniert userID am Hub im Hintergrund und liefert
// einen Channel mit dekodierten Events. cleanup() beendet den Subscriber.
// parsedEvent is the test-side decoded SSE message: the SSE `event:` line and
// the parsed JSON envelope body. The hub itself only sees typed events.Payload
// values; this struct exists so assertions can read both the event name and
// the envelope payload without importing the sse package's internals.
type parsedEvent struct {
	Type string
	Data any
}

func captureEvents(t *testing.T, hub *sse.Hub, userID uuid.UUID) (<-chan parsedEvent, func()) {
	t.Helper()
	fr := newFlushRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/events", nil).WithContext(ctx)
	subDone := make(chan struct{})
	go func() {
		hub.Subscribe(fr, req, userID)
		close(subDone)
	}()
	// Erstes ": connected"-Chunk abwarten, damit Subscribe sicher registriert ist.
	select {
	case <-fr.chunks:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("hub.Subscribe: kein initialer keep-alive")
	}
	events := make(chan parsedEvent, 32)
	parserDone := make(chan struct{})
	go func() {
		defer close(events)
		for {
			select {
			case <-parserDone:
				return
			case chunk, ok := <-fr.chunks:
				if !ok {
					return
				}
				ev, parsed := parseSSEChunk(chunk)
				if parsed {
					events <- ev
				}
			}
		}
	}()
	cleanup := func() {
		cancel()
		<-subDone
		close(parserDone)
	}
	return events, cleanup
}

func parseSSEChunk(chunk []byte) (parsedEvent, bool) {
	var typ, data string
	for _, line := range strings.Split(string(chunk), "\n") {
		switch {
		case strings.HasPrefix(line, "event: "):
			typ = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
	if typ == "" {
		return parsedEvent{}, false
	}
	var payload any
	_ = json.Unmarshal([]byte(data), &payload)
	return parsedEvent{Type: typ, Data: payload}, true
}

// waitForEvent blockiert bis ein Event mit passendem Type kommt oder Timeout.
func waitForEvent(t *testing.T, ch <-chan parsedEvent, typ string, timeout time.Duration) parsedEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("event channel closed before %q arrived", typ)
			}
			if ev.Type == typ {
				return ev
			}
		case <-deadline:
			t.Fatalf("timeout waiting for event %q", typ)
		}
	}
}
