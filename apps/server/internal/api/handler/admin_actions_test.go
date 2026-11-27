package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/auth"
)

// adminReq builds a request carrying the admin claims and a {userID} URL param.
func adminReq(admin *auth.Claims, method, target, userID, body string) *http.Request {
	r := reqWithClaims(method, target, bytes.NewBufferString(body), admin)
	return withURLParams(r, "userID", userID)
}

func TestAdminSetQuota_RejectsMalformedUserID(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)
	r := adminReq(admin, http.MethodPut, "/admin/users/bad/quota", "bad", `{"quota_bytes":1000}`)
	w := httptest.NewRecorder()
	adminH.SetQuota(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidUserID)
}

func TestAdminSetQuota_RejectsMalformedBody(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)
	target := createTestUser(t, env)
	r := adminReq(admin, http.MethodPut, "/admin/users/x/quota", target.UserID.String(), "{not json")
	w := httptest.NewRecorder()
	adminH.SetQuota(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidBody)
}

func TestAdminSetQuota_Succeeds(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)
	target := createTestUser(t, env)
	r := adminReq(admin, http.MethodPut, "/admin/users/x/quota", target.UserID.String(), `{"quota_bytes":123456}`)
	w := httptest.NewRecorder()
	adminH.SetQuota(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
}

func TestAdminUnlockUser_RejectsMalformedUserID(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)
	r := adminReq(admin, http.MethodPost, "/admin/users/bad/unlock", "bad", "")
	w := httptest.NewRecorder()
	adminH.UnlockUser(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidUserID)
}

func TestAdminUnlockUser_RejectsUnknownUser(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)
	r := adminReq(admin, http.MethodPost, "/admin/users/x/unlock", uuid.New().String(), "")
	w := httptest.NewRecorder()
	adminH.UnlockUser(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrUserNotFound)
}

func TestAdminUnlockUser_Succeeds(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)
	target := createTestUser(t, env)
	r := adminReq(admin, http.MethodPost, "/admin/users/x/unlock", target.UserID.String(), "")
	w := httptest.NewRecorder()
	adminH.UnlockUser(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
}

func TestAdminSetActive_RejectsSelfLock(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)
	r := adminReq(admin, http.MethodPut, "/admin/users/x/active", admin.UserID.String(), `{"active":false}`)
	w := httptest.NewRecorder()
	adminH.SetActive(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInvalidTargetUser)
}

func TestAdminSetActive_DeactivatesOtherUser(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)
	target := createTestUser(t, env)
	r := adminReq(admin, http.MethodPut, "/admin/users/x/active", target.UserID.String(), `{"active":false}`)
	w := httptest.NewRecorder()
	adminH.SetActive(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
}

func TestAdminListAuditLog_Succeeds(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)
	r := reqWithClaims(http.MethodGet, "/admin/audit-log?limit=50", nil, admin)
	w := httptest.NewRecorder()
	adminH.ListAuditLog(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["events"]; !ok {
		t.Errorf("response missing events key: %v", resp)
	}
}

func TestAdminListAuditLog_RejectsBadFilters(t *testing.T) {
	adminH, _, env := newAdminTestEnv(t)
	admin := createTestUser(t, env)
	cases := []struct {
		name  string
		query string
	}{
		{"user", "?user=not-a-uuid"},
		{"from", "?from=not-a-date"},
		{"to", "?to=not-a-date"},
		{"limit", "?limit=0"},
		{"offset", "?offset=-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := reqWithClaims(http.MethodGet, "/admin/audit-log"+tc.query, nil, admin)
			w := httptest.NewRecorder()
			adminH.ListAuditLog(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s: status = %d, want 400; body=%s", tc.name, w.Code, w.Body.String())
			}
		})
	}
}
