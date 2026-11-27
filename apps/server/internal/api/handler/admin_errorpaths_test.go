package handler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/audit"
	"privasync/server/internal/auth"
	"privasync/server/internal/config"
	"privasync/server/internal/model"
)

// errBoom is a generic (non-typed) failure used to drive the ErrInternal (500)
// branches that a real DB cannot be made to hit deterministically. The seam
// (ADR-0029) lets a fake return it on exactly one method.
var errBoom = errors.New("boom")

// ── Fakes: implement only the narrow admin* interfaces, erroring on demand ──

// fakeAdminUsers errors on the method named in errOn; every other method
// returns a benign zero value so the handler reaches the targeted call.
type fakeAdminUsers struct{ errOn map[string]error }

func (f fakeAdminUsers) List(context.Context) ([]model.User, error) {
	return nil, f.errOn["List"]
}
func (f fakeAdminUsers) FailedAttemptsByUser(context.Context, time.Duration) (map[string]int, error) {
	if e := f.errOn["FailedAttemptsByUser"]; e != nil {
		return nil, e
	}
	return map[string]int{}, nil
}
func (f fakeAdminUsers) Create(context.Context, string, string, string, model.Role) (*model.User, error) {
	if e := f.errOn["Create"]; e != nil {
		return nil, e
	}
	return &model.User{ID: uuid.New(), Username: "fresh"}, nil
}
func (f fakeAdminUsers) CreatePasswordResetToken(context.Context, uuid.UUID, string, time.Time) error {
	return f.errOn["CreatePasswordResetToken"]
}
func (f fakeAdminUsers) SetQuota(context.Context, uuid.UUID, int64) error {
	return f.errOn["SetQuota"]
}
func (f fakeAdminUsers) SetRole(context.Context, uuid.UUID, model.Role) error {
	return f.errOn["SetRole"]
}
func (f fakeAdminUsers) FindByID(_ context.Context, id uuid.UUID) (*model.User, error) {
	if e := f.errOn["FindByID"]; e != nil {
		return nil, e
	}
	return &model.User{ID: id, Username: "target"}, nil
}
func (f fakeAdminUsers) ClearFailedAttempts(context.Context, string) error {
	return f.errOn["ClearFailedAttempts"]
}
func (f fakeAdminUsers) SetActive(context.Context, uuid.UUID, bool) error {
	return f.errOn["SetActive"]
}

type fakeAdminAudits struct{ errOn map[string]error }

func (f fakeAdminAudits) Log(context.Context, audit.Event) error { return f.errOn["Log"] }
func (f fakeAdminAudits) List(context.Context, audit.ListFilter) ([]audit.Event, error) {
	if e := f.errOn["List"]; e != nil {
		return nil, e
	}
	return nil, nil
}

type fakeAdminQuota struct{ errOn map[string]error }

func (f fakeAdminQuota) RecomputeViaBus(context.Context, uuid.UUID) error {
	return f.errOn["RecomputeViaBus"]
}

// fakeAdminHandler wires an AdminHandler over the fakes with a deterministic
// invite-token generator. Individual tests set the errOn maps.
func fakeAdminHandler(users fakeAdminUsers, audits fakeAdminAudits, q fakeAdminQuota) *AdminHandler {
	return &AdminHandler{
		users:   users,
		audits:  audits,
		mailer:  nil,
		quota:   q,
		cfg:     config.AuthConfig{MaxLoginAttempts: 5, LoginLockoutWindow: time.Minute},
		baseURL: "http://test",
		newInviteToken: func() (string, error) {
			return "invite-token-fixed", nil
		},
	}
}

func adminErrClaims() *auth.Claims {
	return &auth.Claims{UserID: uuid.New(), Role: model.RoleAdmin, TokenType: auth.TokenTypeAccess}
}

func assert500(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
	assertCode(t, w, ErrInternal)
}

func TestAdminListUsers_RepoErrorIs500(t *testing.T) {
	h := fakeAdminHandler(fakeAdminUsers{errOn: map[string]error{"List": errBoom}}, fakeAdminAudits{}, fakeAdminQuota{})
	r := reqWithClaims(http.MethodGet, "/admin/users", nil, adminErrClaims())
	w := httptest.NewRecorder()
	h.ListUsers(w, r)
	assert500(t, w)
}

func TestAdminCreateUser_RepoErrorsAre500(t *testing.T) {
	cases := []struct {
		name  string
		errOn map[string]error
	}{
		{"create fails", map[string]error{"Create": errBoom}},
		{"reset-token fails", map[string]error{"CreatePasswordResetToken": errBoom}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := fakeAdminHandler(fakeAdminUsers{errOn: tc.errOn}, fakeAdminAudits{}, fakeAdminQuota{})
			r := reqWithClaims(http.MethodPost, "/admin/users",
				bytes.NewBufferString(`{"username":"neu","email":"neu@t.local","role":"user"}`), adminErrClaims())
			w := httptest.NewRecorder()
			h.CreateUser(w, r)
			assert500(t, w)
		})
	}
}

