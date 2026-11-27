package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"privasync/server/internal/auth"
	"privasync/server/internal/model"
)

// seedFile uploads content through the real UploadFile pipeline (staging →
// coordinator → storage) and returns the persisted row, so mutation tests
// operate on a file that actually exists on disk and in the DB.
func seedFile(t *testing.T, env *testEnv, claims *auth.Claims, root *model.SyncRoot, path, content string) *model.File {
	t.Helper()
	r := reqWithClaims("POST", "/sync-roots/"+root.ID.String()+"/files?path="+path, bytes.NewReader([]byte(content)), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.UploadFile(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("seed upload %s: status %d body=%s", path, w.Code, w.Body.String())
	}
	env.drainOutbox(t)
	f, err := env.files.FindByPath(context.Background(), root.ID, path)
	if err != nil {
		t.Fatalf("find seeded file %s: %v", path, err)
	}
	return f
}

// patchMove issues a PATCH move/copy against the own-root files endpoint.
func patchMove(env *testEnv, claims *auth.Claims, rootID, op, from, to string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"op": op, "from": from, "to": to})
	r := reqWithClaims(http.MethodPatch, "/sync-roots/"+rootID+"/files", bytes.NewReader(body), claims)
	r = withURLParams(r, "rootID", rootID)
	w := httptest.NewRecorder()
	env.handler.MoveFile(w, r)
	return w
}

func TestCopyFile_SucceedsAndMaterialisesDestination(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	src := seedFile(t, env, claims, root, "/a.txt", "hello copy")

	w := patchMove(env, claims, root.ID.String(), "copy", "/a.txt", "/b.txt")
	if w.Code != http.StatusOK {
		t.Fatalf("copy status = %d, body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	dst, err := env.files.FindByPath(context.Background(), root.ID, "/b.txt")
	if err != nil {
		t.Fatalf("destination not found after copy: %v", err)
	}
	if !bytes.Equal(dst.ContentHash, src.ContentHash) {
		t.Errorf("copied content hash differs from source")
	}
	// Source must survive a copy.
	if _, err := env.files.FindByPath(context.Background(), root.ID, "/a.txt"); err != nil {
		t.Errorf("source vanished after copy: %v", err)
	}
}

func TestCopyFile_RejectsUnknownSource(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	w := patchMove(env, claims, root.ID.String(), "copy", "/missing.txt", "/b.txt")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSourceNotInDB)
}

func TestCopyFile_RejectsOccupiedDestination(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	seedFile(t, env, claims, root, "/a.txt", "src")
	seedFile(t, env, claims, root, "/b.txt", "dst")

	w := patchMove(env, claims, root.ID.String(), "copy", "/a.txt", "/b.txt")
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrDestinationOccupied)
}

func TestMoveFile_SucceedsAndRemovesSource(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	seedFile(t, env, claims, root, "/a.txt", "move me")

	w := patchMove(env, claims, root.ID.String(), "move", "/a.txt", "/b.txt")
	if w.Code != http.StatusOK {
		t.Fatalf("move status = %d, body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	if _, err := env.files.FindByPath(context.Background(), root.ID, "/b.txt"); err != nil {
		t.Errorf("destination missing after move: %v", err)
	}
	if f, err := env.files.FindByPath(context.Background(), root.ID, "/a.txt"); err == nil && !f.IsDeleted {
		t.Errorf("source still live after move")
	}
}

func TestMoveFile_RejectsUnsupportedOp(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	w := patchMove(env, claims, root.ID.String(), "frobnicate", "/a.txt", "/b.txt")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrUnsupportedOp)
}

func TestMoveFile_RejectsSamePath(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	w := patchMove(env, claims, root.ID.String(), "move", "/a.txt", "/a.txt")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSamePath)
}

func TestMoveFile_RejectsMalformedBody(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	r := reqWithClaims(http.MethodPatch, "/sync-roots/"+root.ID.String()+"/files", bytes.NewReader([]byte("{not json")), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.MoveFile(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestMoveFile_RejectsUnknownSource(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	w := patchMove(env, claims, root.ID.String(), "move", "/missing.txt", "/b.txt")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSourceNotInDB)
}

func TestDeleteFile_SoftDeletesAndReturnsNoContent(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	seedFile(t, env, claims, root, "/gone.txt", "bye")

	r := reqWithClaims(http.MethodDelete, "/sync-roots/"+root.ID.String()+"/files?path=/gone.txt", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.DeleteFile(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	f, err := env.files.FindByPath(context.Background(), root.ID, "/gone.txt")
	if err == nil && !f.IsDeleted {
		t.Errorf("file not soft-deleted after DELETE")
	}
}

func TestDeleteFile_RejectsUnknownPath(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	r := reqWithClaims(http.MethodDelete, "/sync-roots/"+root.ID.String()+"/files?path=/nope.txt", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.DeleteFile(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrFileNotFound)
}

func TestResolveOwnRoot_RejectsMalformedRootID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)

	r := reqWithClaims(http.MethodDelete, "/sync-roots/not-a-uuid/files?path=/x.txt", nil, claims)
	r = withURLParams(r, "rootID", "not-a-uuid")
	w := httptest.NewRecorder()
	env.handler.DeleteFile(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRootID)
}

func TestResolveOwnRoot_RejectsForeignRoot(t *testing.T) {
	env := newTestEnv(t)
	owner := createTestUser(t, env)
	root := createTestSyncRoot(t, env, owner.UserID, "root1")
	intruder := createTestUser(t, env)

	r := reqWithClaims(http.MethodDelete, "/sync-roots/"+root.ID.String()+"/files?path=/x.txt", nil, intruder)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.DeleteFile(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSyncRootNotFound)
}

func TestCreateDirectory_CreatesEntry(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	body, _ := json.Marshal(map[string]any{"path": "/subdir"})
	r := reqWithClaims(http.MethodPost, "/sync-roots/"+root.ID.String()+"/directories", bytes.NewReader(body), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.CreateDirectory(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	dir, err := env.files.FindByPath(context.Background(), root.ID, "/subdir")
	if err != nil {
		t.Fatalf("directory not persisted: %v", err)
	}
	if !dir.IsDir {
		t.Errorf("created entry is not a directory")
	}
}

func TestCreateDirectory_RejectsMissingPath(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	body, _ := json.Marshal(map[string]any{"path": ""})
	r := reqWithClaims(http.MethodPost, "/sync-roots/"+root.ID.String()+"/directories", bytes.NewReader(body), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.CreateDirectory(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrPathRequired)
}

func TestCreateDirectory_ConflictWhenPathOccupiedByFile(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	seedFile(t, env, claims, root, "/busy", "iamfile")

	body, _ := json.Marshal(map[string]any{"path": "/busy"})
	r := reqWithClaims(http.MethodPost, "/sync-roots/"+root.ID.String()+"/directories", bytes.NewReader(body), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.CreateDirectory(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrDestinationOccupied)
}

// assertCode fails unless the JSON error body carries the expected code.
func assertCode(t *testing.T, w *httptest.ResponseRecorder, want string) {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error body: %v (body=%s)", err, w.Body.String())
	}
	if resp["code"] != want {
		t.Errorf("code = %v, want %q", resp["code"], want)
	}
}
