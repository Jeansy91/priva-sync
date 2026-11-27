package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"privasync/server/internal/auth"
)

// publicRootShareToken shares a whole sync root (path "/"), the case
// shared_mounts_listing.go calls a root-share.
func publicRootShareToken(t *testing.T, env *testEnv, claims *auth.Claims) string {
	t.Helper()
	root := createTestSyncRoot(t, env, claims.UserID, "root_whole")

	for _, p := range []string{"/top.txt", "/folder/nested.txt"} {
		r := reqWithClaims("POST",
			"/sync-roots/"+root.ID.String()+"/files?path="+p,
			bytes.NewReader([]byte("payload"+p)), claims)
		r = withURLParams(r, "rootID", root.ID.String())
		w := httptest.NewRecorder()
		env.handler.UploadFile(w, r)
		if w.Code != 200 {
			t.Fatalf("upload %s: status=%d body=%s", p, w.Code, w.Body.String())
		}
		env.drainOutbox(t)
	}

	body := bytes.NewBufferString(`{"path":"/","share_type":"public"}`)
	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/shares", body, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.shareH.CreateShare(w, r)
	if w.Code != 201 {
		t.Fatalf("create root share: status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Token *string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode share: %v", err)
	}
	if resp.Token == nil || *resp.Token == "" {
		t.Fatalf("share token missing")
	}
	return *resp.Token
}

// A share on "/" used to produce an empty ZIP and therefore a 404: the
// subtree query built `path LIKE '//%'`, which matches nothing.
func TestPublicShareDownload_RootShareReturnsWholeTree(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	token := publicRootShareToken(t, env, claims)

	r := httptest.NewRequest("GET", "/public/shares/"+token+"/download", nil)
	r = withURLParams(r, "token", token)
	w := httptest.NewRecorder()
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
	if !names["top.txt"] || !names["folder/nested.txt"] {
		t.Fatalf("root-share zip is missing entries: %+v", names)
	}
}
