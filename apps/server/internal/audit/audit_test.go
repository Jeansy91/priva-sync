package audit

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestFromRequest_ExtractsIPAndUserAgent(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "203.0.113.42:51234"
	req.Header.Set("User-Agent", "PrivaSync/1.0 (test)")

	uid := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	evt := FromRequest(req, &uid, EventLoginSuccess, map[string]any{"method": "password"})

	if evt.EventType != EventLoginSuccess {
		t.Fatalf("event type: got %q want %q", evt.EventType, EventLoginSuccess)
	}
	if evt.UserID == nil || *evt.UserID != uid {
		t.Fatalf("user id mismatch: got %v want %v", evt.UserID, uid)
	}
	if evt.IPAddress != "203.0.113.42" {
		t.Errorf("ip: got %q want %q", evt.IPAddress, "203.0.113.42")
	}
	if evt.UserAgent != "PrivaSync/1.0 (test)" {
		t.Errorf("ua: got %q", evt.UserAgent)
	}
	if v, ok := evt.Details["method"]; !ok || v != "password" {
		t.Errorf("details lost: %v", evt.Details)
	}
}

// The forwarding header is only believed when httpx's middleware resolved it
// from a trusted proxy. FromRequest on a bare request must fall back to the
// peer address instead of copying whatever the caller sent — the value goes
// into an INET column, where an unvalidated one loses the whole entry.
func TestFromRequest_IgnoresForwardedForFromUntrustedPeer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.1")

	evt := FromRequest(req, nil, EventLogout, nil)
	if evt.IPAddress != "127.0.0.1" {
		t.Errorf("ip: got %q want the peer address %q", evt.IPAddress, "127.0.0.1")
	}
	if evt.UserID != nil {
		t.Errorf("anon event should have nil user id, got %v", evt.UserID)
	}
}

// Whatever a caller sends, the recorded address is always parseable — the
// column is INET and a rejected INSERT would silently drop the audit trail.
func TestFromRequest_IPAddressIsAlwaysValidOrEmpty(t *testing.T) {
	for _, forged := range []string{"not-an-ip", "'; DROP TABLE audit_log; --", "1.2.3.4, junk"} {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "127.0.0.1:1"
		req.Header.Set("X-Forwarded-For", forged)
		req.Header.Set("X-Real-IP", forged)

		evt := FromRequest(req, nil, EventLoginFail, nil)
		if evt.IPAddress != "" && net.ParseIP(evt.IPAddress) == nil {
			t.Errorf("X-Forwarded-For %q produced unparsable ip_address %q", forged, evt.IPAddress)
		}
	}
}

// Pin event-type wire values. These strings end up in the DB and in the
// web/desktop UI's i18n keys — changing one is a breaking change. Adding a
// new event? Add the constant here AND a translation in
// packages/i18n/src/locales/{de,en}.json (covered by keyParity test).
func TestEventTypeWireValues(t *testing.T) {
	cases := map[string]string{
		"EventLoginSuccess":         "auth.login.success",
		"EventLoginFail":            "auth.login.fail",
		"EventLoginLockout":         "auth.login.lockout",
		"EventLogout":               "auth.logout",
		"EventTokenRefresh":         "auth.token_refresh",
		"EventPasswordChanged":      "auth.password_changed",
		"EventPasswordReset":        "auth.password_reset",
		"EventUsernameChanged":      "auth.username_changed",
		"EventEmailChangeRequested": "auth.email_change_requested",
		"EventEmailChanged":         "auth.email_changed",
		"Event2FAEnabled":           "auth.2fa_enabled",
		"Event2FADisabled":          "auth.2fa_disabled",
		"EventFileDeleted":          "files.deleted",
		"EventFileOfficeSaved":      "files.office_saved",
		"EventShareCreated":         "shares.created",
		"EventShareUpdated":         "shares.updated",
		"EventShareDeleted":         "shares.deleted",
		"EventAdminUserCreated":     "admin.user_created",
		"EventAdminUserUpdated":     "admin.user_updated",
		"EventAdminUserDeleted":     "admin.user_deleted",
		"EventAdminQuotaSet":        "admin.quota_set",
		"EventAdminQuotaRecomputed": "admin.quota_recomputed",
		"EventAdminUserUnlocked":    "admin.user_unlocked",
	}
	actual := map[string]string{
		"EventLoginSuccess":         EventLoginSuccess,
		"EventLoginFail":            EventLoginFail,
		"EventLoginLockout":         EventLoginLockout,
		"EventLogout":               EventLogout,
		"EventTokenRefresh":         EventTokenRefresh,
		"EventPasswordChanged":      EventPasswordChanged,
		"EventPasswordReset":        EventPasswordReset,
		"EventUsernameChanged":      EventUsernameChanged,
		"EventEmailChangeRequested": EventEmailChangeRequested,
		"EventEmailChanged":         EventEmailChanged,
		"Event2FAEnabled":           Event2FAEnabled,
		"Event2FADisabled":          Event2FADisabled,
		"EventFileDeleted":          EventFileDeleted,
		"EventFileOfficeSaved":      EventFileOfficeSaved,
		"EventShareCreated":         EventShareCreated,
		"EventShareUpdated":         EventShareUpdated,
		"EventShareDeleted":         EventShareDeleted,
		"EventAdminUserCreated":     EventAdminUserCreated,
		"EventAdminUserUpdated":     EventAdminUserUpdated,
		"EventAdminUserDeleted":     EventAdminUserDeleted,
		"EventAdminQuotaSet":        EventAdminQuotaSet,
		"EventAdminQuotaRecomputed": EventAdminQuotaRecomputed,
		"EventAdminUserUnlocked":    EventAdminUserUnlocked,
	}
	for name, want := range cases {
		if got := actual[name]; got != want {
			t.Errorf("%s: got %q want %q", name, got, want)
		}
	}
}

func TestFromRequest_HandlesMissingHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "" // unparseable

	evt := FromRequest(req, nil, EventLoginFail, nil)
	if evt.IPAddress != "" {
		t.Errorf("empty addr should yield empty ip, got %q", evt.IPAddress)
	}
	if evt.UserAgent != "" {
		t.Errorf("missing ua should yield empty, got %q", evt.UserAgent)
	}
}
