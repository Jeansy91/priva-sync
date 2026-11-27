package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// SPAHandler serves a built single-page-app from a directory: hashed assets
// return a 200, and any unmatched path falls back to index.html so
// client-side routing works.
type SPAHandler struct {
	root      string
	indexPath string
	fs        http.Handler
}

func NewSPAHandler(root string) *SPAHandler {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return &SPAHandler{
		root:      abs,
		indexPath: filepath.Join(abs, "index.html"),
		fs:        http.FileServer(http.Dir(abs)),
	}
}

func (h *SPAHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// API/Update/Health-Pfade nicht auf SPA fallen lassen — JSON-404 ist korrekt.
	if strings.HasPrefix(r.URL.Path, "/api/") ||
		strings.HasPrefix(r.URL.Path, "/updates/") ||
		r.URL.Path == "/healthz" {
		http.NotFound(w, r)
		return
	}

	// nosemgrep: go.lang.security.filepath-clean-misuse.filepath-clean-misuse -- Clean is not the sanitiser here; the resolved candidate is verified below to stay under the absolute root (h.root is filepath.Abs'd in the constructor), which is the actual traversal guard
	clean := filepath.Clean(r.URL.Path)
	if strings.HasPrefix(clean, "..") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	candidate := filepath.Join(h.root, clean)
	// Trailing separator on the prefix so a sibling like `<root>-evil` cannot
	// pass as if it were inside `<root>`; the root itself is also allowed.
	if candidate != h.root && !strings.HasPrefix(candidate, h.root+string(os.PathSeparator)) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	// #nosec G703 -- the taint analysis follows r.URL.Path into os.Stat and does
	// not model the containment check directly above: a candidate outside the
	// absolute root is answered with 400 before reaching here, and Clean on an
	// absolute URL path cannot escape upwards in the first place. The bytes are
	// then served by http.FileServer over http.Dir, which sanitises again --
	// candidate is only ever stat'ed. Same reasoning as the nosemgrep above.
	info, err := os.Stat(candidate)
	if err == nil && !info.IsDir() {
		h.fs.ServeHTTP(w, r)
		return
	}

	http.ServeFile(w, r, h.indexPath)
}
