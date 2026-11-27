package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestSearchFilesGlobal_IncludesSharedMountStripped drives the Shared-Mount
// branch of SearchFilesGlobal: the recipient owns no roots, so the only group
// comes from a user-share where they are the target, and file paths must be
// stripped to be mount-relative (giver-root prefix removed).
func TestSearchFilesGlobal_IncludesSharedMountStripped(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "aroot_"+uniqueSuffix())

	// One hit under the shared path, one outside it (must not leak to bob).
	seedSearchFile(t, env, ctx, alice.UserID, root.ID, "/shared/docs/bericht.pdf", false)
	seedSearchFile(t, env, ctx, alice.UserID, root.ID, "/privat/bericht.pdf", false)

	createUserShare(t, env, alice, bob, root.ID, "/shared", false)

	resp := doGlobalSearch(t, env, bob.Claims, "bericht", nil)
	if len(resp.Groups) != 1 {
		t.Fatalf("expected exactly the mount group, got %+v", resp.Groups)
	}
	g := resp.Groups[0]
	if len(g.Files) != 1 || g.Files[0].Path != "/docs/bericht.pdf" {
		t.Fatalf("mount path must be stripped to mount-relative, got %+v", g.Files)
	}
}

func TestSearchFiles_InvalidRootID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	r := reqWithClaims(http.MethodGet, "/api/v1/sync-roots/not-a-uuid/files/search?q=x", nil, claims)
	r = withURLParams(r, "rootID", "not-a-uuid")
	w := httptest.NewRecorder()
	env.handler.SearchFiles(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid root id must be 400, got %d", w.Code)
	}
}

func TestSearchFiles_UnknownRootNotFound(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	unknown := uuid.New()
	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+unknown.String()+"/files/search?q=x", nil, claims)
	r = withURLParams(r, "rootID", unknown.String())
	w := httptest.NewRecorder()
	env.handler.SearchFiles(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown/unowned root must be 404, got %d", w.Code)
	}
}
