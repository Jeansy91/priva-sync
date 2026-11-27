package repository

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DirectoryStats holds the recursive aggregates for a directory:
// direct-child count, total subtree file bytes, total subtree files,
// most recent file mtime in the subtree. See ADR-0008.
type DirectoryStats struct {
	ItemCount   int        // direct children (files + dirs, explicit + implicit)
	FileCount   int        // all files in subtree
	TotalSize   int64      // sum of file sizes in subtree
	LatestMTime *time.Time // newest file mtime in subtree
}

// DirectoryStats returns the recursive aggregates under dirPath.
func (r *FileRepository) DirectoryStats(ctx context.Context, syncRootID uuid.UUID, dirPath string) (DirectoryStats, error) {
	prefix := dirPath
	if prefix == "" {
		prefix = "/"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	lo, hi, _ := pathPrefixRange(prefix)

	var stats DirectoryStats
	err := r.db.QueryRow(ctx, `
		SELECT
		    COALESCE(COUNT(*) FILTER (WHERE is_dir = FALSE), 0)                        AS file_count,
		    COALESCE(SUM(size) FILTER (WHERE is_dir = FALSE), 0)                       AS total_size,
		    MAX(mtime) FILTER (WHERE is_dir = FALSE)                                   AS latest_mtime,
		    COUNT(DISTINCT split_part(substring(path FROM length($1) + 1), '/', 1))    AS item_count
		FROM files
		WHERE sync_root_id = $2
		  AND is_deleted   = FALSE
		  AND path ~>=~ $3 AND path ~<~ $4
		  AND length(path) > length($1)
	`, prefix, syncRootID, lo, hi).Scan(&stats.FileCount, &stats.TotalSize, &stats.LatestMTime, &stats.ItemCount)
	if err != nil {
		return DirectoryStats{}, err
	}
	return stats, nil
}
