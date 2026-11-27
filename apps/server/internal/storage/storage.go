package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"lukechampine.com/blake3"
)

// Service encapsulates all filesystem operations.
// All files live under: {dataPath}/{userID}/{syncRootID}/{filePath}
// Trash entries under:  {dataPath}/{userID}/{syncRootID}/.trash/{path}/{deletedAt}/{basename}
type Service struct {
	dataPath string
}

func NewService(dataPath string) *Service {
	return &Service{dataPath: dataPath}
}

// DataPath returns the configured data root directory.
func (s *Service) DataPath() string { return s.dataPath }

// CreateRootDir creates the directory for a sync root.
func (s *Service) CreateRootDir(userID uuid.UUID, rootID string) error {
	dir := s.rootDir(userID, rootID)
	return os.MkdirAll(dir, 0755)
}

// WriteFile writes the contents of a reader to a file and returns the bytes
// written + the BLAKE3 hash. Writes to a temporary file first and renames
// atomically so a partial file never becomes visible.
func (s *Service) WriteFile(userID uuid.UUID, rootID, filePath string, r io.Reader) (written int64, hash []byte, err error) {
	absPath, err := s.safePath(userID, rootID, filePath)
	if err != nil {
		return 0, nil, err
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return 0, nil, fmt.Errorf("creating directories: %w", err)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(absPath), ".tmp-*")
	if err != nil {
		return 0, nil, fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := blake3.New(32, nil)
	mw := io.MultiWriter(tmpFile, hasher)

	written, err = io.Copy(mw, r)
	if err != nil {
		return 0, nil, fmt.Errorf("writing file: %w", err)
	}

	if err = tmpFile.Sync(); err != nil {
		return 0, nil, fmt.Errorf("syncing file: %w", err)
	}
	if err = tmpFile.Close(); err != nil {
		return 0, nil, fmt.Errorf("closing file: %w", err)
	}

	if err = os.Rename(tmpPath, absPath); err != nil {
		return 0, nil, fmt.Errorf("renaming temp file: %w", err)
	}

	return written, hasher.Sum(nil), nil
}

// Open returns a read-only, seekable handle to a stored file. Path traversal
// is guarded by safePath. The caller owns closing the returned file. Used by
// the WebDAV adapter, where http.ServeContent needs an io.ReadSeeker.
func (s *Service) Open(userID uuid.UUID, rootID, filePath string) (*os.File, error) {
	absPath, err := s.safePath(userID, rootID, filePath)
	if err != nil {
		return nil, err
	}
	return os.Open(absPath)
}

// CreateStaging creates a uniquely-named staging file next to its eventual
// target inside the sync root, and returns the staging file's slash-relative
// path plus an open handle. Callers that build bytes incrementally (WebDAV PUT,
// delta reconstruction) stream into the handle, then hand the staging path to
// the Sync Coordinator, which renames it onto the target. Path traversal is
// guarded by safePath; the caller owns closing and (on failure) removing the
// staging file.
func (s *Service) CreateStaging(userID uuid.UUID, rootID, targetPath string) (string, *os.File, error) {
	staging := targetPath + ".staging-" + uuid.NewString()
	absPath, err := s.safePath(userID, rootID, staging)
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return "", nil, fmt.Errorf("creating staging dir: %w", err)
	}
	f, err := os.Create(absPath)
	if err != nil {
		return "", nil, fmt.Errorf("creating staging file: %w", err)
	}
	return staging, f, nil
}

// ServeFile streamt eine Datei in den Writer.
func (s *Service) ServeFile(userID uuid.UUID, rootID, filePath string, w io.Writer) error {
	absPath, err := s.safePath(userID, rootID, filePath)
	if err != nil {
		return err
	}

	f, err := os.Open(absPath)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("file not found")
	}
	if err != nil {
		return fmt.Errorf("opening file: %w", err)
	}
	defer f.Close()

	_, err = io.Copy(w, f)
	return err
}

// DeleteFile permanently removes a file (skipping the trash).
func (s *Service) DeleteFile(userID uuid.UUID, rootID, filePath string) error {
	absPath, err := s.safePath(userID, rootID, filePath)
	if err != nil {
		return err
	}

	if err := os.Remove(absPath); errors.Is(err, os.ErrNotExist) {
		return nil // already gone is fine
	}
	return err
}

