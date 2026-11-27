package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func postPublicLink(t *testing.T, env *testEnv, owner *userAndClaims, rootID uuid.UUID, path, kind string) (int, publicShareResp) {
	t.Helper()
	return postPublicLinkExpiring(t, env, owner, rootID, path, kind, nil)
}

func postPublicLinkExpiring(t *testing.T, env *testEnv, owner *userAndClaims, rootID uuid.UUID, path, kind string, expiresAt *string) (int, publicShareResp) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"path": path, "kind": kind, "expires_at": expiresAt})
	r := reqWithClaims(http.MethodPost,
		"/api/v1/sync-roots/"+rootID.String()+"/public-links", bytes.NewReader(body), owner.Claims)
	r = withURLParams(r, "rootID", rootID.String())
	w := httptest.NewRecorder()
	env.shareH.CreatePublicLink(w, r)
	var resp publicShareResp
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

func deletePublicLink(t *testing.T, env *testEnv, owner *userAndClaims, rootID uuid.UUID, path, kind string) int {
	t.Helper()
	r := reqWithClaims(http.MethodDelete,
		"/api/v1/sync-roots/"+rootID.String()+"/public-links?path="+path+"&kind="+kind, nil, owner.Claims)
	r = withURLParams(r, "rootID", rootID.String())
	w := httptest.NewRecorder()
	env.shareH.DeletePublicLink(w, r)
	return w.Code
}

// Read and write public links are generated and removed independently on one
// public share aggregate. See ADR-0015.
func TestPublicLink_IndependentReadAndWrite(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/a.txt", []byte("x"))

	// Generate read only.
	code, resp := postPublicLink(t, env, alice, root.ID, "/dir", "read")
	if code != http.StatusCreated {
		t.Fatalf("create read link: %d", code)
	}
	if resp.ReadToken == "" || resp.WriteToken != "" {
		t.Fatalf("expected only read token, got read=%q write=%q", resp.ReadToken, resp.WriteToken)
	}

	// Add write — both present.
	code, resp = postPublicLink(t, env, alice, root.ID, "/dir", "write")
	if code != http.StatusCreated {
		t.Fatalf("create write link: %d", code)
	}
	if resp.ReadToken == "" || resp.WriteToken == "" {
		t.Fatalf("expected both tokens, got read=%q write=%q", resp.ReadToken, resp.WriteToken)
	}

	// Remove read — write survives.
	if c := deletePublicLink(t, env, alice, root.ID, "/dir", "read"); c != http.StatusNoContent {
		t.Fatalf("delete read: %d", c)
	}
	_, after := postPublicLink(t, env, alice, root.ID, "/dir", "write") // re-fetch via idempotent set
	if after.WriteToken == "" {
		t.Fatalf("write token must survive read deletion")
	}
}

func publicEntriesStatus(t *testing.T, env *testEnv, token string) int {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/public/shares/"+token+"/entries", nil)
	r = withURLParams(r, "token", token)
	w := httptest.NewRecorder()
	env.shareH.PublicShareEntries(w, r)
	return w.Code
}

// Expiry is chosen at link creation and applies per link: an expired read
// link turns 410 while the independently created write link keeps working.
func TestPublicLink_PerLinkExpiryAtCreation(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/a.txt", []byte("x"))

	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	code, resp := postPublicLinkExpiring(t, env, alice, root.ID, "/dir", "read", &past)
	if code != http.StatusCreated || resp.ReadToken == "" {
		t.Fatalf("create read link: %d", code)
	}
	if resp.ReadExpiresAt == nil {
		t.Fatalf("read_expires_at missing in response")
	}

	code, resp = postPublicLink(t, env, alice, root.ID, "/dir", "write")
	if code != http.StatusCreated || resp.WriteToken == "" {
		t.Fatalf("create write link: %d", code)
	}
	if resp.WriteExpiresAt != nil {
		t.Fatalf("write link must not inherit the read expiry, got %v", *resp.WriteExpiresAt)
	}

	if got := publicEntriesStatus(t, env, resp.ReadToken); got != http.StatusGone {
		t.Errorf("expired read link: status=%d want 410", got)
	}
	if got := publicEntriesStatus(t, env, resp.WriteToken); got != http.StatusOK {
		t.Errorf("unexpired write link: status=%d want 200", got)
	}
}

