package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/model"
)

func TestListUsers_ReturnsCreatedUsers(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	claims := createTestUser(t, env)

	r := reqWithClaims("GET", "/admin/users", nil, claims)
	w := httptest.NewRecorder()
	adminH.ListUsers(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, u := range resp {
		if u["id"] == claims.UserID.String() {
			found = true
		}
	}
	if !found {
		t.Fatalf("created user %s not present in ListUsers response", claims.UserID)
	}
}

func TestSetQuota_UpdatesUserQuota(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)
	target := createTestUser(t, env)

	body := bytes.NewBufferString(`{"quota_bytes":123456789}`)
	r := reqWithClaims("PUT", "/admin/users/"+target.UserID.String()+"/quota", body, admin)
	r = withURLParams(r, "userID", target.UserID.String())
	w := httptest.NewRecorder()
	adminH.SetQuota(w, r)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	u, err := env.users.FindByID(context.Background(), target.UserID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if u.QuotaBytes != 123456789 {
		t.Fatalf("QuotaBytes = %d, want 123456789", u.QuotaBytes)
	}
}

func TestSetQuota_InvalidUserIDRejected(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)

	body := bytes.NewBufferString(`{"quota_bytes":1}`)
	r := reqWithClaims("PUT", "/admin/users/not-a-uuid/quota", body, admin)
	r = withURLParams(r, "userID", "not-a-uuid")
	w := httptest.NewRecorder()
	adminH.SetQuota(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestUnlockUser_ClearsFailedAttempts(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)
	target := createTestUser(t, env)
	targetUser, err := env.users.FindByID(context.Background(), target.UserID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}

	if err := env.users.RecordLoginAttempt(context.Background(), targetUser.Username, "127.0.0.1", false); err != nil {
		t.Fatalf("RecordLoginAttempt: %v", err)
	}

	r := reqWithClaims("POST", "/admin/users/"+target.UserID.String()+"/unlock", nil, admin)
	r = withURLParams(r, "userID", target.UserID.String())
	w := httptest.NewRecorder()
	adminH.UnlockUser(w, r)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	count, err := env.users.CountFailedAttempts(context.Background(), targetUser.Username, 24*time.Hour)
	if err != nil {
		t.Fatalf("CountFailedAttempts: %v", err)
	}
	if count != 0 {
		t.Fatalf("failed-attempt count = %d after unlock, want 0", count)
	}
}

func TestUnlockUser_UnknownUserNotFound(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)

	unknown := uuid.New()
	r := reqWithClaims("POST", "/admin/users/"+unknown.String()+"/unlock", nil, admin)
	r = withURLParams(r, "userID", unknown.String())
	w := httptest.NewRecorder()
	adminH.UnlockUser(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

func TestSetActive_DeactivatesTargetUser(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)
	target := createTestUser(t, env)

	body := bytes.NewBufferString(`{"active":false}`)
	r := reqWithClaims("PUT", "/admin/users/"+target.UserID.String()+"/active", body, admin)
	r = withURLParams(r, "userID", target.UserID.String())
	w := httptest.NewRecorder()
	adminH.SetActive(w, r)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	u, err := env.users.FindByID(context.Background(), target.UserID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if u.IsActive {
		t.Fatal("user must be inactive after SetActive(false)")
	}
}

func TestSetActive_CannotLockSelf(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)

	body := bytes.NewBufferString(`{"active":false}`)
	r := reqWithClaims("PUT", "/admin/users/"+admin.UserID.String()+"/active", body, admin)
	r = withURLParams(r, "userID", admin.UserID.String())
	w := httptest.NewRecorder()
	adminH.SetActive(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestSetRole_PromotesToAdmin(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)
	target := createTestUser(t, env)

	body := bytes.NewBufferString(`{"role":"admin"}`)
	r := reqWithClaims("PUT", "/admin/users/"+target.UserID.String()+"/role", body, admin)
	r = withURLParams(r, "userID", target.UserID.String())
	w := httptest.NewRecorder()
	adminH.SetRole(w, r)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	u, err := env.users.FindByID(context.Background(), target.UserID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if u.Role != model.RoleAdmin {
		t.Fatalf("Role = %v, want admin", u.Role)
	}
}

func TestSetRole_CannotDemoteSelf(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)

	body := bytes.NewBufferString(`{"role":"user"}`)
	r := reqWithClaims("PUT", "/admin/users/"+admin.UserID.String()+"/role", body, admin)
	r = withURLParams(r, "userID", admin.UserID.String())
	w := httptest.NewRecorder()
	adminH.SetRole(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestSetRole_InvalidRoleRejected(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)
	target := createTestUser(t, env)

	body := bytes.NewBufferString(`{"role":"superadmin"}`)
	r := reqWithClaims("PUT", "/admin/users/"+target.UserID.String()+"/role", body, admin)
	r = withURLParams(r, "userID", target.UserID.String())
	w := httptest.NewRecorder()
	adminH.SetRole(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestListAuditLog_ReturnsRecordedEvents(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)
	target := createTestUser(t, env)

	// SetActive above logs an audit event as a side effect -- trigger one here too.
	body := bytes.NewBufferString(`{"quota_bytes":42}`)
	setQuotaReq := reqWithClaims("PUT", "/admin/users/"+target.UserID.String()+"/quota", body, admin)
	setQuotaReq = withURLParams(setQuotaReq, "userID", target.UserID.String())
	adminH.SetQuota(httptest.NewRecorder(), setQuotaReq)

	r := reqWithClaims("GET", "/admin/audit-log?user="+admin.UserID.String()+"&limit=10", nil, admin)
	w := httptest.NewRecorder()
	adminH.ListAuditLog(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Count == 0 {
		t.Fatal("ListAuditLog returned no events after SetQuota logged one")
	}
}

func TestListAuditLog_InvalidUserFilterRejected(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)

	r := reqWithClaims("GET", "/admin/audit-log?user=not-a-uuid", nil, admin)
	w := httptest.NewRecorder()
	adminH.ListAuditLog(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestListAuditLog_InvalidLimitRejected(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)

	r := reqWithClaims("GET", "/admin/audit-log?limit=not-a-number", nil, admin)
	w := httptest.NewRecorder()
	adminH.ListAuditLog(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}
