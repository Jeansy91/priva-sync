package handler

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/audit"
	"privasync/server/internal/auth"
	"privasync/server/internal/avatar"
	"privasync/server/internal/config"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

// This file drives AuthHandler's ErrInternal (500) branches through the narrow
// role interfaces from auth.go (ADR-0029). The fakes error on the method named
// in errOn and return benign values otherwise, so the handler reaches exactly
// the targeted repo/storage call. No DB, deterministic. errBoom/assert500/
// assertCode are shared with admin_errorpaths_test.go.

// ── Fakes ────────────────────────────────────────────────────────────────

// fakeAuthUsers implements authUserStore. `user` is what every Find* returns
// (with its ID defaulted to the requested id when zero); tests set fields to
// steer post-lookup branches. Token lookups return short-lived, unused tokens.
type fakeAuthUsers struct {
	errOn map[string]error
	user  model.User
}

func (f fakeAuthUsers) find(id uuid.UUID) (*model.User, error) {
	u := f.user
	if u.ID == uuid.Nil {
		u.ID = id
	}
	return &u, nil
}
func (f fakeAuthUsers) FindByID(_ context.Context, id uuid.UUID) (*model.User, error) {
	if e := f.errOn["FindByID"]; e != nil {
		return nil, e
	}
	return f.find(id)
}
func (f fakeAuthUsers) FindByUsername(_ context.Context, _ string) (*model.User, error) {
	if e := f.errOn["FindByUsername"]; e != nil {
		return nil, e
	}
	return f.find(uuid.New())
}
func (f fakeAuthUsers) FindByEmail(_ context.Context, _ string) (*model.User, error) {
	if e := f.errOn["FindByEmail"]; e != nil {
		return nil, e
	}
	return f.find(uuid.New())
}
func (f fakeAuthUsers) RecordLoginAttempt(context.Context, string, string, bool) error {
	return f.errOn["RecordLoginAttempt"]
}
func (f fakeAuthUsers) CountFailedAttempts(context.Context, string, time.Duration) (int, error) {
	return 0, f.errOn["CountFailedAttempts"]
}
func (f fakeAuthUsers) UpdateTOTP(context.Context, uuid.UUID, *string, bool) error {
	return f.errOn["UpdateTOTP"]
}
func (f fakeAuthUsers) UpdateUsername(context.Context, uuid.UUID, string) error {
	return f.errOn["UpdateUsername"]
}
func (f fakeAuthUsers) UpdateEmail(context.Context, uuid.UUID, string) error {
	return f.errOn["UpdateEmail"]
}
func (f fakeAuthUsers) UpdatePassword(context.Context, uuid.UUID, string) error {
	return f.errOn["UpdatePassword"]
}
func (f fakeAuthUsers) SetAvatarPath(context.Context, uuid.UUID, string) error {
	return f.errOn["SetAvatarPath"]
}
func (f fakeAuthUsers) ClearAvatarPath(context.Context, uuid.UUID) error {
	return f.errOn["ClearAvatarPath"]
}
func (f fakeAuthUsers) SetPreferredLanguage(context.Context, uuid.UUID, string) error {
	return f.errOn["SetPreferredLanguage"]
}
func (f fakeAuthUsers) SetShowFileTree(context.Context, uuid.UUID, bool) error {
	return f.errOn["SetShowFileTree"]
}
func (f fakeAuthUsers) SetShowFileCheckboxes(context.Context, uuid.UUID, bool) error {
	return f.errOn["SetShowFileCheckboxes"]
}
func (f fakeAuthUsers) SetTrashView(context.Context, uuid.UUID, string) error {
	return f.errOn["SetTrashView"]
}
func (f fakeAuthUsers) ReplaceBackupCodes(context.Context, uuid.UUID, []string) error {
	return f.errOn["ReplaceBackupCodes"]
}
func (f fakeAuthUsers) ListBackupCodes(context.Context, uuid.UUID) ([]model.BackupCode, error) {
	if e := f.errOn["ListBackupCodes"]; e != nil {
		return nil, e
	}
	return nil, nil
}
func (f fakeAuthUsers) ConsumeBackupCode(context.Context, uuid.UUID, string) error {
	return f.errOn["ConsumeBackupCode"]
}
func (f fakeAuthUsers) CreateRefreshToken(context.Context, uuid.UUID, string, time.Time) error {
	return f.errOn["CreateRefreshToken"]
}
func (f fakeAuthUsers) FindRefreshToken(_ context.Context, _ string) (*model.RefreshToken, error) {
	if e := f.errOn["FindRefreshToken"]; e != nil {
		return nil, e
	}
	return &model.RefreshToken{UserID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (f fakeAuthUsers) MarkRefreshTokenRotated(context.Context, string) error {
	return f.errOn["MarkRefreshTokenRotated"]
}
func (f fakeAuthUsers) DeleteRotatedRefreshTokens(context.Context, uuid.UUID, time.Duration) error {
	return f.errOn["DeleteRotatedRefreshTokens"]
}
func (f fakeAuthUsers) DeleteRefreshToken(context.Context, string) error {
	return f.errOn["DeleteRefreshToken"]
}
func (f fakeAuthUsers) DeleteAllRefreshTokens(context.Context, uuid.UUID) error {
	return f.errOn["DeleteAllRefreshTokens"]
}
func (f fakeAuthUsers) CreatePasswordResetToken(context.Context, uuid.UUID, string, time.Time) error {
	return f.errOn["CreatePasswordResetToken"]
}
func (f fakeAuthUsers) FindPasswordResetToken(_ context.Context, _ string) (*model.PasswordResetToken, error) {
	if e := f.errOn["FindPasswordResetToken"]; e != nil {
		return nil, e
	}
	return &model.PasswordResetToken{ID: uuid.New(), UserID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (f fakeAuthUsers) MarkPasswordResetTokenUsed(context.Context, uuid.UUID) error {
	return f.errOn["MarkPasswordResetTokenUsed"]
}
func (f fakeAuthUsers) CreateEmailChangeToken(context.Context, uuid.UUID, string, string, time.Time) error {
	return f.errOn["CreateEmailChangeToken"]
}
func (f fakeAuthUsers) ConsumeEmailChangeToken(_ context.Context, _ string) (*model.EmailChangeToken, error) {
	if e := f.errOn["ConsumeEmailChangeToken"]; e != nil {
		return nil, e
	}
	return &model.EmailChangeToken{ID: uuid.New(), UserID: uuid.New(), NewEmail: "neu@t.local", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

type fakeAuthAudits struct{ errOn map[string]error }

func (f fakeAuthAudits) Log(context.Context, audit.Event) error { return f.errOn["Log"] }

type fakeAuthPasskeys struct{ errOn map[string]error }

func (f fakeAuthPasskeys) Create(context.Context, uuid.UUID, []byte, []byte, string) (*model.PasskeyCredential, error) {
	if e := f.errOn["Create"]; e != nil {
		return nil, e
	}
	return &model.PasskeyCredential{ID: uuid.New()}, nil
}
func (f fakeAuthPasskeys) ListByUser(context.Context, uuid.UUID) ([]model.PasskeyCredential, error) {
	if e := f.errOn["ListByUser"]; e != nil {
		return nil, e
	}
	return nil, nil
}
func (f fakeAuthPasskeys) FindByCredentialID(context.Context, []byte) (uuid.UUID, error) {
	if e := f.errOn["FindByCredentialID"]; e != nil {
		return uuid.Nil, e
	}
	return uuid.New(), nil
}
func (f fakeAuthPasskeys) UpdateSignCount(context.Context, []byte, []byte) error {
	return f.errOn["UpdateSignCount"]
}
func (f fakeAuthPasskeys) Delete(context.Context, uuid.UUID, uuid.UUID) error {
	return f.errOn["Delete"]
}

type fakeAuthStorage struct{ errOn map[string]error }

func (f fakeAuthStorage) WriteAvatar(uuid.UUID, []byte) error { return f.errOn["WriteAvatar"] }
func (f fakeAuthStorage) DeleteAvatar(uuid.UUID) error        { return f.errOn["DeleteAvatar"] }
func (f fakeAuthStorage) AvatarPath(uuid.UUID) string         { return "" }

// fakeAuthHandler assembles an AuthHandler over the fakes. webAuthn stays nil —
// every branch tested here errors before any WebAuthn call.
func fakeAuthHandler(users fakeAuthUsers, passkeys fakeAuthPasskeys, stor fakeAuthStorage) *AuthHandler {
	return &AuthHandler{
		users:    users,
		audits:   fakeAuthAudits{},
		stor:     stor,
		mailer:   nil,
		passkeys: passkeys,
		cfg:      config.AuthConfig{JWTSecret: "test-secret", AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour, MaxLoginAttempts: 5, LoginLockoutWindow: time.Minute},
		baseURL:  "http://test",
		issuer:   "PrivaSync",
	}
}

func authErrClaims() *auth.Claims {
	return &auth.Claims{UserID: uuid.New(), Role: model.RoleAdmin, TokenType: auth.TokenTypeAccess}
}

// authErrReq builds a claims-bearing request with a JSON body.
func authErrReq(method, target, body string) *http.Request {
	return reqWithClaims(method, target, bytes.NewBufferString(body), authErrClaims())
}

// ── Profile / preferences ─────────────────────────────────────────────────

func TestAuthProfile_RepoErrorsAre500(t *testing.T) {
	cases := []struct {
		name   string
		method string
		body   string
		errKey string
		call   func(*AuthHandler, http.ResponseWriter, *http.Request)
	}{
		{"language", http.MethodPut, `{"language":"de"}`, "SetPreferredLanguage", (*AuthHandler).SetLanguage},
		{"file-tree", http.MethodPut, `{"show":true}`, "SetShowFileTree", (*AuthHandler).SetShowFileTree},
		{"file-checkboxes", http.MethodPut, `{"show":true}`, "SetShowFileCheckboxes", (*AuthHandler).SetShowFileCheckboxes},
		{"trash-view", http.MethodPut, `{"view":"list"}`, "SetTrashView", (*AuthHandler).SetTrashView},
		{"username", http.MethodPut, `{"username":"neuer_name"}`, "UpdateUsername", (*AuthHandler).SetUsername},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := fakeAuthHandler(fakeAuthUsers{errOn: map[string]error{tc.errKey: errBoom}}, fakeAuthPasskeys{}, fakeAuthStorage{})
			r := authErrReq(tc.method, "/auth/"+tc.name, tc.body)
			w := httptest.NewRecorder()
			tc.call(h, w, r)
			assert500(t, w)
		})
	}
}

func TestAuthSetUsername_Conflict(t *testing.T) {
	h := fakeAuthHandler(fakeAuthUsers{errOn: map[string]error{"UpdateUsername": repository.ErrConflict}}, fakeAuthPasskeys{}, fakeAuthStorage{})
	r := authErrReq(http.MethodPut, "/auth/username", `{"username":"neuer_name"}`)
	w := httptest.NewRecorder()
	h.SetUsername(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
}

// ── TOTP ──────────────────────────────────────────────────────────────────

func TestAuthTOTP_RepoErrorsAre500(t *testing.T) {
	t.Run("setup update fails", func(t *testing.T) {
		users := fakeAuthUsers{errOn: map[string]error{"UpdateTOTP": errBoom}, user: model.User{Username: "u"}}
		h := fakeAuthHandler(users, fakeAuthPasskeys{}, fakeAuthStorage{})
		w := httptest.NewRecorder()
		h.SetupTOTP(w, authErrReq(http.MethodPost, "/auth/totp/setup", ""))
		assert500(t, w)
	})
	t.Run("regenerate replace fails", func(t *testing.T) {
		users := fakeAuthUsers{errOn: map[string]error{"ReplaceBackupCodes": errBoom}, user: model.User{TOTPEnabled: true}}
		h := fakeAuthHandler(users, fakeAuthPasskeys{}, fakeAuthStorage{})
		w := httptest.NewRecorder()
		h.RegenerateBackupCodes(w, authErrReq(http.MethodPost, "/auth/totp/backup-codes/regenerate", ""))
		assert500(t, w)
	})
	t.Run("list fails", func(t *testing.T) {
		h := fakeAuthHandler(fakeAuthUsers{errOn: map[string]error{"ListBackupCodes": errBoom}}, fakeAuthPasskeys{}, fakeAuthStorage{})
		w := httptest.NewRecorder()
		h.ListBackupCodes(w, authErrReq(http.MethodGet, "/auth/totp/backup-codes", ""))
		assert500(t, w)
	})
	t.Run("disable update fails", func(t *testing.T) {
		h := fakeAuthHandler(fakeAuthUsers{errOn: map[string]error{"UpdateTOTP": errBoom}}, fakeAuthPasskeys{}, fakeAuthStorage{})
		w := httptest.NewRecorder()
		h.DisableTOTP(w, authErrReq(http.MethodDelete, "/auth/totp", ""))
		assert500(t, w)
	})
}

// ── Password ──────────────────────────────────────────────────────────────

func TestAuthChangePassword_UpdateErrorIs500(t *testing.T) {
	hash, err := auth.HashPassword("currentpassword")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	users := fakeAuthUsers{
		errOn: map[string]error{"UpdatePassword": errBoom},
		user:  model.User{PasswordHash: hash, IsActive: true},
	}
	h := fakeAuthHandler(users, fakeAuthPasskeys{}, fakeAuthStorage{})
	w := httptest.NewRecorder()
	h.ChangePassword(w, authErrReq(http.MethodPut, "/auth/password",
		`{"current_password":"currentpassword","new_password":"brandnewpassword123"}`))
	assert500(t, w)
}

func TestAuthConfirmPasswordReset_ErrorsAre500(t *testing.T) {
	t.Run("token lookup fails", func(t *testing.T) {
		h := fakeAuthHandler(fakeAuthUsers{errOn: map[string]error{"FindPasswordResetToken": errBoom}}, fakeAuthPasskeys{}, fakeAuthStorage{})
		w := httptest.NewRecorder()
		h.ConfirmPasswordReset(w, authErrReq(http.MethodPost, "/auth/password/reset-confirm",
			`{"token":"abc","new_password":"brandnewpassword123"}`))
		assert500(t, w)
	})
	t.Run("update fails", func(t *testing.T) {
		users := fakeAuthUsers{errOn: map[string]error{"UpdatePassword": errBoom}, user: model.User{IsActive: true}}
		h := fakeAuthHandler(users, fakeAuthPasskeys{}, fakeAuthStorage{})
		w := httptest.NewRecorder()
		h.ConfirmPasswordReset(w, authErrReq(http.MethodPost, "/auth/password/reset-confirm",
			`{"token":"abc","new_password":"brandnewpassword123"}`))
		assert500(t, w)
	})
}

// ── Email change ──────────────────────────────────────────────────────────

func TestAuthRequestEmailChange_ErrorsAre500(t *testing.T) {
	t.Run("conflict lookup fails", func(t *testing.T) {
		h := fakeAuthHandler(fakeAuthUsers{errOn: map[string]error{"FindByEmail": errBoom}}, fakeAuthPasskeys{}, fakeAuthStorage{})
		w := httptest.NewRecorder()
		h.RequestEmailChange(w, authErrReq(http.MethodPost, "/auth/profile/email-change", `{"new_email":"neu@t.local"}`))
		assert500(t, w)
	})
	t.Run("token insert fails", func(t *testing.T) {
		users := fakeAuthUsers{errOn: map[string]error{
			"FindByEmail":            repository.ErrNotFound, // no conflict
			"CreateEmailChangeToken": errBoom,
		}}
		h := fakeAuthHandler(users, fakeAuthPasskeys{}, fakeAuthStorage{})
		w := httptest.NewRecorder()
		h.RequestEmailChange(w, authErrReq(http.MethodPost, "/auth/profile/email-change", `{"new_email":"neu@t.local"}`))
		assert500(t, w)
	})
}

func TestAuthConfirmEmailChange_ErrorsAre500(t *testing.T) {
	t.Run("consume fails", func(t *testing.T) {
		h := fakeAuthHandler(fakeAuthUsers{errOn: map[string]error{"ConsumeEmailChangeToken": errBoom}}, fakeAuthPasskeys{}, fakeAuthStorage{})
		w := httptest.NewRecorder()
		h.ConfirmEmailChange(w, authErrReq(http.MethodPost, "/auth/profile/email-change/confirm", `{"token":"abc"}`))
		assert500(t, w)
	})
	t.Run("update fails", func(t *testing.T) {
		h := fakeAuthHandler(fakeAuthUsers{errOn: map[string]error{"UpdateEmail": errBoom}}, fakeAuthPasskeys{}, fakeAuthStorage{})
		w := httptest.NewRecorder()
		h.ConfirmEmailChange(w, authErrReq(http.MethodPost, "/auth/profile/email-change/confirm", `{"token":"abc"}`))
		assert500(t, w)
	})
	t.Run("update conflict is 409", func(t *testing.T) {
		h := fakeAuthHandler(fakeAuthUsers{errOn: map[string]error{"UpdateEmail": repository.ErrConflict}}, fakeAuthPasskeys{}, fakeAuthStorage{})
		w := httptest.NewRecorder()
		h.ConfirmEmailChange(w, authErrReq(http.MethodPost, "/auth/profile/email-change/confirm", `{"token":"abc"}`))
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
		}
	})
}

// ── Session ───────────────────────────────────────────────────────────────

func TestAuthLogin_LookupErrorIs500(t *testing.T) {
	h := fakeAuthHandler(fakeAuthUsers{errOn: map[string]error{"FindByUsername": errBoom}}, fakeAuthPasskeys{}, fakeAuthStorage{})
	w := httptest.NewRecorder()
	h.Login(w, authErrReq(http.MethodPost, "/auth/login", `{"username":"u","password":"p"}`))
	assert500(t, w)
}

func TestAuthRefresh_LookupErrorIs500(t *testing.T) {
	h := fakeAuthHandler(fakeAuthUsers{errOn: map[string]error{"FindRefreshToken": errBoom}}, fakeAuthPasskeys{}, fakeAuthStorage{})
	r := authErrReq(http.MethodPost, "/auth/refresh", "")
	r.AddCookie(&http.Cookie{Name: "refresh_token", Value: "tok"})
	w := httptest.NewRecorder()
	h.Refresh(w, r)
	assert500(t, w)
}

func TestAuthRefresh_IssueTokenCreateErrorIs500(t *testing.T) {
	users := fakeAuthUsers{errOn: map[string]error{"CreateRefreshToken": errBoom}, user: model.User{IsActive: true}}
	h := fakeAuthHandler(users, fakeAuthPasskeys{}, fakeAuthStorage{})
	r := authErrReq(http.MethodPost, "/auth/refresh", "")
	r.AddCookie(&http.Cookie{Name: "refresh_token", Value: "tok"})
	w := httptest.NewRecorder()
	h.Refresh(w, r)
	assert500(t, w)
}

// ── Avatar ────────────────────────────────────────────────────────────────

func TestAuthDeleteAvatar_ErrorsAre500(t *testing.T) {
	t.Run("storage delete fails", func(t *testing.T) {
		h := fakeAuthHandler(fakeAuthUsers{}, fakeAuthPasskeys{}, fakeAuthStorage{errOn: map[string]error{"DeleteAvatar": errBoom}})
		w := httptest.NewRecorder()
		h.DeleteAvatar(w, authErrReq(http.MethodDelete, "/auth/avatar", ""))
		assert500(t, w)
	})
	t.Run("clear path fails", func(t *testing.T) {
		h := fakeAuthHandler(fakeAuthUsers{errOn: map[string]error{"ClearAvatarPath": errBoom}}, fakeAuthPasskeys{}, fakeAuthStorage{})
		w := httptest.NewRecorder()
		h.DeleteAvatar(w, authErrReq(http.MethodDelete, "/auth/avatar", ""))
		assert500(t, w)
	})
}

func TestAuthPutAvatar_ErrorsAre500(t *testing.T) {
	png := avatarPNG(t)
	cases := []struct {
		name  string
		users fakeAuthUsers
		stor  fakeAuthStorage
	}{
		{"write fails", fakeAuthUsers{}, fakeAuthStorage{errOn: map[string]error{"WriteAvatar": errBoom}}},
		{"set path fails", fakeAuthUsers{errOn: map[string]error{"SetAvatarPath": errBoom}}, fakeAuthStorage{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, ct := avatarMultipart(t, png)
			r := httptest.NewRequest(http.MethodPut, "/auth/avatar", body)
			r.Header.Set("Content-Type", ct)
			r = r.WithContext(context.WithValue(r.Context(), middleware.ContextKeyClaims, authErrClaims()))
			h := fakeAuthHandler(tc.users, fakeAuthPasskeys{}, tc.stor)
			w := httptest.NewRecorder()
			h.PutAvatar(w, r)
			assert500(t, w)
		})
	}
}

// avatarPNG returns a valid PNG that survives avatar.Process.
func avatarPNG(t *testing.T) []byte {
	t.Helper()
	// A 16x16 RGBA PNG is well under avatarMaxBytes and decodes cleanly.
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	out := buf.Bytes()
	if _, err := avatar.Process(out, avatarMaxBytes); err != nil {
		t.Fatalf("avatar.Process rejected fixture: %v", err)
	}
	return out
}

func avatarMultipart(t *testing.T, png []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("avatar", "a.png")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := fw.Write(png); err != nil {
		t.Fatalf("write png: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return &body, mw.FormDataContentType()
}

// ── Passkeys ──────────────────────────────────────────────────────────────

func TestAuthPasskey_RepoErrorsAre500(t *testing.T) {
	t.Run("register begin list fails", func(t *testing.T) {
		h := fakeAuthHandler(fakeAuthUsers{}, fakeAuthPasskeys{errOn: map[string]error{"ListByUser": errBoom}}, fakeAuthStorage{})
		w := httptest.NewRecorder()
		h.RegisterPasskeyBegin(w, authErrReq(http.MethodPost, "/auth/passkey/register/begin", ""))
		assert500(t, w)
	})
	t.Run("list fails", func(t *testing.T) {
		h := fakeAuthHandler(fakeAuthUsers{}, fakeAuthPasskeys{errOn: map[string]error{"ListByUser": errBoom}}, fakeAuthStorage{})
		w := httptest.NewRecorder()
		h.ListPasskeys(w, authErrReq(http.MethodGet, "/auth/passkey", ""))
		assert500(t, w)
	})
	t.Run("delete fails", func(t *testing.T) {
		h := fakeAuthHandler(fakeAuthUsers{}, fakeAuthPasskeys{errOn: map[string]error{"Delete": errBoom}}, fakeAuthStorage{})
		r := withURLParams(authErrReq(http.MethodDelete, "/auth/passkey/x", ""), "passkeyID", uuid.NewString())
		w := httptest.NewRecorder()
		h.DeletePasskey(w, r)
		assert500(t, w)
	})
	t.Run("delete not found is 404", func(t *testing.T) {
		h := fakeAuthHandler(fakeAuthUsers{}, fakeAuthPasskeys{errOn: map[string]error{"Delete": repository.ErrNotFound}}, fakeAuthStorage{})
		r := withURLParams(authErrReq(http.MethodDelete, "/auth/passkey/x", ""), "passkeyID", uuid.NewString())
		w := httptest.NewRecorder()
		h.DeletePasskey(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
		}
	})
}
