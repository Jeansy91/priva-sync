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

// Error-path tests for ShareHandler (ADR-0029). The share* interfaces let a
// hand-written fake return errBoom on exactly one method so the ErrInternal
// (500) branches — unreachable from a real DB deterministically — get covered.
// errBoom + assert500 + reqWithClaims/withURLParams are shared helpers from
// admin_errorpaths_test.go / testutil_test.go.

// ── Fakes: implement only the narrow share* interfaces ──

// fakeShareStore errors on the method named in errOn; every other method
// returns a benign value so the handler reaches the targeted call. owner is
// used as the aggregate's UserID so owner-gated handlers (Update/DeleteGrant)
// can be driven when the caller's claims match it.
type fakeShareStore struct {
	errOn map[string]error
	owner uuid.UUID
}

func (f fakeShareStore) okShare() *model.Share {
	rid := uuid.New()
	return &model.Share{
		ID:         uuid.New(),
		UserID:     f.owner,
		SyncRootID: &rid,
		Path:       "/",
		ShareType:  model.ShareTypeUser,
		CanWrite:   true,
	}
}

func (f fakeShareStore) Create(context.Context, *model.Share) (*model.Share, error) {
	if e := f.errOn["Create"]; e != nil {
		return nil, e
	}
	return f.okShare(), nil
}
func (f fakeShareStore) EnsureUserShare(context.Context, uuid.UUID, uuid.UUID, string) (*model.Share, error) {
	if e := f.errOn["EnsureUserShare"]; e != nil {
		return nil, e
	}
	return f.okShare(), nil
}
func (f fakeShareStore) EnsurePublicShare(context.Context, uuid.UUID, uuid.UUID, string) (*model.Share, error) {
	if e := f.errOn["EnsurePublicShare"]; e != nil {
		return nil, e
	}
	return f.okShare(), nil
}
func (f fakeShareStore) FindPublicShare(context.Context, uuid.UUID, uuid.UUID, string) (*model.Share, error) {
	if e := f.errOn["FindPublicShare"]; e != nil {
		return nil, e
	}
	return f.okShare(), nil
}
func (f fakeShareStore) SetPublicToken(context.Context, uuid.UUID, uuid.UUID, string, string, *time.Time) (*model.Share, error) {
	if e := f.errOn["SetPublicToken"]; e != nil {
		return nil, e
	}
	return f.okShare(), nil
}
func (f fakeShareStore) ClearPublicToken(context.Context, uuid.UUID, uuid.UUID, string) (*model.Share, error) {
	if e := f.errOn["ClearPublicToken"]; e != nil {
		return nil, e
	}
	return f.okShare(), nil
}
func (f fakeShareStore) FindMountByGrant(context.Context, uuid.UUID, uuid.UUID) (*model.Share, error) {
	if e := f.errOn["FindMountByGrant"]; e != nil {
		return nil, e
	}
	return f.okShare(), nil
}
func (f fakeShareStore) ListByUser(context.Context, uuid.UUID) ([]model.Share, error) {
	return nil, f.errOn["ListByUser"]
}
func (f fakeShareStore) ListMountsForTarget(context.Context, uuid.UUID) ([]model.SharedMount, error) {
	return nil, f.errOn["ListMountsForTarget"]
}
func (f fakeShareStore) ListBySyncRoot(context.Context, uuid.UUID) ([]repository.ShareWithTarget, error) {
	return nil, f.errOn["ListBySyncRoot"]
}
func (f fakeShareStore) FindByID(context.Context, uuid.UUID) (*model.Share, error) {
	if e := f.errOn["FindByID"]; e != nil {
		return nil, e
	}
	return f.okShare(), nil
}
func (f fakeShareStore) FindByToken(context.Context, string) (*model.Share, error) {
	if e := f.errOn["FindByToken"]; e != nil {
		return nil, e
	}
	return f.okShare(), nil
}
func (f fakeShareStore) Delete(context.Context, uuid.UUID, uuid.UUID) error {
	return f.errOn["Delete"]
}
func (f fakeShareStore) DeleteByTarget(context.Context, uuid.UUID, uuid.UUID) error {
	return f.errOn["DeleteByTarget"]
}
func (f fakeShareStore) AddGrant(context.Context, uuid.UUID, uuid.UUID, bool, *time.Time) (*model.ShareGrant, error) {
	if e := f.errOn["AddGrant"]; e != nil {
		return nil, e
	}
	return &model.ShareGrant{ID: uuid.New()}, nil
}
func (f fakeShareStore) RemoveGrant(context.Context, uuid.UUID, uuid.UUID) (*model.ShareGrant, error) {
	if e := f.errOn["RemoveGrant"]; e != nil {
		return nil, e
	}
	return &model.ShareGrant{ID: uuid.New()}, nil
}
func (f fakeShareStore) UpdateGrant(context.Context, uuid.UUID, uuid.UUID, bool, *time.Time) (*model.ShareGrant, error) {
	if e := f.errOn["UpdateGrant"]; e != nil {
		return nil, e
	}
	return &model.ShareGrant{ID: uuid.New()}, nil
}
func (f fakeShareStore) EffectiveAccess(context.Context, uuid.UUID, string) ([]repository.AccessUser, error) {
	return nil, f.errOn["EffectiveAccess"]
}

