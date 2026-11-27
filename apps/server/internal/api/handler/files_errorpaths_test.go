package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/audit"
	"privasync/server/internal/auth"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

// Drives FileHandler's ErrInternal (500) branches through the narrow role
// interfaces from files.go (ADR-0029). storage/quota/coordinator stay concrete,
// so only the repo-failure branches reachable *before* any storage/coordinator
// call are covered here — that is where the seam adds value. errBoom/assert500/
// assertCode/reqWithClaims/withURLParams are shared across the package.

// ── Fakes ────────────────────────────────────────────────────────────────

type fakeFileStore struct {
	errOn   map[string]error
	entries []model.File
}

func (f fakeFileStore) FindByPath(context.Context, uuid.UUID, string) (*model.File, error) {
	if e := f.errOn["FindByPath"]; e != nil {
		return nil, e
	}
	return &model.File{ID: uuid.New()}, nil
}
func (f fakeFileStore) ListDirectoryLevel(context.Context, repository.ListDirectoryLevelParams) ([]model.File, error) {
	if e := f.errOn["ListDirectoryLevel"]; e != nil {
		return nil, e
	}
	return f.entries, nil
}
func (f fakeFileStore) EachInSubtree(context.Context, uuid.UUID, string, func(model.File) error) error {
	return f.errOn["EachInSubtree"]
}
func (f fakeFileStore) GetChangedSince(context.Context, uuid.UUID, int64, int) ([]model.File, error) {
	if e := f.errOn["GetChangedSince"]; e != nil {
		return nil, e
	}
	return nil, nil
}
func (f fakeFileStore) ListMoveEventsSince(context.Context, uuid.UUID, int64, int) ([]model.MoveEvent, error) {
	if e := f.errOn["ListMoveEventsSince"]; e != nil {
		return nil, e
	}
	return nil, nil
}
func (f fakeFileStore) SearchFiles(context.Context, repository.SearchFilesParams) ([]model.File, error) {
	if e := f.errOn["SearchFiles"]; e != nil {
		return nil, e
	}
	return nil, nil
}
func (f fakeFileStore) DirectoryStats(context.Context, uuid.UUID, string) (repository.DirectoryStats, error) {
	if e := f.errOn["DirectoryStats"]; e != nil {
		return repository.DirectoryStats{}, e
	}
	return repository.DirectoryStats{}, nil
}
func (f fakeFileStore) CreateDirectory(context.Context, uuid.UUID, uuid.UUID, string, time.Time) (*model.File, error) {
	if e := f.errOn["CreateDirectory"]; e != nil {
		return nil, e
	}
	return &model.File{ID: uuid.New()}, nil
}
func (f fakeFileStore) FindByPaths(context.Context, uuid.UUID, []string) ([]model.File, error) {
	if e := f.errOn["FindByPaths"]; e != nil {
		return nil, e
	}
	return nil, nil
}

type fakeSyncRootStore struct {
	errOn map[string]error
	roots []model.SyncRoot
}

func (f fakeSyncRootStore) CreateSyncRoot(_ context.Context, _ uuid.UUID, name, _ string) (*model.SyncRoot, error) {
	if e := f.errOn["CreateSyncRoot"]; e != nil {
		return nil, e
	}
	return &model.SyncRoot{ID: uuid.New(), Name: name}, nil
}
func (f fakeSyncRootStore) GetSyncRoot(_ context.Context, id, userID uuid.UUID) (*model.SyncRoot, error) {
	if e := f.errOn["GetSyncRoot"]; e != nil {
		return nil, e
	}
	return &model.SyncRoot{ID: id, UserID: userID, Path: "root"}, nil
}
func (f fakeSyncRootStore) ListSyncRoots(context.Context, uuid.UUID) ([]model.SyncRoot, error) {
	if e := f.errOn["ListSyncRoots"]; e != nil {
		return nil, e
	}
	return f.roots, nil
}
func (f fakeSyncRootStore) ListSyncRootsWithOwner(context.Context, uuid.UUID) ([]model.SyncRootWithOwner, error) {
	if e := f.errOn["ListSyncRootsWithOwner"]; e != nil {
		return nil, e
	}
	return nil, nil
}
func (f fakeSyncRootStore) UpdateSyncRootName(_ context.Context, rootID, _ uuid.UUID, name string) (*model.SyncRoot, error) {
	if e := f.errOn["UpdateSyncRootName"]; e != nil {
		return nil, e
	}
	return &model.SyncRoot{ID: rootID, Name: name}, nil
}

