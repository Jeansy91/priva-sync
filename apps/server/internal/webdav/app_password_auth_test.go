package webdav_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"privasync/server/internal/model"
)

// appPasswordFor issues a WebDAV credential for the user and returns its
// plaintext secret.
func (r *rig) appPasswordFor(t *testing.T, u *model.User) string {
	t.Helper()
	_, secret, err := r.appPasswords.Create(context.Background(), u.ID, "test-device")
	if err != nil {
		t.Fatalf("create app password: %v", err)
	}
	return secret
}

func (r *rig) propfindWithBasic(t *testing.T, username, password string) int {
	t.Helper()
	req := httptest.NewRequest("PROPFIND", "/", nil)
	req.SetBasicAuth(username, password)
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)
	return rec.Code
}

// Basic Auth cannot ask for a second factor. Accepting the account password
// there means an account with TOTP enabled is fully reachable on port 8081
// with the password alone — the second factor becomes decoration.
func TestAuth_AccountPasswordRejectedRegardlessOfTOTP(t *testing.T) {
	r := newRig(t)

	withTOTP := r.makeUser(t)
	secret := "JBSWY3DPEHPK3PXP"
	if err := r.users.UpdateTOTP(context.Background(), withTOTP.ID, &secret, true); err != nil {
		t.Fatalf("enable totp: %v", err)
	}
	withoutTOTP := r.makeUser(t)

	// The account password must not open WebDAV for either of them: a rule
	// that only applies to 2FA accounts would silently stop protecting one
	// the moment its owner turns 2FA off.
	for _, u := range []*model.User{withTOTP, withoutTOTP} {
		if code := r.propfindWithBasic(t, u.Username, testPassword); code != http.StatusUnauthorized {
			t.Errorf("account password for %q = %d, want 401", u.Username, code)
		}
	}
}

func TestAuth_AppPasswordAccepted(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	r.makeRoot(t, u.ID, "Docs")

	if code := r.propfindWithBasic(t, u.Username, r.appPasswordFor(t, u)); code != http.StatusMultiStatus {
		t.Fatalf("app password = %d, want 207", code)
	}
}

func TestAuth_AppPasswordRejectedCases(t *testing.T) {
	r := newRig(t)
	owner := r.makeUser(t)
	stranger := r.makeUser(t)
	secret := r.appPasswordFor(t, owner)

	tests := []struct {
		name     string
		username string
		password string
	}{
		{name: "unknown secret", username: owner.Username, password: "aaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhh"},
		{name: "empty secret", username: owner.Username, password: ""},
		// The secret alone identifies its owner; presenting it under someone
		// else's name must not authenticate as either of them.
		{name: "someone else's username", username: stranger.Username, password: secret},
		{name: "unknown username", username: "nobody", password: secret},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code := r.propfindWithBasic(t, tt.username, tt.password); code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", code)
			}
		})
	}
}

// Revoking a device's credential must take effect immediately.
func TestAuth_RevokedAppPasswordStopsWorking(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	r.makeRoot(t, u.ID, "Docs")
	ctx := context.Background()

	ap, secret, err := r.appPasswords.Create(ctx, u.ID, "laptop")
	if err != nil {
		t.Fatalf("create app password: %v", err)
	}
	if code := r.propfindWithBasic(t, u.Username, secret); code != http.StatusMultiStatus {
		t.Fatalf("before revoke = %d, want 207", code)
	}

	if err := r.appPasswords.Delete(ctx, ap.ID, u.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if code := r.propfindWithBasic(t, u.Username, secret); code != http.StatusUnauthorized {
		t.Fatalf("after revoke = %d, want 401", code)
	}
}

// An inactive account must not stay reachable through a credential it was
// issued while it was still active.
func TestAuth_InactiveUserRejectedDespiteAppPassword(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	secret := r.appPasswordFor(t, u)

	if _, err := r.pool.Exec(context.Background(),
		`UPDATE users SET is_active = FALSE WHERE id = $1`, u.ID); err != nil {
		t.Fatalf("deactivate user: %v", err)
	}
	if code := r.propfindWithBasic(t, u.Username, secret); code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", code)
	}
}

// last_used_at is what lets an owner tell a live device from a forgotten one
// before revoking it.
func TestAuth_AppPasswordUseIsRecorded(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	r.makeRoot(t, u.ID, "Docs")
	ctx := context.Background()

	_, secret, err := r.appPasswords.Create(ctx, u.ID, "laptop")
	if err != nil {
		t.Fatalf("create app password: %v", err)
	}
	if code := r.propfindWithBasic(t, u.Username, secret); code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", code)
	}

	listed, err := r.appPasswords.ListByUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if listed[0].LastUsedAt == nil {
		t.Error("using an app password did not record last_used_at")
	}
}
