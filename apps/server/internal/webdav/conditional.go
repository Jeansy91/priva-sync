package webdav

import (
	"net/http"
	"strings"
)

// mutatingMethods are the WebDAV methods whose preconditions the adapter must
// enforce against the request-target resource. GET/HEAD/PROPFIND are excluded:
// http.ServeContent already honours conditional GETs via the same ETag.
var mutatingMethods = map[string]bool{
	"PUT":       true,
	"DELETE":    true,
	"MKCOL":     true,
	"PROPPATCH": true,
	"MOVE":      true,
	"COPY":      true,
}

// checkPrecondition enforces RFC 7232 If-Match / If-None-Match for
// state-changing methods. gwebdav does not implement these, so the adapter
// does, using the content-hash ETag. It returns true to proceed; on a failed
// precondition it writes 412 and returns false.
func (d *davFS) checkPrecondition(w http.ResponseWriter, r *http.Request) bool {
	if !mutatingMethods[r.Method] {
		return true
	}
	ifMatch := r.Header.Get("If-Match")
	ifNoneMatch := r.Header.Get("If-None-Match")
	if ifMatch == "" && ifNoneMatch == "" {
		return true
	}

	etag, exists, err := d.currentETag(r.Context(), r.URL.Path)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return false
	}

	if ifMatch != "" && !ifMatchSatisfied(ifMatch, etag, exists) {
		http.Error(w, "Precondition Failed", http.StatusPreconditionFailed)
		return false
	}
	if ifNoneMatch != "" && !ifNoneMatchSatisfied(ifNoneMatch, etag, exists) {
		http.Error(w, "Precondition Failed", http.StatusPreconditionFailed)
		return false
	}
	return true
}

// ifMatchSatisfied reports whether an If-Match header passes: "*" requires the
// resource to exist; an etag list requires the current strong validator to be
// among them.
func ifMatchSatisfied(header, etag string, exists bool) bool {
	if strings.TrimSpace(header) == "*" {
		return exists
	}
	return exists && etagInList(header, etag)
}

// ifNoneMatchSatisfied reports whether an If-None-Match header passes: "*"
// requires the resource to be absent; an etag list requires none to match.
func ifNoneMatchSatisfied(header, etag string, exists bool) bool {
	if strings.TrimSpace(header) == "*" {
		return !exists
	}
	return !(exists && etagInList(header, etag))
}

// etagInList reports whether etag appears in a comma-separated header list,
// comparing on the strong validator (the weak "W/" prefix is stripped).
func etagInList(header, etag string) bool {
	if etag == "" {
		return false
	}
	for _, candidate := range strings.Split(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(candidate), "W/") == etag {
			return true
		}
	}
	return false
}
