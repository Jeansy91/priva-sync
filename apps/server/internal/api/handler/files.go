package handler

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/audit"
	"privasync/server/internal/mail"
	"privasync/server/internal/model"
	"privasync/server/internal/officedoc"
	"privasync/server/internal/quota"
	"privasync/server/internal/repository"
	"privasync/server/internal/sse"
	"privasync/server/internal/storage"
	syncpkg "privasync/server/internal/sync"
)

// Changes-feed page size (ADR-0036) -- larger than the interactive search
// page size (files_search.go) since this drives bulk desktop sync, not
// as-you-type UI.
const (
	changesLimitDefault = 1000
	changesLimitMax     = 5000
)

// Narrow per-handler role interfaces (ADR-0029) decouple FileHandler from the
// concrete repositories so the repo-failure (500) branches become testable with
// hand-written fakes. storage/quota/coordinator/mailer/hub stay concrete — they
// flow into the shared mutationDeps (mutation_context.go) and converting them
// would ripple into ShareHandler, a separate seam.
type fileStore interface {
	FindByPath(ctx context.Context, syncRootID uuid.UUID, path string) (*model.File, error)
	ListDirectoryLevel(ctx context.Context, p repository.ListDirectoryLevelParams) ([]model.File, error)
	EachInSubtree(ctx context.Context, syncRootID uuid.UUID, dirPath string, fn func(model.File) error) error
	GetChangedSince(ctx context.Context, syncRootID uuid.UUID, sinceSeq int64, limit int) ([]model.File, error)
	ListMoveEventsSince(ctx context.Context, syncRootID uuid.UUID, sinceSeq int64, limit int) ([]model.MoveEvent, error)
	SearchFiles(ctx context.Context, p repository.SearchFilesParams) ([]model.File, error)
	DirectoryStats(ctx context.Context, syncRootID uuid.UUID, dirPath string) (repository.DirectoryStats, error)
	CreateDirectory(ctx context.Context, userID, syncRootID uuid.UUID, dirPath string, mtime time.Time) (*model.File, error)
	FindByPaths(ctx context.Context, syncRootID uuid.UUID, paths []string) ([]model.File, error)
}

type fileSyncRootStore interface {
	CreateSyncRoot(ctx context.Context, userID uuid.UUID, name, path string) (*model.SyncRoot, error)
	GetSyncRoot(ctx context.Context, id, userID uuid.UUID) (*model.SyncRoot, error)
	ListSyncRoots(ctx context.Context, userID uuid.UUID) ([]model.SyncRoot, error)
	ListSyncRootsWithOwner(ctx context.Context, userID uuid.UUID) ([]model.SyncRootWithOwner, error)
	UpdateSyncRootName(ctx context.Context, rootID, userID uuid.UUID, name string) (*model.SyncRoot, error)
}

type fileConflictStore interface {
	ListOpenConflicts(ctx context.Context, userID, rootID uuid.UUID) ([]model.Conflict, error)
	FindOpenConflictDetail(ctx context.Context, conflictID uuid.UUID) (*repository.ConflictDetail, error)
}

type fileTrashStore interface {
	ListTrash(ctx context.Context, syncRootID uuid.UUID) ([]model.File, error)
	FindTrashedByID(ctx context.Context, fileID, userID uuid.UUID) (*model.File, error)
	PurgeTrashRecord(ctx context.Context, fileID uuid.UUID) error
}

type fileShareStore interface {
	FindMountByGrant(ctx context.Context, grantID, targetUserID uuid.UUID) (*model.Share, error)
	ListMountsForTarget(ctx context.Context, targetUserID uuid.UUID) ([]model.SharedMount, error)
}

type fileAuditStore interface {
	Log(ctx context.Context, e audit.Event) error
}

// fileUserStore is only the modifier-summary lookup behind resolveLastModifiers.
type fileUserStore interface {
	FindSummariesByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]model.UserSummary, error)
}

type FileHandler struct {
	files          fileStore
	syncRoots      fileSyncRootStore
	conflicts      fileConflictStore
	trash          fileTrashStore
	users          fileUserStore
	shares         fileShareStore
	audits         fileAuditStore
	storage        *storage.Service
	mailer         *mail.Mailer
	hub            *sse.Hub
	quota          *quota.Service
	coordinator    syncpkg.Coordinator
	baseURL        string
	maxUploadBytes int64
	officeCfg      officedoc.Config
}

