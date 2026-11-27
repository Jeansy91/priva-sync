package model

import (
	"time"

	"github.com/google/uuid"
)

// PasskeyCredential is a stored WebAuthn credential record. Credential holds
// the JSON-marshaled webauthn.Credential (public key, sign count, flags,
// transports, AAGUID, attestation) as-is — storing the whole struct instead
// of decomposed columns avoids drift against the go-webauthn library's
// internal field layout.
type PasskeyCredential struct {
	ID           uuid.UUID  `db:"id"            json:"id"`
	UserID       uuid.UUID  `db:"user_id"       json:"-"`
	CredentialID []byte     `db:"credential_id" json:"-"`
	Credential   []byte     `db:"credential"    json:"-"`
	Name         string     `db:"name"          json:"name"`
	CreatedAt    time.Time  `db:"created_at"    json:"created_at"`
	LastUsedAt   *time.Time `db:"last_used_at"  json:"last_used_at"`
}
