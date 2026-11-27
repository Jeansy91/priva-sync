package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func setUserAvatarPath(t *testing.T, env *testEnv, userID uuid.UUID, path string) {
	t.Helper()
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE users SET avatar_path = $1 WHERE id = $2`, path, userID); err != nil {
		t.Fatalf("set avatar path: %v", err)
	}
}

type syncRootListEntry struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Path  string    `json:"path"`
	Owner struct {
		ID        uuid.UUID `json:"id"`
		Username  string    `json:"username"`
		AvatarURL *string   `json:"avatar_url"`
	} `json:"owner"`
}

// Owner block must be present so the web detail panel can show who owns
// each sync root without a second round-trip. See ADR-0009.
func TestListSyncRoots_IncludesOwner(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	createTestSyncRoot(t, env, claims.UserID, "owner_"+uniqueSuffix())

	r := reqWithClaims(http.MethodGet, "/api/v1/sync-roots", nil, claims)
	w := httptest.NewRecorder()
	env.handler.ListSyncRoots(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var got []syncRootListEntry
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 root, got %d", len(got))
	}
	if got[0].Owner.ID != claims.UserID {
		t.Fatalf("owner id mismatch: got %s want %s", got[0].Owner.ID, claims.UserID)
	}
	if got[0].Owner.Username == "" {
		t.Fatalf("owner username empty")
	}
	if got[0].Owner.AvatarURL != nil {
		t.Fatalf("owner avatar_url should be nil when no avatar uploaded, got %v", *got[0].Owner.AvatarURL)
	}
}

// When the owning user uploaded an avatar, the response must point at the
// avatar download endpoint so the web client can render a real image.
func TestListSyncRoots_OwnerAvatarURLWhenAvatarSet(t *testing.T) {
	env := newTestEnv(t)
	claims := createTestUser(t, env)
	createTestSyncRoot(t, env, claims.UserID, "avatar_"+uniqueSuffix())

	setUserAvatarPath(t, env, claims.UserID, "avatars/test.png")

	r := reqWithClaims(http.MethodGet, "/api/v1/sync-roots", nil, claims)
	w := httptest.NewRecorder()
	env.handler.ListSyncRoots(w, r)
	var got []syncRootListEntry
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 root, got %d", len(got))
	}
	want := "/api/v1/auth/avatar/" + claims.UserID.String()
	if got[0].Owner.AvatarURL == nil || *got[0].Owner.AvatarURL != want {
		t.Fatalf("avatar_url mismatch: got %v want %s", got[0].Owner.AvatarURL, want)
	}
}
