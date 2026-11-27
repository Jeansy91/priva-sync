package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"privasync/server/internal/model"
)

// Codes sind hoch-entrope Zufallstokens (>= 64 bit). SHA-256 reicht — Argon2id
// waere Overkill und wuerde pro Login ~1 s kosten (bis zu 10 Vergleiche).

func (r *UserRepository) ReplaceBackupCodes(ctx context.Context, userID uuid.UUID, plaintextCodes []string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM backup_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, code := range plaintextCodes {
		if _, err := tx.Exec(ctx,
			`INSERT INTO backup_codes (user_id, code_hash) VALUES ($1, $2)`,
			userID, hashToken(code),
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *UserRepository) ListBackupCodes(ctx context.Context, userID uuid.UUID) ([]model.BackupCode, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, user_id, code_hash, used_at, created_at
		 FROM backup_codes WHERE user_id = $1 ORDER BY created_at ASC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.BackupCode
	for rows.Next() {
		var c model.BackupCode
		if err := rows.Scan(&c.ID, &c.UserID, &c.CodeHash, &c.UsedAt, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ConsumeBackupCode marks an unused code as spent. Returns ErrNotFound if
// the code is unknown or already used — atomic via UPDATE ... RETURNING so
// two parallel logins can't burn the same code.
func (r *UserRepository) ConsumeBackupCode(ctx context.Context, userID uuid.UUID, plaintext string) error {
	hash := hashToken(plaintext)
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `
		UPDATE backup_codes SET used_at = NOW()
		WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL
		RETURNING id
	`, userID, hash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
