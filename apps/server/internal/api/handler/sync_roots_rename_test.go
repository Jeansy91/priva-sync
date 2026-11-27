package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// PATCH /sync-roots/{rootID} renames the display name in place: 200 with the
// new name, and the row reads back renamed.
func TestRenameSyncRoot_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "Test")

	r := reqWithClaims("PATCH", "/api/v1/sync-roots/"+root.ID.String(),
		strings.NewReader(`{"name":"Test 2"}`), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.RenameSyncRoot(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp struct{ Name string }
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Name != "Test 2" {
		t.Fatalf("response name = %q, want %q", resp.Name, "Test 2")
	}

	got, err := env.syncRoots.GetSyncRoot(context.Background(), root.ID, claims.UserID)
	if err != nil {
		t.Fatalf("reload root: %v", err)
	}
	if got.Name != "Test 2" {
		t.Fatalf("persisted name = %q, want %q", got.Name, "Test 2")
	}
}

// Renaming onto a name the same user already uses returns 409 — the partial
// unique (user_id, name) blocks the collision.
func TestRenameSyncRoot_DuplicateName_409(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	createTestSyncRoot(t, env, claims.UserID, "Taken")
	root := createTestSyncRoot(t, env, claims.UserID, "Movable")

	r := reqWithClaims("PATCH", "/api/v1/sync-roots/"+root.ID.String(),
		strings.NewReader(`{"name":"Taken"}`), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.RenameSyncRoot(w, r)

	if w.Code != 409 {
		t.Fatalf("status = %d, want 409 (body: %s)", w.Code, w.Body.String())
	}
}

// A blank name is rejected with 400 before touching the DB.
func TestRenameSyncRoot_BlankName_400(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "Keep")

	r := reqWithClaims("PATCH", "/api/v1/sync-roots/"+root.ID.String(),
		strings.NewReader(`{"name":"   "}`), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.RenameSyncRoot(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
}

// Renaming a root owned by another user returns 404 (ownership scoping).
func TestRenameSyncRoot_NotOwned_404(t *testing.T) {
	env := newTestEnv(t)
	owner := createTestUser(t, env)
	other := createTestUser(t, env)
	root := createTestSyncRoot(t, env, owner.UserID, "Owned")

	r := reqWithClaims("PATCH", "/api/v1/sync-roots/"+root.ID.String(),
		strings.NewReader(`{"name":"Hijack"}`), other)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.RenameSyncRoot(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404 (body: %s)", w.Code, w.Body.String())
	}
}
