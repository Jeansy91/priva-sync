/**
 * Typed wrappers around Tauri invoke() commands.
 * Mirrors the Rust command signatures in src-tauri/src/.
 */
import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";

// ─── Types ────────────────────────────────────────────────────────────────────

// What happens to the two competing versions on resolve (ADR-0003). Mirrors
// the server/web strategy names and the Rust `ResolveStrategy`.
export type ResolveStrategy = "keep_current" | "keep_conflict" | "keep_both";

export interface DiffLine {
  kind: "same" | "removed" | "added";
  text: string;
}

// Result of comparing a conflict's server version against the local copy.
export type ConflictDiff =
  | { kind: "lines"; lines: DiffLine[] }
  | { kind: "binary" };

// Size and modification time of one side of a conflict, read from disk.
export interface VersionFacts {
  size:        number;
  modified_ms: number | null;
}

export interface ConflictVersions {
  current:       VersionFacts;
  incoming:      VersionFacts;
  incoming_name: string;
}

// Both sides of an image conflict as data URLs.
export interface ConflictPreview {
  current:  string;
  incoming: string;
}

export interface SyncStatus {
  pending_count:  number;
  conflict_count: number;
  status:         "idle" | "syncing" | "conflict" | "offline" | "empty" | "unauthenticated";
}

export interface ClientConfig {
  server_url:         string;
  username:           string;
  is_logged_in:       boolean;
  supports_on_demand: boolean;
}

export interface LoginOutcome {
  requires_2fa:  boolean;
  session_token: string | null;
}

export interface SyncRootEntry {
  id:             string;
  name:           string;
  local_path:     string;
  server_root_id: string;
  /** "on_demand" (placeholder, Windows only) or "eager" (full download) */
  download_mode:  "on_demand" | "eager";
  /** When true the client only downloads — no uploads or deletes. */
  read_only:      boolean;
  /** When true the sync engine skips this root entirely. */
  paused:         boolean;
  /** "owned" or "mount" (ADR-0011). Mounts route to /shared-mounts. */
  kind:           "owned" | "mount";
}

export interface RemoveSyncRootOptions {
  dehydrated_count: number;
  dehydrated_bytes: number;
}

/** One exclusion-pattern line the gitignore engine rejected. */
export interface PatternError {
  line:    number;  // 1-based row number
  pattern: string;
  message: string;
}

/** How many files the current exclusion patterns catch, plus a sample. */
export interface ExcludedPreview {
  total:  number;
  sample: string[];  // "RootName/rel/path", capped
}

export interface ServerSyncRoot {
  id:   string;
  name: string;
  path: string;
}

/** A Shared Mount the recipient could activate but hasn't yet (ADR-0011). */
export interface AvailableMount {
  share_id:       string;
  name:           string;
  path:           string;
  can_write:      boolean;
  owner_username: string;
}

export interface LocalFile {
  sync_root_id: string;
  path:         string;
  size:         number;
  mtime:        number;
  content_hash: string;
  sync_status:  string;
  permissions?: string;
}

// ─── Commands ─────────────────────────────────────────────────────────────────

