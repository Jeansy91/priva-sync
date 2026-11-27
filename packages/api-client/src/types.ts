// ─── Auth ─────────────────────────────────────────────────────────────────────

export interface LoginRequest {
  username: string
  password: string
}

export interface LoginResponse {
  access_token?: string
  expires_in?: number
  requires_2fa: boolean
  session_token?: string
}

export interface Verify2FARequest {
  session_token: string
  totp_code?: string
  backup_code?: string
}

export interface TokenResponse {
  access_token: string
  expires_in: number
  requires_2fa: false
}

export interface Me {
  id: string
  username: string
  email: string
  role: 'user' | 'admin'
  totp_enabled: boolean
  quota_bytes: number
  used_bytes: number
  trash_bytes: number
  avatar_url: string | null
  // '' = not explicitly set yet — client keeps its browser/OS detection.
  preferred_language: 'de' | 'en' | ''
  show_file_tree: boolean
  show_file_checkboxes: boolean
  trash_view: 'list' | 'tree'
}

export interface TOTPSetupResponse {
  secret: string
  otpauth_url: string
}

export interface BackupCode {
  id: string
  used_at: string | null
  created_at: string
}

export interface BackupCodesResponse {
  backup_codes: string[]
}

export interface BackupCodesListResponse {
  total: number
  unused: number
  codes: BackupCode[]
}

// `options.publicKey` is the raw WebAuthn JSON options object (go-webauthn
// marshals its CredentialCreation/CredentialAssertion wrapped in a `publicKey`
// member, mirroring the shape navigator.credentials.create()/get() itself
// expects) — apps/web/src/lib/webauthn.ts unwraps it before handing the inner
// object to parseCreationOptionsFromJSON/parseRequestOptionsFromJSON.
export interface PasskeyRegisterBeginResponse {
  options: { publicKey: PublicKeyCredentialCreationOptionsJSON }
  challenge_token: string
}

export interface PasskeyRegisterFinishRequest {
  challenge_token: string
  credential: unknown
  name: string
}

export interface PasskeyCredentialInfo {
  id: string
  name: string
  created_at: string
  last_used_at: string | null
}

export interface PasskeyLoginBeginResponse {
  options: { publicKey: PublicKeyCredentialRequestOptionsJSON }
  challenge_token: string
}

export interface PasskeyLoginFinishRequest {
  challenge_token: string
  credential: unknown
}

// ─── Files ────────────────────────────────────────────────────────────────────

export interface UserSummaryWithAvatar {
  id:         string
  username:   string
  avatar_url: string | null
}

// One row of the giver-side share map: enough info to render a badge
// for a path (icon by type, tooltip with target username). See ADR-0010.
export interface PathShareInfo {
  // For user-shares this is the recipient's grant id; for public the share id.
  id:              string
  // The share aggregate id — addresses the grant edit endpoints (ADR-0015).
  share_id:        string
  share_type:      'public' | 'user'
  target_user_id?: string
  target_username?: string
  can_write:       boolean
  expires_at:      string | null
  // Public dual-link tokens (only on public entries, owner-only endpoint).
  read_token?:     string | null
  write_token?:    string | null
  // Per-link expiry, chosen at link creation (ADR-0015).
  read_expires_at?:  string | null
  write_expires_at?: string | null
}

// Recipient-side projection of a user-share. See ADR-0010.
export interface SharedMount {
  id:         string
  name:       string
  path:       string
  is_file:    boolean
  can_write:  boolean
  expires_at: string | null
  owner:      UserSummaryWithAvatar
}

export interface SyncRoot {
  id: string
  user_id: string
  name: string
  path: string
  created_at: string
  // Always present in fresh /sync-roots responses (server contract since
  // ADR-0009). Optional only for backward compatibility with cached client
  // state and older mocks.
  owner?: UserSummaryWithAvatar
}

export interface FileEntry {
  id: string
  path: string
  size: number
  mtime: string
  content_hash: string
  is_deleted: boolean
  is_dir?: boolean
  updated_at: string
  // ADR-0013: embedded so callers skip a follow-up /users lookup.
  last_modifier?: UserSummaryWithAvatar | null
}

export interface UploadResponse {
  id: string
  path: string
  size: number
  content_hash: string
  updated_at: string
}

// Result of an optimistic-lock text save (web editor). The server answers 200
// with the new server_version, or 409 when the caller's If-Match is stale —
// the latter is a value here, not a thrown error, so the editor can open its
// merge dialog instead of treating it as a failure.
export interface SaveOk {
  status: 'ok'
  version: number
  contentHash: string
}
export interface SaveConflict {
  status: 'conflict'
  serverVersion: number
  serverHash: string
  // Set only on a "real" conflict where the server already materialised a
  // loser copy (no If-Match supplied); absent on a pure version mismatch.
  conflictPath?: string
}
export type SaveResult = SaveOk | SaveConflict

// ─── Conflicts ────────────────────────────────────────────────────────────────

export interface Conflict {
  id: string
  file_id: string
  conflict_path: string
  client_id: string | null
  resolved: boolean
  created_at: string
  resolved_at: string | null
}

// What happens to the two competing versions on resolve (ADR-0003).
export type ResolveStrategy = 'keep_current' | 'keep_conflict' | 'keep_both'

// Both versions of a conflict side by side, for an informed resolve choice.
// Paths are mount-relative when fetched through the shared-mounts endpoint.
export interface ConflictVersions {
  id: string
  file_id: string
  file_path: string
  conflict_path: string
  created_at: string
  current: {
    size: number
    mtime: string
  }
  conflict: {
    size: number
    // The loser copy's own modification time; null for conflicts recorded
    // before the server stored it.
    mtime: string | null
    created_at: string
  }
}

