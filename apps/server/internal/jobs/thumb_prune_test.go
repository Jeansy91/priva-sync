package jobs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseThumbHash(t *testing.T) {
	cases := map[string]string{
		"deadbeef_256_cover.jpg": "deadbeef",
		"abc123_64_square.png":   "abc123",
		"no-underscore.jpg":      "",
		"_leadingunderscore.jpg": "",
		"nothex_256_cover.jpg":   "",
	}
	for name, want := range cases {
		if got := parseThumbHash(name); got != want {
			t.Errorf("parseThumbHash(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestRemoveIfEmpty(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := removeIfEmpty(empty); err != nil {
		t.Fatalf("removeIfEmpty(empty dir): %v", err)
	}
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Error("empty directory was not removed")
	}

	nonEmpty := filepath.Join(dir, "nonempty")
	if err := os.Mkdir(nonEmpty, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := removeIfEmpty(nonEmpty); err != nil {
		t.Fatalf("removeIfEmpty(non-empty dir): %v", err)
	}
	if _, err := os.Stat(nonEmpty); err != nil {
		t.Error("non-empty directory was removed")
	}
}

func TestRemoveAll_CountsRegularFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "root")
	if err := os.MkdirAll(filepath.Join(target, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"a.jpg", "sub/b.jpg"} {
		if err := os.WriteFile(filepath.Join(target, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	n := removeAll(target)
	if n != 2 {
		t.Errorf("removeAll count = %d, want 2", n)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("target directory was not removed")
	}
}

func TestPruneThumbFiles_RemovesOnlyStaleHashes(t *testing.T) {
	dir := t.TempDir()
	live := "aabbcc"
	stale := "ddeeff"
	for _, name := range []string{live + "_256_cover.jpg", stale + "_256_cover.jpg", "malformed.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	removed := pruneThumbFiles(dir, map[string]struct{}{live: {}})
	if removed != 1 {
		t.Fatalf("pruneThumbFiles removed = %d, want 1", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, live+"_256_cover.jpg")); err != nil {
		t.Error("live hash file was removed")
	}
	if _, err := os.Stat(filepath.Join(dir, stale+"_256_cover.jpg")); !os.IsNotExist(err) {
		t.Error("stale hash file was not removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "malformed.jpg")); err != nil {
		t.Error("malformed (non-cache-schema) file should be left alone")
	}
}