// TrashDirName is the name of the per-root trash directory.
// Never used as a sync-root directory name — reserved.
const TrashDirName = ".trash"

// trashTimestamp formats t in a filesystem-safe variant of RFC3339.
// Colons replaced with '-' so the same layout works on NTFS/FAT32 if ever extracted.
func trashTimestamp(t time.Time) string {
	return strings.ReplaceAll(t.UTC().Format(time.RFC3339), ":", "-")
}

// BuildTrashPath returns the slash-separated trash-relative path that
// TrashFile would produce for the given source path at time t. Used by the
// SyncCoordinator to pre-compute the destination before enqueuing the
// physical move into the outbox.
func BuildTrashPath(filePath string, t time.Time) string {
	rel := strings.TrimPrefix(filepath.ToSlash(filePath), "/")
	idx := strings.LastIndex(rel, "/")
	base := rel
	dir := ""
	if idx >= 0 {
		base = rel[idx+1:]
		dir = rel[:idx]
	}
	ts := trashTimestamp(t)
	if dir == "" {
		return TrashDirName + "/" + base + "/" + ts + "/" + base
	}
	return TrashDirName + "/" + dir + "/" + base + "/" + ts + "/" + base
}

// parseTrashTimestamp inverts trashTimestamp.
func parseTrashTimestamp(s string) (time.Time, error) {
	// Undo the colon→dash swap in the time portion only (date part has no colons,
	// but we restore all dashes after the 'T' that came from colons).
	t := s
	if i := strings.Index(s, "T"); i != -1 {
		head := s[:i+1]
		tail := strings.ReplaceAll(s[i+1:], "-", ":")
		// The trailing "Z" had no colon; ReplaceAll leaves it untouched.
		t = head + tail
	}
	return time.Parse(time.RFC3339, t)
}

// TrashFile moves a file into the trash.
// Layout: {root}/.trash/{path}/{deleted-at}/{basename}
//   - {path}       = original file path relative to the root (directory)
//   - {deleted-at} = RFC3339 timestamp (colons replaced with '-', FS-safe)
//   - {basename}   = original filename
//
// Re-deleting the same path produces another timestamped sub-folder, so old
// versions are retained. Returns the relative trash path (ToSlash).
func (s *Service) TrashFile(userID uuid.UUID, rootID, filePath string) (trashPath string, err error) {
	absPath, err := s.safePath(userID, rootID, filePath)
	if err != nil {
		return "", fmt.Errorf("invalid source path: %w", err)
	}

	if _, statErr := os.Stat(absPath); errors.Is(statErr, os.ErrNotExist) {
		return "", nil // file already gone — nothing to trash
	}

	base := s.rootDir(userID, rootID)
	rel := strings.TrimPrefix(filepath.ToSlash(filePath), "/")
	if rel == "" {
		return "", fmt.Errorf("cannot trash root")
	}
	ts := trashTimestamp(time.Now())
	trashDir := filepath.Join(base, TrashDirName, filepath.FromSlash(rel), ts)
	if err := os.MkdirAll(trashDir, 0755); err != nil {
		return "", fmt.Errorf("creating trash dir: %w", err)
	}

	dst := filepath.Join(trashDir, filepath.Base(absPath))
	if err := os.Rename(absPath, dst); err != nil {
		return "", fmt.Errorf("moving to trash: %w", err)
	}

	relOut, _ := filepath.Rel(base, dst)
	return filepath.ToSlash(relOut), nil
}

