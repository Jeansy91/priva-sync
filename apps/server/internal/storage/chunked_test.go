package storage

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type stringerID string

func (s stringerID) String() string { return string(s) }

func TestWriteChunkAt_FinalizeChunks_DeleteChunkDir(t *testing.T) {
	s := NewService(t.TempDir())
	userID := uuid.New()
	sessionID := stringerID("session-" + uuid.NewString())

	if err := s.CreateRootDir(userID, "root1"); err != nil {
		t.Fatalf("CreateRootDir: %v", err)
	}

	chunks := []string{"hello ", "brave ", "new wo"}
	for i, c := range chunks {
		n, err := s.WriteChunkAt(sessionID, int64(i*6), strings.NewReader(c))
		if err != nil {
			t.Fatalf("WriteChunkAt(%d): %v", i, err)
		}
		if n != int64(len(c)) {
			t.Errorf("WriteChunkAt(%d) wrote %d bytes, want %d", i, n, len(c))
		}
	}

	want := "hello brave new wo"
	written, hash, err := s.FinalizeChunks(sessionID, userID, "root1", "assembled.txt", nil)
	if err != nil {
		t.Fatalf("FinalizeChunks: %v", err)
	}
	if written != int64(len(want)) {
		t.Errorf("FinalizeChunks read %d bytes, want %d", written, len(want))
	}
	if len(hash) == 0 {
		t.Error("FinalizeChunks returned an empty hash")
	}

	var buf bytes.Buffer
	if err := s.ServeFile(userID, "root1", "assembled.txt", &buf); err != nil {
		t.Fatalf("ServeFile: %v", err)
	}
	if buf.String() != want {
		t.Fatalf("assembled content = %q, want %q", buf.String(), want)
	}

	if err := s.DeleteChunkDir(sessionID); err != nil {
		t.Fatalf("DeleteChunkDir: %v", err)
	}
	if _, err := s.WriteChunkAt(sessionID, 0, strings.NewReader("x")); err != nil {
		t.Fatalf("WriteChunkAt after DeleteChunkDir should recreate the dir, got: %v", err)
	}
}

// Nothing was ever uploaded for this session, so there is no staging file to
// finalise. The assembler maps fs.ErrNotExist to a permanent failure — a
// session whose bytes are gone cannot be retried into existence.
func TestFinalizeChunks_MissingStagingFileFails(t *testing.T) {
	root := t.TempDir()
	s := NewService(root)
	userID := uuid.New()
	sessionID := stringerID("session-" + uuid.NewString())

	if err := s.CreateRootDir(userID, "root1"); err != nil {
		t.Fatalf("CreateRootDir: %v", err)
	}

	_, _, err := s.FinalizeChunks(sessionID, userID, "root1", "out.txt", nil)
	if err == nil {
		t.Fatal("FinalizeChunks: want error when the staging file is missing")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want fs.ErrNotExist, got %v", err)
	}
	if err := s.ServeFile(userID, "root1", "out.txt", io.Discard); err == nil {
		t.Error("a failed finalise published a file into the user root")
	}
}

// The staging file must never live in the user root, where listing,
// directory_repair and FS recovery would all see a half-uploaded file.
func TestWriteChunkAt_StagesOutsideTheUserRoot(t *testing.T) {
	root := t.TempDir()
	s := NewService(root)
	userID := uuid.New()
	sessionID := stringerID("session-" + uuid.NewString())

	if err := s.CreateRootDir(userID, "root1"); err != nil {
		t.Fatalf("CreateRootDir: %v", err)
	}
	if _, err := s.WriteChunkAt(sessionID, 0, strings.NewReader("bytes")); err != nil {
		t.Fatalf("WriteChunkAt: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, ".chunks", sessionID.String(), "data")); err != nil {
		t.Fatalf("staging file not under .chunks/{sessionID}/: %v", err)
	}
	entries, err := os.ReadDir(s.RootDir(userID, "root1"))
	if err != nil {
		t.Fatalf("ReadDir root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("user root holds staged bytes: %v", entries)
	}
}

// findTempFiles walks the data path and collects files whose name starts with
// the given prefix.
func findTempFiles(t *testing.T, root, prefix string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), prefix) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return found
}