export const cmd = {
  getSyncStatus:       ()                                          => invoke<SyncStatus>("get_sync_status"),
  getConflicts:        ()                                          => invoke<LocalFile[]>("get_conflicts"),
  getConfig:           ()                                          => invoke<ClientConfig>("get_config"),

  login: (serverUrl: string, username: string, password: string)  => invoke<LoginOutcome>("login", { serverUrl, username, password }),
  verifyTotp: (sessionToken: string, code: string, isBackup: boolean) =>
    invoke<void>("verify_totp", { sessionToken, code, isBackup }),
  logout:              ()                                          => invoke<void>("logout"),
  fetchMyAvatar:       ()                                          => invoke<number[] | null>("fetch_my_avatar"),
  wipeAccountLocal:    ()                                          => invoke<void>("wipe_account_local"),

  listServerSyncRoots: ()                                          => invoke<ServerSyncRoot[]>("list_server_sync_roots"),
  createServerSyncRoot: (name: string)                             => invoke<ServerSyncRoot>("create_server_sync_root", { name }),
  listSyncRoots:       ()                                          => invoke<SyncRootEntry[]>("list_sync_roots"),
  addSyncRoot: (id: string, name: string, localPath: string, serverRootId: string, downloadMode: string) =>
    invoke<void>("add_sync_root", { id, name, localPath, serverRootId, downloadMode }),
  removeSyncRoot:  (id: string)                                    => invoke<void>("remove_sync_root", { id }),

  // Shared Mounts (ADR-0011). activateMount stores id == shareId so the mount
  // routes to /shared-mounts/{id}; removeMount drops it locally only.
  listAvailableMounts: ()                                          => invoke<AvailableMount[]>("list_available_mounts"),
  activateMount: (shareId: string, name: string, localPath: string, canWrite: boolean, ownerUsername: string) =>
    invoke<void>("activate_mount", { shareId, name, localPath, canWrite, ownerUsername }),
  removeMount: (shareId: string)                                   => invoke<void>("remove_mount", { shareId }),
  getRemoveSyncRootOptions: (id: string) =>
    invoke<RemoveSyncRootOptions>("get_remove_sync_root_options", { id }),
  setDownloadMode:     (id: string, mode: string)                  => invoke<void>("set_download_mode", { id, mode }),
  getIgnorePatterns:        ()                  => invoke<string>("get_ignore_patterns"),
  getDefaultIgnorePatterns: ()                  => invoke<string>("get_default_ignore_patterns"),
  validateIgnorePatterns:   (patterns: string)  => invoke<PatternError[]>("validate_ignore_patterns", { patterns }),
  countExcludedFiles:       ()                  => invoke<ExcludedPreview>("count_excluded_files"),
  setIgnorePatterns:        (patterns: string)  => invoke<void>("set_ignore_patterns", { patterns }),
  forceSync:           ()                                          => invoke<void>("force_sync"),
  confirmBulkDelete:   (rootId: string, direction: BulkDeleteDirection) =>
    invoke<void>("confirm_bulk_delete", { rootId, direction }),
  pauseSyncRoot:       (rootId: string)                           => invoke<void>("pause_sync_root", { rootId }),
  resumeSyncRoot:      (rootId: string)                           => invoke<void>("resume_sync_root", { rootId }),
  restoreLocalFiles:   (rootId: string)                           => invoke<void>("restore_local_files", { rootId }),
  resolveConflict:     (rootId: string, path: string, strategy: ResolveStrategy) =>
    invoke<void>("resolve_conflict", { rootId, path, strategy }),
  conflictDiff:        (rootId: string, path: string)             => invoke<ConflictDiff>("conflict_diff", { rootId, path }),
  conflictVersions:    (rootId: string, path: string)             => invoke<ConflictVersions>("conflict_versions", { rootId, path }),
  conflictPreview:     (rootId: string, path: string)             => invoke<ConflictPreview | null>("conflict_preview", { rootId, path }),

  listSyncFailures:    ()                                          => invoke<SyncFailure[]>("list_sync_failures"),
  retrySyncFailure:    (kind: string, rootId: string, path: string) =>
    invoke<void>("retry_sync_failure", { kind, rootId, path }),

  getUploadConcurrency: ()                 => invoke<number>("get_upload_concurrency"),
  setUploadConcurrency: (value: number)    => invoke<void>("set_upload_concurrency", { value }),

  getAutostart:        ()                  => invoke<boolean>("get_autostart"),
  setAutostart:        (enabled: boolean)  => invoke<void>("set_autostart", { enabled }),

  getLanguage:          ()                 => invoke<string | null>("get_language"),
  setLanguage:          (lang: string)     => invoke<void>("set_language", { lang }),
  setEffectiveLanguage: (lang: string)     => invoke<void>("set_effective_language", { lang }),

  checkForUpdate:      (serverUrl?: string) => invoke<UpdateInfo | null>("check_for_update", { serverUrl }),
  installUpdate:       ()                  => invoke<void>("install_update"),
  getUpdateStage:      ()                  => invoke<UpdateStage>("get_update_stage"),
  startAutoUpdateFor:  (serverUrl: string) => invoke<void>("start_auto_update_for", { serverUrl }),
  verifyInstance:      (url: string)       => invoke<boolean>("verify_instance", { url }),
};

export interface UpdateInfo {
  version:      string;
  current:      string;
  notes:        string | null;
  release_date: string | null;
}

/**
 * What the startup auto-update is doing right now. The whole UI stays behind
 * the update gate until this reads "idle" (ADR-0060).
 */
export type UpdateStage =
  | { stage: "idle" }
  | { stage: "checking" }
  | { stage: "downloading"; version: string; downloaded: number; total: number | null }
  | { stage: "installing";  version: string };

// ─── Events ───────────────────────────────────────────────────────────────────

export interface ConflictEvent { path: string; conflict_copy: string; }

/** "outgoing": client-initiated deletes about to push to the server (the
 * original guard). "incoming": server-initiated deletes the client is about
 * to apply locally — a distinct, independently-confirmed guard, see
 * `confirm_bulk_delete`'s doc comment on the Rust side. */
