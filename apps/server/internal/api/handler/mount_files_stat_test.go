package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"privasync/server/internal/auth"
)

func postMountFilesStat(env *testEnv, claims *auth.Claims, shareID, body string) *httptest.ResponseRecorder {
	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID+"/files/stat", bytes.NewBufferString(body), claims)
	r = withURLParams(r, "shareID", shareID)
	w := httptest.NewRecorder()
	env.shareH.MountFilesStat(w, r)
	return w
}

// Twin of TestFilesStat_DistinguishesFoundDeletedAndUnknown, but scoped to a
// mount and relativized -- same ADR-0062 contract, giver-side absolute paths
// must never leak into the response.
func TestMountFilesStat_DistinguishesFoundDeletedAndUnknown(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/keep.txt", []byte("hello"))
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/gone.txt", []byte("bye"))
	deleteFileAs(t, env, alice.Claims, root.ID, "/2024/gone.txt")
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	w := postMountFilesStat(env, bob.Claims, shareID.String(),
		`{"paths":["/keep.txt","/gone.txt","/never.txt"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Results []fileStatEntry `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}
	if len(resp.Results) != 3 {
		t.Fatalf("results = %+v, want 3 entries", resp.Results)
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

// A third party gets 404, same boundary as every other mount endpoint.
func TestMountFilesStat_RejectsThirdParty(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	carol := makeUser(t, env, "carol_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/x.txt", []byte("x"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	w := postMountFilesStat(env, carol.Claims, shareID.String(), `{"paths":["/x.txt"]}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("third-party should get 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// A path that escapes the share root must be rejected -- same guard as every
// other mount-scoped path (see TestListMountFiles_RejectsSubPathEscape).
func TestMountFilesStat_RejectsSubPathEscape(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/x.txt", []byte("x"))
	uploadFileAs(t, env, alice.Claims, root.ID, "/2025/secret.txt", []byte("nope"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	w := postMountFilesStat(env, bob.Claims, shareID.String(), `{"paths":["../2025/secret.txt"]}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
	}
}
