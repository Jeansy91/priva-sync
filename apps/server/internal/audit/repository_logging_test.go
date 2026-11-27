package audit

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Callers deliberately discard Log's error -- the audit trail must never fail
// a request. That makes the repository the last place a failed INSERT can
// still be noticed, so it has to say so itself.
func TestRepositoryLog_FailedInsertIsLogged(t *testing.T) {
	// Valid DSN, unroutable host: pgxpool connects lazily, so construction
	// succeeds and the failure surfaces on Exec -- no test DB needed.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, "postgres://u:p@127.0.0.1:1/nonexistent?connect_timeout=1")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	err = NewRepository(pool).Log(ctx, Event{EventType: EventLoginFail, IPAddress: "203.0.113.7"})

	if err == nil {
		t.Fatal("Log should return the insert error so callers may react")
	}
	if got := logs.String(); !strings.Contains(got, EventLoginFail) {
		t.Errorf("failed audit insert was not logged; captured output: %q", got)
	}
}
