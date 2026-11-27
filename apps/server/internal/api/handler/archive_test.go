package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"privasync/server/internal/auth"
)

func listArchive(env *testEnv, claims *auth.Claims, rootID, query string) *httptest.ResponseRecorder {
	r := reqWithClaims(http.MethodGet, "/sync-roots/"+rootID+"/files/archive"+query, nil, claims)
	r = withURLParams(r, "rootID", rootID)
	w := httptest.NewRecorder()
	env.handler.ListArchive(w, r)
	return w
}

// ListsEntries / RejectsNonZip / UnknownRoot are covered in
// shared_mounts_archive_test.go — these fill the remaining branches.

func TestListArchive_RejectsMalformedRootID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	w := listArchive(env, claims, "bad", "?path=/a.zip")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRootID)
}

func TestListArchive_RejectsUnknownFile(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	w := listArchive(env, claims, root.ID.String(), "?path=/missing.zip")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrFileNotFound)
}

func TestListArchive_RejectsCorruptArchive(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, claims, root.ID, "/bad.zip", []byte("this is not a zip file"))

	w := listArchive(env, claims, root.ID.String(), "?path=/bad.zip")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}
