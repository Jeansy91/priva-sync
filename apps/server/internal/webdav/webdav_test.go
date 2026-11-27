package webdav_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/auth"
	"privasync/server/internal/bus"
	"privasync/server/internal/config"
	"privasync/server/internal/db"
	"privasync/server/internal/model"
	"privasync/server/internal/outbox"
	"privasync/server/internal/quota"
	"privasync/server/internal/repository"
	"privasync/server/internal/sse"
	"privasync/server/internal/storage"
	syncpkg "privasync/server/internal/sync"
	"privasync/server/internal/webdav"
)

// ─── Test harness ──────────────────────────────────────────────────────────
//
// Mirrors the sync-package rig: real Postgres, real Storage on tmpfs, a
// coordinator wired exactly as production. The outbox worker is driven by
// hand via drain() so writes settle deterministically.

var (
	poolOnce sync.Once
	pool     *pgxpool.Pool
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
		pool = p
	})
	if poolErr != nil {
		t.Skipf("test DB unavailable: %v", poolErr)
	}
	return pool
}

// The account password. Not a WebDAV credential any more (ADR-0040) --
// makeUser still sets one so the tests can assert it is refused.
const testPassword = "webdav-secret"

type rig struct {
	pool         *pgxpool.Pool
	users        *repository.UserRepository
	appPasswords *repository.AppPasswordRepository

	// davSecrets caches one app-password secret per user, so repeated
	// requests reuse the credential instead of minting a new one each time.
	davSecrets map[uuid.UUID]string

	syncRoots *repository.SyncRootRepository
	files     *repository.FileRepository
	stor      *storage.Service
	outRep    *outbox.Repository
	hub       *sse.Hub
	coord     *syncpkg.Impl
	authCfg   config.AuthConfig
	handler   *webdav.Handler
	dir       string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	p := getPool(t)
	dir := t.TempDir()
	stor := storage.NewService(dir)
	files := repository.NewFileRepository(p)
	syncRoots := repository.NewSyncRootRepository(p, stor)
	conflicts := repository.NewConflictRepository(p)
	trash := repository.NewTrashRepository(p)
	users := repository.NewUserRepository(p, stor)
	q := quota.New(users, p, nil, "http://test")
	b := bus.New(bus.Config{ReactiveWorkers: 1})
	q.Register(b)
	hub := sse.NewHub()
	outRep := outbox.NewRepository(p)
	handlers := syncpkg.BuildHandlers(stor, hub, nil, users, outRep, "http://test")
	worker := outbox.NewWorker(outRep, handlers)
	worker.PollInterval = time.Hour
	coord := syncpkg.NewCoordinator(p, files, syncRoots, conflicts, trash, outRep, worker, q, b, stor)
	if _, err := p.Exec(context.Background(), `DELETE FROM outbox`); err != nil {
		t.Fatalf("wipe outbox: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = p.Exec(ctx, `DELETE FROM outbox`)
		b.Close()
	})

	authCfg := config.AuthConfig{JWTSecret: "test-secret"}
	appPasswords := repository.NewAppPasswordRepository(p)
	h := webdav.NewHandler(users, appPasswords, syncRoots, files, stor, coord, authCfg)

	return &rig{
		pool: p, users: users, appPasswords: appPasswords,
		davSecrets: map[uuid.UUID]string{},
		syncRoots:  syncRoots, files: files,
		stor: stor, outRep: outRep, hub: hub, coord: coord,
		authCfg: authCfg, handler: h, dir: dir,
	}
}

func (r *rig) makeUser(t *testing.T) *model.User {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	u, err := r.users.Create(context.Background(), "dav_"+suffix, "dav_"+suffix+"@test.local", hash, model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = r.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID)
	})
	return u
}

func (r *rig) makeRoot(t *testing.T, userID uuid.UUID, name string) *model.SyncRoot {
	t.Helper()
	sr, err := r.syncRoots.CreateSyncRoot(context.Background(), userID, name, "/"+name)
	if err != nil {
		t.Fatalf("create sync root %q: %v", name, err)
	}
	return sr
}

