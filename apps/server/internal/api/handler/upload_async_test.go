package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/auth"
	"privasync/server/internal/model"
)

// completeUploadRaw posts the complete request and returns whatever it
// answered — unlike completeUpload it does not follow up on a 202.
func completeUploadRaw(env *testEnv, claims *auth.Claims, root *model.SyncRoot, uploadID uuid.UUID) *httptest.ResponseRecorder {
	r := reqWithClaims(http.MethodPost, "/sync-roots/"+root.ID.String()+"/uploads/"+uploadID.String()+"/complete", nil, claims)
	r = withURLParams(r, "rootID", root.ID.String(), "uploadID", uploadID.String())
	w := httptest.NewRecorder()
	env.uploadH.CompleteUpload(w, r)
	return w
}

func uploadStatus(env *testEnv, claims *auth.Claims, root *model.SyncRoot, uploadID uuid.UUID, query string) *httptest.ResponseRecorder {
	r := reqWithClaims(http.MethodGet, "/sync-roots/"+root.ID.String()+"/uploads/"+uploadID.String()+"/status"+query, nil, claims)
	r = withURLParams(r, "rootID", root.ID.String(), "uploadID", uploadID.String())
	w := httptest.NewRecorder()
	env.uploadH.UploadStatus(w, r)
	return w
}

func decodeStatus(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal status body %q: %v", w.Body.String(), err)
	}
	return body
}

