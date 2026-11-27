package jobs

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PruneThumbs entfernt Cache-Dateien unterhalb von <data>/thumbs/v2, die
// keinem aktiven Inhalt mehr zugeordnet sind. Verwaiste Einträge entstehen,
// wenn Dateien geändert (neuer Hash), gelöscht oder ihre Sync-Roots /
// Benutzer entfernt werden — die alten Cache-Einträge bleiben sonst
// dauerhaft auf der Platte liegen.
//
// Layout: thumbs/v2/<userID>/<rootID>/<hex(hash)>_<size>_<fit>.<jpg|png>
//
// Eine Datei wird gelöscht, wenn EINE der folgenden Bedingungen zutrifft:
//   - userID-Verzeichnis gehört keinem existierenden Benutzer mehr
//   - rootID-Verzeichnis gehört keiner existierenden Sync-Root mehr
//   - der Content-Hash kommt in keiner aktiven (nicht-gelöschten) Datei
//     dieser Sync-Root mehr vor
//
// Leere User- und Root-Verzeichnisse werden ebenfalls entfernt.
// Liefert die Anzahl entfernter Dateien.
func PruneThumbs(ctx context.Context, pool *pgxpool.Pool, dataPath string) (int, error) {
	root := filepath.Join(dataPath, "thumbs", "v2")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return 0, nil
	}

	users, err := loadIDSet(ctx, pool, `SELECT id FROM users`)
	if err != nil {
		return 0, fmt.Errorf("load users: %w", err)
	}
	roots, err := loadIDSet(ctx, pool, `SELECT id FROM sync_roots`)
	if err != nil {
		return 0, fmt.Errorf("load sync_roots: %w", err)
	}
	hashesByRoot, err := loadActiveHashesByRoot(ctx, pool)
	if err != nil {
		return 0, fmt.Errorf("load content hashes: %w", err)
	}

	removed := 0
	userDirs, err := os.ReadDir(root)
	if err != nil {
		return 0, fmt.Errorf("read thumbs dir: %w", err)
	}
	for _, ud := range userDirs {
		if !ud.IsDir() {
			continue
		}
		userPath := filepath.Join(root, ud.Name())
		uid, err := uuid.Parse(ud.Name())
		if err != nil {
			continue
		}
		if _, ok := users[uid]; !ok {
			n := removeAll(userPath)
			removed += n
			continue
		}
		rootDirs, err := os.ReadDir(userPath)
		if err != nil {
			slog.Warn("thumb prune: read user dir failed", "dir", userPath, "error", err)
			continue
		}
		for _, rd := range rootDirs {
			if !rd.IsDir() {
				continue
			}
			rootPath := filepath.Join(userPath, rd.Name())
			rid, err := uuid.Parse(rd.Name())
			if err != nil {
				continue
			}
			if _, ok := roots[rid]; !ok {
				removed += removeAll(rootPath)
				continue
			}
			liveHashes := hashesByRoot[rid]
			removed += pruneThumbFiles(rootPath, liveHashes)
			_ = removeIfEmpty(rootPath)
		}
		_ = removeIfEmpty(userPath)
	}
	return removed, nil
}

// pruneThumbFiles geht alle Cache-Dateien eines Sync-Root-Verzeichnisses durch
// und entfernt jene, deren Content-Hash nicht in liveHashes vorkommt.
func pruneThumbFiles(dir string, liveHashes map[string]struct{}) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		hashHex := parseThumbHash(name)
		if hashHex == "" {
			continue
		}
		if _, ok := liveHashes[hashHex]; ok {
			continue
		}
		full := filepath.Join(dir, name)
		if err := os.Remove(full); err != nil {
			slog.Warn("thumb prune: remove failed", "path", full, "error", err)
			continue
		}
		removed++
	}
	return removed
}

// parseThumbHash extrahiert den Hex-Hash-Präfix vor dem ersten Underscore.
// Liefert "" für Dateinamen, die nicht dem Cache-Schema entsprechen.
func parseThumbHash(name string) string {
	us := strings.IndexByte(name, '_')
	if us <= 0 {
		return ""
	}
	candidate := name[:us]
	if _, err := hex.DecodeString(candidate); err != nil {
		return ""
	}
	return candidate
}

func loadActiveHashesByRoot(ctx context.Context, pool *pgxpool.Pool) (map[uuid.UUID]map[string]struct{}, error) {
	rows, err := pool.Query(ctx, `
		SELECT sync_root_id, content_hash
		  FROM files
		 WHERE NOT is_deleted
		   AND content_hash IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]map[string]struct{}{}
	for rows.Next() {
		var rootID uuid.UUID
		var hash []byte
		if err := rows.Scan(&rootID, &hash); err != nil {
			return nil, err
		}
		set, ok := out[rootID]
		if !ok {
			set = map[string]struct{}{}
			out[rootID] = set
		}
		set[hex.EncodeToString(hash)] = struct{}{}
	}
	return out, rows.Err()
}

// removeAll entfernt einen Pfad rekursiv und liefert die Anzahl regulärer
// Dateien, die dabei verschwunden sind (für die Job-Statistik).
func removeAll(path string) int {
	count := 0
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			count++
		}
		return nil
	})
	if err := os.RemoveAll(path); err != nil {
		slog.Warn("thumb prune: removeAll failed", "path", path, "error", err)
		return 0
	}
	return count
}

func removeIfEmpty(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return os.Remove(path)
	}
	return nil
}