// fakeShareFiles: FindByPath defaults to ErrNotFound so isFileShare / public
// dispatch pick the directory branch; every other method errors on demand.
type fakeShareFiles struct{ errOn map[string]error }

func (f fakeShareFiles) FindByPath(context.Context, uuid.UUID, string) (*model.File, error) {
	if e := f.errOn["FindByPath"]; e != nil {
		return nil, e
	}
	return nil, repository.ErrNotFound
}
func (f fakeShareFiles) EachInSubtree(context.Context, uuid.UUID, string, func(model.File) error) error {
	return f.errOn["EachInSubtree"]
}
func (f fakeShareFiles) ListDirectoryLevel(context.Context, repository.ListDirectoryLevelParams) ([]model.File, error) {
	return nil, f.errOn["ListDirectoryLevel"]
}
func (f fakeShareFiles) CreateDirectory(context.Context, uuid.UUID, uuid.UUID, string, time.Time) (*model.File, error) {
	if e := f.errOn["CreateDirectory"]; e != nil {
		return nil, e
	}
	return &model.File{ID: uuid.New(), Path: "/foo"}, nil
}
func (f fakeShareFiles) ListChangedFilesSinceUnderPrefix(context.Context, uuid.UUID, string, int64, int) ([]model.File, error) {
	return nil, f.errOn["ListChangedFilesSinceUnderPrefix"]
}
func (f fakeShareFiles) ListMoveEventsSinceUnderPrefix(context.Context, uuid.UUID, string, int64, int) ([]model.MoveEvent, error) {
	return nil, f.errOn["ListMoveEventsSinceUnderPrefix"]
}
func (f fakeShareFiles) DirectoryStats(context.Context, uuid.UUID, string) (repository.DirectoryStats, error) {
	return repository.DirectoryStats{}, f.errOn["DirectoryStats"]
}
func (f fakeShareFiles) FindByPaths(context.Context, uuid.UUID, []string) ([]model.File, error) {
	return nil, f.errOn["FindByPaths"]
}

// fakeShareSyncRoots: GetSyncRoot defaults to a benign root so giver-side
// authorisation passes; set errOn to drive the 404/500 fork.
type fakeShareSyncRoots struct{ errOn map[string]error }

func (f fakeShareSyncRoots) GetSyncRoot(context.Context, uuid.UUID, uuid.UUID) (*model.SyncRoot, error) {
	if e := f.errOn["GetSyncRoot"]; e != nil {
		return nil, e
	}
	return &model.SyncRoot{ID: uuid.New()}, nil
}

type fakeShareConflicts struct{ errOn map[string]error }

func (f fakeShareConflicts) ListOpenConflictsUnderPrefix(context.Context, uuid.UUID, uuid.UUID, string) ([]model.Conflict, error) {
	return nil, f.errOn["ListOpenConflictsUnderPrefix"]
}
func (f fakeShareConflicts) FindOpenConflictDetail(context.Context, uuid.UUID) (*repository.ConflictDetail, error) {
	if e := f.errOn["FindOpenConflictDetail"]; e != nil {
		return nil, e
	}
	return &repository.ConflictDetail{}, nil
}

