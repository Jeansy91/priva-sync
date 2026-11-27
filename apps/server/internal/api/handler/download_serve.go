package handler

import (
	"net/http"
	"path"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/storage"
)

// serveFileContent answers a download over a seekable handle rather than
// copying the whole file into the response. That is what buys Range support:
// http.ServeContent turns a range request into a 206 covering only the
// requested window, so a client fetching a few kilobytes of a multi-GB file no
// longer pulls the entire file across the wire (ADR-0047).
//
// Callers set their own identity headers (X-Content-Hash, X-Server-Version)
// beforehand; those describe the whole file and stay on a partial response.
func serveFileContent(
	w http.ResponseWriter,
	r *http.Request,
	store *storage.Service,
	ownerID uuid.UUID,
	rootID, filePath string,
	mtime time.Time,
) {
	f, err := store.Open(ownerID, rootID, filePath)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrFileNotFound, "file not found")
		return
	}
	defer f.Close()

	http.ServeContent(w, r, path.Base(filePath), mtime, f)
}