func TestAdminCreateUser_InviteTokenErrorIs500(t *testing.T) {
	h := fakeAdminHandler(fakeAdminUsers{}, fakeAdminAudits{}, fakeAdminQuota{})
	h.newInviteToken = func() (string, error) { return "", errBoom }
	r := reqWithClaims(http.MethodPost, "/admin/users",
		bytes.NewBufferString(`{"username":"neu","email":"neu@t.local","role":"user"}`), adminErrClaims())
	w := httptest.NewRecorder()
	h.CreateUser(w, r)
	assert500(t, w)
}

func TestAdminSetQuota_RepoErrorIs500(t *testing.T) {
	h := fakeAdminHandler(fakeAdminUsers{errOn: map[string]error{"SetQuota": errBoom}}, fakeAdminAudits{}, fakeAdminQuota{})
	r := adminReq(adminErrClaims(), http.MethodPut, "/admin/users/x/quota", uuid.NewString(), `{"quota_bytes":1000}`)
	w := httptest.NewRecorder()
	h.SetQuota(w, r)
	assert500(t, w)
}

func TestAdminSetRole_RepoErrorIs500(t *testing.T) {
	h := fakeAdminHandler(fakeAdminUsers{errOn: map[string]error{"SetRole": errBoom}}, fakeAdminAudits{}, fakeAdminQuota{})
	r := adminReq(adminErrClaims(), http.MethodPut, "/admin/users/x/role", uuid.NewString(), `{"role":"user"}`)
	w := httptest.NewRecorder()
	h.SetRole(w, r)
	assert500(t, w)
}

func TestAdminSetActive_RepoErrorIs500(t *testing.T) {
	h := fakeAdminHandler(fakeAdminUsers{errOn: map[string]error{"SetActive": errBoom}}, fakeAdminAudits{}, fakeAdminQuota{})
	// Target differs from the caller so validateActiveChange passes.
	r := adminReq(adminErrClaims(), http.MethodPut, "/admin/users/x/active", uuid.NewString(), `{"active":true}`)
	w := httptest.NewRecorder()
	h.SetActive(w, r)
	assert500(t, w)
}

func TestAdminUnlockUser_RepoErrorsAre500(t *testing.T) {
	cases := []struct {
		name  string
		errOn map[string]error
	}{
		{"lookup fails", map[string]error{"FindByID": errBoom}},
		{"clear fails", map[string]error{"ClearFailedAttempts": errBoom}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := fakeAdminHandler(fakeAdminUsers{errOn: tc.errOn}, fakeAdminAudits{}, fakeAdminQuota{})
			r := adminReq(adminErrClaims(), http.MethodPost, "/admin/users/x/unlock", uuid.NewString(), "")
			w := httptest.NewRecorder()
			h.UnlockUser(w, r)
			assert500(t, w)
		})
	}
}

func TestAdminRecomputeQuota_ErrorsAre500(t *testing.T) {
	t.Run("recompute fails", func(t *testing.T) {
		h := fakeAdminHandler(fakeAdminUsers{}, fakeAdminAudits{}, fakeAdminQuota{errOn: map[string]error{"RecomputeViaBus": errBoom}})
		r := adminReq(adminErrClaims(), http.MethodPost, "/admin/users/x/recompute-quota", uuid.NewString(), "")
		w := httptest.NewRecorder()
		h.RecomputeQuota(w, r)
		assert500(t, w)
	})
	t.Run("reload fails", func(t *testing.T) {
		h := fakeAdminHandler(fakeAdminUsers{errOn: map[string]error{"FindByID": errBoom}}, fakeAdminAudits{}, fakeAdminQuota{})
		r := adminReq(adminErrClaims(), http.MethodPost, "/admin/users/x/recompute-quota", uuid.NewString(), "")
		w := httptest.NewRecorder()
		h.RecomputeQuota(w, r)
		assert500(t, w)
	})
}

func TestAdminListAuditLog_RepoErrorIs500(t *testing.T) {
	h := fakeAdminHandler(fakeAdminUsers{}, fakeAdminAudits{errOn: map[string]error{"List": errBoom}}, fakeAdminQuota{})
	r := reqWithClaims(http.MethodGet, "/admin/audit-log", nil, adminErrClaims())
	w := httptest.NewRecorder()
	h.ListAuditLog(w, r)
	assert500(t, w)
}
