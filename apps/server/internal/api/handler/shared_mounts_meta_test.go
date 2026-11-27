package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A directory mount's stats endpoint reports subtree aggregates — item/file
// counts and total size — mirroring the own-root directory-stats endpoint.
func TestMountStats_DirectoryReturnsAggregates(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/a.txt", []byte("aaa"))
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/b.txt", []byte("bb"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", false)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/stats", nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MountStats(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Kind      string `json:"kind"`
		FileCount int    `json:"file_count"`
		TotalSize int64  `json:"total_size"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Kind != "dir" {
		t.Errorf("kind = %q, want dir", resp.Kind)
	}
	if resp.FileCount != 2 {
		t.Errorf("file_count = %d, want 2", resp.FileCount)
	}
	if resp.TotalSize != 5 {
		t.Errorf("total_size = %d, want 5", resp.TotalSize)
	}
}

// A file mount's stats endpoint reports the underlying file's own metadata
// instead of subtree aggregates.
func TestMountStats_FileReturnsFileMetadata(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/cat.png", []byte("img-bytes"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024/cat.png", false)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/stats", nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MountStats(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Kind string `json:"kind"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Kind != "file" {
		t.Errorf("kind = %q, want file", resp.Kind)
	}
	if resp.Size != int64(len("img-bytes")) {
		t.Errorf("size = %d, want %d", resp.Size, len("img-bytes"))
	}
}

// A third party must not reach mount stats (resolveMount boundary, 404).
func TestMountStats_RejectsThirdParty(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	carol := makeUser(t, env, "carol_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/x.txt", []byte("x"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", false)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/stats", nil, carol.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MountStats(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// A directory mount can mint a ZIP-download token for its subtree.
func TestMintMountDownloadToken_DirectoryReturnsToken(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/a.txt", []byte("aaa"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", false)

	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/download-token", nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MintMountDownloadToken(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Token     string `json:"token"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Token == "" {
		t.Error("empty token")
	}
	if resp.ExpiresIn <= 0 {
		t.Errorf("expires_in = %d, want > 0", resp.ExpiresIn)
	}
}

// A file mount's token serves the file itself, not a one-entry ZIP — the
// browser downloads it natively instead of the page buffering it (ADR-0073).
func TestMintMountDownloadToken_FileMountServesRawFile(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/cat.png", []byte("img"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024/cat.png", false)

	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/download-token", nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MintMountDownloadToken(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("mint status %d body=%s", w.Code, w.Body.String())
	}
	var tok struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &tok)

	sw := serveDownloadToken(t, env, tok.Token, "")
	if sw.Code != http.StatusOK || sw.Body.String() != "img" {
		t.Fatalf("serve status %d body=%q, want 200 \"img\"", sw.Code, sw.Body.String())
	}
	if disp := sw.Header().Get("Content-Disposition"); !strings.Contains(disp, `filename="cat.png"`) {
		t.Errorf("Content-Disposition = %q, want cat.png", disp)
	}
}

// A sub-path inside a directory mount scopes the token to that file.
func TestMintMountDownloadToken_SubPathFile(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/a.txt", []byte("aaa"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", false)

	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/download-token?path=/a.txt", nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MintMountDownloadToken(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("mint status %d body=%s", w.Code, w.Body.String())
	}
	var tok struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &tok)

	if sw := serveDownloadToken(t, env, tok.Token, ""); sw.Body.String() != "aaa" {
		t.Fatalf("serve status %d body=%q, want \"aaa\"", sw.Code, sw.Body.String())
	}
}

// A sub-path must not climb out of the mount.
func TestMintMountDownloadToken_SubPathEscapeRejected(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/a.txt", []byte("aaa"))
	uploadFileAs(t, env, alice.Claims, root.ID, "/secret.txt", []byte("s"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", false)

	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/download-token?path=/../secret.txt", nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MintMountDownloadToken(w, r)
	if w.Code == http.StatusOK {
		t.Fatalf("mint status 200 for an escaping path, body=%s", w.Body.String())
	}
}
