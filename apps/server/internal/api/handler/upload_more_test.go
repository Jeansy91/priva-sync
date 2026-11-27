package handler

import (
	"bytes"
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"lukechampine.com/blake3"

	"privasync/server/internal/auth"
	"privasync/server/internal/model"
)

// putChunk uploads a single chunk of an existing session at byte offset.
func putChunk(env *testEnv, claims *auth.Claims, root *model.SyncRoot, uploadID uuid.UUID, offset string, content []byte) *httptest.ResponseRecorder {
	r := reqWithClaims(http.MethodPut, "/sync-roots/"+root.ID.String()+"/uploads/"+uploadID.String()+"/chunks/"+offset, bytes.NewReader(content), claims)
	r = withURLParams(r, "rootID", root.ID.String(), "uploadID", uploadID.String(), "offset", offset)
	w := httptest.NewRecorder()
	env.uploadH.UploadChunk(w, r)
	return w
}

// completeUpload posts the complete request, optionally with an If-Match
// header, and returns the verdict. Complete only accepts the work and answers
// 202; the test environment assembles inline, so asking a second time
// replays the finished result and the assertions stay about the outcome rather
// than about the acceptance.
func completeUpload(env *testEnv, claims *auth.Claims, root *model.SyncRoot, uploadID uuid.UUID, ifMatch string) *httptest.ResponseRecorder {
	w := postComplete(env, claims, root, uploadID, ifMatch)
	if w.Code == http.StatusAccepted {
		w = postComplete(env, claims, root, uploadID, ifMatch)
	}
	return w
}

func postComplete(env *testEnv, claims *auth.Claims, root *model.SyncRoot, uploadID uuid.UUID, ifMatch string) *httptest.ResponseRecorder {
	r := reqWithClaims(http.MethodPost, "/sync-roots/"+root.ID.String()+"/uploads/"+uploadID.String()+"/complete", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String(), "uploadID", uploadID.String())
	if ifMatch != "" {
		r.Header.Set("If-Match", ifMatch)
	}
	w := httptest.NewRecorder()
	env.uploadH.CompleteUpload(w, r)
	return w
}

func TestInitUpload_RejectsMalformedRootID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)

	r := reqWithClaims(http.MethodPost, "/sync-roots/bad/uploads", bytes.NewBufferString(`{"path":"/a.txt","total_size":5}`), claims)
	r = withURLParams(r, "rootID", "bad")
	w := httptest.NewRecorder()
	env.uploadH.InitUpload(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRootID)
}

func TestInitUpload_RejectsUnknownRoot(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	unknown := uuid.New()

	r := reqWithClaims(http.MethodPost, "/sync-roots/"+unknown.String()+"/uploads", bytes.NewBufferString(`{"path":"/a.txt","total_size":5}`), claims)
	r = withURLParams(r, "rootID", unknown.String())
	w := httptest.NewRecorder()
	env.uploadH.InitUpload(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSyncRootNotFound)
}

func TestInitUpload_AcceptsContentHashAndMtime(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	sum := blake3.Sum256([]byte("payload"))
	body := `{"path":"/a.txt","total_size":7,"content_hash":"` + hex.EncodeToString(sum[:]) +
		`","mtime":"2026-01-02T15:04:05Z"}`
	r := reqWithClaims(http.MethodPost, "/sync-roots/"+root.ID.String()+"/uploads", bytes.NewBufferString(body), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.uploadH.InitUpload(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
}

func TestUploadChunk_RejectsMalformedUploadID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	r := reqWithClaims(http.MethodPut, "/sync-roots/"+root.ID.String()+"/uploads/not-a-uuid/chunks/0", bytes.NewReader([]byte("x")), claims)
	r = withURLParams(r, "rootID", root.ID.String(), "uploadID", "not-a-uuid", "offset", "0")
	w := httptest.NewRecorder()
	env.uploadH.UploadChunk(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidUploadID)
}

func TestUploadChunk_RejectsNegativeOffset(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())
	uploadID := initUpload(t, env, claims, root.ID, "/f.txt", 5)

	w := putChunk(env, claims, root, uploadID, "-1", []byte("x"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidChunkOffset)
}

func TestUploadChunk_RejectsUnknownSession(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	w := putChunk(env, claims, root, uuid.New(), "0", []byte("x"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrUploadSessionMixed)
}

func TestCompleteUpload_RejectsMalformedRootID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)

	r := reqWithClaims(http.MethodPost, "/sync-roots/bad/uploads/"+uuid.New().String()+"/complete", nil, claims)
	r = withURLParams(r, "rootID", "bad", "uploadID", uuid.New().String())
	w := httptest.NewRecorder()
	env.uploadH.CompleteUpload(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRootID)
}

