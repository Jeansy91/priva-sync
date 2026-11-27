package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// traversal is a slash-relative path that escapes the sync root; every
// path-taking Service method must reject it via safePath.
const traversal = "../../../etc/passwd"

func TestWriteFile_RejectsTraversal(t *testing.T) {
	s := NewService(t.TempDir())
	if _, _, err := s.WriteFile(uuid.New(), "root1", traversal, strings.NewReader("x")); err == nil {
		t.Fatal("WriteFile must reject a path escaping the sync root")
	}
}

func TestDeleteFile_RejectsTraversal(t *testing.T) {
	s := NewService(t.TempDir())
	if err := s.DeleteFile(uuid.New(), "root1", traversal); err == nil {
		t.Fatal("DeleteFile must reject a traversal path")
	}
}

func TestDeleteFile_MissingIsNoop(t *testing.T) {
	s := NewService(t.TempDir())
	uid := uuid.New()
	if err := s.CreateRootDir(uid, "root1"); err != nil {
		t.Fatalf("CreateRootDir: %v", err)
	}
	if err := s.DeleteFile(uid, "root1", "gone.txt"); err != nil {
		t.Fatalf("DeleteFile on missing file must be a no-op, got %v", err)
	}
}

func TestMoveFile_RoundTripAndTraversal(t *testing.T) {
	s := NewService(t.TempDir())
	uid := uuid.New()
	writeTestFile(t, s, uid, "root1", "a/from.txt", "payload")

	if err := s.MoveFile(uid, "root1", "a/from.txt", "b/to.txt"); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.RootDir(uid, "root1"), "b", "to.txt")); err != nil {
		t.Fatalf("moved file must exist at destination: %v", err)
	}

	if err := s.MoveFile(uid, "root1", traversal, "b/to.txt"); err == nil {
		t.Fatal("MoveFile must reject a traversal source")
	}
	if err := s.MoveFile(uid, "root1", "b/to.txt", traversal); err == nil {
		t.Fatal("MoveFile must reject a traversal destination")
	}
}

func TestCopyFile_RoundTripAndTraversal(t *testing.T) {
	s := NewService(t.TempDir())
	uid := uuid.New()
	writeTestFile(t, s, uid, "root1", "src.txt", "copy me")

	if err := s.CopyFile(uid, "root1", "src.txt", "dir/dst.txt"); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(s.RootDir(uid, "root1"), "dir", "dst.txt"))
	if err != nil || string(got) != "copy me" {
		t.Fatalf("copy content = %q, err = %v", got, err)
	}
	// Original must still be present (copy, not move).
	if _, err := os.Stat(filepath.Join(s.RootDir(uid, "root1"), "src.txt")); err != nil {
		t.Fatalf("source must remain after copy: %v", err)
	}

	if err := s.CopyFile(uid, "root1", traversal, "dst.txt"); err == nil {
		t.Fatal("CopyFile must reject a traversal source")
	}
	if err := s.CopyFile(uid, "root1", "src.txt", traversal); err == nil {
		t.Fatal("CopyFile must reject a traversal destination")
	}
}

func TestCopyFile_MissingSourceFails(t *testing.T) {
	s := NewService(t.TempDir())
	uid := uuid.New()
	if err := s.CreateRootDir(uid, "root1"); err != nil {
		t.Fatalf("CreateRootDir: %v", err)
	}
	if err := s.CopyFile(uid, "root1", "nope.txt", "dst.txt"); err == nil {
		t.Fatal("CopyFile must fail when the source does not exist")
	}
}

func TestCopyFile_DestParentIsFileFails(t *testing.T) {
	s := NewService(t.TempDir())
	uid := uuid.New()
	writeTestFile(t, s, uid, "root1", "src.txt", "x")
	writeTestFile(t, s, uid, "root1", "blocker", "not a dir")
	// "blocker" is a regular file, so MkdirAll of "blocker/child" must fail.
	if err := s.CopyFile(uid, "root1", "src.txt", "blocker/child.txt"); err == nil {
		t.Fatal("CopyFile must fail when a destination parent is a regular file")
	}
}

func TestCopyPath_FileDirAndMissing(t *testing.T) {
	s := NewService(t.TempDir())
	uid := uuid.New()

	// Missing source is a tolerated no-op.
	if err := s.CreateRootDir(uid, "root1"); err != nil {
		t.Fatalf("CreateRootDir: %v", err)
	}
	if err := s.CopyPath(uid, "root1", "ghost", "out"); err != nil {
		t.Fatalf("CopyPath on missing source must be a no-op, got %v", err)
	}

	// Single-file source delegates to CopyFile.
	writeTestFile(t, s, uid, "root1", "one.txt", "single")
	if err := s.CopyPath(uid, "root1", "one.txt", "copy/one.txt"); err != nil {
		t.Fatalf("CopyPath (file): %v", err)
	}

	// Directory source is walked recursively, including a nested file.
	writeTestFile(t, s, uid, "root1", "tree/sub/leaf.txt", "leaf")
	if err := s.CopyPath(uid, "root1", "tree", "tree-copy"); err != nil {
		t.Fatalf("CopyPath (dir): %v", err)
	}
	got, err := os.ReadFile(filepath.Join(s.RootDir(uid, "root1"), "tree-copy", "sub", "leaf.txt"))
	if err != nil || string(got) != "leaf" {
		t.Fatalf("walked copy = %q, err = %v", got, err)
	}
}