func NewFileHandler(
	files fileStore,
	syncRoots fileSyncRootStore,
	conflicts fileConflictStore,
	trash fileTrashStore,
	users fileUserStore,
	shares fileShareStore,
	audits fileAuditStore,
	stor *storage.Service,
	mailer *mail.Mailer,
	hub *sse.Hub,
	q *quota.Service,
	coord syncpkg.Coordinator,
	baseURL string,
	maxUploadMB int64,
	officeCfg officedoc.Config,
) *FileHandler {
	return &FileHandler{
		files:          files,
		syncRoots:      syncRoots,
		conflicts:      conflicts,
		trash:          trash,
		users:          users,
		shares:         shares,
		audits:         audits,
		storage:        stor,
		mailer:         mailer,
		hub:            hub,
		quota:          q,
		coordinator:    coord,
		baseURL:        baseURL,
		maxUploadBytes: maxUploadMB * 1024 * 1024,
		officeCfg:      officeCfg,
	}
}

const (
	listLimitDefault = 500
	listLimitMax     = 1000
)

// ListFiles returns the direct children under `path` — files plus
// explicit and implicit directories — paginated by keyset on the
// segment name. See ADR-0008.
func (h *FileHandler) ListFiles(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}

	if _, err := h.syncRoots.GetSyncRoot(r.Context(), rootID, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
		return
	}

	prefix := r.URL.Query().Get("path")
	if prefix == "" {
		prefix = "/"
	}
	prefix = path.Clean("/" + strings.TrimLeft(prefix, "/"))

	limit := listLimitDefault
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > listLimitMax {
		limit = listLimitMax
	}
	cursor := r.URL.Query().Get("cursor")

	entries, err := h.files.ListDirectoryLevel(r.Context(), repository.ListDirectoryLevelParams{
		SyncRootID: rootID,
		PathPrefix: prefix,
		Cursor:     cursor,
		Limit:      limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	// Every entry is a row since ADR-0041, so id, mtime and updated_at are
	// always there -- they used to be pointers because an implicit directory
	// had none.
	type entryResponse struct {
		Name         string                 `json:"name"`
		Path         string                 `json:"path"`
		IsDir        bool                   `json:"is_dir"`
		ID           uuid.UUID              `json:"id"`
		Size         int64                  `json:"size"`
		MTime        time.Time              `json:"mtime"`
		ContentHash  string                 `json:"content_hash,omitempty"`
		UpdatedAt    time.Time              `json:"updated_at"`
		LastModifier *userSummaryWithAvatar `json:"last_modifier,omitempty"`
	}

	wrappedPrefix := prefix
	if !strings.HasSuffix(wrappedPrefix, "/") {
		wrappedPrefix += "/"
	}

	modifierIDs := make([]uuid.UUID, 0, len(entries))
	for _, e := range entries {
		if e.LastModifierUserID != nil {
			modifierIDs = append(modifierIDs, *e.LastModifierUserID)
		}
	}
	modifiers, err := resolveLastModifiers(r.Context(), h.users, modifierIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	resp := struct {
		Entries    []entryResponse `json:"entries"`
		NextCursor string          `json:"next_cursor"`
	}{
		Entries: make([]entryResponse, 0, len(entries)),
	}
	for _, e := range entries {
		resp.Entries = append(resp.Entries, entryResponse{
			Name:         strings.TrimPrefix(e.Path, wrappedPrefix),
			Path:         e.Path,
			IsDir:        e.IsDir,
			ID:           e.ID,
			Size:         e.Size,
			MTime:        e.MTime,
			ContentHash:  hex.EncodeToString(e.ContentHash),
			UpdatedAt:    e.UpdatedAt,
			LastModifier: modifiers.lookupOptional(e.LastModifierUserID),
		})
	}
	if len(entries) == limit {
		resp.NextCursor = strings.TrimPrefix(entries[len(entries)-1].Path, wrappedPrefix)
	}
	writeJSON(w, http.StatusOK, resp)
}

// DirectoryStats returns recursive aggregates for a directory in the
// current sync root: direct-child count, all files in the subtree,
// total bytes, newest file mtime.
func (h *FileHandler) DirectoryStats(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}
	if _, err := h.syncRoots.GetSyncRoot(r.Context(), rootID, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
		return
	}

	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		dirPath = "/"
	}
	dirPath = path.Clean("/" + strings.TrimLeft(dirPath, "/"))

	stats, err := h.files.DirectoryStats(r.Context(), rootID, dirPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, struct {
		ItemCount   int        `json:"item_count"`
		FileCount   int        `json:"file_count"`
		TotalSize   int64      `json:"total_size"`
		LatestMTime *time.Time `json:"latest_mtime,omitempty"`
	}{
		ItemCount:   stats.ItemCount,
		FileCount:   stats.FileCount,
		TotalSize:   stats.TotalSize,
		LatestMTime: stats.LatestMTime,
	})
}