type fakeShareAudits struct{ errOn map[string]error }

func (f fakeShareAudits) Log(context.Context, audit.Event) error { return f.errOn["Log"] }

// fakeShareHandler wires a ShareHandler over the fakes. The concrete deps the
// error paths never reach (users/storage/mailer/hub/quota/coordinator) stay
// nil — every test below returns before touching them.
func fakeShareHandler(shares fakeShareStore, files fakeShareFiles, roots fakeShareSyncRoots, conflicts fakeShareConflicts) *ShareHandler {
	return &ShareHandler{
		shares:         shares,
		files:          files,
		syncRoots:      roots,
		conflicts:      conflicts,
		audits:         fakeShareAudits{},
		baseURL:        "http://test",
		maxUploadBytes: 100 << 20,
	}
}

func shareErrClaims(uid uuid.UUID) *auth.Claims {
	return &auth.Claims{UserID: uid, Role: model.RoleUser, TokenType: auth.TokenTypeAccess}
}

// ── shares.go ──

func TestShareListByRoot_RepoErrorIs500(t *testing.T) {
	h := fakeShareHandler(fakeShareStore{errOn: map[string]error{"ListBySyncRoot": errBoom}}, fakeShareFiles{}, fakeShareSyncRoots{}, fakeShareConflicts{})
	r := withURLParams(reqWithClaims(http.MethodGet, "/sync-roots/x/shares", nil, shareErrClaims(uuid.New())), "rootID", uuid.NewString())
	w := httptest.NewRecorder()
	h.ListSharesByRoot(w, r)
	assert500(t, w)
}

func TestShareAccess_RepoErrorIs500(t *testing.T) {
	h := fakeShareHandler(fakeShareStore{errOn: map[string]error{"EffectiveAccess": errBoom}}, fakeShareFiles{}, fakeShareSyncRoots{}, fakeShareConflicts{})
	r := withURLParams(reqWithClaims(http.MethodGet, "/sync-roots/x/access", nil, shareErrClaims(uuid.New())), "rootID", uuid.NewString())
	w := httptest.NewRecorder()
	h.ShareAccess(w, r)
	assert500(t, w)
}

func TestShareList_RepoErrorIs500(t *testing.T) {
	h := fakeShareHandler(fakeShareStore{errOn: map[string]error{"ListByUser": errBoom}}, fakeShareFiles{}, fakeShareSyncRoots{}, fakeShareConflicts{})
	r := reqWithClaims(http.MethodGet, "/shares", nil, shareErrClaims(uuid.New()))
	w := httptest.NewRecorder()
	h.ListShares(w, r)
	assert500(t, w)
}

func TestShareCreate_RootLookupErrorIs500(t *testing.T) {
	h := fakeShareHandler(fakeShareStore{}, fakeShareFiles{}, fakeShareSyncRoots{errOn: map[string]error{"GetSyncRoot": errBoom}}, fakeShareConflicts{})
	r := withURLParams(reqWithClaims(http.MethodPost, "/sync-roots/x/shares", nil, shareErrClaims(uuid.New())), "rootID", uuid.NewString())
	w := httptest.NewRecorder()
	h.CreateShare(w, r)
	assert500(t, w)
}

func TestShareCreate_PublicCreateErrorIs500(t *testing.T) {
	h := fakeShareHandler(fakeShareStore{errOn: map[string]error{"Create": errBoom}}, fakeShareFiles{}, fakeShareSyncRoots{}, fakeShareConflicts{})
	body := bytes.NewBufferString(`{"path":"/x","share_type":"public"}`)
	r := withURLParams(reqWithClaims(http.MethodPost, "/sync-roots/x/shares", body, shareErrClaims(uuid.New())), "rootID", uuid.NewString())
	w := httptest.NewRecorder()
	h.CreateShare(w, r)
	assert500(t, w)
}

