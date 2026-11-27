package db_test

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"privasync/server/internal/config"
	"privasync/server/internal/db"
)

func loadDotenv() {
	dir, _ := os.Getwd()
	for i := 0; i < 6; i++ {
		path := filepath.Join(dir, ".env")
		if f, err := os.Open(path); err == nil {
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				eq := strings.IndexByte(line, '=')
				if eq < 1 {
					continue
				}
				k := strings.TrimSpace(line[:eq])
				v := strings.Trim(strings.TrimSpace(line[eq+1:]), `"'`)
				if os.Getenv(k) == "" {
					os.Setenv(k, os.ExpandEnv(v))
				}
			}
			f.Close()
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	loadDotenv()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	return url
}

func TestNewPool_ConnectsAndPings(t *testing.T) {
	url := testDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := db.NewPool(ctx, config.DatabaseConfig{URL: url, MaxConns: 4, MinConns: 1})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestNewPool_InvalidURLRejected(t *testing.T) {
	ctx := context.Background()
	if _, err := db.NewPool(ctx, config.DatabaseConfig{URL: "not-a-valid-url"}); err == nil {
		t.Fatal("NewPool: want error for invalid URL")
	}
}

func TestNewPool_UnreachableHostFailsOnPing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := db.NewPool(ctx, config.DatabaseConfig{
		URL:      "postgres://user:pass@127.0.0.1:1/nonexistent",
		MaxConns: 1,
		MinConns: 1,
	})
	if err == nil {
		t.Fatal("NewPool: want error for unreachable host")
	}
}

func TestMigrate_IsIdempotent(t *testing.T) {
	url := testDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := db.NewPool(ctx, config.DatabaseConfig{URL: url, MaxConns: 4, MinConns: 1})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(pool); err != nil {
		t.Fatalf("Migrate (first run): %v", err)
	}
	// Running again against an already-migrated DB must be a no-op, not an error.
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("Migrate (second run): %v", err)
	}
}
