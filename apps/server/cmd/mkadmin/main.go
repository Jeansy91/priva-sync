// cmd/mkadmin creates (or replaces) an admin user in the database.
//
// Usage:
//
//	DATABASE_URL=postgres://… mkadmin -username admin -email admin@example.com -password 'secret'
//
// Runs migrations first.  If the username already exists, the password hash
// and role are updated (idempotent).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"privasync/server/internal/auth"
	"privasync/server/internal/config"
	"privasync/server/internal/db"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(); err != nil {
		slog.Error("mkadmin failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	username := flag.String("username", "admin", "Admin username")
	email    := flag.String("email", "admin@example.com", "Admin email")
	password := flag.String("password", "", "Admin password (required)")
	dbURL    := flag.String("db", "", "PostgreSQL connection URL (overrides DATABASE_URL env var)")
	flag.Parse()

	if *password == "" {
		return fmt.Errorf("-password is required")
	}
	if *dbURL == "" {
		*dbURL = os.Getenv("DATABASE_URL")
	}
	if *dbURL == "" {
		return fmt.Errorf("database URL required: set DATABASE_URL or use -db flag")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.NewPool(ctx, config.DatabaseConfig{URL: *dbURL, MaxConns: 2, MinConns: 1})
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	if err := db.Migrate(pool); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	hash, err := auth.HashPassword(*password)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}

	tag, err := pool.Exec(ctx, `
		INSERT INTO users (username, email, password_hash, role, is_active)
		VALUES ($1, $2, $3, 'admin', TRUE)
		ON CONFLICT (username) DO UPDATE
		   SET password_hash = EXCLUDED.password_hash,
		       email         = EXCLUDED.email,
		       role          = 'admin',
		       is_active     = TRUE,
		       updated_at    = now()
	`, *username, *email, hash)
	if err != nil {
		return fmt.Errorf("inserting admin: %w", err)
	}

	slog.Info("admin ready", "username", *username, "email", *email, "rows", tag.RowsAffected())
	return nil
}
