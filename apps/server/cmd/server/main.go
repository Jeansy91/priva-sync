package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"privasync/server/internal/api"
	"privasync/server/internal/api/middleware"
	"privasync/server/internal/assembler"
	"privasync/server/internal/audit"
	"privasync/server/internal/bootstrap"
	"privasync/server/internal/bus"
	"privasync/server/internal/config"
	"privasync/server/internal/cron"
	"privasync/server/internal/db"
	"privasync/server/internal/httpx"
	"privasync/server/internal/jobs"
	"privasync/server/internal/mail"
	"privasync/server/internal/outbox"
	"privasync/server/internal/quota"
	"privasync/server/internal/repository"
	"privasync/server/internal/sse"
	"privasync/server/internal/storage"
	syncpkg "privasync/server/internal/sync"
	"privasync/server/internal/webdav"
)

const trashRetention = 30 * 24 * time.Hour

// WebDAV rate limit. Far looser than the login route because a real client
// (rclone, a mounted drive) issues many requests per second legitimately;
// tight enough that credential guessing on a port without a second factor
// costs an attacker real time.
const (
	webdavRateLimitPerSecond = 20
	webdavRateLimitBurst     = 100
)
const trashPurgeInterval = 24 * time.Hour

// Chunked-upload assembly (ADR-0043). Few workers on purpose: the work
// is bound by the data volume, so more of them only make the same disk slower
// while multiplying the staging space in use. One per user keeps a queue of
// large files from starving everyone else.
const (
	assemblyWorkers     = 2
	assemblyPerUser     = 1
	assemblyMaxAttempts = 3
	assemblyRetryDelay  = 30 * time.Second
	// A single attempt on a very large file is minutes of I/O; the cap is
	// there to release a worker that is genuinely stuck, not to bound normal
	// work.
	assemblyTimeout = 6 * time.Hour
	// How long a finished session stays answerable. The desktop retries a
	// failed transfer for up to twelve hours, and asks about the result over
	// the status endpoint — the row must outlive that.
	completedUploadRetention = 24 * time.Hour
)

// openDailyLogFile opens (or creates) <dataPath>/.logs/server-YYYY-MM-DD.log
// in append mode. The file is left open for the lifetime of the process.
func openDailyLogFile(dataPath string) (*os.File, error) {
	logDir := filepath.Join(dataPath, ".logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("server-%s.log", time.Now().UTC().Format("2006-01-02"))
	return os.OpenFile(filepath.Join(logDir, name),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
}

// nextMidnightAfter returns the first 00:00:00 UTC that is at least
// stepDays full days after now (>= stepDays*24h away). stepDays=1 → tomorrow
// midnight; stepDays=7 → midnight 7 days from now.
func nextMidnightAfter(now time.Time, stepDays int) time.Time {
	n := now.UTC()
	base := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
	return base.AddDate(0, 0, stepDays)
}

// runAtMidnightEveryNDays runs the named job once at startup, then every
// stepDays days at 00:00 UTC. The registry's next-run hint is updated before
// each wait so the admin UI always shows the upcoming execution.
func runAtMidnightEveryNDays(ctx context.Context, reg *jobs.Registry, name, scheduleDesc string, stepDays int) {
	fire := func() {
		if _, err := reg.RunOnce(ctx, name); err != nil && !errors.Is(err, jobs.ErrAlreadyRunning) {
			slog.Error("scheduled job: run failed", "job", name, "error", err)
		}
	}
	reg.SetSchedule(name, scheduleDesc, time.Now().UTC())
	fire()
	for {
		next := nextMidnightAfter(time.Now(), stepDays)
		reg.SetSchedule(name, scheduleDesc, next)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			fire()
		}
	}
}

// runUploadSessionMaintenance sweeps expired upload sessions once at startup
// and then every trashPurgeInterval (24 h). Trash retention moved to the
// bus-driven cron.Purger.
func runUploadSessionMaintenance(ctx context.Context, uploads *repository.UploadRepository, stor *storage.Service) {
	purgeUploadSessions(ctx, uploads, stor)
	ticker := time.NewTicker(trashPurgeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			purgeUploadSessions(ctx, uploads, stor)
		}
	}
}

