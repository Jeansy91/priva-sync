package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository persists + reads audit events. Write errors are ignored by the
// caller (audit must never fail the request); read errors propagate via the
// handler to the admin.
type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Log writes an event. `details` is serialised to JSONB; nil → NULL.
func (r *Repository) Log(ctx context.Context, e Event) error {
	var detailsJSON []byte
	if e.Details != nil {
		var err error
		detailsJSON, err = json.Marshal(e.Details)
		if err != nil {
			return fmt.Errorf("marshal audit details: %w", err)
		}
	}
	var ip any
	if e.IPAddress != "" {
		ip = e.IPAddress
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO audit_log (user_id, event_type, ip_address, user_agent, details)
		VALUES ($1, $2, $3, $4, $5)
	`, e.UserID, e.EventType, ip, e.UserAgent, detailsJSON)
	if err != nil {
		// Callers discard this error on purpose (audit must never fail a
		// request), so this is the last place a lost entry can be noticed.
		slog.Warn("audit insert failed",
			"event_type", e.EventType, "user_id", e.UserID, "error", err)
	}
	return err
}

// List returns events matching the filter, newest first. See ListFilter for
// limit/offset; default limit is 100.
func (r *Repository) List(ctx context.Context, f ListFilter) ([]Event, error) {
	sql, args := BuildListQuery(f)
	rows, err := r.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query audit_log: %w", err)
	}
	defer rows.Close()

	events := make([]Event, 0, 32)
	for rows.Next() {
		var e Event
		var ip *string
		var ua *string
		var details []byte
		if err := rows.Scan(&e.ID, &e.UserID, &e.EventType, &ip, &ua, &details, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan audit row: %w", err)
		}
		if ip != nil {
			e.IPAddress = *ip
		}
		if ua != nil {
			e.UserAgent = *ua
		}
		if len(details) > 0 {
			if err := json.Unmarshal(details, &e.Details); err != nil {
				return nil, fmt.Errorf("unmarshal audit details: %w", err)
			}
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
