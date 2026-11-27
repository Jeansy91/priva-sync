package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicShare_RejectsUnknownToken(t *testing.T) {
	env := newTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/public/shares/nope/", nil)
	r = withURLParams(r, "token", "nope")
	w := httptest.NewRecorder()
	env.shareH.PublicShare(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrShareNotFound)
}

func TestPublicShare_ReturnsViewForValidToken(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	token := publicShareTokenForDir(t, env, claims)

	r := httptest.NewRequest(http.MethodGet, "/public/shares/"+token, nil)
	r = withURLParams(r, "token", token)
	w := httptest.NewRecorder()
	env.shareH.PublicShare(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	// The anonymous view must never echo the raw tokens back.
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["token"] != nil || resp["read_token"] != nil || resp["write_token"] != nil {
		t.Errorf("public view leaked a token: %v", resp)
	}
}

func TestPublicShareDownload_RejectsUnknownToken(t *testing.T) {
	env := newTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/public/shares/nope/download", nil)
	r = withURLParams(r, "token", "nope")
	w := httptest.NewRecorder()
	env.shareH.PublicShareDownload(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrShareNotFound)
}

func TestPublicShareDownload_ServesFileViaSub(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	token := publicShareTokenForDir(t, env, claims)

	r := httptest.NewRequest(http.MethodGet, "/public/shares/"+token+"/download?sub=/a.txt", nil)
	r = withURLParams(r, "token", token)
	w := httptest.NewRecorder()
	env.shareH.PublicShareDownload(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "payload-a.txt" {
		t.Errorf("served body = %q, want %q", got, "payload-a.txt")
	}
}

func TestPublicShareDownload_RejectsSubEscape(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	token := publicShareTokenForDir(t, env, claims)

	r := httptest.NewRequest(http.MethodGet, "/public/shares/"+token+"/download?sub=../../etc/passwd", nil)
	r = withURLParams(r, "token", token)
	w := httptest.NewRecorder()
	env.shareH.PublicShareDownload(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidPath)
}

func TestPublicShareDownload_ZipsDirectory(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	token := publicShareTokenForDir(t, env, claims)

	// No sub → the share root is a directory → ZIP stream.
	r := httptest.NewRequest(http.MethodGet, "/public/shares/"+token+"/download", nil)
	r = withURLParams(r, "token", token)
	w := httptest.NewRecorder()
	env.shareH.PublicShareDownload(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
}