func TestShareUpdateGrant_RepoErrorsAre500(t *testing.T) {
	owner := uuid.New()
	cases := []struct {
		name  string
		errOn map[string]error
	}{
		{"lookup fails", map[string]error{"FindByID": errBoom}},
		{"update fails", map[string]error{"UpdateGrant": errBoom}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := fakeShareHandler(fakeShareStore{owner: owner, errOn: tc.errOn}, fakeShareFiles{}, fakeShareSyncRoots{}, fakeShareConflicts{})
			body := bytes.NewBufferString(`{"can_write":true}`)
			r := withURLParams(reqWithClaims(http.MethodPatch, "/shares/x/grants/y", body, shareErrClaims(owner)), "shareID", uuid.NewString(), "userID", uuid.NewString())
			w := httptest.NewRecorder()
			h.UpdateShareGrant(w, r)
			assert500(t, w)
		})
	}
}

func TestShareDeleteGrant_RepoErrorsAre500(t *testing.T) {
	owner := uuid.New()
	cases := []struct {
		name  string
		errOn map[string]error
	}{
		{"lookup fails", map[string]error{"FindByID": errBoom}},
		{"remove fails", map[string]error{"RemoveGrant": errBoom}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := fakeShareHandler(fakeShareStore{owner: owner, errOn: tc.errOn}, fakeShareFiles{}, fakeShareSyncRoots{}, fakeShareConflicts{})
			r := withURLParams(reqWithClaims(http.MethodDelete, "/shares/x/grants/y", nil, shareErrClaims(owner)), "shareID", uuid.NewString(), "userID", uuid.NewString())
			w := httptest.NewRecorder()
			h.DeleteShareGrant(w, r)
			assert500(t, w)
		})
	}
}

func TestShareDelete_RepoErrorIs500(t *testing.T) {
	h := fakeShareHandler(fakeShareStore{errOn: map[string]error{"Delete": errBoom}}, fakeShareFiles{}, fakeShareSyncRoots{}, fakeShareConflicts{})
	r := withURLParams(reqWithClaims(http.MethodDelete, "/shares/x", nil, shareErrClaims(uuid.New())), "shareID", uuid.NewString())
	w := httptest.NewRecorder()
	h.DeleteShare(w, r)
	assert500(t, w)
}

// ── shares_public.go ──

func TestPublicShare_RepoErrorIs500(t *testing.T) {
	h := fakeShareHandler(fakeShareStore{errOn: map[string]error{"FindByToken": errBoom}}, fakeShareFiles{}, fakeShareSyncRoots{}, fakeShareConflicts{})
	r := withURLParams(reqWithClaims(http.MethodGet, "/public/shares/tok", nil, shareErrClaims(uuid.New())), "token", "tok")
	w := httptest.NewRecorder()
	h.PublicShare(w, r)
	assert500(t, w)
}

func TestPublicShareEntries_RepoErrorsAre500(t *testing.T) {
	cases := []struct {
		name   string
		shares map[string]error
		files  map[string]error
	}{
		{"token lookup fails", map[string]error{"FindByToken": errBoom}, nil},
		{"list level fails", nil, map[string]error{"ListDirectoryLevel": errBoom}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := fakeShareHandler(fakeShareStore{errOn: tc.shares}, fakeShareFiles{errOn: tc.files}, fakeShareSyncRoots{}, fakeShareConflicts{})
			r := withURLParams(reqWithClaims(http.MethodGet, "/public/shares/tok/entries", nil, shareErrClaims(uuid.New())), "token", "tok")
			w := httptest.NewRecorder()
			h.PublicShareEntries(w, r)
			assert500(t, w)
		})
	}
}

func TestPublicShareDownload_RepoErrorsAre500(t *testing.T) {
	cases := []struct {
		name   string
		shares map[string]error
		files  map[string]error
	}{
		{"token lookup fails", map[string]error{"FindByToken": errBoom}, nil},
		{"subtree fails", nil, map[string]error{"EachInSubtree": errBoom}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := fakeShareHandler(fakeShareStore{errOn: tc.shares}, fakeShareFiles{errOn: tc.files}, fakeShareSyncRoots{}, fakeShareConflicts{})
			r := withURLParams(reqWithClaims(http.MethodGet, "/public/shares/tok/download", nil, shareErrClaims(uuid.New())), "token", "tok")
			w := httptest.NewRecorder()
			h.PublicShareDownload(w, r)
			assert500(t, w)
		})
	}
}

