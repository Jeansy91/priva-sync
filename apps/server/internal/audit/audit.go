// Package audit produces and persists structured audit-log records for
// security-relevant events. The DB schema lives in migration 001 (table
// `audit_log`). Repository operations are in repository.go; event helpers
// here are pure so they remain trivially testable.
package audit

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/httpx"
)

// Event-type constants — strings are stable and end up in the DB. Add new
// events here; do not scatter them ad-hoc through handler code.
const (
	EventLoginSuccess         = "auth.login.success"
	EventLoginFail            = "auth.login.fail"
	EventLoginLockout         = "auth.login.lockout"
	EventLogout               = "auth.logout"
	EventPasswordChanged      = "auth.password_changed"
	EventUsernameChanged      = "auth.username_changed"
	EventEmailChangeRequested = "auth.email_change_requested"
	EventEmailChanged         = "auth.email_changed"
	EventPasswordReset        = "auth.password_reset"
	Event2FAEnabled           = "auth.2fa_enabled"
	Event2FADisabled          = "auth.2fa_disabled"
	EventTokenRefresh         = "auth.token_refresh"      //#nosec G101 -- event-type name, not a credential
	EventPasskeyRegistered    = "auth.passkey_registered" //#nosec G101 -- event-type name, not a credential
	EventPasskeyRemoved       = "auth.passkey_removed"    //#nosec G101 -- event-type name, not a credential
	EventPasskeyLogin         = "auth.passkey_login"      //#nosec G101 -- event-type name, not a credential
	EventFileDeleted          = "files.deleted"
	EventFileOfficeSaved      = "files.office_saved"
	EventShareCreated         = "shares.created"
	EventShareUpdated         = "shares.updated"
	EventShareDeleted         = "shares.deleted"
	EventAdminUserCreated     = "admin.user_created"
	EventAdminUserUpdated     = "admin.user_updated"
	EventAdminUserDeleted     = "admin.user_deleted"
	EventAdminQuotaSet        = "admin.quota_set"
	EventAdminQuotaRecomputed = "admin.quota_recomputed"
	EventAdminUserUnlocked    = "admin.user_unlocked"
)

// Event is the normalised in-memory form of an audit record before persist.
// `UserID` may be nil, e.g. for failed logins.
type Event struct {
	ID        uuid.UUID      `json:"id"`
	UserID    *uuid.UUID     `json:"user_id"`
	EventType string         `json:"event_type"`
	IPAddress string         `json:"ip_address"`
	UserAgent string         `json:"user_agent"`
	Details   map[string]any `json:"details"`
	CreatedAt time.Time      `json:"created_at"`
}

// FromRequest builds an event skeleton from an HTTP request. The client
// address comes from httpx.ClientIP, which applies the trusted-proxy policy
// and always yields a validated value: audit_log.ip_address is an INET
// column, so an unvalidated header value would fail the INSERT and make the
// audit entry disappear silently.
func FromRequest(r *http.Request, userID *uuid.UUID, eventType string, details map[string]any) Event {
	return Event{
		UserID:    userID,
		EventType: eventType,
		IPAddress: httpx.ClientIP(r),
		UserAgent: r.Header.Get("User-Agent"),
		Details:   details,
	}
}