func purgeUploadSessions(ctx context.Context, uploads *repository.UploadRepository, stor *storage.Service) {
	sessions, err := uploads.ListExpiredSessions(ctx, time.Now())
	if err != nil {
		slog.Error("session purge: listing failed", "error", err)
		return
	}
	purged := 0
	staging := 0
	for _, s := range sessions {
		_ = stor.DeleteChunkDir(s.ID)
		// The staging copy is written next to the target and renamed onto it,
		// so a session that never completed leaves a full-size copy behind.
		if n, err := stor.PurgeStagingFor(s.UserID, s.SyncRootID.String(), s.Path); err != nil {
			slog.Warn("session purge: staging cleanup failed", "session_id", s.ID, "error", err)
		} else {
			staging += n
		}
		if err := uploads.DeleteSession(ctx, s.ID); err != nil {
			slog.Warn("session purge: record delete failed", "session_id", s.ID, "error", err)
			continue
		}
		purged++
	}
	if purged > 0 {
		slog.Info("upload session purge complete", "purged", purged, "staging_files", staging)
	}

	// Chunk directories can outlive their session — an interrupted purge, a row
	// removed without its bytes. Nothing else collects them, and each holds a
	// full copy of an abandoned upload.
	live, err := uploads.ListSessionIDs(ctx)
	if err != nil {
		slog.Error("session purge: listing live sessions failed", "error", err)
		return
	}
	liveIDs := make(map[string]struct{}, len(live))
	for _, id := range live {
		liveIDs[id.String()] = struct{}{}
	}
	if removed, err := stor.PurgeOrphanChunkDirs(liveIDs); err != nil {
		slog.Warn("session purge: orphaned chunk cleanup failed", "error", err)
	} else if removed > 0 {
		slog.Info("orphaned chunk directories removed", "count", removed)
	}
}

