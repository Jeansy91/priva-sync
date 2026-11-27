// Package recovery reconstructs the PostgreSQL database from the on-disk
// /data directory. It reads JSON snapshots (users/sync_roots/shares) plus the
// file tree (hashes recomputed) and inserts rows with ON CONFLICT DO NOTHING.
//
// The package is invoked by cmd/recover but also imported by tests, so the
// orchestration entry points are exported and take an already-configured
// pgxpool.Pool.
package recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"lukechampine.com/blake3"

	"privasync/server/internal/storage"
)

// Counts reports how many rows were inserted per table.
type Counts struct {
	Users, SyncRoots, Shares, Files, Trashed int
}

// RunAll executes the full recovery pipeline against pool, reading from
// dataPath.  Migrations must already have run.
func RunAll(ctx context.Context, pool *pgxpool.Pool, dataPath string) (Counts, error) {
	var c Counts
	n, err := RecoverUsers(ctx, pool, dataPath)
	if err != nil {
		return c, fmt.Errorf("recovering users: %w", err)
	}
	c.Users = n

	n, err = RecoverSyncRoots(ctx, pool, dataPath)
	if err != nil {
		return c, fmt.Errorf("recovering sync roots: %w", err)
	}
	c.SyncRoots = n

	n, err = RecoverShares(ctx, pool, dataPath)
	if err != nil {
		return c, fmt.Errorf("recovering shares: %w", err)
	}
	c.Shares = n

	live, trashed, err := RecoverFiles(ctx, pool, dataPath)
	if err != nil {
		return c, fmt.Errorf("recovering files: %w", err)
	}
	c.Files = live
	c.Trashed = trashed
	return c, nil
}

// ─── Users ────────────────────────────────────────────────────────────────────

type userMeta struct {
	ID                string  `json:"id"`
	Username          string  `json:"username"`
	Email             string  `json:"email"`
	PasswordHash      string  `json:"password_hash"`
	TOTPSecret        *string `json:"totp_secret"`
	TOTPEnabled       bool    `json:"totp_enabled"`
	Role              string  `json:"role"`
	QuotaBytes        int64   `json:"quota_bytes"`
	IsActive          bool    `json:"is_active"`
	EmailNotify       bool    `json:"email_notify"`
	AvatarPath        *string `json:"avatar_path"`
	PreferredLanguage string  `json:"preferred_language"`
	ShowFileTree       bool    `json:"show_file_tree"`
	ShowFileCheckboxes bool    `json:"show_file_checkboxes"`
	TrashView          string  `json:"trash_view"`
	CreatedAt         string  `json:"created_at"`
}

