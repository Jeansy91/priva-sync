package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestListShares_ReturnsSharesOwnedByCaller(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	carol := makeUser(t, env, "carol_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/x.txt", []byte("x"))
	shareID := shareAggregateID(t, env, alice.UserID, root.ID, "/dir")
	if _, err := env.shares.AddGrant(context.Background(), shareID, carol.UserID, true, nil); err != nil {
		t.Fatalf("AddGrant: %v", err)
	}

	r := reqWithClaims("GET", "/shares", nil, alice.Claims)
	w := httptest.NewRecorder()
	env.shareH.ListShares(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("ListShares returned no shares for the owning user")
	}
}

func TestListShares_EmptyForUserWithNoShares(t *testing.T) {
	env := newTestEnv(t)
	lonely := makeUser(t, env, "lonely_"+uniqueSuffix())

	r := reqWithClaims("GET", "/shares", nil, lonely.Claims)
	w := httptest.NewRecorder()
	env.shareH.ListShares(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp []map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp) != 0 {
		t.Fatalf("ListShares = %d entries, want 0 for a user with no shares", len(resp))
	}
}

func TestDeleteShareGrant_RemovesGrant(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	carol := makeUser(t, env, "carol_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/x.txt", []byte("x"))
	shareID := shareAggregateID(t, env, alice.UserID, root.ID, "/dir")
	if _, err := env.shares.AddGrant(context.Background(), shareID, carol.UserID, true, nil); err != nil {
		t.Fatalf("AddGrant: %v", err)
	}

	r := reqWithClaims("DELETE", "/shares/"+shareID.String()+"/grants/"+carol.UserID.String(), nil, alice.Claims)
	r = withURLParams(r, "shareID", shareID.String(), "userID", carol.UserID.String())
	w := httptest.NewRecorder()
	env.shareH.DeleteShareGrant(w, r)

	if w.Code != 200 && w.Code != 204 {
		t.Fatalf("status = %d, want 200 or 204; body=%s", w.Code, w.Body.String())
	}
	grants, err := env.shares.ListGrants(context.Background(), shareID)
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if findGrant(grants, carol.UserID) != nil {
		t.Fatal("grant for carol still present after DeleteShareGrant")
	}
}

func TestDeleteShareGrant_NotOwnerRejected(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	carol := makeUser(t, env, "carol_"+uniqueSuffix())
	mallory := makeUser(t, env, "mallory_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/x.txt", []byte("x"))
	shareID := shareAggregateID(t, env, alice.UserID, root.ID, "/dir")
	if _, err := env.shares.AddGrant(context.Background(), shareID, carol.UserID, true, nil); err != nil {
		t.Fatalf("AddGrant: %v", err)
	}

	r := reqWithClaims("DELETE", "/shares/"+shareID.String()+"/grants/"+carol.UserID.String(), nil, mallory.Claims)
	r = withURLParams(r, "shareID", shareID.String(), "userID", carol.UserID.String())
	w := httptest.NewRecorder()
	env.shareH.DeleteShareGrant(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404 (share not owned by caller); body=%s", w.Code, w.Body.String())
	}
	grants, err := env.shares.ListGrants(context.Background(), shareID)
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if findGrant(grants, carol.UserID) == nil {
		t.Fatal("grant was removed despite the caller not owning the share")
	}
}

func TestDeleteShareGrant_UnknownShareNotFound(t *testing.T) {
	env := newTestEnv(t)
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	carol := makeUser(t, env, "carol_"+uniqueSuffix())

	r := reqWithClaims("DELETE", "/shares/00000000-0000-0000-0000-000000000000/grants/"+carol.UserID.String(), nil, alice.Claims)
	r = withURLParams(r, "shareID", "00000000-0000-0000-0000-000000000000", "userID", carol.UserID.String())
	w := httptest.NewRecorder()
	env.shareH.DeleteShareGrant(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}
