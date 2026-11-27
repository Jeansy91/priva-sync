package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestListConflicts_RejectsMalformedRootID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)

	r := reqWithClaims(http.MethodGet, "/sync-roots/bad/conflicts", nil, claims)
	r = withURLParams(r, "rootID", "bad")
	w := httptest.NewRecorder()
	env.handler.ListConflicts(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRootID)
}

func TestListConflicts_RejectsUnknownRoot(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	unknown := uuid.New()

	r := reqWithClaims(http.MethodGet, "/sync-roots/"+unknown.String()+"/conflicts", nil, claims)
	r = withURLParams(r, "rootID", unknown.String())
	w := httptest.NewRecorder()
	env.handler.ListConflicts(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSyncRootNotFound)
}

func TestListConflicts_ReturnsEmptyForCleanRoot(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	r := reqWithClaims(http.MethodGet, "/sync-roots/"+root.ID.String()+"/conflicts", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.ListConflicts(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var conflicts []any
	if err := json.Unmarshal(w.Body.Bytes(), &conflicts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts on a clean root, got %d", len(conflicts))
	}
}

func TestGetConflict_RejectsMalformedID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)

	r := reqWithClaims(http.MethodGet, "/conflicts/bad", nil, claims)
	r = withURLParams(r, "conflictID", "bad")
	w := httptest.NewRecorder()
	env.handler.GetConflict(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidConflict)
}

func TestGetConflict_UnknownNotFound(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	unknown := uuid.New()

	r := reqWithClaims(http.MethodGet, "/conflicts/"+unknown.String(), nil, claims)
	r = withURLParams(r, "conflictID", unknown.String())
	w := httptest.NewRecorder()
	env.handler.GetConflict(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrConflictNotFound)
}

func TestGetConflictFile_RejectsMalformedID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)

	r := reqWithClaims(http.MethodGet, "/conflicts/bad/file", nil, claims)
	r = withURLParams(r, "conflictID", "bad")
	w := httptest.NewRecorder()
	env.handler.GetConflictFile(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidConflict)
}

func TestGetConflictFile_UnknownNotFound(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	unknown := uuid.New()

	r := reqWithClaims(http.MethodGet, "/conflicts/"+unknown.String()+"/file", nil, claims)
	r = withURLParams(r, "conflictID", unknown.String())
	w := httptest.NewRecorder()
	env.handler.GetConflictFile(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrConflictNotFound)
}