// putFile uploads a file through the Sync Coordinator exactly like the REST
// upload adapter does (staging write → UploadFile → drain the rename), so the
// `files` row and on-disk bytes match production state.
func (r *rig) putFile(t *testing.T, u *model.User, root *model.SyncRoot, p string, body []byte) {
	t.Helper()
	staging := p + ".staging-" + uuid.NewString()
	full := filepath.Join(r.dir, u.ID.String(), root.ID.String(), staging)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	if err := os.WriteFile(full, body, 0o644); err != nil {
		t.Fatalf("write staging: %v", err)
	}
	if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID:      u.ID,
		SyncRootID:  root.ID,
		Path:        p,
		StagingPath: staging,
		Size:        int64(len(body)),
		ContentHash: []byte{0x01},
		MTime:       time.Now(),
	}); err != nil {
		t.Fatalf("UploadFile %q: %v", p, err)
	}
	r.drainOutbox(t)
}

func (r *rig) drainOutbox(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	handlers := syncpkg.BuildHandlers(r.stor, r.hub, nil, r.users, outbox.NewRepository(pool), "http://test")
	for i := 0; i < 50; i++ {
		e, err := r.outRep.ClaimNext(ctx)
		if errors.Is(err, outbox.ErrNotFound) {
			return
		}
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		h, ok := handlers[e.Kind]
		if !ok {
			t.Fatalf("no handler for kind %q", e.Kind)
		}
		if err := h(ctx, e.Payload); err != nil {
			t.Fatalf("handler %q: %v", e.Kind, err)
		}
		if err := r.outRep.MarkDone(ctx, e.ID); err != nil {
			t.Fatalf("mark done: %v", err)
		}
	}
	t.Fatalf("outbox did not drain in 50 iterations")
}

// davSecret returns the user's WebDAV credential, minting one on first use.
func (r *rig) davSecret(t *testing.T, u *model.User) string {
	t.Helper()
	if s, ok := r.davSecrets[u.ID]; ok {
		return s
	}
	_, secret, err := r.appPasswords.Create(context.Background(), u.ID, "webdav-test")
	if err != nil {
		t.Fatalf("create app password: %v", err)
	}
	r.davSecrets[u.ID] = secret
	return secret
}

// do issues a WebDAV request authenticated as the given user via Basic Auth
// with an app password — the account password is not a WebDAV credential
// (ADR-0040). One credential per user is minted on first use and reused, so
// the tests exercise the same path a mounted client takes.
func (r *rig) do(t *testing.T, u *model.User, method, target string, body io.Reader, depth string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	req.SetBasicAuth(u.Username, r.davSecret(t, u))
	if depth != "" {
		req.Header.Set("Depth", depth)
	}
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)
	return rec
}

// ─── Slice 1: top-level PROPFIND lists sync roots by name ────────────────────

func TestPropfindRoot_ListsSyncRootsByName(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	r.makeRoot(t, u.ID, "Photos")
	r.makeRoot(t, u.ID, "Documents")

	rec := r.do(t, u, "PROPFIND", "/", nil, "1")

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207 Multi-Status, got %d\nbody: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, name := range []string{"Photos", "Documents"} {
		if !strings.Contains(body, name) {
			t.Errorf("PROPFIND / body missing sync root %q\nbody: %s", name, body)
		}
	}
	// The internal sync-root UUID must NOT leak into the namespace.
	if strings.Contains(body, u.ID.String()) {
		t.Errorf("PROPFIND / leaked user UUID into listing")
	}
}

// ─── Slice 2: GET returns file bytes ─────────────────────────────────────────

func TestGet_ReturnsFileBytes(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID, "Docs")
	want := []byte("hello webdav world")
	r.putFile(t, u, root, "/hello.txt", want)

	rec := r.do(t, u, "GET", "/Docs/hello.txt", nil, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\nbody: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != string(want) {
		t.Errorf("GET body = %q, want %q", rec.Body.String(), want)
	}
}

