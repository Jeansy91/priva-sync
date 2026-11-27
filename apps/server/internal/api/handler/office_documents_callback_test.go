package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/audit"
	"privasync/server/internal/auth"
	"privasync/server/internal/model"
	"privasync/server/internal/officedoc"
)

// mustDownload fetches the current content of path via the normal download
// route, so tests assert against what a client actually sees rather than
// reaching around the handler into storage.
func mustDownload(t *testing.T, env *testEnv, claims *auth.Claims, rootID uuid.UUID, path string) []byte {
	t.Helper()
	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+rootID.String()+"/files/download?path="+path, nil, claims)
	r = withURLParams(r, "rootID", rootID.String())
	w := httptest.NewRecorder()
	env.handler.DownloadFile(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("download %s: status=%d body=%s", path, w.Code, w.Body.String())
	}
	return w.Body.Bytes()
}

func officeCallbackToken(t *testing.T, env *testEnv, ownerID, rootID, actorID uuid.UUID, path string, baseVersion int64) string {
	t.Helper()
	tok, err := officedoc.SignCallbackToken(env.handler.officeCfg.JWTSecret, officedoc.CallbackClaims{
		OwnerID:     ownerID.String(),
		RootID:      rootID.String(),
		Path:        path,
		ActorUserID: actorID.String(),
		BaseVersion: baseVersion,
	})
	if err != nil {
		t.Fatalf("sign callback token: %v", err)
	}
	return tok
}

// seedEditableOfficeDoc uploads /report.docx for a fresh owner and mints a
// callback token bound to its current server_version — the shared setup
// every callback test needs before it can exercise one specific behavior.
func seedEditableOfficeDoc(t *testing.T, env *testEnv, content string) (alice *userAndClaims, root *model.SyncRoot, token string) {
	t.Helper()
	alice = makeUser(t, env, "alice_"+uniqueSuffix())
	root = createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/report.docx", []byte(content))
	dbFile, err := env.files.FindByPath(context.Background(), root.ID, "/report.docx")
	if err != nil {
		t.Fatalf("find file: %v", err)
	}
	token = officeCallbackToken(t, env, alice.UserID, root.ID, alice.UserID, "/report.docx", dbFile.ServerVersion)
	return alice, root, token
}

// newOfficeEnv starts a stand-in document server and builds an env configured
// to talk to exactly that server, so a callback quoting the server's own URL
// passes the origin check the way a real deployment does.
func newOfficeEnv(t *testing.T, h http.HandlerFunc) (*testEnv, *httptest.Server) {
	t.Helper()
	docServer := httptest.NewServer(h)
	t.Cleanup(docServer.Close)
	return newTestEnv(t, officedoc.Config{URL: docServer.URL, JWTSecret: testOfficeCfg.JWTSecret}), docServer
}

func postOfficeCallback(t *testing.T, env *testEnv, token string, status int, url string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"status": status, "url": url})
	r := httptest.NewRequest(http.MethodPost, "/office/documents/callback?token="+token, bytes.NewReader(body))
	w := httptest.NewRecorder()
	env.handler.OfficeCallback(w, r)
	return w
}

