package audit

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBuildListQuery_NoFilters(t *testing.T) {
	sql, args := BuildListQuery(ListFilter{Limit: 50})
	if !strings.Contains(sql, "FROM audit_log") {
		t.Fatalf("missing FROM clause: %s", sql)
	}
	if !strings.Contains(sql, "ORDER BY created_at DESC") {
		t.Errorf("missing ORDER BY: %s", sql)
	}
	if !strings.Contains(sql, "LIMIT $1") {
		t.Errorf("limit not parameterised: %s", sql)
	}
	if len(args) != 1 || args[0] != 50 {
		t.Errorf("args: got %v want [50]", args)
	}
}

func TestBuildListQuery_FiltersUserAndEventAndRange(t *testing.T) {
	uid := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	sql, args := BuildListQuery(ListFilter{
		UserID:    &uid,
		EventType: EventLoginSuccess,
		From:      &from,
		To:        &to,
		Limit:     20,
		Offset:    40,
	})
	for _, frag := range []string{
		"user_id = $",
		"event_type = $",
		"created_at >= $",
		"created_at < $",
		"LIMIT $",
		"OFFSET $",
	} {
		if !strings.Contains(sql, frag) {
			t.Errorf("missing clause %q in %s", frag, sql)
		}
	}
	if len(args) != 6 {
		t.Fatalf("args count: got %d want 6 (%v)", len(args), args)
	}
}

func TestBuildListQuery_DefaultLimit(t *testing.T) {
	sql, args := BuildListQuery(ListFilter{})
	if !strings.Contains(sql, "LIMIT $1") {
		t.Fatalf("missing limit clause: %s", sql)
	}
	if args[0] != 100 {
		t.Errorf("default limit: got %v want 100", args[0])
	}
}