// ─── Admin ────────────────────────────────────────────────────────────────────

export interface AdminUser {
  id: string
  username: string
  email: string
  role: 'user' | 'admin'
  quota_bytes: number
  used_bytes: number
  trash_bytes: number
  totp_enabled: boolean
  is_active: boolean
  is_locked: boolean
  avatar_url: string | null
}

export interface CreateUserRequest {
  username: string
  email: string
  role?: 'user' | 'admin'
}

export interface AuditEvent {
  id:         string
  user_id:    string | null
  event_type: string
  ip_address: string
  user_agent: string
  details:    Record<string, unknown> | null
  created_at: string
}

export interface AuditLogListParams {
  user?:   string
  event?:  string
  from?:   string
  to?:     string
  limit?:  number
  offset?: number
}

export interface AuditLogListResponse {
  events: AuditEvent[]
  count:  number
}

export interface AdminStats {
  version: string
  users: {
    total:  number
    active: number
    admin:  number
    totp:   number
    locked: number
  }
  sync_roots: { total: number }
  files: {
    live:      number
    trash:     number
    conflicts: number
  }
  shares: {
    total:              number
    active:             number
    password_protected: number
  }
  storage: {
    used_bytes:       number
    trash_bytes:      number
    quota_bytes:      number
    disk_free_bytes:  number
    disk_total_bytes: number
  }
  quota_health: {
    warning80:  number
    critical95: number
  }
  activity_24h: {
    logins:        number
    failed_logins: number
  }
  health: {
    outbox_pending: number
    db_size_bytes:  number
    uptime_seconds: number
  }
}

export interface AdminJob {
  id:           string
  label:        string
  status:       '' | 'running' | 'success' | 'error'
  message:      string
  items:        number
  started_at:   string | null
  finished_at:  string | null
  running:      boolean
  schedule:     string
  next_run_at:  string | null
}

export interface AdminClient {
  id:             string
  user_id:        string
  username:       string
  device_id:      string
  name:           string
  platform:       'windows' | 'macos' | 'linux' | 'mobile'
  os_version:     string | null
  client_version: string | null
  last_ip:        string | null
  last_seen:      string | null
  created_at:     string
}

// ─── Shares ───────────────────────────────────────────────────────────────────

export interface Share {
  id:               string
  // For user-shares the affected recipient's grant id — the recipient's mount
  // anchor (ADR-0015). Present on CreateShare/grant responses.
  grant_id?:        string
  sync_root_id:     string | null
  target_user_id?:  string | null
  path:             string
  share_type:       'public' | 'user'
  token:            string | null
  // Public dual-link: a read link always, a write link only for directories.
  read_token?:      string | null
  write_token?:     string | null
  expires_at:       string | null
  // Per-link expiry, chosen at link creation (ADR-0015).
  read_expires_at?:  string | null
  write_expires_at?: string | null
  can_write:        boolean
  created_at:       string
}

// One recipient with effective access to a path, for the detail-panel avatar
// list. avatar_url is null when the user has no avatar. See ADR-0015.
export interface AccessUser {
  id:         string
  username:   string
  avatar_url: string | null
}

export interface ShareAccess {
  writers: AccessUser[]
  readers: AccessUser[]
}

export interface UserSummary {
  id:       string
  username: string
}

export interface PublicShareEntry {
  name:   string
  path:   string
  is_dir: boolean
  size:   number
  mtime:  string
}

export interface PublicShareView {
  share_type: 'public' | 'user'
  path:       string
  is_file:    boolean
  can_write:  boolean
  entries:    PublicShareEntry[]
}

export interface CreateShareRequest {
  path:             string
  share_type:       'public' | 'user'
  target_username?: string
  target_user_id?:  string
  expires_at?:      string
  can_write?:       boolean
}

// Owner-only in-place change of one recipient's grant (ADR-0015). can_write is
// forced false server-side for file shares; expires_at is optional.
export interface UpdateGrantRequest {
  can_write:    boolean
  expires_at?:  string | null
}

// ─── Trash ────────────────────────────────────────────────────────────────────

export interface TrashEntry {
  id: string
  path: string
  size: number
  content_hash: string
  deleted_at: string | null
}

// ─── Office-Vorschau (ADR-0027) ────────────────────────────────────────────────

// Signed, short-lived editor config for the external OnlyOffice-API-compatible
// document server. `config` is passed through verbatim to that server's own
// `DocsAPI.DocEditor` — its shape is defined by the document server, not us.
export interface OfficeConfigResponse {
  document_server_url: string
  config: Record<string, unknown>
}

// ─── Errors ───────────────────────────────────────────────────────────────────

export interface ApiError {
  error: string
  code?: string
}

export class ApiRequestError extends Error {
  constructor(
    public readonly status: number,
    public readonly body: ApiError
  ) {
    super(`API error ${status}: ${body.error}`)
    this.name = 'ApiRequestError'
  }
}

/**
 * A WebDAV credential. The account password is not one (ADR-0040), so this is
 * what a file manager or rclone authenticates with.
 */
export interface AppPasswordInfo {
  id: string
  name: string
  created_at: string
  last_used_at: string | null
}

/**
 * Creation response. `secret` is the only time the plaintext exists outside
 * the client -- the server stores a hash and cannot hand it out again.
 */
export interface AppPasswordCreated {
  id: string
  name: string
  created_at: string
  secret: string
}

export interface AppPasswordListResponse {
  app_passwords: AppPasswordInfo[]
}