// requeueStrandedAssemblies picks up sessions that were queued or being
// assembled when the process last stopped. Exactly one server instance runs
// (ADR-0039), so nothing else can be holding them — this sweep is the whole
// recovery story for work interrupted by a restart or a crash.
func requeueStrandedAssemblies(ctx context.Context, uploads *repository.UploadRepository, queue *assembler.Pool) {
	stranded, err := uploads.RequeueInFlight(ctx)
	if err != nil {
		slog.Error("assembly sweep: listing stranded sessions failed", "error", err)
		return
	}
	for _, s := range stranded {
		queue.Submit(assembler.Job{SessionID: s.ID, UserID: s.UserID})
	}
	if len(stranded) > 0 {
		slog.Info("assembly sweep: stranded upload sessions requeued", "count", len(stranded))
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "Path to config file (optional)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Tee logs to <data>/.logs/server-YYYY-MM-DD.log so they survive
	// terminal restarts. Stdout output is preserved for `docker logs`.
	if logFile, err := openDailyLogFile(cfg.Storage.DataPath); err == nil {
		logger := slog.New(slog.NewJSONHandler(
			io.MultiWriter(os.Stdout, logFile),
			&slog.HandlerOptions{Level: slog.LevelInfo},
		))
		slog.SetDefault(logger)
	} else {
		slog.Warn("log file setup failed — stdout only", "error", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := db.NewPool(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	slog.Info("running database migrations")
	if err := db.Migrate(pool); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	// Services
	stor := storage.NewService(cfg.Storage.DataPath)
	// Chunked uploads publish their bytes by renaming out of .chunks into the
	// sync root. Refuse to start when that rename cannot work: the alternative
	// is every large upload failing after the whole transfer (ADR-0045).
	if err := stor.VerifyChunkVolume(); err != nil {
		return fmt.Errorf("verifying chunk staging volume: %w", err)
	}

	// Repositories
	users := repository.NewUserRepository(pool, stor)
	files := repository.NewFileRepository(pool)
	syncRoots := repository.NewSyncRootRepository(pool, stor)
	conflicts := repository.NewConflictRepository(pool)
	trash := repository.NewTrashRepository(pool)
	uploads := repository.NewUploadRepository(pool)
	shares := repository.NewShareRepository(pool, stor)
	audits := audit.NewRepository(pool)

	// SSE Hub
	sseHub := sse.NewHub()

	// Mailer (no-op when cfg.SMTP.Enabled=false).
	mailer, err := mail.New(cfg.SMTP)
	if err != nil {
		return fmt.Errorf("mail init: %w", err)
	}
	mailerCtx, mailerCancel := context.WithCancel(context.Background())
	defer mailerCancel()
	mailer.Start(mailerCtx)
	defer mailer.Stop()

	// First-run Admin-Bootstrap: emits a one-shot setup token to stdout
	// when no admin exists. See internal/bootstrap/setup.go.
	setupState, err := bootstrap.Init(ctx, users)
	if err != nil {
		return fmt.Errorf("bootstrap init: %w", err)
	}

	// Outbox + SyncCoordinator
	outboxRepo := outbox.NewRepository(pool)
	outboxHandlers := syncpkg.BuildHandlers(stor, sseHub, mailer, users, outboxRepo, cfg.Server.BaseURL)
	// Fail the boot if any enqueued kind lacks a handler, rather than letting
	// such rows back off forever at retry time.
	if err := outboxHandlers.Require(outbox.AllKinds...); err != nil {
		return fmt.Errorf("outbox handlers: %w", err)
	}
	outboxWorker := outbox.NewWorker(outboxRepo, outboxHandlers)
	outboxCtx, outboxCancel := context.WithCancel(context.Background())
	defer outboxCancel()
	go outboxWorker.Run(outboxCtx)

	// DomainEventBus: in-process Pub/Sub for domain events.
	// Critical subscribers run sync in the publisher's TX; reactive
	// subscribers run on the worker pool post-commit.
	domainBus := bus.New(bus.Config{})
	defer domainBus.Close()

	// Self-registering subscribers: each package declares its own bus
	// contract (critical vs reactive) in Register; main.go just constructs and
	// wires them in one call. Order is preserved so critical dispatch for a
	// shared event stays quota → mail → sse.
	quotaSvcMain := quota.New(users, pool, mailer, cfg.Server.BaseURL)
	mailSub := mail.NewSubscriber(outboxRepo, users)
	quotaSSESub := syncpkg.NewQuotaSSESubscriber(outboxRepo)
	bus.WireSubscribers(domainBus, quotaSvcMain, mailSub, quotaSSESub)

	coord := syncpkg.NewCoordinator(pool, files, syncRoots, conflicts, trash, outboxRepo, outboxWorker, quotaSvcMain, domainBus, stor)

	// Admin-triggerable background jobs. Registered before the router so the
	// admin handler can list them; the scheduled trash sweep also goes
	// through the registry so manual + scheduled runs share the same path.
	jobRegistry := jobs.NewRegistry(pool)
	purger := cron.NewPurger(pool, syncRoots, trash, stor, domainBus, trashRetention)
	// Labels intentionally left blank: the web UI translates by job ID
	// (admin.jobs.labels.<id>) so every visible string is i18n-clean.
	jobRegistry.Register("trash_purge", "", func(ctx context.Context) (int, error) {
		return purger.PurgeOnce(ctx)
	})
	jobRegistry.Register("sidecar_prune", "", func(ctx context.Context) (int, error) {
		return jobs.PruneSidecars(ctx, pool, cfg.Storage.DataPath)
	})
	jobRegistry.Register("thumb_prune", "", func(ctx context.Context) (int, error) {
		return jobs.PruneThumbs(ctx, pool, cfg.Storage.DataPath)
	})
	jobRegistry.Register("directory_repair", "", func(ctx context.Context) (int, error) {
		return jobs.RepairDirectories(ctx, files)
	})
	jobRegistry.Register("quota_recount", "", func(ctx context.Context) (int, error) {
		return jobs.RecountQuotas(ctx, users, quotaSvcMain)
	})
	// Manual-only: destructive cleanup of /data/{user_id}/ dirs whose user is
	// gone from BOTH the DB and the users-sidecar floor. No SetSchedule call —
	// admin triggers explicitly from the UI.
	jobRegistry.Register("orphan_user_dirs_prune", "", func(ctx context.Context) (int, error) {
		return jobs.PruneOrphanUserDirs(ctx, pool, cfg.Storage.DataPath)
	})

	// Chunked-upload assembly. Reading every chunk of a multi-gigabyte upload,
	// hashing it and committing it takes minutes — longer than any request may
	// live — so complete only accepts the work and this pool does it.
	assemblySvc := assembler.NewService(uploads, stor, coord, assembler.Options{
		MaxAttempts: assemblyMaxAttempts,
		Retain:      completedUploadRetention,
		Timeout:     assemblyTimeout,
	})
	assemblyQueue := assembler.New(assembler.Config{
		Workers:    assemblyWorkers,
		PerUser:    assemblyPerUser,
		RetryDelay: assemblyRetryDelay,
		Run:        assemblySvc.Assemble,
	})
	assemblyQueue.Start()
	defer assemblyQueue.Stop()
	requeueStrandedAssemblies(ctx, uploads, assemblyQueue)

	// HTTP Router (API + Web-Frontend)
	apiRouter := api.NewRouter(cfg, pool, users, files, syncRoots, conflicts, trash, uploads, shares, audits, stor, sseHub, setupState, mailer, coord, quotaSvcMain, jobRegistry, assemblyQueue)

	// WebDAV-Server. Rate-limited like the login route: Basic Auth on this
	// port has no second factor and no interactive client to slow an attacker
	// down, so the listener itself is the only brake.
	appPasswords := repository.NewAppPasswordRepository(pool)
	webdavHandler := webdav.NewHandler(users, appPasswords, syncRoots, files, stor, coord, cfg.Auth)
	webdavTrusted, _ := httpx.ParseTrustedProxies(cfg.Server.TrustedProxies)
	webdavRL := middleware.NewRateLimiter(webdavRateLimitPerSecond, webdavRateLimitBurst)

	// Absolute for the request headers only: IdleTimeouts re-arms both on every
	// body read and write, so only a stalled peer is cut off (ADR-0072).
	const (
		readTimeout  = 30 * time.Second
		writeTimeout = 120 * time.Second
	)
	webdavH := webdavTrusted.Middleware(webdavRL.Middleware(webdavHandler))

	apiServer := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.APIPort),
		Handler:      httpx.IdleTimeouts(readTimeout, writeTimeout, apiRouter.Handler()),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  120 * time.Second,
	}

	webdavServer := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.WebDAVPort),
		Handler:      httpx.IdleTimeouts(readTimeout, writeTimeout, webdavH),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  120 * time.Second,
	}

	// Background maintenance: 30-day trash retention runs through the job
	// registry so manual + scheduled runs share the same last-run record.
	// Upload session cleanup stays out of the registry (no admin trigger).
	maintenanceCtx, maintenanceCancel := context.WithCancel(context.Background())
	defer maintenanceCancel()
	go runAtMidnightEveryNDays(maintenanceCtx, jobRegistry, "trash_purge", "daily_midnight_utc", 1)
	go runAtMidnightEveryNDays(maintenanceCtx, jobRegistry, "sidecar_prune", "weekly_midnight_utc", 7)
	go runAtMidnightEveryNDays(maintenanceCtx, jobRegistry, "thumb_prune", "weekly_midnight_utc", 7)
	go runAtMidnightEveryNDays(maintenanceCtx, jobRegistry, "directory_repair", "weekly_midnight_utc", 7)
	go runAtMidnightEveryNDays(maintenanceCtx, jobRegistry, "quota_recount", "weekly_midnight_utc", 7)
	go runUploadSessionMaintenance(maintenanceCtx, uploads, stor)

	// Graceful Shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	serverErrors := make(chan error, 2)

	go func() {
		slog.Info("starting API server", "addr", apiServer.Addr)
		serverErrors <- apiServer.ListenAndServe()
	}()

	go func() {
		slog.Info("starting WebDAV server", "addr", webdavServer.Addr)
		serverErrors <- webdavServer.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("server error: %w", err)
		}
	case sig := <-shutdown:
		slog.Info("shutting down", "signal", sig)

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutdownCancel()

		if err := apiServer.Shutdown(shutdownCtx); err != nil {
			_ = apiServer.Close()
		}
		if err := webdavServer.Shutdown(shutdownCtx); err != nil {
			_ = webdavServer.Close()
		}
	}

	return nil
}
