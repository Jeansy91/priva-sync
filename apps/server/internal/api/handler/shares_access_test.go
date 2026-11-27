package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type accessUserJSON struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}
type accessJSON struct {
	Writers []accessUserJSON `json:"writers"`
	Readers []accessUserJSON `json:"readers"`
}

func getAccess(t *testing.T, env *testEnv, owner *userAndClaims, rootID, path string) accessJSON {
	t.Helper()
	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+rootID+"/access?path="+path, nil, owner.Claims)
	r = withURLParams(r, "rootID", rootID)
	w := httptest.NewRecorder()
	env.shareH.ShareAccess(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("access: status=%d body=%s", w.Code, w.Body.String())
	}
	var got accessJSON
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func hasUser(list []accessUserJSON, username string) bool {
	for _, u := range list {
		if u.Username == username {
			return true
		}
	}
	return false
}

// A file inside a shared directory inherits the directory's grants: the access
// list resolves from the nearest ancestor share and splits writers/readers.
// See ADR-0015.
func TestShareAccess_InheritsFromAncestorShare(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	carol := makeUser(t, env, "carol_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/sub/file.txt", []byte("x"))

	createUserShare(t, env, alice, bob, root.ID, "/dir", true)   // writer
	createUserShare(t, env, alice, carol, root.ID, "/dir", false) // reader

	got := getAccess(t, env, alice, root.ID.String(), "/dir/sub/file.txt")
	if !hasUser(got.Writers, bob.Username) {
		t.Errorf("bob not a writer: %+v", got.Writers)
	}
	if !hasUser(got.Readers, carol.Username) {
		t.Errorf("carol not a reader: %+v", got.Readers)
	}
	if hasUser(got.Writers, carol.Username) {
		t.Errorf("carol wrongly a writer")
	}
}

// Only the root owner may query the access map of their root.
func TestShareAccess_RejectsNonOwner(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())

	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/access?path=/", nil, bob.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.shareH.ShareAccess(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("non-owner should get 404, got %d", w.Code)
	}
}
