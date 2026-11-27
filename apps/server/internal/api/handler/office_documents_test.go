package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"privasync/server/internal/officedoc"
)

var testOfficeCfg = officedoc.Config{
	URL:       "https://docserver.test",
	JWTSecret: "shared-secret-for-integration-tests",
}

func decodeOfficeConfig(t *testing.T, body []byte) officeConfigResponse {
	t.Helper()
	var resp officeConfigResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode office config response: %v body=%s", err, body)
	}
	return resp
}

// TestOfficeConfig_DisabledReturns404 verifies the AC "without a configured
// document server, behaviour for Office files stays unchanged" — the
// endpoint refuses before touching root/file lookups at all.
func TestOfficeConfig_DisabledReturns404(t *testing.T) {
	env := newTestEnv(t) // default officedoc.Config{} — disabled
	alice := makeUser(t, env, "alice_"+uniqueSuffix())

	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/00000000-0000-0000-0000-000000000000/files/office-config?path=/x.docx",
		nil, alice.Claims)
	r = withURLParams(r, "rootID", "00000000-0000-0000-0000-000000000000")
	w := httptest.NewRecorder()
	env.handler.OfficeConfig(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}

// TestOfficeConfig_ReturnsEditableConfigForOwner is the core happy path: an
// enabled document server, the file's owner requesting their own DOCX,
// expects a signed, editable editor config with save-back wired up — owning
// the root is both read and write access here (ADR-0027, edit per ADR-0033).
func TestOfficeConfig_ReturnsEditableConfigForOwner(t *testing.T) {
	env := newTestEnv(t, testOfficeCfg)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/report.docx", []byte("fake docx bytes"))

	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/files/office-config?path=/report.docx",
		nil, alice.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.OfficeConfig(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	resp := decodeOfficeConfig(t, w.Body.Bytes())
	if resp.DocumentServerURL != testOfficeCfg.URL {
		t.Fatalf("document_server_url = %q, want %q", resp.DocumentServerURL, testOfficeCfg.URL)
	}
	doc, ok := resp.Config["document"].(map[string]any)
	if !ok {
		t.Fatalf("expected document object, got %v", resp.Config["document"])
	}
	perms, ok := doc["permissions"].(map[string]any)
	if !ok || perms["edit"] != true {
		t.Fatalf("expected permissions.edit == true, got %v", perms)
	}
	editorCfg, ok := resp.Config["editorConfig"].(map[string]any)
	if !ok || editorCfg["mode"] != "edit" {
		t.Fatalf("expected editorConfig.mode == edit, got %v", editorCfg)
	}
	callbackURL, _ := editorCfg["callbackUrl"].(string)
	if callbackURL == "" {
		t.Fatal("expected non-empty callbackUrl for editable config")
	}
	token, _ := resp.Config["token"].(string)
	if token == "" {
		t.Fatal("expected non-empty config token")
	}
	// The token must verify against the document-server's own secret —
	// never the app's session JWTSecret (separate signing scheme by design).
	parsed, err := jwt.Parse(token, func(*jwt.Token) (interface{}, error) {
		return []byte(testOfficeCfg.JWTSecret), nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("token does not verify against document-server secret: %v", err)
	}
}

// TestOfficeConfig_EditableByFormat pins the format side of editability
// (ADR-0049): ODF round-trips through the document server unchanged and
// stays editable for the owner, while a format the document server only
// converts on open (legacy binary, macro-enabled) is served view-only —
// owning the root notwithstanding — so a save-back can never store
// converted bytes under the original extension.
func TestOfficeConfig_EditableByFormat(t *testing.T) {
	env := newTestEnv(t, testOfficeCfg)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())

	cases := []struct {
		path         string
		wantEditable bool
	}{
		{"/text.odt", true},
		{"/calc.ods", true},
		{"/slides.odp", true},
		{"/legacy.doc", false},
		{"/legacy.xls", false},
		{"/macros.docm", false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			uploadFileAs(t, env, alice.Claims, root.ID, tc.path, []byte("fake office bytes"))

			r := reqWithClaims(http.MethodGet,
				"/api/v1/sync-roots/"+root.ID.String()+"/files/office-config?path="+tc.path,
				nil, alice.Claims)
			r = withURLParams(r, "rootID", root.ID.String())
			w := httptest.NewRecorder()
			env.handler.OfficeConfig(w, r)

			if w.Code != http.StatusOK {
				t.Fatalf("status %d body=%s", w.Code, w.Body.String())
			}
			resp := decodeOfficeConfig(t, w.Body.Bytes())
			doc, _ := resp.Config["document"].(map[string]any)
			perms, ok := doc["permissions"].(map[string]any)
			if !ok || perms["edit"] != tc.wantEditable {
				t.Fatalf("permissions.edit = %v, want %v", perms, tc.wantEditable)
			}
			editorCfg, _ := resp.Config["editorConfig"].(map[string]any)
			wantMode := "view"
			if tc.wantEditable {
				wantMode = "edit"
			}
			if editorCfg["mode"] != wantMode {
				t.Fatalf("editorConfig.mode = %v, want %q", editorCfg["mode"], wantMode)
			}
			cb, _ := editorCfg["callbackUrl"].(string)
			if (cb != "") != tc.wantEditable {
				t.Fatalf("callbackUrl = %q, want empty=%v", cb, !tc.wantEditable)
			}
		})
	}
}

