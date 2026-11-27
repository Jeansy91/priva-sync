package handler

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// DownloadSyncRoot is the direct authenticated whole-root ZIP download (no
// mint/serve token round-trip) — it streams every live file in the caller's
// own sync root.
func TestDownloadSyncRoot_ReturnsWholeRootZip(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	uploadHelper(t, env, claims, root, "/dir/a.txt", "AAA", nil)
	uploadHelper(t, env, claims, root, "/other.txt", "X", nil)

	r := reqWithClaims(http.MethodGet, "/api/v1/sync-roots/"+root.ID.String()+"/download", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.DownloadSyncRoot(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}

	disp := w.Header().Get("Content-Disposition")
	if !strings.Contains(disp, `filename="root1.zip"`) {
		t.Errorf("filename: %q, want contains root1.zip", disp)
	}

	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("zip parse: %v", err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	want := []string{"dir/a.txt", "other.txt"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("zip entries: got %v, want %v", names, want)
	}

	for _, f := range zr.File {
		if f.Name == "dir/a.txt" {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open zip entry: %v", err)
			}
			b, _ := io.ReadAll(rc)
			rc.Close()
			if string(b) != "AAA" {
				t.Errorf("dir/a.txt content = %q, want AAA", b)
			}
		}
	}
}

func TestDownloadSyncRoot_InvalidRootID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)

	r := reqWithClaims(http.MethodGet, "/api/v1/sync-roots/not-a-uuid/download", nil, claims)
	r = withURLParams(r, "rootID", "not-a-uuid")
	w := httptest.NewRecorder()
	env.handler.DownloadSyncRoot(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// A caller must not be able to download another user's sync root.
func TestDownloadSyncRoot_RejectsForeignRoot(t *testing.T) {
	env := newTestEnv(t)
	owner := createTestUser(t, env)
	intruder := createTestUser(t, env)
	root := createTestSyncRoot(t, env, owner.UserID, "root1")
	uploadHelper(t, env, owner, root, "/a.txt", "AAA", nil)

	r := reqWithClaims(http.MethodGet, "/api/v1/sync-roots/"+root.ID.String()+"/download", nil, intruder)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.DownloadSyncRoot(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404, body=%s", w.Code, w.Body.String())
	}
}