func RecoverUsers(ctx context.Context, pool *pgxpool.Pool, dataPath string) (int, error) {
	dir := filepath.Join(dataPath, storage.DirUsers)
	entries, err := readJSONDir[userMeta](dir)
	if err != nil {
		return 0, err
	}

	inserted := 0
	for _, m := range entries {
		createdAt, _ := time.Parse(time.RFC3339, m.CreatedAt)
		lang := m.PreferredLanguage
		if lang == "" {
			lang = "de"
		}
		trashView := m.TrashView
		if trashView == "" {
			trashView = "list"
		}
		tag, err := pool.Exec(ctx, `
			INSERT INTO users
				(id, username, email, password_hash, totp_secret, totp_enabled,
				 role, quota_bytes, is_active, email_notify,
				 avatar_path, preferred_language, show_file_tree,
				 show_file_checkboxes, trash_view,
				 created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16)
			ON CONFLICT (id) DO NOTHING
		`, m.ID, m.Username, m.Email, m.PasswordHash, m.TOTPSecret, m.TOTPEnabled,
			m.Role, m.QuotaBytes, m.IsActive, m.EmailNotify,
			m.AvatarPath, lang, m.ShowFileTree, m.ShowFileCheckboxes, trashView, createdAt)
		if err != nil {
			slog.Warn("user insert failed", "id", m.ID, "error", err)
			continue
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, nil
}

// ─── Sync Roots ───────────────────────────────────────────────────────────────

type rootMeta struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	CreatedAt string `json:"created_at"`
}

func RecoverSyncRoots(ctx context.Context, pool *pgxpool.Pool, dataPath string) (int, error) {
	dir := filepath.Join(dataPath, storage.DirSyncRoots)
	entries, err := readJSONDir[rootMeta](dir)
	if err != nil {
		return 0, err
	}

	inserted := 0
	for _, m := range entries {
		createdAt, _ := time.Parse(time.RFC3339, m.CreatedAt)
		tag, err := pool.Exec(ctx, `
			INSERT INTO sync_roots (id, user_id, name, path, created_at)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (id) DO NOTHING
		`, m.ID, m.UserID, m.Name, m.Path, createdAt)
		if err != nil {
			slog.Warn("sync root insert failed", "id", m.ID, "error", err)
			continue
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, nil
}

// ─── Shares ───────────────────────────────────────────────────────────────────

type shareMeta struct {
	ID           string  `json:"id"`
	UserID       string  `json:"user_id"`
	SyncRootID   *string `json:"sync_root_id"`
	TargetUserID *string `json:"target_user_id"`
	Path         string  `json:"path"`
	ShareType    string  `json:"share_type"`
	Token        *string `json:"token"`
	PasswordHash *string `json:"password_hash"`
	ExpiresAt    *string `json:"expires_at"`
	CanWrite     bool    `json:"can_write"`
	CreatedAt    string  `json:"created_at"`
}

func RecoverShares(ctx context.Context, pool *pgxpool.Pool, dataPath string) (int, error) {
	dir := filepath.Join(dataPath, storage.DirShares)
	entries, err := readJSONDir[shareMeta](dir)
	if err != nil {
		return 0, err
	}

	inserted := 0
	for _, m := range entries {
		createdAt, _ := time.Parse(time.RFC3339, m.CreatedAt)
		var expiresAt *time.Time
		if m.ExpiresAt != nil {
			t, _ := time.Parse(time.RFC3339, *m.ExpiresAt)
			expiresAt = &t
		}
		tag, err := pool.Exec(ctx, `
			INSERT INTO shares
				(id, user_id, sync_root_id, target_user_id, path, share_type,
				 token, password_hash, expires_at, can_write, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (id) DO NOTHING
		`, m.ID, m.UserID, m.SyncRootID, m.TargetUserID, m.Path, m.ShareType,
			m.Token, m.PasswordHash, expiresAt, m.CanWrite, createdAt)
		if err != nil {
			slog.Warn("share insert failed", "id", m.ID, "error", err)
			continue
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, nil
}

// ─── Files ────────────────────────────────────────────────────────────────────

// RecoverFiles walks each user's sync root directories and reconstructs file
// records.  Hashes are recomputed from file contents.  Empty directories on
// disk produce is_dir=TRUE rows so explicit folders survive recovery.
// Trashed entries are recovered from each root's .trash/ subtree.
// Returns (liveRowsInserted, trashedRowsInserted).
func RecoverFiles(ctx context.Context, pool *pgxpool.Pool, dataPath string) (int, int, error) {
	rows, err := pool.Query(ctx, `SELECT id, user_id FROM sync_roots`)
	if err != nil {
		return 0, 0, fmt.Errorf("querying sync roots: %w", err)
	}
	type rootRow struct {
		id     uuid.UUID
		userID uuid.UUID
	}
	var roots []rootRow
	for rows.Next() {
		var r rootRow
		if err := rows.Scan(&r.id, &r.userID); err != nil {
			rows.Close()
			return 0, 0, err
		}
		roots = append(roots, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	live, trashed := 0, 0
	for _, root := range roots {
		l, t, err := recoverRootFiles(ctx, pool, root.id, root.userID, dataPath)
		if err != nil {
			slog.Warn("file recovery failed for root", "root_id", root.id, "error", err)
			continue
		}
		live += l
		trashed += t
	}
	return live, trashed, nil
}

func recoverRootFiles(
	ctx context.Context,
	pool *pgxpool.Pool,
	rootID, userID uuid.UUID,
	dataPath string,
) (liveCount, trashCount int, err error) {
	absRoot := filepath.Join(dataPath, userID.String(), rootID.String())
	if _, err := os.Stat(absRoot); os.IsNotExist(err) {
		return 0, 0, nil
	}

	trashDir := filepath.Join(absRoot, storage.TrashDirName)

	walkErr := filepath.WalkDir(absRoot, func(absPath string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if absPath == trashDir {
			return fs.SkipDir
		}
		if d.IsDir() {
			// Insert is_dir row for empty directories (except the root itself).
			if absPath == absRoot {
				return nil
			}
			if isEmptyDir(absPath, trashDir) {
				rel, _ := filepath.Rel(absRoot, absPath)
				logicalPath := "/" + filepath.ToSlash(rel)
				info, ierr := d.Info()
				if ierr != nil {
					return nil
				}
				tag, qerr := pool.Exec(ctx, `
					INSERT INTO files
						(user_id, sync_root_id, path, size, mtime, content_hash,
						 is_deleted, is_dir, updated_at, server_version)
					VALUES ($1,$2,$3,0,$4,NULL,FALSE,TRUE,$4,1)
					ON CONFLICT (sync_root_id, path) WHERE is_deleted = FALSE DO NOTHING
				`, userID, rootID, logicalPath, info.ModTime())
				if qerr != nil {
					slog.Warn("empty-dir insert failed", "path", logicalPath, "error", qerr)
					return nil
				}
				liveCount += int(tag.RowsAffected())
			}
			return nil
		}

		rel, _ := filepath.Rel(absRoot, absPath)
		logicalPath := "/" + filepath.ToSlash(rel)

		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}

		hash, herr := hashFile(absPath)
		if herr != nil {
			slog.Warn("hashing file failed", "path", logicalPath, "error", herr)
			return nil
		}

		tag, qerr := pool.Exec(ctx, `
			INSERT INTO files
				(user_id, sync_root_id, path, size, mtime, content_hash, is_deleted,
				 is_dir, updated_at, server_version)
			VALUES ($1,$2,$3,$4,$5,$6,FALSE,FALSE,$5,1)
			ON CONFLICT (sync_root_id, path) WHERE is_deleted = FALSE DO NOTHING
		`, userID, rootID, logicalPath, info.Size(), info.ModTime(), hash)
		if qerr != nil {
			slog.Warn("file insert failed", "path", logicalPath, "error", qerr)
			return nil
		}
		liveCount += int(tag.RowsAffected())
		return nil
	})
	if walkErr != nil {
		return liveCount, 0, walkErr
	}

	if _, err := os.Stat(trashDir); err == nil {
		trashCount, _ = recoverTrash(ctx, pool, rootID, userID, absRoot, trashDir)
	}
	return liveCount, trashCount, nil
}

// isEmptyDir reports whether dir contains no entries other than the root's
// .trash subtree.  Used to decide whether to emit an is_dir=TRUE row.
func isEmptyDir(dir, trashDir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if filepath.Join(dir, e.Name()) == trashDir {
			continue
		}
		return false
	}
	return true
}

func recoverTrash(
	ctx context.Context,
	pool *pgxpool.Pool,
	rootID, userID uuid.UUID,
	absRoot, trashDir string,
) (int, error) {
	inserted := 0
	err := filepath.WalkDir(trashDir, func(absPath string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}

		relFromTrash, _ := filepath.Rel(trashDir, absPath)
		segs := strings.Split(filepath.ToSlash(relFromTrash), "/")
		if len(segs) < 3 {
			slog.Warn("trash entry too shallow, skipping", "path", relFromTrash)
			return nil
		}

		deletedAt, perr := parseTrashTimestamp(segs[len(segs)-2])
		if perr != nil {
			slog.Warn("trash entry has unparseable timestamp, skipping",
				"path", relFromTrash, "error", perr)
			return nil
		}

		originalPath := "/" + strings.Join(segs[:len(segs)-2], "/")
		relTrashFromRoot, _ := filepath.Rel(absRoot, absPath)
		trashPath := filepath.ToSlash(relTrashFromRoot)

		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}

		hash, herr := hashFile(absPath)
		if herr != nil {
			slog.Warn("hashing trash file failed", "path", originalPath, "error", herr)
			return nil
		}

		// Trashed rows fall outside the partial-unique on (sync_root_id, path)
		// (which only covers is_deleted=FALSE), so ON CONFLICT inference would
		// fail. Re-running recovery against an already-recovered tree therefore
		// duplicates trash rows — acceptable trade-off until trash gets its own
		// unique key on (sync_root_id, trash_path).
		tag, qerr := pool.Exec(ctx, `
			INSERT INTO files
				(user_id, sync_root_id, path, size, mtime, content_hash, is_deleted,
				 updated_at, server_version, trash_path, deleted_at)
			VALUES ($1,$2,$3,$4,$5,$6,TRUE,$5,1,$7,$8)
		`, userID, rootID, originalPath, info.Size(), info.ModTime(), hash, trashPath, deletedAt)
		if qerr != nil {
			slog.Warn("trash insert failed", "path", originalPath, "error", qerr)
			return nil
		}
		inserted += int(tag.RowsAffected())
		return nil
	})
	return inserted, err
}

// parseTrashTimestamp inverts storage.trashTimestamp: colons in the time
// portion were replaced with '-' so the value is FS-safe across NTFS/FAT32.
func parseTrashTimestamp(s string) (time.Time, error) {
	t := s
	if i := strings.Index(s, "T"); i != -1 {
		head := s[:i+1]
		tail := strings.ReplaceAll(s[i+1:], "-", ":")
		t = head + tail
	}
	return time.Parse(time.RFC3339, t)
}

func hashFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := blake3.New(32, nil)
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// readJSONDir reads all *.json files in dir and decodes them into T.
// Missing directory is treated as empty (no error).
func readJSONDir[T any](dir string) ([]T, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var result []T
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			slog.Warn("skipping unreadable file", "file", e.Name(), "error", err)
			continue
		}
		var v T
		if err := json.Unmarshal(data, &v); err != nil {
			slog.Warn("skipping invalid JSON", "file", e.Name(), "error", err)
			continue
		}
		result = append(result, v)
	}
	return result, nil
}