// Chunk directories outlive their session whenever a purge is interrupted or a
// session row disappears without its bytes. Nothing else ever removes them, so
// they accumulate at the full size of every abandoned upload.
func TestPurgeOrphanChunkDirs_RemovesOnlyDirsWithoutASession(t *testing.T) {
	root := t.TempDir()
	s := NewService(root)

	live := stringerID("live-" + uuid.NewString())
	orphan := stringerID("orphan-" + uuid.NewString())
	for _, id := range []stringerID{live, orphan} {
		if _, err := s.WriteChunkAt(id, 0, strings.NewReader("bytes")); err != nil {
			t.Fatalf("WriteChunkAt(%s): %v", id, err)
		}
	}

	removed, err := s.PurgeOrphanChunkDirs(map[string]struct{}{live.String(): {}})
	if err != nil {
		t.Fatalf("PurgeOrphanChunkDirs: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	if _, err := os.Stat(filepath.Join(root, ".chunks", live.String())); err != nil {
		t.Errorf("live session's chunks were removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".chunks", orphan.String())); !os.IsNotExist(err) {
		t.Errorf("orphaned chunk dir survived: %v", err)
	}
}

// A staging file is written next to its target and renamed onto it. When the
// upload never completes, the staging copy stays — a full-size copy of the
// file per abandoned attempt.
func TestPurgeStagingFor_RemovesOnlyTheTargetsStagingFiles(t *testing.T) {
	s := NewService(t.TempDir())
	userID := uuid.New()
	if err := s.CreateRootDir(userID, "root1"); err != nil {
		t.Fatalf("CreateRootDir: %v", err)
	}

	staging, f, err := s.CreateStaging(userID, "root1", "/mod.pak")
	if err != nil {
		t.Fatalf("CreateStaging: %v", err)
	}
	f.Close()
	if _, _, err := s.WriteFile(userID, "root1", "/other.bin", strings.NewReader("keep me")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	removed, err := s.PurgeStagingFor(userID, "root1", "/mod.pak")
	if err != nil {
		t.Fatalf("PurgeStagingFor: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	if err := s.ServeFile(userID, "root1", staging, io.Discard); err == nil {
		t.Error("staging file survived the purge")
	}
	if err := s.ServeFile(userID, "root1", "/other.bin", io.Discard); err != nil {
		t.Errorf("an unrelated file was removed: %v", err)
	}
}

// Chunks arrive in whatever order the client's parallel uploads finish. Each
// one writes at its own offset into a single sparse staging file, so the
// result must not depend on that order.
func TestWriteChunkAt_OrderIndependent(t *testing.T) {
	const chunkSize = 8
	body := "the quick brown fox jumps"
	chunks := []string{"the quic", "k brown ", "fox jump", "s"}

	assemble := func(order []int) string {
		s := NewService(t.TempDir())
		userID := uuid.New()
		sessionID := stringerID("session-" + uuid.NewString())
		if err := s.CreateRootDir(userID, "root1"); err != nil {
			t.Fatalf("CreateRootDir: %v", err)
		}
		for _, i := range order {
			offset := int64(i) * chunkSize
			n, err := s.WriteChunkAt(sessionID, offset, strings.NewReader(chunks[i]))
			if err != nil {
				t.Fatalf("WriteChunkAt(%d): %v", i, err)
			}
			if n != int64(len(chunks[i])) {
				t.Errorf("WriteChunkAt(%d) wrote %d bytes, want %d", i, n, len(chunks[i]))
			}
		}
		written, hash, err := s.FinalizeChunks(sessionID, userID, "root1", "out.txt", nil)
		if err != nil {
			t.Fatalf("FinalizeChunks: %v", err)
		}
		if written != int64(len(body)) {
			t.Errorf("FinalizeChunks reported %d bytes, want %d", written, len(body))
		}
		if len(hash) == 0 {
			t.Error("FinalizeChunks returned an empty hash")
		}
		var buf bytes.Buffer
		if err := s.ServeFile(userID, "root1", "out.txt", &buf); err != nil {
			t.Fatalf("ServeFile: %v", err)
		}
		return buf.String()
	}

	ascending := assemble([]int{0, 1, 2, 3})
	if ascending != body {
		t.Fatalf("ascending order produced %q, want %q", ascending, body)
	}
	if shuffled := assemble([]int{2, 0, 3, 1}); shuffled != ascending {
		t.Fatalf("out-of-order produced %q, want %q", shuffled, ascending)
	}
}

// Finalising an upload is a rename out of .chunks/ into the user root. Across
// volumes that rename fails with EXDEV — and it fails at the end of a
// multi-gigabyte upload, every time, for every user. The probe turns that into
// a startup error.
func TestVerifyChunkVolume(t *testing.T) {
	root := t.TempDir()
	s := NewService(root)

	if err := s.VerifyChunkVolume(); err != nil {
		t.Fatalf("VerifyChunkVolume on a single-volume data path: %v", err)
	}
	if probes := findTempFiles(t, root, ".volume-probe-"); len(probes) != 0 {
		t.Errorf("the probe left files behind: %v", probes)
	}

	// A data path that is not a usable directory cannot be probed at all,
	// and that is a startup problem in its own right.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := NewService(file).VerifyChunkVolume(); err == nil {
		t.Error("VerifyChunkVolume must fail when the data path is not a directory")
	}
}
