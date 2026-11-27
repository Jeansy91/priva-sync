package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func decodeMediaStreamToken(t *testing.T, body []byte) mediaStreamTokenResponse {
	t.Helper()
	var resp mediaStreamTokenResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode media stream token response: %v body=%s", err, body)
	}
	return resp
}

// TestServeMediaStream_ServesBytesForValidToken exercises the public,
// unauthenticated media-fetch endpoint a <video>/<audio> element pulls bytes
// from directly — the browser cannot send our Bearer header on a native
// `src`, so access is gated by the opaque, path-bound token instead (ADR-0067).
func TestServeMediaStream_ServesBytesForValidToken(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())
	content := []byte("fake mp4 bytes")
	uploadFileAs(t, env, alice.Claims, root.ID, "/clip.mp4", content)

	token := mintMediaStreamToken(alice.UserID, root.ID, "/clip.mp4")

	r := httptest.NewRequest(http.MethodGet, "/media/"+token, nil)
	r = withURLParams(r, "token", token)
	w := httptest.NewRecorder()
	env.handler.ServeMediaStream(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != string(content) {
		t.Fatalf("body = %q, want %q", w.Body.String(), content)
	}
}

// TestServeMediaStream_TokenIsReusable is the behavior that distinguishes
// this grant from downloadGrant: a player fires many separate Range
// requests while seeking/buffering, so the token must not be consumed on
// first use.
func TestServeMediaStream_TokenIsReusable(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())
	content := []byte("fake mp4 bytes")
	uploadFileAs(t, env, alice.Claims, root.ID, "/clip.mp4", content)

	token := mintMediaStreamToken(alice.UserID, root.ID, "/clip.mp4")

	for i := 0; i < 2; i++ {
		r := httptest.NewRequest(http.MethodGet, "/media/"+token, nil)
		r = withURLParams(r, "token", token)
		w := httptest.NewRecorder()
		env.handler.ServeMediaStream(w, r)

		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status %d body=%s", i+1, w.Code, w.Body.String())
		}
	}
}

// TestServeMediaStream_RangeRequestReturns206 proves the endpoint is wired to
// serveFileContent (Range-capable, ADR-0047), not storage.ServeFile — this is
// the entire point of the grant: a player fetching a byte window doesn't pull
// the whole file across the wire.
func TestServeMediaStream_RangeRequestReturns206(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())
	content := []byte("0123456789abcdef")
	uploadFileAs(t, env, alice.Claims, root.ID, "/clip.mp4", content)

	token := mintMediaStreamToken(alice.UserID, root.ID, "/clip.mp4")

	r := httptest.NewRequest(http.MethodGet, "/media/"+token, nil)
	r.Header.Set("Range", "bytes=2-5")
	r = withURLParams(r, "token", token)
	w := httptest.NewRecorder()
	env.handler.ServeMediaStream(w, r)

	if w.Code != http.StatusPartialContent {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if want := "bytes 2-5/16"; w.Header().Get("Content-Range") != want {
		t.Fatalf("Content-Range = %q, want %q", w.Header().Get("Content-Range"), want)
	}
	if w.Body.String() != "2345" {
		t.Fatalf("body = %q, want %q", w.Body.String(), "2345")
	}
}

