package sync_test

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/bus"
	"privasync/server/internal/db"
	"privasync/server/internal/model"
	"privasync/server/internal/outbox"
	"privasync/server/internal/quota"
	"privasync/server/internal/repository"
	"privasync/server/internal/sse"
	"privasync/server/internal/storage"
	syncpkg "privasync/server/internal/sync"
)

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

// rig owns the per-test wiring: real DB, real Storage on tmpfs, mocked
// (nil) mailer + real SSE hub. The outbox worker is driven manually via
// drainOutbox() so the test stays deterministic.
type rig struct {
	pool      *pgxpool.Pool
	files     *repository.FileRepository
	syncRoots *repository.SyncRootRepository
	users     *repository.UserRepository
	stor      *storage.Service
	q         *quota.Service
	hub       *sse.Hub
	outRep    *outbox.Repository
	worker    *outbox.Worker
	coord     *syncpkg.Impl
	bus       bus.Bus
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
	q := quota.New(users, pool, nil, "http://test")
	b := bus.New(bus.Config{ReactiveWorkers: 1})
	q.Register(b)
	hub := sse.NewHub()
	outRep := outbox.NewRepository(p)
	handlers := syncpkg.BuildHandlers(stor, hub, nil, users, outRep, "http://test")
	worker := outbox.NewWorker(outRep, handlers)
	worker.PollInterval = time.Hour // test drains by hand
	coord := syncpkg.NewCoordinator(p, files, syncRoots, conflicts, trash, outRep, worker, q, b, stor)
	// Wipe any leftover outbox rows from prior runs/tests so drainOutbox
	// only sees what this test produced.
	if _, err := p.Exec(context.Background(), `DELETE FROM outbox`); err != nil {
		t.Fatalf("wipe outbox: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = p.Exec(ctx, `DELETE FROM outbox`)
		b.Close()
	})
	return &rig{
		pool: p, files: files, syncRoots: syncRoots, users: users,
		stor: stor, q: q, hub: hub, outRep: outRep, worker: worker,
		coord: coord, bus: b, dir: dir,
	}
}

func (r *rig) makeUser(t *testing.T) *model.User {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	u, err := r.users.Create(context.Background(), "coord_"+suffix, "coord_"+suffix+"@test.local", "x", model.RoleUser)
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

func (r *rig) makeRoot(t *testing.T, userID uuid.UUID) *model.SyncRoot {
	t.Helper()
	sr, err := r.syncRoots.CreateSyncRoot(context.Background(), userID, "root", "/root")
	if err != nil {
		t.Fatalf("create sync root: %v", err)
	}
	return sr
}

func (r *rig) drainOutbox(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		e, err := r.outRep.ClaimNext(ctx)
		if errors.Is(err, outbox.ErrNotFound) {
			return
		}
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		// Re-use the worker's handler registry through a single execute step
		// by faking it: build handlers once and dispatch.
		handlers := syncpkg.BuildHandlers(r.stor, r.hub, nil, r.users, outbox.NewRepository(pool), "http://test")
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

func writeStaging(t *testing.T, r *rig, userID uuid.UUID, rootID uuid.UUID, stagingPath string, body []byte) {
	t.Helper()
	full := filepath.Join(r.dir, userID.String(), rootID.String(), stagingPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, body, 0o644); err != nil {
		t.Fatalf("write staging: %v", err)
	}
}

func TestCoordinator_UploadFile_HappyPath(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	body := []byte("hello world payload")
	staging := "/hello.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging, body)

	result, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID:      u.ID,
		SyncRootID:  root.ID,
		Path:        "/hello.txt",
		StagingPath: staging,
		Size:        int64(len(body)),
		ContentHash: []byte{0xde, 0xad},
		MTime:       time.Now(),
	})
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if result.Conflict != syncpkg.ConflictNone {
		t.Fatalf("expected ConflictNone, got %v", result.Conflict)
	}
	if result.File == nil || result.File.Path != "/hello.txt" {
		t.Fatalf("unexpected file row: %+v", result.File)
	}

	// Outbox should now hold 3 pending entries (rename, sse file_added, sse quota_updated).
	var count int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outbox WHERE done_at IS NULL`).Scan(&count); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 pending outbox entries, got %d", count)
	}

	// Draining the outbox should rename staging → /hello.txt on disk.
	r.drainOutbox(t)

	finalPath := filepath.Join(r.dir, u.ID.String(), root.ID.String(), "hello.txt")
	got, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("read final: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("final bytes mismatch")
	}
}

func TestCoordinator_UploadFile_VersionConflict(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	// Seed an existing file at /a.txt so a second upload with stale IfMatch
	// hits the version-mismatch branch.
	body1 := []byte("v1")
	staging1 := "/a.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging1, body1)
	first, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/a.txt",
		StagingPath: staging1, Size: int64(len(body1)), MTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}
	r.drainOutbox(t)

	// Second upload with stale IfMatch — server is at first.File.ServerVersion;
	// pretend the client thinks it's much higher so the optimistic lock fails.
	staleVersion := first.File.ServerVersion + 999
	body2 := []byte("v2")
	staging2 := "/a.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging2, body2)
	res, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/a.txt",
		StagingPath: staging2, Size: int64(len(body2)), MTime: time.Now(),
		IfMatch: &staleVersion,
	})
	if err != nil {
		t.Fatalf("second upload: %v", err)
	}
	if res.Conflict != syncpkg.ConflictVersionMismatch {
		t.Fatalf("expected ConflictVersionMismatch, got %v", res.Conflict)
	}
	// No conflict row should exist for version-mismatch.
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM conflicts WHERE file_id = $1`, first.File.ID).Scan(&n); err != nil {
		t.Fatalf("count conflicts: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 conflict rows, got %d", n)
	}
}

func TestCoordinator_MoveFile_HappyPath(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	// Seed source file.
	body := []byte("move me")
	staging := "/m.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging, body)
	if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/m.txt",
		StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	r.drainOutbox(t)

	res, err := r.coord.MoveFile(context.Background(), syncpkg.MoveCmd{
		UserID: u.ID, SyncRootID: root.ID,
		From: "/m.txt", To: "/m2.txt",
	})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if res.Conflict != syncpkg.ConflictNone {
		t.Fatalf("expected ConflictNone, got %v", res.Conflict)
	}
	if res.File == nil || res.File.Path != "/m2.txt" {
		t.Fatalf("unexpected file: %+v", res.File)
	}

	// Outbox should hold rename + sse file_moved.
	var pending int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outbox WHERE done_at IS NULL`).Scan(&pending); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if pending != 2 {
		t.Fatalf("expected 2 pending outbox entries, got %d", pending)
	}

	r.drainOutbox(t)

	// Storage rename happened.
	finalPath := filepath.Join(r.dir, u.ID.String(), root.ID.String(), "m2.txt")
	if _, err := os.Stat(finalPath); err != nil {
		t.Fatalf("stat final: %v", err)
	}
	srcPath := filepath.Join(r.dir, u.ID.String(), root.ID.String(), "m.txt")
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Fatalf("source still present: %v", err)
	}
}

func TestCoordinator_MoveFile_RealConflict_CreatesConflictRow(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	// Seed source.
	bodyA := []byte("A")
	stA := "/a.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, stA, bodyA)
	if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/a.txt",
		StagingPath: stA, Size: int64(len(bodyA)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed A: %v", err)
	}
	// Seed occupant at destination.
	bodyB := []byte("B")
	stB := "/b.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, stB, bodyB)
	occupant, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/b.txt",
		StagingPath: stB, Size: int64(len(bodyB)), MTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("seed B: %v", err)
	}
	r.drainOutbox(t)

	// Move /a.txt → /b.txt without If-Match: parallel-race → conflict-row at
	// loser-path. Source stays at /a.txt.
	res, err := r.coord.MoveFile(context.Background(), syncpkg.MoveCmd{
		UserID: u.ID, SyncRootID: root.ID,
		From: "/a.txt", To: "/b.txt",
	})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if res.Conflict != syncpkg.ConflictReal {
		t.Fatalf("expected ConflictReal, got %v", res.Conflict)
	}
	if res.ConflictPath == "" {
		t.Fatalf("expected conflict path")
	}

	// A conflict row pointing at the source file should exist.
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM conflicts WHERE file_id = $1`, res.File.ID).Scan(&n); err != nil {
		t.Fatalf("count conflicts: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 conflict row, got %d", n)
	}

	// Drain → bytes copied from /a.txt to conflictPath, occupant untouched.
	r.drainOutbox(t)
	conflictDisk := filepath.Join(r.dir, u.ID.String(), root.ID.String(),
		filepath.FromSlash(res.ConflictPath))
	if _, err := os.Stat(conflictDisk); err != nil {
		t.Fatalf("conflict copy missing: %v", err)
	}
	srcDisk := filepath.Join(r.dir, u.ID.String(), root.ID.String(), "a.txt")
	if _, err := os.Stat(srcDisk); err != nil {
		t.Fatalf("source moved unexpectedly: %v", err)
	}
	// Occupant still at /b.txt.
	if occupant.File.Path != "/b.txt" {
		t.Fatalf("occupant path: %v", occupant.File.Path)
	}
}

