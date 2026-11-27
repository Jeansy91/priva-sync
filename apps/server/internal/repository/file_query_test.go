package repository_test

import (
	"context"
	"testing"

	"privasync/server/internal/repository"
)

func TestGetSyncRootByID_FileRepository(t *testing.T) {
	pool := getPool(t)
	files := repository.NewFileRepository(pool)
	u := createTestUser(t)
	sr := createTestSyncRoot(t, u.ID)

	got, err := files.GetSyncRootByID(context.Background(), sr.ID)
	if err != nil {
		t.Fatalf("GetSyncRootByID: %v", err)
	}
	if got.ID != sr.ID {
		t.Fatalf("GetSyncRootByID id = %v, want %v", got.ID, sr.ID)
	}

	if _, err := files.GetSyncRootByID(context.Background(), u.ID); err != repository.ErrNotFound {
		t.Fatalf("GetSyncRootByID unknown id: err = %v, want ErrNotFound", err)
	}
}

func TestListChangedFilesSinceUnderPrefix_ScopesByPrefixAndCursor(t *testing.T) {
	pool := getPool(t)
	files := repository.NewFileRepository(pool)
	u := createTestUser(t)
	sr := createTestSyncRoot(t, u.ID)

	inside, err := files.Upsert(context.Background(), fileAt(u.ID, sr.ID, "/mount/inside.txt"))
	if err != nil {
		t.Fatalf("upsert inside.txt: %v", err)
	}
	if _, err := files.Upsert(context.Background(), fileAt(u.ID, sr.ID, "/outside/other.txt")); err != nil {
		t.Fatalf("upsert other.txt: %v", err)
	}

	got, err := files.ListChangedFilesSinceUnderPrefix(context.Background(), sr.ID, "/mount", 0, 100)
	if err != nil {
		t.Fatalf("ListChangedFilesSinceUnderPrefix: %v", err)
	}
	wantPaths(t, "ListChangedFilesSinceUnderPrefix(/mount)", pathsOf(got),
		[]string{"/mount", "/mount/inside.txt"})

	// A cursor already past the newest row yields nothing.
	got, err = files.ListChangedFilesSinceUnderPrefix(context.Background(), sr.ID, "/mount", inside.Seq, 100)
	if err != nil {
		t.Fatalf("ListChangedFilesSinceUnderPrefix (drained cursor): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListChangedFilesSinceUnderPrefix past the cursor = %+v, want none", got)
	}
}

func TestGetChangedSince_PaginatesWithoutGapsOrDuplicates(t *testing.T) {
	pool := getPool(t)
	files := repository.NewFileRepository(pool)
	u := createTestUser(t)
	sr := createTestSyncRoot(t, u.ID)

	const total = 25
	for i := 0; i < total; i++ {
		if _, err := files.Upsert(context.Background(), fileAt(u.ID, sr.ID, "/p"+uniqueSuffix()+".txt")); err != nil {
			t.Fatalf("seed upsert %d: %v", i, err)
		}
	}

	// Page through with a small page size, as if a fresh sync were catching
	// up. Every page must extend the previous one -- no row skipped, no row
	// repeated -- and the walk must terminate on a short page.
	const pageSize = 7
	seen := make(map[string]bool, total)
	var cursor int64
	for pages := 0; ; pages++ {
		if pages > total { // guard against an infinite loop on a real bug
			t.Fatalf("did not terminate after %d pages", pages)
		}
		page, err := files.GetChangedSince(context.Background(), sr.ID, cursor, pageSize)
		if err != nil {
			t.Fatalf("GetChangedSince page %d: %v", pages, err)
		}
		for _, f := range page {
			if seen[f.Path] {
				t.Fatalf("duplicate path %q across pages", f.Path)
			}
			seen[f.Path] = true
			if f.Seq <= cursor {
				t.Fatalf("page not past cursor: seq=%d cursor=%d", f.Seq, cursor)
			}
			cursor = f.Seq
		}
		if len(page) < pageSize {
			break
		}
	}
	if len(seen) != total {
		t.Fatalf("paginated through %d rows, want %d", len(seen), total)
	}
}

func TestGetChangedSince_EmptyPageWhenNoChanges(t *testing.T) {
	pool := getPool(t)
	files := repository.NewFileRepository(pool)
	u := createTestUser(t)
	sr := createTestSyncRoot(t, u.ID)

	got, err := files.Upsert(context.Background(), fileAt(u.ID, sr.ID, "/only.txt"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	page, err := files.GetChangedSince(context.Background(), sr.ID, got.Seq, 100)
	if err != nil {
		t.Fatalf("GetChangedSince: %v", err)
	}
	if len(page) != 0 {
		t.Fatalf("GetChangedSince past the latest seq = %+v, want empty page", page)
	}
}

func TestMoveFileWithVersionCheck_DetectsConflictAndMoves(t *testing.T) {
	pool := getPool(t)
	files := repository.NewFileRepository(pool)
	u := createTestUser(t)
	sr := createTestSyncRoot(t, u.ID)

	created, err := files.Upsert(context.Background(), fileAt(u.ID, sr.ID, "/from.txt"))
	if err != nil {
		t.Fatalf("upsert from.txt: %v", err)
	}

	if _, conflict, err := files.MoveFileWithVersionCheck(
		context.Background(), sr.ID, "/from.txt", "/to.txt", created.ServerVersion+1, u.ID,
	); err != nil || !conflict {
		t.Fatalf("MoveFileWithVersionCheck with wrong version: conflict=%v err=%v, want conflict=true", conflict, err)
	}

	moved, conflict, err := files.MoveFileWithVersionCheck(
		context.Background(), sr.ID, "/from.txt", "/to.txt", created.ServerVersion, u.ID,
	)
	if err != nil {
		t.Fatalf("MoveFileWithVersionCheck: %v", err)
	}
	if conflict {
		t.Fatal("MoveFileWithVersionCheck with correct version reported a conflict")
	}
	if moved.Path != "/to.txt" {
		t.Fatalf("MoveFileWithVersionCheck moved path = %q, want /to.txt", moved.Path)
	}

	if _, conflict, err := files.MoveFileWithVersionCheck(
		context.Background(), sr.ID, "/gone.txt", "/somewhere.txt", 0, u.ID,
	); err != nil || !conflict {
		t.Fatalf("MoveFileWithVersionCheck on missing source: conflict=%v err=%v, want conflict=true, err=nil", conflict, err)
	}
}
