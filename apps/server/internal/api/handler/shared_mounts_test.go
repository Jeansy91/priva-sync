package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/auth"
)

type userAndClaims struct {
	UserID   uuid.UUID
	Username string
	Claims   *auth.Claims
}

// makeUser seeds a user for a handler test. The name is only a readable
// prefix: it gets a unique suffix, because the test database is shared and
// survives a run that was interrupted before its cleanup. A fixed name turns
// such a leftover into "already exists" on every later run, which looks like
// a broken test and is not one. No caller asserts on the literal name; the
// ones that check a username read it back off the returned user.
func makeUser(t *testing.T, env *testEnv, username string) *userAndClaims {
	t.Helper()
	username = uniqueUsername(username)
	u := seedUser(t, env, username, username+"@t.local")
	return &userAndClaims{
		UserID:   u.ID,
		Username: u.Username,
		Claims:   &auth.Claims{UserID: u.ID, Role: u.Role, TokenType: auth.TokenTypeAccess},
	}
}

type mountListEntry struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	IsFile    bool      `json:"is_file"`
	CanWrite  bool      `json:"can_write"`
	ExpiresAt *string   `json:"expires_at"`
	Owner     struct {
		ID        uuid.UUID `json:"id"`
		Username  string    `json:"username"`
		AvatarURL *string   `json:"avatar_url"`
	} `json:"owner"`
}