func TestServeMediaStream_InvalidTokenReturns404(t *testing.T) {
	env := newTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/media/does-not-exist", nil)
	r = withURLParams(r, "token", "does-not-exist")
	w := httptest.NewRecorder()
	env.handler.ServeMediaStream(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}

// TestMintMediaStreamToken_ReturnsUsableTokenForOwner is the end-to-end happy
// path: the Bearer-authed mint endpoint hands back a token, and that token
// works against the public, unauthenticated serve endpoint.
func TestMintMediaStreamToken_ReturnsUsableTokenForOwner(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())
	content := []byte("fake mp4 bytes")
	uploadFileAs(t, env, alice.Claims, root.ID, "/clip.mp4", content)

	r := reqWithClaims(http.MethodPost,
		"/api/v1/sync-roots/"+root.ID.String()+"/files/media-token?path=/clip.mp4",
		nil, alice.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.MintMediaStreamToken(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	resp := decodeMediaStreamToken(t, w.Body.Bytes())
	if resp.Token == "" {
		t.Fatal("expected non-empty token")
	}

	sr := httptest.NewRequest(http.MethodGet, "/media/"+resp.Token, nil)
	sr = withURLParams(sr, "token", resp.Token)
	sw := httptest.NewRecorder()
	env.handler.ServeMediaStream(sw, sr)

	if sw.Code != http.StatusOK {
		t.Fatalf("serve status %d body=%s", sw.Code, sw.Body.String())
	}
	if sw.Body.String() != string(content) {
		t.Fatalf("serve body = %q, want %q", sw.Body.String(), content)
	}
}

// TestMintMediaStreamToken_RejectsNonOwner mirrors OfficeConfig's ownership
// check: a user who doesn't own the root gets no token — 404, matching the
// no-existence-disclosure shape used everywhere else in this API.
func TestMintMediaStreamToken_RejectsNonOwner(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/clip.mp4", []byte("fake mp4 bytes"))

	r := reqWithClaims(http.MethodPost,
		"/api/v1/sync-roots/"+root.ID.String()+"/files/media-token?path=/clip.mp4",
		nil, bob.Claims)
	r = withURLParams(r, "rootID", root.ID.String())
	w := httptest.NewRecorder()
	env.handler.MintMediaStreamToken(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}

// TestMintMountMediaStreamToken_ReturnsTokenForReadOnlyRecipient covers the
// Shared-Mount side: a read-only recipient (can_write=false) can still mint
// a media-stream token — playback needs only read access, unlike editing.
func TestMintMountMediaStreamToken_ReturnsTokenForReadOnlyRecipient(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())
	content := []byte("fake mp4 bytes")
	uploadFileAs(t, env, alice.Claims, root.ID, "/shared/clip.mp4", content)
	shareID := createUserShare(t, env, alice, bob, root.ID, "/shared/clip.mp4", false)

	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/files/media-token",
		nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MintMountMediaStreamToken(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	resp := decodeMediaStreamToken(t, w.Body.Bytes())
	if resp.Token == "" {
		t.Fatal("expected non-empty token")
	}

	sr := httptest.NewRequest(http.MethodGet, "/media/"+resp.Token, nil)
	sr = withURLParams(sr, "token", resp.Token)
	sw := httptest.NewRecorder()
	env.handler.ServeMediaStream(sw, sr)

	if sw.Code != http.StatusOK {
		t.Fatalf("serve status %d body=%s", sw.Code, sw.Body.String())
	}
	if sw.Body.String() != string(content) {
		t.Fatalf("serve body = %q, want %q", sw.Body.String(), content)
	}
}

// TestMintMountMediaStreamToken_RejectsThirdParty mirrors the mount-side AC
// used throughout this API: a user who is neither giver nor recipient gets
// no valid token.
func TestMintMountMediaStreamToken_RejectsThirdParty(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	carol := makeUser(t, env, "carol_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/shared/clip.mp4", []byte("fake mp4 bytes"))
	shareID := createUserShare(t, env, alice, bob, root.ID, "/shared/clip.mp4", false)

	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/files/media-token",
		nil, carol.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MintMountMediaStreamToken(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("third-party should get 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestServeMediaStream_SurvivesShareRevocationUntilTTL pins a deliberate,
// documented trade-off (ADR-0067): the grant caches Owner/Root/Path at mint
// time rather than re-checking the share on every Range request, so revoking
// a share after a token was minted does not immediately invalidate it — the
// token still works until its own short TTL expires. This is the same
// revocation-window bound the ADR accepts, not an oversight; the test exists
// so a future change to that behavior is a deliberate decision, not a
// silent regression either way.
func TestServeMediaStream_SurvivesShareRevocationUntilTTL(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())
	content := []byte("fake mp4 bytes")
	uploadFileAs(t, env, alice.Claims, root.ID, "/shared/clip.mp4", content)
	shareID := createUserShare(t, env, alice, bob, root.ID, "/shared/clip.mp4", false)

	r := reqWithClaims(http.MethodPost,
		"/api/v1/shared-mounts/"+shareID.String()+"/files/media-token",
		nil, bob.Claims)
	r = withURLParams(r, "shareID", shareID.String())
	w := httptest.NewRecorder()
	env.shareH.MintMountMediaStreamToken(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("mint status %d body=%s", w.Code, w.Body.String())
	}
	token := decodeMediaStreamToken(t, w.Body.Bytes()).Token

	aggID := shareAggregateID(t, env, alice.UserID, root.ID, "/shared/clip.mp4")
	dr := reqWithClaims(http.MethodDelete, "/api/v1/shares/"+aggID.String()+"/grants/"+bob.UserID.String(), nil, alice.Claims)
	dr = withURLParams(dr, "shareID", aggID.String(), "userID", bob.UserID.String())
	dw := httptest.NewRecorder()
	env.shareH.DeleteShareGrant(dw, dr)
	if dw.Code != http.StatusNoContent && dw.Code != http.StatusOK {
		t.Fatalf("revoke status %d body=%s", dw.Code, dw.Body.String())
	}

	sr := httptest.NewRequest(http.MethodGet, "/media/"+token, nil)
	sr = withURLParams(sr, "token", token)
	sw := httptest.NewRecorder()
	env.handler.ServeMediaStream(sw, sr)

	if sw.Code != http.StatusOK {
		t.Fatalf("serve after revocation: status %d body=%s (expected the grant to survive until TTL — see ADR-0067)", sw.Code, sw.Body.String())
	}
}

func TestServeMediaStream_MissingTokenReturns400(t *testing.T) {
	env := newTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/media/", nil)
	r = withURLParams(r, "token", "")
	w := httptest.NewRecorder()
	env.handler.ServeMediaStream(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}
