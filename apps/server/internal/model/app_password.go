package model

import (
	"time"

	"github.com/google/uuid"
)

// AppPassword is a per-device credential for WebDAV. Basic Auth cannot ask
// for a second factor, so the account password is not accepted there; an app
// password stands in and can be revoked on its own without touching the
// account (ADR-0040).
//
// The secret itself never leaves the server after creation — only its
// SHA-256 hash is stored, and the plaintext is shown to the user exactly once.
type AppPassword struct {
	ID         uuid.UUID  `db:"id"           json:"id"`
	UserID     uuid.UUID  `db:"user_id"      json:"-"`
	Name       string     `db:"name"         json:"name"`
	SecretHash string     `db:"secret_hash"  json:"-"`
	LastUsedAt *time.Time `db:"last_used_at" json:"last_used_at"`
	CreatedAt  time.Time  `db:"created_at"   json:"created_at"`
}