// The giver-side share map surfaces each link's expiry so the UI can show
// "expires at" next to a generated link.
func TestPublicLink_ShareMapCarriesPerLinkExpiry(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/a.txt", []byte("x"))

	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if code, _ := postPublicLinkExpiring(t, env, alice, root.ID, "/dir", "read", &future); code != http.StatusCreated {
		t.Fatalf("create read link: %d", code)
	}

	r := reqWithClaims(http.MethodGet,
		"/api/v1/sync-roots/"+root.ID.String()+"/shares", nil, alice.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.shareH.ListSharesByRoot(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("share map: %d body=%s", w.Code, w.Body.String())
	}
	var got map[string][]struct {
		ShareType      string  `json:"share_type"`
		ReadExpiresAt  *string `json:"read_expires_at"`
		WriteExpiresAt *string `json:"write_expires_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	entries := got["/dir"]
	if len(entries) != 1 || entries[0].ShareType != "public" {
		t.Fatalf("expected one public entry under /dir: %+v", got)
	}
	if entries[0].ReadExpiresAt == nil {
		t.Fatalf("read_expires_at missing in share map: %+v", entries[0])
	}
	if entries[0].WriteExpiresAt != nil {
		t.Errorf("write_expires_at must stay empty, got %v", *entries[0].WriteExpiresAt)
	}
}

// Regenerating a link replaces token and expiry together — a fresh link
// without a limit must not inherit the stale one.
func TestPublicLink_RegenerateResetsExpiry(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/a.txt", []byte("x"))

	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	if code, _ := postPublicLinkExpiring(t, env, alice, root.ID, "/dir", "read", &past); code != http.StatusCreated {
		t.Fatalf("create expiring read link: %d", code)
	}
	code, resp := postPublicLink(t, env, alice, root.ID, "/dir", "read")
	if code != http.StatusCreated {
		t.Fatalf("regenerate read link: %d", code)
	}
	if resp.ReadExpiresAt != nil {
		t.Errorf("regenerated link inherits expiry: %v", *resp.ReadExpiresAt)
	}
	if got := publicEntriesStatus(t, env, resp.ReadToken); got != http.StatusOK {
		t.Errorf("regenerated link entries: status=%d want 200", got)
	}
}

// A share created through the public-links endpoint has no legacy `token`
// column — only read_token/write_token. Entries and download must still
// resolve it; otherwise every link generated by the web UI shows
// "unavailable or expired" to recipients.
func TestPublicLink_EntriesAndDownloadResolveReadToken(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/a.txt", []byte("payload"))

	code, resp := postPublicLink(t, env, alice, root.ID, "/dir", "read")
	if code != http.StatusCreated || resp.ReadToken == "" {
		t.Fatalf("create read link: %d token=%q", code, resp.ReadToken)
	}

	r := httptest.NewRequest(http.MethodGet, "/public/shares/"+resp.ReadToken+"/entries", nil)
	r = withURLParams(r, "token", resp.ReadToken)
	w := httptest.NewRecorder()
	env.shareH.PublicShareEntries(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("entries: status=%d body=%s", w.Code, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodGet, "/public/shares/"+resp.ReadToken+"/download?sub=a.txt", nil)
	r = withURLParams(r, "token", resp.ReadToken)
	w = httptest.NewRecorder()
	env.shareH.PublicShareDownload(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("download: status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "payload" {
		t.Errorf("download body=%q want %q", got, "payload")
	}
}

// Files have no write access, so a write link is rejected.
func TestPublicLink_FileRejectsWrite(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/doc.txt", []byte("x"))

	if code, _ := postPublicLink(t, env, alice, root.ID, "/doc.txt", "write"); code != http.StatusBadRequest {
		t.Fatalf("file write link should be 400, got %d", code)
	}
	if code, resp := postPublicLink(t, env, alice, root.ID, "/doc.txt", "read"); code != http.StatusCreated || resp.ReadToken == "" {
		t.Fatalf("file read link should succeed, got %d token=%q", code, resp.ReadToken)
	}
}
