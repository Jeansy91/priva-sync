package sync_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/repository"
	syncpkg "privasync/server/internal/sync"
)

// A random sync-root id the user does not own must fail authorization in every
// mutating coordinator entry point (GetSyncRoot → ErrNotFound, wrapped).
func TestCoordinator_MutationsRejectUnownedRoot(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	ctx := context.Background()
	ghost := uuid.New()

	if _, err := r.coord.MoveFile(ctx, syncpkg.MoveCmd{
		UserID: u.ID, SyncRootID: ghost, From: "/a", To: "/b",
	}); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("MoveFile authorize: want ErrNotFound, got %v", err)
	}
	if _, err := r.coord.DeleteFile(ctx, syncpkg.DeleteCmd{
		UserID: u.ID, SyncRootID: ghost, Path: "/a",
	}); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("DeleteFile authorize: want ErrNotFound, got %v", err)
	}
	if _, err := r.coord.DeleteSubtree(ctx, syncpkg.DeleteSubtreeCmd{
		UserID: u.ID, SyncRootID: ghost, Path: "/a",
	}); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("DeleteSubtree authorize: want ErrNotFound, got %v", err)
	}
	if _, err := r.coord.DeleteSyncRoot(ctx, syncpkg.DeleteSyncRootCmd{
		UserID: u.ID, SyncRootID: ghost,
	}); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("DeleteSyncRoot authorize: want ErrNotFound, got %v", err)
	}
	if _, err := r.coord.RestoreFromTrash(ctx, syncpkg.RestoreCmd{
		UserID: u.ID, SyncRootID: ghost, FileID: uuid.New(),
	}); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("RestoreFromTrash authorize: want ErrNotFound, got %v", err)
	}
}

// Re-deleting an already-trashed path hits the IsDeleted guard → ErrNotFound.
func TestCoordinator_DeleteFile_AlreadyDeleted(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)
	ctx := context.Background()

	body := []byte("bytes")
	staging := "/dup.txt.staging-" + uuid.NewString()
	writeStaging(t, r, u.ID, root.ID, staging, body)
	if _, err := r.coord.UploadFile(ctx, syncpkg.UploadCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/dup.txt",
		StagingPath: staging, Size: int64(len(body)),
	}); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	r.drainOutbox(t)

	if _, err := r.coord.DeleteFile(ctx, syncpkg.DeleteCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/dup.txt",
	}); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	r.drainOutbox(t)

	if _, err := r.coord.DeleteFile(ctx, syncpkg.DeleteCmd{
		UserID: u.ID, SyncRootID: root.ID, Path: "/dup.txt",
	}); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("second delete: want ErrNotFound, got %v", err)
	}
}

// RestoreFromTrash with an authorized root but an unknown trashed id must
// surface the lookup's ErrNotFound.
func TestCoordinator_RestoreFromTrash_UnknownID(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)
	ctx := context.Background()

	if _, err := r.coord.RestoreFromTrash(ctx, syncpkg.RestoreCmd{
		UserID: u.ID, SyncRootID: root.ID, FileID: uuid.New(),
	}); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("restore unknown id: want ErrNotFound, got %v", err)
	}
}
