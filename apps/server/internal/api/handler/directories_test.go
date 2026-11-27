package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCreateDirectory_PersistsAndAppearsInListing verifies the round-trip
// for empty-directory sync: POST /directories writes an is_dir entry, GET
// /files returns it as a listing row with is_dir=true.
func TestCreateDirectory_PersistsAndAppearsInListing(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	// POST /sync-roots/{rootID}/directories
	body, _ := json.Marshal(map[string]any{"path": "/empty-folder"})
	r := reqWithClaims(http.MethodPost,
		"/api/v1/sync-roots/"+root.ID.String()+"/directories",
		bytes.NewReader(body), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.CreateDirectory(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("create dir: status %d, body=%s", w.Code, w.Body.String())
	}
	var created struct {
		Path  string `json:"path"`
		IsDir bool   `json:"is_dir"`
		Size  int64  `json:"size"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Path != "/empty-folder" || !created.IsDir || created.Size != 0 {
		t.Fatalf("unexpected created entry: %+v", created)
	}

	// GET /sync-roots/{rootID}/files?path=/
	r2 := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/files?path=/", nil, claims)
	r2 = withURLParams(r2, "rootID", root.ID.String())
	w2 := httptest.NewRecorder()
	env.handler.ListFiles(w2, r2)

	if w2.Code != http.StatusOK {
		t.Fatalf("list: status %d", w2.Code)
	}
	var resp struct {
		Entries []struct {
			Path  string `json:"path"`
			IsDir bool   `json:"is_dir"`
		} `json:"entries"`
	}
	listing := resp.Entries
	_ = listing
	if err := json.Unmarshal(w2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	listing = resp.Entries
	var found bool
	for _, e := range listing {
		if e.Path == "/empty-folder" && e.IsDir {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("dir not in listing: %+v", listing)
	}
}

// TestCreateDirectory_NestedBelowRoot verifies that a directory can be
// created at an arbitrary depth below the sync-root, not just at the top
// level.  Required so the UI can offer "create subfolder" anywhere in the
// tree.
func TestCreateDirectory_NestedBelowRoot(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "rootNested")

	mkdir := func(p string) {
		body, _ := json.Marshal(map[string]any{"path": p})
		r := reqWithClaims(http.MethodPost,
			"/api/v1/sync-roots/"+root.ID.String()+"/directories",
			bytes.NewReader(body), claims)
		r = withURLParams(r, "rootID", root.ID.String())
		w := httptest.NewRecorder()
		env.handler.CreateDirectory(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("mkdir %s: status %d, body=%s", p, w.Code, w.Body.String())
		}
	}
	mkdir("/parent")
	mkdir("/parent/child")
	mkdir("/parent/child/grandchild")

	// Listing of /parent/child must contain the grandchild dir.
	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/files?path=/parent/child",
		nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.ListFiles(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list: status %d", w.Code)
	}
	var resp struct {
		Entries []struct {
			Path  string `json:"path"`
			IsDir bool   `json:"is_dir"`
		} `json:"entries"`
	}
	listing := resp.Entries
	_ = listing
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	listing = resp.Entries
	var found bool
	for _, e := range listing {
		if e.Path == "/parent/child/grandchild" && e.IsDir {
			found = true
		}
	}
	if !found {
		t.Fatalf("grandchild missing in listing of /parent/child: %+v", listing)
	}
}

// TestGetChanges_IncludesIsDirForDirectoryEntries verifies that the
// /changes endpoint returns the is_dir flag for created directories.
// Without it, another client cannot distinguish server directories from
// files and won't recreate them locally.
func TestGetChanges_IncludesIsDirForDirectoryEntries(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "rootChg")

	// Verzeichnis anlegen.
	body, _ := json.Marshal(map[string]any{"path": "/some-dir"})
	r := reqWithClaims(http.MethodPost,
		"/api/v1/sync-roots/"+root.ID.String()+"/directories",
		bytes.NewReader(body), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.CreateDirectory(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create dir: %d, %s", w.Code, w.Body.String())
	}

	// Changes ohne since-Parameter (alles seit Epoch).
	r2 := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/changes?feed_version=2", nil, claims)
	r2 = withURLParams(r2, "rootID", root.ID.String())
	w2 := httptest.NewRecorder()
	env.handler.GetChanges(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("changes: %d, %s", w2.Code, w2.Body.String())
	}
	var resp struct {
		Changes []struct {
			Path  string `json:"path"`
			IsDir bool   `json:"is_dir"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var found bool
	for _, c := range resp.Changes {
		if c.Path == "/some-dir" {
			if !c.IsDir {
				t.Fatalf("is_dir muss true sein fuer /some-dir, got %+v", c)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("/some-dir nicht in changes-Liste: %s", w2.Body.String())
	}
}

// TestDeleteDirectory_CascadeTrashesDescendants verifies that DELETE on a
// dir entry moves the whole subtree (dir row + all nested files/dirs) into
// the trash.
func TestDeleteDirectory_CascadeTrashesDescendants(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "rootC")

	// Setup: /folder (dir) + /folder/a.txt + /folder/sub (dir) + /folder/sub/b.txt
	createDir := func(p string) {
		body, _ := json.Marshal(map[string]any{"path": p})
		r := reqWithClaims(http.MethodPost,
			"/api/v1/sync-roots/"+root.ID.String()+"/directories",
			bytes.NewReader(body), claims)
		r = withURLParams(r, "rootID", root.ID.String())
		w := httptest.NewRecorder()
		env.handler.CreateDirectory(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("create dir %s: %d, %s", p, w.Code, w.Body.String())
		}
	}
	uploadFile := func(p, content string) {
		r := reqWithClaims(http.MethodPut,
			"/api/v1/sync-roots/"+root.ID.String()+"/files?path="+p,
			bytes.NewBufferString(content), claims)
		r = withURLParams(r, "rootID", root.ID.String())
		w := httptest.NewRecorder()
		env.handler.UploadFile(w, r)
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("upload %s: %d, %s", p, w.Code, w.Body.String())
		}
		env.drainOutbox(t)
	}
	createDir("/folder")
	createDir("/folder/sub")
	uploadFile("/folder/a.txt", "aaa")
	uploadFile("/folder/sub/b.txt", "bb")

	// DELETE /sync-roots/{rootID}/files?path=/folder
	r := reqWithClaims(http.MethodDelete,
		"/api/v1/sync-roots/"+root.ID.String()+"/files?path=/folder", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.DeleteFile(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete dir: %d, %s", w.Code, w.Body.String())
	}

	// Listing /folder/ sollte leer sein (alles getrasht).
	r2 := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/files?path=/folder",
		nil, claims)
	r2 = withURLParams(r2, "rootID", root.ID.String())
	w2 := httptest.NewRecorder()
	env.handler.ListFiles(w2, r2)
	var listing []any
	_ = json.Unmarshal(w2.Body.Bytes(), &listing)
	if len(listing) != 0 {
		t.Fatalf("expected empty listing after cascade delete, got %d: %s", len(listing), w2.Body.String())
	}

	// Trash sollte beide Files enthalten.
	rt := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/trash", nil, claims)
	rt = withURLParams(rt, "rootID", root.ID.String())
	wt := httptest.NewRecorder()
	env.handler.ListTrash(wt, rt)
	var trash []struct {
		Path  string `json:"path"`
		IsDir bool   `json:"is_dir"`
	}
	if err := json.Unmarshal(wt.Body.Bytes(), &trash); err != nil {
		t.Fatalf("decode trash: %v", err)
	}
	got := map[string]bool{}
	for _, e := range trash {
		got[e.Path] = true
	}
	if !got["/folder/a.txt"] || !got["/folder/sub/b.txt"] {
		t.Fatalf("expected both files in trash, got %+v", trash)
	}
}

// TestCreateDirectory_ConflictWithExistingFile must return 409 when a file
// already exists at the path (path uniqueness across files + dirs).
func TestCreateDirectory_ConflictWithExistingFile(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root2")

	// Eine Datei am gleichen Pfad zuerst hochladen.
	uploadBody := bytes.NewBufferString("hello")
	r := reqWithClaims(http.MethodPut,
		"/api/v1/sync-roots/"+root.ID.String()+"/files?path=/clash",
		uploadBody, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.UploadFile(w, r)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("seed file: status %d, body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	// Jetzt CreateDirectory auf den gleichen Pfad → 409.
	body, _ := json.Marshal(map[string]any{"path": "/clash"})
	r2 := reqWithClaims(http.MethodPost,
		"/api/v1/sync-roots/"+root.ID.String()+"/directories",
		bytes.NewReader(body), claims)
	r2 = withURLParams(r2, "rootID", root.ID.String())
	w2 := httptest.NewRecorder()
	env.handler.CreateDirectory(w2, r2)
	if w2.Code != http.StatusConflict {
		t.Fatalf("expected 409 conflict, got %d, body=%s", w2.Code, w2.Body.String())
	}
}
