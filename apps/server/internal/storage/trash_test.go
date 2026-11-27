package storage

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func writeTestFile(t *testing.T, s *Service, userID uuid.UUID, rootID, path, content string) {
	t.Helper()
	if err := s.CreateRootDir(userID, rootID); err != nil {
		t.Fatalf("CreateRootDir: %v", err)
	}
	if _, _, err := s.WriteFile(userID, rootID, path, strings.NewReader(content)); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestTrashFile_MovesFileUnderTrashDir(t *testing.T) {
	s := NewService(t.TempDir())
	userID := uuid.New()
	writeTestFile(t, s, userID, "root1", "docs/report.txt", "hello")

	trashPath, err := s.TrashFile(userID, "root1", "docs/report.txt")
	if err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if !strings.HasPrefix(trashPath, TrashDirName+"/") {
		t.Fatalf("trashPath = %q, want prefix %q", trashPath, TrashDirName+"/")
	}
	if _, err := os.Stat(s.RootDir(userID, "root1") + string(os.PathSeparator) + "docs/report.txt"); !os.IsNotExist(err) {
		t.Fatal("original file must be gone after TrashFile")
	}
}

func TestTrashFile_MissingSourceIsNoop(t *testing.T) {
	s := NewService(t.TempDir())
	userID := uuid.New()
	if err := s.CreateRootDir(userID, "root1"); err != nil {
		t.Fatalf("CreateRootDir: %v", err)
	}

	trashPath, err := s.TrashFile(userID, "root1", "does/not/exist.txt")
	if err != nil {
		t.Fatalf("TrashFile (missing source): %v", err)
	}
	if trashPath != "" {
		t.Fatalf("trashPath = %q, want empty for missing source", trashPath)
	}
}

func TestTrashFile_RestoreFile_RoundTrip(t *testing.T) {
	s := NewService(t.TempDir())
	userID := uuid.New()
	writeTestFile(t, s, userID, "root1", "notes.txt", "content")

	trashPath, err := s.TrashFile(userID, "root1", "notes.txt")
	if err != nil {
		t.Fatalf("TrashFile: %v", err)
	}

	if err := s.RestoreFile(userID, "root1", trashPath, "notes-restored.txt"); err != nil {
		t.Fatalf("RestoreFile: %v", err)
	}

	restored, err := os.ReadFile(s.RootDir(userID, "root1") + string(os.PathSeparator) + "notes-restored.txt")
	if err != nil {
		t.Fatalf("reading restored file: %v", err)
	}
	if string(restored) != "content" {
		t.Fatalf("restored content = %q, want %q", restored, "content")
	}
}

func TestRestoreFile_RejectsPathEscapingTrashRoot(t *testing.T) {
	s := NewService(t.TempDir())
	userID := uuid.New()
	if err := s.CreateRootDir(userID, "root1"); err != nil {
		t.Fatalf("CreateRootDir: %v", err)
	}

	err := s.RestoreFile(userID, "root1", "../../etc/passwd", "target.txt")
	if err == nil {
		t.Fatal("RestoreFile: want error for a trash path escaping the root")
	}
}

func TestDeleteTrash_RemovesFileAndPrunesEmptyDirs(t *testing.T) {
	s := NewService(t.TempDir())
	userID := uuid.New()
	writeTestFile(t, s, userID, "root1", "doc.txt", "x")

	trashPath, err := s.TrashFile(userID, "root1", "doc.txt")
	if err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if err := s.DeleteTrash(userID, "root1", trashPath); err != nil {
		t.Fatalf("DeleteTrash: %v", err)
	}

	abs := s.RootDir(userID, "root1") + string(os.PathSeparator) + TrashDirName
	entries, err := os.ReadDir(abs)
	if err != nil {
		t.Fatalf("ReadDir(.trash): %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf(".trash still has %d entries after DeleteTrash+prune, want 0", len(entries))
	}
}

func TestDeleteTrash_NonexistentIsNotAnError(t *testing.T) {
	s := NewService(t.TempDir())
	userID := uuid.New()
	if err := s.CreateRootDir(userID, "root1"); err != nil {
		t.Fatalf("CreateRootDir: %v", err)
	}
	if err := s.DeleteTrash(userID, "root1", TrashDirName+"/doc.txt/2026-01-01T00-00-00Z/doc.txt"); err != nil {
		t.Fatalf("DeleteTrash (nonexistent) = %v, want nil", err)
	}
}

func TestParseTrashTimestamp_InvertsTrashTimestamp(t *testing.T) {
	now := time.Date(2026, 4, 17, 18, 22, 3, 0, time.UTC)
	encoded := trashTimestamp(now)

	got, err := parseTrashTimestamp(encoded)
	if err != nil {
		t.Fatalf("parseTrashTimestamp(%q): %v", encoded, err)
	}
	if !got.Equal(now) {
		t.Fatalf("parseTrashTimestamp round-trip = %v, want %v", got, now)
	}
}

func TestParseTrashTimestamp_InvalidInputRejected(t *testing.T) {
	if _, err := parseTrashTimestamp("not-a-timestamp"); err == nil {
		t.Fatal("parseTrashTimestamp: want error for malformed input")
	}
}
