package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/auth"
)

// makeOwnConflict blind-overwrites an existing file in the user's own root
// (no If-Match), which materialises a loser copy + conflict row (ADR-0003),
// and returns the conflict id.
func makeOwnConflict(t *testing.T, env *testEnv, claims *auth.Claims, rootID uuid.UUID, path string, loserBytes []byte) uuid.UUID {
	t.Helper()
	r := reqWithClaims(http.MethodPost,
		"/api/v1/sync-roots/"+rootID.String()+"/files?path="+path,
		bytes.NewReader(loserBytes), claims)
	r = withURLParams(r, "rootID", rootID.String())
	w := httptest.NewRecorder()
	env.handler.UploadFile(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("setup: blind overwrite should 409, got %d body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	open := listOwnConflicts(t, env, claims, rootID)
	if len(open) != 1 {
		t.Fatalf("setup: expected 1 open conflict, got %d", len(open))
	}
	return open[0].ID
}

type conflictListEntry struct {
	ID           uuid.UUID `json:"id"`
	ConflictPath string    `json:"conflict_path"`
}

func listOwnConflicts(t *testing.T, env *testEnv, claims *auth.Claims, rootID uuid.UUID) []conflictListEntry {
	t.Helper()
	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+rootID.String()+"/conflicts", nil, claims)
	r = withURLParams(r, "rootID", rootID.String())
	w := httptest.NewRecorder()
	env.handler.ListConflicts(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list conflicts: status=%d body=%s", w.Code, w.Body.String())
	}
	var got []conflictListEntry
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("list conflicts: bad json: %v", err)
	}
	return got
}

func downloadAs(t *testing.T, env *testEnv, claims *auth.Claims, rootID uuid.UUID, path string) (int, []byte) {
	t.Helper()
	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+rootID.String()+"/files/download?path="+path, nil, claims)
	r = withURLParams(r, "rootID", rootID.String())
	w := httptest.NewRecorder()
	env.handler.DownloadFile(w, r)
	return w.Code, w.Body.Bytes()
}

func resolveOwnConflict(t *testing.T, env *testEnv, claims *auth.Claims, conflictID uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	r := reqWithClaims(http.MethodPost,
		"/api/v1/conflicts/"+conflictID.String()+"/resolve", rd, claims)
	r = withURLParams(r, "conflictID", conflictID.String())
	w := httptest.NewRecorder()
	env.handler.ResolveConflict(w, r)
	return w
}

