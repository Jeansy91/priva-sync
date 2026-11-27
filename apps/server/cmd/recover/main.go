// cmd/recover reconstructs the PostgreSQL database from the on-disk data store.
//
// Run this when the database has been lost but the /data directory is intact.
// It reads:
//   - /data/users/{id}.json            → users table
//   - /data/sync_roots/{id}.json       → sync_roots table
//   - /data/shares/{id}.json           → shares table
//   - /data/{user-id}/{root-id}/…      → files table (hash recomputed via blake3)
//   - /data/{user-id}/{root-id}/.trash → trashed file rows
//
// Existing rows are left untouched (INSERT … ON CONFLICT DO NOTHING).
// Migrations are run automatically before recovery begins.
//
// Usage:
//
//	DATABASE_URL=postgres://… recover -data /data
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"privasync/server/internal/config"
	"privasync/server/internal/db"
	"privasync/server/internal/recovery"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		slog.Error("recovery failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	dataPath := flag.String("data", "/data", "Path to the PrivaSync data directory")
	dbURL := flag.String("db", "", "PostgreSQL connection URL (overrides DATABASE_URL env var)")
	flag.Parse()

	if *dbURL == "" {
		*dbURL = os.Getenv("DATABASE_URL")
	}
	if *dbURL == "" {
		return fmt.Errorf("database URL required: set DATABASE_URL or use -db flag")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	cfg := config.DatabaseConfig{URL: *dbURL, MaxConns: 5, MinConns: 1}
	pool, err := db.NewPool(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	slog.Info("running migrations")
	if err := db.Migrate(pool); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	slog.Info("running recovery", "data", *dataPath)
	c, err := recovery.RunAll(ctx, pool, *dataPath)
	if err != nil {
		return err
	}
	slog.Info("recovery complete",
		"users", c.Users,
		"sync_roots", c.SyncRoots,
		"shares", c.Shares,
		"files", c.Files,
		"trashed", c.Trashed,
	)
	return nil
}
