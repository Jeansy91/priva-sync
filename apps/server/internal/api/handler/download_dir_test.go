package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// TestDownloadToken_SubtreePath verifies that a mint+serve cycle with a
// directory path returns a ZIP containing only that subtree's files, with
// paths relative to the subtree root and a filename derived from the
// directory's last segment.
func TestDownloadToken_SubtreePath(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	uploadHelper(t, env, claims, root, "/dir/a.txt", "AAA", nil)
	uploadHelper(t, env, claims, root, "/dir/sub/b.txt", "BB", nil)
	uploadHelper(t, env, claims, root, "/other.txt", "X", nil)

	// Mint with JSON body {path:"/dir"}.
	mintBody, _ := json.Marshal(map[string]any{"path": "/dir"})
	mr := reqWithClaims(http.MethodPost,
		"/api/v1/sync-roots/"+root.ID.String()+"/download-token",
		bytes.NewReader(mintBody), claims)
	mr.Header.Set("Content-Type", "application/json")
	mr = withURLParams(mr, "rootID", root.ID.String())
	mw := httptest.NewRecorder()
	env.handler.MintDownloadToken(mw, mr)
	if mw.Code != http.StatusOK {
		t.Fatalf("mint status %d body=%s", mw.Code, mw.Body.String())
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(mw.Body.Bytes(), &tok); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if tok.Token == "" {
		t.Fatal("empty token")
	}

	// Serve with the public endpoint.
	sr := httptest.NewRequest(http.MethodGet, "/download/"+tok.Token, nil)
	sr = withURLParams(sr, "token", tok.Token)
	sw := httptest.NewRecorder()
	env.handler.ServeTokenDownload(sw, sr)
	if sw.Code != http.StatusOK {
		t.Fatalf("serve status %d body=%s", sw.Code, sw.Body.String())
	}

	disp := sw.Header().Get("Content-Disposition")
	if !strings.Contains(disp, `filename="dir.zip"`) {
		t.Errorf("filename: %q, want contains dir.zip", disp)
	}

	zr, err := zip.NewReader(bytes.NewReader(sw.Body.Bytes()), int64(sw.Body.Len()))
	if err != nil {
		t.Fatalf("zip parse: %v", err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	want := []string{"a.txt", "sub/b.txt"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("zip entries: got %v, want %v", names, want)
	}

	// Verify content of a.txt is preserved.
	for _, f := range zr.File {
		if f.Name == "a.txt" {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open zip entry: %v", err)
			}
			b, _ := io.ReadAll(rc)
			rc.Close()
			if string(b) != "AAA" {
				t.Errorf("a.txt content = %q, want AAA", b)
			}
		}
	}
}

// TestDownloadToken_EmptyPathServesWholeRoot verifies backwards compatibility:
// minting with no path (or "/") still returns the whole sync root, named after
// the root.
func TestDownloadToken_EmptyPathServesWholeRoot(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	uploadHelper(t, env, claims, root, "/dir/a.txt", "AAA", nil)
	uploadHelper(t, env, claims, root, "/other.txt", "X", nil)

	mr := reqWithClaims(http.MethodPost,
		"/api/v1/sync-roots/"+root.ID.String()+"/download-token", nil, claims)
	mr = withURLParams(mr, "rootID", root.ID.String())
	mw := httptest.NewRecorder()
	env.handler.MintDownloadToken(mw, mr)
	if mw.Code != http.StatusOK {
		t.Fatalf("mint status %d body=%s", mw.Code, mw.Body.String())
	}
	var tok struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(mw.Body.Bytes(), &tok)

	sr := httptest.NewRequest(http.MethodGet, "/download/"+tok.Token, nil)
	sr = withURLParams(sr, "token", tok.Token)
	sw := httptest.NewRecorder()
	env.handler.ServeTokenDownload(sw, sr)
	if sw.Code != http.StatusOK {
		t.Fatalf("serve status %d body=%s", sw.Code, sw.Body.String())
	}
	disp := sw.Header().Get("Content-Disposition")
	if !strings.Contains(disp, `filename="root1.zip"`) {
		t.Errorf("filename: %q, want contains root1.zip", disp)
	}

	zr, err := zip.NewReader(bytes.NewReader(sw.Body.Bytes()), int64(sw.Body.Len()))
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
}

// serveDownloadToken hits the public GET /download/{token} endpoint, with an
// optional Range header.
func serveDownloadToken(t *testing.T, env *testEnv, token, rangeHdr string) *httptest.ResponseRecorder {
	t.Helper()
	sr := httptest.NewRequest(http.MethodGet, "/download/"+token, nil)
	if rangeHdr != "" {
		sr.Header.Set("Range", rangeHdr)
	}
	sr = withURLParams(sr, "token", token)
	sw := httptest.NewRecorder()
	env.handler.ServeTokenDownload(sw, sr)
	return sw
}

// A token minted for a file serves the file itself — not a ZIP — so the web
// client can hand the download to the browser instead of buffering a Blob
// (ADR-0073). Range stays supported, which lets the browser resume.
func TestDownloadToken_FilePathServesRawFile(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	uploadHelper(t, env, claims, root, "/dir/report.bin", "0123456789", nil)

	mint := func() string {
		body, _ := json.Marshal(map[string]any{"path": "/dir/report.bin"})
		mr := reqWithClaims(http.MethodPost,
			"/api/v1/sync-roots/"+root.ID.String()+"/download-token",
			bytes.NewReader(body), claims)
		mr.Header.Set("Content-Type", "application/json")
		mr = withURLParams(mr, "rootID", root.ID.String())
		mw := httptest.NewRecorder()
		env.handler.MintDownloadToken(mw, mr)
		if mw.Code != http.StatusOK {
			t.Fatalf("mint status %d body=%s", mw.Code, mw.Body.String())
		}
		var tok struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(mw.Body.Bytes(), &tok)
		return tok.Token
	}

	token := mint()
	sw := serveDownloadToken(t, env, token, "")
	if sw.Code != http.StatusOK || sw.Body.String() != "0123456789" {
		t.Fatalf("serve status %d body=%q", sw.Code, sw.Body.String())
	}
	if disp := sw.Header().Get("Content-Disposition"); !strings.Contains(disp, `filename="report.bin"`) {
		t.Errorf("Content-Disposition = %q, want report.bin", disp)
	}
	if cl := sw.Header().Get("Content-Length"); cl != "10" {
		t.Errorf("Content-Length = %q, want 10", cl)
	}

	// Resuming reuses the same URL: the grant must survive the first request.
	pw := serveDownloadToken(t, env, token, "bytes=4-6")
	if pw.Code != http.StatusPartialContent || pw.Body.String() != "456" {
		t.Fatalf("resume status %d body=%q, want 206 \"456\"", pw.Code, pw.Body.String())
	}
}

// A ZIP grant stays single-use: its stream has no length to resume from.
func TestDownloadToken_ZipGrantIsSingleUse(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	uploadHelper(t, env, claims, root, "/dir/a.txt", "AAA", nil)

	token := mintDownloadToken(claims.UserID, root.ID, "/dir")
	if sw := serveDownloadToken(t, env, token, ""); sw.Code != http.StatusOK {
		t.Fatalf("first serve status %d body=%s", sw.Code, sw.Body.String())
	}
	if sw := serveDownloadToken(t, env, token, ""); sw.Code != http.StatusNotFound {
		t.Fatalf("second serve status %d, want 404", sw.Code)
	}
}

// TestDownloadToken_MalformedBodyRejected ensures the mint endpoint rejects a
// non-JSON body instead of silently issuing a whole-root token.
func TestDownloadToken_MalformedBodyRejected(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	mr := reqWithClaims(http.MethodPost,
		"/api/v1/sync-roots/"+root.ID.String()+"/download-token",
		bytes.NewReader([]byte("not json")), claims)
	mr.Header.Set("Content-Type", "application/json")
	mr = withURLParams(mr, "rootID", root.ID.String())
	mw := httptest.NewRecorder()
	env.handler.MintDownloadToken(mw, mr)
	if mw.Code != http.StatusBadRequest {
		t.Fatalf("mint status %d, want 400, body=%s", mw.Code, mw.Body.String())
	}
}