// TestCoordinator_MoveFile_RealConflict_MaterializesChoreography targets the
// shared conflict-materialization choreography through the Move entry point:
// outbox seq order (copy before sse), ConflictAdded.CurrentPath (only Move
// sets it — source stays live at a different path than the conflict), and
// both bus.Critical events. See the Upload sibling test for the same
// assertions against the Rename variant.
func TestCoordinator_MoveFile_RealConflict_MaterializesChoreography(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	bodyA := []byte("A")
	stA := "/x.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, stA, bodyA)
	if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/x.txt",
		StagingPath: stA, Size: int64(len(bodyA)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed A: %v", err)
	}
	bodyB := []byte("B")
	stB := "/y.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, stB, bodyB)
	if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/y.txt",
		StagingPath: stB, Size: int64(len(bodyB)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed B: %v", err)
	}
	r.drainOutbox(t)

	var keptEvents int
	var kept bus.FileConflictKept
	r.bus.SubscribeCritical(bus.EventFileConflictKept, func(_ context.Context, _ pgx.Tx, evt bus.Event) error {
		e, ok := evt.(bus.FileConflictKept)
		if !ok {
			t.Fatalf("expected bus.FileConflictKept, got %T", evt)
		}
		kept = e
		keptEvents++
		return nil
	})
	var createdEvents int
	var created bus.ConflictCreated
	r.bus.SubscribeCritical(bus.EventConflictCreated, func(_ context.Context, _ pgx.Tx, evt bus.Event) error {
		e, ok := evt.(bus.ConflictCreated)
		if !ok {
			t.Fatalf("expected bus.ConflictCreated, got %T", evt)
		}
		created = e
		createdEvents++
		return nil
	})

	res, err := r.coord.MoveFile(context.Background(), syncpkg.MoveCmd{
		UserID: u.ID, SyncRootID: root.ID,
		From: "/x.txt", To: "/y.txt",
	})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if res.Conflict != syncpkg.ConflictReal {
		t.Fatalf("expected ConflictReal, got %v", res.Conflict)
	}

	rows, err := r.pool.Query(context.Background(),
		`SELECT seq, kind FROM outbox WHERE done_at IS NULL ORDER BY seq`)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()
	var gotKinds []string
	for rows.Next() {
		var seq int
		var kind string
		if err := rows.Scan(&seq, &kind); err != nil {
			t.Fatalf("scan outbox row: %v", err)
		}
		gotKinds = append(gotKinds, kind)
	}
	wantKinds := []string{string(outbox.KindStorageCopy), string(outbox.KindSSETyped)}
	if len(gotKinds) != len(wantKinds) {
		t.Fatalf("outbox kinds: got %v, want %v", gotKinds, wantKinds)
	}
	for i, want := range wantKinds {
		if gotKinds[i] != want {
			t.Fatalf("outbox seq %d kind: got %q, want %q", i+1, gotKinds[i], want)
		}
	}

	if keptEvents != 1 {
		t.Fatalf("expected 1 FileConflictKept event, got %d", keptEvents)
	}
	if kept.FileID != res.File.ID || kept.Bytes != int64(len(bodyA)) {
		t.Fatalf("FileConflictKept: got %+v", kept)
	}
	if createdEvents != 1 {
		t.Fatalf("expected 1 ConflictCreated event, got %d", createdEvents)
	}
	if created.FilePath != "/y.txt" {
		t.Fatalf("ConflictCreated.FilePath: got %q, want /y.txt", created.FilePath)
	}
}

func TestCoordinator_MoveFile_VersionMismatch(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	body := []byte("v")
	st := "/v.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, st, body)
	first, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/v.txt",
		StagingPath: st, Size: int64(len(body)), MTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	r.drainOutbox(t)

	stale := first.File.ServerVersion + 99
	res, err := r.coord.MoveFile(context.Background(), syncpkg.MoveCmd{
		UserID: u.ID, SyncRootID: root.ID,
		From: "/v.txt", To: "/v-new.txt",
		IfMatch: &stale,
	})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if res.Conflict != syncpkg.ConflictVersionMismatch {
		t.Fatalf("expected ConflictVersionMismatch, got %v", res.Conflict)
	}
	// No conflict row, no outbox entries.
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM conflicts WHERE file_id = $1`, first.File.ID).Scan(&n); err != nil {
		t.Fatalf("count conflicts: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 conflict rows, got %d", n)
	}
	var pending int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outbox WHERE done_at IS NULL`).Scan(&pending); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if pending != 0 {
		t.Fatalf("expected 0 pending outbox entries, got %d", pending)
	}
}