func TestFileSize_ExistingMissingAndTraversal(t *testing.T) {
	s := NewService(t.TempDir())
	uid := uuid.New()
	writeTestFile(t, s, uid, "root1", "sized.txt", "12345")

	n, err := s.FileSize(uid, "root1", "sized.txt")
	if err != nil || n != 5 {
		t.Fatalf("FileSize = %d, err = %v; want 5", n, err)
	}

	n, err = s.FileSize(uid, "root1", "missing.txt")
	if err != nil || n != 0 {
		t.Fatalf("FileSize(missing) = %d, err = %v; want 0, nil", n, err)
	}

	if _, err := s.FileSize(uid, "root1", traversal); err == nil {
		t.Fatal("FileSize must reject a traversal path")
	}
}

func TestHashFile_SuccessAndErrors(t *testing.T) {
	s := NewService(t.TempDir())
	uid := uuid.New()
	_, want, err := s.WriteFile(uid, "root1", "h.txt", strings.NewReader("hash this"))
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := s.HashFile(uid, "root1", "h.txt")
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	if string(got) != string(want) {
		t.Fatal("HashFile hash must match the hash returned by WriteFile")
	}

	if _, err := s.HashFile(uid, "root1", "missing.txt"); err == nil {
		t.Fatal("HashFile must fail on a missing file")
	}
	if _, err := s.HashFile(uid, "root1", traversal); err == nil {
		t.Fatal("HashFile must reject a traversal path")
	}
}

func TestOpenAndServeFile(t *testing.T) {
	s := NewService(t.TempDir())
	uid := uuid.New()
	writeTestFile(t, s, uid, "root1", "read.txt", "readable")

	f, err := s.Open(uid, "root1", "read.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	f.Close()
	if _, err := s.Open(uid, "root1", traversal); err == nil {
		t.Fatal("Open must reject a traversal path")
	}

	var sb strings.Builder
	if err := s.ServeFile(uid, "root1", "read.txt", &sb); err != nil {
		t.Fatalf("ServeFile: %v", err)
	}
	if sb.String() != "readable" {
		t.Fatalf("ServeFile wrote %q, want %q", sb.String(), "readable")
	}
	if err := s.ServeFile(uid, "root1", "missing.txt", &sb); err == nil {
		t.Fatal("ServeFile must report an error for a missing file")
	}
	if err := s.ServeFile(uid, "root1", traversal, &sb); err == nil {
		t.Fatal("ServeFile must reject a traversal path")
	}
}

func TestTrashRestoreDeleteTrash_GuardBranches(t *testing.T) {
	s := NewService(t.TempDir())
	uid := uuid.New()
	if err := s.CreateRootDir(uid, "root1"); err != nil {
		t.Fatalf("CreateRootDir: %v", err)
	}

	// TrashFile refuses to trash the root itself.
	if _, err := s.TrashFile(uid, "root1", "/"); err == nil {
		t.Fatal("TrashFile must refuse to trash the root")
	}

	// RestoreFile and DeleteTrash both reject a trash path escaping the root.
	if err := s.RestoreFile(uid, "root1", "../../escape", "x.txt"); err == nil {
		t.Fatal("RestoreFile must reject a trash path outside the root")
	}
	if err := s.DeleteTrash(uid, "root1", "../../escape"); err == nil {
		t.Fatal("DeleteTrash must reject a trash path outside the root")
	}
	// DeleteTrash on a nonexistent-but-valid trash path is a no-op.
	if err := s.DeleteTrash(uid, "root1", ".trash/gone/2026-01-01T00-00-00Z/gone"); err != nil {
		t.Fatalf("DeleteTrash on missing entry must be a no-op, got %v", err)
	}
}

func TestCopyPath_TraversalDestRejected(t *testing.T) {
	s := NewService(t.TempDir())
	uid := uuid.New()
	writeTestFile(t, s, uid, "root1", "dir/leaf.txt", "x")
	if err := s.CopyPath(uid, "root1", "dir", traversal); err == nil {
		t.Fatal("CopyPath must reject a traversal destination for a directory source")
	}
}

func TestFinalizeChunks_InvalidFinalPathRejected(t *testing.T) {
	s := NewService(t.TempDir())
	if _, _, err := s.FinalizeChunks(uuid.New(), uuid.New(), "root1", traversal, nil); err == nil {
		t.Fatal("FinalizeChunks must reject a traversal final path")
	}
}

func TestMakeDirAndCreateStaging(t *testing.T) {
	s := NewService(t.TempDir())
	uid := uuid.New()

	if err := s.MakeDir(uid, "root1", "new/nested"); err != nil {
		t.Fatalf("MakeDir: %v", err)
	}
	info, err := os.Stat(filepath.Join(s.RootDir(uid, "root1"), "new", "nested"))
	if err != nil || !info.IsDir() {
		t.Fatalf("MakeDir must create a directory: err=%v", err)
	}
	if err := s.MakeDir(uid, "root1", traversal); err == nil {
		t.Fatal("MakeDir must reject a traversal path")
	}

	rel, f, err := s.CreateStaging(uid, "root1", "target.bin")
	if err != nil {
		t.Fatalf("CreateStaging: %v", err)
	}
	f.Close()
	if !strings.HasPrefix(rel, "target.bin.staging-") {
		t.Fatalf("staging path = %q, want target.bin.staging- prefix", rel)
	}
	if _, _, err := s.CreateStaging(uid, "root1", traversal); err == nil {
		t.Fatal("CreateStaging must reject a traversal target")
	}
}
