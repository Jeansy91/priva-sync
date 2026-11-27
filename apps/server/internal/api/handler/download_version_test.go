package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The web editor needs the file's server_version at load time so it can send
// it back as If-Match on save and detect concurrent edits. Both download
// handlers must expose it via the X-Server-Version response header.

func TestDownloadFile_SetsServerVersionHeader(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	// Fresh upload is server_version 0; an If-Match overwrite bumps it to 1 so
	// the header proves it reflects the real column, not a hardcoded default.
	uploadFileAs(t, env, alice.Claims, root.ID, "/note.txt", []byte("v1"))
	w0 := reqWithClaims(http.MethodPut,
		"/api/v1/sync-roots/"+root.ID.String()+"/files?path=/note.txt",
		bytes.NewReader([]byte("v2-edited")), alice.Claims)
	w0 = withURLParams(w0, "rootID", root.ID.String())
	w0.Header.Set("If-Match", "0")
	rec0 := httptest.NewRecorder()
	env.handler.UploadFile(rec0, w0)
	if rec0.Code != http.StatusOK {
		t.Fatalf("overwrite status %d body=%s", rec0.Code, rec0.Body.String())
	}
	env.drainOutbox(t)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/files/download?path=/note.txt", nil, alice.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.DownloadFile(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("download status %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Server-Version"); got != "1" {
		t.Fatalf("X-Server-Version = %q, want \"1\"", got)
	}
}

func TestDownloadMountFile_SetsServerVersionHeader(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/note.txt", []byte("v1"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", false)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/download?path=/note.txt", nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.DownloadMountFile(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("download status %d body=%s", w.Code, w.Body.String())
	}
	// Fresh file → server_version 0; the header must still be present and exact.
	if got := w.Header().Get("X-Server-Version"); got != "0" {
		t.Fatalf("X-Server-Version = %q, want \"0\"", got)
	}
}