// An unknown strategy must be rejected with 400 and leave the conflict open.
func TestResolveConflict_InvalidStrategyRejected(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/notes.txt", []byte("winner"))
	conflictID := makeOwnConflict(t, env, alice.Claims, root.ID, "/notes.txt", []byte("loser"))

	w := resolveOwnConflict(t, env, alice.Claims, conflictID, `{"strategy":"merge-magic"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid strategy should 400, got %d body=%s", w.Code, w.Body.String())
	}
	if got := listOwnConflicts(t, env, alice.Claims, root.ID); len(got) != 1 {
		t.Fatalf("conflict must stay open after rejected resolve, got %d open", len(got))
	}
}

// Explicit keep_current behaves like the historical default: conflict row
// closes, the original file keeps its bytes.
func TestResolveConflict_ExplicitKeepCurrent(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/notes.txt", []byte("winner"))
	conflictID := makeOwnConflict(t, env, alice.Claims, root.ID, "/notes.txt", []byte("loser"))

	w := resolveOwnConflict(t, env, alice.Claims, conflictID, `{"strategy":"keep_current"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("keep_current: status=%d body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	if got := listOwnConflicts(t, env, alice.Claims, root.ID); len(got) != 0 {
		t.Fatalf("conflict should be resolved, still listed: %+v", got)
	}
	code, body := downloadAs(t, env, alice.Claims, root.ID, "/notes.txt")
	if code != http.StatusOK || string(body) != "winner" {
		t.Fatalf("original must keep winner bytes, got status=%d body=%q", code, body)
	}
}

// Resolving must push conflict_resolved to the user's other sessions (SPEC
// §7) so a second desktop client re-syncs immediately instead of waiting for
// the next poll cycle.
func TestResolveConflict_PublishesConflictResolved(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/notes.txt", []byte("winner"))
	conflictID := makeOwnConflict(t, env, alice.Claims, root.ID, "/notes.txt", []byte("loser"))

	events, cleanup := captureEvents(t, env.hub, alice.UserID)
	defer cleanup()

	w := resolveOwnConflict(t, env, alice.Claims, conflictID, `{"strategy":"keep_current"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("resolve: status=%d body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	ev := waitForEvent(t, events, "conflict_resolved", 2*time.Second)
	data := eventDataMap(t, ev)
	if data["conflict_id"] != conflictID.String() {
		t.Errorf("conflict_id: got %v, want %s", data["conflict_id"], conflictID)
	}
	if data["sync_root_id"] != root.ID.String() {
		t.Errorf("sync_root_id: got %v, want %s", data["sync_root_id"], root.ID)
	}
	if data["file_path"] != "/notes.txt" {
		t.Errorf("file_path: got %v, want /notes.txt", data["file_path"])
	}
	if data["file_id"] == "" || data["file_id"] == nil {
		t.Errorf("file_id empty")
	}
}

// keep_conflict promotes the loser copy: the original path now serves the
// conflicting bytes, the conflict row closes, and resolving twice is a 404.
func TestResolveConflict_KeepConflictReplacesOriginal(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/notes.txt", []byte("winner"))
	conflictID := makeOwnConflict(t, env, alice.Claims, root.ID, "/notes.txt", []byte("loser-bytes"))

	w := resolveOwnConflict(t, env, alice.Claims, conflictID, `{"strategy":"keep_conflict"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("keep_conflict: status=%d body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	if got := listOwnConflicts(t, env, alice.Claims, root.ID); len(got) != 0 {
		t.Fatalf("conflict should be resolved, still listed: %+v", got)
	}
	code, body := downloadAs(t, env, alice.Claims, root.ID, "/notes.txt")
	if code != http.StatusOK || string(body) != "loser-bytes" {
		t.Fatalf("original must serve conflict bytes, got status=%d body=%q", code, body)
	}

	// Settled conflicts cannot be resolved again.
	w2 := resolveOwnConflict(t, env, alice.Claims, conflictID, `{"strategy":"keep_conflict"}`)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("second resolve should 404, got %d body=%s", w2.Code, w2.Body.String())
	}
}

// keep_both settles the conflict without discarding anything: the original
// keeps its bytes and the loser copy becomes a regular, downloadable file.
func TestResolveConflict_KeepBothPromotesLoserCopy(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/notes.txt", []byte("winner"))
	conflictID := makeOwnConflict(t, env, alice.Claims, root.ID, "/notes.txt", []byte("loser-bytes"))

	open := listOwnConflicts(t, env, alice.Claims, root.ID)
	if len(open) != 1 {
		t.Fatalf("setup: expected 1 open conflict, got %d", len(open))
	}
	conflictPath := open[0].ConflictPath

	w := resolveOwnConflict(t, env, alice.Claims, conflictID, `{"strategy":"keep_both"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("keep_both: status=%d body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	if got := listOwnConflicts(t, env, alice.Claims, root.ID); len(got) != 0 {
		t.Fatalf("conflict should be resolved, still listed: %+v", got)
	}
	code, body := downloadAs(t, env, alice.Claims, root.ID, "/notes.txt")
	if code != http.StatusOK || string(body) != "winner" {
		t.Fatalf("original must keep winner bytes, got status=%d body=%q", code, body)
	}
	code, body = downloadAs(t, env, alice.Claims, root.ID, conflictPath)
	if code != http.StatusOK || string(body) != "loser-bytes" {
		t.Fatalf("loser copy must become a regular file, got status=%d body=%q", code, body)
	}
}

// A writable mount recipient resolves with keep_conflict: the giver's
// original file now serves the recipient's conflicting bytes (ADR-0011
// permission model, strategy semantics as in the own-root case).
func TestResolveMountConflict_KeepConflictReplacesOriginal(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/seed.txt", []byte("seed"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)
	makeMountConflict(t, env, alice, bob, root.ID, shareID, "/seed.txt") // loser bytes: "bob-blind"

	lr := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/conflicts", nil, bob.Claims)
	lr = withURLParams(lr, "shareID", shareID.String())
	lw := httptest.NewRecorder()
	env.shareH.MountConflicts(lw, lr)
	var listed []conflictListEntry
	if err := json.Unmarshal(lw.Body.Bytes(), &listed); err != nil || len(listed) != 1 {
		t.Fatalf("setup: expected 1 mount conflict, got %d (err=%v)", len(listed), err)
	}
	conflictID := listed[0].ID

	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/conflicts/"+conflictID.String()+"/resolve",
		strings.NewReader(`{"strategy":"keep_conflict"}`), bob.Claims)
	r = withURLParams(r, "shareID", shareID.String(), "conflictID", conflictID.String())
	w := httptest.NewRecorder()
	env.shareH.ResolveMountConflict(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("mount keep_conflict: status=%d body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	code, body := downloadAs(t, env, alice.Claims, root.ID, "/2024/seed.txt")
	if code != http.StatusOK || string(body) != "bob-blind" {
		t.Fatalf("giver's file must serve conflict bytes, got status=%d body=%q", code, body)
	}
}

// Resolving a mount conflict must push conflict_resolved to both the
// giver (their root changed) and the recipient (their mount view is stale) —
// not just the party that clicked resolve.
func TestResolveMountConflict_PublishesConflictResolvedToBothParties(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/seed.txt", []byte("seed"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)
	makeMountConflict(t, env, alice, bob, root.ID, shareID, "/seed.txt") // loser bytes: "bob-blind"

	lr := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/conflicts", nil, bob.Claims)
	lr = withURLParams(lr, "shareID", shareID.String())
	lw := httptest.NewRecorder()
	env.shareH.MountConflicts(lw, lr)
	var listed []conflictListEntry
	if err := json.Unmarshal(lw.Body.Bytes(), &listed); err != nil || len(listed) != 1 {
		t.Fatalf("setup: expected 1 mount conflict, got %d (err=%v)", len(listed), err)
	}
	conflictID := listed[0].ID

	giverEvents, giverCleanup := captureEvents(t, env.hub, alice.UserID)
	defer giverCleanup()
	recipientEvents, recipientCleanup := captureEvents(t, env.hub, bob.UserID)
	defer recipientCleanup()

	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/conflicts/"+conflictID.String()+"/resolve",
		strings.NewReader(`{"strategy":"keep_conflict"}`), bob.Claims)
	r = withURLParams(r, "shareID", shareID.String(), "conflictID", conflictID.String())
	w := httptest.NewRecorder()
	env.shareH.ResolveMountConflict(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("mount keep_conflict: status=%d body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)

	giverEv := waitForEvent(t, giverEvents, "conflict_resolved", 2*time.Second)
	if data := eventDataMap(t, giverEv); data["conflict_id"] != conflictID.String() {
		t.Errorf("giver conflict_id: got %v, want %s", data["conflict_id"], conflictID)
	}
	recipientEv := waitForEvent(t, recipientEvents, "conflict_resolved", 2*time.Second)
	if data := eventDataMap(t, recipientEv); data["conflict_id"] != conflictID.String() {
		t.Errorf("recipient conflict_id: got %v, want %s", data["conflict_id"], conflictID)
	}
}

// The mount route validates the strategy like the own-root route.
func TestResolveMountConflict_InvalidStrategyRejected(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/seed.txt", []byte("seed"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)
	makeMountConflict(t, env, alice, bob, root.ID, shareID, "/seed.txt")

	lr := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/conflicts", nil, bob.Claims)
	lr = withURLParams(lr, "shareID", shareID.String())
	lw := httptest.NewRecorder()
	env.shareH.MountConflicts(lw, lr)
	var listed []conflictListEntry
	if err := json.Unmarshal(lw.Body.Bytes(), &listed); err != nil || len(listed) != 1 {
		t.Fatalf("setup: expected 1 mount conflict, got %d (err=%v)", len(listed), err)
	}

	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/conflicts/"+listed[0].ID.String()+"/resolve",
		strings.NewReader(`{"strategy":"merge-magic"}`), bob.Claims)
	r = withURLParams(r, "shareID", shareID.String(), "conflictID", listed[0].ID.String())
	w := httptest.NewRecorder()
	env.shareH.ResolveMountConflict(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid strategy should 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// GET /conflicts/{id} exposes both versions' metadata so a client can offer
// an informed choice: the live file (size, mtime) and the loser copy (size,
// detected-at). Foreign users get a 404.
func TestGetConflict_ReturnsBothVersions(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/notes.txt", []byte("winner"))
	conflictID := makeOwnConflict(t, env, alice.Claims, root.ID, "/notes.txt", []byte("loser-bytes"))

	r := reqWithClaims(http.MethodGet,
		"/api/v1/conflicts/"+conflictID.String(), nil, alice.Claims)
	r = withURLParams(r, "conflictID", conflictID.String())
	w := httptest.NewRecorder()
	env.handler.GetConflict(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("get conflict: status=%d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		ID           uuid.UUID `json:"id"`
		FilePath     string    `json:"file_path"`
		ConflictPath string    `json:"conflict_path"`
		Current      struct {
			Size  int64  `json:"size"`
			MTime string `json:"mtime"`
		} `json:"current"`
		Conflict struct {
			Size      int64  `json:"size"`
			CreatedAt string `json:"created_at"`
		} `json:"conflict"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad json: %v body=%s", err, w.Body.String())
	}
	if got.FilePath != "/notes.txt" {
		t.Errorf("file_path = %q, want /notes.txt", got.FilePath)
	}
	if got.ConflictPath == "" || got.ConflictPath == got.FilePath {
		t.Errorf("conflict_path missing or equal to file_path: %q", got.ConflictPath)
	}
	if got.Current.Size != int64(len("winner")) {
		t.Errorf("current.size = %d, want %d", got.Current.Size, len("winner"))
	}
	if got.Conflict.Size != int64(len("loser-bytes")) {
		t.Errorf("conflict.size = %d, want %d", got.Conflict.Size, len("loser-bytes"))
	}
	if got.Current.MTime == "" || got.Conflict.CreatedAt == "" {
		t.Errorf("timestamps missing: current.mtime=%q conflict.created_at=%q", got.Current.MTime, got.Conflict.CreatedAt)
	}

	// A different user must not see it.
	mallory := makeUser(t, env, "mallory_"+uniqueSuffix())
	r2 := reqWithClaims(http.MethodGet,
		"/api/v1/conflicts/"+conflictID.String(), nil, mallory.Claims)
	r2 = withURLParams(r2, "conflictID", conflictID.String())
	w2 := httptest.NewRecorder()
	env.handler.GetConflict(w2, r2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("foreign user should get 404, got %d", w2.Code)
	}
}

// GET /conflicts/{id}/file serves the loser copy's bytes — upload and move
// conflicts have no files row at the conflict path, so the path-based
// download cannot serve them.
func TestGetConflictFile_ServesLoserBytes(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/notes.txt", []byte("winner"))
	conflictID := makeOwnConflict(t, env, alice.Claims, root.ID, "/notes.txt", []byte("loser-bytes"))

	r := reqWithClaims(http.MethodGet,
		"/api/v1/conflicts/"+conflictID.String()+"/file", nil, alice.Claims)
	r = withURLParams(r, "conflictID", conflictID.String())
	w := httptest.NewRecorder()
	env.handler.GetConflictFile(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "loser-bytes" {
		t.Fatalf("conflict file: status=%d body=%q", w.Code, w.Body.String())
	}
}

// The mount detail endpoints mirror the own-root ones but relativize paths
// to the mount root and gate on mount membership instead of ownership.
func TestMountConflictVersions_RelativizedAndGated(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/seed.txt", []byte("seed"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/2024", true)
	makeMountConflict(t, env, alice, bob, root.ID, shareID, "/seed.txt") // loser: "bob-blind"

	lr := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/conflicts", nil, bob.Claims)
	lr = withURLParams(lr, "shareID", shareID.String())
	lw := httptest.NewRecorder()
	env.shareH.MountConflicts(lw, lr)
	var listed []conflictListEntry
	if err := json.Unmarshal(lw.Body.Bytes(), &listed); err != nil || len(listed) != 1 {
		t.Fatalf("setup: expected 1 mount conflict, got %d (err=%v)", len(listed), err)
	}
	conflictID := listed[0].ID

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/conflicts/"+conflictID.String(), nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String(), "conflictID", conflictID.String())
	w := httptest.NewRecorder()
	env.shareH.MountConflictVersions(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("mount conflict versions: status=%d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		FilePath     string `json:"file_path"`
		ConflictPath string `json:"conflict_path"`
		Current      struct {
			Size int64 `json:"size"`
		} `json:"current"`
		Conflict struct {
			Size int64 `json:"size"`
		} `json:"conflict"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad json: %v body=%s", err, w.Body.String())
	}
	if got.FilePath != "/seed.txt" {
		t.Errorf("file_path must be mount-relative, got %q", got.FilePath)
	}
	if strings.HasPrefix(got.ConflictPath, "/2024") {
		t.Errorf("conflict_path leaked giver layout: %q", got.ConflictPath)
	}
	if got.Current.Size != int64(len("seed")) || got.Conflict.Size != int64(len("bob-blind")) {
		t.Errorf("sizes wrong: current=%d conflict=%d", got.Current.Size, got.Conflict.Size)
	}

	// Loser bytes via the mount-scoped file endpoint.
	fr := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/conflicts/"+conflictID.String()+"/file", nil, bob.Claims)
	fr = withURLParams(fr, "shareID", shareID.String(), "conflictID", conflictID.String())
	fw := httptest.NewRecorder()
	env.shareH.MountConflictFile(fw, fr)
	if fw.Code != http.StatusOK || fw.Body.String() != "bob-blind" {
		t.Fatalf("mount conflict file: status=%d body=%q", fw.Code, fw.Body.String())
	}

	// A third party without the mount gets a 404.
	carol := makeUser(t, env, "carol_"+uniqueSuffix())
	cr := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/conflicts/"+conflictID.String(), nil, carol.Claims)
	cr = withURLParams(cr, "shareID", shareID.String(), "conflictID", conflictID.String())
	cw := httptest.NewRecorder()
	env.shareH.MountConflictVersions(cw, cr)
	if cw.Code != http.StatusNotFound {
		t.Fatalf("third party should get 404, got %d", cw.Code)
	}
}

// An empty body keeps backward compatibility with pre-strategy clients and
// resolves as keep_current.
func TestResolveConflict_EmptyBodyDefaultsToKeepCurrent(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/notes.txt", []byte("winner"))
	conflictID := makeOwnConflict(t, env, alice.Claims, root.ID, "/notes.txt", []byte("loser"))

	w := resolveOwnConflict(t, env, alice.Claims, conflictID, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("empty body: status=%d body=%s", w.Code, w.Body.String())
	}
	if got := listOwnConflicts(t, env, alice.Claims, root.ID); len(got) != 0 {
		t.Fatalf("conflict should be resolved, still listed: %+v", got)
	}
}
