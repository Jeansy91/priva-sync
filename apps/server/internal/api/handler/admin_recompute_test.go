package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/audit"
	"privasync/server/internal/auth"
	"privasync/server/internal/bus"
	"privasync/server/internal/config"
	"privasync/server/internal/model"
	"privasync/server/internal/quota"
)

// newAdminHandlerWithQuota builds an AdminHandler backed by a real quota
// service registered on a fresh bus — RecomputeQuota publishes
// AdminQuotaAdjusted, which only lands if a subscriber is wired.
func newAdminHandlerWithQuota(t *testing.T, env *testEnv) *AdminHandler {
	t.Helper()
	audits := audit.NewRepository(env.pool)
	q := quota.New(env.users, env.pool, nil, "http://test")
	b := bus.New(bus.Config{ReactiveWorkers: 1})
	q.Register(b)
	t.Cleanup(b.Close)
	return NewAdminHandler(env.users, audits, nil, q, config.AuthConfig{}, "http://test")
}

// RecomputeQuota heals drift between the users.used_bytes/trash_bytes
// counters and the truth in the files table. Seed a user whose counters are
// wrong, then verify the response — and the persisted row — reflect the
// recomputed (correct) totals.
func TestRecomputeQuota_HealsDriftAndReturnsNewTotals(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	adminH := newAdminHandlerWithQuota(t, env)

	target := seedUser(t, env, "recomp_"+uniqueSuffix(), "recomp_"+uniqueSuffix()+"@t.local")
	root := createTestSyncRoot(t, env, target.ID, "recomp_root_"+uniqueSuffix())
	if _, err := env.files.Upsert(ctx, &model.File{
		UserID: target.ID, SyncRootID: root.ID, Path: "/a.txt", Size: 777, MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	// Deliberately desync the cached counters from the files-table truth.
	if _, err := env.pool.Exec(ctx,
		`UPDATE users SET used_bytes = 999999, trash_bytes = 111 WHERE id = $1`, target.ID); err != nil {
		t.Fatalf("desync counters: %v", err)
	}

	admin := &auth.Claims{UserID: uuid.New(), Role: model.RoleAdmin}
	r := reqWithClaims("POST", "/api/v1/admin/users/"+target.ID.String()+"/recompute-quota", nil, admin)
	r = withURLParams(r, "userID", target.ID.String())
	w := httptest.NewRecorder()
	adminH.RecomputeQuota(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		UsedBytes  int64 `json:"used_bytes"`
		TrashBytes int64 `json:"trash_bytes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.UsedBytes != 777 {
		t.Errorf("used_bytes = %d, want 777", resp.UsedBytes)
	}
	if resp.TrashBytes != 0 {
		t.Errorf("trash_bytes = %d, want 0", resp.TrashBytes)
	}

	got, err := env.users.FindByID(ctx, target.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.UsedBytes != 777 || got.TrashBytes != 0 {
		t.Errorf("persisted counters = (%d, %d), want (777, 0)", got.UsedBytes, got.TrashBytes)
	}
}

func TestRecomputeQuota_InvalidUserID(t *testing.T) {
	env := newTestEnv(t)
	adminH := newAdminHandlerWithQuota(t, env)

	admin := &auth.Claims{UserID: uuid.New(), Role: model.RoleAdmin}
	r := reqWithClaims("POST", "/api/v1/admin/users/not-a-uuid/recompute-quota", nil, admin)
	r = withURLParams(r, "userID", "not-a-uuid")
	w := httptest.NewRecorder()
	adminH.RecomputeQuota(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != ErrInvalidUserID {
		t.Errorf("code = %v, want %q", resp["code"], ErrInvalidUserID)
	}
}

func TestRecomputeQuota_UnknownUser(t *testing.T) {
	env := newTestEnv(t)
	adminH := newAdminHandlerWithQuota(t, env)

	admin := &auth.Claims{UserID: uuid.New(), Role: model.RoleAdmin}
	unknown := uuid.New()
	r := reqWithClaims("POST", "/api/v1/admin/users/"+unknown.String()+"/recompute-quota", nil, admin)
	r = withURLParams(r, "userID", unknown.String())
	w := httptest.NewRecorder()
	adminH.RecomputeQuota(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != ErrUserNotFound {
		t.Errorf("code = %v, want %q", resp["code"], ErrUserNotFound)
	}
}
