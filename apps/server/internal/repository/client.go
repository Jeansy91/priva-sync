package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/model"
)

// ClientRepository persists per-device client identification records used by
// the admin overview (which user, OS, version, IP, last seen).
type ClientRepository struct {
	db *pgxpool.Pool
}

func NewClientRepository(db *pgxpool.Pool) *ClientRepository {
	return &ClientRepository{db: db}
}

// UpsertClientInput identifies a client at login time. DeviceID is the stable
// per-installation identifier the client persists locally.
type UpsertClientInput struct {
	UserID        uuid.UUID
	DeviceID      string
	Name          string
	Platform      string
	OSVersion     *string
	ClientVersion *string
	LastIP        *string
}

const clientColumns = `c.id, c.user_id, c.device_id, c.name, c.platform, c.os_version, c.client_version, host(c.last_ip) AS last_ip, c.last_seen, c.created_at`

// Upsert inserts a row matching (user_id, device_id) or updates the existing
// one. last_seen is set to NOW() so the admin overview can rank by recency.
func (r *ClientRepository) Upsert(ctx context.Context, in UpsertClientInput) (*model.Client, error) {
	var c model.Client
	err := r.db.QueryRow(ctx, `
		WITH c AS (
			INSERT INTO clients (user_id, device_id, name, platform, os_version, client_version, last_ip, last_seen)
			VALUES ($1, $2, $3, $4, $5, $6, $7::inet, NOW())
			ON CONFLICT (user_id, device_id) DO UPDATE SET
				name           = EXCLUDED.name,
				platform       = EXCLUDED.platform,
				os_version     = EXCLUDED.os_version,
				client_version = EXCLUDED.client_version,
				last_ip        = EXCLUDED.last_ip,
				last_seen      = NOW()
			RETURNING *
		)
		SELECT `+clientColumns+` FROM c`,
		in.UserID, in.DeviceID, in.Name, in.Platform,
		in.OSVersion, in.ClientVersion, in.LastIP,
	).Scan(
		&c.ID, &c.UserID, &c.DeviceID, &c.Name, &c.Platform,
		&c.OSVersion, &c.ClientVersion, &c.LastIP, &c.LastSeen, &c.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert client: %w", err)
	}
	return &c, nil
}

// TouchLastSeen advances last_seen (and last_ip if non-nil) on the row
// matching (user_id, device_id). Refresh-time heartbeat for the admin
// overview. Returns ErrNotFound when no row exists yet (legitimate during
// rollout while older clients haven't re-logged in).
func (r *ClientRepository) TouchLastSeen(ctx context.Context, userID uuid.UUID, deviceID string, ip *string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE clients
		   SET last_seen = NOW(),
		       last_ip   = COALESCE($3::inet, last_ip)
		 WHERE user_id = $1 AND device_id = $2`,
		userID, deviceID, ip)
	if err != nil {
		return fmt.Errorf("touch client: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListAll returns every client row joined to its owning user's username,
// sorted by last_seen DESC (NULLS LAST). Drives the admin overview.
func (r *ClientRepository) ListAll(ctx context.Context) ([]model.ClientWithUser, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+clientColumns+`, u.username
		  FROM clients c
		  JOIN users u ON u.id = c.user_id
		 ORDER BY c.last_seen DESC NULLS LAST`)
	if err != nil {
		return nil, fmt.Errorf("list clients: %w", err)
	}
	defer rows.Close()
	out := []model.ClientWithUser{}
	for rows.Next() {
		var cw model.ClientWithUser
		if err := rows.Scan(
			&cw.ID, &cw.UserID, &cw.DeviceID, &cw.Name, &cw.Platform,
			&cw.OSVersion, &cw.ClientVersion, &cw.LastIP, &cw.LastSeen, &cw.CreatedAt,
			&cw.Username,
		); err != nil {
			return nil, fmt.Errorf("scan client: %w", err)
		}
		out = append(out, cw)
	}
	return out, rows.Err()
}

// Delete removes a single client row by id. The record is a report, not a
// grant: deleting it revokes nothing, and the same installation re-registers
// itself on its next login or token refresh.
func (r *ClientRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM clients WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete client: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