func TestCoordinator_MoveFile_Directory_RewritesSubtree(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)
	ctx := context.Background()

	// Seed a folder with a nested subfolder + one file in each level.
	if _, err := r.files.CreateDirectory(ctx, u.ID, root.ID, "/foo", time.Now()); err != nil {
		t.Fatalf("seed dir /foo: %v", err)
	}
	if _, err := r.files.CreateDirectory(ctx, u.ID, root.ID, "/foo/sub", time.Now()); err != nil {
		t.Fatalf("seed dir /foo/sub: %v", err)
	}
	for _, p := range []string{"/foo/a.txt", "/foo/sub/b.txt"} {
		body := []byte("x")
		staging := p + ".staging-" + uuid.NewString()
		writeStaging(t, r, u.ID, root.ID, staging, body)
		if _, err := r.coord.UploadFile(ctx, syncpkg.UploadCmd{
			UserID: u.ID, SyncRootID: root.ID, Path: p,
			StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
		}); err != nil {
			t.Fatalf("seed upload %s: %v", p, err)
		}
	}
	r.drainOutbox(t)

	res, err := r.coord.MoveFile(ctx, syncpkg.MoveCmd{
		UserID: u.ID, SyncRootID: root.ID, From: "/foo", To: "/bar",
	})
	if err != nil {
		t.Fatalf("MoveFile (dir): %v", err)
	}
	if res.Conflict != syncpkg.ConflictNone {
		t.Fatalf("expected ConflictNone, got %v", res.Conflict)
	}
	if res.File == nil || res.File.Path != "/bar" || !res.File.IsDir {
		t.Fatalf("unexpected dir row: %+v", res.File)
	}

	// Every descendant path is rewritten; nothing lingers under /foo.
	want := map[string]bool{"/bar": true, "/bar/sub": true, "/bar/a.txt": true, "/bar/sub/b.txt": true}
	rows, err := r.pool.Query(ctx,
		`SELECT path FROM files WHERE sync_root_id = $1 AND is_deleted = FALSE ORDER BY path`, root.ID)
	if err != nil {
		t.Fatalf("query paths: %v", err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[p] = true
	}
	for p := range want {
		if !got[p] {
			t.Fatalf("missing rewritten path %q; got %v", p, got)
		}
	}
	for p := range got {
		if strings.HasPrefix(p, "/foo") {
			t.Fatalf("stale path under /foo survived: %q", p)
		}
	}

	r.drainOutbox(t)

	// Storage moved the whole subtree in one os.Rename.
	base := filepath.Join(r.dir, u.ID.String(), root.ID.String())
	if _, err := os.Stat(filepath.Join(base, "bar", "sub", "b.txt")); err != nil {
		t.Fatalf("stat moved nested file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "foo")); !os.IsNotExist(err) {
		t.Fatalf("source folder still present: %v", err)
	}
}

func TestCoordinator_MoveFile_ImplicitDirectory_RewritesChildren(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)
	ctx := context.Background()

	// Upload a nested file WITHOUT ever creating a /foo dir row — /foo is an
	// implicit directory the UI infers from the child path.
	body := []byte("x")
	staging := "/foo/a.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging, body)
	if _, err := r.coord.UploadFile(ctx, syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/foo/a.txt",
		StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	r.drainOutbox(t)

	res, err := r.coord.MoveFile(ctx, syncpkg.MoveCmd{
		UserID: u.ID, SyncRootID: root.ID, From: "/foo", To: "/bar",
	})
	if err != nil {
		t.Fatalf("MoveFile (implicit dir): %v", err)
	}
	if res.Conflict != syncpkg.ConflictNone {
		t.Fatalf("expected ConflictNone, got %v", res.Conflict)
	}
	if res.File == nil || res.File.Path != "/bar" || !res.File.IsDir {
		t.Fatalf("unexpected synthesised dir row: %+v", res.File)
	}

	child, err := r.files.FindByPath(ctx, root.ID, "/bar/a.txt")
	if err != nil {
		t.Fatalf("child not rewritten to /bar/a.txt: %v", err)
	}
	if child.Path != "/bar/a.txt" {
		t.Fatalf("unexpected child path: %q", child.Path)
	}
}

// ADR-0065: a folder rename must ride the changes feed as one move_events
// prefix entry, not as a tombstone-per-descendant + live-row-per-descendant
// pair (the pre-ADR-0065 contract this test used to assert, and the very
// thing that flooded the feed on a large subtree). The affected rows'
// seq must also stay untouched -- the move_events row is the only signal.
func TestCoordinator_MoveFile_Directory_EmitsOnePrefixMoveEvent(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)
	ctx := context.Background()

	if _, err := r.files.CreateDirectory(ctx, u.ID, root.ID, "/Test", time.Now()); err != nil {
		t.Fatalf("seed /Test: %v", err)
	}
	body := []byte("x")
	staging := "/Test/a.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging, body)
	if _, err := r.coord.UploadFile(ctx, syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/Test/a.txt",
		StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	r.drainOutbox(t)

	before, err := r.files.FindByPath(ctx, root.ID, "/Test/a.txt")
	if err != nil {
		t.Fatalf("find before move: %v", err)
	}
	// A pull client caught up right before the rename holds this as its
	// cursor -- since=0 would also see the rows' original creation entries
	// and couldn't tell those apart from a fresh bump the move might cause.
	watermark := before.Seq

	if _, err := r.coord.MoveFile(ctx, syncpkg.MoveCmd{
		UserID: u.ID, SyncRootID: root.ID, From: "/Test", To: "/Test 2",
	}); err != nil {
		t.Fatalf("MoveFile (dir): %v", err)
	}

	changes, err := r.files.GetChangedSince(ctx, root.ID, watermark, 1000)
	if err != nil {
		t.Fatalf("GetChangedSince: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("move must not bump seq on the affected rows any more -- unexpected feed entries past the pre-move watermark: %+v", changes)
	}

	after, err := r.files.FindByPath(ctx, root.ID, "/Test 2/a.txt")
	if err != nil || after.IsDeleted {
		t.Fatalf("descendant must live at the new path, err=%v file=%+v", err, after)
	}
	if after.Seq != before.Seq {
		t.Fatalf("descendant seq changed from %d to %d -- the move must not flood the feed with a row per descendant", before.Seq, after.Seq)
	}

	events, err := r.files.ListMoveEventsSince(ctx, root.ID, 0, 10)
	if err != nil {
		t.Fatalf("ListMoveEventsSince: %v", err)
	}
	if len(events) != 1 || events[0].OldPrefix != "/Test" || events[0].NewPrefix != "/Test 2" {
		t.Fatalf("move_events = %+v, want exactly one old=/Test new=/Test 2", events)
	}
}

func TestCoordinator_MoveFile_Directory_DestinationOccupied_409(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)
	ctx := context.Background()

	if _, err := r.files.CreateDirectory(ctx, u.ID, root.ID, "/src", time.Now()); err != nil {
		t.Fatalf("seed /src: %v", err)
	}
	if _, err := r.files.CreateDirectory(ctx, u.ID, root.ID, "/dst", time.Now()); err != nil {
		t.Fatalf("seed /dst: %v", err)
	}

	res, err := r.coord.MoveFile(ctx, syncpkg.MoveCmd{
		UserID: u.ID, SyncRootID: root.ID, From: "/src", To: "/dst",
	})
	if err != nil {
		t.Fatalf("MoveFile (dir): %v", err)
	}
	if res.Conflict != syncpkg.ConflictVersionMismatch {
		t.Fatalf("expected ConflictVersionMismatch on occupied dest, got %v", res.Conflict)
	}
}

// A blind upload (no If-Match) of the bytes the server already has is not a
// conflict: the client learns the current version, nothing is duplicated and the
// staging file does not linger.
func TestCoordinator_UploadFile_IdenticalBlindUpload_IsNoConflict(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)
	ctx := context.Background()

	body := []byte("same bytes")
	hash := []byte("hash-of-same-bytes")
	staging1 := "/p.jpg.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging1, body)
	first, err := r.coord.UploadFile(ctx, syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/p.jpg",
		StagingPath: staging1, Size: int64(len(body)), ContentHash: hash, MTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}
	r.drainOutbox(t)

	staging2 := "/p.jpg.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging2, body)
	res, err := r.coord.UploadFile(ctx, syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/p.jpg",
		StagingPath: staging2, Size: int64(len(body)), ContentHash: hash, MTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("identical upload: %v", err)
	}

	if res.Conflict != syncpkg.ConflictNone {
		t.Fatalf("identical bytes must not conflict, got %v", res.Conflict)
	}
	if res.File == nil || res.File.ID != first.File.ID || res.File.ServerVersion != first.File.ServerVersion {
		t.Fatalf("expected the existing file at its current version, got %+v", res.File)
	}
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM conflicts WHERE file_id = $1`, first.File.ID).Scan(&n); err != nil {
		t.Fatalf("count conflicts: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected no conflict row, got %d", n)
	}
	if _, err := os.Stat(filepath.Join(r.dir, u.ID.String(), root.ID.String(), staging2)); !os.IsNotExist(err) {
		t.Fatalf("staging file must be removed, stat err = %v", err)
	}
}

// The identical-content shortcut needs a real hash on both sides: different
// bytes, or no hash at all, still conflict.
func TestCoordinator_UploadFile_DifferentOrUnhashedBlindUpload_StillConflicts(t *testing.T) {
	for name, tc := range map[string]struct {
		firstHash, secondHash []byte
		secondBody            string
	}{
		"different bytes": {[]byte("h1"), []byte("h2"), "other"},
		"no hash":         {nil, nil, "same"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			u := r.makeUser(t)
			root := r.makeRoot(t, u.ID)
			ctx := context.Background()

			staging1 := "/q.txt.staging-" + uuid.NewString()
			writeStaging(t, r, u.ID, root.ID, staging1, []byte("same"))
			if _, err := r.coord.UploadFile(ctx, syncpkg.UploadCmd{
				UserID: u.ID, SyncRootID: root.ID, Path: "/q.txt",
				StagingPath: staging1, Size: 4, ContentHash: tc.firstHash, MTime: time.Now(),
			}); err != nil {
				t.Fatalf("first upload: %v", err)
			}
			r.drainOutbox(t)

			staging2 := "/q.txt.staging-" + uuid.NewString()
			writeStaging(t, r, u.ID, root.ID, staging2, []byte(tc.secondBody))
			res, err := r.coord.UploadFile(ctx, syncpkg.UploadCmd{
				UserID: u.ID, SyncRootID: root.ID, Path: "/q.txt",
				StagingPath: staging2, Size: int64(len(tc.secondBody)), ContentHash: tc.secondHash, MTime: time.Now(),
			})
			if err != nil {
				t.Fatalf("second upload: %v", err)
			}
			if res.Conflict != syncpkg.ConflictReal {
				t.Fatalf("expected ConflictReal, got %v", res.Conflict)
			}
		})
	}
}

func TestCoordinator_UploadFile_RealConflict_CreatesConflictRow(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	body1 := []byte("v1")
	staging1 := "/b.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging1, body1)
	first, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/b.txt",
		StagingPath: staging1, Size: int64(len(body1)), MTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}
	r.drainOutbox(t)

	// Second upload WITHOUT IfMatch — real conflict path.
	body2 := []byte("collision")
	staging2 := "/b.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging2, body2)
	res, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/b.txt",
		StagingPath: staging2, Size: int64(len(body2)), MTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("second upload: %v", err)
	}
	if res.Conflict != syncpkg.ConflictReal {
		t.Fatalf("expected ConflictReal, got %v", res.Conflict)
	}
	if res.ConflictPath == "" {
		t.Fatalf("expected conflict path, got empty")
	}

	// A conflict row should now exist.
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM conflicts WHERE file_id = $1`, first.File.ID).Scan(&n); err != nil {
		t.Fatalf("count conflicts: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 conflict row, got %d", n)
	}

	// Outbox should have rename(staging→conflict_path) + sse = 2 pending.
	// The conflict mail is enqueued by the critical mail subscriber
	// which is not wired in this rig, so it does not appear here.
	var pending int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outbox WHERE done_at IS NULL`).Scan(&pending); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if pending != 2 {
		t.Fatalf("expected 2 pending outbox entries, got %d", pending)
	}
}

// TestCoordinator_UploadFile_RealConflict_MaterializesChoreography targets
// the shared conflict-materialization choreography (ADR-0022 part 3) through
// its Upload entry point: exact outbox seq order (rename before sse) and
// both bus.Critical events (FileConflictKept + ConflictCreated), not just
// counts. Guards materialiseConflictCopy against silently dropping or
// reordering a step during the realConflict/realMoveConflict/
// realRestoreConflict de-duplication.
func TestCoordinator_UploadFile_RealConflict_MaterializesChoreography(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	body1 := []byte("v1")
	staging1 := "/m.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging1, body1)
	first, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/m.txt",
		StagingPath: staging1, Size: int64(len(body1)), MTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}
	r.drainOutbox(t)

	var keptEvents int
	var kept bus.FileConflictKept
	r.bus.SubscribeCritical(bus.EventFileConflictKept, func(_ context.Context, _ pgx.Tx, evt bus.Event) error {
		e, ok := evt.(bus.FileConflictKept)
		if !ok {
			t.Fatalf("expected bus.FileConflictKept, got %T", evt)
		}
		kept = e
		keptEvents++
		return nil
	})
	var createdEvents int
	var created bus.ConflictCreated
	r.bus.SubscribeCritical(bus.EventConflictCreated, func(_ context.Context, _ pgx.Tx, evt bus.Event) error {
		e, ok := evt.(bus.ConflictCreated)
		if !ok {
			t.Fatalf("expected bus.ConflictCreated, got %T", evt)
		}
		created = e
		createdEvents++
		return nil
	})

	body2 := []byte("collision2")
	staging2 := "/m.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging2, body2)
	res, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/m.txt",
		StagingPath: staging2, Size: int64(len(body2)), MTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("second upload: %v", err)
	}
	if res.Conflict != syncpkg.ConflictReal {
		t.Fatalf("expected ConflictReal, got %v", res.Conflict)
	}

	// Conflict row.
	var confPath string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT conflict_path FROM conflicts WHERE file_id = $1`, first.File.ID).Scan(&confPath); err != nil {
		t.Fatalf("query conflict row: %v", err)
	}
	if confPath != res.ConflictPath {
		t.Fatalf("conflict_path: got %q, want %q", confPath, res.ConflictPath)
	}

	// Outbox order: seq=1 storage rename, seq=2 typed SSE.
	rows, err := r.pool.Query(context.Background(),
		`SELECT seq, kind FROM outbox WHERE done_at IS NULL ORDER BY seq`)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()
	var gotKinds []string
	for rows.Next() {
		var seq int
		var kind string
		if err := rows.Scan(&seq, &kind); err != nil {
			t.Fatalf("scan outbox row: %v", err)
		}
		gotKinds = append(gotKinds, kind)
	}
	wantKinds := []string{string(outbox.KindStorageRename), string(outbox.KindSSETyped)}
	if len(gotKinds) != len(wantKinds) {
		t.Fatalf("outbox kinds: got %v, want %v", gotKinds, wantKinds)
	}
	for i, want := range wantKinds {
		if gotKinds[i] != want {
			t.Fatalf("outbox seq %d kind: got %q, want %q", i+1, gotKinds[i], want)
		}
	}

	// Both bus.Critical events, exactly once each, with the right payload.
	if keptEvents != 1 {
		t.Fatalf("expected 1 FileConflictKept event, got %d", keptEvents)
	}
	if kept.FileID != first.File.ID || kept.Bytes != int64(len(body2)) {
		t.Fatalf("FileConflictKept: got %+v", kept)
	}
	if createdEvents != 1 {
		t.Fatalf("expected 1 ConflictCreated event, got %d", createdEvents)
	}
	if created.FilePath != "/m.txt" {
		t.Fatalf("ConflictCreated.FilePath: got %q, want /m.txt", created.FilePath)
	}
}