type fakeConflictStore struct{ errOn map[string]error }

func (f fakeConflictStore) ListOpenConflicts(context.Context, uuid.UUID, uuid.UUID) ([]model.Conflict, error) {
	if e := f.errOn["ListOpenConflicts"]; e != nil {
		return nil, e
	}
	return nil, nil
}
func (f fakeConflictStore) FindOpenConflictDetail(context.Context, uuid.UUID) (*repository.ConflictDetail, error) {
	if e := f.errOn["FindOpenConflictDetail"]; e != nil {
		return nil, e
	}
	return &repository.ConflictDetail{}, nil
}

type fakeTrashStore struct{ errOn map[string]error }

func (f fakeTrashStore) ListTrash(context.Context, uuid.UUID) ([]model.File, error) {
	if e := f.errOn["ListTrash"]; e != nil {
		return nil, e
	}
	return nil, nil
}
func (f fakeTrashStore) FindTrashedByID(_ context.Context, fileID, _ uuid.UUID) (*model.File, error) {
	if e := f.errOn["FindTrashedByID"]; e != nil {
		return nil, e
	}
	return &model.File{ID: fileID}, nil // TrashPath nil → skips storage delete
}
func (f fakeTrashStore) PurgeTrashRecord(context.Context, uuid.UUID) error {
	return f.errOn["PurgeTrashRecord"]
}

type fakeFileShareStore struct{ errOn map[string]error }

func (f fakeFileShareStore) FindMountByGrant(context.Context, uuid.UUID, uuid.UUID) (*model.Share, error) {
	if e := f.errOn["FindMountByGrant"]; e != nil {
		return nil, e
	}
	return &model.Share{ID: uuid.New()}, nil
}
func (f fakeFileShareStore) ListMountsForTarget(context.Context, uuid.UUID) ([]model.SharedMount, error) {
	if e := f.errOn["ListMountsForTarget"]; e != nil {
		return nil, e
	}
	return nil, nil
}

type fakeFileAudits struct{ errOn map[string]error }

func (f fakeFileAudits) Log(context.Context, audit.Event) error { return f.errOn["Log"] }

type fakeFileUsers struct{ errOn map[string]error }

func (f fakeFileUsers) FindSummariesByIDs(context.Context, []uuid.UUID) (map[uuid.UUID]model.UserSummary, error) {
	if e := f.errOn["FindSummariesByIDs"]; e != nil {
		return nil, e
	}
	return map[uuid.UUID]model.UserSummary{}, nil
}

// fileErrEnv assembles a FileHandler over the fakes. storage/quota/coordinator/
// mailer/hub stay nil — no tested branch reaches them.
type fileErrEnv struct {
	files     fakeFileStore
	syncRoots fakeSyncRootStore
	conflicts fakeConflictStore
	trash     fakeTrashStore
	shares    fakeFileShareStore
	users     fakeFileUsers
}

func (e fileErrEnv) handler() *FileHandler {
	return &FileHandler{
		files:     e.files,
		syncRoots: e.syncRoots,
		conflicts: e.conflicts,
		trash:     e.trash,
		users:     e.users,
		shares:    e.shares,
		audits:    fakeFileAudits{},
	}
}

func fileErrClaims() *auth.Claims {
	return &auth.Claims{UserID: uuid.New(), Role: model.RoleUser, TokenType: auth.TokenTypeAccess}
}

func rootReq(method, target string, body string) *http.Request {
	if body == "" {
		return reqWithClaims(method, target, nil, fileErrClaims())
	}
	return reqWithClaims(method, target, bytes.NewBufferString(body), fileErrClaims())
}

// ── files.go ──────────────────────────────────────────────────────────────

func TestFileList_ListDirectoryErrorIs500(t *testing.T) {
	env := fileErrEnv{files: fakeFileStore{errOn: map[string]error{"ListDirectoryLevel": errBoom}}}
	r := withURLParams(rootReq(http.MethodGet, "/files", ""), "rootID", uuid.NewString())
	w := httptest.NewRecorder()
	env.handler().ListFiles(w, r)
	assert500(t, w)
}