func decodeCallbackResponse(t *testing.T, body []byte) int {
	t.Helper()
	var resp struct {
		Error int `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode callback response: %v body=%s", err, body)
	}
	return resp.Error
}

// TestOfficeCallback_MissingTokenReturns400 — the callback route requires
// the opaque token query param; without it there is nothing to authorize.
func TestOfficeCallback_MissingTokenReturns400(t *testing.T) {
	env := newTestEnv(t, testOfficeCfg)
	w := postOfficeCallback(t, env, "", 2, "https://docserver.test/edited")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}

// TestOfficeCallback_InvalidTokenLeavesFileUntouched is the AC "a save
// callback with an invalid/expired JWT is rejected without changing the
// file" — a garbage token must fail closed before any fetch/persist happens.
func TestOfficeCallback_InvalidTokenLeavesFileUntouched(t *testing.T) {
	env := newTestEnv(t, testOfficeCfg)
	alice, root, _ := seedEditableOfficeDoc(t, env, "original")

	w := postOfficeCallback(t, env, "not-a-real-token", 2, "https://docserver.test/edited")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if got := mustDownload(t, env, alice.Claims, root.ID, "/report.docx"); string(got) != "original" {
		t.Fatalf("file was modified: got %q", got)
	}
}

// TestOfficeCallback_MustSavePersistsEditAndLogsAudit is the core AC: saving
// in the editor persists the change server-side, retrievable via the normal
// download path, and produces an audit-log entry (files.office_saved).
func TestOfficeCallback_MustSavePersistsEditAndLogsAudit(t *testing.T) {
	env, docServer := newOfficeEnv(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("edited by office"))
	})
	alice, root, token := seedEditableOfficeDoc(t, env, "original")

	w := postOfficeCallback(t, env, token, 2 /* MustSave */, docServer.URL)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if errCode := decodeCallbackResponse(t, w.Body.Bytes()); errCode != 0 {
		t.Fatalf("callback response error = %d, want 0", errCode)
	}
	env.drainOutbox(t) // the staging->target rename runs via outbox, same as any upload
	if got := mustDownload(t, env, alice.Claims, root.ID, "/report.docx"); string(got) != "edited by office" {
		t.Fatalf("file content = %q, want %q", got, "edited by office")
	}

	events, err := env.audits.List(context.Background(), audit.ListFilter{EventType: audit.EventFileOfficeSaved, UserID: &alice.UserID})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(events))
	}
}

// TestOfficeCallback_MustForceSavePersists — status 6 (force-save) behaves
// exactly like status 2 (save): both carry new bytes to persist.
func TestOfficeCallback_MustForceSavePersists(t *testing.T) {
	env, docServer := newOfficeEnv(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("force-saved"))
	})
	alice, root, token := seedEditableOfficeDoc(t, env, "original")

	w := postOfficeCallback(t, env, token, 6 /* MustForceSave */, docServer.URL)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	env.drainOutbox(t)
	if got := mustDownload(t, env, alice.Claims, root.ID, "/report.docx"); string(got) != "force-saved" {
		t.Fatalf("file content = %q, want %q", got, "force-saved")
	}
}

// TestOfficeCallback_LifecyclePingOnlyAcks covers every status besides
// MustSave/MustForceSave (editing started, closed without changes, save
// error, ...): the document server expects a plain ack, and nothing gets
// fetched or persisted.
func TestOfficeCallback_LifecyclePingOnlyAcks(t *testing.T) {
	env := newTestEnv(t, testOfficeCfg)
	alice, root, token := seedEditableOfficeDoc(t, env, "original")
	// status 1 = "being edited" — a lifecycle ping with no url to fetch from.
	w := postOfficeCallback(t, env, token, 1, "")

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if errCode := decodeCallbackResponse(t, w.Body.Bytes()); errCode != 0 {
		t.Fatalf("callback response error = %d, want 0", errCode)
	}
	if got := mustDownload(t, env, alice.Claims, root.ID, "/report.docx"); string(got) != "original" {
		t.Fatalf("file was modified by a lifecycle ping: got %q", got)
	}
	events, err := env.audits.List(context.Background(), audit.ListFilter{EventType: audit.EventFileOfficeSaved, UserID: &alice.UserID})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no audit entry for a lifecycle ping, got %d", len(events))
	}
}

// TestOfficeCallback_FetchFailureReturnsErrorOne — the document server's
// GET for the edited bytes failing (unreachable/non-200) must surface as
// {"error":1} so the editor shows a save failure, and must never touch the
// stored file.
func TestOfficeCallback_FetchFailureReturnsErrorOne(t *testing.T) {
	env, docServer := newOfficeEnv(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	alice, root, token := seedEditableOfficeDoc(t, env, "original")

	w := postOfficeCallback(t, env, token, 2, docServer.URL)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if errCode := decodeCallbackResponse(t, w.Body.Bytes()); errCode != 1 {
		t.Fatalf("callback response error = %d, want 1", errCode)
	}
	if got := mustDownload(t, env, alice.Claims, root.ID, "/report.docx"); string(got) != "original" {
		t.Fatalf("file was modified despite fetch failure: got %q", got)
	}
}

// TestOfficeCallback_ConcurrentWriteMaterializesConflictNotOverwrite is the
// AC "concurrent write/edit outside the editor triggers the existing
// conflict handling, no silent data loss": the callback's BaseVersion goes
// stale between config-mint and save, so the retry (IfMatch cleared) must
// route through the real-conflict/loser-copy path (ADR-0003) instead of
// clobbering the concurrent writer's content.
func TestOfficeCallback_ConcurrentWriteMaterializesConflictNotOverwrite(t *testing.T) {
	env, docServer := newOfficeEnv(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("edited by office, stale base"))
	})
	// Callback token minted against the pre-concurrent-write version.
	alice, root, token := seedEditableOfficeDoc(t, env, "original")

	// Someone else writes the file while the editor session is open.
	overwrite := reqWithClaims(http.MethodPut,
		"/api/v1/sync-roots/"+root.ID.String()+"/files?path=/report.docx",
		bytes.NewReader([]byte("concurrent write")), alice.Claims)
	overwrite = withURLParams(overwrite, "rootID", root.ID.String())
	overwrite.Header.Set("If-Match", "0")
	recOverwrite := httptest.NewRecorder()
	env.handler.UploadFile(recOverwrite, overwrite)
	if recOverwrite.Code != http.StatusOK {
		t.Fatalf("concurrent overwrite: status=%d body=%s", recOverwrite.Code, recOverwrite.Body.String())
	}
	env.drainOutbox(t)

	w := postOfficeCallback(t, env, token, 2, docServer.URL)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if errCode := decodeCallbackResponse(t, w.Body.Bytes()); errCode != 0 {
		t.Fatalf("callback response error = %d, want 0", errCode)
	}

	// The concurrent writer's content must survive untouched at the
	// original path — the office edit must not silently clobber it.
	if got := mustDownload(t, env, alice.Claims, root.ID, "/report.docx"); string(got) != "concurrent write" {
		t.Fatalf("kept file content = %q, want %q (must not be silently overwritten)", got, "concurrent write")
	}

	open, err := env.conflicts.ListOpenConflicts(context.Background(), alice.UserID, root.ID)
	if err != nil {
		t.Fatalf("list conflicts: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("expected 1 open conflict from the office save, got %d", len(open))
	}
}

// The callback token is not a secret from the user: it is handed out in the
// /office-config response so the document server can present it later. Any
// authenticated caller can therefore replay the callback with a url of their
// own choosing. Fetching it would turn the server into a proxy for everything
// it can reach -- and because the bytes are stored in the caller's own file,
// they can be downloaded straight back out.
func TestOfficeCallback_RefusesForeignFetchURL(t *testing.T) {
	var reached bool
	env, docServer := newOfficeEnv(t, func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		_, _ = w.Write([]byte("must not be fetched"))
	})
	alice, root, token := seedEditableOfficeDoc(t, env, "original")

	// A second server stands in for "somewhere the server can reach but the
	// caller may not" -- internal admin panel, cloud metadata, database UI.
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("internal secret"))
	}))
	defer internal.Close()

	for _, target := range []string{
		internal.URL + "/latest/meta-data/",
		"http://169.254.169.254/latest/meta-data/",
		docServer.URL + ".evil.test/x",
	} {
		w := postOfficeCallback(t, env, token, 2 /* MustSave */, target)

		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200 with an error envelope; body=%s", target, w.Code, w.Body.String())
		}
		if errCode := decodeCallbackResponse(t, w.Body.Bytes()); errCode != 1 {
			t.Fatalf("%s: callback response error = %d, want 1 (refused)", target, errCode)
		}
	}

	if reached {
		t.Error("the document server was contacted for a callback it never sent")
	}
	env.drainOutbox(t)
	if got := mustDownload(t, env, alice.Claims, root.ID, "/report.docx"); string(got) != "original" {
		t.Fatalf("file content = %q, want it untouched (%q)", got, "original")
	}
}
