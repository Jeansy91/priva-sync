package repository

import (
	"context"

	"github.com/google/uuid"

	"privasync/server/internal/model"
)

func (r *UserRepository) UpdateTOTP(ctx context.Context, userID uuid.UUID, secret *string, enabled bool) error {
	var u model.User
	err := scanUser(r.db.QueryRow(ctx, `
		UPDATE users SET totp_secret = $1, totp_enabled = $2, updated_at = NOW()
		WHERE id = $3
		RETURNING `+userColumns,
		secret, enabled, userID,
	), &u)
	if err != nil {
		return err
	}
	r.writeMeta(&u)
	return nil
}

func (r *UserRepository) UpdateUsername(ctx context.Context, userID uuid.UUID, username string) error {
	var u model.User
	err := scanUser(r.db.QueryRow(ctx, `
		UPDATE users SET username = $1, updated_at = NOW()
		WHERE id = $2
		RETURNING `+userColumns,
		username, userID,
	), &u)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return err
	}
	r.writeMeta(&u)
	return nil
}

// UpdateEmail persistiert eine neue Mailadresse. Eindeutigkeit wird per
// UNIQUE-Constraint erzwungen; bei Konflikt liefert die Funktion ErrConflict.
func (r *UserRepository) UpdateEmail(ctx context.Context, userID uuid.UUID, email string) error {
	var u model.User
	err := scanUser(r.db.QueryRow(ctx, `
		UPDATE users SET email = $1, updated_at = NOW()
		WHERE id = $2
		RETURNING `+userColumns,
		email, userID,
	), &u)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return err
	}
	r.writeMeta(&u)
	return nil
}

func (r *UserRepository) UpdatePassword(ctx context.Context, userID uuid.UUID, hash string) error {
	var u model.User
	err := scanUser(r.db.QueryRow(ctx, `
		UPDATE users SET password_hash = $1, updated_at = NOW()
		WHERE id = $2
		RETURNING `+userColumns,
		hash, userID,
	), &u)
	if err != nil {
		return err
	}
	r.writeMeta(&u)
	return nil
}

// SetAvatarPath setzt den Avatar-Pfad relativ zu {data_path}/avatars/.
func (r *UserRepository) SetAvatarPath(ctx context.Context, userID uuid.UUID, path string) error {
	var u model.User
	err := scanUser(r.db.QueryRow(ctx, `
		UPDATE users SET avatar_path = $1, updated_at = NOW()
		WHERE id = $2
		RETURNING `+userColumns,
		path, userID,
	), &u)
	if err != nil {
		return err
	}
	r.writeMeta(&u)
	return nil
}

// SetPreferredLanguage stores the user's UI/mail language. A CHECK
// constraint enforces 'de' or 'en' only.
func (r *UserRepository) SetPreferredLanguage(ctx context.Context, userID uuid.UUID, lang string) error {
	var u model.User
	err := scanUser(r.db.QueryRow(ctx, `
		UPDATE users SET preferred_language = $1, updated_at = NOW()
		WHERE id = $2
		RETURNING `+userColumns,
		lang, userID,
	), &u)
	if err != nil {
		return err
	}
	r.writeMeta(&u)
	return nil
}

// SetShowFileTree toggles the file-tree sidebar visibility for the web
// frontend. Persisted per user so the toggle applies across devices and
// sessions.
func (r *UserRepository) SetShowFileTree(ctx context.Context, userID uuid.UUID, show bool) error {
	var u model.User
	err := scanUser(r.db.QueryRow(ctx, `
		UPDATE users SET show_file_tree = $1, updated_at = NOW()
		WHERE id = $2
		RETURNING `+userColumns,
		show, userID,
	), &u)
	if err != nil {
		return err
	}
	r.writeMeta(&u)
	return nil
}

// SetShowFileCheckboxes toggles the multi-select checkbox column / overlay in
// the web file browser. Persisted per user so the toggle applies across
// devices and sessions.
func (r *UserRepository) SetShowFileCheckboxes(ctx context.Context, userID uuid.UUID, show bool) error {
	var u model.User
	err := scanUser(r.db.QueryRow(ctx, `
		UPDATE users SET show_file_checkboxes = $1, updated_at = NOW()
		WHERE id = $2
		RETURNING `+userColumns,
		show, userID,
	), &u)
	if err != nil {
		return err
	}
	r.writeMeta(&u)
	return nil
}

// SetTrashView persistiert die bevorzugte Papierkorb-Ansicht pro User
// ('list' oder 'tree'). CHECK-Constraint auf der Spalte filtert ungueltige
// Werte serverseitig.
func (r *UserRepository) SetTrashView(ctx context.Context, userID uuid.UUID, view string) error {
	var u model.User
	err := scanUser(r.db.QueryRow(ctx, `
		UPDATE users SET trash_view = $1, updated_at = NOW()
		WHERE id = $2
		RETURNING `+userColumns,
		view, userID,
	), &u)
	if err != nil {
		return err
	}
	r.writeMeta(&u)
	return nil
}

// ClearAvatarPath setzt avatar_path auf NULL.
func (r *UserRepository) ClearAvatarPath(ctx context.Context, userID uuid.UUID) error {
	var u model.User
	err := scanUser(r.db.QueryRow(ctx, `
		UPDATE users SET avatar_path = NULL, updated_at = NOW()
		WHERE id = $1
		RETURNING `+userColumns,
		userID,
	), &u)
	if err != nil {
		return err
	}
	r.writeMeta(&u)
	return nil
}