// ── shares_public_links.go ──

func TestCreatePublicLink_RepoErrorsAre500(t *testing.T) {
	cases := []struct {
		name  string
		errOn map[string]error
	}{
		{"ensure fails", map[string]error{"EnsurePublicShare": errBoom}},
		{"set token fails", map[string]error{"SetPublicToken": errBoom}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := fakeShareHandler(fakeShareStore{errOn: tc.errOn}, fakeShareFiles{}, fakeShareSyncRoots{}, fakeShareConflicts{})
			body := bytes.NewBufferString(`{"path":"/x","kind":"read"}`)
			r := withURLParams(reqWithClaims(http.MethodPost, "/sync-roots/x/public-links", body, shareErrClaims(uuid.New())), "rootID", uuid.NewString())
			w := httptest.NewRecorder()
			h.CreatePublicLink(w, r)
			assert500(t, w)
		})
	}
}

func TestDeletePublicLink_RepoErrorsAre500(t *testing.T) {
	cases := []struct {
		name  string
		errOn map[string]error
	}{
		{"find fails", map[string]error{"FindPublicShare": errBoom}},
		{"clear fails", map[string]error{"ClearPublicToken": errBoom}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := fakeShareHandler(fakeShareStore{errOn: tc.errOn}, fakeShareFiles{}, fakeShareSyncRoots{}, fakeShareConflicts{})
			r := withURLParams(reqWithClaims(http.MethodDelete, "/sync-roots/x/public-links?path=/x&kind=read", nil, shareErrClaims(uuid.New())), "rootID", uuid.NewString())
			w := httptest.NewRecorder()
			h.DeletePublicLink(w, r)
			assert500(t, w)
		})
	}
}

// ── shared_mounts_*.go ──

func TestListSharedMounts_RepoErrorIs500(t *testing.T) {
	h := fakeShareHandler(fakeShareStore{errOn: map[string]error{"ListMountsForTarget": errBoom}}, fakeShareFiles{}, fakeShareSyncRoots{}, fakeShareConflicts{})
	r := reqWithClaims(http.MethodGet, "/shared-mounts", nil, shareErrClaims(uuid.New()))
	w := httptest.NewRecorder()
	h.ListSharedMounts(w, r)
	assert500(t, w)
}

func TestListMountFiles_RepoErrorsAre500(t *testing.T) {
	cases := []struct {
		name   string
		shares map[string]error
		files  map[string]error
	}{
		{"resolve fails", map[string]error{"FindMountByGrant": errBoom}, nil},
		{"list level fails", nil, map[string]error{"ListDirectoryLevel": errBoom}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := fakeShareHandler(fakeShareStore{errOn: tc.shares}, fakeShareFiles{errOn: tc.files}, fakeShareSyncRoots{}, fakeShareConflicts{})
			r := withURLParams(reqWithClaims(http.MethodGet, "/shared-mounts/x/files", nil, shareErrClaims(uuid.New())), "shareID", uuid.NewString())
			w := httptest.NewRecorder()
			h.ListMountFiles(w, r)
			assert500(t, w)
		})
	}
}

func TestMountChanges_RepoErrorIs500(t *testing.T) {
	h := fakeShareHandler(fakeShareStore{}, fakeShareFiles{errOn: map[string]error{"ListChangedFilesSinceUnderPrefix": errBoom}}, fakeShareSyncRoots{}, fakeShareConflicts{})
	r := withURLParams(reqWithClaims(http.MethodGet, "/shared-mounts/x/changes?feed_version=2", nil, shareErrClaims(uuid.New())), "shareID", uuid.NewString())
	w := httptest.NewRecorder()
	h.MountChanges(w, r)
	assert500(t, w)
}