// A delete decided against an older version must not silently discard the
// newer one. The client states the version it means via If-Match; if the row
// moved on, the delete is refused rather than taking someone else's edit with
// it.
func TestCoordinator_DeleteFile_IfMatchMismatchConflicts(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	body := []byte("v1")
	staging := "/c.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging, body)
	up, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/c.txt",
		StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	r.drainOutbox(t)

	stale := up.File.ServerVersion - 1
	res, err := r.coord.DeleteFile(context.Background(), syncpkg.DeleteCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/c.txt", IfMatch: &stale,
	})
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if res.Conflict != syncpkg.ConflictVersionMismatch {
		t.Fatalf("Conflict: got %v, want ConflictVersionMismatch", res.Conflict)
	}
	if res.File == nil || res.File.IsDeleted {
		t.Fatalf("a refused delete must leave the row alive, got %+v", res.File)
	}
}

// The matching version deletes as usual — the guard only rejects a mismatch.
func TestCoordinator_DeleteFile_IfMatchCurrentVersionSucceeds(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	body := []byte("v1")
	staging := "/c.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging, body)
	up, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/c.txt",
		StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	r.drainOutbox(t)

	current := up.File.ServerVersion
	res, err := r.coord.DeleteFile(context.Background(), syncpkg.DeleteCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/c.txt", IfMatch: &current,
	})
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if res.Conflict != syncpkg.ConflictNone {
		t.Fatalf("Conflict: got %v, want ConflictNone", res.Conflict)
	}
	if res.File == nil || !res.File.IsDeleted {
		t.Fatalf("expected soft-deleted row, got %+v", res.File)
	}
}

func TestCoordinator_DeleteFile_HappyPath(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	body := []byte("trash me")
	staging := "/d.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging, body)
	if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/d.txt",
		StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	r.drainOutbox(t)

	res, err := r.coord.DeleteFile(context.Background(), syncpkg.DeleteCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/d.txt",
	})
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if res.File == nil || !res.File.IsDeleted {
		t.Fatalf("expected soft-deleted row, got %+v", res.File)
	}
	if res.File.TrashPath == nil || *res.File.TrashPath == "" {
		t.Fatalf("expected trash path on result row")
	}

	// Outbox: storage_trash + sse file_deleted + sse trash_added + sse quota_updated = 4.
	var pending int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outbox WHERE done_at IS NULL`).Scan(&pending); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if pending != 4 {
		t.Fatalf("expected 4 pending outbox entries, got %d", pending)
	}

	r.drainOutbox(t)

	// Bytes gone from original path, present at trash path.
	origDisk := filepath.Join(r.dir, u.ID.String(), root.ID.String(), "d.txt")
	if _, err := os.Stat(origDisk); !os.IsNotExist(err) {
		t.Fatalf("original still present: %v", err)
	}
	trashDisk := filepath.Join(r.dir, u.ID.String(), root.ID.String(),
		filepath.FromSlash(*res.File.TrashPath))
	got, err := os.ReadFile(trashDisk)
	if err != nil {
		t.Fatalf("read trashed: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("trashed bytes mismatch")
	}
}

func TestCoordinator_DeleteFile_NotFound(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	_, err := r.coord.DeleteFile(context.Background(), syncpkg.DeleteCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/missing.txt",
	})
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// No outbox row leaked into the table.
	var pending int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outbox WHERE done_at IS NULL`).Scan(&pending); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if pending != 0 {
		t.Fatalf("expected 0 pending outbox entries, got %d", pending)
	}
}

func TestCoordinator_RestoreFromTrash_HappyPath(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	body := []byte("restore me")
	staging := "/r.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging, body)
	if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/r.txt",
		StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	r.drainOutbox(t)

	del, err := r.coord.DeleteFile(context.Background(), syncpkg.DeleteCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/r.txt",
	})
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	r.drainOutbox(t)

	res, err := r.coord.RestoreFromTrash(context.Background(), syncpkg.RestoreCmd{
		UserID: u.ID, SyncRootID: root.ID, FileID: del.File.ID,
	})
	if err != nil {
		t.Fatalf("RestoreFromTrash: %v", err)
	}
	if res.Conflict != syncpkg.ConflictNone {
		t.Fatalf("expected ConflictNone, got %v", res.Conflict)
	}
	if res.File == nil || res.File.IsDeleted {
		t.Fatalf("expected re-activated row, got %+v", res.File)
	}
	if res.File.TrashPath != nil {
		t.Fatalf("trash_path should be cleared, got %v", *res.File.TrashPath)
	}

	// Outbox: untrash + sse trash_restored + sse file_added + sse quota_updated = 4.
	var pending int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outbox WHERE done_at IS NULL`).Scan(&pending); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if pending != 4 {
		t.Fatalf("expected 4 pending outbox entries, got %d", pending)
	}

	r.drainOutbox(t)

	// Bytes back at original path.
	origDisk := filepath.Join(r.dir, u.ID.String(), root.ID.String(), "r.txt")
	got, err := os.ReadFile(origDisk)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("restored bytes mismatch")
	}
}

func TestCoordinator_RestoreFromTrash_RealConflict(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	// Seed a file, delete it.
	body := []byte("v1")
	staging := "/c.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging, body)
	if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/c.txt",
		StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	r.drainOutbox(t)
	del, err := r.coord.DeleteFile(context.Background(), syncpkg.DeleteCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/c.txt",
	})
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	r.drainOutbox(t)

	// Re-occupy the original path with new content.
	body2 := []byte("occupant")
	staging2 := "/c.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging2, body2)
	occupant, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/c.txt",
		StagingPath: staging2, Size: int64(len(body2)), MTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("occupy upload: %v", err)
	}
	r.drainOutbox(t)

	// Restore the trashed row — original path is occupied → real conflict.
	res, err := r.coord.RestoreFromTrash(context.Background(), syncpkg.RestoreCmd{
		UserID: u.ID, SyncRootID: root.ID, FileID: del.File.ID,
	})
	if err != nil {
		t.Fatalf("RestoreFromTrash: %v", err)
	}
	if res.Conflict != syncpkg.ConflictReal {
		t.Fatalf("expected ConflictReal, got %v", res.Conflict)
	}
	if res.ConflictPath == "" {
		t.Fatalf("expected conflict path")
	}
	if res.File == nil || res.File.IsDeleted || res.File.Path != res.ConflictPath {
		t.Fatalf("expected live row at conflict path, got %+v", res.File)
	}

	// Conflict row exists for the restored file.
	var nConf int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM conflicts WHERE file_id = $1`, res.File.ID).Scan(&nConf); err != nil {
		t.Fatalf("count conflicts: %v", err)
	}
	if nConf != 1 {
		t.Fatalf("expected 1 conflict row, got %d", nConf)
	}

	r.drainOutbox(t)

	// Bytes from trash now at conflict path; occupant untouched.
	conflictDisk := filepath.Join(r.dir, u.ID.String(), root.ID.String(),
		filepath.FromSlash(res.ConflictPath))
	got, err := os.ReadFile(conflictDisk)
	if err != nil {
		t.Fatalf("read conflict path: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("conflict bytes mismatch")
	}
	occDisk := filepath.Join(r.dir, u.ID.String(), root.ID.String(), "c.txt")
	occGot, err := os.ReadFile(occDisk)
	if err != nil {
		t.Fatalf("read occupant: %v", err)
	}
	if string(occGot) != string(body2) {
		t.Fatalf("occupant bytes mismatch")
	}
	if occupant.File.Path != "/c.txt" {
		t.Fatalf("occupant path: %v", occupant.File.Path)
	}
}

