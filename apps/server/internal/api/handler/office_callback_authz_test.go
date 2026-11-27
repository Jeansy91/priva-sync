package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// mountedOfficeDoc sets alice up with a document, shares its directory with
// bob at can_write, and returns the callback token bob's editor session would
// carry — the exact wire path MountOfficeConfig produces.
func mountedOfficeDoc(t *testing.T, env *testEnv) (alice, bob *userAndClaims, rootID, grantID uuid.UUID, token, docServerURL string) {
	t.Helper()
	alice = makeUser(t, env, "alice_"+uniqueSuffix())
	bob = makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "root_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/team/report.docx", []byte("original"))
	grantID = createUserShare(t, env, alice, bob, root.ID, "/team", true)

	r := reqWithClaims(http.MethodGet,
		"/api/v1/shared-mounts/"+grantID.String()+"/files/office-config?path=/report.docx", nil, bob.Claims)
	r = withURLParams(r, "shareID", grantID.String())
	w := httptest.NewRecorder()
	env.shareH.MountOfficeConfig(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("office config: status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Config struct {
			EditorConfig struct {
				CallbackURL string `json:"callbackUrl"`
			} `json:"editorConfig"`
		} `json:"config"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode office config: %v", err)
	}
	cb := resp.Config.EditorConfig.CallbackURL
	if cb == "" {
		t.Fatal("editable mount config carried no callbackUrl")
	}
	// The token is the query part the document server would present back.
	idx := bytes.LastIndexByte([]byte(cb), '=')
	if idx < 0 {
		t.Fatalf("unexpected callback url shape: %s", cb)
	}
	return alice, bob, root.ID, grantID, cb[idx+1:], ""
}

func fileContent(t *testing.T, env *testEnv, claims *userAndClaims, rootID uuid.UUID, path string) string {
	t.Helper()
	return string(mustDownload(t, env, claims.Claims, rootID, path))
}

// The callback token lives for 24h and used to be the only thing consulted at
// save time. Revoking the share therefore did not stop the writes it
// authorised — the one path that skipped the live grant check every other
// mount operation performs.
func TestOfficeCallback_RejectsSaveAfterAccessChanged(t *testing.T) {
	tests := []struct {
		name   string
		revoke func(t *testing.T, env *testEnv, alice, bob *userAndClaims, rootID, grantID uuid.UUID)
	}{
		{
			name: "share deleted",
			revoke: func(t *testing.T, env *testEnv, alice, bob *userAndClaims, rootID, grantID uuid.UUID) {
				aggID := shareAggregateID(t, env, alice.UserID, rootID, "/team")
				r := reqWithClaims(http.MethodDelete,
					"/api/v1/shares/"+aggID.String()+"/grants/"+bob.UserID.String(), nil, alice.Claims)
				r = withURLParams(r, "shareID", aggID.String(), "userID", bob.UserID.String())
				w := httptest.NewRecorder()
				env.shareH.DeleteShareGrant(w, r)
				if w.Code != http.StatusNoContent {
					t.Fatalf("delete grant: status=%d body=%s", w.Code, w.Body.String())
				}
			},
		},
		{
			name: "write access withdrawn",
			revoke: func(t *testing.T, env *testEnv, alice, bob *userAndClaims, rootID, grantID uuid.UUID) {
				aggID := shareAggregateID(t, env, alice.UserID, rootID, "/team")
				body := bytes.NewBufferString(`{"can_write":false}`)
				r := reqWithClaims(http.MethodPatch,
					"/api/v1/shares/"+aggID.String()+"/grants/"+bob.UserID.String(), body, alice.Claims)
				r = withURLParams(r, "shareID", aggID.String(), "userID", bob.UserID.String())
				w := httptest.NewRecorder()
				env.shareH.UpdateShareGrant(w, r)
				if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
					t.Fatalf("patch grant: status=%d body=%s", w.Code, w.Body.String())
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, docServer := newOfficeEnv(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("written after revocation"))
			})
			alice, bob, rootID, grantID, token, _ := mountedOfficeDoc(t, env)

			tt.revoke(t, env, alice, bob, rootID, grantID)

			w := postOfficeCallback(t, env, token, 2 /* MustSave */, docServer.URL)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if errCode := decodeCallbackResponse(t, w.Body.Bytes()); errCode != 1 {
				t.Fatalf("callback error = %d, want 1 — the save went through after revocation", errCode)
			}
			env.drainOutbox(t)
			if got := fileContent(t, env, alice, rootID, "/team/report.docx"); got != "original" {
				t.Fatalf("file content = %q, want it untouched", got)
			}
		})
	}
}

// The legitimate case has to keep working: a still-valid mount saves back.
func TestOfficeCallback_AcceptsSaveWhileMountIsValid(t *testing.T) {
	env, docServer := newOfficeEnv(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("edited by bob"))
	})
	alice, _, rootID, _, token, _ := mountedOfficeDoc(t, env)

	w := postOfficeCallback(t, env, token, 2, docServer.URL)
	if errCode := decodeCallbackResponse(t, w.Body.Bytes()); errCode != 0 {
		t.Fatalf("callback error = %d, want 0; body=%s", errCode, w.Body.String())
	}
	env.drainOutbox(t)
	if got := fileContent(t, env, alice, rootID, "/team/report.docx"); got != "edited by bob" {
		t.Fatalf("file content = %q, want the edit to have landed", got)
	}
}
