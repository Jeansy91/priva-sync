package repository_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/repository"
)

// seqOf reads the seq column directly -- the repository layer doesn't expose
// it yet (that lands with the paginated changes-feed endpoint), so this test
// only needs to prove the trigger's guarantee, not a Go-level API for it.
func seqOf(t *testing.T, fileID uuid.UUID) int64 {
	t.Helper()
	pool := getPool(t)
	var seq int64
	if err := pool.QueryRow(context.Background(),
		"SELECT seq FROM files WHERE id = $1", fileID,
	).Scan(&seq); err != nil {
		t.Fatalf("read seq: %v", err)
	}
	return seq
}

func TestFilesSeq_StrictlyIncreasesOnInsertAndUpdate(t *testing.T) {
	files := repository.NewFileRepository(getPool(t))
	u := createTestUser(t)
	sr := createTestSyncRoot(t, u.ID)

	a, err := files.Upsert(context.Background(), fileAt(u.ID, sr.ID, "/a.txt"))
	if err != nil {
		t.Fatalf("insert a.txt: %v", err)
	}
	seqA1 := seqOf(t, a.ID)

	b, err := files.Upsert(context.Background(), fileAt(u.ID, sr.ID, "/b.txt"))
	if err != nil {
		t.Fatalf("insert b.txt: %v", err)
	}
	seqB1 := seqOf(t, b.ID)
	if seqB1 <= seqA1 {
		t.Fatalf("seq did not increase across inserts: a=%d b=%d", seqA1, seqB1)
	}

	// Re-upsert a.txt (hits the ON CONFLICT DO UPDATE branch, not INSERT) --
	// the trigger must fire there too, or an in-place edit would never
	// surface in the changes-feed cursor.
	if _, err := files.Upsert(context.Background(), fileAt(u.ID, sr.ID, "/a.txt")); err != nil {
		t.Fatalf("update a.txt: %v", err)
	}
	seqA2 := seqOf(t, a.ID)
	if seqA2 <= seqB1 {
		t.Fatalf("seq did not increase across an update: b=%d a(updated)=%d", seqB1, seqA2)
	}
}

func TestFilesSeq_UniqueUnderConcurrentWrites(t *testing.T) {
	files := repository.NewFileRepository(getPool(t))
	u := createTestUser(t)
	sr := createTestSyncRoot(t, u.ID)

	const n = 20
	var wg sync.WaitGroup
	ids := make([]uuid.UUID, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f, err := files.Upsert(context.Background(), fileAt(u.ID, sr.ID, "/concurrent/"+uniqueSuffix()+".txt"))
			if err == nil {
				ids[i] = f.ID
			}
			errs[i] = err
		}(i)
	}
	wg.Wait()

	seen := make(map[int64]bool, n)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent upsert %d: %v", i, err)
		}
		seq := seqOf(t, ids[i])
		if seen[seq] {
			t.Fatalf("duplicate seq %d assigned under concurrent writes", seq)
		}
		seen[seq] = true
	}
}