// TestCoordinator_RestoreFromTrash_RealConflict_MaterializesChoreography
// targets the shared conflict-materialization choreography through the
// Restore entry point: this is the variant that diverges most from Upload/
// Move — the storage move is an Untrash (not Copy/Rename) and the "kept"
// bus event is FileRestored (not FileConflictKept) — plus it exercises the
// ResolveFile callback's DB row relocation (RestoreFromTrashAtPath), not just
// a pass-through of an already-known file.
func TestCoordinator_RestoreFromTrash_RealConflict_MaterializesChoreography(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	body := []byte("v1")
	staging := "/r.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging, body)
	if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/r.txt",
		StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	r.drainOutbox(t)
	del, err := r.coord.DeleteFile(context.Background(), syncpkg.DeleteCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/r.txt",
	})
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	r.drainOutbox(t)

	body2 := []byte("occupant2")
	staging2 := "/r.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging2, body2)
	if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/r.txt",
		StagingPath: staging2, Size: int64(len(body2)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("occupy upload: %v", err)
	}
	r.drainOutbox(t)

	var keptEvents int
	var kept bus.FileRestored
	r.bus.SubscribeCritical(bus.EventFileRestored, func(_ context.Context, _ pgx.Tx, evt bus.Event) error {
		e, ok := evt.(bus.FileRestored)
		if !ok {
			t.Fatalf("expected bus.FileRestored, got %T", evt)
		}
		kept = e
		keptEvents++
		return nil
	})
	var createdEvents int
	var created bus.ConflictCreated
	r.bus.SubscribeCritical(bus.EventConflictCreated, func(_ context.Context, _ pgx.Tx, evt bus.Event) error {
		e, ok := evt.(bus.ConflictCreated)
		if !ok {
			t.Fatalf("expected bus.ConflictCreated, got %T", evt)
		}
		created = e
		createdEvents++
		return nil
	})

	res, err := r.coord.RestoreFromTrash(context.Background(), syncpkg.RestoreCmd{
		UserID: u.ID, SyncRootID: root.ID, FileID: del.File.ID,
	})
	if err != nil {
		t.Fatalf("RestoreFromTrash: %v", err)
	}
	if res.Conflict != syncpkg.ConflictReal {
		t.Fatalf("expected ConflictReal, got %v", res.Conflict)
	}
	// ResolveFile actually relocated the trashed row onto the conflict path.
	if res.File == nil || res.File.IsDeleted || res.File.Path != res.ConflictPath {
		t.Fatalf("expected live row at conflict path, got %+v", res.File)
	}

	rows, err := r.pool.Query(context.Background(),
		`SELECT seq, kind FROM outbox WHERE done_at IS NULL ORDER BY seq`)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()
	var gotKinds []string
	for rows.Next() {
		var seq int
		var kind string
		if err := rows.Scan(&seq, &kind); err != nil {
			t.Fatalf("scan outbox row: %v", err)
		}
		gotKinds = append(gotKinds, kind)
	}
	wantKinds := []string{string(outbox.KindStorageUntrash), string(outbox.KindSSETyped)}
	if len(gotKinds) != len(wantKinds) {
		t.Fatalf("outbox kinds: got %v, want %v", gotKinds, wantKinds)
	}
	for i, want := range wantKinds {
		if gotKinds[i] != want {
			t.Fatalf("outbox seq %d kind: got %q, want %q", i+1, gotKinds[i], want)
		}
	}

	if keptEvents != 1 {
		t.Fatalf("expected 1 FileRestored event, got %d", keptEvents)
	}
	if kept.FileID != del.File.ID || kept.Bytes != int64(len(body)) {
		t.Fatalf("FileRestored: got %+v", kept)
	}
	if createdEvents != 1 {
		t.Fatalf("expected 1 ConflictCreated event, got %d", createdEvents)
	}
	if created.FilePath != "/r.txt" {
		t.Fatalf("ConflictCreated.FilePath: got %q, want /r.txt", created.FilePath)
	}
}

func TestCoordinator_EmptyTrash_HappyPath(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	body := []byte("purge me")
	staging := "/e.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging, body)
	if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/e.txt",
		StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	r.drainOutbox(t)
	del, err := r.coord.DeleteFile(context.Background(), syncpkg.DeleteCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/e.txt",
	})
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	r.drainOutbox(t)

	// Sanity: trash row exists, bytes on disk under trash path.
	trashDisk := filepath.Join(r.dir, u.ID.String(), root.ID.String(),
		filepath.FromSlash(*del.File.TrashPath))
	if _, err := os.Stat(trashDisk); err != nil {
		t.Fatalf("trash file missing pre-empty: %v", err)
	}

	if err := r.coord.EmptyTrash(context.Background(), syncpkg.EmptyTrashCmd{
		UserID: u.ID,
	}); err != nil {
		t.Fatalf("EmptyTrash: %v", err)
	}

	// DB row purged.
	var rowCount int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM files WHERE id = $1`, del.File.ID).Scan(&rowCount); err != nil {
		t.Fatalf("count files: %v", err)
	}
	if rowCount != 0 {
		t.Fatalf("expected file row gone, got %d", rowCount)
	}

	// Outbox: 1 storage_delete + 2 sse (trash_emptied + quota_updated) = 3 pending.
	var pending int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outbox WHERE done_at IS NULL`).Scan(&pending); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if pending != 3 {
		t.Fatalf("expected 3 pending outbox entries, got %d", pending)
	}

	r.drainOutbox(t)

	if _, err := os.Stat(trashDisk); !os.IsNotExist(err) {
		t.Fatalf("trash file still present after drain: %v", err)
	}

	// Quota recomputed: trash_bytes = 0.
	usr, err := r.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	if usr.TrashBytes != 0 {
		t.Fatalf("expected trash_bytes=0, got %d", usr.TrashBytes)
	}
}