// RestoreFile moves a trashed file back to its original location.
// `relTrashPath` is the slash-separated path stored in files.trash_path
// (e.g. ".trash/docs/report.pdf/2026-04-17T18-22-03Z/report.pdf").
func (s *Service) RestoreFile(userID uuid.UUID, rootID, relTrashPath, originalPath string) error {
	base := s.rootDir(userID, rootID)
	src := filepath.Clean(filepath.Join(base, filepath.FromSlash(relTrashPath)))
	if !strings.HasPrefix(src, filepath.Clean(base)+string(os.PathSeparator)) {
		return fmt.Errorf("invalid trash path: outside root")
	}

	dst, err := s.safePath(userID, rootID, originalPath)
	if err != nil {
		return fmt.Errorf("invalid restore target path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("creating destination directories: %w", err)
	}

	if err := os.Rename(src, dst); err != nil {
		return err
	}
	s.pruneEmptyTrashDirs(base, filepath.Dir(src))
	return nil
}

// DeleteTrash permanently removes a previously trashed file from disk
// and cleans up now-empty parent directories up to .trash/.
func (s *Service) DeleteTrash(userID uuid.UUID, rootID, relTrashPath string) error {
	base := s.rootDir(userID, rootID)
	abs := filepath.Clean(filepath.Join(base, filepath.FromSlash(relTrashPath)))
	if !strings.HasPrefix(abs, filepath.Clean(base)+string(os.PathSeparator)) {
		return fmt.Errorf("invalid trash path: outside root")
	}
	if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.pruneEmptyTrashDirs(base, filepath.Dir(abs))
	return nil
}

// pruneEmptyTrashDirs removes empty ancestor directories inside .trash/ up to
// (but not including) the per-root .trash/ directory itself.
func (s *Service) pruneEmptyTrashDirs(rootBase, startDir string) {
	trashRoot := filepath.Join(rootBase, TrashDirName)
	dir := startDir
	for {
		if dir == trashRoot || !strings.HasPrefix(dir, trashRoot+string(os.PathSeparator)) {
			return
		}
		if err := os.Remove(dir); err != nil {
			return // not empty or other error — stop
		}
		dir = filepath.Dir(dir)
	}
}

// MoveFile verschiebt eine Datei atomar innerhalb desselben Sync-Roots.
func (s *Service) MoveFile(userID uuid.UUID, rootID, fromPath, toPath string) error {
	src, err := s.safePath(userID, rootID, fromPath)
	if err != nil {
		return fmt.Errorf("invalid source path: %w", err)
	}
	dst, err := s.safePath(userID, rootID, toPath)
	if err != nil {
		return fmt.Errorf("invalid destination path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("creating destination directories: %w", err)
	}

	return os.Rename(src, dst)
}

// CopyFile dupliziert eine Datei innerhalb des Sync-Roots. Verwendet von
// Move-Move-Konflikten: das Original bleibt am current-Server-Pfad stehen,
// die Kopie landet unter dem Konflikt-Markerpfad.
func (s *Service) CopyFile(userID uuid.UUID, rootID, fromPath, toPath string) error {
	src, err := s.safePath(userID, rootID, fromPath)
	if err != nil {
		return fmt.Errorf("invalid source path: %w", err)
	}
	dst, err := s.safePath(userID, rootID, toPath)
	if err != nil {
		return fmt.Errorf("invalid destination path: %w", err)
	}
	return copyRegularFile(src, dst)
}

// CopyPath duplicates either a single file or a whole directory subtree from
// fromPath to toPath within the same sync root. A directory source is walked
// recursively (including empty directories); a regular file delegates to
// CopyFile. Used by the copy pipeline. A missing source is tolerated as
// a no-op so a crash between the DB commit and this storage step stays
// self-healing — the DB rows are the source of truth.
func (s *Service) CopyPath(userID uuid.UUID, rootID, fromPath, toPath string) error {
	src, err := s.safePath(userID, rootID, fromPath)
	if err != nil {
		return fmt.Errorf("invalid source path: %w", err)
	}
	info, err := os.Stat(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	if !info.IsDir() {
		return s.CopyFile(userID, rootID, fromPath, toPath)
	}

	dstRoot, err := s.safePath(userID, rootID, toPath)
	if err != nil {
		return fmt.Errorf("invalid destination path: %w", err)
	}
	return filepath.WalkDir(src, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0755)
		}
		return copyRegularFile(p, dst)
	})
}

// copyRegularFile streams the bytes of src into dst, creating parent
// directories as needed. Shared by CopyFile and the directory walk in
// CopyPath.
func copyRegularFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("creating destination directories: %w", err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening source: %w", err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("creating destination: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copying bytes: %w", err)
	}
	return nil
}

// FileSize returns the on-disk size of a file. No tracking entry required —
// e.g. for conflict copies recorded only in the `conflicts` table. Returns
// 0 when the file does not exist.
func (s *Service) FileSize(userID uuid.UUID, rootID, filePath string) (int64, error) {
	abs, err := s.safePath(userID, rootID, filePath)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return info.Size(), nil
}

