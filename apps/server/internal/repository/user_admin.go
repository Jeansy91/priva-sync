package repository

import (
	"context"

	"github.com/google/uuid"

	"privasync/server/internal/model"
)

func (r *UserRepository) SetActive(ctx context.Context, userID uuid.UUID, active bool) error {
	var u model.User
	err := scanUser(r.db.QueryRow(ctx, `
		UPDATE users SET is_active = $1, updated_at = NOW()
		WHERE id = $2
		RETURNING `+userColumns,
		active, userID,
	), &u)
	if err != nil {
		return err
	}
	r.writeMeta(&u)
	return nil
}

func (r *UserRepository) SetRole(ctx context.Context, userID uuid.UUID, role model.Role) error {
	var u model.User
	err := scanUser(r.db.QueryRow(ctx, `
		UPDATE users SET role = $1, updated_at = NOW()
		WHERE id = $2
		RETURNING `+userColumns,
		role, userID,
	), &u)
	if err != nil {
		return err
	}
	r.writeMeta(&u)
	return nil
}

func (r *UserRepository) SetQuota(ctx context.Context, userID uuid.UUID, quotaBytes int64) error {
	var u model.User
	err := scanUser(r.db.QueryRow(ctx, `
		UPDATE users SET quota_bytes = $1, updated_at = NOW()
		WHERE id = $2
		RETURNING `+userColumns,
		quotaBytes, userID,
	), &u)
	if err != nil {
		return err
	}
	r.writeMeta(&u)
	return nil
}