func TestCompleteUpload_RejectsUnknownSession(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	w := completeUpload(env, claims, root, uuid.New(), "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrUploadSessionMixed)
}

func TestCompleteUpload_RejectsSessionFromDifferentRoot(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	rootA := createTestSyncRoot(t, env, claims.UserID, "rootA_"+uniqueSuffix())
	rootB := createTestSyncRoot(t, env, claims.UserID, "rootB_"+uniqueSuffix())
	uploadID := initUpload(t, env, claims, rootA.ID, "/f.txt", 5)

	// Complete against rootB while the session belongs to rootA.
	w := completeUpload(env, claims, rootB, uploadID, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSessionWrongRoot)
}

func TestCompleteUpload_RejectsContentHashMismatch(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	content := []byte("real content")
	wrong := blake3.Sum256([]byte("not the content"))
	body := `{"path":"/m.txt","total_size":` + strconv.Itoa(len(content)) + `,"content_hash":"` + hex.EncodeToString(wrong[:]) + `"}`
	uploadID := initUploadRaw(t, env, claims, root, body)

	if w := putChunk(env, claims, root, uploadID, "0", content); w.Code != http.StatusOK {
		t.Fatalf("chunk status = %d; body=%s", w.Code, w.Body.String())
	}
	w := completeUpload(env, claims, root, uploadID, "")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrContentHashMismatch)
}

func TestCompleteUpload_RejectsInvalidIfMatch(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	content := []byte("data")
	uploadID := initUpload(t, env, claims, root.ID, "/i.txt", len(content))
	if w := putChunk(env, claims, root, uploadID, "0", content); w.Code != http.StatusOK {
		t.Fatalf("chunk status = %d; body=%s", w.Code, w.Body.String())
	}
	w := completeUpload(env, claims, root, uploadID, "not-a-number")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestCompleteUpload_SucceedsWithMatchingHash(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	content := []byte("verified payload")
	sum := blake3.Sum256(content)
	body := `{"path":"/v.txt","total_size":` + strconv.Itoa(len(content)) + `,"content_hash":"` + hex.EncodeToString(sum[:]) +
		`","mtime":"2026-01-02T15:04:05Z"}`
	uploadID := initUploadRaw(t, env, claims, root, body)

	if w := putChunk(env, claims, root, uploadID, "0", content); w.Code != http.StatusOK {
		t.Fatalf("chunk status = %d; body=%s", w.Code, w.Body.String())
	}
	w := completeUpload(env, claims, root, uploadID, "")
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 200/201; body=%s", w.Code, w.Body.String())
	}
	if _, err := env.files.FindByPath(context.Background(), root.ID, "/v.txt"); err != nil {
		t.Fatalf("file not persisted after complete: %v", err)
	}
}

// initUploadRaw posts a raw InitUpload body and returns the new upload id.
func initUploadRaw(t *testing.T, env *testEnv, claims *auth.Claims, root *model.SyncRoot, body string) uuid.UUID {
	t.Helper()
	r := reqWithClaims(http.MethodPost, "/sync-roots/"+root.ID.String()+"/uploads", bytes.NewBufferString(body), claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.uploadH.InitUpload(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("initUploadRaw status = %d; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		UploadID uuid.UUID `json:"upload_id"`
	}
	decodeBody(t, w, &resp)
	return resp.UploadID
}