// HashFile computes the BLAKE3 hash of a file without loading it fully into RAM.
func (s *Service) HashFile(userID uuid.UUID, rootID, filePath string) ([]byte, error) {
	absPath, err := s.safePath(userID, rootID, filePath)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	hasher := blake3.New(32, nil)
	if _, err := io.Copy(hasher, f); err != nil {
		return nil, err
	}
	return hasher.Sum(nil), nil
}

// ─── Chunked Upload ───────────────────────────────────────────────────────────

// chunkDir returns the path to the temporary directory that holds chunks for
// a given upload session.  Located under {dataPath}/.chunks/{sessionID}/.
func (s *Service) chunkDir(sessionID fmt.Stringer) string {
	return filepath.Join(s.dataPath, ".chunks", sessionID.String())
}

// stagingFile is the single file every chunk of a session writes into, each
// at its own offset. Its path is derived purely from the session ID, so it
// stays out of the user root where listing, directory_repair and FS recovery
// would see it.
func (s *Service) stagingFile(sessionID fmt.Stringer) string {
	return filepath.Join(s.chunkDir(sessionID), "data")
}

// WriteChunkAt writes raw chunk bytes at their offset in the session's staging
// file, creating it sparse on first touch. Returns the number of bytes written.
//
// Chunks arrive in whatever order parallel uploads finish; each call uses its
// own handle, so the writes do not share a file offset. A gap left by a chunk
// that never arrives reads back as zero bytes — the caller must verify size and
// hash before publishing the file, or a hole becomes silent corruption.
func (s *Service) WriteChunkAt(sessionID fmt.Stringer, offset int64, r io.Reader) (int64, error) {
	if err := os.MkdirAll(s.chunkDir(sessionID), 0755); err != nil {
		return 0, fmt.Errorf("creating chunk dir: %w", err)
	}

	f, err := os.OpenFile(s.stagingFile(sessionID), os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return 0, fmt.Errorf("opening staging file: %w", err)
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, fmt.Errorf("seeking to chunk offset: %w", err)
	}
	n, err := io.Copy(f, r)
	if err != nil {
		return 0, fmt.Errorf("writing chunk: %w", err)
	}
	return n, f.Sync()
}

// FinalizeChunks reads the session's staging file once — hashing it and, when
// tee is non-nil, mirroring the bytes there — and then moves it to finalPath.
// Returns (bytesRead, blake3Hash, error).
//
// The single pass is the point: the bytes are already whole on disk, so the
// total hash and the delta block signatures both come out of this one read
// instead of two full-length ones.
//
// The move is a rename, which requires the chunk directory and the data root to
// live on the same volume — see VerifyChunkVolume, which asserts that at start.
// The chunk directory is NOT deleted here — call DeleteChunkDir after success.
func (s *Service) FinalizeChunks(
	sessionID fmt.Stringer,
	userID uuid.UUID,
	rootID string,
	finalPath string,
	tee io.Writer,
) (written int64, sum []byte, err error) {
	absPath, err := s.safePath(userID, rootID, finalPath)
	if err != nil {
		return 0, nil, fmt.Errorf("invalid final path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return 0, nil, fmt.Errorf("creating destination directories: %w", err)
	}

	stagingPath := s.stagingFile(sessionID)
	f, err := os.Open(stagingPath)
	if err != nil {
		return 0, nil, fmt.Errorf("opening staging file: %w", err)
	}

	hasher := blake3.New(32, nil)
	var sink io.Writer = hasher
	if tee != nil {
		sink = io.MultiWriter(hasher, tee)
	}
	// A 1 MiB buffer over a multi-gigabyte file: the delta block size, and two
	// orders of magnitude fewer syscalls than io.Copy's default.
	written, err = io.CopyBuffer(sink, f, make([]byte, 1<<20))
	f.Close()
	if err != nil {
		return 0, nil, fmt.Errorf("reading staging file: %w", err)
	}

	if err := os.Rename(stagingPath, absPath); err != nil {
		return 0, nil, fmt.Errorf("finalising upload: %w", err)
	}
	return written, hasher.Sum(nil), nil
}

// VerifyChunkVolume asserts the invariant the chunked-upload path rests on:
// {dataPath}/.chunks and the sync roots under {dataPath} live on the same
// volume, so publishing an upload is a rename and not a second full copy.
//
// It probes rather than inspects, because "same volume" has no portable
// answer — a bind mount, a junction or a per-user mount under the data path
// all break it in ways a device number would not reveal. Called at startup:
// once this is wrong, every chunked upload fails at its very last step, after
// the bytes have already been transferred.
func (s *Service) VerifyChunkVolume() error {
	dir := filepath.Join(s.dataPath, ".chunks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating chunk dir: %w", err)
	}

	name := ".volume-probe-" + uuid.NewString()
	from := filepath.Join(dir, name)
	to := filepath.Join(s.dataPath, name)
	if err := os.WriteFile(from, nil, 0644); err != nil {
		return fmt.Errorf("writing volume probe: %w", err)
	}
	defer func() { _ = os.Remove(from) }()

	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf(
			"chunk dir %s and the data root must be on the same volume, renaming across them failed: %w",
			dir, err)
	}
	return os.Remove(to)
}

