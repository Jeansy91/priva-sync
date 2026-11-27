package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"privasync/server/internal/audit"
	"privasync/server/internal/auth"
	"privasync/server/internal/config"
	"privasync/server/internal/model"
)

// userWithPassword creates a user with a real (verifiable) password hash,
// bypassing seedUser's placeholder "x" hash.
func userWithPassword(t *testing.T, env *testEnv, password string) *model.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	u, err := env.users.Create(context.Background(),
		"pw_test_"+uniqueSuffix(), "pw_test_"+uniqueSuffix()+"@t.local", hash, model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	registerUserCleanup(t, env.pool, u.ID)
	return u
}

func authCfg() config.AuthConfig {
	return config.AuthConfig{
		JWTSecret:          "test-secret-at-least-32-bytes-long",
		AccessTokenTTL:     5 * time.Minute,
		RefreshTokenTTL:    time.Hour,
		TwoFASessionTTL:    5 * time.Minute,
		MaxLoginAttempts:   10,
		LoginLockoutWindow: time.Hour,
	}
}

// ─── TOTP setup / confirm / disable ──────────────────────────────────────────

func TestSetupTOTP_ReturnsSecretAndURL(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")
	claims := createTestUser(t, env)

	r := reqWithClaims("POST", "/auth/totp/setup", nil, claims)
	w := httptest.NewRecorder()
	h.SetupTOTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["secret"] == "" || resp["otpauth_url"] == "" {
		t.Fatalf("missing secret/otpauth_url in response: %v", resp)
	}

	u, err := env.users.FindByID(context.Background(), claims.UserID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if u.TOTPEnabled {
		t.Fatal("TOTPEnabled must stay false until ConfirmTOTP")
	}
	if u.TOTPSecret == nil {
		t.Fatal("TOTPSecret must be persisted after setup")
	}
}

func setupTOTPFor(t *testing.T, h *AuthHandler, env *testEnv, claims *auth.Claims) string {
	t.Helper()
	r := reqWithClaims("POST", "/auth/totp/setup", nil, claims)
	w := httptest.NewRecorder()
	h.SetupTOTP(w, r)
	if w.Code != 200 {
		t.Fatalf("SetupTOTP status = %d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp.Secret
}

func TestConfirmTOTP_ValidCodeEnablesAndReturnsBackupCodes(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")
	claims := createTestUser(t, env)
	secret := setupTOTPFor(t, h, env, claims)

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}

	body := bytes.NewBufferString(`{"code":"` + code + `"}`)
	r := reqWithClaims("POST", "/auth/totp/confirm", body, claims)
	w := httptest.NewRecorder()
	h.ConfirmTOTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		BackupCodes []string `json:"backup_codes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.BackupCodes) != backupCodeCount {
		t.Fatalf("got %d backup codes, want %d", len(resp.BackupCodes), backupCodeCount)
	}

	u, err := env.users.FindByID(context.Background(), claims.UserID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if !u.TOTPEnabled {
		t.Fatal("TOTPEnabled must be true after ConfirmTOTP")
	}
}

func TestConfirmTOTP_WrongCodeRejected(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")
	claims := createTestUser(t, env)
	setupTOTPFor(t, h, env, claims)

	body := bytes.NewBufferString(`{"code":"000000"}`)
	r := reqWithClaims("POST", "/auth/totp/confirm", body, claims)
	w := httptest.NewRecorder()
	h.ConfirmTOTP(w, r)

	if w.Code != 401 {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
}

func TestConfirmTOTP_NotSetUpRejected(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")
	claims := createTestUser(t, env)

	body := bytes.NewBufferString(`{"code":"123456"}`)
	r := reqWithClaims("POST", "/auth/totp/confirm", body, claims)
	w := httptest.NewRecorder()
	h.ConfirmTOTP(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func confirmTOTPFor(t *testing.T, h *AuthHandler, env *testEnv, claims *auth.Claims) {
	t.Helper()
	secret := setupTOTPFor(t, h, env, claims)
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	body := bytes.NewBufferString(`{"code":"` + code + `"}`)
	r := reqWithClaims("POST", "/auth/totp/confirm", body, claims)
	w := httptest.NewRecorder()
	h.ConfirmTOTP(w, r)
	if w.Code != 200 {
		t.Fatalf("ConfirmTOTP status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestRegenerateBackupCodes_ReplacesExisting(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")
	claims := createTestUser(t, env)
	confirmTOTPFor(t, h, env, claims)

	r := reqWithClaims("POST", "/auth/totp/backup-codes/regenerate", nil, claims)
	w := httptest.NewRecorder()
	h.RegenerateBackupCodes(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		BackupCodes []string `json:"backup_codes"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.BackupCodes) != backupCodeCount {
		t.Fatalf("got %d backup codes, want %d", len(resp.BackupCodes), backupCodeCount)
	}
}

func TestRegenerateBackupCodes_TOTPNotEnabledRejected(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")
	claims := createTestUser(t, env)

	r := reqWithClaims("POST", "/auth/totp/backup-codes/regenerate", nil, claims)
	w := httptest.NewRecorder()
	h.RegenerateBackupCodes(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestListBackupCodes_ReportsUnusedCount(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")
	claims := createTestUser(t, env)
	confirmTOTPFor(t, h, env, claims)

	r := reqWithClaims("GET", "/auth/totp/backup-codes", nil, claims)
	w := httptest.NewRecorder()
	h.ListBackupCodes(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Total  int `json:"total"`
		Unused int `json:"unused"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Total != backupCodeCount || resp.Unused != backupCodeCount {
		t.Fatalf("total=%d unused=%d, want both %d", resp.Total, resp.Unused, backupCodeCount)
	}
}

func TestDisableTOTP_ClearsSecretAndFlag(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")
	claims := createTestUser(t, env)
	confirmTOTPFor(t, h, env, claims)

	r := reqWithClaims("DELETE", "/auth/totp", nil, claims)
	w := httptest.NewRecorder()
	h.DisableTOTP(w, r)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	u, err := env.users.FindByID(context.Background(), claims.UserID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if u.TOTPEnabled || u.TOTPSecret != nil {
		t.Fatal("TOTP must be fully disabled (flag false, secret nil)")
	}
}

func TestNormalizeBackupCode_HandlesSeparatorsAndCase(t *testing.T) {
	cases := map[string]string{
		"AbCd-EfGh-1234-5678": "abcd-efgh-1234-5678",
		"abcdefgh12345678":    "abcd-efgh-1234-5678",
		" abcd efgh1234 5678": "abcd-efgh-1234-5678",
		"too-short":           "tooshort",
	}
	for in, want := range cases {
		if got := normalizeBackupCode(in); got != want {
			t.Errorf("normalizeBackupCode(%q) = %q, want %q", in, got, want)
		}
	}
}

// ─── Verify2FA / Logout / Me ─────────────────────────────────────────────────

func TestVerify2FA_ValidTOTPCodeIssuesTokens(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	cfg := authCfg()
	h := NewAuthHandler(env.users, audits, env.storage, nil, cfg, "http://localhost", "PrivaSync")
	claims := createTestUser(t, env)
	confirmTOTPFor(t, h, env, claims)

	u, err := env.users.FindByID(context.Background(), claims.UserID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	sessionToken, err := auth.Issue2FASessionToken(cfg.JWTSecret, cfg.TwoFASessionTTL, u.ID)
	if err != nil {
		t.Fatalf("Issue2FASessionToken: %v", err)
	}
	code, err := totp.GenerateCode(*u.TOTPSecret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}

	body := bytes.NewBufferString(`{"session_token":"` + sessionToken + `","totp_code":"` + code + `"}`)
	r := httptest.NewRequest("POST", "/auth/2fa", body)
	w := httptest.NewRecorder()
	h.Verify2FA(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["access_token"] == nil && resp["accessToken"] == nil {
		t.Fatalf("response missing access token: %v", resp)
	}
}

func TestVerify2FA_InvalidSessionTokenRejected(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")

	body := bytes.NewBufferString(`{"session_token":"garbage","totp_code":"123456"}`)
	r := httptest.NewRequest("POST", "/auth/2fa", body)
	w := httptest.NewRecorder()
	h.Verify2FA(w, r)

	if w.Code != 401 {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
}

func TestVerify2FA_MissingCodeRejected(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	cfg := authCfg()
	h := NewAuthHandler(env.users, audits, env.storage, nil, cfg, "http://localhost", "PrivaSync")
	claims := createTestUser(t, env)
	confirmTOTPFor(t, h, env, claims)

	sessionToken, err := auth.Issue2FASessionToken(cfg.JWTSecret, cfg.TwoFASessionTTL, claims.UserID)
	if err != nil {
		t.Fatalf("Issue2FASessionToken: %v", err)
	}
	body := bytes.NewBufferString(`{"session_token":"` + sessionToken + `"}`)
	r := httptest.NewRequest("POST", "/auth/2fa", body)
	w := httptest.NewRecorder()
	h.Verify2FA(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestVerify2FA_BackupCodeConsumesIt(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	cfg := authCfg()
	h := NewAuthHandler(env.users, audits, env.storage, nil, cfg, "http://localhost", "PrivaSync")
	claims := createTestUser(t, env)
	confirmTOTPFor(t, h, env, claims)

	codes, err := env.users.ListBackupCodes(context.Background(), claims.UserID)
	if err != nil || len(codes) == 0 {
		t.Fatalf("ListBackupCodes: %v (n=%d)", err, len(codes))
	}
	// We only have hashes -- regenerate a known plaintext set instead. Must be
	// in the canonical XXXX-XXXX-XXXX-XXXX form so normalizeBackupCode is a
	// no-op and the stored hash matches what Verify2FA re-derives.
	const knownCode = "abcd-ef12-3456-7890"
	if err := env.users.ReplaceBackupCodes(context.Background(), claims.UserID, []string{knownCode}); err != nil {
		t.Fatalf("ReplaceBackupCodes: %v", err)
	}

	sessionToken, err := auth.Issue2FASessionToken(cfg.JWTSecret, cfg.TwoFASessionTTL, claims.UserID)
	if err != nil {
		t.Fatalf("Issue2FASessionToken: %v", err)
	}
	body := bytes.NewBufferString(`{"session_token":"` + sessionToken + `","backup_code":"` + knownCode + `"}`)
	r := httptest.NewRequest("POST", "/auth/2fa", body)
	w := httptest.NewRecorder()
	h.Verify2FA(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	// Replay must fail -- the code is single-use.
	sessionToken2, _ := auth.Issue2FASessionToken(cfg.JWTSecret, cfg.TwoFASessionTTL, claims.UserID)
	body2 := bytes.NewBufferString(`{"session_token":"` + sessionToken2 + `","backup_code":"known-code-123"}`)
	r2 := httptest.NewRequest("POST", "/auth/2fa", body2)
	w2 := httptest.NewRecorder()
	h.Verify2FA(w2, r2)
	if w2.Code != 401 {
		t.Fatalf("replay status = %d, want 401", w2.Code)
	}
}

func TestLogout_NoCookieStillSucceeds(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")

	r := httptest.NewRequest("POST", "/auth/logout", nil)
	w := httptest.NewRecorder()
	h.Logout(w, r)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}

func TestLogout_WithCookieDeletesRefreshToken(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")
	claims := createTestUser(t, env)

	if err := env.users.CreateRefreshToken(context.Background(), claims.UserID, "logout-test-token", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}

	r := httptest.NewRequest("POST", "/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: "refresh_token", Value: "logout-test-token"})
	w := httptest.NewRecorder()
	h.Logout(w, r)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if _, err := env.users.FindRefreshToken(context.Background(), "logout-test-token"); err == nil {
		t.Fatal("refresh token must be deleted after logout")
	}
}

func TestMe_ReturnsUserProfile(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")
	claims := createTestUser(t, env)

	r := reqWithClaims("GET", "/auth/me", nil, claims)
	w := httptest.NewRecorder()
	h.Me(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["id"] == nil {
		t.Fatalf("response missing id: %v", resp)
	}
}

// ─── ChangePassword / RequestPasswordReset ───────────────────────────────────

func TestChangePassword_Success(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")
	u := userWithPassword(t, env, "original-password-123")
	claims := &auth.Claims{UserID: u.ID, Role: u.Role, TokenType: auth.TokenTypeAccess}

	body := bytes.NewBufferString(`{"current_password":"original-password-123","new_password":"brand-new-password-456"}`)
	r := reqWithClaims("PUT", "/auth/password", body, claims)
	w := httptest.NewRecorder()
	h.ChangePassword(w, r)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	updated, err := env.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if err := auth.VerifyPassword("brand-new-password-456", updated.PasswordHash); err != nil {
		t.Fatalf("new password does not verify: %v", err)
	}
}

func TestChangePassword_WrongCurrentPasswordRejected(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")
	u := userWithPassword(t, env, "original-password-123")
	claims := &auth.Claims{UserID: u.ID, Role: u.Role, TokenType: auth.TokenTypeAccess}

	body := bytes.NewBufferString(`{"current_password":"totally-wrong","new_password":"brand-new-password-456"}`)
	r := reqWithClaims("PUT", "/auth/password", body, claims)
	w := httptest.NewRecorder()
	h.ChangePassword(w, r)

	if w.Code != 401 {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
}

func TestChangePassword_NewPasswordTooShortRejected(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")
	u := userWithPassword(t, env, "original-password-123")
	claims := &auth.Claims{UserID: u.ID, Role: u.Role, TokenType: auth.TokenTypeAccess}

	body := bytes.NewBufferString(`{"current_password":"original-password-123","new_password":"short"}`)
	r := reqWithClaims("PUT", "/auth/password", body, claims)
	w := httptest.NewRecorder()
	h.ChangePassword(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestChangePassword_MissingFieldsRejected(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")
	claims := createTestUser(t, env)

	body := bytes.NewBufferString(`{"current_password":"","new_password":""}`)
	r := reqWithClaims("PUT", "/auth/password", body, claims)
	w := httptest.NewRecorder()
	h.ChangePassword(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestRequestPasswordReset_UnknownEmailReturns200(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")

	body := bytes.NewBufferString(`{"email":"nobody-` + uniqueSuffix() + `@t.local"}`)
	r := httptest.NewRequest("POST", "/auth/password/reset-request", body)
	w := httptest.NewRecorder()
	h.RequestPasswordReset(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestRequestPasswordReset_InvalidBodyReturns200(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")

	r := httptest.NewRequest("POST", "/auth/password/reset-request", bytes.NewBufferString(`not-json`))
	w := httptest.NewRecorder()
	h.RequestPasswordReset(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (silent success on bad input); body=%s", w.Code, w.Body.String())
	}
}

func TestRequestPasswordReset_InactiveUserReturns200WithoutMail(t *testing.T) {
	env := newTestEnv(t)
	audits := audit.NewRepository(env.pool)
	h := NewAuthHandler(env.users, audits, env.storage, nil, authCfg(), "http://localhost", "PrivaSync")
	u := userWithPassword(t, env, "irrelevant-password-123")
	if err := env.users.SetActive(context.Background(), u.ID, false); err != nil {
		t.Fatalf("SetActive(false): %v", err)
	}

	body := bytes.NewBufferString(`{"email":"` + u.Email + `"}`)
	r := httptest.NewRequest("POST", "/auth/password/reset-request", body)
	w := httptest.NewRecorder()
	h.RequestPasswordReset(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}