// TestOfficeConfig_UnsupportedFileType rejects extensions the document server
// cannot open at all before any file lookup.
func TestOfficeConfig_UnsupportedFileType(t *testing.T) {
	env := newTestEnv(t, testOfficeCfg)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/notes.txt", []byte("plain text"))

	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/files/office-config?path=/notes.txt",
		nil, alice.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.OfficeConfig(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}

// TestOfficeConfig_RejectsUserWithoutReadAccess is the AC "a user without
// read access to the path gets no valid editor config" for an own sync
// root: bob does not own alice's root and has no share, so the ownership
// lookup itself fails closed — 404, the same signal an unauthorized user
// gets everywhere else in this API (no existence disclosure).
func TestOfficeConfig_RejectsUserWithoutReadAccess(t *testing.T) {
	env := newTestEnv(t, testOfficeCfg)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/report.docx", []byte("fake docx bytes"))

	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/files/office-config?path=/report.docx",
		nil, bob.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.OfficeConfig(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}

// TestMountOfficeConfig_ReturnsConfigForReadOnlyRecipient covers the
// Shared-Mount side of the same AC: a recipient with a read-only share
// (can_write=false) still gets a valid view-only config — this slice never
// checks can_write, view-only is the only mode.
func TestMountOfficeConfig_ReturnsConfigForReadOnlyRecipient(t *testing.T) {
	env := newTestEnv(t, testOfficeCfg)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/shared/deck.pptx", []byte("fake pptx bytes"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/shared/deck.pptx", false)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/files/office-config",
		nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MountOfficeConfig(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	resp := decodeOfficeConfig(t, w.Body.Bytes())
	doc, ok := resp.Config["document"].(map[string]any)
	if !ok {
		t.Fatalf("expected document object, got %v", resp.Config["document"])
	}
	perms, ok := doc["permissions"].(map[string]any)
	if !ok || perms["edit"] != false {
		t.Fatalf("expected permissions.edit == false, got %v", perms)
	}
}

// TestMountOfficeConfig_ReturnsEditableConfigForWriteRecipient covers the
// write-recipient half of the same AC: a share with can_write=true gets an
// editable config with save-back wired up, exactly like an own-root file
// (ADR-0033) — editability follows the mount's own can_write flag.
// A file share never carries write access, only a directory share does
// (ADR-0015) — so this exercises the write path via a dir-share containing
// the document, addressed by ?path= relative to the mount root.
func TestMountOfficeConfig_ReturnsEditableConfigForWriteRecipient(t *testing.T) {
	env := newTestEnv(t, testOfficeCfg)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/shared/deck.pptx", []byte("fake pptx bytes"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/shared", true)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/files/office-config?path=/deck.pptx",
		nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MountOfficeConfig(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	resp := decodeOfficeConfig(t, w.Body.Bytes())
	doc, ok := resp.Config["document"].(map[string]any)
	if !ok {
		t.Fatalf("expected document object, got %v", resp.Config["document"])
	}
	perms, ok := doc["permissions"].(map[string]any)
	if !ok || perms["edit"] != true {
		t.Fatalf("expected permissions.edit == true, got %v", perms)
	}
	editorCfg, ok := resp.Config["editorConfig"].(map[string]any)
	if !ok || editorCfg["mode"] != "edit" {
		t.Fatalf("expected editorConfig.mode == edit, got %v", editorCfg)
	}
	if cb, _ := editorCfg["callbackUrl"].(string); cb == "" {
		t.Fatal("expected non-empty callbackUrl for editable config")
	}
}

// TestMountOfficeConfig_RejectsThirdParty is the mount-side AC "a user
// without read access gets no valid editor config" — carol is neither
// giver nor recipient of bob's mount and must get 404, matching the
// existing resolveMount boundary (see TestListMountFiles_RejectsThirdParty).
func TestMountOfficeConfig_RejectsThirdParty(t *testing.T) {
	env := newTestEnv(t, testOfficeCfg)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	carol := makeUser(t, env, "carol_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/shared/deck.pptx", []byte("fake pptx bytes"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/shared/deck.pptx", false)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+shareID.String()+"/files/office-config",
		nil, carol.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MountOfficeConfig(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("third-party should get 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestMountOfficeConfig_DisabledReturns404 mirrors the own-root disabled
// case for the mount-scoped endpoint.
func TestMountOfficeConfig_DisabledReturns404(t *testing.T) {
	env := newTestEnv(t) // disabled
	bob := makeUser(t, env, "bob_"+uniqueSuffix())

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/00000000-0000-0000-0000-000000000000/files/office-config",
		nil, bob.Claims)
	r = withURLParams(r, "shareID", "00000000-0000-0000-0000-000000000000")
	w := httptest.NewRecorder()
	env.shareH.MountOfficeConfig(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}

// TestServeOfficeDocument_ServesBytesForValidToken exercises the public,
// unauthenticated document-fetch endpoint the document server itself pulls
// bytes from — access is gated purely by the opaque token, not a Bearer
// header (the document server cannot send ours). See ADR-0027.
func TestServeOfficeDocument_ServesBytesForValidToken(t *testing.T) {
	env := newTestEnv(t, testOfficeCfg)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())
	content := []byte("fake xlsx bytes")
	uploadFileAs(t, env, alice.Claims, root.ID, "/sheet.xlsx", content)

	token := mintOfficeDocToken(alice.UserID, root.ID, "/sheet.xlsx")

	r := httptest.NewRequest(http.MethodGet, "/office/documents/"+token, nil)
	r = withURLParams(r, "token", token)
	w := httptest.NewRecorder()
	env.handler.ServeOfficeDocument(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != string(content) {
		t.Fatalf("body = %q, want %q", w.Body.String(), content)
	}
}

func TestServeOfficeDocument_InvalidTokenReturns404(t *testing.T) {
	env := newTestEnv(t, testOfficeCfg)

	r := httptest.NewRequest(http.MethodGet, "/office/documents/does-not-exist", nil)
	r = withURLParams(r, "token", "does-not-exist")
	w := httptest.NewRecorder()
	env.handler.ServeOfficeDocument(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}

func TestServeOfficeDocument_MissingTokenReturns400(t *testing.T) {
	env := newTestEnv(t, testOfficeCfg)

	r := httptest.NewRequest(http.MethodGet, "/office/documents/", nil)
	r = withURLParams(r, "token", "")
	w := httptest.NewRecorder()
	env.handler.ServeOfficeDocument(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}
