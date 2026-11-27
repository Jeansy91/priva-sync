package repository

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/model"
)

// appPasswordEntropyBytes sizes the generated secret. 20 bytes is 160 bits,
// far past guessing range, which is what lets the lookup use a plain SHA-256
// hash instead of a slow KDF — same trade-off the backup codes make, and the
// reason a WebDAV request costs one indexed lookup rather than an Argon2id
// pass per stored credential.
const appPasswordEntropyBytes = 20

type AppPasswordRepository struct {
	db *pgxpool.Pool
}

func NewAppPasswordRepository(db *pgxpool.Pool) *AppPasswordRepository {
	return &AppPasswordRepository{db: db}
}

// Create issues a new app password and returns it together with its plaintext
// secret. The secret is not recoverable afterwards — only its hash is stored,
// so the caller must show it to the user right away.
func (r *AppPasswordRepository) Create(ctx context.Context, userID uuid.UUID, name string) (*model.AppPassword, string, error) {
	secret, err := newAppPasswordSecret()
	if err != nil {
		return nil, "", err
	}
	var ap model.AppPassword
	err = r.db.QueryRow(ctx, `
		INSERT INTO app_passwords (user_id, name, secret_hash)
		VALUES ($1, $2, $3)
		RETURNING id, user_id, name, secret_hash, last_used_at, created_at
	`, userID, name, hashToken(secret)).
		Scan(&ap.ID, &ap.UserID, &ap.Name, &ap.SecretHash, &ap.LastUsedAt, &ap.CreatedAt)
	if err != nil {
		return nil, "", fmt.Errorf("create app password: %w", err)
	}
	return &ap, secret, nil
}

// FindBySecret resolves a presented secret to its stored credential, or
// ErrNotFound when nothing matches.
func (r *AppPasswordRepository) FindBySecret(ctx context.Context, secret string) (*model.AppPassword, error) {
	if secret == "" {
		return nil, ErrNotFound
	}
	var ap model.AppPassword
	err := r.db.QueryRow(ctx, `
		SELECT id, user_id, name, secret_hash, last_used_at, created_at
		FROM app_passwords WHERE secret_hash = $1
	`, hashToken(secret)).
		Scan(&ap.ID, &ap.UserID, &ap.Name, &ap.SecretHash, &ap.LastUsedAt, &ap.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &ap, err
}

func (r *AppPasswordRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]model.AppPassword, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, name, secret_hash, last_used_at, created_at
		FROM app_passwords WHERE user_id = $1 ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.AppPassword, 0, 4)
	for rows.Next() {
		var ap model.AppPassword
		if err := rows.Scan(&ap.ID, &ap.UserID, &ap.Name, &ap.SecretHash, &ap.LastUsedAt, &ap.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ap)
	}
	return out, rows.Err()
}

// Delete revokes one app password. Scoped to the owner so an id alone is not
// enough to revoke someone else's credential.
func (r *AppPasswordRepository) Delete(ctx context.Context, id, userID uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM app_passwords WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchLastUsed records that the credential was just accepted, so the owner
// can tell a live device from a forgotten one before revoking it.
func (r *AppPasswordRepository) TouchLastUsed(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE app_passwords SET last_used_at = NOW() WHERE id = $1`, id)
	return err
}

// newAppPasswordSecret returns a secret in xxxx-xxxx-xxxx-xxxx-xxxx-xxxx-xxxx-xxxx
// form: lower-case base32 without padding, grouped for transcription. Base32
// avoids the case-folding and look-alike trouble a user hits when typing this
// into a file manager's credential dialog.
func newAppPasswordSecret() (string, error) {
	raw := make([]byte, appPasswordEntropyBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating app password: %w", err)
	}
	enc := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))

	var b strings.Builder
	for i := 0; i < len(enc); i += 4 {
		if i > 0 {
			b.WriteByte('-')
		}
		b.WriteString(enc[i:min(i+4, len(enc))])
	}
	return b.String(), nil
}
