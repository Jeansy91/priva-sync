package handler

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/config"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
	"privasync/server/internal/version"
)

// DiskUsageFunc returns the free and total byte count for the filesystem
// hosting dir. Injected so tests run without touching the OS.
type DiskUsageFunc func(dir string) (free, total int64, err error)

// AdminStatsHandler serves the read-only admin info board.
type AdminStatsHandler struct {
	users     *repository.UserRepository
	pool      *pgxpool.Pool
	dataDir   string
	auth      config.AuthConfig
	DiskUsage DiskUsageFunc
	StartedAt time.Time
}

func NewAdminStatsHandler(users *repository.UserRepository, pool *pgxpool.Pool, dataDir string, auth config.AuthConfig) *AdminStatsHandler {
	return &AdminStatsHandler{
		users: users, pool: pool, dataDir: dataDir, auth: auth,
		DiskUsage: diskUsageZero,
		StartedAt: time.Now(),
	}
}

func diskUsageZero(string) (int64, int64, error) { return 0, 0, nil }

// GET /api/v1/admin/stats
func (h *AdminStatsHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	users, err := h.users.List(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	failed, err := h.users.FailedAttemptsByUser(ctx, h.auth.LoginLockoutWindow)
	if err != nil {
		failed = map[string]int{}
	}

	var total, active, admin, totp, locked int
	for _, u := range users {
		total++
		if u.IsActive {
			active++
		}
		if u.Role == model.RoleAdmin {
			admin++
		}
		if u.TOTPEnabled {
			totp++
		}
		if failed[u.Username] >= h.auth.MaxLoginAttempts {
			locked++
		}
	}

	var syncRoots int
	_ = h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM sync_roots`).Scan(&syncRoots)

	var filesLive, filesTrash int
	_ = h.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE is_deleted = FALSE),
			COUNT(*) FILTER (WHERE is_deleted = TRUE  AND trash_path IS NOT NULL)
		FROM files
	`).Scan(&filesLive, &filesTrash)

	var conflicts int
	_ = h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM conflicts WHERE resolved_at IS NULL`).Scan(&conflicts)

	var usedSum, trashSum, quotaSum int64
	_ = h.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(used_bytes),0), COALESCE(SUM(trash_bytes),0), COALESCE(SUM(quota_bytes),0)
		FROM users
	`).Scan(&usedSum, &trashSum, &quotaSum)

	diskFree, diskTotal, _ := h.DiskUsage(h.dataDir)

	var actLogins, actFailed int
	_ = h.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE event_type = 'auth.login.success'),
			COUNT(*) FILTER (WHERE event_type = 'auth.login.fail')
		FROM audit_log
		WHERE created_at > NOW() - INTERVAL '24 hours'
	`).Scan(&actLogins, &actFailed)

	var outboxPending int
	_ = h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox WHERE done_at IS NULL`).Scan(&outboxPending)

	var dbSize int64
	_ = h.pool.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&dbSize)

	var qWarn, qCrit int
	_ = h.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE quota_state = 'warning80'),
			COUNT(*) FILTER (WHERE quota_state = 'critical95')
		FROM users
	`).Scan(&qWarn, &qCrit)

	var sharesTotal, sharesActive, sharesPwd int
	_ = h.pool.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE expires_at IS NULL OR expires_at > NOW()),
			COUNT(*) FILTER (WHERE password_hash IS NOT NULL)
		FROM shares
	`).Scan(&sharesTotal, &sharesActive, &sharesPwd)

	writeJSON(w, http.StatusOK, map[string]any{
		"version": version.Version,
		"users": map[string]any{
			"total":  total,
			"active": active,
			"admin":  admin,
			"totp":   totp,
			"locked": locked,
		},
		"sync_roots": map[string]any{
			"total": syncRoots,
		},
		"files": map[string]any{
			"live":      filesLive,
			"trash":     filesTrash,
			"conflicts": conflicts,
		},
		"shares": map[string]any{
			"total":              sharesTotal,
			"active":             sharesActive,
			"password_protected": sharesPwd,
		},
		"health": map[string]any{
			"outbox_pending": outboxPending,
			"db_size_bytes":  dbSize,
			"uptime_seconds": int64(time.Since(h.StartedAt).Seconds()),
		},
		"activity_24h": map[string]any{
			"logins":        actLogins,
			"failed_logins": actFailed,
		},
		"quota_health": map[string]any{
			"warning80":  qWarn,
			"critical95": qCrit,
		},
		"storage": map[string]any{
			"used_bytes":       usedSum,
			"trash_bytes":      trashSum,
			"quota_bytes":      quotaSum,
			"disk_free_bytes":  diskFree,
			"disk_total_bytes": diskTotal,
		},
	})
}