// GET /api/v1/sync-roots/{rootID}/changes?since=<seq>&limit=<n>&feed_version=2
//
// Keyset-paginated changes-feed (ADR-0036): since is the seq cursor of the
// last row the caller already has (0 for a first sync), not a timestamp.
// Callers page through by re-issuing the call with next_cursor until the
// response omits it. feed_version=2 is required (ADR-0065): the feed now
// merges in move_events, and an older client would otherwise see a
// directory move as silently missing data.
func (h *FileHandler) GetChanges(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}

	if _, err := h.syncRoots.GetSyncRoot(r.Context(), rootID, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
		return
	}

	if !requireFeedVersion(w, r) {
		return
	}

	var sinceSeq int64
	if s := r.URL.Query().Get("since"); s != "" {
		sinceSeq, err = strconv.ParseInt(s, 10, 64)
		if err != nil || sinceSeq < 0 {
			writeError(w, http.StatusBadRequest, ErrInvalidSince, "invalid since parameter (expects a seq cursor)")
			return
		}
	}

	limit := changesLimitDefault
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > changesLimitMax {
		limit = changesLimitMax
	}

	// Overfetch by one on each source (ADR-0065): lets mergeChangeFeed tell
	// "more past this page" apart from "exactly filled the page" regardless
	// of how the merge splits between the two sources.
	files, err := h.files.GetChangedSince(r.Context(), rootID, sinceSeq, limit+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	moves, err := h.files.ListMoveEventsSince(r.Context(), rootID, sinceSeq, limit+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, buildChangesResponse(files, moves, limit, nil))
}

// filesStatMaxPaths caps a single stat request -- the desktop retry sweep
// batches its due failed_server_changes rows into blocks of this size
// (ADR-0062), so a request this endpoint would refuse can only come from a
// misbehaving or hostile client, never the reference client under normal
// operation.
const filesStatMaxPaths = 500

