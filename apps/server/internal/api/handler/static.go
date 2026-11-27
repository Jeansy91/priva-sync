package handler

import (
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// SPAHandler serves a built single-page-app from a directory: hashed assets
// return a 200, and any unmatched path falls back to index.html so
// client-side routing works.
type SPAHandler struct {
	root      string
	indexPath string
	fsys      fs.FS
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
		fsys:      os.DirFS(abs),
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

	// A ".." segment is never legitimate for a static asset.
	for _, seg := range strings.Split(r.URL.Path, "/") {
		if seg == ".." {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
	}

	// The lookup goes through an fs.FS rooted at the asset directory, which
	// only accepts slash-separated paths below that root. The file is only
	// stat'ed here; its bytes are served by http.FileServer over http.Dir.
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "."
	}
	info, err := fs.Stat(h.fsys, name)
	if err == nil && !info.IsDir() {
		h.fs.ServeHTTP(w, r)
		return
	}

	http.ServeFile(w, r, h.indexPath)
}
