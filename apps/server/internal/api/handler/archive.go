package handler

import (
	"archive/zip"
	"bytes"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
)

type archiveEntry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

// GET /api/v1/sync-roots/{rootID}/files/archive?path=/foo.zip
// Liest das ZIP serverseitig und liefert sortierten Inhalt als JSON. So muss
// der Client das Archiv nicht vollständig herunterladen, nur um es zu zeigen.
func (h *FileHandler) ListArchive(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}
	root, err := h.syncRoots.GetSyncRoot(r.Context(), rootID, claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
		return
	}

	cleanPath, err := sanitizePath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidPath, "invalid path")
		return
	}
	if !strings.EqualFold(filepath.Ext(cleanPath), ".zip") {
		writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_ARCHIVE_FORMAT",
			"only .zip supported")
		return
	}

	dbFile, err := h.files.FindByPath(r.Context(), rootID, cleanPath)
	if !isLiveFile(dbFile, err) {
		writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
		return
	}

	// The whole ZIP is streamed into memory: archive/zip needs an io.ReaderAt
	// for random access. Very large archives would want a file-handle variant;
	// this is the pragmatic middle.
	var buf bytes.Buffer
	if err := h.storage.ServeFile(claims.UserID, root.ID.String(), cleanPath, &buf); err != nil {
		writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
		return
	}

	reader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARCHIVE", "could not read archive")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"entries": zipEntries(reader)})
}

// GET /api/v1/shared-mounts/{shareID}/archive?path=/foo.zip
// Mount-scoped mirror of ListArchive so a recipient can preview a .zip
// inside a Shared Mount without direct access to the giver's root. For a
// file-mount `path` is "/". See ADR-0012.
func (h *FileHandler) MountArchive(w http.ResponseWriter, r *http.Request) {
	share := h.resolveMountForThumb(w, r)
	if share == nil {
		return
	}
	target, ok := mountSubPath(w, r, share, false)
	if !ok {
		return
	}
	cleanPath, err := sanitizePath(target)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidPath, "invalid path")
		return
	}
	if !strings.EqualFold(filepath.Ext(cleanPath), ".zip") {
		writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_ARCHIVE_FORMAT",
			"only .zip supported")
		return
	}

	dbFile, err := h.files.FindByPath(r.Context(), *share.SyncRootID, cleanPath)
	if !isLiveFile(dbFile, err) {
		writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
		return
	}

	var buf bytes.Buffer
	if err := h.storage.ServeFile(share.UserID, share.SyncRootID.String(), cleanPath, &buf); err != nil {
		writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
		return
	}

	reader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARCHIVE", "could not read archive")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"entries": zipEntries(reader)})
}

func zipEntries(reader *zip.Reader) []archiveEntry {
	entries := make([]archiveEntry, 0, len(reader.File))
	for _, f := range reader.File {
		entries = append(entries, archiveEntry{
			Name:  f.Name,
			Size:  int64(f.UncompressedSize64), //#nosec G115 -- listing metadata only; a crafted size merely wraps to a wrong displayed number, nothing allocates or indexes on it
			IsDir: f.FileInfo().IsDir(),
		})
	}
	return entries
}

// startZipResponse writes the archive headers and opens the writer. Callers
// that stream a subtree call it on their first entry rather than up front:
// until one file has been found the response can still be a 404, and an
// archive with nothing in it is worse than an error.
func startZipResponse(w http.ResponseWriter, name string) (*zip.Writer, http.Flusher) {
	filename := sanitizeName(name)
	if filename == "" {
		filename = "archive"
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDispositionAttachment(filename+".zip"))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Reverse-proxy hint: do not buffer, or the client sees nothing for a long
	// time and the connection can time out.
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, _ := w.(http.Flusher)
	return zip.NewWriter(w), flusher
}
