package model

import (
	"time"

	"github.com/google/uuid"
)

type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

type User struct {
	ID             uuid.UUID `db:"id"`
	Username       string    `db:"username"`
	Email          string    `db:"email"`
	PasswordHash   string    `db:"password_hash"`
	TOTPSecret     *string   `db:"totp_secret"`
	TOTPEnabled    bool      `db:"totp_enabled"`
	Role           Role      `db:"role"`
	QuotaBytes     int64     `db:"quota_bytes"`
	UsedBytes      int64     `db:"used_bytes"`
	TrashBytes     int64     `db:"trash_bytes"`
	QuotaState     string    `db:"quota_state"`
	IsActive          bool      `db:"is_active"`
	EmailNotify       bool      `db:"email_notify"`
	AvatarPath        *string   `db:"avatar_path"`
	PreferredLanguage string    `db:"preferred_language"`
	ShowFileTree       bool      `db:"show_file_tree"`
	ShowFileCheckboxes bool      `db:"show_file_checkboxes"`
	TrashView          string    `db:"trash_view"`
	CreatedAt         time.Time `db:"created_at"`
	UpdatedAt         time.Time `db:"updated_at"`
}

type Client struct {
	ID            uuid.UUID  `db:"id"`
	UserID        uuid.UUID  `db:"user_id"`
	DeviceID      string     `db:"device_id"`
	Name          string     `db:"name"`
	Platform      string     `db:"platform"`
	OSVersion     *string    `db:"os_version"`
	ClientVersion *string    `db:"client_version"`
	LastIP        *string    `db:"last_ip"`
	LastSeen      *time.Time `db:"last_seen"`
	CreatedAt     time.Time  `db:"created_at"`
}

// ClientWithUser is the admin-view projection of a client with its owning
// user's display name. Username is joined in for the admin overview.
type ClientWithUser struct {
	Client
	Username string `db:"username"`
}

// UserSummary is the minimal owner/modifier projection embedded into file
// listings and detail panels (ADR-0009 / ADR-0013). AvatarPath is nil when
// the user has no avatar uploaded.
type UserSummary struct {
	ID         uuid.UUID `db:"id"`
	Username   string    `db:"username"`
	AvatarPath *string   `db:"avatar_path"`
}

// RotatedAt is nil for the current token of a session. A rotated token stays
// usable for the configured grace window, so parallel refreshes from one
// client do not knock each other out (ADR-0046).
type RefreshToken struct {
	ID        uuid.UUID  `db:"id"`
	UserID    uuid.UUID  `db:"user_id"`
	TokenHash string     `db:"token_hash"`
	ExpiresAt time.Time  `db:"expires_at"`
	RotatedAt *time.Time `db:"rotated_at"`
	CreatedAt time.Time  `db:"created_at"`
}

type PasswordResetToken struct {
	ID        uuid.UUID  `db:"id"`
	UserID    uuid.UUID  `db:"user_id"`
	TokenHash string     `db:"token_hash"`
	ExpiresAt time.Time  `db:"expires_at"`
	UsedAt    *time.Time `db:"used_at"`
	CreatedAt time.Time  `db:"created_at"`
}

type EmailChangeToken struct {
	ID        uuid.UUID `db:"id"`
	UserID    uuid.UUID `db:"user_id"`
	NewEmail  string    `db:"new_email"`
	TokenHash string    `db:"token_hash"`
	ExpiresAt time.Time `db:"expires_at"`
	CreatedAt time.Time `db:"created_at"`
}

type BackupCode struct {
	ID        uuid.UUID  `db:"id"         json:"id"`
	UserID    uuid.UUID  `db:"user_id"    json:"-"`
	CodeHash  string     `db:"code_hash"  json:"-"`
	UsedAt    *time.Time `db:"used_at"    json:"used_at"`
	CreatedAt time.Time  `db:"created_at" json:"created_at"`
}
