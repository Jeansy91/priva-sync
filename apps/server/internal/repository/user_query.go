package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"privasync/server/internal/model"
)

func (r *UserRepository) Create(ctx context.Context, username, email, passwordHash string, role model.Role) (*model.User, error) {
	var u model.User
	err := scanUser(r.db.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, role)
		VALUES ($1, $2, $3, $4)
		RETURNING `+userColumns,
		username, email, passwordHash, role,
	), &u)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("creating user: %w", err)
	}
	r.writeMeta(&u)
	return &u, nil
}

func (r *UserRepository) FindByUsername(ctx context.Context, username string) (*model.User, error) {
	return r.findOne(ctx, `SELECT `+userColumns+` FROM users WHERE username = $1`, username)
}

func (r *UserRepository) FindByID(ctx context.Context, id uuid.UUID) (*model.User, error) {
	return r.findOne(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id)
}

// FindSummariesByIDs batches a `id, username, avatar_path` lookup for the
// given set of user IDs. Missing IDs are simply absent from the returned
// map. Empty input is a no-op. Used by file-listing handlers to embed
// last_modifier without joining users into every listing query (ADR-0013).
func (r *UserRepository) FindSummariesByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]model.UserSummary, error) {
	out := make(map[uuid.UUID]model.UserSummary, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx,
		`SELECT id, username, avatar_path FROM users WHERE id = ANY($1)`, ids,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s model.UserSummary
		if err := rows.Scan(&s.ID, &s.Username, &s.AvatarPath); err != nil {
			return nil, err
		}
		out[s.ID] = s
	}
	return out, rows.Err()
}

// SearchByUsernamePrefix returns active users with a case-insensitive prefix
// match on username, excludeID excluded, capped by limit. Sorted by username
// for a stable UI display.
func (r *UserRepository) SearchByUsernamePrefix(ctx context.Context, prefix string, excludeID uuid.UUID, limit int) ([]model.User, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+userColumns+` FROM users
		WHERE is_active = TRUE
		  AND id <> $1
		  AND username ILIKE $2 || '%'
		ORDER BY username ASC
		LIMIT $3
	`, excludeID, prefix, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.User
	for rows.Next() {
		var u model.User
		if err := scanUser(rows, &u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// FindByEmail performs a case-insensitive lookup. Used by the
// password-reset flow where the user types their email manually.
func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*model.User, error) {
	return r.findOne(ctx, `SELECT `+userColumns+` FROM users WHERE LOWER(email) = LOWER($1)`, email)
}

// HasAdmin reports whether at least one active user with role='admin' exists.
func (r *UserRepository) HasAdmin(ctx context.Context) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM users WHERE role = 'admin' AND is_active = TRUE)
	`).Scan(&exists)
	return exists, err
}

func (r *UserRepository) List(ctx context.Context) ([]model.User, error) {
	rows, err := r.db.Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []model.User
	for rows.Next() {
		var u model.User
		if err := scanUser(rows, &u); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}