func TestPropfindFile_ReportsSize(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID, "Docs")
	want := []byte("twelve bytes")
	r.putFile(t, u, root, "/note.txt", want)

	rec := r.do(t, u, "PROPFIND", "/Docs/note.txt", nil, "0")

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d\nbody: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "note.txt") {
		t.Errorf("PROPFIND missing file name\nbody: %s", body)
	}
	if !strings.Contains(body, "<D:getcontentlength>12</D:getcontentlength>") {
		t.Errorf("PROPFIND missing/incorrect content length\nbody: %s", body)
	}
}

// ─── Slice 3: PROPFIND lists a sub-directory one level deep ──────────────────

func TestPropfindSubdir_ListsChildren(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID, "Docs")
	// "/a" is an implicit directory: only deeper file rows establish it.
	r.putFile(t, u, root, "/a/file2.txt", []byte("two"))
	r.putFile(t, u, root, "/a/b/file1.txt", []byte("one"))

	rec := r.do(t, u, "PROPFIND", "/Docs/a", nil, "1")

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d\nbody: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Immediate children of /a are the file "file2.txt" and the sub-dir "b".
	if !strings.Contains(body, "/Docs/a/file2.txt") {
		t.Errorf("missing immediate file child\nbody: %s", body)
	}
	if !strings.Contains(body, "/Docs/a/b/") {
		t.Errorf("missing immediate sub-dir child\nbody: %s", body)
	}
	// The deeper file must NOT appear at depth 1.
	if strings.Contains(body, "file1.txt") {
		t.Errorf("depth-1 listing leaked a grandchild\nbody: %s", body)
	}
}

// ─── Slice 4: PUT a new file flows through the Sync Coordinator ───────────────

func TestPut_NewFile_IsFirstClassMutation(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID, "Docs")
	payload := []byte("uploaded over webdav")

	rec := r.do(t, u, "PUT", "/Docs/report.txt", bytes.NewReader(payload), "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d\nbody: %s", rec.Code, rec.Body.String())
	}
	r.drainOutbox(t)

	// A real `files` row exists (not a ghost file): visible to the rest of the
	// system, with the correct size and hash.
	f, err := r.files.FindByPath(context.Background(), root.ID, "/report.txt")
	if err != nil {
		t.Fatalf("file row missing after PUT: %v", err)
	}
	if f.Size != int64(len(payload)) {
		t.Errorf("file row size = %d, want %d", f.Size, len(payload))
	}

	// Quota was charged — proof the write was gated, not a raw FS write.
	u2, err := r.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if u2.UsedBytes != int64(len(payload)) {
		t.Errorf("UsedBytes = %d, want %d (quota not charged)", u2.UsedBytes, len(payload))
	}

	// And it reads back over WebDAV.
	get := r.do(t, u, "GET", "/Docs/report.txt", nil, "")
	if get.Code != http.StatusOK || get.Body.String() != string(payload) {
		t.Errorf("GET after PUT = %d %q, want 200 %q", get.Code, get.Body.String(), payload)
	}
}

// ─── Slice 5: PUT over an existing file overwrites, no loser copy ─────────────

