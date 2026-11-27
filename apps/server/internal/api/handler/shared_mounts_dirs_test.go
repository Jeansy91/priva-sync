package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A writable mount can create an explicit empty directory; it shows up in
// the giver's own-root listing under share.path (ADR-0011 symmetry).
func TestCreateMountDirectory_CreatesEmptyFolder(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/seed.txt", []byte("seed"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	body := bytes.NewBufferString(`{"path":"/newdir"}`)
	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/directories", body, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.CreateMountDirectory(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create mount dir: status=%d body=%s", w.Code, w.Body.String())
	}
	// Response path must be mount-relative, not the giver's absolute path.
	var resp struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Path != "/newdir" {
		t.Fatalf("response path must be mount-relative: got %q want %q", resp.Path, "/newdir")
	}

	// Verify it landed in Alice's own root under /2024/newdir.
	r = reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/files?path=/2024", nil, alice.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w = httptest.NewRecorder()
	env.handler.ListFiles(w, r)
	var listing struct {
		Entries []struct {
			Name  string `json:"name"`
			IsDir bool   `json:"is_dir"`
		} `json:"entries"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listing)
	found := false
	for _, e := range listing.Entries {
		if e.Name == "newdir" && e.IsDir {
			found = true
		}
	}
	if !found {
		t.Fatalf("created dir missing in owner listing: %+v", listing.Entries)
	}
}

// A read-only mount must reject directory creation with 403.
func TestCreateMountDirectory_ForbiddenWhenReadOnly(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/x.txt", []byte("x"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", false)

	body := bytes.NewBufferString(`{"path":"/nope"}`)
	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/directories", body, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.CreateMountDirectory(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("read-only mount dir create should be 403, got %d body=%s", w.Code, w.Body.String())
	}
}

// A directory path escaping share.path via '..' must be rejected with 403.
func TestCreateMountDirectory_RejectsEscape(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/x.txt", []byte("x"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	body := bytes.NewBufferString(`{"path":"/../2025/evil"}`)
	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/directories", body, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.CreateMountDirectory(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("escape dir create should be 403, got %d body=%s", w.Code, w.Body.String())
	}
}