export type BulkDeleteDirection = "incoming" | "outgoing";

export interface BulkDeleteWarning {
  root_id:      string;
  root_name:    string;
  delete_count: number;
  total_count:  number;
  direction:    BulkDeleteDirection;
}

export interface SyncProgressEvent {
  root_id: string;
  current: number;
  total:   number;
  file:    string;
  action:  "upload" | "download" | "delete" | "permissions" | "file_done" | "done";
}

/** The sync_status_changed event now carries the full status object. */
export function onSyncStatusChanged(cb: (status: SyncStatus) => void) {
  return listen<SyncStatus>("sync_status_changed", (e) => cb(e.payload));
}

export function onSyncConflict(cb: (e: ConflictEvent) => void) {
  return listen<ConflictEvent>("sync_conflict", (e) => cb(e.payload));
}

export function onSessionExpired(cb: () => void) {
  return listen("session_expired", () => cb());
}

export function onSyncProgress(cb: (e: SyncProgressEvent) => void) {
  return listen<SyncProgressEvent>("sync_progress", (e) => cb(e.payload));
}

export function onBulkDeleteWarning(cb: (e: BulkDeleteWarning) => void) {
  return listen<BulkDeleteWarning>("sync:bulk_delete_warning", (e) => cb(e.payload));
}

export interface PathWarning {
  root_id:  string;
  path:     string;
  // Machine code the UI translates: "invalid_chars" | "case_collision" | "path_too_long".
  code:     string;
  // Interpolation params for the translated message (e.g. { len, limit } for path_too_long).
  params:   Record<string, number>;
  // false → file was synced anyway with only a warning (e.g. long path); true → skipped.
  blocking: boolean;
}

export function onPathWarning(cb: (e: PathWarning) => void) {
  return listen<PathWarning>("sync:path_warning", (e) => cb(e.payload));
}

export interface RootOrphaned {
  root_id:    string;
  root_name:  string;
  local_path: string;
}

export function onRootOrphaned(cb: (e: RootOrphaned) => void) {
  return listen<RootOrphaned>("sync:root_orphaned", (e) => cb(e.payload));
}

export interface RootRenamed {
  root_id: string;
  name:    string;
}

/** Emitted when an owned root was renamed in the web UI and the pull mirrored
 *  the new name into the local DB. Refresh the list to show the label. */
export function onRootRenamed(cb: (e: RootRenamed) => void) {
  return listen<RootRenamed>("sync:root_renamed", (e) => cb(e.payload));
}

/** Emitted when a share was added/revoked server-side (ADR-0011): refresh the
 *  available-mounts list. The authoritative state is the pull, this is latency. */
export function onSharesChanged(cb: () => void) {
  return listen("shares_changed", () => cb());
}

export interface SyncFailure {
  kind:          "upload" | "delete" | "server_change";
  root_id:       string;
  path:          string;
  last_error:    string;
  attempt_count: number;
  next_retry_at: number | null;
}

export interface SyncFailureEvent {
  kind:  "upload" | "delete" | "server_change";
  path:  string;
  error: string;
}

export function onSyncFailure(cb: (e: SyncFailureEvent) => void) {
  return listen<SyncFailureEvent>("sync_failure", (e) => cb(e.payload));
}

export function onUpdateAvailable(cb: (info: UpdateInfo) => void) {
  return listen<UpdateInfo>("update_available", (e) => cb(e.payload));
}

/** Progress of the startup auto-update — drives the gate screen. */
export function onUpdateStage(cb: (stage: UpdateStage) => void) {
  return listen<UpdateStage>("update_stage", (e) => cb(e.payload));
}

export interface TransferSpeed {
  upload_bps:   number;
  download_bps: number;
}

export function onTransferSpeed(cb: (e: TransferSpeed) => void) {
  return listen<TransferSpeed>("transfer_speed", (e) => cb(e.payload));
}

/** Pages the tray context menu can jump to — mirrors the `tray_navigate` payloads in `main.rs`. */
export type TrayNavigateTarget = "sync" | "conflicts" | "settings";

const TRAY_NAVIGATE_TARGETS: readonly string[] = ["sync", "conflicts", "settings"];

/**
 * Tray context menu asking the UI to jump to a page. The payload crosses the
 * event bus as a plain string, so it is validated here — once — instead of in
 * every consumer.
 */
export function onTrayNavigate(cb: (target: TrayNavigateTarget) => void) {
  return listen<string>("tray_navigate", (e) => {
    if (TRAY_NAVIGATE_TARGETS.includes(e.payload)) {
      cb(e.payload as TrayNavigateTarget);
    }
  });
}