type fileStatEntry struct {
	Path          string    `json:"path"`
	Found         bool      `json:"found"`
	ID            uuid.UUID `json:"id,omitempty"`
	Size          int64     `json:"size,omitempty"`
	MTime         time.Time `json:"mtime,omitempty"`
	ContentHash   string    `json:"content_hash,omitempty"`
	IsDeleted     bool      `json:"is_deleted,omitempty"`
	IsDir         bool      `json:"is_dir,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
	ServerVersion int64     `json:"server_version,omitempty"`
}

// statEntriesFor turns a batch lookup result into one entry per requested
// path, in the caller's order -- a path FindByPaths did not return becomes
// Found:false rather than being silently dropped, which is the whole point
// of this endpoint (ADR-0062): the caller must be able to tell "server never
// had this" apart from "server had it and deleted it" (Found:true,
// IsDeleted:true), instead of a retry treating a missing entry as "still
// there, unchanged".
func statEntriesFor(paths []string, found []model.File) []fileStatEntry {
	byPath := make(map[string]model.File, len(found))
	for _, f := range found {
		byPath[f.Path] = f
	}
	entries := make([]fileStatEntry, 0, len(paths))
	for _, p := range paths {
		f, ok := byPath[p]
		if !ok {
			entries = append(entries, fileStatEntry{Path: p, Found: false})
			continue
		}
		entries = append(entries, fileStatEntry{
			Path:          p,
			Found:         true,
			ID:            f.ID,
			Size:          f.Size,
			MTime:         f.MTime,
			ContentHash:   hex.EncodeToString(f.ContentHash),
			IsDeleted:     f.IsDeleted,
			IsDir:         f.IsDir,
			UpdatedAt:     f.UpdatedAt,
			ServerVersion: f.ServerVersion,
		})
	}
	return entries
}

// POST /api/v1/sync-roots/{rootID}/files/stat
//
// Batched fresh-state lookup for a list of paths (ADR-0062): backs the
// desktop retry sweep so a re-applied failed_server_changes row acts on
// current server state instead of the stale snapshot it failed with, which
// could otherwise resurrect a file the server has since deleted. POST (not
// GET) because the path list can run to hundreds of entries -- the same
// split the sibling `/files/delta` (GET, one path) vs `/files/delta/apply`
// (POST, JSON body) already draws.
func (h *FileHandler) FilesStat(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rootID, err := uuid.Parse(chi.URLParam(r, "rootID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRootID, "invalid root id")
		return
	}
	if _, err := h.syncRoots.GetSyncRoot(r.Context(), rootID, claims.UserID); err != nil {
		writeError(w, http.StatusNotFound, ErrSyncRootNotFound, "sync root not found")
		return
	}

	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}
	if len(body.Paths) > filesStatMaxPaths {
		writeError(w, http.StatusBadRequest, ErrTooManyPaths,
			fmt.Sprintf("at most %d paths per request", filesStatMaxPaths))
		return
	}

	found, err := h.files.FindByPaths(r.Context(), rootID, body.Paths)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, struct {
		Results []fileStatEntry `json:"results"`
	}{Results: statEntriesFor(body.Paths, found)})
}

// ─── last_modifier embed (ADR-0013) ──────────────────────────────────────────

// userSummaryWithAvatar mirrors api-client UserSummaryWithAvatar.
type userSummaryWithAvatar struct {
	ID        uuid.UUID `json:"id"`
	Username  string    `json:"username"`
	AvatarURL *string   `json:"avatar_url"`
}

type modifierLookup map[uuid.UUID]userSummaryWithAvatar

// resolveLastModifiers batches the users lookup for the embedded
// last_modifier field on file listings. Missing IDs are absent from the
// returned map and render as null on the wire.
func resolveLastModifiers(ctx context.Context, users fileUserStore, ids []uuid.UUID) (modifierLookup, error) {
	unique := dedupUUIDs(ids)
	if len(unique) == 0 {
		return modifierLookup{}, nil
	}
	summaries, err := users.FindSummariesByIDs(ctx, unique)
	if err != nil {
		return nil, err
	}
	out := make(modifierLookup, len(summaries))
	for id, s := range summaries {
		var avatarURL *string
		if s.AvatarPath != nil {
			url := "/api/v1/auth/avatar/" + s.ID.String()
			avatarURL = &url
		}
		out[id] = userSummaryWithAvatar{ID: s.ID, Username: s.Username, AvatarURL: avatarURL}
	}
	return out, nil
}

func (m modifierLookup) lookupOptional(id *uuid.UUID) *userSummaryWithAvatar {
	if id == nil {
		return nil
	}
	if s, ok := m[*id]; ok {
		return &s
	}
	return nil
}

func dedupUUIDs(ids []uuid.UUID) []uuid.UUID {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// contentDispositionAttachment baut einen RFC 6266-konformen
// Content-Disposition-Header. Sonderzeichen (Komma, Semikolon, Umlaute) im
// Dateinamen brachen vorher den Header-Parse im Browser; daher kommen sowohl
// ein gefiltertes ASCII-`filename=` als auch ein UTF-8-`filename*=` raus.
func contentDispositionAttachment(name string) string {
	ascii := strings.Map(func(r rune) rune {
		switch r {
		case '"', '\\', '\r', '\n':
			return '_'
		}
		if r < 0x20 || r > 0x7E {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		ascii, url.PathEscape(name))
}

// sanitizePath normalises a path and blocks path-traversal attacks.
// Uses path.Clean (forward-slash, platform-independent) rather than filepath.Clean
// so that "//foo" is not interpreted as a UNC path on Windows. path.Clean on an
// absolute path fully resolves and discards any ".." without ever escaping
// root (e.g. "/../../etc" -> "/etc"), so the result is inherently safe — no
// separate traversal check needed. A prior substring check for ".." here
// false-positived on legitimate filenames that merely contain it (e.g.
// "Super Mario Bros..nes").
func sanitizePath(p string) (string, error) {
	return path.Clean("/" + strings.TrimLeft(p, "/")), nil
}

func sanitizeName(name string) string {
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	return strings.ToLower(replacer.Replace(strings.TrimSpace(name)))
}