// oneChunkSession opens a session and delivers its single chunk, leaving it
// ready for a complete.
func oneChunkSession(t *testing.T, env *testEnv, claims *auth.Claims, root *model.SyncRoot, path string, content []byte) uuid.UUID {
	t.Helper()
	uploadID := initUpload(t, env, claims, root.ID, path, len(content))
	if w := putChunk(env, claims, root, uploadID, "0", content); w.Code != http.StatusOK {
		t.Fatalf("putChunk status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	return uploadID
}

// Assembling a multi-gigabyte upload takes minutes; no request survives that.
// Complete therefore only accepts the work.
func TestCompleteUpload_AcceptsTheWorkAndAnswers202(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	uploadID := oneChunkSession(t, env, claims, root, "/accepted.txt", []byte("assemble me later"))

	w := completeUploadRaw(env, claims, root, uploadID)
	if w.Code != http.StatusAccepted {
		t.Fatalf("CompleteUpload status = %d, want 202; body=%s", w.Code, w.Body.String())
	}
	body := decodeStatus(t, w)
	if body["status"] != model.UploadSessionQueued {
		t.Errorf("status = %v, want %q", body["status"], model.UploadSessionQueued)
	}
}

// The status endpoint is where the client picks the verdict up.
func TestUploadStatus_ReportsTheTerminalResult(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	uploadID := oneChunkSession(t, env, claims, root, "/verdict.txt", []byte("assembled"))
	if w := completeUploadRaw(env, claims, root, uploadID); w.Code != http.StatusAccepted {
		t.Fatalf("CompleteUpload status = %d, want 202", w.Code)
	}

	w := uploadStatus(env, claims, root, uploadID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("UploadStatus status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := decodeStatus(t, w)
	if body["status"] != model.UploadSessionCompleted {
		t.Fatalf("status = %v, want %q; body=%s", body["status"], model.UploadSessionCompleted, w.Body.String())
	}
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result object in %s", w.Body.String())
	}
	if result["kind"] != model.UploadResultOK {
		t.Errorf("result kind = %v, want %q", result["kind"], model.UploadResultOK)
	}
	if _, has := result["server_version"]; !has {
		t.Error("result carries no server_version")
	}
}

// A chunk arriving after the assembly was accepted would rewrite bytes the
// worker is reading, or bytes already committed.
func TestUploadChunk_RefusesAChunkOnceTheSessionIsClosed(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	content := []byte("no second thoughts")
	uploadID := oneChunkSession(t, env, claims, root, "/closed.txt", content)
	if w := completeUploadRaw(env, claims, root, uploadID); w.Code != http.StatusAccepted {
		t.Fatalf("CompleteUpload status = %d, want 202", w.Code)
	}

	w := putChunk(env, claims, root, uploadID, "0", content)
	if w.Code != http.StatusConflict {
		t.Fatalf("UploadChunk status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	if code := decodeStatus(t, w)["code"]; code != ErrUploadSessionClosed {
		t.Errorf("code = %v, want %q", code, ErrUploadSessionClosed)
	}
}

// Waiting is for a session in the worker's hands. A session still receiving
// chunks answers at once — a resuming client asks for exactly this.
func TestUploadStatus_DoesNotWaitOnAnOpenSession(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	uploadID := initUpload(t, env, claims, root.ID, "/still-open.txt", 32)

	start := time.Now()
	w := uploadStatus(env, claims, root, uploadID, "?wait=30s")
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("waited %s on an open session", elapsed)
	}
	if got := decodeStatus(t, w)["status"]; got != model.UploadSessionOpen {
		t.Errorf("status = %v, want %q", got, model.UploadSessionOpen)
	}
}

// The long poll keeps the normal case quasi-synchronous: the client asks once
// and is answered the moment the worker is done.
func TestUploadStatus_WaitReturnsAsSoonAsTheAssemblyIsDone(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	uploadID := initUpload(t, env, claims, root.ID, "/long-poll.txt", 32)
	if _, err := env.uploads.MarkQueued(t.Context(), uploadID, nil); err != nil {
		t.Fatalf("MarkQueued: %v", err)
	}

	version := int64(3)
	timer := time.AfterFunc(200*time.Millisecond, func() {
		_ = env.uploads.MarkCompleted(context.Background(), uploadID,
			model.UploadSessionResult{Kind: model.UploadResultOK, ServerVersion: &version},
			time.Hour)
	})
	defer timer.Stop()

	start := time.Now()
	w := uploadStatus(env, claims, root, uploadID, "?wait=30s")
	elapsed := time.Since(start)

	if got := decodeStatus(t, w)["status"]; got != model.UploadSessionCompleted {
		t.Fatalf("status = %v, want %q; body=%s", got, model.UploadSessionCompleted, w.Body.String())
	}
	if elapsed > 10*time.Second {
		t.Errorf("long poll took %s to notice a finished assembly", elapsed)
	}
}

// And it gives up when the stated time is up, so the answer stays well inside
// what a reverse proxy tolerates.
func TestUploadStatus_WaitGivesUpAfterTheStatedTime(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	uploadID := initUpload(t, env, claims, root.ID, "/patience.txt", 32)
	if _, err := env.uploads.MarkQueued(t.Context(), uploadID, nil); err != nil {
		t.Fatalf("MarkQueued: %v", err)
	}

	start := time.Now()
	w := uploadStatus(env, claims, root, uploadID, "?wait=1s")
	elapsed := time.Since(start)

	if got := decodeStatus(t, w)["status"]; got != model.UploadSessionQueued {
		t.Errorf("status = %v, want %q", got, model.UploadSessionQueued)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("returned after %s, want it to hold for about a second", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Errorf("held for %s, far past the requested second", elapsed)
	}
}

// The status of a session carries a conflict path and a server version — both
// belong to its owner only.
func TestUploadStatus_HidesAnotherUsersSession(t *testing.T) {
	env := newTestEnv(t)
	owner := createTestUser(t, env)
	root := createTestSyncRoot(t, env, owner.UserID, "root_"+uniqueSuffix())
	uploadID := oneChunkSession(t, env, owner, root, "/private.txt", []byte("mine alone"))
	if w := completeUploadRaw(env, owner, root, uploadID); w.Code != http.StatusAccepted {
		t.Fatalf("CompleteUpload status = %d, want 202", w.Code)
	}

	stranger := createTestUser(t, env)
	w := uploadStatus(env, stranger, root, uploadID, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("UploadStatus for a stranger = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}
