package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"privasync/server/internal/audit"
	"privasync/server/internal/auth"
	"privasync/server/internal/config"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

// Upsert inserts a new client row on first call and updates the same row
// (matched by user_id + device_id) on a second call instead of creating a
// duplicate. Mutable fields (name, os_version, client_version, last_ip,
// last_seen) reflect the most recent call.
func TestClientRepository_UpsertCreatesAndUpdates(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	user := seedUser(t, env, "client_"+uniqueSuffix(), "client_"+uniqueSuffix()+"@t.local")

	clients := repository.NewClientRepository(env.pool)

	osV1 := "11.0.26220"
	cliV1 := "0.4.0"
	ip1 := "10.0.0.1"
	c1, err := clients.Upsert(ctx, repository.UpsertClientInput{
		UserID:        user.ID,
		DeviceID:      "dev-abc",
		Name:          "WORK-LAPTOP",
		Platform:      "windows",
		OSVersion:     &osV1,
		ClientVersion: &cliV1,
		LastIP:        &ip1,
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if c1.ID.String() == "" {
		t.Fatal("first upsert: empty id")
	}
	if c1.LastSeen == nil {
		t.Fatal("first upsert: last_seen not set")
	}
	firstSeen := *c1.LastSeen

	time.Sleep(10 * time.Millisecond)

	osV2 := "11.0.26300"
	cliV2 := "0.5.0"
	ip2 := "10.0.0.2"
	c2, err := clients.Upsert(ctx, repository.UpsertClientInput{
		UserID:        user.ID,
		DeviceID:      "dev-abc",
		Name:          "WORK-LAPTOP-RENAMED",
		Platform:      "windows",
		OSVersion:     &osV2,
		ClientVersion: &cliV2,
		LastIP:        &ip2,
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if c2.ID != c1.ID {
		t.Fatalf("upsert created duplicate row: got %s want %s", c2.ID, c1.ID)
	}
	if c2.Name != "WORK-LAPTOP-RENAMED" {
		t.Errorf("name not updated: got %q", c2.Name)
	}
	if c2.OSVersion == nil || *c2.OSVersion != osV2 {
		t.Errorf("os_version not updated: %v", c2.OSVersion)
	}
	if c2.ClientVersion == nil || *c2.ClientVersion != cliV2 {
		t.Errorf("client_version not updated: %v", c2.ClientVersion)
	}
	if c2.LastIP == nil || *c2.LastIP != ip2 {
		t.Errorf("last_ip not updated: %v", c2.LastIP)
	}
	if c2.LastSeen == nil || !c2.LastSeen.After(firstSeen) {
		t.Errorf("last_seen not advanced: first=%v second=%v", firstSeen, c2.LastSeen)
	}
}

// ListAll joins username and orders by last_seen DESC NULLS LAST so the most
// recently active client shows up first in the admin overview.
func TestClientRepository_ListAllJoinsUserAndSorts(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	clients := repository.NewClientRepository(env.pool)

	// Baseline so the assertion survives clients seeded by other tests.
	base, err := clients.ListAll(ctx)
	if err != nil {
		t.Fatalf("baseline list: %v", err)
	}
	baseCount := len(base)

	uA := seedUser(t, env, "lst_a_"+uniqueSuffix(), "lst_a_"+uniqueSuffix()+"@t.local")
	uB := seedUser(t, env, "lst_b_"+uniqueSuffix(), "lst_b_"+uniqueSuffix()+"@t.local")

	// Older first, then newer second — newer must come first in the result.
	older, err := clients.Upsert(ctx, repository.UpsertClientInput{
		UserID: uA.ID, DeviceID: "dev-old", Name: "A-PC", Platform: "linux",
	})
	if err != nil {
		t.Fatalf("upsert older: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	newer, err := clients.Upsert(ctx, repository.UpsertClientInput{
		UserID: uB.ID, DeviceID: "dev-new", Name: "B-PC", Platform: "macos",
	})
	if err != nil {
		t.Fatalf("upsert newer: %v", err)
	}

	out, err := clients.ListAll(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != baseCount+2 {
		t.Fatalf("list size: got %d want %d", len(out), baseCount+2)
	}
	// The two seeded rows must appear consecutively at the top in newer→older
	// order (other tests' rows may have older last_seen but never newer).
	if out[0].ID != newer.ID {
		t.Errorf("expected newer first: got id=%s name=%s", out[0].ID, out[0].Name)
	}
	if out[0].Username != uB.Username {
		t.Errorf("username join: got %q want %q", out[0].Username, uB.Username)
	}
	if out[1].ID != older.ID {
		t.Errorf("expected older second: got id=%s name=%s", out[1].ID, out[1].Name)
	}
	if out[1].Username != uA.Username {
		t.Errorf("username join: got %q want %q", out[1].Username, uA.Username)
	}
}

// A successful Login that carries a client identification block must create
// (or refresh) a clients row for that user/device. This is the contract the
// admin overview relies on — sign in = appear in the list.
func TestAuth_Login_UpsertsClientRow(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	clients := repository.NewClientRepository(env.pool)

	hash, err := auth.HashPassword("secret-pw-1")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := env.users.Create(ctx, "auth_cli_"+uniqueSuffix(), "auth_cli_"+uniqueSuffix()+"@t.local", hash, model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	registerUserCleanup(t, env.pool, u.ID)

	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, config.AuthConfig{
		JWTSecret:          "test-secret",
		AccessTokenTTL:     5 * time.Minute,
		RefreshTokenTTL:    1 * time.Hour,
		MaxLoginAttempts:   10,
		LoginLockoutWindow: time.Hour,
	}, "http://localhost", "PrivaSync")
	h.clients = clients

	body, _ := json.Marshal(map[string]any{
		"username": u.Username,
		"password": "secret-pw-1",
		"client": map[string]any{
			"device_id":      "dev-login-1",
			"device_name":    "TEST-PC",
			"platform":       "windows",
			"os_version":     "11.0.26220",
			"client_version": "0.4.2",
		},
	})
	r := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	r.RemoteAddr = "10.0.0.55:1234"
	w := httptest.NewRecorder()
	h.Login(w, r)

	if w.Code != 200 {
		t.Fatalf("login status %d: %s", w.Code, w.Body.String())
	}

	all, err := clients.ListAll(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var got *model.ClientWithUser
	for i := range all {
		if all[i].UserID == u.ID && all[i].DeviceID == "dev-login-1" {
			got = &all[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("login did not upsert a client row for user=%s device=dev-login-1", u.ID)
	}
	if got.Name != "TEST-PC" {
		t.Errorf("name: got %q want TEST-PC", got.Name)
	}
	if got.OSVersion == nil || *got.OSVersion != "11.0.26220" {
		t.Errorf("os_version: %v", got.OSVersion)
	}
	if got.ClientVersion == nil || *got.ClientVersion != "0.4.2" {
		t.Errorf("client_version: %v", got.ClientVersion)
	}
	if got.LastIP == nil || *got.LastIP != "10.0.0.55" {
		t.Errorf("last_ip: %v", got.LastIP)
	}
}

// GET /api/v1/admin/clients returns every client row as JSON with the joined
// username, sorted by last_seen DESC. Drives the admin Geraete-Uebersicht.
func TestAdminClientsHandler_List(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	clients := repository.NewClientRepository(env.pool)

	u := seedUser(t, env, "adm_cli_"+uniqueSuffix(), "adm_cli_"+uniqueSuffix()+"@t.local")
	osV := "11.0.26220"
	cliV := "0.4.2"
	ip := "10.0.0.99"
	_, err := clients.Upsert(ctx, repository.UpsertClientInput{
		UserID: u.ID, DeviceID: "dev-admin-1", Name: "ADM-PC", Platform: "windows",
		OSVersion: &osV, ClientVersion: &cliV, LastIP: &ip,
	})
	if err != nil {
		t.Fatalf("seed client: %v", err)
	}

	h := NewAdminClientsHandler(clients)
	r := reqWithClaims("GET", "/api/v1/admin/clients", nil, &auth.Claims{Role: "admin"})
	w := httptest.NewRecorder()
	h.List(w, r)

	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Clients []map[string]any `json:"clients"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var got map[string]any
	for _, c := range body.Clients {
		if c["device_id"] == "dev-admin-1" {
			got = c
			break
		}
	}
	if got == nil {
		t.Fatalf("seeded client not in response: %s", w.Body.String())
	}
	if got["username"] != u.Username {
		t.Errorf("username: %v want %s", got["username"], u.Username)
	}
	if got["name"] != "ADM-PC" {
		t.Errorf("name: %v", got["name"])
	}
	if got["platform"] != "windows" {
		t.Errorf("platform: %v", got["platform"])
	}
	if got["os_version"] != osV {
		t.Errorf("os_version: %v", got["os_version"])
	}
	if got["client_version"] != cliV {
		t.Errorf("client_version: %v", got["client_version"])
	}
	if got["last_ip"] != ip {
		t.Errorf("last_ip: %v", got["last_ip"])
	}
	if _, ok := got["last_seen"].(string); !ok {
		t.Errorf("last_seen missing/non-string: %v", got["last_seen"])
	}
}

// Refresh that carries the X-Client-Device header must bump last_seen and
// last_ip on the matching clients row so the admin overview reflects ongoing
// activity, not just the moment of login.
func TestAuth_Refresh_TouchesClientRow(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	clients := repository.NewClientRepository(env.pool)

	hash, err := auth.HashPassword("pw")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := env.users.Create(ctx, "rfr_"+uniqueSuffix(), "rfr_"+uniqueSuffix()+"@t.local", hash, model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	registerUserCleanup(t, env.pool, u.ID)

	osV := "11.0.1"
	cliV := "0.4.0"
	ip0 := "10.0.0.1"
	seeded, err := clients.Upsert(ctx, repository.UpsertClientInput{
		UserID: u.ID, DeviceID: "dev-rfr", Name: "PC", Platform: "windows",
		OSVersion: &osV, ClientVersion: &cliV, LastIP: &ip0,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	seededSeen := *seeded.LastSeen

	// Plant a refresh token directly so we don't need to drive Login first.
	rawRefresh := "test-refresh-" + uniqueSuffix()
	if err := env.users.CreateRefreshToken(ctx, u.ID, rawRefresh, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create refresh: %v", err)
	}

	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, config.AuthConfig{
		JWTSecret:       "s",
		AccessTokenTTL:  5 * time.Minute,
		RefreshTokenTTL: time.Hour,
	}, "http://localhost", "PrivaSync").WithClients(clients)

	time.Sleep(20 * time.Millisecond)

	r := httptest.NewRequest("POST", "/api/v1/auth/refresh", nil)
	r.AddCookie(&http.Cookie{Name: "refresh_token", Value: rawRefresh})
	r.Header.Set("X-Client-Device", "dev-rfr")
	r.RemoteAddr = "10.0.0.222:443"
	w := httptest.NewRecorder()
	h.Refresh(w, r)

	if w.Code != 200 {
		t.Fatalf("refresh status %d: %s", w.Code, w.Body.String())
	}
	all, err := clients.ListAll(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var after *model.ClientWithUser
	for i := range all {
		if all[i].ID == seeded.ID {
			after = &all[i]
			break
		}
	}
	if after == nil {
		t.Fatal("seeded client row gone after refresh")
	}
	if after.LastSeen == nil || !after.LastSeen.After(seededSeen) {
		t.Errorf("last_seen not advanced: before=%v after=%v", seededSeen, after.LastSeen)
	}
	if after.LastIP == nil || *after.LastIP != "10.0.0.222" {
		t.Errorf("last_ip not updated: %v", after.LastIP)
	}
}

// A long-lived desktop session refreshes for weeks without ever logging in
// again, so the identification it sends on refresh -- not just the device
// header -- is what keeps client_version truthful after an in-place update,
// and what lets a session older than the client registry register at all.
func TestAuth_Refresh_UpsertsFromIdentificationBody(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	clients := repository.NewClientRepository(env.pool)

	hash, err := auth.HashPassword("pw")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := env.users.Create(ctx, "rfu_"+uniqueSuffix(), "rfu_"+uniqueSuffix()+"@t.local", hash, model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	registerUserCleanup(t, env.pool, u.ID)

	h := NewAuthHandler(env.users, audit.NewRepository(env.pool), env.storage, nil, config.AuthConfig{
		JWTSecret:       "s",
		AccessTokenTTL:  5 * time.Minute,
		RefreshTokenTTL: time.Hour,
	}, "http://localhost", "PrivaSync").WithClients(clients)

	refreshOnce := func(version string) {
		t.Helper()
		rawRefresh := "test-refresh-" + uniqueSuffix()
		if err := env.users.CreateRefreshToken(ctx, u.ID, rawRefresh, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("create refresh: %v", err)
		}
		body, _ := json.Marshal(map[string]any{
			"client": map[string]any{
				"device_id":      "dev-rfu",
				"device_name":    "RFU-PC",
				"platform":       "windows",
				"os_version":     "11.0.26220",
				"client_version": version,
			},
		})
		r := httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewReader(body))
		r.AddCookie(&http.Cookie{Name: "refresh_token", Value: rawRefresh})
		r.RemoteAddr = "10.0.0.77:443"
		w := httptest.NewRecorder()
		h.Refresh(w, r)
		if w.Code != 200 {
			t.Fatalf("refresh status %d: %s", w.Code, w.Body.String())
		}
	}

	find := func() *model.ClientWithUser {
		t.Helper()
		all, err := clients.ListAll(ctx)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for i := range all {
			if all[i].UserID == u.ID && all[i].DeviceID == "dev-rfu" {
				return &all[i]
			}
		}
		return nil
	}

	// No row exists yet: the refresh alone has to create one.
	refreshOnce("0.4.0")
	got := find()
	if got == nil {
		t.Fatalf("refresh did not create a client row for user=%s device=dev-rfu", u.ID)
	}
	if got.ClientVersion == nil || *got.ClientVersion != "0.4.0" {
		t.Fatalf("client_version: %v want 0.4.0", got.ClientVersion)
	}
	if got.LastIP == nil || *got.LastIP != "10.0.0.77" {
		t.Errorf("last_ip: %v", got.LastIP)
	}

	// The app updated itself in place; the next refresh must carry the change.
	refreshOnce("0.7.4")
	got = find()
	if got.ClientVersion == nil || *got.ClientVersion != "0.7.4" {
		t.Errorf("client_version after update: %v want 0.7.4", got.ClientVersion)
	}
}

// A 2FA-gated login never reaches Login's upsert, so without the second-factor
// step recording the identification the device stays invisible in the overview.
func TestVerify2FA_UpsertsClientRow(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	clients := repository.NewClientRepository(env.pool)
	cfg := authCfg()
	h := NewAuthHandler(env.users, audit.NewRepository(env.pool), env.storage, nil, cfg,
		"http://localhost", "PrivaSync").WithClients(clients)

	claims := createTestUser(t, env)
	confirmTOTPFor(t, h, env, claims)
	u, err := env.users.FindByID(ctx, claims.UserID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}

	sessionToken, err := auth.Issue2FASessionToken(cfg.JWTSecret, cfg.TwoFASessionTTL, u.ID)
	if err != nil {
		t.Fatalf("Issue2FASessionToken: %v", err)
	}
	body, _ := json.Marshal(map[string]any{
		"session_token": sessionToken,
		"totp_code":     currentTOTP(t, u),
		"client": map[string]any{
			"device_id":      "dev-2fa",
			"device_name":    "TOTP-PC",
			"platform":       "windows",
			"os_version":     "11.0.26220",
			"client_version": "0.7.4",
		},
	})
	r := httptest.NewRequest("POST", "/api/v1/auth/2fa", bytes.NewReader(body))
	r.RemoteAddr = "10.0.0.88:1234"
	w := httptest.NewRecorder()
	h.Verify2FA(w, r)

	if w.Code != 200 {
		t.Fatalf("2fa status %d: %s", w.Code, w.Body.String())
	}
	all, err := clients.ListAll(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for i := range all {
		if all[i].UserID == u.ID && all[i].DeviceID == "dev-2fa" {
			if all[i].ClientVersion == nil || *all[i].ClientVersion != "0.7.4" {
				t.Errorf("client_version: %v", all[i].ClientVersion)
			}
			return
		}
	}
	t.Fatalf("2fa verify did not upsert a client row for user=%s device=dev-2fa", u.ID)
}

// Deleting a client is bookkeeping on the overview, not a revocation: the row
// goes away, and an unknown or malformed id is reported rather than ignored.
func TestAdminClientsHandler_Delete(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	clients := repository.NewClientRepository(env.pool)
	u := seedUser(t, env, "del_cli_"+uniqueSuffix(), "del_cli_"+uniqueSuffix()+"@t.local")

	seeded, err := clients.Upsert(ctx, repository.UpsertClientInput{
		UserID: u.ID, DeviceID: "dev-del", Name: "DEL-PC", Platform: "windows",
	})
	if err != nil {
		t.Fatalf("seed client: %v", err)
	}

	h := NewAdminClientsHandler(clients)

	del := func(id string) *httptest.ResponseRecorder {
		t.Helper()
		r := reqWithClaims("DELETE", "/api/v1/admin/clients/"+id, nil, &auth.Claims{Role: "admin"})
		r = withURLParams(r, "clientID", id)
		w := httptest.NewRecorder()
		h.Delete(w, r)
		return w
	}

	if w := del(seeded.ID.String()); w.Code != http.StatusNoContent {
		t.Fatalf("delete status %d: %s", w.Code, w.Body.String())
	}
	all, err := clients.ListAll(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for i := range all {
		if all[i].ID == seeded.ID {
			t.Fatalf("client %s still listed after delete", seeded.ID)
		}
	}

	if w := del(seeded.ID.String()); w.Code != http.StatusNotFound {
		t.Errorf("second delete status %d want 404", w.Code)
	}
	if w := del("not-a-uuid"); w.Code != http.StatusBadRequest {
		t.Errorf("malformed id status %d want 400", w.Code)
	}
}
