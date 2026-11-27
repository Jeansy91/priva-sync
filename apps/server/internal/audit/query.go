package audit

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ListFilter describes the optional query parameters for
// `GET /admin/audit-log`. Nil pointers / empty strings are skipped.
type ListFilter struct {
	UserID    *uuid.UUID
	EventType string
	From      *time.Time
	To        *time.Time
	Limit     int
	Offset    int
}

// BuildListQuery returns the SQL + args slice for the given filter. Pulled
// out as a pure function so the filter logic stays unit-testable without a
// live DB. Limit/Offset are appended at the end; a missing limit is
// normalised to 100.
func BuildListQuery(f ListFilter) (string, []any) {
	var b strings.Builder
	args := make([]any, 0, 6)
	b.WriteString(`SELECT id, user_id, event_type, host(ip_address), user_agent, details, created_at
FROM audit_log`)

	conds := []string{}
	add := func(clause string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf(clause, len(args)))
	}
	if f.UserID != nil {
		add("user_id = $%d", *f.UserID)
	}
	if f.EventType != "" {
		add("event_type = $%d", f.EventType)
	}
	if f.From != nil {
		add("created_at >= $%d", *f.From)
	}
	if f.To != nil {
		add("created_at < $%d", *f.To)
	}
	if len(conds) > 0 {
		b.WriteString("\nWHERE ")
		b.WriteString(strings.Join(conds, " AND "))
	}
	b.WriteString("\nORDER BY created_at DESC")

	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)
	fmt.Fprintf(&b, "\nLIMIT $%d", len(args))
	if f.Offset > 0 {
		args = append(args, f.Offset)
		fmt.Fprintf(&b, " OFFSET $%d", len(args))
	}
	return b.String(), args
}
