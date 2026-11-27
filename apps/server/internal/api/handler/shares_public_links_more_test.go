package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/auth"
)

// postPublicLinkRaw / deletePublicLinkRaw post arbitrary bodies/queries so the
// guard branches can be driven (the typed helpers in shares_public_links_test.go
// only build valid requests).
func postPublicLinkRaw(env *testEnv, claims *auth.Claims, rootID, body string) *httptest.ResponseRecorder {
	r := reqWithClaims(http.MethodPost, "/sync-roots/"+rootID+"/public-links", bytes.NewBufferString(body), claims)
	r = withURLParams(r, "rootID", rootID)
	w := httptest.NewRecorder()
	env.shareH.CreatePublicLink(w, r)
	return w
}

func deletePublicLinkRaw(env *testEnv, claims *auth.Claims, rootID, query string) *httptest.ResponseRecorder {
	r := reqWithClaims(http.MethodDelete, "/sync-roots/"+rootID+"/public-links"+query, nil, claims)
	r = withURLParams(r, "rootID", rootID)
	w := httptest.NewRecorder()
	env.shareH.DeletePublicLink(w, r)
	return w
}

func TestCreatePublicLink_RejectsMalformedRootID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	w := postPublicLinkRaw(env, claims, "bad", `{"path":"/dir","kind":"read"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRootID)
}

func TestCreatePublicLink_RejectsUnknownRoot(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	w := postPublicLinkRaw(env, claims, uuid.New().String(), `{"path":"/dir","kind":"read"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSyncRootNotFound)
}

func TestCreatePublicLink_RejectsBadKind(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	w := postPublicLinkRaw(env, claims, root.ID.String(), `{"path":"/dir","kind":"execute"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrShareFieldsMiss)
}

func TestCreatePublicLink_RejectsInvalidExpiresAt(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, claims, root.ID, "/dir/x.txt", []byte("x"))
	w := postPublicLinkRaw(env, claims, root.ID.String(), `{"path":"/dir","kind":"read","expires_at":"nope"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidExpiresAt)
}

func TestDeletePublicLink_RejectsMalformedRootID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	w := deletePublicLinkRaw(env, claims, "bad", "?path=/dir&kind=read")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRootID)
}

func TestDeletePublicLink_RejectsMissingFields(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	w := deletePublicLinkRaw(env, claims, root.ID.String(), "?path=&kind=read")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrShareFieldsMiss)
}

func TestDeletePublicLink_NoOpWhenNoShare(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	w := deletePublicLinkRaw(env, claims, root.ID.String(), "?path=/never-shared&kind=read")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
}

func TestDeletePublicLink_RemovesExistingLink(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, claims, root.ID, "/dir/x.txt", []byte("x"))
	if w := postPublicLinkRaw(env, claims, root.ID.String(), `{"path":"/dir","kind":"read"}`); w.Code != http.StatusCreated {
		t.Fatalf("create link: status=%d body=%s", w.Code, w.Body.String())
	}
	w := deletePublicLinkRaw(env, claims, root.ID.String(), "?path=/dir&kind=read")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
}
