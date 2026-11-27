package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/model"
	"privasync/server/internal/storage"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("already exists")

type UserRepository struct {
	db   *pgxpool.Pool
	stor *storage.Service
}

func NewUserRepository(db *pgxpool.Pool, stor *storage.Service) *UserRepository {
	return &UserRepository{db: db, stor: stor}
}

// writeMeta persists the user's credentials to disk.  Errors are logged but
// never propagated — the database write already succeeded.
func (r *UserRepository) writeMeta(u *model.User) {
	if err := r.stor.WriteUserMeta(u); err != nil {
		slog.Warn("user meta write failed", "user_id", u.ID, "error", err)
	}
}

const userColumns = `id, username, email, password_hash, totp_secret, totp_enabled,
	role, quota_bytes, used_bytes, trash_bytes, quota_state,
	is_active, email_notify, avatar_path, preferred_language, show_file_tree,
	show_file_checkboxes, trash_view, created_at, updated_at`

func (r *UserRepository) findOne(ctx context.Context, query string, args ...any) (*model.User, error) {
	row := r.db.QueryRow(ctx, query, args...)
	var u model.User
	if err := scanUser(row, &u); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

func scanUser(row interface {
	Scan(dest ...any) error
}, u *model.User) error {
	return row.Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash,
		&u.TOTPSecret, &u.TOTPEnabled, &u.Role,
		&u.QuotaBytes, &u.UsedBytes, &u.TrashBytes,
		&u.QuotaState,
		&u.IsActive, &u.EmailNotify, &u.AvatarPath, &u.PreferredLanguage,
		&u.ShowFileTree, &u.ShowFileCheckboxes, &u.TrashView,
		&u.CreatedAt, &u.UpdatedAt,
	)
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}