// PurgeOrphanChunkDirs removes chunk directories that no live session claims.
// A directory outlives its session whenever a purge is interrupted or a row
// disappears without its bytes, and nothing else ever collects them — each one
// holds a full copy of an abandoned upload. Returns how many were removed.
func (s *Service) PurgeOrphanChunkDirs(live map[string]struct{}) (int, error) {
	base := filepath.Join(s.dataPath, ".chunks")
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil // nothing has ever been chunked here
	}
	if err != nil {
		return 0, fmt.Errorf("listing chunk dirs: %w", err)
	}

	removed := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, ok := live[e.Name()]; ok {
			continue
		}
		if err := os.RemoveAll(filepath.Join(base, e.Name())); err != nil {
			return removed, fmt.Errorf("removing orphaned chunk dir %s: %w", e.Name(), err)
		}
		removed++
	}
	return removed, nil
}

// PurgeStagingFor removes the staging files left next to targetPath. Staging
// is written beside its target and renamed onto it, so an upload that never
// completes leaves a full-size copy behind. Returns how many were removed.
func (s *Service) PurgeStagingFor(userID uuid.UUID, rootID, targetPath string) (int, error) {
	absPath, err := s.safePath(userID, rootID, targetPath)
	if err != nil {
		return 0, err
	}
	matches, err := filepath.Glob(absPath + ".staging-*")
	if err != nil {
		return 0, fmt.Errorf("globbing staging files: %w", err)
	}

	removed := 0
	for _, m := range matches {
		if err := os.Remove(m); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, fmt.Errorf("removing staging file: %w", err)
		}
		removed++
	}
	return removed, nil
}

// DeleteChunkDir removes all temporary chunk files for a session.
func (s *Service) DeleteChunkDir(sessionID fmt.Stringer) error {
	return os.RemoveAll(s.chunkDir(sessionID))
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// MakeDir creates a directory inside a sync root.  Used by the empty-directory
// sync feature so the on-disk tree mirrors the DB and recovery can detect it.
// Idempotent — existing dirs are not an error.
func (s *Service) MakeDir(userID uuid.UUID, rootID, dirPath string) error {
	absPath, err := s.safePath(userID, rootID, dirPath)
	if err != nil {
		return err
	}
	return os.MkdirAll(absPath, 0755)
}

// RootDir returns the absolute path of a sync root on disk.
// Layout: {dataPath}/{userID}/{rootID}/
func (s *Service) RootDir(userID uuid.UUID, rootID string) string {
	return s.rootDir(userID, rootID)
}

func (s *Service) rootDir(userID uuid.UUID, rootID string) string {
	return filepath.Join(s.dataPath, userID.String(), rootID)
}

// safePath erstellt den absoluten Pfad und verhindert Path-Traversal.
func (s *Service) safePath(userID uuid.UUID, rootID, filePath string) (string, error) {
	base := s.rootDir(userID, rootID)
	// filePath is already cleaned by sanitizePath(); double-check for safety.
	joined := filepath.Join(base, filepath.FromSlash(filePath))
	clean := filepath.Clean(joined)

	if !strings.HasPrefix(clean, filepath.Clean(base)+string(os.PathSeparator)) &&
		clean != filepath.Clean(base) {
		return "", fmt.Errorf("invalid path: outside sync root")
	}

	return clean, nil
}