func TestPut_Overwrite_NoConflictCopy(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID, "Docs")

	first := r.do(t, u, "PUT", "/Docs/a.txt", bytes.NewReader([]byte("v1")), "")
	if first.Code != http.StatusCreated {
		t.Fatalf("first PUT = %d", first.Code)
	}
	r.drainOutbox(t)

	second := r.do(t, u, "PUT", "/Docs/a.txt", bytes.NewReader([]byte("version two")), "")
	if second.Code != http.StatusCreated {
		t.Fatalf("overwrite PUT = %d\nbody: %s", second.Code, second.Body.String())
	}
	r.drainOutbox(t)

	// Content is the new version.
	get := r.do(t, u, "GET", "/Docs/a.txt", nil, "")
	if get.Body.String() != "version two" {
		t.Errorf("GET = %q, want %q", get.Body.String(), "version two")
	}

	// No loser copy was materialised: exactly one entry under the root.
	entries, err := r.files.ListDirectoryLevel(context.Background(), repository.ListDirectoryLevelParams{
		SyncRootID: root.ID, PathPrefix: "/", Limit: 100,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 || path.Base(entries[0].Path) != "a.txt" {
		var names []string
		for _, e := range entries {
			names = append(names, path.Base(e.Path))
		}
		t.Errorf("expected only [a.txt], got %v", names)
	}

	// Quota reflects only the live file, not v1 + v2 + a conflict copy.
	u2, _ := r.users.FindByID(context.Background(), u.ID)
	if u2.UsedBytes != int64(len("version two")) {
		t.Errorf("UsedBytes = %d, want %d", u2.UsedBytes, len("version two"))
	}
}

// ─── Slice 6: MKCOL creates a directory ──────────────────────────────────────

func TestMkcol_CreatesDirectory(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID, "Docs")

	rec := r.do(t, u, "MKCOL", "/Docs/newdir", nil, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("MKCOL = %d\nbody: %s", rec.Code, rec.Body.String())
	}

	f, err := r.files.FindByPath(context.Background(), root.ID, "/newdir")
	if err != nil {
		t.Fatalf("directory row missing: %v", err)
	}
	if !f.IsDir {
		t.Errorf("created row is not a directory")
	}

	// It shows up in the parent listing.
	list := r.do(t, u, "PROPFIND", "/Docs", nil, "1")
	if !strings.Contains(list.Body.String(), "/Docs/newdir/") {
		t.Errorf("new dir missing from listing\nbody: %s", list.Body.String())
	}
}

// ─── Slice 7: DELETE routes through the coordinator ──────────────────────────

func TestDelete_File_SoftDeletesAndReleasesQuota(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID, "Docs")
	r.putFile(t, u, root, "/gone.txt", []byte("bye"))

	rec := r.do(t, u, "DELETE", "/Docs/gone.txt", nil, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d\nbody: %s", rec.Code, rec.Body.String())
	}
	r.drainOutbox(t)

	if _, err := r.files.FindByPath(context.Background(), root.ID, "/gone.txt"); err == nil {
		t.Errorf("file row still live after DELETE")
	}
	get := r.do(t, u, "GET", "/Docs/gone.txt", nil, "")
	if get.Code != http.StatusNotFound {
		t.Errorf("GET after DELETE = %d, want 404", get.Code)
	}
}

func TestDelete_Directory_CascadeTrashesSubtree(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID, "Docs")
	r.putFile(t, u, root, "/dir/a.txt", []byte("a"))
	r.putFile(t, u, root, "/dir/sub/b.txt", []byte("b"))

	rec := r.do(t, u, "DELETE", "/Docs/dir", nil, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE dir = %d\nbody: %s", rec.Code, rec.Body.String())
	}
	r.drainOutbox(t)

	for _, p := range []string{"/dir/a.txt", "/dir/sub/b.txt"} {
		if _, err := r.files.FindByPath(context.Background(), root.ID, p); err == nil {
			t.Errorf("%s still live after subtree DELETE", p)
		}
	}
}

// ─── Slice 8: MOVE routes through the coordinator ────────────────────────────

func (r *rig) doMove(t *testing.T, u *model.User, from, to string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("MOVE", from, nil)
	req.SetBasicAuth(u.Username, r.davSecret(t, u))
	req.Header.Set("Destination", to)
	req.Header.Set("Overwrite", "T")
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)
	return rec
}

// ─── Slice 11: content-hash ETag ─────────────────────────────────────────────

