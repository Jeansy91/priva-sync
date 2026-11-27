package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"privasync/server/internal/auth"
	"privasync/server/internal/model"
)

// publicShareTokenForDir uploads two files into /folder and returns the
// public share token. Caller is responsible for env setup.
func publicShareTokenForDir(t *testing.T, env *testEnv, claims *auth.Claims) string {
	t.Helper()

	root := createTestSyncRoot(t, env, claims.UserID, "root_pub")

	for _, name := range []string{"a.txt", "b.txt"} {
		r := reqWithClaims("POST",
			"/sync-roots/"+root.ID.String()+"/files?path=/folder/"+name,
			bytes.NewReader([]byte("payload-"+name)), claims)
		r = withURLParams(r, "rootID", root.ID.String())
		w := httptest.NewRecorder()
		env.handler.UploadFile(w, r)
		if w.Code != 200 {
			t.Fatalf("upload %s: status=%d body=%s", name, w.Code, w.Body.String())
		}
		env.drainOutbox(t)
	}

	body := bytes.NewBufferString(`{"path":"/folder","share_type":"public"}`)
	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/shares", body, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.shareH.CreateShare(w, r)
	if w.Code != 201 {
		t.Fatalf("create share: status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct{ Token *string `json:"token"` }
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode share: %v", err)
	}
	if resp.Token == nil || *resp.Token == "" {
		t.Fatalf("share token missing")
	}
	return *resp.Token
}

func TestPublicShareEntries_DirReturnsChildren(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)

	token := publicShareTokenForDir(t, env, claims)

	r := httptest.NewRequest("GET", "/public/shares/"+token+"/entries", nil)
	r = withURLParams(r, "token", token)
	w := httptest.NewRecorder()
	env.shareH.PublicShareEntries(w, r)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Path    string `json:"path"`
		IsFile  bool   `json:"is_file"`
		Entries []struct {
			Name  string `json:"name"`
			Path  string `json:"path"`
			IsDir bool   `json:"is_dir"`
			Size  int64  `json:"size"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Path != "/folder" {
		t.Errorf("path=%q want /folder", got.Path)
	}
	if got.IsFile {
		t.Errorf("is_file=true, want false for dir share")
	}
	names := map[string]bool{}
	for _, e := range got.Entries {
		names[e.Name] = true
	}
	if !names["a.txt"] || !names["b.txt"] {
		t.Errorf("missing entries: got %+v", got.Entries)
	}
}

func TestPublicShareEntries_ExpiredReturns410(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "expired_root")

	r := reqWithClaims("POST",
		"/sync-roots/"+root.ID.String()+"/files?path=/file.txt",
		bytes.NewReader([]byte("x")), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.UploadFile(w, r)
	if w.Code != 200 {
		t.Fatalf("upload: %d", w.Code)
	}
	env.drainOutbox(t)

	expires := time.Now().Add(-time.Hour)
	token := "tok-expired-" + strings.ReplaceAll(string(model.ShareTypePublic), " ", "")
	share := &model.Share{
		UserID: claims.UserID, SyncRootID: &root.ID,
		Path: "/file.txt", ShareType: model.ShareTypePublic,
		Token: &token, ExpiresAt: &expires,
	}
	if _, err := env.pool.Exec(context.Background(),
		`INSERT INTO shares (user_id, sync_root_id, path, share_type, token, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		share.UserID, share.SyncRootID, share.Path, share.ShareType, share.Token, share.ExpiresAt,
	); err != nil {
		t.Fatalf("insert share: %v", err)
	}

	r = httptest.NewRequest("GET", "/public/shares/"+token+"/entries", nil)
	r = withURLParams(r, "token", token)
	w = httptest.NewRecorder()
	env.shareH.PublicShareEntries(w, r)
	if w.Code != 410 {
		t.Fatalf("status=%d want 410, body=%s", w.Code, w.Body.String())
	}
}

func TestPublicShareEntries_RejectsPathEscape(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	token := publicShareTokenForDir(t, env, claims)

	r := httptest.NewRequest("GET", "/public/shares/"+token+"/entries?sub=../secret", nil)
	r = withURLParams(r, "token", token)
	w := httptest.NewRecorder()
	env.shareH.PublicShareEntries(w, r)
	if w.Code != 403 {
		t.Fatalf("status=%d want 403, body=%s", w.Code, w.Body.String())
	}
}

func TestPublicShareDownload_DirReturnsZip(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	token := publicShareTokenForDir(t, env, claims)

	r := httptest.NewRequest("GET", "/public/shares/"+token+"/download", nil)
	r = withURLParams(r, "token", token)
	w := httptest.NewRecorder()
	env.shareH.PublicShareDownload(w, r)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content-type=%q want application/zip", ct)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("zip parse: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["a.txt"] || !names["b.txt"] {
		t.Fatalf("missing entries in zip: %+v", names)
	}
}

func TestPublicShareDownload_SubDirReturnsZip(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "subdir_root")

	for _, p := range []string{"/folder/inner/x.txt", "/folder/inner/y.txt", "/folder/outside.txt"} {
		r := reqWithClaims("POST",
			"/sync-roots/"+root.ID.String()+"/files?path="+p,
			bytes.NewReader([]byte("c")), claims)
		r = withURLParams(r, "rootID", root.ID.String())
		w := httptest.NewRecorder()
		env.handler.UploadFile(w, r)
		if w.Code != 200 {
			t.Fatalf("upload %s: %d", p, w.Code)
		}
		env.drainOutbox(t)
	}

	body := bytes.NewBufferString(`{"path":"/folder","share_type":"public"}`)
	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/shares", body, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.shareH.CreateShare(w, r)
	if w.Code != 201 {
		t.Fatalf("create share: %d body=%s", w.Code, w.Body.String())
	}
	var sh struct{ Token *string `json:"token"` }
	_ = json.Unmarshal(w.Body.Bytes(), &sh)

	r = httptest.NewRequest("GET", "/public/shares/"+*sh.Token+"/download?sub=inner", nil)
	r = withURLParams(r, "token", *sh.Token)
	w = httptest.NewRecorder()
	env.shareH.PublicShareDownload(w, r)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("zip parse: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["x.txt"] || !names["y.txt"] {
		t.Fatalf("subdir zip missing entries: %+v", names)
	}
	if names["outside.txt"] {
		t.Fatalf("zip includes file outside subdir: %+v", names)
	}
}

