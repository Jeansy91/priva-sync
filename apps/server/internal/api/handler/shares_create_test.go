package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/auth"
)

// postCreateShare posts a raw CreateShare body against {rootID}.
func postCreateShare(env *testEnv, ownerClaims *auth.Claims, rootID, body string) *httptest.ResponseRecorder {
	r := reqWithClaims(http.MethodPost, "/sync-roots/"+rootID+"/shares", bytes.NewBufferString(body), ownerClaims)
	r = withURLParams(r, "rootID", rootID)
	w := httptest.NewRecorder()
	env.shareH.CreateShare(w, r)
	return w
}

func TestCreateShare_RejectsMalformedRootID(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())

	w := postCreateShare(env, alice.Claims, "bad", `{"path":"/dir","share_type":"public"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidRootID)
}

func TestCreateShare_RejectsUnknownRoot(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())

	w := postCreateShare(env, alice.Claims, uuid.New().String(), `{"path":"/dir","share_type":"public"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSyncRootNotFound)
}

func TestCreateShare_RejectsMissingFields(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())

	w := postCreateShare(env, alice.Claims, root.ID.String(), `{"path":"","share_type":""}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrShareFieldsMiss)
}

func TestCreateShare_RejectsInvalidShareType(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())

	w := postCreateShare(env, alice.Claims, root.ID.String(), `{"path":"/dir","share_type":"carrier-pigeon"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrShareTypeInvalid)
}

func TestCreateShare_RejectsInvalidExpiresAt(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())

	w := postCreateShare(env, alice.Claims, root.ID.String(), `{"path":"/dir","share_type":"public","expires_at":"not-a-date"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidExpiresAt)
}

func TestCreateShare_PublicDirectoryGetsTwoTokens(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/x.txt", []byte("x"))

	w := postCreateShare(env, alice.Claims, root.ID.String(), `{"path":"/dir","share_type":"public"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
}

func TestCreateShare_PublicFileGetsOneToken(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/x.txt", []byte("x"))

	w := postCreateShare(env, alice.Claims, root.ID.String(), `{"path":"/dir/x.txt","share_type":"public"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
}

func TestCreateShare_UserShareRejectsMalformedTargetID(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/x.txt", []byte("x"))

	w := postCreateShare(env, alice.Claims, root.ID.String(),
		`{"path":"/dir","share_type":"user","target_user_id":"not-a-uuid"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidTargetUser)
}

func TestCreateShare_UserShareRejectsUnknownTargetID(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/x.txt", []byte("x"))

	w := postCreateShare(env, alice.Claims, root.ID.String(),
		`{"path":"/dir","share_type":"user","target_user_id":"`+uuid.New().String()+`"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrTargetUserNotFound)
}

func TestCreateShare_UserShareRejectsUnknownUsername(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/x.txt", []byte("x"))

	w := postCreateShare(env, alice.Claims, root.ID.String(),
		`{"path":"/dir","share_type":"user","target_username":"ghost_`+uniqueSuffix()+`"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrTargetUserNotFound)
}

func TestCreateShare_UserShareRejectsMissingTarget(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/x.txt", []byte("x"))

	w := postCreateShare(env, alice.Claims, root.ID.String(), `{"path":"/dir","share_type":"user"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrShareUserMissing)
}

func TestCreateShare_UserShareRejectsSelfShare(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/x.txt", []byte("x"))

	w := postCreateShare(env, alice.Claims, root.ID.String(),
		`{"path":"/dir","share_type":"user","target_user_id":"`+alice.UserID.String()+`"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrShareSelf)
}

func TestCreateShare_UserShareSucceedsByUsername(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/x.txt", []byte("x"))

	w := postCreateShare(env, alice.Claims, root.ID.String(),
		`{"path":"/dir","share_type":"user","target_username":"`+bob.Username+`","can_write":true}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
}