func TestGet_ContentHashETag(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID, "Docs")

	put := r.do(t, u, "PUT", "/Docs/f.txt", bytes.NewReader([]byte("abc")), "")
	if put.Code != http.StatusCreated {
		t.Fatalf("PUT = %d", put.Code)
	}
	r.drainOutbox(t)

	row, err := r.files.FindByPath(context.Background(), root.ID, "/f.txt")
	if err != nil {
		t.Fatalf("row missing: %v", err)
	}
	want := `"` + hex.EncodeToString(row.ContentHash) + `"`

	get := r.do(t, u, "GET", "/Docs/f.txt", nil, "")
	if got := get.Header().Get("ETag"); got != want {
		t.Errorf("GET ETag = %q, want content-hash %q", got, want)
	}

	// PUT echoes the same content-hash ETag in its response.
	if got := put.Header().Get("ETag"); got != want {
		t.Errorf("PUT ETag = %q, want %q", got, want)
	}
}

// ─── Slices 12+13: RFC 7232 conditional requests ─────────────────────────────

func (r *rig) doWithHeaders(t *testing.T, u *model.User, method, target string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	req.SetBasicAuth(u.Username, r.davSecret(t, u))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)
	return rec
}

func TestPut_IfNoneMatchStar_RefusesOverwrite(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID, "Docs")

	// On a fresh path "*" allows the create.
	created := r.doWithHeaders(t, u, "PUT", "/Docs/x.txt", bytes.NewReader([]byte("v1")),
		map[string]string{"If-None-Match": "*"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create-only on new = %d, want 201", created.Code)
	}
	r.drainOutbox(t)

	// On an existing path "*" must fail with 412 and leave the content intact.
	blocked := r.doWithHeaders(t, u, "PUT", "/Docs/x.txt", bytes.NewReader([]byte("v2")),
		map[string]string{"If-None-Match": "*"})
	if blocked.Code != http.StatusPreconditionFailed {
		t.Fatalf("create-only on existing = %d, want 412", blocked.Code)
	}
	get := r.do(t, u, "GET", "/Docs/x.txt", nil, "")
	if get.Body.String() != "v1" {
		t.Errorf("content changed despite 412: %q", get.Body.String())
	}
	_ = root
}

func TestPut_IfMatch_StaleEtagRejected(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID, "Docs")
	r.putFile(t, u, root, "/y.txt", []byte("original"))

	// Stale ETag → 412, content unchanged.
	stale := r.doWithHeaders(t, u, "PUT", "/Docs/y.txt", bytes.NewReader([]byte("new")),
		map[string]string{"If-Match": `"deadbeef"`})
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match = %d, want 412", stale.Code)
	}
	if g := r.do(t, u, "GET", "/Docs/y.txt", nil, ""); g.Body.String() != "original" {
		t.Errorf("content changed on stale If-Match: %q", g.Body.String())
	}

	// Correct ETag → overwrite succeeds.
	row, _ := r.files.FindByPath(context.Background(), root.ID, "/y.txt")
	current := `"` + hex.EncodeToString(row.ContentHash) + `"`
	ok := r.doWithHeaders(t, u, "PUT", "/Docs/y.txt", bytes.NewReader([]byte("fresh")),
		map[string]string{"If-Match": current})
	if ok.Code != http.StatusCreated {
		t.Fatalf("matching If-Match = %d, want 201", ok.Code)
	}
	r.drainOutbox(t)
	if g := r.do(t, u, "GET", "/Docs/y.txt", nil, ""); g.Body.String() != "fresh" {
		t.Errorf("overwrite failed: %q", g.Body.String())
	}
}

func TestDelete_IfMatch_StaleRejected(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID, "Docs")
	r.putFile(t, u, root, "/z.txt", []byte("keep"))

	stale := r.doWithHeaders(t, u, "DELETE", "/Docs/z.txt", nil,
		map[string]string{"If-Match": `"nope"`})
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale DELETE If-Match = %d, want 412", stale.Code)
	}
	if _, err := r.files.FindByPath(context.Background(), root.ID, "/z.txt"); err != nil {
		t.Errorf("file deleted despite 412: %v", err)
	}
}

func (r *rig) doCopy(t *testing.T, u *model.User, from, to, depth string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("COPY", from, nil)
	req.SetBasicAuth(u.Username, r.davSecret(t, u))
	req.Header.Set("Destination", to)
	req.Header.Set("Overwrite", "T")
	if depth != "" {
		req.Header.Set("Depth", depth)
	}
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)
	return rec
}

