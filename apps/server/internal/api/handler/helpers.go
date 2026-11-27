package handler

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"

	"privasync/server/internal/model"
)

// isLiveFile reports whether a FindByPath lookup landed on a regular file
// that still exists: no error, no tombstone, and not a directory.
//
// The handlers that serve bytes used to ask only the first two, because a row
// at a path meant a file -- directories had no rows to find. Since ADR-0041
// they do, so the short check answers "is there anything here", which is a
// different question and turns every directory share into a file share.
func isLiveFile(f *model.File, err error) bool {
	return err == nil && !f.IsDeleted && !f.IsDir
}

// newSecureToken returns a 256-bit random token, base64url-encoded — the
// format shared by reset and invite links.
func newSecureToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(raw), nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}
