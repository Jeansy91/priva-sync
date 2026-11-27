package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/auth"
)

// getWithRoot issues a GET carrying claims + {rootID} URL param.
func getWithRoot(h func(http.ResponseWriter, *http.Request), claims *auth.Claims, rootID, target string) *httptest.ResponseRecorder {
	r := reqWithClaims(http.MethodGet, target, nil, claims)
	r = withURLParams(r, "rootID", rootID)
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestDirectoryStats_RejectsMalformedRootID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	w := getWithRoot(env.handler.DirectoryStats, claims, "bad", "/sync-roots/bad/stats")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRootID)
}

func TestDirectoryStats_RejectsUnknownRoot(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	unknown := uuid.New()
	w := getWithRoot(env.handler.DirectoryStats, claims, unknown.String(), "/sync-roots/"+unknown.String()+"/stats")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSyncRootNotFound)
}

func TestDirectoryStats_Succeeds(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, claims, root.ID, "/dir/a.txt", []byte("hello"))

	w := getWithRoot(env.handler.DirectoryStats, claims, root.ID.String(),
		"/sync-roots/"+root.ID.String()+"/stats?path=/dir")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestGetChanges_RejectsMalformedRootID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	w := getWithRoot(env.handler.GetChanges, claims, "bad", "/sync-roots/bad/changes")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRootID)
}

func TestGetChanges_RejectsUnknownRoot(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	unknown := uuid.New()
	w := getWithRoot(env.handler.GetChanges, claims, unknown.String(), "/sync-roots/"+unknown.String()+"/changes")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSyncRootNotFound)
}

func TestGetChanges_RejectsInvalidSince(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	w := getWithRoot(env.handler.GetChanges, claims, root.ID.String(),
		"/sync-roots/"+root.ID.String()+"/changes?feed_version=2&since=not-a-date")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidSince)
}

// ADR-0065: the feed now merges in move_events, so a client that never
// declares feed_version=2 must get a loud error instead of a feed with an
// invisible move -- unconditionally, not just when a move is pending.
func TestGetChanges_RequiresFeedVersion(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	w := getWithRoot(env.handler.GetChanges, claims, root.ID.String(),
		"/sync-roots/"+root.ID.String()+"/changes")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrFeedVersionRequired)

	w = getWithRoot(env.handler.GetChanges, claims, root.ID.String(),
		"/sync-roots/"+root.ID.String()+"/changes?feed_version=1")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("feed_version=1: status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrFeedVersionRequired)
}

func TestGetChanges_Succeeds(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, claims, root.ID, "/a.txt", []byte("hello"))

	w := getWithRoot(env.handler.GetChanges, claims, root.ID.String(),
		"/sync-roots/"+root.ID.String()+"/changes?feed_version=2")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

// A directory move must surface as exactly one moves[] entry, not one
// changes[] row per descendant (ADR-0065).
func TestGetChanges_SurfacesADirectoryMoveAsOnePrefixEvent(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, claims, root.ID, "/old/sub/a.txt", []byte("hello"))

	if _, err := env.files.MoveDirectory(context.Background(), root.ID, "/old", "/new", claims.UserID); err != nil {
		t.Fatalf("MoveDirectory: %v", err)
	}

	w := getWithRoot(env.handler.GetChanges, claims, root.ID.String(),
		"/sync-roots/"+root.ID.String()+"/changes?feed_version=2")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Moves []struct {
			OldPrefix string `json:"old_prefix"`
			NewPrefix string `json:"new_prefix"`
		} `json:"moves"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Moves) != 1 {
		t.Fatalf("moves = %+v, want exactly 1", resp.Moves)
	}
	if resp.Moves[0].OldPrefix != "/old" || resp.Moves[0].NewPrefix != "/new" {
		t.Fatalf("move = %+v, want old=/old new=/new", resp.Moves[0])
	}
}

func TestDownloadFile_RejectsMalformedRootID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	w := getWithRoot(env.handler.DownloadFile, claims, "bad", "/sync-roots/bad/files/download?path=/a.txt")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRootID)
}

func TestDownloadFile_RejectsUnknownRoot(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	unknown := uuid.New()
	w := getWithRoot(env.handler.DownloadFile, claims, unknown.String(),
		"/sync-roots/"+unknown.String()+"/files/download?path=/a.txt")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSyncRootNotFound)
}

func TestDownloadFile_RejectsUnknownPath(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	w := getWithRoot(env.handler.DownloadFile, claims, root.ID.String(),
		"/sync-roots/"+root.ID.String()+"/files/download?path=/missing.txt")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrFileNotFound)
}

func TestDownloadFile_ServesContent(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, claims, root.ID, "/a.txt", []byte("served-bytes"))

	w := getWithRoot(env.handler.DownloadFile, claims, root.ID.String(),
		"/sync-roots/"+root.ID.String()+"/files/download?path=/a.txt")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "served-bytes" {
		t.Errorf("body = %q, want %q", got, "served-bytes")
	}
}

// getRangeWithRoot is getWithRoot plus a Range header — the only difference
// that matters for a partial-content download.
func getRangeWithRoot(
	h func(http.ResponseWriter, *http.Request),
	claims *auth.Claims, rootID, target, byteRange string,
) *httptest.ResponseRecorder {
	r := reqWithClaims(http.MethodGet, target, nil, claims)
	r = withURLParams(r, "rootID", rootID)
	r.Header.Set("Range", byteRange)
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

// A range request must transfer only the requested window. The desktop
// Cloud-Filter hydration asks for exactly the bytes Explorer touched; without
// this the whole file crosses the wire per request.
func TestDownloadFile_ServesRequestedByteRange(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, claims, root.ID, "/a.txt", []byte("0123456789"))

	w := getRangeWithRoot(env.handler.DownloadFile, claims, root.ID.String(),
		"/sync-roots/"+root.ID.String()+"/files/download?path=/a.txt", "bytes=2-5")

	if w.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206; body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "2345" {
		t.Errorf("body = %q, want %q", got, "2345")
	}
	if got := w.Header().Get("Content-Range"); got != "bytes 2-5/10" {
		t.Errorf("Content-Range = %q, want %q", got, "bytes 2-5/10")
	}
	// The client takes the file's identity from this header, so a partial
	// response must still carry it — it describes the whole file, not the window.
	if w.Header().Get("X-Content-Hash") == "" {
		t.Error("X-Content-Hash missing on a partial response")
	}
}
