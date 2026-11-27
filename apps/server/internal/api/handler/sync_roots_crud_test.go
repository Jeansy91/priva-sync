package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/auth"
)

func postSyncRoot(env *testEnv, claims *auth.Claims, body string) *httptest.ResponseRecorder {
	r := reqWithClaims(http.MethodPost, "/sync-roots", bytes.NewBufferString(body), claims)
	w := httptest.NewRecorder()
	env.handler.CreateSyncRoot(w, r)
	return w
}

func TestCreateSyncRoot_RejectsMissingName(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	w := postSyncRoot(env, claims, `{"name":""}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrNameRequired)
}

func TestCreateSyncRoot_Succeeds(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	w := postSyncRoot(env, claims, `{"name":"Documents `+uniqueSuffix()+`"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID == uuid.Nil {
		t.Errorf("expected a non-nil sync root id")
	}
}

func TestRenameSyncRoot_RejectsMalformedRootID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	r := reqWithClaims(http.MethodPatch, "/sync-roots/bad", bytes.NewBufferString(`{"name":"x"}`), claims)
	r = withURLParams(r, "rootID", "bad")
	w := httptest.NewRecorder()
	env.handler.RenameSyncRoot(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRootID)
}

func TestRenameSyncRoot_RejectsMissingName(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	r := reqWithClaims(http.MethodPatch, "/sync-roots/"+root.ID.String(), bytes.NewBufferString(`{"name":"  "}`), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.RenameSyncRoot(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrNameRequired)
}

func TestRenameSyncRoot_RejectsUnknownRoot(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	unknown := uuid.New()
	r := reqWithClaims(http.MethodPatch, "/sync-roots/"+unknown.String(), bytes.NewBufferString(`{"name":"x"}`), claims)
	r = withURLParams(r, "rootID", unknown.String())
	w := httptest.NewRecorder()
	env.handler.RenameSyncRoot(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSyncRootNotFound)
}

func TestRenameSyncRoot_Succeeds(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	r := reqWithClaims(http.MethodPatch, "/sync-roots/"+root.ID.String(), bytes.NewBufferString(`{"name":"Renamed"}`), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.RenameSyncRoot(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteSyncRoot_RejectsMalformedRootID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	r := reqWithClaims(http.MethodDelete, "/sync-roots/bad", nil, claims)
	r = withURLParams(r, "rootID", "bad")
	w := httptest.NewRecorder()
	env.handler.DeleteSyncRoot(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRootID)
}

func TestDeleteSyncRoot_RejectsUnknownRoot(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	unknown := uuid.New()
	r := reqWithClaims(http.MethodDelete, "/sync-roots/"+unknown.String(), nil, claims)
	r = withURLParams(r, "rootID", unknown.String())
	w := httptest.NewRecorder()
	env.handler.DeleteSyncRoot(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSyncRootNotFound)
}

func TestDeleteSyncRoot_Succeeds(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, claims, root.ID, "/a.txt", []byte("bye"))

	r := reqWithClaims(http.MethodDelete, "/sync-roots/"+root.ID.String(), nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.DeleteSyncRoot(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)
}
