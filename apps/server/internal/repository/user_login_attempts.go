package repository

import (
	"context"
	"time"
)

func (r *UserRepository) RecordLoginAttempt(ctx context.Context, username, ip string, succeeded bool) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO login_attempts (username, ip_address, succeeded) VALUES ($1, $2::inet, $3)`,
		username, ip, succeeded,
	)
	return err
}

func (r *UserRepository) CountFailedAttempts(ctx context.Context, username string, window time.Duration) (int, error) {
	var count int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM login_attempts
		WHERE username = $1 AND succeeded = FALSE AND created_at > NOW() - $2::interval
	`, username, window.String()).Scan(&count)
	return count, err
}

func (r *UserRepository) ClearFailedAttempts(ctx context.Context, username string) error {
	_, err := r.db.Exec(ctx,
		`DELETE FROM login_attempts WHERE username = $1 AND succeeded = FALSE`,
		username,
	)
	return err
}

func (r *UserRepository) FailedAttemptsByUser(ctx context.Context, window time.Duration) (map[string]int, error) {
	rows, err := r.db.Query(ctx, `
		SELECT username, COUNT(*) FROM login_attempts
		WHERE succeeded = FALSE AND created_at > NOW() - $1::interval
		GROUP BY username
	`, window.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var username string
		var count int
		if err := rows.Scan(&username, &count); err != nil {
			return nil, err
		}
		counts[username] = count
	}
	return counts, rows.Err()
}