func TestPublicShareDownload_SingleFile(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "dl_root")

	content := "stream-content-42"
	r := reqWithClaims("POST",
		"/sync-roots/"+root.ID.String()+"/files?path=/doc.txt",
		bytes.NewReader([]byte(content)), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.UploadFile(w, r)
	if w.Code != 200 {
		t.Fatalf("upload: %d body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	body := bytes.NewBufferString(`{"path":"/doc.txt","share_type":"public"}`)
	r = reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/shares", body, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w = httptest.NewRecorder()
	env.shareH.CreateShare(w, r)
	if w.Code != 201 {
		t.Fatalf("create share: %d body=%s", w.Code, w.Body.String())
	}
	var sh struct{ Token *string `json:"token"` }
	_ = json.Unmarshal(w.Body.Bytes(), &sh)

	r = httptest.NewRequest("GET", "/public/shares/"+*sh.Token+"/download", nil)
	r = withURLParams(r, "token", *sh.Token)
	w = httptest.NewRecorder()
	env.shareH.PublicShareDownload(w, r)
	if w.Code != 200 {
		t.Fatalf("download status=%d body=%s", w.Code, w.Body.String())
	}
	got, _ := io.ReadAll(w.Body)
	if string(got) != content {
		t.Fatalf("body=%q want %q", string(got), content)
	}
}

// A directory share holding no files must stay a 404 rather than deliver an
// empty archive. Before ADR-0041 that fell out of the directory having no
// rows at all; now it has one, so the ZIP writer is only started once a file
// entry is actually found.
func TestPublicShareDownload_DirectoryWithoutFilesIs404(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "empty_dir_root")

	body := bytes.NewBufferString(`{"path":"/hollow","op":"mkdir"}`)
	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/directories", body, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.CreateDirectory(w, r)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("create directory: %d body=%s", w.Code, w.Body.String())
	}

	body = bytes.NewBufferString(`{"path":"/hollow","share_type":"public"}`)
	r = reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/shares", body, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w = httptest.NewRecorder()
	env.shareH.CreateShare(w, r)
	if w.Code != 201 {
		t.Fatalf("create share: %d body=%s", w.Code, w.Body.String())
	}
	var sh struct {
		Token *string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &sh)

	r = httptest.NewRequest("GET", "/public/shares/"+*sh.Token+"/download", nil)
	r = withURLParams(r, "token", *sh.Token)
	w = httptest.NewRecorder()
	env.shareH.PublicShareDownload(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404, body=%s", w.Code, w.Body.String())
	}
}