func TestCopy_File_DuplicatesThroughCoordinator(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID, "Docs")
	r.putFile(t, u, root, "/orig.txt", []byte("source bytes"))

	rec := r.doCopy(t, u, "/Docs/orig.txt", "/Docs/copy.txt", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("COPY = %d\nbody: %s", rec.Code, rec.Body.String())
	}
	r.drainOutbox(t)

	// Both are real, independent file rows (source preserved).
	if _, err := r.files.FindByPath(context.Background(), root.ID, "/orig.txt"); err != nil {
		t.Errorf("source lost after COPY: %v", err)
	}
	cp, err := r.files.FindByPath(context.Background(), root.ID, "/copy.txt")
	if err != nil {
		t.Fatalf("copy row missing: %v", err)
	}
	if cp.Size != int64(len("source bytes")) {
		t.Errorf("copy size = %d, want %d", cp.Size, len("source bytes"))
	}
	get := r.do(t, u, "GET", "/Docs/copy.txt", nil, "")
	if get.Body.String() != "source bytes" {
		t.Errorf("GET copy = %q", get.Body.String())
	}
}

func TestCopy_Directory_DuplicatesSubtree(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID, "Docs")
	r.putFile(t, u, root, "/src/a.txt", []byte("a"))
	r.putFile(t, u, root, "/src/sub/b.txt", []byte("b"))

	rec := r.doCopy(t, u, "/Docs/src", "/Docs/dst", "infinity")
	if rec.Code != http.StatusCreated {
		t.Fatalf("COPY dir = %d\nbody: %s", rec.Code, rec.Body.String())
	}
	r.drainOutbox(t)

	for _, p := range []string{"/dst/a.txt", "/dst/sub/b.txt"} {
		if _, err := r.files.FindByPath(context.Background(), root.ID, p); err != nil {
			t.Errorf("copied file %s missing: %v", p, err)
		}
	}
	// Source subtree is preserved.
	if _, err := r.files.FindByPath(context.Background(), root.ID, "/src/a.txt"); err != nil {
		t.Errorf("source file lost after dir COPY: %v", err)
	}
}

// ─── Slice 14: cross-root MOVE (copy + delete) ───────────────────────────────

func TestMove_CrossRoot_File(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	a := r.makeRoot(t, u.ID, "A")
	b := r.makeRoot(t, u.ID, "B")
	r.putFile(t, u, a, "/doc.txt", []byte("cross root data"))

	rec := r.doMove(t, u, "/A/doc.txt", "/B/doc.txt")
	if rec.Code != http.StatusCreated {
		t.Fatalf("cross-root MOVE = %d\nbody: %s", rec.Code, rec.Body.String())
	}
	r.drainOutbox(t)

	if _, err := r.files.FindByPath(context.Background(), a.ID, "/doc.txt"); err == nil {
		t.Errorf("source still live in root A after cross-root MOVE")
	}
	if _, err := r.files.FindByPath(context.Background(), b.ID, "/doc.txt"); err != nil {
		t.Errorf("destination missing in root B: %v", err)
	}
	get := r.do(t, u, "GET", "/B/doc.txt", nil, "")
	if get.Body.String() != "cross root data" {
		t.Errorf("GET B/doc.txt = %q", get.Body.String())
	}
}

func TestMove_CrossRoot_Directory(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	a := r.makeRoot(t, u.ID, "A")
	b := r.makeRoot(t, u.ID, "B")
	r.putFile(t, u, a, "/proj/main.go", []byte("package main"))
	r.putFile(t, u, a, "/proj/sub/util.go", []byte("package sub"))

	rec := r.doMove(t, u, "/A/proj", "/B/proj")
	if rec.Code != http.StatusCreated {
		t.Fatalf("cross-root dir MOVE = %d\nbody: %s", rec.Code, rec.Body.String())
	}
	r.drainOutbox(t)

	for _, p := range []string{"/proj/main.go", "/proj/sub/util.go"} {
		if _, err := r.files.FindByPath(context.Background(), b.ID, p); err != nil {
			t.Errorf("dest %s missing in root B: %v", p, err)
		}
		if _, err := r.files.FindByPath(context.Background(), a.ID, p); err == nil {
			t.Errorf("source %s still live in root A", p)
		}
	}
}

