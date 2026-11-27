package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/auth"
)

// postFilesStat posts a raw FilesStat body against {rootID}.
func postFilesStat(env *testEnv, claims *auth.Claims, rootID, body string) *httptest.ResponseRecorder {
	r := reqWithClaims(http.MethodPost, "/sync-roots/"+rootID+"/files/stat", bytes.NewBufferString(body), claims)
	r = withURLParams(r, "rootID", rootID)
	w := httptest.NewRecorder()
	env.handler.FilesStat(w, r)
	return w
}

func TestFilesStat_RejectsMalformedRootID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)

	w := postFilesStat(env, claims, "bad", `{"paths":["/a.txt"]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRootID)
}

func TestFilesStat_RejectsUnknownRoot(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)

	w := postFilesStat(env, claims, uuid.New().String(), `{"paths":["/a.txt"]}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSyncRootNotFound)
}

// The ADR-0062 acceptance criterion: a retry must be able to tell a path the
// server never had apart from one it deleted, and must see a changed path's
// *current* state, not the one that was true when the retry row was queued.
func TestFilesStat_DistinguishesFoundDeletedAndUnknown(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	uploadFileAs(t, env, claims, root.ID, "/keep.txt", []byte("hello"))
	uploadFileAs(t, env, claims, root.ID, "/gone.txt", []byte("bye"))
	deleteFileAs(t, env, claims, root.ID, "/gone.txt")

	w := postFilesStat(env, claims, root.ID.String(),
		`{"paths":["/keep.txt","/gone.txt","/never.txt"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Results []fileStatEntry `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, w.Body.String())
	}
	if len(resp.Results) != 3 {
		t.Fatalf("results = %+v, want 3 entries (one per requested path, in order)", resp.Results)
	}

	keep, gone, never := resp.Results[0], resp.Results[1], resp.Results[2]
	if keep.Path != "/keep.txt" || !keep.Found || keep.IsDeleted {
		t.Errorf("keep.txt = %+v, want found=true is_deleted=false", keep)
	}
	if gone.Path != "/gone.txt" || !gone.Found || !gone.IsDeleted {
		t.Errorf("gone.txt = %+v, want found=true is_deleted=true", gone)
	}
	if never.Path != "/never.txt" || never.Found {
		t.Errorf("never.txt = %+v, want found=false", never)
	}
}

func TestFilesStat_RejectsInvalidBody(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	w := postFilesStat(env, claims, root.ID.String(), `not json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidBody)
}

func TestFilesStat_RejectsTooManyPaths(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	paths := make([]string, filesStatMaxPaths+1)
	for i := range paths {
		paths[i] = fmt.Sprintf(`"/p%d.txt"`, i)
	}
	body := `{"paths":[` + strings.Join(paths, ",") + `]}`

	w := postFilesStat(env, claims, root.ID.String(), body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrTooManyPaths)
}