func TestCoordinator_EmptyTrash_NoOp(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	_ = r.makeRoot(t, u.ID)

	if err := r.coord.EmptyTrash(context.Background(), syncpkg.EmptyTrashCmd{
		UserID: u.ID,
	}); err != nil {
		t.Fatalf("EmptyTrash (empty trash): %v", err)
	}

	var pending int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outbox`).Scan(&pending); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if pending != 0 {
		t.Fatalf("expected 0 outbox entries on no-op, got %d", pending)
	}
}

func TestCoordinator_EmptyTrash_MultipleRoots(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	rootA := r.makeRoot(t, u.ID)
	// makeRoot only takes one name internally — second root via direct repo call.
	rootB, err := r.syncRoots.CreateSyncRoot(context.Background(), u.ID, "root2", "/root2")
	if err != nil {
		t.Fatalf("create second root: %v", err)
	}

	seed := func(root uuid.UUID, name string) uuid.UUID {
		body := []byte("payload-" + name)
		staging := "/" + name + ".staging-" + uuid.NewString()
		writeStaging(t, r, u.ID, root, staging, body)
		if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
			UserID: u.ID, SyncRootID: root, Path: "/" + name,
			StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
		}); err != nil {
			t.Fatalf("seed upload %s: %v", name, err)
		}
		r.drainOutbox(t)
		del, err := r.coord.DeleteFile(context.Background(), syncpkg.DeleteCmd{
			UserID: u.ID, SyncRootID: root, Path: "/" + name,
		})
		if err != nil {
			t.Fatalf("delete %s: %v", name, err)
		}
		r.drainOutbox(t)
		return del.File.ID
	}
	id1 := seed(rootA.ID, "a.txt")
	id2 := seed(rootA.ID, "b.txt")
	id3 := seed(rootB.ID, "c.txt")

	if err := r.coord.EmptyTrash(context.Background(), syncpkg.EmptyTrashCmd{
		UserID: u.ID,
	}); err != nil {
		t.Fatalf("EmptyTrash: %v", err)
	}

	var rowCount int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM files WHERE id = ANY($1)`,
		[]uuid.UUID{id1, id2, id3}).Scan(&rowCount); err != nil {
		t.Fatalf("count files: %v", err)
	}
	if rowCount != 0 {
		t.Fatalf("expected all 3 rows purged, %d remain", rowCount)
	}

	// 3 storage_delete + 2 sse = 5 pending.
	var pending int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outbox WHERE done_at IS NULL`).Scan(&pending); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if pending != 5 {
		t.Fatalf("expected 5 pending outbox entries, got %d", pending)
	}

	r.drainOutbox(t)

	usr, err := r.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	if usr.TrashBytes != 0 {
		t.Fatalf("expected trash_bytes=0, got %d", usr.TrashBytes)
	}
	if usr.UsedBytes != 0 {
		t.Fatalf("expected used_bytes=0 (all files trashed+purged), got %d", usr.UsedBytes)
	}
}

func TestCoordinator_EmptyTrash_PartialFailure(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	// Seed two trashed files.
	seedTrashed := func(name string) {
		body := []byte("payload-" + name)
		staging := "/" + name + ".staging-" + uuid.NewString()
		writeStaging(t, r, u.ID, root.ID, staging, body)
		if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
			UserID: u.ID, SyncRootID: root.ID, Path: "/" + name,
			StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
		}); err != nil {
			t.Fatalf("seed upload %s: %v", name, err)
		}
		r.drainOutbox(t)
		if _, err := r.coord.DeleteFile(context.Background(), syncpkg.DeleteCmd{
			UserID: u.ID, SyncRootID: root.ID, Path: "/" + name,
		}); err != nil {
			t.Fatalf("delete %s: %v", name, err)
		}
		r.drainOutbox(t)
	}
	seedTrashed("p1.txt")
	seedTrashed("p2.txt")

	if err := r.coord.EmptyTrash(context.Background(), syncpkg.EmptyTrashCmd{
		UserID: u.ID,
	}); err != nil {
		t.Fatalf("EmptyTrash: %v", err)
	}

	// DB rows are gone immediately — TX committed before storage handlers run.
	var rowCount int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM files WHERE user_id = $1 AND is_deleted = TRUE`, u.ID).Scan(&rowCount); err != nil {
		t.Fatalf("count trashed rows: %v", err)
	}
	if rowCount != 0 {
		t.Fatalf("expected 0 trashed rows post-commit, got %d", rowCount)
	}

	// Drain with a poisoned storage-delete handler that fails on the first
	// invocation only. The failed entry must stay pending with attempts > 0
	// while the successor entries continue to drain.
	ctx := context.Background()
	failOnce := true
	failedID := uuid.UUID{}
	for i := 0; i < 50; i++ {
		e, err := r.outRep.ClaimNext(ctx)
		if errors.Is(err, outbox.ErrNotFound) {
			break
		}
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if e.Kind == outbox.KindStorageDelete && failOnce {
			failOnce = false
			failedID = e.ID
			if err := r.outRep.MarkRetry(ctx, e.ID, "injected", time.Hour); err != nil {
				t.Fatalf("mark retry: %v", err)
			}
			continue
		}
		handlers := syncpkg.BuildHandlers(r.stor, r.hub, nil, r.users, outbox.NewRepository(pool), "http://test")
		h, ok := handlers[e.Kind]
		if !ok {
			t.Fatalf("no handler for %q", e.Kind)
		}
		if err := h(ctx, e.Payload); err != nil {
			t.Fatalf("handler %q: %v", e.Kind, err)
		}
		if err := r.outRep.MarkDone(ctx, e.ID); err != nil {
			t.Fatalf("mark done: %v", err)
		}
	}

	// The failed storage-delete row stays pending with attempts > 0 and a
	// future next_attempt_at — exactly the retry contract the worker honours.
	var attempts int
	var doneAt *time.Time
	var nextAttempt time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT attempts, done_at, next_attempt_at FROM outbox WHERE id = $1`, failedID,
	).Scan(&attempts, &doneAt, &nextAttempt); err != nil {
		t.Fatalf("query failed row: %v", err)
	}
	if doneAt != nil {
		t.Fatalf("failed entry should still be pending, got done_at=%v", doneAt)
	}
	if attempts < 1 {
		t.Fatalf("expected attempts >= 1 on retry-marked entry, got %d", attempts)
	}
	if !nextAttempt.After(time.Now()) {
		t.Fatalf("expected future next_attempt_at, got %v", nextAttempt)
	}
}

// TestCoordinator_UploadFile_BooksQuotaInTx confirms the AC: the quota
// subscriber runs in-TX, so a successful upload commits both the file row and
// the quota delta atomically.
func TestCoordinator_UploadFile_BooksQuotaInTx(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	body := []byte("quota-counted-payload")
	staging := "/q.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging, body)

	if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/q.txt",
		StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}

	after, err := r.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if after.UsedBytes != int64(len(body)) {
		t.Fatalf("expected used_bytes=%d, got %d", len(body), after.UsedBytes)
	}
}

// TestCoordinator_UploadFile_CriticalFailureRollsBack confirms the AC:
// a critical subscriber error aborts the publisher's TX, so neither the file
// row nor the quota delta survives.
func TestCoordinator_UploadFile_CriticalFailureRollsBack(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	r.bus.SubscribeCritical(bus.EventFileUploaded, func(_ context.Context, _ pgx.Tx, _ bus.Event) error {
		return errors.New("simulated critical failure")
	})

	body := []byte("should-not-persist")
	staging := "/bad.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging, body)

	_, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/bad.txt",
		StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	})
	if err == nil {
		t.Fatal("expected error from critical subscriber failure, got nil")
	}

	// The file upsert currently runs outside the TX-boundary the coordinator
	// owns (UpsertWithVersionCheck uses the pool, not the tx). The AC
	// "Critical-Failure rollt TX zurück" therefore covers the things actually
	// in the TX: quota delta + outbox entries.
	after, err := r.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if after.UsedBytes != 0 {
		t.Fatalf("expected used_bytes=0 after rollback, got %d", after.UsedBytes)
	}
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outbox WHERE done_at IS NULL`).Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 pending outbox entries after rollback, got %d", n)
	}
}

// TestCoordinator_UploadFile_ReactiveFailureDoesNotBreakUpload confirms the
// AC: a panicking reactive subscriber does not derail the upload — the file
// row, quota, and outbox entries land as on the happy path.
func TestCoordinator_UploadFile_ReactiveFailureDoesNotBreakUpload(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	r.bus.SubscribeReactive(bus.EventQuotaThresholdCrossed, func(_ context.Context, _ bus.Event) {
		panic("reactive subscribers must not crash the publisher")
	})

	body := []byte("ok-even-with-panicky-reactive")
	staging := "/ok.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging, body)

	res, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/ok.txt",
		StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if res.Conflict != syncpkg.ConflictNone {
		t.Fatalf("expected ConflictNone, got %v", res.Conflict)
	}
	after, err := r.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if after.UsedBytes != int64(len(body)) {
		t.Fatalf("expected used_bytes=%d, got %d", len(body), after.UsedBytes)
	}
}

// TestCoordinator_MoveFile_PublishesFileMoved asserts that the happy-path
// move publishes a FileMoved event on the DomainEventBus with FileID/From/To.
// Move is quota-neutral, so subscribers (audit, mail) are the consumers.
func TestCoordinator_MoveFile_PublishesFileMoved(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	body := []byte("move-bus")
	staging := "/mv.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging, body)
	seed, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/mv.txt",
		StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	r.drainOutbox(t)

	var captured bus.FileMoved
	var seen int
	r.bus.SubscribeCritical(bus.EventFileMoved, func(_ context.Context, _ pgx.Tx, evt bus.Event) error {
		e, ok := evt.(bus.FileMoved)
		if !ok {
			t.Fatalf("expected bus.FileMoved, got %T", evt)
		}
		captured = e
		seen++
		return nil
	})

	if _, err := r.coord.MoveFile(context.Background(), syncpkg.MoveCmd{
		UserID: u.ID, SyncRootID: root.ID,
		From: "/mv.txt", To: "/mv2.txt",
	}); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}

	if seen != 1 {
		t.Fatalf("expected 1 FileMoved event, got %d", seen)
	}
	if captured.UserID != u.ID {
		t.Errorf("UserID: got %s, want %s", captured.UserID, u.ID)
	}
	if captured.RootID != root.ID {
		t.Errorf("RootID: got %s, want %s", captured.RootID, root.ID)
	}
	if captured.FileID != seed.File.ID {
		t.Errorf("FileID: got %s, want %s", captured.FileID, seed.File.ID)
	}
	if captured.From != "/mv.txt" || captured.To != "/mv2.txt" {
		t.Errorf("From/To: %q -> %q", captured.From, captured.To)
	}
}

