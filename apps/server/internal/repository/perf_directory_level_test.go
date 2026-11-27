package repository_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/repository"
)

// Measurement harness for the directory-listing cost on large roots.
// Skipped unless PRIVASYNC_PERF_ROWS is set, because seeding a million
// rows takes minutes and has no place in the normal suite:
//
//	PRIVASYNC_PERF_ROWS=1000000 go test ./internal/repository/ \
//	  -run TestPerf_ListDirectoryLevel -v -timeout 30m
//
// The question is whether the listing cost tracks the page size or the size
// of the subtree underneath it. Reading the query can suggest an answer; the
// plan is what EXPLAIN prints, so the harness prints it.
func TestPerf_ListDirectoryLevel(t *testing.T) {
	rows := os.Getenv("PRIVASYNC_PERF_ROWS")
	if rows == "" {
		t.Skip("set PRIVASYNC_PERF_ROWS to run the directory-listing measurement")
	}
	total, err := strconv.Atoi(rows)
	if err != nil {
		t.Fatalf("PRIVASYNC_PERF_ROWS: %v", err)
	}

	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	// A wide top level over a flat-ish tree: `/dirNNNN/fileNNNNNNNN.txt`.
	// Listing "/" then has to produce a few hundred entries out of a root
	// holding a million rows, which is the first thing the production UI asks
	// for.
	const topLevelDirs = 500
	seedStart := time.Now()
	if _, err := pool.Exec(ctx, `
		INSERT INTO files (user_id, sync_root_id, path, size, mtime, is_deleted, is_dir, updated_at)
		SELECT $1, $2,
		       '/dir' || lpad((i % $4)::text, 4, '0') || '/file' || lpad(i::text, 8, '0') || '.txt',
		       1024, NOW(), FALSE, FALSE, NOW()
		FROM generate_series(1, $3) AS i
	`, u.ID, root.ID, total, topLevelDirs); err != nil {
		t.Fatalf("seed %d rows: %v", total, err)
	}
	// The bulk insert goes around the repository, so the directory rows every
	// path needs above it (ADR-0041) are added here the way the repair sweep
	// would.
	if _, _, err := files.MaterialiseMissingDirectories(ctx); err != nil {
		t.Fatalf("materialise directories: %v", err)
	}
	t.Logf("seeded %d rows in %s", total, time.Since(seedStart).Round(time.Millisecond))

	if _, err := pool.Exec(ctx, `ANALYZE files`); err != nil {
		t.Fatalf("ANALYZE: %v", err)
	}

	// The root level used to be the worst case: it had to reduce every row in
	// the root to its first segment before it could take a page. One
	// directory down ran the same query against a subtree 1/topLevelDirs the
	// size, so the two were orders apart. Since directories are rows both are
	// one index range, and the gap should be gone.
	for _, tc := range []struct{ name, prefix string }{
		{name: "root level", prefix: "/"},
		{name: "one directory down", prefix: "/dir0001"},
	} {
		start := time.Now()
		entries, err := files.ListDirectoryLevel(ctx, repository.ListDirectoryLevelParams{
			SyncRootID: root.ID,
			PathPrefix: tc.prefix,
			Limit:      100,
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(entries) == 0 {
			t.Fatalf("%s returned no entries -- the seed is wrong, not the query", tc.name)
		}
		t.Logf("%-20s %8d rows in root -> %3d entries in %s",
			tc.name, total, len(entries), time.Since(start).Round(time.Millisecond))
	}

	t.Logf("PLAN for the root level:\n%s", explainLevel(t, pool, root.ID.String(), "/"))
}

// explainLevel runs EXPLAIN (ANALYZE, BUFFERS) over the statement
// ListDirectoryLevel issues, so the measurement can name the real cost driver
// instead of inferring one. The SQL is a copy and will drift from the
// repository if that query changes -- it is a diagnostic, not an assertion.
func explainLevel(t *testing.T, pool *pgxpool.Pool, rootID, prefix string) string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		EXPLAIN (ANALYZE, BUFFERS)
		SELECT id, path, is_dir
		FROM files
		WHERE sync_root_id = $1
		  AND parent_path  = $2
		  AND is_deleted   = FALSE
		  AND path > $2
		ORDER BY path ASC
		LIMIT 100`, rootID, prefix)
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	defer rows.Close()

	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan.WriteString("  " + line + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	return plan.String()
}
