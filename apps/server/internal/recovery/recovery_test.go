package recovery_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/db"
	"privasync/server/internal/model"
	"privasync/server/internal/recovery"
	"privasync/server/internal/repository"
	"privasync/server/internal/storage"
)

// ─── Test infra ───────────────────────────────────────────────────────────────

var (
	testPoolOnce sync.Once
	testPool     *pgxpool.Pool
	testPoolErr  error
)

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

// deleteUser purges all rows for a user via CASCADE.  Used both for cleanup
// and to simulate "DB lost" before recovery.
func deleteUser(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		t.Logf("cleanup user %s: %v", userID, err)
	}
}

type fixture struct {
	pool      *pgxpool.Pool
	dataDir   string
	stor      *storage.Service
	users     *repository.UserRepository
	files     *repository.FileRepository
	syncRoots *repository.SyncRootRepository
	shares    *repository.ShareRepository
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := getTestPool(t)
	dir := t.TempDir()
	stor := storage.NewService(dir)
	return &fixture{
		pool:      pool,
		dataDir:   dir,
		stor:      stor,
		users:     repository.NewUserRepository(pool, stor),
		files:     repository.NewFileRepository(pool),
		syncRoots: repository.NewSyncRootRepository(pool, stor),
		shares:    repository.NewShareRepository(pool, stor),
	}
}

func (fx *fixture) createUser(t *testing.T) *model.User {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	u, err := fx.users.Create(context.Background(),
		"recov_"+suffix, "recov_"+suffix+"@test.local", "hashed-pw", model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { deleteUser(t, fx.pool, u.ID) })
	return u
}

// ─── T1: Userdaten round-trip ─────────────────────────────────────────────────

func TestRecover_UserRoundTrip_PreservesAllPersistedFields(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	u := fx.createUser(t)

	// Mutate every persisted setting so defaults can't accidentally satisfy
	// the assertion.
	if err := fx.users.SetQuota(ctx, u.ID, 9_000_000_000); err != nil {
		t.Fatalf("set quota: %v", err)
	}
	if err := fx.users.SetRole(ctx, u.ID, model.RoleAdmin); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if err := fx.users.SetAvatarPath(ctx, u.ID, "av/abc.png"); err != nil {
		t.Fatalf("set avatar: %v", err)
	}
	if err := fx.users.SetPreferredLanguage(ctx, u.ID, "en"); err != nil {
		t.Fatalf("set lang: %v", err)
	}
	if err := fx.users.SetShowFileTree(ctx, u.ID, false); err != nil {
		t.Fatalf("set show file tree: %v", err)
	}
	if err := fx.users.SetTrashView(ctx, u.ID, "tree"); err != nil {
		t.Fatalf("set trash view: %v", err)
	}

	// "Lose" the DB.  CASCADE wipes related rows; meta JSON files survive.
	if _, err := fx.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID); err != nil {
		t.Fatalf("wipe user: %v", err)
	}

	if _, err := recovery.RunAll(ctx, fx.pool, fx.dataDir); err != nil {
		t.Fatalf("recovery: %v", err)
	}

	got, err := fx.users.FindByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("find recovered user: %v", err)
	}
	if got.Username != u.Username {
		t.Errorf("username: got %q want %q", got.Username, u.Username)
	}
	if got.Email != u.Email {
		t.Errorf("email: got %q want %q", got.Email, u.Email)
	}
	if got.PasswordHash != "hashed-pw" {
		t.Errorf("password hash not preserved: %q", got.PasswordHash)
	}
	if got.QuotaBytes != 9_000_000_000 {
		t.Errorf("quota: got %d want 9_000_000_000", got.QuotaBytes)
	}
	if got.Role != model.RoleAdmin {
		t.Errorf("role: got %q want admin", got.Role)
	}
	if got.AvatarPath == nil || *got.AvatarPath != "av/abc.png" {
		t.Errorf("avatar_path not preserved: %v", got.AvatarPath)
	}
	if got.PreferredLanguage != "en" {
		t.Errorf("preferred_language: got %q want en", got.PreferredLanguage)
	}
	if got.ShowFileTree != false {
		t.Errorf("show_file_tree: got %v want false", got.ShowFileTree)
	}
	if got.TrashView != "tree" {
		t.Errorf("trash_view: got %q want tree", got.TrashView)
	}
}

// ─── T2: SyncRoot round-trip ──────────────────────────────────────────────────

func TestRecover_SyncRootRoundTrip(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	u := fx.createUser(t)

	sr, err := fx.syncRoots.CreateSyncRoot(ctx, u.ID, "Docs", "/Docs")
	if err != nil {
		t.Fatalf("create root: %v", err)
	}

	// Wipe sync root row only — keep user.
	if _, err := fx.pool.Exec(ctx, `DELETE FROM sync_roots WHERE id = $1`, sr.ID); err != nil {
		t.Fatalf("wipe root: %v", err)
	}

	if _, err := recovery.RunAll(ctx, fx.pool, fx.dataDir); err != nil {
		t.Fatalf("recovery: %v", err)
	}

	got, err := fx.syncRoots.GetSyncRoot(ctx, sr.ID, u.ID)
	if err != nil {
		t.Fatalf("get recovered root: %v", err)
	}
	if got.Name != "Docs" || got.Path != "/Docs" || got.UserID != u.ID {
		t.Errorf("root mismatch: %+v", got)
	}
}

