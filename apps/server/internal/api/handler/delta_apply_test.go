package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/auth"
)

// postDeltaApply posts a raw DeltaApply body against {rootID}.
func postDeltaApply(env *testEnv, claims *auth.Claims, rootID, body string) *httptest.ResponseRecorder {
	r := reqWithClaims(http.MethodPost, "/sync-roots/"+rootID+"/files/delta/apply", bytes.NewBufferString(body), claims)
	r = withURLParams(r, "rootID", rootID)
	w := httptest.NewRecorder()
	env.deltaH.DeltaApply(w, r)
	return w
}

func TestDeltaApply_RejectsMalformedRootID(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)

	w := postDeltaApply(env, claims, "bad", `{"path":"/x","expected_hash":"ab","ops":[]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRootID)
}

func TestDeltaApply_RejectsUnknownRoot(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)

	w := postDeltaApply(env, claims, uuid.New().String(), `{"path":"/x","expected_hash":"ab","ops":[]}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSyncRootNotFound)
}

func TestDeltaApply_RejectsMalformedBody(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	w := postDeltaApply(env, claims, root.ID.String(), "{not json")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidBody)
}

func TestDeltaApply_RejectsMissingExpectedHash(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	w := postDeltaApply(env, claims, root.ID.String(), `{"path":"/x","expected_hash":"","ops":[]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrUploadFields)
}

func TestDeltaApply_RejectsInvalidExpectedHash(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	w := postDeltaApply(env, claims, root.ID.String(), `{"path":"/x","expected_hash":"zz","ops":[]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrUploadFields)
}

func TestDeltaApply_RejectsInvalidOp(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	// A data op whose payload is not valid base64.
	w := postDeltaApply(env, claims, root.ID.String(),
		`{"path":"/x","expected_hash":"ab","ops":[{"data":"!!!not-base64"}]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrDeltaInvalidOp)
}

func TestDeltaApply_RejectsMissingBase(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	root := createTestSyncRoot(t, env, claims.UserID, "root_"+uniqueSuffix())

	// Valid early fields + a valid data op, but no server file at the path.
	w := postDeltaApply(env, claims, root.ID.String(),
		`{"path":"/nonexistent.bin","expected_hash":"ab","ops":[{"data":"aGVsbG8="}]}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrDeltaNoBase)
}
