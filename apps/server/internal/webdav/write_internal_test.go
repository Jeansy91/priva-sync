package webdav

import (
	"context"
	"encoding/hex"
	"io"
	"os"
	"testing"

	"lukechampine.com/blake3"
)

func TestIsWriteFlag(t *testing.T) {
	cases := []struct {
		name string
		flag int
		want bool
	}{
		{"read-only", os.O_RDONLY, false},
		{"write-only", os.O_WRONLY, true},
		{"read-write", os.O_RDWR, true},
		{"create", os.O_CREATE, true},
		{"truncate", os.O_TRUNC, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isWriteFlag(c.flag); got != c.want {
				t.Errorf("isWriteFlag(%v) = %v, want %v", c.flag, got, c.want)
			}
		})
	}
}

func TestWriteHandle_UnsupportedReadSeekReaddir(t *testing.T) {
	h := &writeHandle{}

	if _, err := h.Read(nil); err != errNotImplemented {
		t.Errorf("Read() err = %v, want errNotImplemented", err)
	}
	if _, err := h.Seek(0, io.SeekStart); err != errNotImplemented {
		t.Errorf("Seek() err = %v, want errNotImplemented", err)
	}
	if _, err := h.Readdir(0); err != errNotImplemented {
		t.Errorf("Readdir() err = %v, want errNotImplemented", err)
	}
}

func TestWriteHandle_StatReportsHasherSumAsETag(t *testing.T) {
	hasher := blake3.New(32, nil)
	_, _ = hasher.Write([]byte("payload"))
	h := &writeHandle{target: "/dir/report.txt", size: 7, hasher: hasher}

	fi, err := h.Stat()
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Name() != "report.txt" {
		t.Errorf("Name() = %q, want base name %q", fi.Name(), "report.txt")
	}
	if fi.Size() != 7 {
		t.Errorf("Size() = %d, want 7", fi.Size())
	}

	want := `"` + hex.EncodeToString(hasher.Sum(nil)) + `"`
	got, err := fi.(fileInfo).ETag(context.Background())
	if err != nil || got != want {
		t.Errorf("ETag() = %q, %v, want %q, nil", got, err, want)
	}
}