// ADR-0065: the second query the merged mount feed depends on gets the same
// 500-on-repo-error coverage as the first.
func TestMountChanges_MoveEventsRepoErrorIs500(t *testing.T) {
	h := fakeShareHandler(fakeShareStore{}, fakeShareFiles{errOn: map[string]error{"ListMoveEventsSinceUnderPrefix": errBoom}}, fakeShareSyncRoots{}, fakeShareConflicts{})
	r := withURLParams(reqWithClaims(http.MethodGet, "/shared-mounts/x/changes?feed_version=2", nil, shareErrClaims(uuid.New())), "shareID", uuid.NewString())
	w := httptest.NewRecorder()
	h.MountChanges(w, r)
	assert500(t, w)
}

func TestMountConflicts_RepoErrorIs500(t *testing.T) {
	h := fakeShareHandler(fakeShareStore{}, fakeShareFiles{}, fakeShareSyncRoots{}, fakeShareConflicts{errOn: map[string]error{"ListOpenConflictsUnderPrefix": errBoom}})
	r := withURLParams(reqWithClaims(http.MethodGet, "/shared-mounts/x/conflicts", nil, shareErrClaims(uuid.New())), "shareID", uuid.NewString())
	w := httptest.NewRecorder()
	h.MountConflicts(w, r)
	assert500(t, w)
}

func TestMountConflictVersions_DetailErrorIs500(t *testing.T) {
	h := fakeShareHandler(fakeShareStore{}, fakeShareFiles{}, fakeShareSyncRoots{}, fakeShareConflicts{errOn: map[string]error{"FindOpenConflictDetail": errBoom}})
	r := withURLParams(reqWithClaims(http.MethodGet, "/shared-mounts/x/conflicts/y", nil, shareErrClaims(uuid.New())), "shareID", uuid.NewString(), "conflictID", uuid.NewString())
	w := httptest.NewRecorder()
	h.MountConflictVersions(w, r)
	assert500(t, w)
}

func TestCreateMountDirectory_RepoErrorIs500(t *testing.T) {
	h := fakeShareHandler(fakeShareStore{}, fakeShareFiles{errOn: map[string]error{"CreateDirectory": errBoom}}, fakeShareSyncRoots{}, fakeShareConflicts{})
	body := bytes.NewBufferString(`{"path":"/foo"}`)
	r := withURLParams(reqWithClaims(http.MethodPost, "/shared-mounts/x/directories", body, shareErrClaims(uuid.New())), "shareID", uuid.NewString())
	w := httptest.NewRecorder()
	h.CreateMountDirectory(w, r)
	assert500(t, w)
}

func TestMountFilesStat_FindByPathsErrorIs500(t *testing.T) {
	h := fakeShareHandler(fakeShareStore{}, fakeShareFiles{errOn: map[string]error{"FindByPaths": errBoom}}, fakeShareSyncRoots{}, fakeShareConflicts{})
	body := bytes.NewBufferString(`{"paths":["/a.txt"]}`)
	r := withURLParams(reqWithClaims(http.MethodPost, "/shared-mounts/x/files/stat", body, shareErrClaims(uuid.New())), "shareID", uuid.NewString())
	w := httptest.NewRecorder()
	h.MountFilesStat(w, r)
	assert500(t, w)
}

func TestMountStats_RepoErrorIs500(t *testing.T) {
	h := fakeShareHandler(fakeShareStore{}, fakeShareFiles{errOn: map[string]error{"DirectoryStats": errBoom}}, fakeShareSyncRoots{}, fakeShareConflicts{})
	r := withURLParams(reqWithClaims(http.MethodGet, "/shared-mounts/x/stats", nil, shareErrClaims(uuid.New())), "shareID", uuid.NewString())
	w := httptest.NewRecorder()
	h.MountStats(w, r)
	assert500(t, w)
}

func TestLeaveSharedMount_RepoErrorIs500(t *testing.T) {
	h := fakeShareHandler(fakeShareStore{errOn: map[string]error{"DeleteByTarget": errBoom}}, fakeShareFiles{}, fakeShareSyncRoots{}, fakeShareConflicts{})
	r := withURLParams(reqWithClaims(http.MethodDelete, "/shared-mounts/x", nil, shareErrClaims(uuid.New())), "shareID", uuid.NewString())
	w := httptest.NewRecorder()
	h.LeaveSharedMount(w, r)
	assert500(t, w)
}
