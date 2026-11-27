package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"privasync/server/internal/model"
)

// ─── Refresh Tokens ──────────────────────────────────────────────────────────

func (r *UserRepository) CreateRefreshToken(ctx context.Context, userID uuid.UUID, token string, expiresAt time.Time) error {
	hash := hashToken(token)
	_, err := r.db.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, hash, expiresAt,
	)
	return err
}

func (r *UserRepository) FindRefreshToken(ctx context.Context, token string) (*model.RefreshToken, error) {
	hash := hashToken(token)
	var t model.RefreshToken
	err := r.db.QueryRow(ctx,
		`SELECT id, user_id, token_hash, expires_at, rotated_at, created_at
		 FROM refresh_tokens WHERE token_hash = $1`,
		hash,
	).Scan(&t.ID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &t.RotatedAt, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}

// MarkRefreshTokenRotated retires a token without dropping it: within the
// grace window it still authenticates a refresh, it just cannot rotate again.
func (r *UserRepository) MarkRefreshTokenRotated(ctx context.Context, token string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE refresh_tokens SET rotated_at = NOW() WHERE token_hash = $1 AND rotated_at IS NULL`,
		hashToken(token),
	)
	return err
}

// DeleteRotatedRefreshTokens drops this user's retired tokens once their grace
// window has passed. Called after each rotation, so retired rows never
// accumulate for the whole refresh-token TTL.
func (r *UserRepository) DeleteRotatedRefreshTokens(ctx context.Context, userID uuid.UUID, grace time.Duration) error {
	_, err := r.db.Exec(ctx,
		`DELETE FROM refresh_tokens
		 WHERE user_id = $1 AND rotated_at < NOW() - make_interval(secs => $2)`,
		userID, grace.Seconds(),
	)
	return err
}

func (r *UserRepository) DeleteRefreshToken(ctx context.Context, token string) error {
	hash := hashToken(token)
	_, err := r.db.Exec(ctx, `DELETE FROM refresh_tokens WHERE token_hash = $1`, hash)
	return err
}

func (r *UserRepository) DeleteAllRefreshTokens(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM refresh_tokens WHERE user_id = $1`, userID)
	return err
}

// ─── Password Reset Tokens ────────────────────────────────────────────────────

func (r *UserRepository) CreatePasswordResetToken(ctx context.Context, userID uuid.UUID, token string, expiresAt time.Time) error {
	hash := hashToken(token)
	_, err := r.db.Exec(ctx,
		`INSERT INTO password_reset_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, hash, expiresAt,
	)
	return err
}

// FindPasswordResetToken returns the token record regardless of expiry /
// used-state. The caller must enforce those checks — keeping the query
// dumb lets the handler distinguish "expired" from "already used".
func (r *UserRepository) FindPasswordResetToken(ctx context.Context, token string) (*model.PasswordResetToken, error) {
	hash := hashToken(token)
	var t model.PasswordResetToken
	err := r.db.QueryRow(ctx,
		`SELECT id, user_id, token_hash, expires_at, used_at, created_at
		 FROM password_reset_tokens WHERE token_hash = $1`,
		hash,
	).Scan(&t.ID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &t.UsedAt, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}

func (r *UserRepository) MarkPasswordResetTokenUsed(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx,
		`UPDATE password_reset_tokens SET used_at = NOW() WHERE id = $1`,
		id,
	)
	return err
}

// ─── Email Change Tokens ──────────────────────────────────────────────────────
//
// Pending email-changes: the row holds the *new* address until the user
// confirms via the link. CreateEmailChangeToken replaces any prior pending
// change for the same user so a fresh request invalidates older mails.

func (r *UserRepository) CreateEmailChangeToken(ctx context.Context, userID uuid.UUID, newEmail, token string, expiresAt time.Time) error {
	hash := hashToken(token)
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM email_change_tokens WHERE user_id = $1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO email_change_tokens (user_id, new_email, token_hash, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		userID, newEmail, hash, expiresAt,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ConsumeEmailChangeToken returns the matching token record and deletes it
// atomically — replay of the confirm link is therefore impossible. Returns
// ErrNotFound when the token is unknown or expired.
func (r *UserRepository) ConsumeEmailChangeToken(ctx context.Context, token string) (*model.EmailChangeToken, error) {
	hash := hashToken(token)
	var t model.EmailChangeToken
	err := r.db.QueryRow(ctx, `
		DELETE FROM email_change_tokens
		WHERE token_hash = $1 AND expires_at > NOW()
		RETURNING id, user_id, new_email, token_hash, expires_at, created_at
	`, hash).Scan(&t.ID, &t.UserID, &t.NewEmail, &t.TokenHash, &t.ExpiresAt, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// CountEmailChangeTokens — Diagnose/Tests.
func (r *UserRepository) CountEmailChangeTokens(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM email_change_tokens WHERE user_id = $1`, userID,
	).Scan(&n)
	return n, err
}
