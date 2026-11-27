package repository_test

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

// Measurement harness for the unbounded result sets. Same gate as the
// directory-listing harness:
//
//	PRIVASYNC_PERF_ROWS=1000000 go test ./internal/repository/ \
//	  -run TestPerf_UnboundedResultSets -v -timeout 30m
//
// These methods have no LIMIT and materialise every row into a slice before
// anything leaves the server. The question is what that costs in heap on a
// root the size the project is aiming at (ADR-0036/0037).
func TestPerf_UnboundedResultSets(t *testing.T) {
	rows := os.Getenv("PRIVASYNC_PERF_ROWS")
	if rows == "" {
		t.Skip("set PRIVASYNC_PERF_ROWS to run the unbounded-result-set measurement")
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

	if _, err := pool.Exec(ctx, `
		INSERT INTO files (user_id, sync_root_id, path, size, mtime, is_deleted, is_dir, updated_at)
		SELECT $1, $2,
		       '/dir' || lpad((i % 500)::text, 4, '0') || '/file' || lpad(i::text, 8, '0') || '.txt',
		       1024, NOW(), FALSE, FALSE, NOW()
		FROM generate_series(1, $3) AS i
	`, u.ID, root.ID, total); err != nil {
		t.Fatalf("seed %d rows: %v", total, err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE files`); err != nil {
		t.Fatalf("ANALYZE: %v", err)
	}

	measure := func(name string, fn func() (int, error)) {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)

		start := time.Now()
		n, fnErr := fn()
		elapsed := time.Since(start)
		if fnErr != nil {
			t.Fatalf("%s: %v", name, fnErr)
		}
		runtime.ReadMemStats(&after)

		t.Logf("%-32s %8d rows -> %8d returned in %-10s heap +%d MiB (peak alloc %d MiB)",
			name, total, n, elapsed.Round(time.Millisecond),
			(after.HeapAlloc-min(after.HeapAlloc, before.HeapAlloc))/(1<<20),
			(after.TotalAlloc-before.TotalAlloc)/(1<<20))
	}

	// The mount delta, now paginated on seq -- kept here as the
	// contrast: same table, same rows, bounded response.
	measure("MountDelta since=0 limit=500", func() (int, error) {
		out, err := files.ListChangedFilesSinceUnderPrefix(ctx, root.ID, "/", 0, 500)
		return len(out), err
	})

	// The subtree a ZIP download walks, and what cascade-delete iterates.
	// Streaming it means the rows in flight are one page, not the subtree
	//. The two sizes below are the claim: a walk over the whole root
	// visits 500x the rows of one directory and must not cost 500x the heap.
	measure("EachInSubtree /dir0001", func() (int, error) {
		n := 0
		err := files.EachInSubtree(ctx, root.ID, "/dir0001", func(model.File) error {
			n++
			return nil
		})
		return n, err
	})

	measure("EachInSubtree / (whole root)", func() (int, error) {
		n := 0
		err := files.EachInSubtree(ctx, root.ID, "/", func(model.File) error {
			n++
			return nil
		})
		return n, err
	})

	// For contrast: the one feed that is already paginated (ADR-0036).
	measure("GetChangedSince limit=500", func() (int, error) {
		out, err := files.GetChangedSince(ctx, root.ID, 0, 500)
		return len(out), err
	})
}