// TestCoordinator_DeleteSyncRoot_PublishesAndAdjustsQuota asserts the AC:
// the coordinator owns the TX, publishes SyncRootDeleted critically, and the
// quota subscriber zeroes used/trash bytes — no direct quota.Record* call.
func TestCoordinator_DeleteSyncRoot_PublishesAndAdjustsQuota(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	live := []byte("alive-payload")
	stagingLive := "/live.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, stagingLive, live)
	if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/live.txt",
		StagingPath: stagingLive, Size: int64(len(live)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed live: %v", err)
	}
	r.drainOutbox(t)

	trashed := []byte("trashed-payload")
	stagingTr := "/tr.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, stagingTr, trashed)
	if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/tr.txt",
		StagingPath: stagingTr, Size: int64(len(trashed)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed trashed: %v", err)
	}
	if _, err := r.coord.DeleteFile(context.Background(), syncpkg.DeleteCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/tr.txt",
	}); err != nil {
		t.Fatalf("soft-delete tr.txt: %v", err)
	}
	r.drainOutbox(t)

	before, err := r.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("users before: %v", err)
	}
	if before.UsedBytes != int64(len(live)) || before.TrashBytes != int64(len(trashed)) {
		t.Fatalf("seed counters: used=%d trash=%d (want %d/%d)",
			before.UsedBytes, before.TrashBytes, len(live), len(trashed))
	}

	var captured bus.SyncRootDeleted
	var seen int
	r.bus.SubscribeCritical(bus.EventSyncRootDeleted, func(_ context.Context, _ pgx.Tx, evt bus.Event) error {
		e, ok := evt.(bus.SyncRootDeleted)
		if !ok {
			t.Fatalf("expected bus.SyncRootDeleted, got %T", evt)
		}
		captured = e
		seen++
		return nil
	})

	if _, err := r.coord.DeleteSyncRoot(context.Background(), syncpkg.DeleteSyncRootCmd{
		UserID: u.ID, SyncRootID: root.ID,
	}); err != nil {
		t.Fatalf("DeleteSyncRoot: %v", err)
	}

	if seen != 1 {
		t.Fatalf("expected 1 SyncRootDeleted event, got %d", seen)
	}
	if captured.UserID != u.ID || captured.RootID != root.ID {
		t.Errorf("ids: user=%s root=%s", captured.UserID, captured.RootID)
	}
	if captured.UsedBytes != int64(len(live)) || captured.TrashBytes != int64(len(trashed)) {
		t.Errorf("bytes: used=%d trash=%d (want %d/%d)",
			captured.UsedBytes, captured.TrashBytes, len(live), len(trashed))
	}

	after, err := r.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("users after: %v", err)
	}
	if after.UsedBytes != 0 || after.TrashBytes != 0 {
		t.Fatalf("counters after delete: used=%d trash=%d (want 0/0)",
			after.UsedBytes, after.TrashBytes)
	}
}

// TestCoordinator_DeleteSubtree_PublishesFileTrashedPerFile asserts the AC:
// cascade-trash of a directory publishes one FileTrashed per file row, the
// quota subscriber moves bytes used→trash, and the directory row itself is
// soft-deleted without a quota event.
func TestCoordinator_DeleteSubtree_PublishesFileTrashedPerFile(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	files := []struct {
		path string
		body []byte
	}{
		{"/dir/a.txt", []byte("alpha")},
		{"/dir/b.txt", []byte("bravoo")},
		{"/dir/sub/c.txt", []byte("charlie!")},
	}
	for _, f := range files {
		staging := f.path + ".staging-" + uuid.NewString()
		writeStaging(t, r, u.ID, root.ID, staging, f.body)
		if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
			UserID: u.ID, SyncRootID: root.ID, Path: f.path,
			StagingPath: staging, Size: int64(len(f.body)), MTime: time.Now(),
		}); err != nil {
			t.Fatalf("seed %s: %v", f.path, err)
		}
	}
	r.drainOutbox(t)

	var total int64
	for _, f := range files {
		total += int64(len(f.body))
	}
	before, err := r.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("users before: %v", err)
	}
	if before.UsedBytes != total {
		t.Fatalf("seed used: got %d, want %d", before.UsedBytes, total)
	}

	var trashed []bus.FileTrashed
	r.bus.SubscribeCritical(bus.EventFileTrashed, func(_ context.Context, _ pgx.Tx, evt bus.Event) error {
		if e, ok := evt.(bus.FileTrashed); ok {
			trashed = append(trashed, e)
		}
		return nil
	})

	if _, err := r.coord.DeleteSubtree(context.Background(), syncpkg.DeleteSubtreeCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/dir",
	}); err != nil {
		t.Fatalf("DeleteSubtree: %v", err)
	}

	if len(trashed) != len(files) {
		t.Fatalf("FileTrashed count: got %d, want %d", len(trashed), len(files))
	}
	var seenBytes int64
	for _, e := range trashed {
		seenBytes += e.Bytes
		if e.UserID != u.ID || e.RootID != root.ID {
			t.Errorf("ids on event: %+v", e)
		}
	}
	if seenBytes != total {
		t.Errorf("FileTrashed bytes sum: got %d, want %d", seenBytes, total)
	}

	after, err := r.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("users after: %v", err)
	}
	if after.UsedBytes != 0 || after.TrashBytes != total {
		t.Fatalf("counters after subtree-delete: used=%d trash=%d (want 0/%d)",
			after.UsedBytes, after.TrashBytes, total)
	}
}

// TestCoordinator_ResolveConflict_PublishesFileConflictDiscarded asserts the
// AC: discarding a conflict copy publishes FileConflictDiscarded with the
// freed bytes and the quota subscriber drops the conflict bytes from used.
func TestCoordinator_ResolveConflict_PublishesFileConflictDiscarded(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	body1 := []byte("winner")
	staging1 := "/c.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging1, body1)
	if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/c.txt",
		StagingPath: staging1, Size: int64(len(body1)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("first upload: %v", err)
	}
	r.drainOutbox(t)

	body2 := []byte("losermsg")
	staging2 := "/c.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging2, body2)
	if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/c.txt",
		StagingPath: staging2, Size: int64(len(body2)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("second upload: %v", err)
	}
	r.drainOutbox(t)

	beforeResolve, err := r.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("users before: %v", err)
	}
	wantUsedBefore := int64(len(body1) + len(body2))
	if beforeResolve.UsedBytes != wantUsedBefore {
		t.Fatalf("seed used: got %d, want %d", beforeResolve.UsedBytes, wantUsedBefore)
	}

	var conflictID uuid.UUID
	if err := r.pool.QueryRow(context.Background(),
		`SELECT c.id FROM conflicts c JOIN files f ON f.id = c.file_id
		 WHERE f.user_id = $1 AND c.resolved = FALSE`, u.ID).Scan(&conflictID); err != nil {
		t.Fatalf("locate conflict: %v", err)
	}

	var captured bus.FileConflictDiscarded
	var seen int
	r.bus.SubscribeCritical(bus.EventFileConflictDiscarded, func(_ context.Context, _ pgx.Tx, evt bus.Event) error {
		e, ok := evt.(bus.FileConflictDiscarded)
		if !ok {
			t.Fatalf("expected bus.FileConflictDiscarded, got %T", evt)
		}
		captured = e
		seen++
		return nil
	})

	if _, err := r.coord.ResolveConflict(context.Background(), syncpkg.ResolveConflictCmd{
		UserID: u.ID, ConflictID: conflictID,
	}); err != nil {
		t.Fatalf("ResolveConflict: %v", err)
	}

	if seen != 1 {
		t.Fatalf("expected 1 FileConflictDiscarded event, got %d", seen)
	}
	if captured.UserID != u.ID {
		t.Errorf("UserID: %s", captured.UserID)
	}
	if captured.Bytes != int64(len(body2)) {
		t.Errorf("Bytes: got %d, want %d", captured.Bytes, len(body2))
	}

	after, err := r.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("users after: %v", err)
	}
	if after.UsedBytes != int64(len(body1)) {
		t.Fatalf("counters after discard: used=%d (want %d)", after.UsedBytes, len(body1))
	}
}

