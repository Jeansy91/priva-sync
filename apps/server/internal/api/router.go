package api

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"

	"privasync/server/internal/api/handler"
	"privasync/server/internal/api/middleware"
	"privasync/server/internal/assembler"
	"privasync/server/internal/audit"
	"privasync/server/internal/bootstrap"
	"privasync/server/internal/clientrelease"
	"privasync/server/internal/config"
	"privasync/server/internal/httpx"
	"privasync/server/internal/jobs"
	"privasync/server/internal/mail"
	"privasync/server/internal/officedoc"
	"privasync/server/internal/quota"
	"privasync/server/internal/repository"
	"privasync/server/internal/sse"
	"privasync/server/internal/storage"
	syncpkg "privasync/server/internal/sync"
	"privasync/server/internal/version"
)

type Router struct {
	mux *chi.Mux
}

func NewRouter(
	cfg *config.Config,
	pool *pgxpool.Pool,
	users *repository.UserRepository,
	files *repository.FileRepository,
	syncRoots *repository.SyncRootRepository,
	conflicts *repository.ConflictRepository,
	trash *repository.TrashRepository,
	uploads *repository.UploadRepository,
	shares *repository.ShareRepository,
	audits *audit.Repository,
	stor *storage.Service,
	hub *sse.Hub,
	setup *bootstrap.State,
	mailer *mail.Mailer,
	coord syncpkg.Coordinator,
	quotaSvc *quota.Service,
	jobReg *jobs.Registry,
	assemblyQueue *assembler.Pool,
) *Router {
	r := chi.NewRouter()

	// ─── Globale Middleware ──────────────────────────────────────────────────

	// Resolves the caller's address once, under the configured trusted-proxy
	// policy, for the rate limiter, the login lockout and the audit log.
	// Replaces chimiddleware.RealIP, which validates the header's format but
	// not its origin — any caller could pick the value that ended up in
	// RemoteAddr. The error is already rejected by config.validate.
	trustedProxies, _ := httpx.ParseTrustedProxies(cfg.Server.TrustedProxies)
	r.Use(trustedProxies.Middleware)
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)
	r.Use(middleware.SecurityHeaders)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.Server.CORSOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-File-Mtime"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Login-specific rate limit: 5 requests/min per IP.
	loginRL := middleware.NewRateLimiter(5.0/60.0, 5)

	// Update-feed rate limit: 1 req/s, burst 30 per IP.
	updateRL := middleware.NewRateLimiter(1, 30)

	// ─── Handler ─────────────────────────────────────────────────────────────

	clientsRepo := repository.NewClientRepository(pool)
	passkeysRepo := repository.NewPasskeyRepository(pool)
	officeCfg := officedoc.Config{URL: cfg.DocumentServer.URL, JWTSecret: cfg.DocumentServer.JWTSecret}
	appPasswordsRepo := repository.NewAppPasswordRepository(pool)
	authH := handler.NewAuthHandler(users, audits, stor, mailer, cfg.Auth, cfg.Server.BaseURL, "PrivaSync").WithClients(clientsRepo).WithPasskeys(passkeysRepo).WithAppPasswords(appPasswordsRepo)
	fileH := handler.NewFileHandler(files, syncRoots, conflicts, trash, users, shares, audits, stor, mailer, hub, quotaSvc, coord, cfg.Server.BaseURL, cfg.Storage.MaxUploadSizeMB, officeCfg)
	uploadH := handler.NewUploadHandler(syncRoots, uploads, stor, quotaSvc, assemblyQueue)
	deltaH := handler.NewDeltaHandler(files, syncRoots, stor, coord)
	shareH := handler.NewShareHandler(shares, files, syncRoots, users, conflicts, audits, stor, mailer, hub, quotaSvc, coord, cfg.Server.BaseURL, cfg.Storage.MaxUploadSizeMB, officeCfg)
	adminH := handler.NewAdminHandler(users, audits, mailer, quotaSvc, cfg.Auth, cfg.Server.BaseURL)
	statsH := handler.NewAdminStatsHandler(users, pool, cfg.Storage.DataPath, cfg.Auth)
	statsH.DiskUsage = handler.RealDiskUsage
	jobsH := handler.NewAdminJobsHandler(jobReg)
	clientsH := handler.NewAdminClientsHandler(clientsRepo)
	sseH := handler.NewSSEHandler(hub)
	// Client manifest embedded at build time (ADR-0088). A broken one is a
	// build defect; serve no updates rather than refusing to start.
	clientManifest, err := clientrelease.Embedded()
	if err != nil {
		slog.Error("updates: embedded client manifest invalid, offering no updates", "error", err)
		clientManifest = clientrelease.Manifest{}
	}
	updateH := handler.NewUpdateHandler(clientManifest, version.Version)
	setupH := handler.NewSetupHandler(setup, users)
	userH := handler.NewUserHandler(users)

	// ─── Routen ──────────────────────────────────────────────────────────────

	r.Route("/api/v1", func(r chi.Router) {

		// First-run admin setup (public, rate-limited to deter token brute-force).
		r.With(loginRL.Middleware).Route("/setup", func(r chi.Router) {
			r.Get("/status", setupH.Status)
			r.Post("/", setupH.Complete)
		})

		// Public share links.
		// Installer listing for the web download page (public, rate-limited).
		r.With(updateRL.Middleware).Get("/client/downloads", updateH.ListDownloads)

		r.Get("/public/shares/{token}", shareH.PublicShare)
		r.Get("/public/shares/{token}/entries", shareH.PublicShareEntries)
		r.Get("/public/shares/{token}/download", shareH.PublicShareDownload)

		// Auth (public)
		r.Route("/auth", func(r chi.Router) {
			r.With(loginRL.Middleware).Post("/login", authH.Login)
			// Second factor is a guessable credential like the first — same limit.
			r.With(loginRL.Middleware).Post("/2fa", authH.Verify2FA)
			r.Post("/refresh", authH.Refresh)
			r.Post("/logout", authH.Logout)
			r.With(loginRL.Middleware).Post("/password/reset-request", authH.RequestPasswordReset)
			r.With(loginRL.Middleware).Post("/password/reset-confirm", authH.ConfirmPasswordReset)
			// Passkey login (ADR-0026): standalone login method, discoverable
			// credentials only — same rate limit as password login.
			r.With(loginRL.Middleware).Post("/passkey/login/begin", authH.PasskeyLoginBegin)
			r.With(loginRL.Middleware).Post("/passkey/login/finish", authH.PasskeyLoginFinish)
		})

		// SSE — long-lived stream, keep-alive. Sits outside the Bearer-only
		// group: a browser's EventSource cannot set headers, so this route
		// also accepts the scoped sse_token cookie (ADR-0038).
		r.With(middleware.AuthenticateSSE(cfg.Auth.JWTSecret)).Get("/events", sseH.Stream)

		// Protected routes.
		r.Group(func(r chi.Router) {
			r.Use(middleware.Authenticate(cfg.Auth.JWTSecret))

			r.Get("/auth/me", authH.Me)
			r.Put("/auth/language", authH.SetLanguage)
			r.Put("/auth/preferences/file-tree", authH.SetShowFileTree)
			r.Put("/auth/preferences/file-checkboxes", authH.SetShowFileCheckboxes)
			r.Put("/auth/preferences/trash-view", authH.SetTrashView)
			r.Put("/auth/avatar", authH.PutAvatar)
			r.Delete("/auth/avatar", authH.DeleteAvatar)
			r.Get("/auth/avatar/{userID}", authH.GetAvatar)
			r.Put("/auth/password", authH.ChangePassword)
			r.Put("/auth/profile/username", authH.SetUsername)
			r.Post("/auth/profile/email-change", authH.RequestEmailChange)
			r.Post("/auth/profile/email-change/confirm", authH.ConfirmEmailChange)
			r.Post("/auth/totp/setup", authH.SetupTOTP)
			r.Post("/auth/totp/confirm", authH.ConfirmTOTP)
			r.Delete("/auth/totp", authH.DisableTOTP)
			r.Get("/auth/totp/backup-codes", authH.ListBackupCodes)
			r.Post("/auth/totp/backup-codes/regenerate", authH.RegenerateBackupCodes)
			r.Post("/auth/passkey/register/begin", authH.RegisterPasskeyBegin)
			r.Post("/auth/passkey/register/finish", authH.RegisterPasskeyFinish)
			r.Get("/auth/passkey", authH.ListPasskeys)
			r.Delete("/auth/passkey/{passkeyID}", authH.DeletePasskey)

			// WebDAV credentials (ADR-0040): the account password is not one.
			r.Post("/auth/app-passwords", authH.CreateAppPassword)
			r.Get("/auth/app-passwords", authH.ListAppPasswords)
			r.Delete("/auth/app-passwords/{appPasswordID}", authH.DeleteAppPassword)

			// Sync roots & files.
			r.Get("/files/search", fileH.SearchFilesGlobal)
			r.Get("/sync-roots", fileH.ListSyncRoots)
			r.Post("/sync-roots", fileH.CreateSyncRoot)
			r.Patch("/sync-roots/{rootID}", fileH.RenameSyncRoot)
			r.Delete("/sync-roots/{rootID}", fileH.DeleteSyncRoot)
			r.Get("/sync-roots/{rootID}/download", fileH.DownloadSyncRoot)
			r.Post("/sync-roots/{rootID}/download-token", fileH.MintDownloadToken)
			r.Get("/sync-roots/{rootID}/files", fileH.ListFiles)
			r.Get("/sync-roots/{rootID}/files/search", fileH.SearchFiles)
			r.Put("/sync-roots/{rootID}/files", fileH.UploadFile)
			r.Patch("/sync-roots/{rootID}/files", fileH.MoveFile)
			r.Get("/sync-roots/{rootID}/files/download", fileH.DownloadFile)
			r.Get("/sync-roots/{rootID}/files/thumbnail", fileH.Thumbnail)
			r.Get("/sync-roots/{rootID}/files/office-config", fileH.OfficeConfig)
			r.Post("/sync-roots/{rootID}/files/media-token", fileH.MintMediaStreamToken)
			r.Get("/sync-roots/{rootID}/files/archive", fileH.ListArchive)
			r.Delete("/sync-roots/{rootID}/files", fileH.DeleteFile)
			r.Post("/sync-roots/{rootID}/directories", fileH.CreateDirectory)
			r.Get("/sync-roots/{rootID}/directories/stats", fileH.DirectoryStats)
			r.Get("/sync-roots/{rootID}/changes", fileH.GetChanges)
			r.Post("/sync-roots/{rootID}/files/stat", fileH.FilesStat)
			r.Get("/sync-roots/{rootID}/conflicts", fileH.ListConflicts)
			r.Get("/sync-roots/{rootID}/trash", fileH.ListTrash)
			r.Post("/sync-roots/{rootID}/trash/{fileID}/restore", fileH.RestoreTrash)
			r.Delete("/sync-roots/{rootID}/trash/{fileID}", fileH.PurgeTrash)
			r.Delete("/trash", fileH.EmptyTrash)

			// Delta-Sync (ADR-0002): block-hash request + reconstruction.
			r.Get("/sync-roots/{rootID}/files/delta", deltaH.DeltaRequest)
			r.Post("/sync-roots/{rootID}/files/delta/apply", deltaH.DeltaApply)

			// Chunked uploads (§8)
			r.Post("/sync-roots/{rootID}/uploads", uploadH.InitUpload)
			r.Put("/sync-roots/{rootID}/uploads/{uploadID}/chunks/{offset}", uploadH.UploadChunk)
			r.Get("/sync-roots/{rootID}/uploads/{uploadID}/status", uploadH.UploadStatus)
			r.Post("/sync-roots/{rootID}/uploads/{uploadID}/complete", uploadH.CompleteUpload)

			// User search (share-recipient picker, all authenticated users).
			r.Get("/users/search", userH.Search)

			// Shares
			r.Get("/shares", shareH.ListShares)
			r.Post("/sync-roots/{rootID}/shares", shareH.CreateShare)
			r.Get("/sync-roots/{rootID}/shares", shareH.ListSharesByRoot)
			r.Get("/sync-roots/{rootID}/access", shareH.ShareAccess)
			r.Post("/sync-roots/{rootID}/public-links", shareH.CreatePublicLink)
			r.Delete("/sync-roots/{rootID}/public-links", shareH.DeletePublicLink)
			r.Patch("/shares/{shareID}/grants/{userID}", shareH.UpdateShareGrant)
			r.Delete("/shares/{shareID}/grants/{userID}", shareH.DeleteShareGrant)
			r.Delete("/shares/{shareID}", shareH.DeleteShare)

			// Shared Mounts — recipient-side projection of user-shares. See ADR-0010.
			r.Get("/shared-mounts", shareH.ListSharedMounts)
			r.Delete("/shared-mounts/{shareID}", shareH.LeaveSharedMount)
			r.Get("/shared-mounts/{shareID}/stats", shareH.MountStats)
			r.Get("/shared-mounts/{shareID}/changes", shareH.MountChanges)
			r.Post("/shared-mounts/{shareID}/files/stat", shareH.MountFilesStat)
			r.Post("/shared-mounts/{shareID}/download-token", shareH.MintMountDownloadToken)
			r.Get("/shared-mounts/{shareID}/thumbnail", fileH.MountThumbnail)
			r.Get("/shared-mounts/{shareID}/files/office-config", shareH.MountOfficeConfig)
			r.Post("/shared-mounts/{shareID}/files/media-token", shareH.MintMountMediaStreamToken)
			r.Get("/shared-mounts/{shareID}/archive", fileH.MountArchive)
			r.Get("/shared-mounts/{shareID}/files", shareH.ListMountFiles)
			r.Get("/shared-mounts/{shareID}/download", shareH.DownloadMountFile)
			r.Post("/shared-mounts/{shareID}/files", shareH.UploadMountFile)
			// PUT alias so the desktop sync engine uploads to a mount with the
			// same verb + path shape as an own root (ADR-0011 symmetry).
			r.Put("/shared-mounts/{shareID}/files", shareH.UploadMountFile)
			r.Delete("/shared-mounts/{shareID}/files", shareH.DeleteMountFile)
			r.Patch("/shared-mounts/{shareID}/files", shareH.MoveMountFile)
			// Download + directories mirror /sync-roots so the desktop only
			// swaps the namespace, never the sub-path (ADR-0011 symmetry).
			r.Get("/shared-mounts/{shareID}/files/download", shareH.DownloadMountFile)
			r.Post("/shared-mounts/{shareID}/directories", shareH.CreateMountDirectory)
			// Conflicts inside the mount (ADR-0011): recipient lists and, with
			// can_write, resolves them — scoped to share.path.
			r.Get("/shared-mounts/{shareID}/conflicts", shareH.MountConflicts)
			r.Get("/shared-mounts/{shareID}/conflicts/{conflictID}", shareH.MountConflictVersions)
			r.Get("/shared-mounts/{shareID}/conflicts/{conflictID}/file", shareH.MountConflictFile)
			r.Post("/shared-mounts/{shareID}/conflicts/{conflictID}/resolve", shareH.ResolveMountConflict)

			// Conflicts.
			r.Get("/conflicts/{conflictID}", fileH.GetConflict)
			r.Get("/conflicts/{conflictID}/file", fileH.GetConflictFile)
			r.Post("/conflicts/{conflictID}/resolve", fileH.ResolveConflict)

			// Admin routes.
			r.Route("/admin", func(r chi.Router) {
				r.Use(middleware.RequireAdmin)
				r.Get("/users", adminH.ListUsers)
				r.Post("/users", adminH.CreateUser)
				r.Put("/users/{userID}/quota", adminH.SetQuota)
				r.Put("/users/{userID}/active", adminH.SetActive)
				r.Put("/users/{userID}/role", adminH.SetRole)
				r.Post("/users/{userID}/unlock", adminH.UnlockUser)
				r.Post("/users/{userID}/recompute-quota", adminH.RecomputeQuota)
				r.Get("/audit-log", adminH.ListAuditLog)
				r.Get("/stats", statsH.GetStats)
				r.Get("/clients", clientsH.List)
				r.Delete("/clients/{clientID}", clientsH.Delete)
				r.Get("/jobs", jobsH.List)
				r.Post("/jobs/{jobID}/run", jobsH.Trigger)
			})
		})
	})

	// Update feed (public, rate-limited, reachable pre-login).
	r.Route("/updates", func(r chi.Router) {
		r.Use(updateRL.Middleware)
		r.Get("/{target}/{version}", updateH.ServeManifest)
	})

	// One-time download token endpoint: lets the browser use the native save
	// dialog (no RAM buffering of large ZIPs) without an auth header.
	r.With(updateRL.Middleware).Get("/download/{token}", fileH.ServeTokenDownload)

	// Office-document fetch token (ADR-0027): the external document server
	// pulls raw bytes here — it cannot send our Bearer auth header.
	r.With(updateRL.Middleware).Get("/office/documents/{token}", fileH.ServeOfficeDocument)

	// Office save-back callback (ADR-0033): the document server POSTs
	// edited bytes here. Same reasoning as the fetch token above — public,
	// authorized by its own opaque callback token instead of Bearer auth.
	r.With(updateRL.Middleware).Post("/office/documents/callback", fileH.OfficeCallback)

	// Media-stream fetch token (ADR-0067): a native <video>/<audio> src
	// cannot carry our Bearer header, so playback is gated by its own
	// opaque, multi-use grant instead.
	r.With(updateRL.Middleware).Get("/media/{token}", fileH.ServeMediaStream)

	// Health Check
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Web frontend (SPA) — MUST come after all API routes. NotFound catches
	// anything unmatched and serves either the file or index.html as a
	// fallback for React Router.
	if cfg.Server.StaticPath != "" {
		spa := handler.NewSPAHandler(cfg.Server.StaticPath)
		r.NotFound(spa.ServeHTTP)
	}

	return &Router{mux: r}
}

func (rt *Router) Handler() http.Handler {
	return rt.mux
}
