package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// A public share on a directory yields two links — a read token and a write
// token. Resolving the write token exposes can_write; the read token does not.
// A file share yields only a read token (files carry no write access).
// See ADR-0015.
func TestCreatePublicShare_DirHasReadAndWriteToken(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/folder/a.txt", []byte("x"))

	resp := createPublicShare(t, env, alice, root.ID, "/folder")
	if resp.ReadToken == "" {
		t.Fatalf("read_token missing")
	}
	if resp.WriteToken == "" {
		t.Fatalf("write_token missing for directory share")
	}
	if resp.ReadToken == resp.WriteToken {
		t.Fatalf("read and write token must differ")
	}

	// Write token resolves with can_write=true; read token does not.
	if cw := publicCanWrite(t, env, resp.WriteToken); !cw {
		t.Errorf("write token can_write = false, want true")
	}
	if cw := publicCanWrite(t, env, resp.ReadToken); cw {
		t.Errorf("read token can_write = true, want false")
	}
}

func TestCreatePublicShare_FileHasNoWriteToken(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/doc.txt", []byte("x"))

	resp := createPublicShare(t, env, alice, root.ID, "/doc.txt")
	if resp.ReadToken == "" {
		t.Fatalf("read_token missing for file share")
	}
	if resp.WriteToken != "" {
		t.Fatalf("file share must not have a write token, got %q", resp.WriteToken)
	}
}

// The anonymous resolve endpoint must not echo any token back: a read-link
// holder would otherwise learn the write token (privilege escalation).
func TestPublicShare_DoesNotLeakTokens(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/folder/a.txt", []byte("x"))

	created := createPublicShare(t, env, alice, root.ID, "/folder")

	r := httptest.NewRequest(http.MethodGet, "/public/shares/"+created.ReadToken, nil)
	r = withURLParams(r, "token", created.ReadToken)
	w := httptest.NewRecorder()
	env.shareH.PublicShare(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("resolve: status=%d body=%s", w.Code, w.Body.String())
	}
	var got publicShareResp
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Token != "" || got.ReadToken != "" || got.WriteToken != "" {
		t.Errorf("anonymous resolve leaks tokens: %+v", got)
	}
}

type publicShareResp struct {
	Token          string  `json:"token"`
	ReadToken      string  `json:"read_token"`
	WriteToken     string  `json:"write_token"`
	ExpiresAt      *string `json:"expires_at"`
	ReadExpiresAt  *string `json:"read_expires_at"`
	WriteExpiresAt *string `json:"write_expires_at"`
}

func createPublicShare(t *testing.T, env *testEnv, owner *userAndClaims, rootID uuid.UUID, path string) publicShareResp {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"path": path, "share_type": "public"})
	r := reqWithClaims(http.MethodPost,
		"/api/v1/sync-roots/"+rootID.String()+"/shares", bytes.NewReader(body), owner.Claims)
	r = withURLParams(r, "rootID", rootID.String())
	w := httptest.NewRecorder()
	env.shareH.CreateShare(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create public share: status=%d body=%s", w.Code, w.Body.String())
	}
	var resp publicShareResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func publicCanWrite(t *testing.T, env *testEnv, token string) bool {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/public/shares/"+token, nil)
	r = withURLParams(r, "token", token)
	w := httptest.NewRecorder()
	env.shareH.PublicShare(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("public share resolve: status=%d body=%s", w.Code, w.Body.String())
	}
	var v struct {
		CanWrite bool `json:"can_write"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v.CanWrite
}
