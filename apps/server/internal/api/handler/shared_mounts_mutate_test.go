package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// writableMountWithFile shares a directory (writable) from alice to bob and
// seeds one file under it, returning bob's grant id (the mount anchor) plus
// the giver root. The mount addresses paths relative to the shared dir.
func writableMountWithFile(t *testing.T, env *testEnv, mountRelFile string) (shareID uuid.UUID, ownerRoot uuid.UUID, giverPath string, bob *userAndClaims) {
	t.Helper()
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob = makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	giverPath = "/2024" + mountRelFile
	uploadFileAs(t, env, alice.Claims, root.ID, giverPath, []byte("mount payload"))
	shareID = createUserShare(t, env, alice, bob, root.ID, "/2024", true)
	return shareID, root.ID, giverPath, bob
}

func TestMoveMountFile_Succeeds(t *testing.T) {
	env := newTestEnv(t)
	shareID, rootID, _, bob := writableMountWithFile(t, env, "/a.txt")

	body := []byte(`{"op":"move","from":"/a.txt","to":"/b.txt"}`)
	r := reqWithClaims(http.MethodPatch, "/shared-mounts/"+shareID.String()+"/files", bytes.NewReader(body), bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MoveMountFile(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)
	// The move lands under the giver's absolute path /2024/b.txt.
	if _, err := env.files.FindByPath(context.Background(), rootID, "/2024/b.txt"); err != nil {
		t.Errorf("moved file not found at giver path: %v", err)
	}
}

func TestMoveMountFile_RejectsMalformedBody(t *testing.T) {
	env := newTestEnv(t)
	shareID, _, _, bob := writableMountWithFile(t, env, "/a.txt")

	r := reqWithClaims(http.MethodPatch, "/shared-mounts/"+shareID.String()+"/files", bytes.NewReader([]byte("{bad")), bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MoveMountFile(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidBody)
}

func TestMoveMountFile_RejectsUnsupportedOp(t *testing.T) {
	env := newTestEnv(t)
	shareID, _, _, bob := writableMountWithFile(t, env, "/a.txt")

	body := []byte(`{"op":"copy","from":"/a.txt","to":"/b.txt"}`)
	r := reqWithClaims(http.MethodPatch, "/shared-mounts/"+shareID.String()+"/files", bytes.NewReader(body), bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MoveMountFile(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrUnsupportedOp)
}

func TestMoveMountFile_RejectsSamePath(t *testing.T) {
	env := newTestEnv(t)
	shareID, _, _, bob := writableMountWithFile(t, env, "/a.txt")

	body := []byte(`{"op":"move","from":"/a.txt","to":"/a.txt"}`)
	r := reqWithClaims(http.MethodPatch, "/shared-mounts/"+shareID.String()+"/files", bytes.NewReader(body), bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MoveMountFile(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrSamePath)
}

func TestDeleteMountFile_Succeeds(t *testing.T) {
	env := newTestEnv(t)
	shareID, rootID, _, bob := writableMountWithFile(t, env, "/a.txt")

	r := reqWithClaims(http.MethodDelete, "/shared-mounts/"+shareID.String()+"/files?path=/a.txt", nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.DeleteMountFile(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)
	f, err := env.files.FindByPath(context.Background(), rootID, "/2024/a.txt")
	if err == nil && !f.IsDeleted {
		t.Errorf("mount file not soft-deleted")
	}
}

func TestDeleteMountFile_RejectsUnknownPath(t *testing.T) {
	env := newTestEnv(t)
	shareID, _, _, bob := writableMountWithFile(t, env, "/a.txt")

	r := reqWithClaims(http.MethodDelete, "/shared-mounts/"+shareID.String()+"/files?path=/missing.txt", nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.DeleteMountFile(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrFileNotFound)
}
