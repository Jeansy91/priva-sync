package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/model"
)

// PasskeyRepository owns passkey_credentials, kept separate from
// UserRepository per ADR-0001 (repository-aggregate-split).
type PasskeyRepository struct {
	db *pgxpool.Pool
}

func NewPasskeyRepository(db *pgxpool.Pool) *PasskeyRepository {
	return &PasskeyRepository{db: db}
}

func (r *PasskeyRepository) Create(ctx context.Context, userID uuid.UUID, credentialID, credential []byte, name string) (*model.PasskeyCredential, error) {
	var c model.PasskeyCredential
	err := r.db.QueryRow(ctx, `
		INSERT INTO passkey_credentials (user_id, credential_id, credential, name)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, credential_id, credential, name, created_at, last_used_at
	`, userID, credentialID, credential, name).Scan(
		&c.ID, &c.UserID, &c.CredentialID, &c.Credential, &c.Name, &c.CreatedAt, &c.LastUsedAt,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *PasskeyRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]model.PasskeyCredential, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, credential_id, credential, name, created_at, last_used_at
		FROM passkey_credentials WHERE user_id = $1 ORDER BY created_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.PasskeyCredential
	for rows.Next() {
		var c model.PasskeyCredential
		if err := rows.Scan(&c.ID, &c.UserID, &c.CredentialID, &c.Credential, &c.Name, &c.CreatedAt, &c.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// FindByCredentialID looks up the owning user's id for a discoverable-login
// credential lookup (WebAuthn rawID -> owner). Returns ErrNotFound if no
// credential with this ID is registered.
func (r *PasskeyRepository) FindByCredentialID(ctx context.Context, credentialID []byte) (uuid.UUID, error) {
	var userID uuid.UUID
	err := r.db.QueryRow(ctx, `
		SELECT user_id FROM passkey_credentials WHERE credential_id = $1
	`, credentialID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return userID, err
}

// UpdateSignCount persists the post-login credential state (sign count via
// the re-marshaled `credential` blob, plus last_used_at) — must run after
// every successful login per the WebAuthn spec's clone-detection guidance.
// Keyed by credential_id (the WebAuthn credential's own ID, already at hand
// from the validated login response) rather than our row's uuid.
func (r *PasskeyRepository) UpdateSignCount(ctx context.Context, credentialID, credential []byte) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE passkey_credentials SET credential = $2, last_used_at = NOW() WHERE credential_id = $1
	`, credentialID, credential)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a credential, scoped to its owner so a user can never
// remove another user's passkey.
func (r *PasskeyRepository) Delete(ctx context.Context, id, userID uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `
		DELETE FROM passkey_credentials WHERE id = $1 AND user_id = $2
	`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