func TestFileList_ModifierLookupErrorIs500(t *testing.T) {
	modID := uuid.New()
	env := fileErrEnv{
		files: fakeFileStore{entries: []model.File{
			{ID: uuid.New(), Path: "/a.txt", LastModifierUserID: &modID},
		}},
		users: fakeFileUsers{errOn: map[string]error{"FindSummariesByIDs": errBoom}},
	}
	r := withURLParams(rootReq(http.MethodGet, "/files", ""), "rootID", uuid.NewString())
	w := httptest.NewRecorder()
	env.handler().ListFiles(w, r)
	assert500(t, w)
}

func TestFilesStat_FindByPathsErrorIs500(t *testing.T) {
	env := fileErrEnv{files: fakeFileStore{errOn: map[string]error{"FindByPaths": errBoom}}}
	r := withURLParams(rootReq(http.MethodPost, "/files/stat", `{"paths":["/a.txt"]}`), "rootID", uuid.NewString())
	w := httptest.NewRecorder()
	env.handler().FilesStat(w, r)
	assert500(t, w)
}

func TestFileDirectoryStats_ErrorIs500(t *testing.T) {
	env := fileErrEnv{files: fakeFileStore{errOn: map[string]error{"DirectoryStats": errBoom}}}
	r := withURLParams(rootReq(http.MethodGet, "/files/stats", ""), "rootID", uuid.NewString())
	w := httptest.NewRecorder()
	env.handler().DirectoryStats(w, r)
	assert500(t, w)
}

func TestFileGetChanges_ErrorIs500(t *testing.T) {
	env := fileErrEnv{files: fakeFileStore{errOn: map[string]error{"GetChangedSince": errBoom}}}
	r := withURLParams(rootReq(http.MethodGet, "/changes?feed_version=2", ""), "rootID", uuid.NewString())
	w := httptest.NewRecorder()
	env.handler().GetChanges(w, r)
	assert500(t, w)
}

// ADR-0065: the second query the merged feed depends on gets the same
// 500-on-repo-error coverage as the first.
func TestFileGetChanges_MoveEventsErrorIs500(t *testing.T) {
	env := fileErrEnv{files: fakeFileStore{errOn: map[string]error{"ListMoveEventsSince": errBoom}}}
	r := withURLParams(rootReq(http.MethodGet, "/changes?feed_version=2", ""), "rootID", uuid.NewString())
	w := httptest.NewRecorder()
	env.handler().GetChanges(w, r)
	assert500(t, w)
}

// ── sync_roots_handler.go ──────────────────────────────────────────────────

func TestFileListSyncRoots_ErrorIs500(t *testing.T) {
	env := fileErrEnv{syncRoots: fakeSyncRootStore{errOn: map[string]error{"ListSyncRootsWithOwner": errBoom}}}
	w := httptest.NewRecorder()
	env.handler().ListSyncRoots(w, rootReq(http.MethodGet, "/sync-roots", ""))
	assert500(t, w)
}

func TestFileCreateSyncRoot_ErrorsAre500(t *testing.T) {
	t.Run("repo error", func(t *testing.T) {
		env := fileErrEnv{syncRoots: fakeSyncRootStore{errOn: map[string]error{"CreateSyncRoot": errBoom}}}
		w := httptest.NewRecorder()
		env.handler().CreateSyncRoot(w, rootReq(http.MethodPost, "/sync-roots", `{"name":"Docs"}`))
		assert500(t, w)
	})
	t.Run("conflict is 409", func(t *testing.T) {
		env := fileErrEnv{syncRoots: fakeSyncRootStore{errOn: map[string]error{"CreateSyncRoot": repository.ErrConflict}}}
		w := httptest.NewRecorder()
		env.handler().CreateSyncRoot(w, rootReq(http.MethodPost, "/sync-roots", `{"name":"Docs"}`))
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
		}
	})
}

