package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Owner toggles a recipient's can_write on the share aggregate in place (no
// revoke+recreate). The endpoint returns 200 and the recipient's grant flips.
// See ADR-0015 (supersedes ADR-0014).
func TestUpdateShareGrant_OwnerTogglesCanWrite(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/x.txt", []byte("x"))
	bobGrant := createUserShare(t, env, alice, bob, root.ID, "/2024", true)
	aggID := shareAggregateID(t, env, alice.UserID, root.ID, "/2024")

	buf, _ := json.Marshal(map[string]any{"can_write": false})
	r := reqWithClaims(http.MethodPatch,
		"/api/v1/shares/"+aggID.String()+"/grants/"+bob.UserID.String(),
		bytes.NewReader(buf), alice.Claims)
	r = withURLParams(r, "shareID", aggID.String(), "userID", bob.UserID.String())
	w := httptest.NewRecorder()
	env.shareH.UpdateShareGrant(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("update grant: status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		CanWrite bool `json:"can_write"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.CanWrite {
		t.Errorf("can_write = true, want false")
	}

	// Persisted on the grant, not just echoed: reload bob's mount.
	mount, err := env.shares.FindMountByGrant(context.Background(), bobGrant, bob.UserID)
	if err != nil {
		t.Fatalf("reload mount: %v", err)
	}
	if mount.CanWrite {
		t.Errorf("persisted can_write = true, want false")
	}
}

// The permission change reaches the recipient via share_updated SSE keyed on
// the grant id (his mount anchor), so an open session flips to read-only
// without a re-fetch. See ADR-0015.
func TestUpdateShareGrant_PublishesShareUpdatedToTarget(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/x.txt", []byte("x"))
	bobGrant := createUserShare(t, env, alice, bob, root.ID, "/2024", true)
	aggID := shareAggregateID(t, env, alice.UserID, root.ID, "/2024")

	bobEvents, bobCleanup := captureEvents(t, env.hub, bob.UserID)
	defer bobCleanup()

	buf, _ := json.Marshal(map[string]any{"can_write": false})
	r := reqWithClaims(http.MethodPatch,
		"/api/v1/shares/"+aggID.String()+"/grants/"+bob.UserID.String(),
		bytes.NewReader(buf), alice.Claims)
	r = withURLParams(r, "shareID", aggID.String(), "userID", bob.UserID.String())
	w := httptest.NewRecorder()
	env.shareH.UpdateShareGrant(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("update: status=%d body=%s", w.Code, w.Body.String())
	}

	ev := waitForEvent(t, bobEvents, "share_updated", 2*time.Second)
	body := eventDataMap(t, ev)
	share, _ := body["share"].(map[string]any)
	if share == nil {
		t.Fatalf("share payload missing: %+v", body)
	}
	if got, _ := share["id"].(string); got != bobGrant.String() {
		t.Errorf("share.id = %v, want grant %s", share["id"], bobGrant)
	}
	if cw, _ := share["can_write"].(bool); cw != false {
		t.Errorf("share.can_write = %v, want false", share["can_write"])
	}
}

// A non-owner must not change a grant. Owner-scoped lookup makes this
// indistinguishable from a missing share — 404, no leak.
func TestUpdateShareGrant_RejectsNonOwner(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/2024/x.txt", []byte("x"))
	createUserShare(t, env, alice, bob, root.ID, "/2024", true)
	aggID := shareAggregateID(t, env, alice.UserID, root.ID, "/2024")

	// Bob (the recipient) tries to grant himself write access.
	buf, _ := json.Marshal(map[string]any{"can_write": true})
	r := reqWithClaims(http.MethodPatch,
		"/api/v1/shares/"+aggID.String()+"/grants/"+bob.UserID.String(),
		bytes.NewReader(buf), bob.Claims)
	r = withURLParams(r, "shareID", aggID.String(), "userID", bob.UserID.String())
	w := httptest.NewRecorder()
	env.shareH.UpdateShareGrant(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-owner, got %d body=%s", w.Code, w.Body.String())
	}
}

// An unknown share id yields 404, same as a non-owner.
func TestUpdateShareGrant_UnknownShare(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())

	buf, _ := json.Marshal(map[string]any{"can_write": false})
	r := reqWithClaims(http.MethodPatch,
		"/api/v1/shares/"+uuid.New().String()+"/grants/"+uuid.New().String(),
		bytes.NewReader(buf), alice.Claims)
	r = withURLParams(r, "shareID", uuid.New().String(), "userID", uuid.New().String())
	w := httptest.NewRecorder()
	env.shareH.UpdateShareGrant(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown share, got %d body=%s", w.Code, w.Body.String())
	}
}
