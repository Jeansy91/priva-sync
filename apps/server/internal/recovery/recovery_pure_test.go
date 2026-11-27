package recovery

import (
	"os"
	"path/filepath"
	"testing"

	"privasync/server/internal/storage"
)

func TestReadJSONDir_MissingDirIsEmpty(t *testing.T) {
	got, err := readJSONDir[rootMeta](filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("missing dir must not error, got %v", err)
	}
	if got != nil {
		t.Fatalf("missing dir must yield nil, got %v", got)
	}
}

func TestReadJSONDir_SkipsNonJSONInvalidAndSubdirs(t *testing.T) {
	dir := t.TempDir()
	// A subdirectory and a non-JSON file are ignored.
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0644); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	// A malformed JSON file is skipped without failing the whole read.
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not valid"), 0644); err != nil {
		t.Fatalf("write broken: %v", err)
	}
	// A valid JSON file is decoded.
	if err := os.WriteFile(filepath.Join(dir, "ok.json"), []byte(`{"id":"r1","name":"root"}`), 0644); err != nil {
		t.Fatalf("write ok: %v", err)
	}

	got, err := readJSONDir[rootMeta](dir)
	if err != nil {
		t.Fatalf("readJSONDir: %v", err)
	}
	if len(got) != 1 || got[0].ID != "r1" || got[0].Name != "root" {
		t.Fatalf("expected exactly the valid entry, got %+v", got)
	}
}

func TestIsEmptyDir(t *testing.T) {
	base := t.TempDir()
	trashDir := filepath.Join(base, storage.TrashDirName)

	// Truly empty directory.
	empty := filepath.Join(base, "empty")
	if err := os.Mkdir(empty, 0755); err != nil {
		t.Fatalf("mkdir empty: %v", err)
	}
	if !isEmptyDir(empty, trashDir) {
		t.Fatal("an empty directory must be reported as empty")
	}

	// A directory whose only child is the reserved .trash counts as empty.
	rootOnlyTrash := t.TempDir()
	otRoot := filepath.Join(rootOnlyTrash, storage.TrashDirName)
	if err := os.Mkdir(otRoot, 0755); err != nil {
		t.Fatalf("mkdir trash: %v", err)
	}
	if !isEmptyDir(rootOnlyTrash, otRoot) {
		t.Fatal("a directory containing only .trash must be reported as empty")
	}

	// A directory with a real file is not empty.
	if err := os.WriteFile(filepath.Join(empty, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if isEmptyDir(empty, trashDir) {
		t.Fatal("a directory with a file must not be reported as empty")
	}

	// An unreadable/nonexistent directory reports not-empty (ReadDir error).
	if isEmptyDir(filepath.Join(base, "ghost"), trashDir) {
		t.Fatal("a nonexistent directory must not be reported as empty")
	}
}
