package webdav

import (
	"context"
	"io"
	"io/fs"
	"os"
	"testing"
	"time"

	"privasync/server/internal/model"
)

// ─── fileInfo ────────────────────────────────────────────────────────────────

func TestDirInfo_ReportsDirectoryModeAndBaseName(t *testing.T) {
	fi := dirInfo("/Docs/sub")

	if fi.Name() != "sub" {
		t.Errorf("Name() = %q, want %q", fi.Name(), "sub")
	}
	if !fi.IsDir() {
		t.Errorf("IsDir() = false, want true")
	}
	if fi.Mode()&os.ModeDir == 0 {
		t.Errorf("Mode() = %v, want ModeDir set", fi.Mode())
	}
	if fi.Size() != 0 {
		t.Errorf("Size() = %d, want 0 for a directory", fi.Size())
	}
	if fi.Sys() != nil {
		t.Errorf("Sys() = %v, want nil", fi.Sys())
	}
}

func TestFileInfoFromRow_LiftsPathBaseSizeMTimeAndHexETag(t *testing.T) {
	mtime := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	f := &model.File{
		Path: "/a/b/report.pdf", Size: 7,
		MTime:       mtime,
		ContentHash: []byte{0xbe, 0xef},
	}

	fi := fileInfoFromRow(f)

	if fi.Name() != "report.pdf" {
		t.Errorf("Name() = %q, want base name %q", fi.Name(), "report.pdf")
	}
	if fi.Size() != 7 {
		t.Errorf("Size() = %d, want 7", fi.Size())
	}
	if !fi.ModTime().Equal(mtime) {
		t.Errorf("ModTime() = %v, want %v", fi.ModTime(), mtime)
	}
	etag, err := fi.ETag(context.Background())
	if err != nil {
		t.Fatalf("ETag: %v", err)
	}
	if etag != `"beef"` {
		t.Errorf("ETag() = %q, want %q", etag, `"beef"`)
	}
}

// A row without a content hash -- a directory -- has no ETag to report.
func TestFileInfoFromRow_NoContentHashHasNoETag(t *testing.T) {
	fi := fileInfoFromRow(&model.File{Path: "/a/sub", IsDir: true})

	if _, err := fi.ETag(context.Background()); err == nil {
		t.Errorf("ETag() = nil error, want ErrNotImplemented for a row with no content hash")
	}
}

// ─── dirHandle ─────────────────────────────────────────────────────────────

func TestDirHandle_Readdir_PagesThroughFixedSlice(t *testing.T) {
	h := &dirHandle{name: "sub", infos: []fs.FileInfo{
		fileInfoFromRow(&model.File{Path: "/a"}),
		fileInfoFromRow(&model.File{Path: "/b"}),
		fileInfoFromRow(&model.File{Path: "/c"}),
	}}

	first, err := h.Readdir(2)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first) != 2 || first[0].Name() != "a" || first[1].Name() != "b" {
		t.Fatalf("first page = %v, want [a b]", first)
	}

	second, err := h.Readdir(2)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second) != 1 || second[0].Name() != "c" {
		t.Fatalf("second page = %v, want [c]", second)
	}

	if _, err := h.Readdir(2); err != io.EOF {
		t.Errorf("exhausted Readdir(2) err = %v, want io.EOF", err)
	}
}

func TestDirHandle_Readdir_NonPositiveCountReturnsAllThenEmpty(t *testing.T) {
	h := &dirHandle{name: "sub", infos: []fs.FileInfo{
		fileInfoFromRow(&model.File{Path: "/a"}),
	}}

	all, err := h.Readdir(-1)
	if err != nil || len(all) != 1 {
		t.Fatalf("Readdir(-1) = %v, %v, want 1 entry no error", all, err)
	}

	rest, err := h.Readdir(-1)
	if err != nil || rest != nil {
		t.Errorf("Readdir(-1) after exhaustion = %v, %v, want nil, nil", rest, err)
	}
}

func TestDirHandle_StatAndCloseAndUnsupportedOps(t *testing.T) {
	h := &dirHandle{name: "sub"}

	fi, err := h.Stat()
	if err != nil || fi.Name() != "sub" || !fi.IsDir() {
		t.Errorf("Stat() = %v, %v, want a directory named sub", fi, err)
	}
	if err := h.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
	if _, err := h.Read(nil); err != errNotImplemented {
		t.Errorf("Read() err = %v, want errNotImplemented", err)
	}
	if _, err := h.Write(nil); err != errNotImplemented {
		t.Errorf("Write() err = %v, want errNotImplemented", err)
	}
	if _, err := h.Seek(0, io.SeekStart); err != errNotImplemented {
		t.Errorf("Seek() err = %v, want errNotImplemented", err)
	}
}

// ─── fileHandle ──────────────────────────────────────────────────────────────

func TestFileHandle_DelegatesReadSeekCloseToUnderlyingFile(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), "webdav-fh-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := tmp.WriteString("hello"); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek temp file: %v", err)
	}

	h := &fileHandle{f: tmp, info: fileInfo{name: "hello.txt"}}

	buf := make([]byte, 5)
	n, err := h.Read(buf)
	if err != nil || n != 5 || string(buf) != "hello" {
		t.Fatalf("Read() = %d, %v, %q, want 5, nil, \"hello\"", n, err, buf)
	}

	if _, err := h.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	fi, err := h.Stat()
	if err != nil || fi.Name() != "hello.txt" {
		t.Errorf("Stat() = %v, %v, want name hello.txt", fi, err)
	}
	if _, err := h.Write(nil); err != errNotImplemented {
		t.Errorf("Write() err = %v, want errNotImplemented", err)
	}
	if _, err := h.Readdir(0); err != errNotImplemented {
		t.Errorf("Readdir() err = %v, want errNotImplemented", err)
	}
	if err := h.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}
