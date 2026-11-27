package sync_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/quota"
	"privasync/server/internal/repository"
	syncpkg "privasync/server/internal/sync"
)

// seedFile uploads a file and drains the outbox so the bytes live on disk and
// the row is fully settled — a shared setup helper for the copy tests.
func seedFile(t *testing.T, r *rig, userID, rootID uuid.UUID, path string, body []byte) {
	t.Helper()
	staging := path + ".staging-" + uuid.NewString()
	writeStaging(t, r, userID, rootID, staging, body)
	if _, err := r.coord.UploadFile(context.Background(), syncpkg.UploadCmd{
		UserID: userID, SyncRootID: rootID, Path: path,
		StagingPath: staging, Size: int64(len(body)), MTime: time.Now(),
	}); err != nil {
		t.Fatalf("seed upload %s: %v", path, err)
	}
	r.drainOutbox(t)
}

func TestCoordinator_CopyFile_HappyPath(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	body := []byte("copy me")
	seedFile(t, r, u.ID, root.ID, "/c.txt", body)

	res, err := r.coord.CopyFile(context.Background(), syncpkg.CopyCmd{
		UserID: u.ID, SyncRootID: root.ID, From: "/c.txt", To: "/c-Kopie.txt",
	})
	if err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if res.File == nil || res.File.Path != "/c-Kopie.txt" {
		t.Fatalf("unexpected result file: %+v", res.File)
	}
	if res.File.ServerVersion != 0 {
		t.Fatalf("expected fresh server_version 0, got %d", res.File.ServerVersion)
	}

	// Original stays put.
	if _, err := r.files.FindByPath(context.Background(), root.ID, "/c.txt"); err != nil {
		t.Fatalf("original gone: %v", err)
	}
	// Copy carries the same size + content hash.
	cp, err := r.files.FindByPath(context.Background(), root.ID, "/c-Kopie.txt")
	if err != nil {
		t.Fatalf("copy row missing: %v", err)
	}
	if cp.Size != int64(len(body)) {
		t.Fatalf("copy size %d != %d", cp.Size, len(body))
	}
	if cp.ID == uuid.Nil {
		t.Fatalf("copy must have its own id")
	}

	// Quota booked the duplicate bytes (original + copy).
	after, err := r.users.FindByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if after.UsedBytes != int64(2*len(body)) {
		t.Fatalf("expected used_bytes=%d, got %d", 2*len(body), after.UsedBytes)
	}

	r.drainOutbox(t)
	// Bytes landed at the copy path.
	dst := filepath.Join(r.dir, u.ID.String(), root.ID.String(), "c-Kopie.txt")
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read copy: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("copy bytes mismatch: %q", got)
	}
}

func TestCoordinator_CopyFile_DestinationOccupied(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	seedFile(t, r, u.ID, root.ID, "/a.txt", []byte("A"))
	seedFile(t, r, u.ID, root.ID, "/b.txt", []byte("BB"))

	_, err := r.coord.CopyFile(context.Background(), syncpkg.CopyCmd{
		UserID: u.ID, SyncRootID: root.ID, From: "/a.txt", To: "/b.txt",
	})
	if !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestCoordinator_CopyFile_QuotaExceeded(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	body := []byte("0123456789") // 10 bytes
	seedFile(t, r, u.ID, root.ID, "/q.txt", body)

	// Quota that fits the original but not a second copy.
	if _, err := r.pool.Exec(context.Background(),
		`UPDATE users SET quota_bytes = $1 WHERE id = $2`, len(body)+5, u.ID); err != nil {
		t.Fatalf("set quota: %v", err)
	}

	_, err := r.coord.CopyFile(context.Background(), syncpkg.CopyCmd{
		UserID: u.ID, SyncRootID: root.ID, From: "/q.txt", To: "/q2.txt",
	})
	if !errors.Is(err, quota.ErrExceeded) {
		t.Fatalf("expected quota.ErrExceeded, got %v", err)
	}
	// No partial copy row.
	if _, err := r.files.FindByPath(context.Background(), root.ID, "/q2.txt"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected no copy row, got %v", err)
	}
}

func TestCoordinator_CopyDirectory_DuplicatesSubtree(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	seedFile(t, r, u.ID, root.ID, "/dir/a.txt", []byte("aaa"))
	seedFile(t, r, u.ID, root.ID, "/dir/sub/b.txt", []byte("bbbb"))

	res, err := r.coord.CopyFile(context.Background(), syncpkg.CopyCmd{
		UserID: u.ID, SyncRootID: root.ID, From: "/dir", To: "/dir-Kopie",
	})
	if err != nil {
		t.Fatalf("CopyFile (dir): %v", err)
	}
	if res.File == nil || res.File.Path != "/dir-Kopie" {
		t.Fatalf("unexpected dir result: %+v", res.File)
	}

	// Both descendants duplicated under the new prefix.
	for _, p := range []string{"/dir-Kopie/a.txt", "/dir-Kopie/sub/b.txt"} {
		if _, err := r.files.FindByPath(context.Background(), root.ID, p); err != nil {
			t.Fatalf("descendant %s missing: %v", p, err)
		}
	}
	// Originals untouched.
	if _, err := r.files.FindByPath(context.Background(), root.ID, "/dir/a.txt"); err != nil {
		t.Fatalf("original descendant gone: %v", err)
	}

	r.drainOutbox(t)
	// Storage copied the whole tree.
	dst := filepath.Join(r.dir, u.ID.String(), root.ID.String(), "dir-Kopie", "sub", "b.txt")
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read nested copy: %v", err)
	}
	if !bytes.Equal(got, []byte("bbbb")) {
		t.Fatalf("nested copy bytes mismatch: %q", got)
	}
}

// Copied empty directories must surface in the desktop changes feed as is_dir
// rows — non-empty dirs are reconstructed from child file paths, but an empty
// dir only syncs if its explicit is_dir row reaches GetChangedSince. Guards
// against a copy that drops or mistypes the directory rows.
func TestCoordinator_CopyDirectory_EmptyDirsReachChangesFeed(t *testing.T) {
	r := newRig(t)
	u := r.makeUser(t)
	root := r.makeRoot(t, u.ID)

	// /box (explicit) with only an explicit empty subdir /box/sub — no files.
	for _, p := range []string{"/box", "/box/sub"} {
		if _, err := r.files.CreateDirectory(context.Background(), u.ID, root.ID, p, time.Now()); err != nil {
			t.Fatalf("create %s: %v", p, err)
		}
	}

	// Cursor a pull client held before the copy -- 0 covers the whole root,
	// which is fine here since root is fresh per test.
	const since = 0

	if _, err := r.coord.CopyFile(context.Background(), syncpkg.CopyCmd{
		UserID: u.ID, SyncRootID: root.ID, From: "/box", To: "/box-Kopie",
	}); err != nil {
		t.Fatalf("CopyFile (dir): %v", err)
	}

	changes, err := r.files.GetChangedSince(context.Background(), root.ID, since, 1000)
	if err != nil {
		t.Fatalf("GetChangedSince: %v", err)
	}
	got := map[string]bool{}
	for _, f := range changes {
		got[f.Path] = f.IsDir
	}
	for _, want := range []string{"/box-Kopie", "/box-Kopie/sub"} {
		if isDir, ok := got[want]; !ok {
			t.Errorf("%s missing from changes feed", want)
		} else if !isDir {
			t.Errorf("%s present but is_dir=false (desktop would not mkdir it)", want)
		}
	}
}
