package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/repository"
)

// mutateEnv bundles a fresh user, sync root and FileRepository for the
// copy/move branch tests.
type mutateEnv struct {
	ctx    context.Context
	repo   *repository.FileRepository
	userID uuid.UUID
	rootID uuid.UUID
	actor  uuid.UUID
}

func newMutateEnv(t *testing.T) mutateEnv {
	t.Helper()
	pool := getPool(t)
	u := createTestUser(t)
	sr := createTestSyncRoot(t, u.ID)
	return mutateEnv{
		ctx:    context.Background(),
		repo:   repository.NewFileRepository(pool),
		userID: u.ID,
		rootID: sr.ID,
		actor:  u.ID,
	}
}

func (e mutateEnv) seed(t *testing.T, path string) {
	t.Helper()
	if _, err := e.repo.Upsert(e.ctx, fileAt(e.userID, e.rootID, path)); err != nil {
		t.Fatalf("seed %q: %v", path, err)
	}
}

func (e mutateEnv) seedDir(t *testing.T, path string) {
	t.Helper()
	if _, err := e.repo.CreateDirectory(e.ctx, e.userID, e.rootID, path, time.Now()); err != nil {
		t.Fatalf("seed dir %q: %v", path, err)
	}
}

func TestCopyFile_SuccessConflictNotFound(t *testing.T) {
	e := newMutateEnv(t)
	e.seed(t, "/src.txt")

	out, err := e.repo.CopyFile(e.ctx, e.rootID, "/src.txt", "/dst.txt", e.actor)
	if err != nil {
		t.Fatalf("CopyFile success: %v", err)
	}
	if out.Path != "/dst.txt" || out.IsDeleted {
		t.Fatalf("copied row unexpected: %+v", out)
	}

	// Destination now occupied -> ErrConflict.
	if _, err := e.repo.CopyFile(e.ctx, e.rootID, "/src.txt", "/dst.txt", e.actor); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}

	// Missing source -> ErrNotFound.
	if _, err := e.repo.CopyFile(e.ctx, e.rootID, "/ghost.txt", "/other.txt", e.actor); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMoveFile_SuccessConflictNotFound(t *testing.T) {
	e := newMutateEnv(t)
	e.seed(t, "/from.txt")

	out, err := e.repo.MoveFile(e.ctx, e.rootID, "/from.txt", "/to.txt", e.actor)
	if err != nil {
		t.Fatalf("MoveFile success: %v", err)
	}
	if out.Path != "/to.txt" {
		t.Fatalf("moved row path = %q, want /to.txt", out.Path)
	}

	// A live row already at the destination blocks the move.
	e.seed(t, "/blocked-src.txt")
	if _, err := e.repo.MoveFile(e.ctx, e.rootID, "/blocked-src.txt", "/to.txt", e.actor); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}

	// Missing source -> ErrNotFound.
	if _, err := e.repo.MoveFile(e.ctx, e.rootID, "/ghost.txt", "/nowhere.txt", e.actor); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMoveDirectory_SuccessConflictNotFound(t *testing.T) {
	e := newMutateEnv(t)
	e.seedDir(t, "/old")
	e.seed(t, "/old/sub/a.txt")

	out, err := e.repo.MoveDirectory(e.ctx, e.rootID, "/old", "/new", e.actor)
	if err != nil {
		t.Fatalf("MoveDirectory success: %v", err)
	}
	if out.Path != "/new" {
		t.Fatalf("moved dir path = %q, want /new", out.Path)
	}
	// The descendant moved with the prefix splice.
	if f, err := e.repo.FindByPath(e.ctx, e.rootID, "/new/sub/a.txt"); err != nil || f.IsDeleted {
		t.Fatalf("descendant must live at new path, err=%v file=%+v", err, f)
	}

	// Destination occupied -> ErrConflict.
	e.seedDir(t, "/src-dir")
	e.seed(t, "/occupied")
	if _, err := e.repo.MoveDirectory(e.ctx, e.rootID, "/src-dir", "/occupied", e.actor); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}

	// Nothing at source -> ErrNotFound.
	if _, err := e.repo.MoveDirectory(e.ctx, e.rootID, "/does-not-exist", "/target", e.actor); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// ADR-0065: a directory move must ride the changes feed as one prefix event,
