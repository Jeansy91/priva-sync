package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/auth"
	"privasync/server/internal/model"
)

// trashFile seeds a file and soft-deletes it through DeleteFile, then returns
// its trashed row (as surfaced by ListTrash) so the trash-lifecycle handlers
// have a real record to act on.
func trashFile(t *testing.T, env *testEnv, claims *auth.Claims, root *model.SyncRoot, path string) uuid.UUID {
	t.Helper()
	seedFile(t, env, claims, root, path, "trash me")

	r := reqWithClaims(http.MethodDelete, "/sync-roots/"+root.ID.String()+"/files?path="+path, nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.DeleteFile(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete for trash %s: status %d body=%s", path, w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	trashed, err := env.trash.ListTrash(t.Context(), root.ID)
	if err != nil {
		t.Fatalf("list trash: %v", err)
	}
	for _, f := range trashed {
		if f.Path == path {
			return f.ID
		}
	}
	t.Fatalf("file %s not found in trash", path)
	return uuid.Nil
}

func TestListTrash_IncludesDeletedFile(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	trashFile(t, env, claims, root, "/x.txt")

	r := reqWithClaims(http.MethodGet, "/sync-roots/"+root.ID.String()+"/trash", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.ListTrash(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var entries []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(entries) != 1 || entries[0]["path"] != "/x.txt" {
		t.Errorf("unexpected trash listing: %v", entries)
	}
}

func TestListTrash_RejectsMalformedRootID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)

	r := reqWithClaims(http.MethodGet, "/sync-roots/bad/trash", nil, claims)
	r = withURLParams(r, "rootID", "bad")
	w := httptest.NewRecorder()
	env.handler.ListTrash(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRootID)
}

func TestRestoreTrash_BringsFileBack(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	fileID := trashFile(t, env, claims, root, "/x.txt")

	r := reqWithClaims(http.MethodPost, "/sync-roots/"+root.ID.String()+"/trash/"+fileID.String()+"/restore", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String(), "fileID", fileID.String())
	w := httptest.NewRecorder()
	env.handler.RestoreTrash(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	f, err := env.files.FindByPath(t.Context(), root.ID, "/x.txt")
	if err != nil {
		t.Fatalf("restored file not found: %v", err)
	}
	if f.IsDeleted {
		t.Errorf("file still marked deleted after restore")
	}
}

func TestRestoreTrash_RejectsUnknownFile(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	unknown := uuid.New()

	r := reqWithClaims(http.MethodPost, "/sync-roots/"+root.ID.String()+"/trash/"+unknown.String()+"/restore", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String(), "fileID", unknown.String())
	w := httptest.NewRecorder()
	env.handler.RestoreTrash(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrTrashedNotFound)
}

func TestRestoreTrash_RejectsMalformedFileID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")

	r := reqWithClaims(http.MethodPost, "/sync-roots/"+root.ID.String()+"/trash/bad/restore", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String(), "fileID", "bad")
	w := httptest.NewRecorder()
	env.handler.RestoreTrash(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidFileID)
}

func TestPurgeTrash_RemovesRecord(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	fileID := trashFile(t, env, claims, root, "/x.txt")

	r := reqWithClaims(http.MethodDelete, "/sync-roots/"+root.ID.String()+"/trash/"+fileID.String(), nil, claims)
	r = withURLParams(r, "rootID", root.ID.String(), "fileID", fileID.String())
	w := httptest.NewRecorder()
	env.handler.PurgeTrash(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}

	remaining, err := env.trash.ListTrash(t.Context(), root.ID)
	if err != nil {
		t.Fatalf("list trash: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("trash still holds %d records after purge", len(remaining))
	}
}

func TestPurgeTrash_RejectsUnknownFile(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	unknown := uuid.New()

	r := reqWithClaims(http.MethodDelete, "/sync-roots/"+root.ID.String()+"/trash/"+unknown.String(), nil, claims)
	r = withURLParams(r, "rootID", root.ID.String(), "fileID", unknown.String())
	w := httptest.NewRecorder()
	env.handler.PurgeTrash(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrTrashedNotFound)
}

func TestPurgeTrash_RejectsForeignRoot(t *testing.T) {
	env := newTestEnv(t)
	owner := createTestUser(t, env)
	root := createTestSyncRoot(t, env, owner.UserID, "root1")
	fileID := trashFile(t, env, owner, root, "/x.txt")
	intruder := createTestUser(t, env)

	r := reqWithClaims(http.MethodDelete, "/sync-roots/"+root.ID.String()+"/trash/"+fileID.String(), nil, intruder)
	r = withURLParams(r, "rootID", root.ID.String(), "fileID", fileID.String())
	w := httptest.NewRecorder()
	env.handler.PurgeTrash(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSyncRootNotFound)
}

func TestEmptyTrash_PurgesEverything(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root1")
	trashFile(t, env, claims, root, "/a.txt")
	trashFile(t, env, claims, root, "/b.txt")

	r := reqWithClaims(http.MethodDelete, "/trash", nil, claims)
	w := httptest.NewRecorder()
	env.handler.EmptyTrash(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	remaining, err := env.trash.ListTrash(t.Context(), root.ID)
	if err != nil {
		t.Fatalf("list trash: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("trash not empty after EmptyTrash: %d records", len(remaining))
	}
}