func TestMove_RenamesFile(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID, "Docs")
	r.putFile(t, u, root, "/old.txt", []byte("payload"))

	rec := r.doMove(t, u, "/Docs/old.txt", "/Docs/new.txt")
	if rec.Code != http.StatusCreated {
		t.Fatalf("MOVE = %d\nbody: %s", rec.Code, rec.Body.String())
	}
	r.drainOutbox(t)

	if _, err := r.files.FindByPath(context.Background(), root.ID, "/old.txt"); err == nil {
		t.Errorf("source still live after MOVE")
	}
	if _, err := r.files.FindByPath(context.Background(), root.ID, "/new.txt"); err != nil {
		t.Errorf("destination missing after MOVE: %v", err)
	}
	get := r.do(t, u, "GET", "/Docs/new.txt", nil, "")
	if get.Body.String() != "payload" {
		t.Errorf("GET new = %q, want payload", get.Body.String())
	}
}

// ─── Slice 9: user isolation + authentication ────────────────────────────────

func TestIsolation_CannotReachOtherUsersTree(t *testing.T) {
	r := newRig(t)
	alice := r.makeUser(t)
	bob := r.makeUser(t)
	bobRoot := r.makeRoot(t, bob.ID, "Bob")
	r.putFile(t, bob, bobRoot, "/secret.txt", []byte("top secret"))

	// Alice lists her own (empty) namespace — Bob's root is absent.
	list := r.do(t, alice, "PROPFIND", "/", nil, "1")
	if strings.Contains(list.Body.String(), "Bob") {
		t.Errorf("Alice's listing leaked Bob's root\nbody: %s", list.Body.String())
	}

	// Alice cannot read Bob's file by name.
	get := r.do(t, alice, "GET", "/Bob/secret.txt", nil, "")
	if get.Code != http.StatusNotFound {
		t.Errorf("Alice GET Bob's file = %d, want 404", get.Code)
	}
	if strings.Contains(get.Body.String(), "top secret") {
		t.Errorf("Alice read Bob's bytes")
	}
}

func TestTraversal_CannotEscapeNamespace(t *testing.T) {
	r := newRig(t)
	alice := r.makeUser(t)
	bob := r.makeUser(t)
	bobRoot := r.makeRoot(t, bob.ID, "Bob")
	r.putFile(t, bob, bobRoot, "/secret.txt", []byte("top secret"))
	r.makeRoot(t, alice.ID, "Alice")

	// A "../" climb is collapsed by path cleaning; it can never reach Bob.
	get := r.do(t, alice, "GET", "/Alice/../Bob/secret.txt", nil, "")
	if strings.Contains(get.Body.String(), "top secret") {
		t.Errorf("traversal reached another user's data\nbody: %s", get.Body.String())
	}
}

func TestAuth_Rejections(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)

	// No credentials.
	req := httptest.NewRequest("PROPFIND", "/", nil)
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no creds = %d, want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("missing WWW-Authenticate challenge")
	}

	// Wrong password.
	req = httptest.NewRequest("PROPFIND", "/", nil)
	req.SetBasicAuth(u.Username, "wrong-password")
	rec = httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password = %d, want 401", rec.Code)
	}
}

func TestAuth_BearerTokenAccepted(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	r.makeRoot(t, u.ID, "Docs")

	token, err := auth.IssueAccessToken(r.authCfg.JWTSecret, time.Hour, u.ID, model.RoleUser)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	req := httptest.NewRequest("PROPFIND", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Depth", "1")
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("bearer PROPFIND = %d, want 207\nbody: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Docs") {
		t.Errorf("bearer listing missing root\nbody: %s", rec.Body.String())
	}
}
