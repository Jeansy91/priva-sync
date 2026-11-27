package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// zipBytes builds a tiny in-memory .zip with the given file names.
func zipBytes(t *testing.T, names ...string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		f, err := zw.Create(n)
		if err != nil {
			t.Fatalf("zip create %s: %v", n, err)
		}
		if _, err := f.Write([]byte("x")); err != nil {
			t.Fatalf("zip write %s: %v", n, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.String()
}

func TestListArchive_ListsEntries(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_listarch")
	root := createTestSyncRoot(t, env, alice.UserID, "root_listarch")

	uploadHelper(t, env, alice.Claims, root, "/pack.zip", zipBytes(t, "b.txt", "a/c.txt"), nil)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/files/archive?path=/pack.zip", nil, alice.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.ListArchive(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var names []string
	for _, e := range resp.Entries {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	got := strings.Join(names, ",")
	if !strings.Contains(got, "a/c.txt") || !strings.Contains(got, "b.txt") {
		t.Fatalf("entries = %q, want a/c.txt and b.txt", got)
	}
}

func TestListArchive_RejectsNonZip(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_listarch2")
	root := createTestSyncRoot(t, env, alice.UserID, "root_listarch2")

	uploadHelper(t, env, alice.Claims, root, "/notes.txt", "hello", nil)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/files/archive?path=/notes.txt", nil, alice.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.ListArchive(w, r)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d, want 415; body=%s", w.Code, w.Body.String())
	}
}

func TestListArchive_UnknownRootNotFound(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_listarch3")

	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/00000000-0000-0000-0000-000000000000/files/archive?path=/pack.zip", nil, alice.Claims)
	r = withURLParams(r, "rootID", "00000000-0000-0000-0000-000000000000")
	w := httptest.NewRecorder()
	env.handler.ListArchive(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

func TestMountArchive_FileMountListsEntries(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_arch")
	bob := makeUser(t, env, "bob_arch")
	root := createTestSyncRoot(t, env, alice.UserID, "root_arch")

	uploadHelper(t, env, alice.Claims, root, "/pack.zip", zipBytes(t, "b.txt", "a/c.txt"), nil)

	// File-mount: share the .zip itself to bob.
	shareID := createUserShare(t, env, alice, bob, root.ID, "/pack.zip", false)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/archive?path=/", nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.handler.MountArchive(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var names []string
	for _, e := range resp.Entries {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	got := strings.Join(names, ",")
	if !strings.Contains(got, "a/c.txt") || !strings.Contains(got, "b.txt") {
		t.Fatalf("entries = %q, want a/c.txt and b.txt", got)
	}
}

func TestMountArchive_RejectsNonZip(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_arch2")
	bob := makeUser(t, env, "bob_arch2")
	root := createTestSyncRoot(t, env, alice.UserID, "root_arch2")

	uploadHelper(t, env, alice.Claims, root, "/notes.txt", "hello", nil)
	shareID := createUserShare(t, env, alice, bob, root.ID, "/notes.txt", false)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/archive?path=/", nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.handler.MountArchive(w, r)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d, want 415; body=%s", w.Code, w.Body.String())
	}
}