// ─── T3: Share round-trip incl target_user_id ─────────────────────────────────

func TestRecover_ShareRoundTrip_PreservesTargetUser(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	owner := fx.createUser(t)
	target := fx.createUser(t)

	sr, err := fx.syncRoots.CreateSyncRoot(ctx, owner.ID, "Shared", "/Shared")
	if err != nil {
		t.Fatalf("create root: %v", err)
	}

	tok := "tok-" + uuid.NewString()
	exp := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	share := &model.Share{
		UserID:       owner.ID,
		SyncRootID:   &sr.ID,
		TargetUserID: &target.ID,
		Path:         "/Shared/file.txt",
		ShareType:    model.ShareTypeUser,
		Token:        &tok,
		ExpiresAt:    &exp,
		CanWrite:     true,
	}
	created, err := fx.shares.Create(ctx, share)
	if err != nil {
		t.Fatalf("create share: %v", err)
	}

	if _, err := fx.pool.Exec(ctx, `DELETE FROM shares WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("wipe share: %v", err)
	}

	if _, err := recovery.RunAll(ctx, fx.pool, fx.dataDir); err != nil {
		t.Fatalf("recovery: %v", err)
	}

	got, err := fx.shares.FindByToken(ctx, tok)
	if err != nil {
		t.Fatalf("find recovered share: %v", err)
	}
	if got.UserID != owner.ID {
		t.Errorf("owner: got %s want %s", got.UserID, owner.ID)
	}
	if got.TargetUserID == nil || *got.TargetUserID != target.ID {
		t.Errorf("target_user_id not preserved: %v", got.TargetUserID)
	}
	if got.SyncRootID == nil || *got.SyncRootID != sr.ID {
		t.Errorf("sync_root_id not preserved: %v", got.SyncRootID)
	}
	if got.Path != "/Shared/file.txt" || !got.CanWrite || got.ShareType != model.ShareTypeUser {
		t.Errorf("share fields mismatch: %+v", got)
	}
}

// ─── T4: DeleteSyncRoot cleans meta files ─────────────────────────────────────

func TestDeleteSyncRoot_RemovesMetaFiles(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	u := fx.createUser(t)

	sr, err := fx.syncRoots.CreateSyncRoot(ctx, u.ID, "Tmp", "/Tmp")
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	tok := "tok-" + uuid.NewString()
	sh, err := fx.shares.Create(ctx, &model.Share{
		UserID:     u.ID,
		SyncRootID: &sr.ID,
		Path:       "/Tmp/x",
		ShareType:  model.ShareTypePublic,
		Token:      &tok,
	})
	if err != nil {
		t.Fatalf("create share: %v", err)
	}

	rootMeta := filepath.Join(fx.dataDir, "sync_roots", sr.ID.String()+".json")
	shareMeta := filepath.Join(fx.dataDir, "shares", sh.ID.String()+".json")
	if _, err := os.Stat(rootMeta); err != nil {
		t.Fatalf("root meta should exist pre-delete: %v", err)
	}
	if _, err := os.Stat(shareMeta); err != nil {
		t.Fatalf("share meta should exist pre-delete: %v", err)
	}

	if err := fx.syncRoots.DeleteSyncRoot(ctx, sr.ID, u.ID); err != nil {
		t.Fatalf("delete root: %v", err)
	}

	if _, err := os.Stat(rootMeta); !os.IsNotExist(err) {
		t.Errorf("root meta still exists after delete: %v", err)
	}
	if _, err := os.Stat(shareMeta); !os.IsNotExist(err) {
		t.Errorf("share meta still exists after cascaded delete: %v", err)
	}
}

// ─── T5: User setters write meta ──────────────────────────────────────────────
//
// Each setter touched in this test should re-write the user's JSON snapshot.
// Asserted by reading the file's mtime back: it must advance after the call.

func TestUserSetters_PersistMetaToDisk(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	u := fx.createUser(t)
	metaPath := filepath.Join(fx.dataDir, "users", u.ID.String()+".json")

	cases := []struct {
		name string
		mut  func(t *testing.T)
		want func(m string) bool
	}{
		{
			name: "SetAvatarPath",
			mut:  func(t *testing.T) { mustNoErr(t, fx.users.SetAvatarPath(ctx, u.ID, "av/x.png")) },
			want: func(m string) bool { return strings.Contains(m, `"avatar_path": "av/x.png"`) },
		},
		{
			name: "SetPreferredLanguage",
			mut:  func(t *testing.T) { mustNoErr(t, fx.users.SetPreferredLanguage(ctx, u.ID, "en")) },
			want: func(m string) bool { return strings.Contains(m, `"preferred_language": "en"`) },
		},
		{
			name: "SetShowFileTree",
			mut:  func(t *testing.T) { mustNoErr(t, fx.users.SetShowFileTree(ctx, u.ID, false)) },
			want: func(m string) bool { return strings.Contains(m, `"show_file_tree": false`) },
		},
		{
			name: "SetTrashView",
			mut:  func(t *testing.T) { mustNoErr(t, fx.users.SetTrashView(ctx, u.ID, "tree")) },
			want: func(m string) bool { return strings.Contains(m, `"trash_view": "tree"`) },
		},
		{
			name: "ClearAvatarPath",
			mut:  func(t *testing.T) { mustNoErr(t, fx.users.ClearAvatarPath(ctx, u.ID)) },
			want: func(m string) bool { return !strings.Contains(m, `"avatar_path"`) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.mut(t)
			data, err := os.ReadFile(metaPath)
			if err != nil {
				t.Fatalf("read meta: %v", err)
			}
			if !tc.want(string(data)) {
				t.Errorf("meta does not reflect %s:\n%s", tc.name, data)
			}
		})
	}
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("setter: %v", err)
	}
}

// ─── T6: Empty-dir recovery ───────────────────────────────────────────────────

func TestRecover_EmptyDirectoryRestored(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	u := fx.createUser(t)
	sr, err := fx.syncRoots.CreateSyncRoot(ctx, u.ID, "Dirs", "/Dirs")
	if err != nil {
		t.Fatalf("create root: %v", err)
	}

	// Create an empty directory both on disk and as a DB row.
	mtime := time.Now().UTC().Truncate(time.Second)
	if _, err := fx.files.CreateDirectory(ctx, u.ID, sr.ID, "/empty-folder", mtime); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	if err := fx.stor.MakeDir(u.ID, sr.ID.String(), "/empty-folder"); err != nil {
		t.Fatalf("mkdir on disk: %v", err)
	}

	// Wipe all file rows and the sync_root row, then re-insert root via recover.
	if _, err := fx.pool.Exec(ctx, `DELETE FROM files WHERE sync_root_id = $1`, sr.ID); err != nil {
		t.Fatalf("wipe files: %v", err)
	}

	if _, _, err := recovery.RecoverFiles(ctx, fx.pool, fx.dataDir); err != nil {
		t.Fatalf("recover files: %v", err)
	}

	var isDir bool
	err = fx.pool.QueryRow(ctx,
		`SELECT is_dir FROM files WHERE sync_root_id = $1 AND path = $2`,
		sr.ID, "/empty-folder",
	).Scan(&isDir)
	if err != nil {
		t.Fatalf("query recovered dir: %v", err)
	}
	if !isDir {
		t.Errorf("recovered row for /empty-folder should have is_dir=TRUE")
	}
}

// ─── T7: Trashed-file recovery ────────────────────────────────────────────────
//
// Exercises the path RecoverFiles takes for a file sitting under the root's
// .trash subtree: hashFile (content hash recomputed from disk), the
// deleted-at timestamp round-trip through parseTrashTimestamp, and the
// recoverTrash insert itself. None of the other recovery tests put a real
// file on disk, so this is the only coverage for that branch.
func TestRecover_TrashedFileRestored(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	u := fx.createUser(t)
	sr, err := fx.syncRoots.CreateSyncRoot(ctx, u.ID, "Trashed", "/Trashed")
	if err != nil {
		t.Fatalf("create root: %v", err)
	}

	content := []byte("recoverable trash content")
	written, hash, err := fx.stor.WriteFile(u.ID, sr.ID.String(), "/doc.txt", strings.NewReader(string(content)))
	if err != nil {
		t.Fatalf("write file: %v", err)
	}
	if written != int64(len(content)) {
		t.Fatalf("written = %d, want %d", written, len(content))
	}
	if _, err := fx.stor.TrashFile(u.ID, sr.ID.String(), "/doc.txt"); err != nil {
		t.Fatalf("trash file: %v", err)
	}

	if _, _, err := recovery.RecoverFiles(ctx, fx.pool, fx.dataDir); err != nil {
		t.Fatalf("recover files: %v", err)
	}

	var (
		gotContentHash []byte
		gotIsDeleted   bool
		gotDeletedAt   time.Time
		gotTrashPath   string
	)
	err = fx.pool.QueryRow(ctx,
		`SELECT content_hash, is_deleted, deleted_at, trash_path
		   FROM files WHERE sync_root_id = $1 AND path = $2 AND is_deleted = TRUE`,
		sr.ID, "/doc.txt",
	).Scan(&gotContentHash, &gotIsDeleted, &gotDeletedAt, &gotTrashPath)
	if err != nil {
		t.Fatalf("query recovered trash row: %v", err)
	}
	if !gotIsDeleted {
		t.Error("recovered trash row should have is_deleted=TRUE")
	}
	if !bytes.Equal(gotContentHash, hash) {
		t.Errorf("content_hash mismatch: got %x want %x", gotContentHash, hash)
	}
	if gotDeletedAt.IsZero() {
		t.Error("deleted_at should be parsed from the trash folder's timestamp segment")
	}
	if !strings.Contains(gotTrashPath, ".trash/") {
		t.Errorf("trash_path = %q, want it to contain .trash/", gotTrashPath)
	}
}