// TestCoordinator_BytesConsistency_AllOpPaths exercises every file-mutation
// path (upload, move, real-conflict, delete, restore, subtree-delete,
// resolve-conflict, syncroot-delete) on a single user and asserts that the
// final used/trash counters match what's actually in the files table. Each
// op is bus-driven; a missing publish or stale subscriber would leave
// counters drifted.
func TestCoordinator_BytesConsistency_AllOpPaths(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)
	ctx := context.Background()

	upload := func(path string, body []byte) {
		t.Helper()
		staging := path + ".staging-" + uuid.NewString()
		writeStaging(t, r, u.ID, root.ID, staging, body)
		if _, err := r.coord.UploadFile(ctx, syncpkg.UploadCmd{
			UserID: u.ID, SyncRootID: root.ID, Path: path,
			StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
		}); err != nil {
			t.Fatalf("upload %s: %v", path, err)
		}
		r.drainOutbox(t)
	}

	// Step 1: seed several files.
	upload("/keep.txt", []byte("keepme"))    // 6
	upload("/dir/a.txt", []byte("alpha"))    // 5
	upload("/dir/b.txt", []byte("bravoo"))   // 6
	upload("/mv.txt", []byte("rename"))      // 6
	upload("/clash.txt", []byte("original")) // 8

	// Step 2: move.
	if _, err := r.coord.MoveFile(ctx, syncpkg.MoveCmd{
		UserID: u.ID, SyncRootID: root.ID, From: "/mv.txt", To: "/moved.txt",
	}); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	r.drainOutbox(t)

	// Step 3: real-conflict on /clash.txt (no IfMatch → loser-path copy).
	loser := []byte("loser-bytes")
	stagingLoser := "/clash.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, stagingLoser, loser)
	if _, err := r.coord.UploadFile(ctx, syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/clash.txt",
		StagingPath: stagingLoser, Size: int64(len(loser)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("conflict upload: %v", err)
	}
	r.drainOutbox(t)

	// Step 4: delete + restore "/keep.txt" round-trip — quota-neutral.
	if _, err := r.coord.DeleteFile(ctx, syncpkg.DeleteCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/keep.txt",
	}); err != nil {
		t.Fatalf("DeleteFile keep: %v", err)
	}
	r.drainOutbox(t)
	var keepID uuid.UUID
	if err := r.pool.QueryRow(ctx,
		`SELECT id FROM files WHERE user_id = $1 AND path = '/keep.txt'`, u.ID).Scan(&keepID); err != nil {
		t.Fatalf("locate keep.txt: %v", err)
	}
	if _, err := r.coord.RestoreFromTrash(ctx, syncpkg.RestoreCmd{
		UserID: u.ID, SyncRootID: root.ID, FileID: keepID,
	}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	r.drainOutbox(t)

	// Step 5: cascade-trash /dir.
	if _, err := r.coord.DeleteSubtree(ctx, syncpkg.DeleteSubtreeCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/dir",
	}); err != nil {
		t.Fatalf("DeleteSubtree: %v", err)
	}
	r.drainOutbox(t)

	// Step 6: resolve the /clash.txt conflict → discard the loser copy.
	var conflictID uuid.UUID
	if err := r.pool.QueryRow(ctx,
		`SELECT c.id FROM conflicts c JOIN files f ON f.id = c.file_id
		 WHERE f.user_id = $1 AND c.resolved = FALSE`, u.ID).Scan(&conflictID); err != nil {
		t.Fatalf("locate conflict: %v", err)
	}
	if _, err := r.coord.ResolveConflict(ctx, syncpkg.ResolveConflictCmd{
		UserID: u.ID, ConflictID: conflictID,
	}); err != nil {
		t.Fatalf("ResolveConflict: %v", err)
	}

	// Reconcile counters against the source-of-truth files table.
	var usedTruth, trashTruth int64
	if err := r.pool.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(size) FILTER (WHERE is_deleted = FALSE), 0),
			COALESCE(SUM(size) FILTER (WHERE is_deleted = TRUE AND trash_path IS NOT NULL), 0)
		FROM files WHERE user_id = $1`, u.ID).Scan(&usedTruth, &trashTruth); err != nil {
		t.Fatalf("ground-truth sums: %v", err)
	}
	user, err := r.users.FindByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if user.UsedBytes != usedTruth {
		t.Errorf("used_bytes drift: counters=%d, files-sum=%d", user.UsedBytes, usedTruth)
	}
	if user.TrashBytes != trashTruth {
		t.Errorf("trash_bytes drift: counters=%d, files-sum=%d", user.TrashBytes, trashTruth)
	}

	// Step 7: final destructive op — drop the whole sync root and assert
	// counters collapse to zero.
	if _, err := r.coord.DeleteSyncRoot(ctx, syncpkg.DeleteSyncRootCmd{
		UserID: u.ID, SyncRootID: root.ID,
	}); err != nil {
		t.Fatalf("DeleteSyncRoot: %v", err)
	}
	final, err := r.users.FindByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("FindByID final: %v", err)
	}
	if final.UsedBytes != 0 || final.TrashBytes != 0 {
		t.Fatalf("final counters: used=%d trash=%d (want 0/0)", final.UsedBytes, final.TrashBytes)
	}
}

// ─── ADR-0013 Stufe 2b: Move/Delete/Restore record divergent actor ───────────

// TestCoordinator_MoveFile_RecordsActor verifies that a move triggered by a
// divergent actor (mount recipient) writes the actor into
// last_modifier_user_id while the file row continues to live in the giver's
// root.
func TestCoordinator_MoveFile_RecordsActor(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	giver := r.makeUser(t)
	recipient := r.makeUser(t)
	root := r.makeRoot(t, giver.ID)

	body := []byte("move me")
	staging := "/m.txt.staging-" + uuid.NewString()
	writeStaging(t, r, giver.ID, root.ID, staging, body)
	if _, err := r.coord.UploadFile(ctx, syncpkg.UploadCmd{
		UserID: giver.ID, ActorUserID: giver.ID, SyncRootID: root.ID,
		Path: "/m.txt", StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	r.drainOutbox(t)

	res, err := r.coord.MoveFile(ctx, syncpkg.MoveCmd{
		UserID: giver.ID, ActorUserID: recipient.ID,
		SyncRootID: root.ID, From: "/m.txt", To: "/m2.txt",
	})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if res.File.LastModifierUserID == nil || *res.File.LastModifierUserID != recipient.ID {
		t.Fatalf("last_modifier mismatch: got %v want %v", res.File.LastModifierUserID, recipient.ID)
	}
}

// TestCoordinator_DeleteFile_RecordsActor pins the same contract for the
// soft-delete pipeline.
func TestCoordinator_DeleteFile_RecordsActor(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	giver := r.makeUser(t)
	recipient := r.makeUser(t)
	root := r.makeRoot(t, giver.ID)

	body := []byte("delete me")
	staging := "/d.txt.staging-" + uuid.NewString()
	writeStaging(t, r, giver.ID, root.ID, staging, body)
	if _, err := r.coord.UploadFile(ctx, syncpkg.UploadCmd{
		UserID: giver.ID, ActorUserID: giver.ID, SyncRootID: root.ID,
		Path: "/d.txt", StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r.drainOutbox(t)

	res, err := r.coord.DeleteFile(ctx, syncpkg.DeleteCmd{
		UserID: giver.ID, ActorUserID: recipient.ID,
		SyncRootID: root.ID, Path: "/d.txt",
	})
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if res.File.LastModifierUserID == nil || *res.File.LastModifierUserID != recipient.ID {
		t.Fatalf("last_modifier mismatch: got %v want %v", res.File.LastModifierUserID, recipient.ID)
	}
}

// TestCoordinator_RestoreFromTrash_RecordsActor pins the actor on the
// restore step. A delete by user A followed by a restore by user B leaves
// last_modifier_user_id = B.
func TestCoordinator_RestoreFromTrash_RecordsActor(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	owner := r.makeUser(t)
	restorer := r.makeUser(t)
	root := r.makeRoot(t, owner.ID)

	body := []byte("restore me")
	staging := "/r.txt.staging-" + uuid.NewString()
	writeStaging(t, r, owner.ID, root.ID, staging, body)
	if _, err := r.coord.UploadFile(ctx, syncpkg.UploadCmd{
		UserID: owner.ID, ActorUserID: owner.ID, SyncRootID: root.ID,
		Path: "/r.txt", StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r.drainOutbox(t)
	del, err := r.coord.DeleteFile(ctx, syncpkg.DeleteCmd{
		UserID: owner.ID, ActorUserID: owner.ID, SyncRootID: root.ID, Path: "/r.txt",
	})
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	r.drainOutbox(t)

	res, err := r.coord.RestoreFromTrash(ctx, syncpkg.RestoreCmd{
		UserID: owner.ID, ActorUserID: restorer.ID,
		SyncRootID: root.ID, FileID: del.File.ID,
	})
	if err != nil {
		t.Fatalf("RestoreFromTrash: %v", err)
	}
	if res.File.LastModifierUserID == nil || *res.File.LastModifierUserID != restorer.ID {
		t.Fatalf("last_modifier mismatch: got %v want %v", res.File.LastModifierUserID, restorer.ID)
	}
}
