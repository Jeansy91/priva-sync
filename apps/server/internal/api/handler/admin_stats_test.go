package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/auth"
	"privasync/server/internal/config"
	"privasync/server/internal/model"
	"privasync/server/internal/version"
)

func TestDiskUsageZero_ReturnsZeroValues(t *testing.T) {
	free, total, err := diskUsageZero("/any/path")
	if err != nil {
		t.Fatalf("diskUsageZero: %v", err)
	}
	if free != 0 || total != 0 {
		t.Errorf("diskUsageZero = (%d, %d), want (0, 0)", free, total)
	}
}

func newAdminStatsHandlerForTest(env *testEnv) *AdminStatsHandler {
	cfg := config.AuthConfig{MaxLoginAttempts: 5, LoginLockoutWindow: 15 * time.Minute}
	h := NewAdminStatsHandler(env.users, env.pool, env.storDir, cfg)
	h.DiskUsage = func(string) (free, total int64, err error) { return 1000, 2000, nil }
	h.StartedAt = time.Now().Add(-time.Hour)
	return h
}

func TestAdminStats_VersionField(t *testing.T) {
	env := newTestEnv(t)
	h := newAdminStatsHandlerForTest(env)

	w := httptest.NewRecorder()
	r := reqWithClaims("GET", "/api/v1/admin/stats", nil, &auth.Claims{Role: "admin"})

	h.GetStats(w, r)

	if w.Code != 200 {
		t.Fatalf("status: got %d want 200, body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got, want := body["version"], version.Version; got != want {
		t.Errorf("version: got %v want %q", got, want)
	}
}

func TestAdminStats_UserCounts(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Baseline-Snapshot: bestehende User aus parallelen Tests zaehlen.
	baseUsers, err := env.users.List(ctx)
	if err != nil {
		t.Fatalf("baseline list: %v", err)
	}
	baseTotal := len(baseUsers)
	baseActive, baseAdmin, baseTOTP := 0, 0, 0
	for _, u := range baseUsers {
		if u.IsActive {
			baseActive++
		}
		if u.Role == model.RoleAdmin {
			baseAdmin++
		}
		if u.TOTPEnabled {
			baseTOTP++
		}
	}

	// Seed: 1 admin, 1 user mit TOTP, 1 deaktivierter user, 1 locked user.
	admin := seedUser(t, env, "stats_admin_"+uniqueSuffix(), "stats_admin_"+uniqueSuffix()+"@t.local")
	if err := env.users.SetRole(ctx, admin.ID, model.RoleAdmin); err != nil {
		t.Fatalf("set role: %v", err)
	}
	totpUser := seedUser(t, env, "stats_totp_"+uniqueSuffix(), "stats_totp_"+uniqueSuffix()+"@t.local")
	if err := env.users.UpdateTOTP(ctx, totpUser.ID, ptr("secret"), true); err != nil {
		t.Fatalf("totp: %v", err)
	}
	inactive := seedUser(t, env, "stats_inactive_"+uniqueSuffix(), "stats_inactive_"+uniqueSuffix()+"@t.local")
	if err := env.users.SetActive(ctx, inactive.ID, false); err != nil {
		t.Fatalf("inactive: %v", err)
	}
	locked := seedUser(t, env, "stats_locked_"+uniqueSuffix(), "stats_locked_"+uniqueSuffix()+"@t.local")
	for i := 0; i < 5; i++ {
		if err := env.users.RecordLoginAttempt(ctx, locked.Username, "127.0.0.1", false); err != nil {
			t.Fatalf("record attempt: %v", err)
		}
	}

	h := newAdminStatsHandlerForTest(env)
	w := httptest.NewRecorder()
	r := reqWithClaims("GET", "/api/v1/admin/stats", nil, &auth.Claims{Role: "admin"})
	h.GetStats(w, r)

	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	users, ok := body["users"].(map[string]any)
	if !ok {
		t.Fatalf("missing users object: %v", body)
	}
	assertNum(t, users, "total", float64(baseTotal+4))
	assertNum(t, users, "active", float64(baseActive+3)) // admin, totp, locked are active; inactive not
	assertNum(t, users, "admin", float64(baseAdmin+1))
	assertNum(t, users, "totp", float64(baseTOTP+1))
	assertNum(t, users, "locked", float64(1))
}

func assertNum(t *testing.T, m map[string]any, key string, want float64) {
	t.Helper()
	got, ok := m[key].(float64)
	if !ok {
		t.Errorf("%s: not a number (%T %v)", key, m[key], m[key])
		return
	}
	if got != want {
		t.Errorf("%s: got %v want %v", key, got, want)
	}
}

func ptr[T any](v T) *T { return &v }

func TestAdminStats_FilesAndSyncRoots(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	base := decodeStats(t, env)

	owner := seedUser(t, env, "stats_files_"+uniqueSuffix(), "stats_files_"+uniqueSuffix()+"@t.local")
	root := createTestSyncRoot(t, env, owner.ID, "stats_root_"+uniqueSuffix())

	// 2 live files, 1 trashed.
	for i, name := range []string{"a.txt", "b.txt"} {
		_, err := env.files.Upsert(ctx, &model.File{
			UserID:     owner.ID,
			SyncRootID: root.ID,
			Path:       name,
			Size:       int64(100 + i*50),
			MTime:      time.Now(),
		})
		if err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
	}
	trashed, err := env.files.Upsert(ctx, &model.File{
		UserID: owner.ID, SyncRootID: root.ID, Path: "c.txt",
		Size: 200, MTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("upsert c: %v", err)
	}
	if _, err := env.trash.SoftDelete(ctx, root.ID, trashed.Path, "trash/c.txt", owner.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	after := decodeStats(t, env)

	syncRoots, ok := after["sync_roots"].(map[string]any)
	if !ok {
		t.Fatalf("missing sync_roots: %v", after)
	}
	assertDelta(t, base, "sync_roots", "total", syncRoots, 1)

	files, ok := after["files"].(map[string]any)
	if !ok {
		t.Fatalf("missing files: %v", after)
	}
	assertDelta(t, base, "files", "live", files, 2)
	assertDelta(t, base, "files", "trash", files, 1)
}

func decodeStats(t *testing.T, env *testEnv) map[string]any {
	t.Helper()
	h := newAdminStatsHandlerForTest(env)
	w := httptest.NewRecorder()
	r := reqWithClaims("GET", "/api/v1/admin/stats", nil, &auth.Claims{Role: "admin"})
	h.GetStats(w, r)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func TestAdminStats_Health(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	base := decodeStats(t, env)

	gid := uuid.New()
	_, err := env.pool.Exec(ctx, `
		INSERT INTO outbox (group_id, seq, kind, payload) VALUES ($1, 1, 'test', '{}'::jsonb)
	`, gid)
	if err != nil {
		t.Fatalf("insert outbox: %v", err)
	}

	after := decodeStats(t, env)
	health, ok := after["health"].(map[string]any)
	if !ok {
		t.Fatalf("missing health: %v", after)
	}
	assertDelta(t, base, "health", "outbox_pending", health, 1)

	if got, _ := health["uptime_seconds"].(float64); got < 3600 {
		t.Errorf("uptime_seconds: got %v want >= 3600", got)
	}
	if got, _ := health["db_size_bytes"].(float64); got <= 0 {
		t.Errorf("db_size_bytes: got %v want > 0", got)
	}
}

func TestAdminStats_Activity24h(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	base := decodeStats(t, env)

	u := seedUser(t, env, "stats_act_"+uniqueSuffix(), "stats_act_"+uniqueSuffix()+"@t.local")

	insert := func(evt string, ageHours int) {
		t.Helper()
		_, err := env.pool.Exec(ctx, `
			INSERT INTO audit_log (user_id, event_type, created_at)
			VALUES ($1, $2, NOW() - make_interval(hours => $3))
		`, u.ID, evt, ageHours)
		if err != nil {
			t.Fatalf("insert audit: %v", err)
		}
	}
	insert("auth.login.success", 1)
	insert("auth.login.success", 2)
	insert("auth.login.fail", 3)
	insert("auth.login.success", 48) // out of window

	after := decodeStats(t, env)
	act, ok := after["activity_24h"].(map[string]any)
	if !ok {
		t.Fatalf("missing activity_24h: %v", after)
	}
	assertDelta(t, base, "activity_24h", "logins", act, 2)
	assertDelta(t, base, "activity_24h", "failed_logins", act, 1)
}

func TestAdminStats_QuotaHealth(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	base := decodeStats(t, env)

	makeUser := func(state string) {
		u := seedUser(t, env, "stats_q_"+uniqueSuffix(), "stats_q_"+uniqueSuffix()+"@t.local")
		if _, err := env.pool.Exec(ctx, `UPDATE users SET quota_state = $1 WHERE id = $2`, state, u.ID); err != nil {
			t.Fatalf("set quota_state: %v", err)
		}
	}
	makeUser("warning80")
	makeUser("warning80")
	makeUser("critical95")

	after := decodeStats(t, env)
	qh, ok := after["quota_health"].(map[string]any)
	if !ok {
		t.Fatalf("missing quota_health: %v", after)
	}
	assertDelta(t, base, "quota_health", "warning80", qh, 2)
	assertDelta(t, base, "quota_health", "critical95", qh, 1)
}

func TestAdminStats_Storage(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	base := decodeStats(t, env)

	u := seedUser(t, env, "stats_st_"+uniqueSuffix(), "stats_st_"+uniqueSuffix()+"@t.local")
	if err := env.users.SetQuota(ctx, u.ID, 1024*1024); err != nil {
		t.Fatalf("quota: %v", err)
	}
	if _, err := env.pool.Exec(ctx,
		`UPDATE users SET used_bytes = 500, trash_bytes = 200 WHERE id = $1`, u.ID,
	); err != nil {
		t.Fatalf("bytes: %v", err)
	}

	after := decodeStats(t, env)
	storage, ok := after["storage"].(map[string]any)
	if !ok {
		t.Fatalf("missing storage: %v", after)
	}
	assertDelta(t, base, "storage", "used_bytes", storage, 500)
	assertDelta(t, base, "storage", "trash_bytes", storage, 200)
	assertDelta(t, base, "storage", "quota_bytes", storage, 1024*1024)
	if got, _ := storage["disk_free_bytes"].(float64); got != 1000 {
		t.Errorf("disk_free_bytes: got %v want 1000", got)
	}
	if got, _ := storage["disk_total_bytes"].(float64); got != 2000 {
		t.Errorf("disk_total_bytes: got %v want 2000", got)
	}
}

func TestAdminStats_Shares(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	base := decodeStats(t, env)

	owner := seedUser(t, env, "stats_sh_"+uniqueSuffix(), "stats_sh_"+uniqueSuffix()+"@t.local")
	root := createTestSyncRoot(t, env, owner.ID, "sh_root_"+uniqueSuffix())

	insertShare := func(token string, expired bool, pwd bool) {
		t.Helper()
		var exp any
		if expired {
			exp = time.Now().Add(-1 * time.Hour)
		} else {
			exp = time.Now().Add(24 * time.Hour)
		}
		var pwdHash any
		if pwd {
			pwdHash = "hashed"
		}
		_, err := env.pool.Exec(ctx, `
			INSERT INTO shares (user_id, sync_root_id, path, share_type, token, expires_at, password_hash, can_write)
			VALUES ($1, $2, '/', 'public', $3, $4, $5, FALSE)
		`, owner.ID, root.ID, token, exp, pwdHash)
		if err != nil {
			t.Fatalf("insert share: %v", err)
		}
	}
	insertShare("tok_active_"+uniqueSuffix(), false, false)
	insertShare("tok_pwd_"+uniqueSuffix(), false, true)
	insertShare("tok_expired_"+uniqueSuffix(), true, false)

	after := decodeStats(t, env)
	shares, ok := after["shares"].(map[string]any)
	if !ok {
		t.Fatalf("missing shares: %v", after)
	}
	assertDelta(t, base, "shares", "total", shares, 3)
	assertDelta(t, base, "shares", "active", shares, 2)
	assertDelta(t, base, "shares", "password_protected", shares, 1)
}

func TestAdminStats_Conflicts(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	base := decodeStats(t, env)

	owner := seedUser(t, env, "stats_cf_"+uniqueSuffix(), "stats_cf_"+uniqueSuffix()+"@t.local")
	root := createTestSyncRoot(t, env, owner.ID, "cf_root_"+uniqueSuffix())
	f, err := env.files.Upsert(ctx, &model.File{
		UserID: owner.ID, SyncRootID: root.ID, Path: "x.txt",
		Size: 10, MTime: time.Now(),
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := env.conflicts.CreateConflict(ctx, f.ID, "x.conflict-1.txt", nil, nil); err != nil {
		t.Fatalf("conflict: %v", err)
	}

	after := decodeStats(t, env)
	files, _ := after["files"].(map[string]any)
	assertDelta(t, base, "files", "conflicts", files, 1)
}

func assertDelta(t *testing.T, base map[string]any, group, key string, after map[string]any, want float64) {
	t.Helper()
	baseGroup, _ := base[group].(map[string]any)
	var baseVal float64
	if baseGroup != nil {
		baseVal, _ = baseGroup[key].(float64)
	}
	gotVal, ok := after[key].(float64)
	if !ok {
		t.Errorf("%s.%s: not a number (%T %v)", group, key, after[key], after[key])
		return
	}
	if delta := gotVal - baseVal; delta != want {
		t.Errorf("%s.%s: delta got %v want %v (base=%v after=%v)", group, key, delta, want, baseVal, gotVal)
	}
}

func uniqueSuffix() string {
	return uuid.NewString()[:8]
}
