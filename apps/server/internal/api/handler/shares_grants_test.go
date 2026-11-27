package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/model"
)

// Tracer bullet for ADR-0015 slice 1: a user-share aggregate carries its
// recipients as share_grants rows. AddGrant persists one, ListGrants reads it
// back with its per-recipient can_write. Proves the migration table + model +
// repo path end-to-end.
func TestShareGrants_AddAndList(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	carol := makeUser(t, env, "carol_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/x.txt", []byte("x"))

	share := shareAggregateID(t, env, alice.UserID, root.ID, "/dir")

	grant, err := env.shares.AddGrant(ctx, share, carol.UserID, true, nil)
	if err != nil {
		t.Fatalf("add grant: %v", err)
	}
	if grant.TargetUserID != carol.UserID {
		t.Errorf("grant target = %v, want %v", grant.TargetUserID, carol.UserID)
	}
	if !grant.CanWrite {
		t.Errorf("grant can_write = false, want true")
	}

	grants, err := env.shares.ListGrants(ctx, share)
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	got := findGrant(grants, carol.UserID)
	if got == nil {
		t.Fatalf("carol grant not listed; got %d grants", len(grants))
	}
	if !got.CanWrite {
		t.Errorf("listed can_write = false, want true")
	}
}

// Re-adding the same recipient is an upsert, not a duplicate: the dialog can
// call AddGrant idempotently and the permission/expiry are overwritten.
func TestShareGrants_AddIsUpsert(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	carol := makeUser(t, env, "carol_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/x.txt", []byte("x"))
	share := shareAggregateID(t, env, alice.UserID, root.ID, "/dir")

	if _, err := env.shares.AddGrant(ctx, share, carol.UserID, false, nil); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if _, err := env.shares.AddGrant(ctx, share, carol.UserID, true, nil); err != nil {
		t.Fatalf("re-add: %v", err)
	}

	grants, _ := env.shares.ListGrants(ctx, share)
	if n := countGrants(grants, carol.UserID); n != 1 {
		t.Fatalf("carol grant count = %d, want 1 (upsert, not duplicate)", n)
	}
	if g := findGrant(grants, carol.UserID); g == nil || !g.CanWrite {
		t.Errorf("upsert did not flip can_write to true")
	}
}

// UpdateGrant flips a recipient's can_write in place; RemoveGrant detaches them.
func TestShareGrants_UpdateAndRemove(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	carol := makeUser(t, env, "carol_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/x.txt", []byte("x"))
	share := shareAggregateID(t, env, alice.UserID, root.ID, "/dir")
	if _, err := env.shares.AddGrant(ctx, share, carol.UserID, true, nil); err != nil {
		t.Fatalf("add: %v", err)
	}

	updated, err := env.shares.UpdateGrant(ctx, share, carol.UserID, false, nil)
	if err != nil {
		t.Fatalf("update grant: %v", err)
	}
	if updated.CanWrite {
		t.Errorf("update can_write = true, want false")
	}

	if _, err := env.shares.RemoveGrant(ctx, share, carol.UserID); err != nil {
		t.Fatalf("remove grant: %v", err)
	}
	grants, _ := env.shares.ListGrants(ctx, share)
	if findGrant(grants, carol.UserID) != nil {
		t.Errorf("carol grant still present after remove")
	}
}

// The headline of ADR-0015: sharing a path to a second user does NOT create a
// second share — it reuses the one aggregate and adds a grant. So both
// CreateShare calls return the same share id and the aggregate carries two
// recipient grants.
func TestCreateShare_SecondUserReusesAggregate(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	alice := makeUser(t, env, "alice_"+uniqueSuffix())
	bob := makeUser(t, env, "bob_"+uniqueSuffix())
	carol := makeUser(t, env, "carol_"+uniqueSuffix())
	root := createTestSyncRoot(t, env, alice.UserID, "ph_"+uniqueSuffix())
	uploadFileAs(t, env, alice.Claims, root.ID, "/dir/x.txt", []byte("x"))

	bobGrant := createUserShare(t, env, alice, bob, root.ID, "/dir", false)
	carolGrant := createUserShare(t, env, alice, carol, root.ID, "/dir", true)

	if bobGrant == carolGrant {
		t.Fatalf("two recipients must get distinct grant ids, both %v", bobGrant)
	}

	// Both recipients hang off the SAME aggregate — one editable share per path.
	agg := shareAggregateID(t, env, alice.UserID, root.ID, "/dir")
	grants, err := env.shares.ListGrants(ctx, agg)
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if len(grants) != 2 {
		t.Fatalf("aggregate grant count = %d, want 2 (one share, two users)", len(grants))
	}
	if findGrant(grants, bob.UserID) == nil {
		t.Errorf("bob grant missing")
	}
	cg := findGrant(grants, carol.UserID)
	if cg == nil {
		t.Fatalf("carol grant missing")
	}
	if !cg.CanWrite {
		t.Errorf("carol can_write = false, want true")
	}
}

func findGrant(grants []model.ShareGrant, target uuid.UUID) *model.ShareGrant {
	for i := range grants {
		if grants[i].TargetUserID == target {
			return &grants[i]
		}
	}
	return nil
}

func countGrants(grants []model.ShareGrant, target uuid.UUID) int {
	n := 0
	for i := range grants {
		if grants[i].TargetUserID == target {
			n++
		}
	}
	return n
}
