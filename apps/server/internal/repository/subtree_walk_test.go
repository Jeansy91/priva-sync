package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

// The walk pages internally, so the property worth pinning is that the
// page boundary is invisible: every row arrives exactly once, in path order,
// however many pages it takes. 1200 rows crosses the 1000-row page.
const acrossPages = 1200

func seedWide(t *testing.T, n int) (*repository.FileRepository, uuid.UUID) {
	t.Helper()
	pool := getPool(t)
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)

	if _, err := pool.Exec(context.Background(), `
		INSERT INTO files (user_id, sync_root_id, path, size, mtime, is_deleted, is_dir)
		SELECT $1, $2, '/wide/f' || lpad(i::text, 6, '0') || '.txt', 1, NOW(), FALSE, FALSE
		FROM generate_series(1, $3) AS i
	`, u.ID, root.ID, n); err != nil {
		t.Fatalf("seed %d rows: %v", n, err)
	}
	// The bulk insert goes around the repository, so the directory row is
	// added the way ensureAncestors would (ADR-0041).
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO files (user_id, sync_root_id, path, size, mtime, is_dir, is_deleted)
		VALUES ($1, $2, '/wide', 0, NOW(), TRUE, FALSE)
	`, u.ID, root.ID); err != nil {
		t.Fatalf("seed directory row: %v", err)
	}
	return repository.NewFileRepository(pool), root.ID
}

func TestEachInSubtree_WalksEveryRowAcrossPageBoundaries(t *testing.T) {
	files, rootID := seedWide(t, acrossPages)

	seen := make(map[string]int, acrossPages)
	var last string
	err := files.EachInSubtree(context.Background(), rootID, "/wide", func(f model.File) error {
		seen[f.Path]++
		if f.Path <= last {
			t.Fatalf("out of order: %q after %q", f.Path, last)
		}
		last = f.Path
		return nil
	})
	if err != nil {
		t.Fatalf("EachInSubtree: %v", err)
	}

	// The directory's own row plus every file below it.
	if len(seen) != acrossPages+1 {
		t.Fatalf("visited %d distinct rows, want %d", len(seen), acrossPages+1)
	}
	for p, n := range seen {
		if n != 1 {
			t.Fatalf("row %q visited %d times", p, n)
		}
	}
}

// A callback that fails stops the walk and surfaces its error: a ZIP whose
// bytes cannot be served must not keep paging through the rest of the tree.
func TestEachInSubtree_StopsOnCallbackError(t *testing.T) {
	files, rootID := seedWide(t, acrossPages)

	boom := errors.New("boom")
	visited := 0
	err := files.EachInSubtree(context.Background(), rootID, "/wide", func(model.File) error {
		visited++
		if visited == 3 {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("EachInSubtree err = %v, want the callback's error", err)
	}
	if visited != 3 {
		t.Fatalf("walk continued past the failure: %d rows visited", visited)
	}
}

// Cascade-delete soft-deletes as it walks. Rows leaving the result set behind
// the cursor must not shift what is still ahead of it.
func TestEachInSubtree_SurvivesDeletionDuringTheWalk(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	files, rootID := seedWide(t, acrossPages)

	seen := 0
	err := files.EachInSubtree(ctx, rootID, "/wide", func(f model.File) error {
		seen++
		_, err := pool.Exec(ctx,
			`UPDATE files SET is_deleted = TRUE WHERE sync_root_id = $1 AND path = $2`,
			rootID, f.Path)
		return err
	})
	if err != nil {
		t.Fatalf("EachInSubtree: %v", err)
	}
	if seen != acrossPages+1 {
		t.Fatalf("visited %d rows while deleting, want %d -- the cursor skipped rows",
			seen, acrossPages+1)
	}
}

// Same page-boundary property for the trash sweep, plus the reason its keyset
// is the id: a row the callback refuses to purge must not be handed back
// forever.
func TestEachTrashedBefore_PagesAndDoesNotRepeatAFailedRow(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	trash := repository.NewTrashRepository(pool)

	const n = 1200
	if _, err := pool.Exec(ctx, `
		INSERT INTO files (user_id, sync_root_id, path, size, mtime, is_deleted, is_dir,
		                   deleted_at, trash_path)
		SELECT $1, $2, '/t' || lpad(i::text, 6, '0') || '.txt', 1, NOW(), TRUE, FALSE,
		       NOW() - interval '40 days', 'trash/' || i
		FROM generate_series(1, $3) AS i
	`, u.ID, root.ID, n); err != nil {
		t.Fatalf("seed trash: %v", err)
	}

	cutoff := time.Now().Add(-24 * time.Hour)
	seen := make(map[uuid.UUID]int, n)
	err := trash.EachTrashedBefore(ctx, cutoff, func(f model.File) error {
		if f.SyncRootID != root.ID {
			return nil // another test's rows share the table
		}
		seen[f.ID]++
		// Nothing is purged, so every row stays in the result set. Without a
		// forward-only keyset the sweep would loop on the first page.
		return nil
	})
	if err != nil {
		t.Fatalf("EachTrashedBefore: %v", err)
	}
	if len(seen) != n {
		t.Fatalf("visited %d distinct rows, want %d", len(seen), n)
	}
	for id, times := range seen {
		if times != 1 {
			t.Fatalf("row %s visited %d times", id, times)
		}
	}
}
