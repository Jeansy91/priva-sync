// cmd/purge-testusers deletes every non-admin user in the database.
//
// FK CASCADE removes the user's sync_roots, files, shares, sessions, etc.
// Storage directories under <data>/<userid> and <data>/users/<userid>.json
// are removed afterwards.
//
// Usage:
//
//	PRIVASYNC_DATABASE__URL=postgres://… go run ./cmd/purge-testusers \
//	    --data-dir apps/server/data [--dry-run]
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dataDir := flag.String("data-dir", "apps/server/data", "Storage root")
	dryRun := flag.Bool("dry-run", false, "List candidates only, do not delete")
	flag.Parse()

	dsn := os.Getenv("PRIVASYNC_DATABASE__URL")
	if dsn == "" {
		slog.Error("PRIVASYNC_DATABASE__URL not set")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		slog.Error("pgxpool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `SELECT id::text, username FROM users WHERE role != 'admin'`)
	if err != nil {
		slog.Error("select", "err", err)
		os.Exit(1)
	}
	type user struct{ id, name string }
	var victims []user
	for rows.Next() {
		var x user
		if err := rows.Scan(&x.id, &x.name); err != nil {
			slog.Error("scan", "err", err)
			os.Exit(1)
		}
		victims = append(victims, x)
	}
	rows.Close()

	fmt.Printf("Found: %d non-admin users\n", len(victims))
	for _, v := range victims {
		fmt.Printf("  %-25s %s\n", v.name, v.id)
	}
	if *dryRun {
		fmt.Println("Dry-run, no changes.")
		return
	}
	if len(victims) == 0 {
		return
	}

	tag, err := pool.Exec(ctx, `DELETE FROM users WHERE role != 'admin'`)
	if err != nil {
		slog.Error("delete", "err", err)
		os.Exit(1)
	}
	fmt.Printf("DB: %d rows deleted (CASCADE)\n", tag.RowsAffected())

	for _, v := range victims {
		dir := filepath.Join(*dataDir, v.id)
		if err := os.RemoveAll(dir); err != nil {
			slog.Warn("rm dir", "id", v.id, "err", err)
		}
		meta := filepath.Join(*dataDir, "users", v.id+".json")
		if err := os.Remove(meta); err != nil && !os.IsNotExist(err) {
			slog.Warn("rm meta", "id", v.id, "err", err)
		}
	}
	fmt.Printf("Storage: %d user directories removed\n", len(victims))
}