func TestFileRenameSyncRoot_ErrorsAre500(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"repo error", errBoom, http.StatusInternalServerError},
		{"not found", repository.ErrNotFound, http.StatusNotFound},
		{"conflict", repository.ErrConflict, http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := fileErrEnv{syncRoots: fakeSyncRootStore{errOn: map[string]error{"UpdateSyncRootName": tc.err}}}
			r := withURLParams(rootReq(http.MethodPatch, "/sync-roots/x", `{"name":"Neu"}`), "rootID", uuid.NewString())
			w := httptest.NewRecorder()
			env.handler().RenameSyncRoot(w, r)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

// ── trash.go ───────────────────────────────────────────────────────────────

func TestFileListTrash_ErrorIs500(t *testing.T) {
	env := fileErrEnv{trash: fakeTrashStore{errOn: map[string]error{"ListTrash": errBoom}}}
	r := withURLParams(rootReq(http.MethodGet, "/trash", ""), "rootID", uuid.NewString())
	w := httptest.NewRecorder()
	env.handler().ListTrash(w, r)
	assert500(t, w)
}

func TestFilePurgeTrash_ErrorsAre500(t *testing.T) {
	t.Run("find fails", func(t *testing.T) {
		env := fileErrEnv{trash: fakeTrashStore{errOn: map[string]error{"FindTrashedByID": errBoom}}}
		r := withURLParams(rootReq(http.MethodDelete, "/trash/x", ""), "rootID", uuid.NewString(), "fileID", uuid.NewString())
		w := httptest.NewRecorder()
		env.handler().PurgeTrash(w, r)
		assert500(t, w)
	})
	t.Run("purge record fails", func(t *testing.T) {
		env := fileErrEnv{trash: fakeTrashStore{errOn: map[string]error{"PurgeTrashRecord": errBoom}}}
		r := withURLParams(rootReq(http.MethodDelete, "/trash/x", ""), "rootID", uuid.NewString(), "fileID", uuid.NewString())
		w := httptest.NewRecorder()
		env.handler().PurgeTrash(w, r)
		assert500(t, w)
	})
	t.Run("trashed not found is 404", func(t *testing.T) {
		env := fileErrEnv{trash: fakeTrashStore{errOn: map[string]error{"FindTrashedByID": repository.ErrNotFound}}}
		r := withURLParams(rootReq(http.MethodDelete, "/trash/x", ""), "rootID", uuid.NewString(), "fileID", uuid.NewString())
		w := httptest.NewRecorder()
		env.handler().PurgeTrash(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
		}
	})
}

// ── conflicts.go ───────────────────────────────────────────────────────────

func TestFileListConflicts_ErrorIs500(t *testing.T) {
	env := fileErrEnv{conflicts: fakeConflictStore{errOn: map[string]error{"ListOpenConflicts": errBoom}}}
	r := withURLParams(rootReq(http.MethodGet, "/conflicts", ""), "rootID", uuid.NewString())
	w := httptest.NewRecorder()
	env.handler().ListConflicts(w, r)
	assert500(t, w)
}

func TestFileGetConflict_ErrorIs500(t *testing.T) {
	env := fileErrEnv{conflicts: fakeConflictStore{errOn: map[string]error{"FindOpenConflictDetail": errBoom}}}
	r := withURLParams(rootReq(http.MethodGet, "/conflicts/x", ""), "conflictID", uuid.NewString())
	w := httptest.NewRecorder()
	env.handler().GetConflict(w, r)
	assert500(t, w)
}

// ── files_search.go ────────────────────────────────────────────────────────

func TestFileSearchGlobal_ErrorsAre500(t *testing.T) {
	t.Run("list roots fails", func(t *testing.T) {
		env := fileErrEnv{syncRoots: fakeSyncRootStore{errOn: map[string]error{"ListSyncRoots": errBoom}}}
		w := httptest.NewRecorder()
		env.handler().SearchFilesGlobal(w, rootReq(http.MethodGet, "/search?q=x", ""))
		assert500(t, w)
	})
	t.Run("search fails", func(t *testing.T) {
		env := fileErrEnv{
			syncRoots: fakeSyncRootStore{roots: []model.SyncRoot{{ID: uuid.New(), Name: "R"}}},
			files:     fakeFileStore{errOn: map[string]error{"SearchFiles": errBoom}},
		}
		w := httptest.NewRecorder()
		env.handler().SearchFilesGlobal(w, rootReq(http.MethodGet, "/search?q=x", ""))
		assert500(t, w)
	})
	t.Run("list mounts fails", func(t *testing.T) {
		env := fileErrEnv{shares: fakeFileShareStore{errOn: map[string]error{"ListMountsForTarget": errBoom}}}
		w := httptest.NewRecorder()
		env.handler().SearchFilesGlobal(w, rootReq(http.MethodGet, "/search?q=x", ""))
		assert500(t, w)
	})
}

func TestFileSearchPerRoot_ErrorIs500(t *testing.T) {
	env := fileErrEnv{files: fakeFileStore{errOn: map[string]error{"SearchFiles": errBoom}}}
	r := withURLParams(rootReq(http.MethodGet, "/files/search?q=x", ""), "rootID", uuid.NewString())
	w := httptest.NewRecorder()
	env.handler().SearchFiles(w, r)
	assert500(t, w)
}