// not one row per descendant. The descendant's own seq must therefore stay
// untouched by the move (only the move_events row gets a fresh seq), and no
// tombstone is left at the vacated paths -- the move_events row is now the
// only feed signal for the whole subtree.
func TestMoveDirectory_EmitsOnePrefixMoveEventNoTombstonesNoSeqBump(t *testing.T) {
	e := newMutateEnv(t)
	e.seedDir(t, "/old")
	e.seed(t, "/old/sub/a.txt")

	before, err := e.repo.FindByPath(e.ctx, e.rootID, "/old/sub/a.txt")
	if err != nil {
		t.Fatalf("find before move: %v", err)
	}

	if _, err := e.repo.MoveDirectory(e.ctx, e.rootID, "/old", "/new", e.actor); err != nil {
		t.Fatalf("MoveDirectory: %v", err)
	}

	after, err := e.repo.FindByPath(e.ctx, e.rootID, "/new/sub/a.txt")
	if err != nil || after.IsDeleted {
		t.Fatalf("descendant must live at new path, err=%v file=%+v", err, after)
	}
	if after.Seq != before.Seq {
		t.Fatalf("descendant seq = %d, want unchanged %d -- the move must not flood the feed with a row per descendant", after.Seq, before.Seq)
	}

	// FindByPaths sees soft-deleted rows too (unlike FindByPath) -- with
	// tombstones gone, the vacated paths must not exist as rows at all.
	vacated, err := e.repo.FindByPaths(e.ctx, e.rootID, []string{"/old", "/old/sub/a.txt"})
	if err != nil {
		t.Fatalf("FindByPaths vacated: %v", err)
	}
	if len(vacated) != 0 {
		t.Fatalf("rows at vacated paths = %+v, want none -- move_events replaces tombstones", vacated)
	}

	events, err := e.repo.ListMoveEventsSince(e.ctx, e.rootID, 0, 10)
	if err != nil {
		t.Fatalf("ListMoveEventsSince: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("move_events = %d, want exactly 1", len(events))
	}
	if events[0].OldPrefix != "/old" || events[0].NewPrefix != "/new" {
		t.Fatalf("move event = %+v, want old=/old new=/new", events[0])
	}
	if events[0].Seq <= before.Seq {
		t.Fatalf("move event seq = %d, want a fresh seq greater than the pre-move descendant seq %d", events[0].Seq, before.Seq)
	}
}

func TestCopyDirectory_SuccessConflictNotFound(t *testing.T) {
	e := newMutateEnv(t)
	e.seedDir(t, "/tree")
	e.seed(t, "/tree/deep/leaf.txt")

	out, err := e.repo.CopyDirectory(e.ctx, e.rootID, "/tree", "/tree-copy", e.actor)
	if err != nil {
		t.Fatalf("CopyDirectory success: %v", err)
	}
	if out.Path != "/tree-copy" {
		t.Fatalf("copied dir path = %q, want /tree-copy", out.Path)
	}
	// Source subtree still present (copy, not move).
	if f, err := e.repo.FindByPath(e.ctx, e.rootID, "/tree/deep/leaf.txt"); err != nil || f.IsDeleted {
		t.Fatalf("source descendant must remain, err=%v file=%+v", err, f)
	}
	// Copied descendant present at the new prefix.
	if f, err := e.repo.FindByPath(e.ctx, e.rootID, "/tree-copy/deep/leaf.txt"); err != nil || f.IsDeleted {
		t.Fatalf("copied descendant must exist, err=%v file=%+v", err, f)
	}

	// Destination occupied -> ErrConflict.
	e.seed(t, "/busy")
	if _, err := e.repo.CopyDirectory(e.ctx, e.rootID, "/tree", "/busy", e.actor); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}

	// Nothing at source -> ErrNotFound.
	if _, err := e.repo.CopyDirectory(e.ctx, e.rootID, "/nada", "/target", e.actor); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
