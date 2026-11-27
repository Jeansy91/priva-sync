package repository_test

import (
	"context"
	"testing"

	"privasync/server/internal/repository"
)

// The mount delta used a wall-clock cursor while the own-root feed moved to a
// monotonic seq (ADR-0036, migration 032). A backwards clock correction gives
// a *new* change a smaller updated_at than one already delivered, and a client
// whose cursor is past that point never sees it again.
func TestListChangedFilesSinceUnderPrefix_SurvivesBackwardsClock(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	first, err := files.Upsert(ctx, fileAt(u.ID, root.ID, "/mount/first.txt"))
	if err != nil {
		t.Fatalf("upsert first: %v", err)
	}
	second, err := files.Upsert(ctx, fileAt(u.ID, root.ID, "/mount/second.txt"))
	if err != nil {
		t.Fatalf("upsert second: %v", err)
	}

	// The clock jumps back: the newer row now carries the older timestamp.
	if _, err := pool.Exec(ctx,
		`UPDATE files SET updated_at = (SELECT updated_at FROM files WHERE id = $1) - interval '1 hour'
		 WHERE id = $2`, first.ID, second.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	// A client that already has the first row asks for what came after it.
	got, err := files.ListChangedFilesSinceUnderPrefix(ctx, root.ID, "/mount", first.Seq, 100)
	if err != nil {
		t.Fatalf("ListChangedFilesSinceUnderPrefix: %v", err)
	}
	if len(got) != 1 || got[0].ID != second.ID {
		t.Fatalf("got %d rows, want exactly the second one — a backdated change was hidden", len(got))
	}
}

func TestListChangedFilesSinceUnderPrefix_ScopesPagesAndKeepsTombstones(t *testing.T) {
	pool := getPool(t)
	ctx := context.Background()
	u := createTestUser(t)
	root := createTestSyncRoot(t, u.ID)
	files := repository.NewFileRepository(pool)

	for _, p := range []string{"/mount/a.txt", "/mount/b.txt", "/mount/c.txt", "/elsewhere/x.txt"} {
		if _, err := files.Upsert(ctx, fileAt(u.ID, root.ID, p)); err != nil {
			t.Fatalf("upsert %s: %v", p, err)
		}
	}
	// A deletion inside the mount must reach the client so it mirrors locally.
	if _, err := pool.Exec(ctx,
		`UPDATE files SET is_deleted = TRUE WHERE sync_root_id = $1 AND path = '/mount/a.txt'`,
		root.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	t.Run("stays inside the mount", func(t *testing.T) {
		got, err := files.ListChangedFilesSinceUnderPrefix(ctx, root.ID, "/mount", 0, 100)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != 4 {
			t.Fatalf("got %d rows, want 4 — the feed leaked outside the mount", len(got))
		}
		var tombstones int
		for _, f := range got {
			if f.IsDeleted {
				tombstones++
			}
		}
		if tombstones != 1 {
			t.Errorf("got %d tombstones, want 1", tombstones)
		}
	})

	t.Run("honours the limit and orders by seq", func(t *testing.T) {
		page, err := files.ListChangedFilesSinceUnderPrefix(ctx, root.ID, "/mount", 0, 2)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(page) != 2 {
			t.Fatalf("got %d rows, want 2 — the limit was ignored", len(page))
		}
		if page[0].Seq >= page[1].Seq {
			t.Errorf("rows not ordered by seq: %d then %d", page[0].Seq, page[1].Seq)
		}

		next, err := files.ListChangedFilesSinceUnderPrefix(ctx, root.ID, "/mount", page[1].Seq, 2)
		if err != nil {
			t.Fatalf("next page: %v", err)
		}
		if len(next) != 2 {
			t.Fatalf("second page has %d rows, want 2", len(next))
		}
	})
}
