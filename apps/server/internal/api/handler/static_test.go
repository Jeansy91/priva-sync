package handler

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestSPAHandler(t *testing.T) *SPAHandler {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>index</html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatalf("write app.js: %v", err)
	}
	return NewSPAHandler(dir)
}

func TestSPAHandler_ServesExistingAsset(t *testing.T) {
	h := newTestSPAHandler(t)

	r := httptest.NewRequest("GET", "/app.js", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != "console.log(1)" {
		t.Errorf("body = %q, want asset content", w.Body.String())
	}
}

func TestSPAHandler_FallsBackToIndexForUnknownRoute(t *testing.T) {
	h := newTestSPAHandler(t)

	r := httptest.NewRequest("GET", "/settings/profile", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != "<html>index</html>" {
		t.Errorf("body = %q, want index.html content", w.Body.String())
	}
}

func TestSPAHandler_404sApiAndUpdatesAndHealthz(t *testing.T) {
	h := newTestSPAHandler(t)

	for _, path := range []string{"/api/v1/files", "/updates/linux-x86_64/1.0.0", "/healthz"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Errorf("ServeHTTP(%s) status = %d, want 404", path, w.Code)
		}
	}
}

// stdlib's http.ServeFile independently rejects any ".." segment in
// r.URL.Path (net/http.containsDotDot), so a traversal attempt never reaches
// the filesystem regardless of our own clean+prefix guard.
func TestSPAHandler_RejectsPathTraversal(t *testing.T) {
	h := newTestSPAHandler(t)

	r := httptest.NewRequest("GET", "/../../../etc/passwd", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

// A sibling directory whose name starts with the root's name must stay
// unreachable, also with an encoded or embedded ".." segment.
func TestSPAHandler_DoesNotServeSiblingOfRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "web")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("index"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "web-secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewSPAHandler(root)

	for _, target := range []string{"/a/../../web-secret.txt", "/%2e%2e/web-secret.txt"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		if w.Code != 400 || strings.Contains(w.Body.String(), "secret") {
			t.Errorf("GET %s: status = %d, body = %q; want 400 without the secret", target, w.Code, w.Body.String())
		}
	}
}