// createUserShare shares the given path from alice to bob and returns bob's
// grant id — the recipient's mount anchor (ADR-0015), which is what the
// shared-mounts endpoints address. Mirrors the real CreateShare wire path.
func createUserShare(t *testing.T, env *testEnv, alice, bob *userAndClaims, rootID uuid.UUID, path string, canWrite bool) uuid.UUID {
	t.Helper()
	body := map[string]any{
		"path":           path,
		"share_type":     "user",
		"target_user_id": bob.UserID.String(),
		"can_write":      canWrite,
	}
	buf, _ := json.Marshal(body)
	r := reqWithClaims(http.MethodPost,
		"/api/v1/sync-roots/"+rootID.String()+"/shares",
		bytes.NewReader(buf), alice.Claims)
	r = withURLParams(r, "rootID", rootID.String())
	w := httptest.NewRecorder()
	env.shareH.CreateShare(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create user share: status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		GrantID uuid.UUID `json:"grant_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode share: %v", err)
	}
	return resp.GrantID
}

// shareAggregateID returns the user-share aggregate id for a path — the
// owner-facing share id used by grant endpoints (PATCH/DELETE .../grants/...).
func shareAggregateID(t *testing.T, env *testEnv, ownerID, rootID uuid.UUID, path string) uuid.UUID {
	t.Helper()
	agg, err := env.shares.EnsureUserShare(context.Background(), ownerID, rootID, path)
	if err != nil {
		t.Fatalf("aggregate id: %v", err)
	}
	return agg.ID
}

// uploadFileAs is a tiny helper to seed a file as `claims` into the given
// sync root at `path`. Mirrors what other handler tests already do, kept
// local so this file stays self-contained.
func uploadFileAs(t *testing.T, env *testEnv, claims *auth.Claims, rootID uuid.UUID, path string, body []byte) {
	t.Helper()
	r := reqWithClaims(http.MethodPost,
		"/api/v1/sync-roots/"+rootID.String()+"/files?path="+path,
		bytes.NewReader(body), claims)
	r = withURLParams(r, "rootID", rootID.String())
	w := httptest.NewRecorder()
	env.handler.UploadFile(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("upload %s: status=%d body=%s", path, w.Code, w.Body.String())
	}
	env.drainOutbox(t)
}

// deleteFileAs soft-deletes a file as `claims`. Leaves a trashed row behind
// at the same path (see migration 020 partial-unique), which is exactly the
// state the is_file derivation must not trip over.
func deleteFileAs(t *testing.T, env *testEnv, claims *auth.Claims, rootID uuid.UUID, path string) {
	t.Helper()
	r := reqWithClaims(http.MethodDelete,
		"/api/v1/sync-roots/"+rootID.String()+"/files?path="+path,
		nil, claims)
	r = withURLParams(r, "rootID", rootID.String())
	w := httptest.NewRecorder()
	env.handler.DeleteFile(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete %s: status=%d body=%s", path, w.Code, w.Body.String())
	}
	env.drainOutbox(t)
}

// Regression: deleting a file and re-uploading it leaves a trashed row beside
// the new live row at the same path (partial-unique covers live rows only —
// migration 020). The is_file derivation joins on path alone, so it could pick
// the trashed row and report is_file=false → the file-mount renders as a folder
// with no preview, or the mount is listed twice. It must read the LIVE row.
// See ADR-0010 / 0012.
func TestListSharedMounts_FileShareIsFileIgnoresTrashedDuplicate(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/cat.png", []byte("v1"))
	deleteFileAs(t, env, alice.Claims, root.ID, "/2024/cat.png")
	// Re-upload: new live row; the trashed row from the delete still sits at
	// the same path.
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/cat.png", []byte("v2"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024/cat.png", false)

	r := reqWithClaims(http.MethodGet, "/api/v1/shared-mounts", nil, bob.Claims)
	w := httptest.NewRecorder()
	env.shareH.ListSharedMounts(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var got []mountListEntry
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 mount, got %d (trashed duplicate leaked): %+v", len(got), got)
	}
	if got[0].ID != shareID {
		t.Fatalf("mount id mismatch: got %s want %s", got[0].ID, shareID)
	}
	if !got[0].IsFile {
		t.Fatalf("file-share with a trashed duplicate at the path should still report is_file=true")
	}
}

// Bob must see every user-share where he is the target as a Shared Mount,
// with the giver's profile embedded so the sidebar can render an avatar
// without a second round-trip. See ADR-0010.
func TestListSharedMounts_ListsIncomingUserShares(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "photos_"+uniqueSuffix())
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	r := reqWithClaims(http.MethodGet, "/api/v1/shared-mounts", nil, bob.Claims)
	w := httptest.NewRecorder()
	env.shareH.ListSharedMounts(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var got []mountListEntry
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(got))
	}
	m := got[0]
	if m.ID != shareID {
		t.Fatalf("mount id = share id mismatch: got %s want %s", m.ID, shareID)
	}
	if m.Name != "2024" {
		t.Fatalf("mount name should be basename(share.path): got %q want %q", m.Name, "2024")
	}
	if m.Path != "/2024" {
		t.Fatalf("path mismatch: got %q want %q", m.Path, "/2024")
	}
	if m.IsFile {
		t.Fatalf("dir-share should have is_file=false")
	}
	if !m.CanWrite {
		t.Fatalf("can_write should be true")
	}
	if m.ExpiresAt != nil {
		t.Fatalf("expires_at should be nil")
	}
	if m.Owner.ID != alice.UserID {
		t.Fatalf("owner id mismatch: got %s want %s", m.Owner.ID, alice.UserID)
	}
	if m.Owner.Username != alice.Username {
		t.Fatalf("owner username mismatch: got %q want %q", m.Owner.Username, alice.Username)
	}
}

// A share that points at the sync-root itself (path "/") is a root-mount.
// path.Base("/") is "/", which would surface a meaningless slash as the
// sidebar label. The recipient must instead see the giver's sync-folder
// name, so the mount reads like a real shared folder. See ADR-0010.
func TestListSharedMounts_RootShareUsesSyncFolderName(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	rootName := "photos_" + uniqueSuffix()
	root := createTestSyncRoot(t, env, alice.UserID, rootName)
	createUserShare(t, env, alice, bob, root.ID, "/", true)

	r := reqWithClaims(http.MethodGet, "/api/v1/shared-mounts", nil, bob.Claims)
	w := httptest.NewRecorder()
	env.shareH.ListSharedMounts(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var got []mountListEntry
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(got))
	}
	m := got[0]
	if m.Name != rootName {
		t.Fatalf("root-mount name should be sync-folder name: got %q want %q", m.Name, rootName)
	}
}

// A share that points straight at a file must surface is_file=true so the
// recipient UI renders it as a previewable file-mount (file row + thumbnail),
// not as a directory. is_file is derived from the matching files row, so the
// shared file must exist and not be deleted. See ADR-0010 / 0012.
func TestListSharedMounts_FileShareReportsIsFile(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/cat.png", []byte("img-bytes"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024/cat.png", false)

	r := reqWithClaims(http.MethodGet, "/api/v1/shared-mounts", nil, bob.Claims)
	w := httptest.NewRecorder()
	env.shareH.ListSharedMounts(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var got []mountListEntry
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(got))
	}
	m := got[0]
	if m.ID != shareID {
		t.Fatalf("mount id mismatch: got %s want %s", m.ID, shareID)
	}
	if !m.IsFile {
		t.Fatalf("file-share should report is_file=true, got false (renders as folder → no preview)")
	}
	if m.Name != "cat.png" {
		t.Fatalf("mount name should be basename(share.path): got %q want %q", m.Name, "cat.png")
	}
}

// Public-token shares have no recipient — they must never appear in
// /shared-mounts.
func TestListSharedMounts_ExcludesPublicShares(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "pub_"+uniqueSuffix())

	body := bytes.NewBufferString(`{"path":"/dir","share_type":"public"}`)
	r := reqWithClaims(http.MethodPost,
		"/api/v1/sync-roots/"+root.ID.String()+"/shares", body, alice.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.shareH.CreateShare(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create public share: status=%d body=%s", w.Code, w.Body.String())
	}

	r = reqWithClaims(http.MethodGet, "/api/v1/shared-mounts", nil, bob.Claims)
	w = httptest.NewRecorder()
	env.shareH.ListSharedMounts(w, r)
	var got []mountListEntry
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got) != 0 {
		t.Fatalf("public share leaked into mounts: %+v", got)
	}
}

// Tracer bullet for the Mount browsing path: Bob lists files inside a
// directory share he received from Alice. The listing must contain the
// files Alice uploaded under share.path, without leaking siblings from
// other parts of Alice's sync root.
func TestListMountFiles_ListsEntriesWithinShare(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/inside.txt", []byte("yo"))
	uploadFileAs(t, env, alice.Claims, root.ID, "/2025/outside.txt", []byte("nope"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/files?path=/",
		nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.ListMountFiles(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Entries []struct {
			Name  string `json:"name"`
			IsDir bool   `json:"is_dir"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	names := map[string]bool{}
	for _, e := range resp.Entries {
		names[e.Name] = true
	}
	if !names["inside.txt"] {
		t.Fatalf("missing inside.txt inside mount: %+v", resp.Entries)
	}
	if names["outside.txt"] {
		t.Fatalf("leaked outside.txt from sibling dir: %+v", resp.Entries)
	}
}

// A third party who is neither giver nor target must not be able to
// list mount files. The boundary returns 404 (not 403) to avoid
// disclosing share existence.
func TestListMountFiles_RejectsThirdParty(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	carol := makeUser(t, env, "carol_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/x.txt", []byte("x"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/files?path=/",
		nil, carol.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.ListMountFiles(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("third-party should get 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// Expired mount returns 410 Gone so the client can distinguish 'gone
// for good' from 'never existed'. Only the listing handler is checked
// here; resolveMount centralises the check for all mount routes.
func TestListMountFiles_GoneWhenExpired(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/x.txt", []byte("x"))

	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	body := map[string]any{
		"path":           "/2024",
		"share_type":     "user",
		"target_user_id": bob.UserID.String(),
		"expires_at":     past,
	}
	buf, _ := json.Marshal(body)
	r := reqWithClaims(http.MethodPost,
		"/api/v1/sync-roots/"+root.ID.String()+"/shares",
		bytes.NewReader(buf), alice.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.shareH.CreateShare(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create expired share: status=%d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		GrantID uuid.UUID `json:"grant_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	r = reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+created.GrantID.String()+"/files?path=/",
		nil, bob.Claims)
	r = withURLParams(r, "shareID", created.GrantID.String())
	w = httptest.NewRecorder()
	env.shareH.ListMountFiles(w, r)
	if w.Code != http.StatusGone {
		t.Fatalf("expected 410 Gone, got %d body=%s", w.Code, w.Body.String())
	}
}

// Bob downloads a single file from his Shared Mount. The handler must
// stream the actual bytes Alice uploaded, scoped to share.path.
func TestDownloadMountFile_ReturnsFileBytes(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	payload := []byte("hello-from-alice")
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/note.txt", payload)
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", false)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/download?path=/note.txt",
		nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.DownloadMountFile(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("download status %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), payload) {
		t.Fatalf("body mismatch: got %q want %q", w.Body.Bytes(), payload)
	}
}

// Bob downloads a file-mount: the share itself points at a file, so the
// fullscreen preview asks for path="/". The handler must resolve "/" to the
// shared file and stream its bytes — this is the path every preview viewer
// (image, pdf, audio, …) takes for a single-file mount. See ADR-0010 / 0012.
func TestDownloadMountFile_FileMount_ResolvesRootPath(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	payload := []byte("hello-from-alice")
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/cat.png", payload)
	// Share the FILE itself, not its parent directory → file-mount.
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024/cat.png", false)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/download?path=/",
		nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.DownloadMountFile(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("download status %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), payload) {
		t.Fatalf("body mismatch: got %q want %q", w.Body.Bytes(), payload)
	}
}

// Bob uploads into his Shared Mount (can_write=true). The file lands in
// Alice's sync root under share.path; Alice's listing must show it.
func TestUploadMountFile_StoresFileInOwnerRoot(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/seed.txt", []byte("seed"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	payload := []byte("bob-was-here")
	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/files?path=/new.txt",
		bytes.NewReader(payload), bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.UploadMountFile(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("upload via mount: status=%d body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	// Verify via Alice's own-root listing that the file is there.
	r = reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/files?path=/2024", nil, alice.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w = httptest.NewRecorder()
	env.handler.ListFiles(w, r)
	var resp struct {
		Entries []struct{ Name string `json:"name"` } `json:"entries"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	names := map[string]bool{}
	for _, e := range resp.Entries {
		names[e.Name] = true
	}
	if !names["new.txt"] {
		t.Fatalf("uploaded file missing in owner listing: %+v", resp.Entries)
	}
}

// can_write=false must reject upload with 403; otherwise the read-only
// promise is broken.
func TestUploadMountFile_ForbiddenWhenReadOnly(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/seed.txt", []byte("seed"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", false)

	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/files?path=/nope.txt",
		bytes.NewReader([]byte("x")), bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.UploadMountFile(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("read-only upload should be 403, got %d body=%s", w.Code, w.Body.String())
	}
}

// Bob deletes a file in his Shared Mount. The file must disappear from
// Alice's listing (soft-delete in her trash). Quota stays with Alice.
func TestDeleteMountFile_RemovesFromOwnerListing(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/doomed.txt", []byte("rip"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	r := reqWithClaims(http.MethodDelete,
		"/api/v1/shared-mounts/"+shareID.String()+"/files?path=/doomed.txt",
		nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.DeleteMountFile(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete via mount: status=%d body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	r = reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/files?path=/2024", nil, alice.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w = httptest.NewRecorder()
	env.handler.ListFiles(w, r)
	var resp struct {
		Entries []struct{ Name string `json:"name"` } `json:"entries"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	for _, e := range resp.Entries {
		if e.Name == "doomed.txt" {
			t.Fatalf("file should have been trashed, still in listing: %+v", resp.Entries)
		}
	}
}

// can_write=false must reject delete with 403.
func TestDeleteMountFile_ForbiddenWhenReadOnly(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/safe.txt", []byte("safe"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", false)

	r := reqWithClaims(http.MethodDelete,
		"/api/v1/shared-mounts/"+shareID.String()+"/files?path=/safe.txt",
		nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.DeleteMountFile(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("read-only delete should be 403, got %d body=%s", w.Code, w.Body.String())
	}
}

// Bob renames/moves a file inside his Shared Mount. The file appears
// at the new path in Alice's own-root listing.
func TestMoveMountFile_RenamesWithinShare(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/old.txt", []byte("payload"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	body := bytes.NewBufferString(`{"op":"move","from":"/old.txt","to":"/new.txt"}`)
	r := reqWithClaims(http.MethodPatch,
		"/api/v1/shared-mounts/"+shareID.String()+"/files",
		body, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MoveMountFile(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("move via mount: status=%d body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	r = reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/files?path=/2024", nil, alice.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w = httptest.NewRecorder()
	env.handler.ListFiles(w, r)
	var resp struct {
		Entries []struct{ Name string `json:"name"` } `json:"entries"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	names := map[string]bool{}
	for _, e := range resp.Entries {
		names[e.Name] = true
	}
	if !names["new.txt"] {
		t.Fatalf("renamed file missing: %+v", resp.Entries)
	}
	if names["old.txt"] {
		t.Fatalf("old path still present: %+v", resp.Entries)
	}
}

// Move target must stay within share.path; otherwise an attacker could
// rename a mount file into Alice's other directories.
func TestMoveMountFile_RejectsTargetOutsideShare(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/x.txt", []byte("x"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	body := bytes.NewBufferString(`{"op":"move","from":"/x.txt","to":"/../2025/x.txt"}`)
	r := reqWithClaims(http.MethodPatch,
		"/api/v1/shared-mounts/"+shareID.String()+"/files",
		body, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MoveMountFile(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("escape-target move should be 403, got %d body=%s", w.Code, w.Body.String())
	}
}

// Alice asks for the share-map of her own sync root and gets every
// share keyed by path — both user-shares (with target username) and
// public-token shares. Powers the Geber-side badges in the Files-
// Browser. See ADR-0010.
func TestListSharesByRoot_GroupsByPath(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/Photos/2024/x.txt", []byte("x"))

	userShareID := createUserShare(t, env, alice, bob, root.ID, "/Photos/2024", true)

	body := bytes.NewBufferString(`{"path":"/Photos","share_type":"public"}`)
	r := reqWithClaims(http.MethodPost,
		"/api/v1/sync-roots/"+root.ID.String()+"/shares", body, alice.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.shareH.CreateShare(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create public share: %d body=%s", w.Code, w.Body.String())
	}

	r = reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/shares", nil, alice.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w = httptest.NewRecorder()
	env.shareH.ListSharesByRoot(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}

	var got map[string][]struct {
		ID             uuid.UUID `json:"id"`
		ShareType      string    `json:"share_type"`
		TargetUsername *string   `json:"target_username"`
		CanWrite       bool      `json:"can_write"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got["/Photos/2024"]) != 1 || got["/Photos/2024"][0].ID != userShareID {
		t.Fatalf("user share not grouped under /Photos/2024: %+v", got)
	}
	if got["/Photos/2024"][0].TargetUsername == nil || *got["/Photos/2024"][0].TargetUsername != bob.Username {
		t.Fatalf("user share missing target_username: %+v", got["/Photos/2024"][0])
	}
	if len(got["/Photos"]) != 1 || got["/Photos"][0].ShareType != "public" {
		t.Fatalf("public share not grouped under /Photos: %+v", got)
	}
}

// Third party who is not the root's owner must not see the share map.
func TestListSharesByRoot_RejectsNonOwner(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())

	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/shares", nil, bob.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.shareH.ListSharesByRoot(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("non-owner should get 404, got %d", w.Code)
	}
}

// When Alice deletes the whole sync root, every recipient of a
// user-share inside that root must receive a ShareRevoked SSE event —
// otherwise their mount stays in the sidebar pointing at nothing. See
// ADR-0010.
func TestDeleteSyncRoot_PublishesShareRevokedToTargets(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/x.txt", []byte("x"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	bobEvents, bobCleanup := captureEvents(t, env.hub, bob.UserID)
	defer bobCleanup()

	r := reqWithClaims(http.MethodDelete,
		"/api/v1/sync-roots/"+root.ID.String(), nil, alice.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.DeleteSyncRoot(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete sync root: status=%d body=%s", w.Code, w.Body.String())
	}

	ev := waitForEvent(t, bobEvents, "share_revoked", 2*time.Second)
	body := eventDataMap(t, ev)
	if got, _ := body["share_id"].(string); got != shareID.String() {
		t.Fatalf("share_revoked share_id mismatch: got %v want %s", body["share_id"], shareID)
	}
}

// Bob's global search must surface hits inside his Shared Mounts so
// mounts feel like first-class roots. Hits are returned in a mount-group
// keyed by the share-id, not the giver's sync-root-id, so Alice's other
// directories never leak into Bob's response. See ADR-0010.
func TestSearchFilesGlobal_IncludesMountHits(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/Photos/2024/secret-note.txt", []byte("yo"))
	uploadFileAs(t, env, alice.Claims, root.ID, "/Photos/2025/other.txt", []byte("nope"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/Photos/2024", true)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/files/search?q=secret", nil, bob.Claims)
	w := httptest.NewRecorder()
	env.handler.SearchFilesGlobal(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("search status: %d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Groups []struct {
			SyncRootID   uuid.UUID `json:"sync_root_id"`
			MountID      *string   `json:"mount_id"`
			SyncRootName string    `json:"sync_root_name"`
			Files        []struct {
				Path string `json:"path"`
			} `json:"files"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var mountGroup *string
	var hit string
	for _, g := range resp.Groups {
		if g.MountID != nil && *g.MountID == shareID.String() {
			mountGroup = g.MountID
			if len(g.Files) > 0 {
				hit = g.Files[0].Path
			}
			break
		}
	}
	if mountGroup == nil {
		t.Fatalf("expected mount group for shareID %s, groups=%+v", shareID, resp.Groups)
	}
	if hit == "" || !strings.Contains(hit, "secret-note.txt") {
		t.Fatalf("missing or wrong hit: %q", hit)
	}
	// Paths must be relativized to mount root, NOT include /Photos/2024.
	if strings.HasPrefix(hit, "/Photos") {
		t.Fatalf("path leaked giver-root prefix: %q", hit)
	}
}

// A sub-path that climbs out of share.path via '..' must be rejected
// with 403, otherwise the mount would leak adjacent directories.
func TestListMountFiles_RejectsSubPathEscape(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/x.txt", []byte("x"))
	uploadFileAs(t, env, alice.Claims, root.ID, "/2025/secret.txt", []byte("nope"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/files?path=../2025",
		nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.ListMountFiles(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
	}
}

// Recipient calls DELETE /shared-mounts/{id} to leave a mount. The share
// must vanish from his mount list and a share_revoked SSE event must
// reach both Alice (giver) and Bob (target) so any open sessions can
// drop the mount immediately. See ADR-0010.
func TestLeaveSharedMount_RemovesMountAndPublishesRevoked(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/x.txt", []byte("x"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	aliceEvents, aliceCleanup := captureEvents(t, env.hub, alice.UserID)
	defer aliceCleanup()
	bobEvents, bobCleanup := captureEvents(t, env.hub, bob.UserID)
	defer bobCleanup()

	r := reqWithClaims(http.MethodDelete,
		"/api/v1/shared-mounts/"+shareID.String(), nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.LeaveSharedMount(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("leave: status=%d body=%s", w.Code, w.Body.String())
	}

	r2 := reqWithClaims(http.MethodGet, "/api/v1/shared-mounts", nil, bob.Claims)
	w2 := httptest.NewRecorder()
	env.shareH.ListSharedMounts(w2, r2)
	var got []mountListEntry
	_ = json.Unmarshal(w2.Body.Bytes(), &got)
	if len(got) != 0 {
		t.Fatalf("mount still listed after leave: %+v", got)
	}

	ev := waitForEvent(t, bobEvents, "share_revoked", 2*time.Second)
	body := eventDataMap(t, ev)
	if got, _ := body["share_id"].(string); got != shareID.String() {
		t.Fatalf("bob share_revoked id mismatch: got %v want %s", body["share_id"], shareID)
	}
	ev = waitForEvent(t, aliceEvents, "share_revoked", 2*time.Second)
	body = eventDataMap(t, ev)
	if got, _ := body["share_id"].(string); got != shareID.String() {
		t.Fatalf("alice share_revoked id mismatch: got %v want %s", body["share_id"], shareID)
	}
}

// Only the target may leave a mount. A third party gets 404 so we do
// not disclose share existence.
func TestLeaveSharedMount_RejectsNonTarget(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	carol := makeUser(t, env, "carol_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/x.txt", []byte("x"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	r := reqWithClaims(http.MethodDelete,
		"/api/v1/shared-mounts/"+shareID.String(), nil, carol.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.LeaveSharedMount(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("non-target should get 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// Mounts whose expiry is in the past should not appear — the share is
// effectively gone from the recipient's perspective.
func TestListSharedMounts_HidesExpiredShares(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "exp_"+uniqueSuffix())

	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	body := map[string]any{
		"path":           "/dir",
		"share_type":     "user",
		"target_user_id": bob.UserID.String(),
		"expires_at":     past,
	}
	buf, _ := json.Marshal(body)
	r := reqWithClaims(http.MethodPost,
		"/api/v1/sync-roots/"+root.ID.String()+"/shares", bytes.NewReader(buf), alice.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.shareH.CreateShare(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create expired share: status=%d body=%s", w.Code, w.Body.String())
	}

	r = reqWithClaims(http.MethodGet, "/api/v1/shared-mounts", nil, bob.Claims)
	w = httptest.NewRecorder()
	env.shareH.ListSharedMounts(w, r)
	var got []mountListEntry
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got) != 0 {
		t.Fatalf("expired share leaked into mounts: %+v", got)
	}
}

// Uploading over an occupied path *without* If-Match must surface a real
// conflict (409 + conflict_path), exactly like the own-root upload path.
// Before the If-Match fix the mount handler ignored result.Conflict and
// returned 200, silently producing a loser copy the desktop never saw. See
// ADR-0011.
func TestUploadMountFile_RealConflictWhenPathOccupied(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/seed.txt", []byte("seed"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/files?path=/seed.txt",
		bytes.NewReader([]byte("bob-blind-write")), bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.UploadMountFile(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("blind overwrite should be 409, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		ConflictPath string `json:"conflict_path"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.ConflictPath == "" {
		t.Fatalf("409 must carry conflict_path, body=%s", w.Body.String())
	}
}

// The mount changes feed must return entries scoped to share.path with
// every path relativized to the mount root — never leaking the giver's
// absolute layout or sibling directories. Powers the desktop mount sync
// (ADR-0011), mirroring the own-root /changes endpoint.
func TestMountChanges_ScopedAndRelativized(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/inside.txt", []byte("yo"))
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/sub/deep.txt", []byte("deep"))
	uploadFileAs(t, env, alice.Claims, root.ID, "/2025/outside.txt", []byte("nope"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/changes?feed_version=2", nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MountChanges(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Changes []struct {
			Path string `json:"path"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := resp.Changes
	paths := map[string]bool{}
	for _, e := range got {
		paths[e.Path] = true
	}
	if !paths["/inside.txt"] {
		t.Fatalf("missing relativized /inside.txt: %+v", got)
	}
	if !paths["/sub/deep.txt"] {
		t.Fatalf("missing relativized /sub/deep.txt: %+v", got)
	}
	for p := range paths {
		if strings.HasPrefix(p, "/2024") || strings.Contains(p, "outside") {
			t.Fatalf("path leaked giver layout or sibling: %q (all=%+v)", p, got)
		}
	}
}

// A third party gets 404 on the mount changes feed (resolveMount boundary).
func TestMountChanges_RejectsThirdParty(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	carol := makeUser(t, env, "carol_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/x.txt", []byte("x"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/changes", nil, carol.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MountChanges(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("third-party should get 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// A stale If-Match on a mount upload must become a version conflict (409 +
// server_version), mirroring RFC 7232 semantics of the own-root handler.
func TestUploadMountFile_VersionConflictWithStaleIfMatch(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/seed.txt", []byte("seed"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/files?path=/seed.txt",
		bytes.NewReader([]byte("bob-stale")), bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	r.Header.Set("If-Match", "999999") // far ahead of the real version
	w := httptest.NewRecorder()
	env.shareH.UploadMountFile(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale If-Match should be 409, got %d body=%s", w.Code, w.Body.String())
	}
}

// Moving onto an occupied destination without If-Match must surface a real
// move conflict (409 + conflict_path). Before the fix the handler returned
// 200 while the coordinator had already produced a loser copy.
func TestMoveMountFile_RealConflictWhenDestOccupied(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/a.txt", []byte("aaa"))
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/b.txt", []byte("bbb"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)

	body := bytes.NewBufferString(`{"op":"move","from":"/a.txt","to":"/b.txt"}`)
	r := reqWithClaims(http.MethodPatch,
		"/api/v1/shared-mounts/"+shareID.String()+"/files",
		body, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MoveMountFile(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("move onto occupied dest should be 409, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		ConflictPath string `json:"conflict_path"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.ConflictPath == "" {
		t.Fatalf("409 must carry conflict_path, body=%s", w.Body.String())
	}
}

// makeMountConflict produces a real conflict inside Bob's writable mount: Bob
// blind-overwrites a path Alice already occupies, so the coordinator routes
// his bytes to a loser copy and creates a conflict row in Alice's root.
// Returns the conflict id (relativized listing is asserted by the caller).
func makeMountConflict(t *testing.T, env *testEnv, alice, bob *userAndClaims, rootID, shareID uuid.UUID, relPath string) {
	t.Helper()
	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/files?path="+relPath,
		bytes.NewReader([]byte("bob-blind")), bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.UploadMountFile(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("setup: blind overwrite should 409, got %d body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)
}

// A recipient lists conflicts inside the mount: paths must be relativized to
// the mount root and scoped to share.path. ADR-0011.
func TestMountConflicts_ListsScopedAndRelativized(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/seed.txt", []byte("seed"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)
	makeMountConflict(t, env, alice, bob, root.ID, shareID, "/seed.txt")

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/conflicts", nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MountConflicts(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var got []struct {
		ID           uuid.UUID `json:"id"`
		ConflictPath string    `json:"conflict_path"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 mount conflict, got %d: %+v", len(got), got)
	}
	if strings.HasPrefix(got[0].ConflictPath, "/2024") {
		t.Fatalf("conflict_path leaked giver layout: %q", got[0].ConflictPath)
	}
	if !strings.HasPrefix(got[0].ConflictPath, "/") {
		t.Fatalf("conflict_path must be mount-relative (leading slash): %q", got[0].ConflictPath)
	}
}

// A recipient with can_write resolves a mount conflict; it then disappears
// from the mount conflict list. ADR-0011.
func TestResolveMountConflict_RemovesConflict(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/seed.txt", []byte("seed"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)
	makeMountConflict(t, env, alice, bob, root.ID, shareID, "/seed.txt")

	// Grab the conflict id.
	lr := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/conflicts", nil, bob.Claims)
	lr = withURLParams(lr, "shareID", shareID.String())
	lw := httptest.NewRecorder()
	env.shareH.MountConflicts(lw, lr)
	var listed []struct {
		ID uuid.UUID `json:"id"`
	}
	_ = json.Unmarshal(lw.Body.Bytes(), &listed)
	if len(listed) != 1 {
		t.Fatalf("setup: expected 1 conflict, got %d", len(listed))
	}
	conflictID := listed[0].ID

	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/conflicts/"+conflictID.String()+"/resolve",
		nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String(), "conflictID", conflictID.String())
	w := httptest.NewRecorder()
	env.shareH.ResolveMountConflict(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("resolve via mount: status=%d body=%s", w.Code, w.Body.String())
	}

	// Gone from the list now.
	lr2 := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/conflicts", nil, bob.Claims)
	lr2 = withURLParams(lr2, "shareID", shareID.String())
	lw2 := httptest.NewRecorder()
	env.shareH.MountConflicts(lw2, lr2)
	var after []struct {
		ID uuid.UUID `json:"id"`
	}
	_ = json.Unmarshal(lw2.Body.Bytes(), &after)
	if len(after) != 0 {
		t.Fatalf("conflict should be resolved, still listed: %+v", after)
	}
}

// A read-only recipient must not resolve a conflict (403). ADR-0011.
func TestResolveMountConflict_ForbiddenWhenReadOnly(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/seed.txt", []byte("seed"))
	// Writable share to manufacture a conflict, then a read-only share for the
	// same path proves the resolve gate is independent of how the conflict arose.
	wShare := createUserShare(t, env, alice, bob, root.ID, "/2024", true)
	makeMountConflict(t, env, alice, bob, root.ID, wShare, "/seed.txt")
	lr := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+wShare.String()+"/conflicts", nil, bob.Claims)
	lr = withURLParams(lr, "shareID", wShare.String())
	lw := httptest.NewRecorder()
	env.shareH.MountConflicts(lw, lr)
	var listed []struct {
		ID uuid.UUID `json:"id"`
	}
	_ = json.Unmarshal(lw.Body.Bytes(), &listed)
	if len(listed) != 1 {
		t.Fatalf("setup: expected 1 conflict, got %d", len(listed))
	}
	conflictID := listed[0].ID

	// Carol gets a read-only share on the same path; she must not resolve.
	carol := makeUser(t, env, "carol_"+uniqueSuffix())
	roShare := createUserShare(t, env, alice, carol, root.ID, "/2024", false)
	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+roShare.String()+"/conflicts/"+conflictID.String()+"/resolve",
		nil, carol.Claims)
	r = withURLParams(r, "shareID", roShare.String(), "conflictID", conflictID.String())
	w := httptest.NewRecorder()
	env.shareH.ResolveMountConflict(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("read-only resolve should be 403, got %d body=%s", w.Code, w.Body.String())
	}
}

// The mount feed now carries the same cursor contract as the own-root one
// (ADR-0036): `since` is a seq, the envelope hands back next_cursor, and a
// timestamp is refused rather than silently misread.
func TestMountChanges_SeqCursorPagesAndRefusesTimestamps(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "seq_"+uniqueSuffix())
	for _, p := range []string{"/m/a.txt", "/m/b.txt", "/m/c.txt"} {
		uploadFileAs(t, env, alice.Claims, root.ID, p, []byte("x"))
	}
	shareID := createUserShare(t, env, alice, bob, root.ID, "/m", true)

	call := func(query string) (paths []string, nextCursor int64, code int) {
		sep := "&"
		if query == "" {
			sep = "?"
		}
		r := reqWithClaims(http.MethodGet,
			"/api/v1/shared-mounts/"+shareID.String()+"/changes"+query+sep+"feed_version=2", nil, bob.Claims)
		r = withURLParams(r, "shareID", shareID.String())
		w := httptest.NewRecorder()
		env.shareH.MountChanges(w, r)
		if w.Code != http.StatusOK {
			return nil, 0, w.Code
		}
		var resp struct {
			Changes []struct {
				Path string `json:"path"`
				Seq  int64  `json:"seq"`
			} `json:"changes"`
			NextCursor int64 `json:"next_cursor"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		for _, c := range resp.Changes {
			if c.Seq == 0 {
				t.Errorf("entry %q carries no seq — the client cannot advance its cursor", c.Path)
			}
			paths = append(paths, c.Path)
		}
		return paths, resp.NextCursor, w.Code
	}

	first, cursor, code := call("?limit=2")
	if code != http.StatusOK {
		t.Fatalf("first page status = %d", code)
	}
	if len(first) != 2 {
		t.Fatalf("first page = %v, want 2 entries", first)
	}
	if cursor == 0 {
		t.Fatal("a full page must hand back next_cursor")
	}

	// Three files plus the directory row their paths hang off (ADR-0041).
	second, _, _ := call("?limit=2&since=" + strconv.FormatInt(cursor, 10))
	if len(second) != 2 {
		t.Fatalf("second page = %v, want the remaining entries", second)
	}
	for _, p := range second {
		for _, q := range first {
			if p == q {
				t.Fatalf("entry %q appeared on both pages", p)
			}
		}
	}

	// The old contract took an RFC3339 timestamp. Accepting one now would
	// silently mis-filter, so it has to be refused outright.
	if _, _, code := call("?since=2026-08-25T06:00:00Z"); code != http.StatusBadRequest {
		t.Fatalf("timestamp cursor status = %d, want 400", code)
	}
}

// A Shared Mount is a sync target like any other (ADR-0011), so its download
// endpoint must answer a range request the same way an owned root's does —
// otherwise a mounted multi-GB file still crosses the wire whole.
func TestDownloadMountFile_ServesRequestedByteRange(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/note.txt", []byte("0123456789"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", false)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/download?path=/note.txt",
		nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	r.Header.Set("Range", "bytes=2-5")
	w := httptest.NewRecorder()
	env.shareH.DownloadMountFile(w, r)

	if w.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206; body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "2345" {
		t.Errorf("body = %q, want %q", got, "2345")
	}
	if got := w.Header().Get("Content-Range"); got != "bytes 2-5/10" {
		t.Errorf("Content-Range = %q, want %q", got, "bytes 2-5/10")
	}
	if w.Header().Get("X-Content-Hash") == "" {
		t.Error("X-Content-Hash missing on a partial response")
	}
}
