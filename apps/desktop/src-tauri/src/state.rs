use std::sync::Arc;
use tokio::sync::RwLock;
use serde::Serialize;
use tauri::{AppHandle, Emitter};
use crate::api::ApiClient;
use crate::command_error::{codes, CommandError};
use crate::db::LocalDb;

#[derive(Serialize)]
pub struct SyncStatus {
    pub pending_count:  usize,
    pub conflict_count: usize,
    pub status:         String,  // idle | syncing | conflict | offline | empty | unauthenticated
}

#[derive(Serialize)]
pub struct ClientConfig {
    pub server_url:        String,
    pub username:          String,
    pub is_logged_in:      bool,
    /// Whether the current platform supports on-demand (placeholder) sync.
    pub supports_on_demand: bool,
}

// --- Commands ----------------------------------------------------------------

#[tauri::command]
pub async fn get_sync_status(
    db:  tauri::State<'_, Arc<LocalDb>>,
    api: tauri::State<'_, Arc<RwLock<ApiClient>>>,
) -> Result<SyncStatus, String> {
    let all       = db.list_pending_all().map_err(|e| e.to_string())?;
    let conflicts = all.iter().filter(|f| f.sync_status == "conflict").count();

    // Frontend boots before the sync engine has emitted the first
    // sync_status_changed event, so make sure the initial poll already
    // distinguishes the not-logged-in / no-roots cases. Otherwise the bar
    // would briefly flash "Synchronized" on every app launch.
    let logged_in = api.read().await.is_authenticated();
    let root_count = db.list_sync_roots().map(|v| v.len()).unwrap_or(0);

    let status = if !logged_in           { "unauthenticated" }
                 else if conflicts > 0   { "conflict" }
                 else if !all.is_empty() { "syncing" }
                 else if root_count == 0 { "empty" }
                 else                    { "idle" };

    Ok(SyncStatus {
        pending_count:  all.len(),
        conflict_count: conflicts,
        status:         status.to_string(),
    })
}

#[tauri::command]
pub async fn get_conflicts(
    db: tauri::State<'_, Arc<LocalDb>>,
) -> Result<Vec<crate::db::LocalFile>, String> {
    let all = db.list_pending_all().map_err(|e| e.to_string())?;
    Ok(all.into_iter().filter(|f| f.sync_status == "conflict").collect())
}

#[derive(Serialize)]
pub struct SyncFailure {
    pub kind:          String,           // "upload" | "delete" | "server_change"
    pub root_id:       String,
    pub path:          String,
    pub last_error:    String,
    pub attempt_count: i64,
    pub next_retry_at: Option<i64>,
}

/// Aggregated list of all current sync problems: local pending_failed
/// uploads/deletes plus failed_server_changes. Source for the UI.
#[tauri::command]
pub async fn list_sync_failures(
    db: tauri::State<'_, Arc<LocalDb>>,
) -> Result<Vec<SyncFailure>, String> {
    let mut out: Vec<SyncFailure> = Vec::new();

    let failures = db.list_all_local_failures().map_err(|e| e.to_string())?;
    for f in failures {
        out.push(SyncFailure {
            kind:          "upload".to_string(),
            root_id:       f.sync_root_id,
            path:          f.path,
            last_error:    f.last_error.unwrap_or_default(),
            attempt_count: f.attempt_count,
            next_retry_at: f.next_retry_at,
        });
    }

    let server_failures = db.list_all_failed_server_changes().map_err(|e| e.to_string())?;
    for s in server_failures {
        out.push(SyncFailure {
            kind:          "server_change".to_string(),
            root_id:       s.sync_root_id,
            path:          s.server_path,
            last_error:    s.last_error,
            attempt_count: s.attempt_count,
            next_retry_at: Some(s.next_retry_at),
        });
    }

    Ok(out)
}

/// Forces an immediate retry for a failure entry by setting
/// `next_retry_at = now`; the next sync cycle picks it up as due.
#[tauri::command]
pub async fn retry_sync_failure(
    db:       tauri::State<'_, Arc<LocalDb>>,
    sync_now: tauri::State<'_, Arc<tokio::sync::Notify>>,
    kind:     String,
    root_id:  String,
    path:     String,
) -> Result<(), String> {
    let now = chrono::Utc::now().timestamp();
    match kind.as_str() {
        "upload" | "delete" => {
            db.reset_failed(&root_id, &path).map_err(|e| e.to_string())?;
        }
        "server_change" => {
            // Pull the due time forward to now — the next process_downloads
            // picks up the entry in its retry sweep.
            let conn_mutex = &db.conn;
            let conn = conn_mutex.lock().unwrap();
            conn.execute(
                "UPDATE failed_server_changes SET next_retry_at = ?1
                 WHERE sync_root_id = ?2 AND server_path = ?3",
                rusqlite::params![now, root_id, path],
            ).map_err(|e| e.to_string())?;
        }
        other => return Err(format!("unknown failure kind: {other}")),
    }
    sync_now.notify_one();
    Ok(())
}

/// Text comparison between a conflict's server version (on disk) and its
/// local copy. `kind` is "lines" with the diff, or "binary" when either side
/// is not text. Drives the desktop resolve dialog's diff view (ADR-0003).
#[derive(Serialize)]
#[serde(tag = "kind", rename_all = "lowercase")]
pub enum ConflictDiff {
    Lines { lines: Vec<crate::sync::conflict_resolve::DiffLine> },
    Binary,
}

/// Disk locations of a conflict's two sides: the synced file (server version)
/// and the loser copy (local bytes).
fn conflict_sides(
    db:      &LocalDb,
    root_id: &str,
    path:    &str,
) -> Result<(std::path::PathBuf, std::path::PathBuf), CommandError> {
    let root = db.get_sync_root(root_id).map_err(|e| e.to_string())?
        .ok_or_else(|| CommandError::coded(codes::SYNC_ROOT_NOT_FOUND, "sync root not found"))?;
    let copy = db.conflict_copy_path(root_id, path).map_err(|e| e.to_string())?
        .ok_or_else(|| CommandError::coded(codes::CONFLICT_COPY_MISSING, "conflict copy path missing"))?;
    let original = std::path::Path::new(&root.local_path).join(path.trim_start_matches('/'));
    Ok((original, std::path::PathBuf::from(copy)))
}

/// Size and modification time of both sides, read from disk, so the resolve
/// dialog can show them for every file type (ADR-0003).
#[derive(Serialize)]
pub struct ConflictVersions {
    pub current:       crate::sync::conflict_resolve::VersionFacts,
    pub incoming:      crate::sync::conflict_resolve::VersionFacts,
    /// File name of the loser copy, shown as its path.
    pub incoming_name: String,
}

#[tauri::command]
pub async fn conflict_versions(
    db:      tauri::State<'_, Arc<LocalDb>>,
    root_id: String,
    path:    String,
) -> Result<ConflictVersions, CommandError> {
    use crate::sync::conflict_resolve::version_facts;

    let (original, copy) = conflict_sides(&db, &root_id, &path)?;
    Ok(ConflictVersions {
        current:       version_facts(&original).map_err(|e| e.to_string())?,
        incoming:      version_facts(&copy).map_err(|e| e.to_string())?,
        incoming_name: copy.file_name().map(|n| n.to_string_lossy().into_owned()).unwrap_or_default(),
    })
}

/// Both sides as `data:` URLs for a side-by-side image comparison.
#[derive(Serialize)]
pub struct ConflictPreview {
    pub current:  String,
    pub incoming: String,
}

/// `None` when the file is not a previewable image or either side exceeds the
/// preview size limit — the dialog then shows the facts only.
#[tauri::command]
pub async fn conflict_preview(
    db:      tauri::State<'_, Arc<LocalDb>>,
    root_id: String,
    path:    String,
) -> Result<Option<ConflictPreview>, CommandError> {
    use crate::sync::conflict_resolve::image_data_url;

    let (original, copy) = conflict_sides(&db, &root_id, &path)?;
    // Reads up to 25 MB per side; keep it off the async executor threads.
    let urls = tokio::task::spawn_blocking(move || -> anyhow::Result<Option<ConflictPreview>> {
        let Some(current) = image_data_url(&original, &original)? else { return Ok(None) };
        let Some(incoming) = image_data_url(&copy, &original)? else { return Ok(None) };
        Ok(Some(ConflictPreview { current, incoming }))
    })
    .await
    .map_err(|e| e.to_string())?
    .map_err(|e| e.to_string())?;
    Ok(urls)
}

#[tauri::command]
pub async fn conflict_diff(
    db:      tauri::State<'_, Arc<LocalDb>>,
    root_id: String,
    path:    String,
) -> Result<ConflictDiff, CommandError> {
    use crate::sync::conflict_resolve::{diff_lines, is_probably_text};

    let (original, copy) = conflict_sides(&db, &root_id, &path)?;
    let current_bytes = std::fs::read(&original).map_err(|e| e.to_string())?;
    let conflict_bytes = std::fs::read(&copy).map_err(|e| e.to_string())?;

    if !is_probably_text(&current_bytes) || !is_probably_text(&conflict_bytes) {
        return Ok(ConflictDiff::Binary);
    }
    let lines = diff_lines(
        &String::from_utf8_lossy(&current_bytes),
        &String::from_utf8_lossy(&conflict_bytes),
    );
    Ok(ConflictDiff::Lines { lines })
}

/// Resolve a desktop conflict with an explicit strategy (ADR-0003). The
/// loser copy lives on disk next to the file; the chosen strategy decides
/// what happens to both (see `sync::conflict_resolve`). After the file ops
/// the row is closed and the engine nudged so `keep_conflict`'s re-upload (or
/// `keep_both`'s new file) lands without waiting for the poll tick.
#[tauri::command]
pub async fn resolve_conflict(
    db:       tauri::State<'_, Arc<LocalDb>>,
    sync_now: tauri::State<'_, Arc<tokio::sync::Notify>>,
    root_id:  String,
    path:     String,
    strategy: String,
) -> Result<(), CommandError> {
    use crate::sync::conflict_resolve::{execute_plan, plan_resolve, NewStatus, ResolveStrategy};

    let strategy = ResolveStrategy::parse(&strategy)
        .ok_or_else(|| format!("unknown resolve strategy: {strategy}"))?;

    let root = db.get_sync_root(&root_id).map_err(|e| e.to_string())?
        .ok_or_else(|| CommandError::coded(codes::SYNC_ROOT_NOT_FOUND, "sync root not found"))?;
    let copy = db.conflict_copy_path(&root_id, &path).map_err(|e| e.to_string())?;

    // Conflict rows created before migration 12 carry no copy path. They can
    // only be dismissed (keep_current — the server version is already on
    // disk); the promote strategies have nothing to act on.
    let copy = match copy {
        Some(c) => c,
        None if strategy == ResolveStrategy::KeepCurrent => {
            db.resolve_conflict(&root_id, &path, false).map_err(|e| e.to_string())?;
            sync_now.notify_one();
            return Ok(());
        }
        None => return Err(CommandError::coded(codes::CONFLICT_COPY_MISSING, "conflict copy path missing")),
    };

    let original = std::path::Path::new(&root.local_path).join(path.trim_start_matches('/'));
    let copy_path = std::path::PathBuf::from(&copy);

    let plan = plan_resolve(strategy, &copy_path);
    execute_plan(&plan, &original, &copy_path).map_err(|e| e.to_string())?;

    let pending = plan.status == NewStatus::Pending;
    db.resolve_conflict(&root_id, &path, pending).map_err(|e| e.to_string())?;
    sync_now.notify_one();
    Ok(())
}

#[tauri::command]
pub async fn get_config(
    db:  tauri::State<'_, Arc<LocalDb>>,
    api: tauri::State<'_, Arc<RwLock<ApiClient>>>,
) -> Result<ClientConfig, String> {
    let server_url   = db.config_get("server_url").map_err(|e| e.to_string())?
        .unwrap_or_default();
    let username     = db.config_get("username").map_err(|e| e.to_string())?
        .unwrap_or_default();
    let is_logged_in = api.read().await.is_authenticated();
    Ok(ClientConfig {
        server_url,
        username,
        is_logged_in,
        supports_on_demand: crate::cloud_files::supports_placeholders(),
    })
}

/// Outcome of a password login attempt. When `requires_2fa` is set the
/// access token has deliberately NOT been persisted yet — the frontend must
/// collect a TOTP/backup code and call `verify_totp` with `session_token`
/// before the user is actually authenticated.
#[derive(Serialize)]
pub struct LoginOutcome {
    pub requires_2fa:  bool,
    pub session_token: Option<String>,
}

#[tauri::command]
pub async fn login(
    app:        AppHandle,
    db:         tauri::State<'_, Arc<LocalDb>>,
    api:        tauri::State<'_, Arc<RwLock<ApiClient>>>,
    server_url: String,
    username:   String,
    password:   String,
) -> Result<LoginOutcome, CommandError> {
    // The user enters the public web URL; the API lives under the same origin
    // (`/api/v1`). Normalise so a pasted scheme-less URL or an `/api` suffix
    // still resolves correctly (ADR-0021).
    let url = crate::public_url::normalize_public_url(&server_url);
    if url.is_empty() {
        return Err(CommandError::coded(codes::SERVER_URL_REQUIRED, "server URL must not be empty"));
    }

    {
        let mut client = api.write().await;
        client.base_url = url.clone();
    }

    let ident = crate::client_id::current(&db);
    let (resp, refresh_token) = api.write().await
        .login(&username, &password, Some(&ident))
        .await?;

    // Persist server URL and username (not password) regardless of the 2FA
    // outcome so a pending 2FA step survives an app restart / retry.
    db.config_set("server_url", &url).map_err(|e| e.to_string())?;
    db.config_set("username", &username).map_err(|e| e.to_string())?;

    if resp.requires_2fa {
        return Ok(LoginOutcome { requires_2fa: true, session_token: resp.session_token });
    }

    // Persist access token
    let token = api.read().await.access_token.clone().unwrap_or_default();
    db.config_set("access_token", &token).map_err(|e| e.to_string())?;

    // Persist refresh token (comes from the Set-Cookie header)
    if let Some(rt) = refresh_token {
        db.config_set("refresh_token", &rt).map_err(|e| e.to_string())?;
    }

    crate::updater::start_polling(app, Some(url), true);

    Ok(LoginOutcome { requires_2fa: false, session_token: None })
}

/// Second step of a 2FA-gated login: exchanges `session_token` + a TOTP or
/// backup code for the real access/refresh token pair.
#[tauri::command]
pub async fn verify_totp(
    app:           AppHandle,
    db:            tauri::State<'_, Arc<LocalDb>>,
    api:           tauri::State<'_, Arc<RwLock<ApiClient>>>,
    session_token: String,
    code:          String,
    is_backup:     bool,
) -> Result<(), CommandError> {
    let ident = crate::client_id::current(&db);

    let (totp_code, backup_code) = if is_backup {
        (None, Some(code.as_str()))
    } else {
        (Some(code.as_str()), None)
    };

    let (_resp, refresh_token) = api.write().await
        .verify_2fa(&session_token, totp_code, backup_code, Some(&ident))
        .await?;

    let token = api.read().await.access_token.clone().unwrap_or_default();
    db.config_set("access_token", &token).map_err(|e| e.to_string())?;

    if let Some(rt) = refresh_token {
        db.config_set("refresh_token", &rt).map_err(|e| e.to_string())?;
    }

    // server_url was already persisted by the preceding login() call that
    // put us on the 2FA path.
    let server_url = db.config_get("server_url").ok().flatten();
    crate::updater::start_polling(app, server_url, true);

    Ok(())
}

#[tauri::command]
pub async fn logout(
    app: AppHandle,
    db:  tauri::State<'_, Arc<LocalDb>>,
    api: tauri::State<'_, Arc<RwLock<ApiClient>>>,
) -> Result<(), String> {
    api.write().await.clear_token();
    db.config_set("access_token", "").map_err(|e| e.to_string())?;
    // Reset the tray icon to the PrivaSync logo: the sync engine will
    // emit "unauthenticated" shortly itself, but set it here explicitly so
    // the tray doesn't only switch on the next run() tick.
    let _ = app.emit("sync_status_changed", serde_json::json!({
        "status":         "unauthenticated",
        "pending_count":  0,
        "conflict_count": 0,
    }));
    Ok(())
}

/// Returns the PNG bytes of the logged-in user's avatar (or None if no
/// avatar is set). The frontend turns it into a Blob URL.
#[tauri::command]
pub async fn fetch_my_avatar(
    api: tauri::State<'_, Arc<RwLock<ApiClient>>>,
) -> Result<Option<Vec<u8>>, String> {
    let client = api.read().await;
    if !client.is_authenticated() {
        return Ok(None);
    }
    let me = match client.me().await {
        Ok(m) => m,
        Err(_) => return Ok(None),
    };
    if me.avatar_url.is_none() {
        return Ok(None);
    }
    client.fetch_avatar(&me.id).await.map_err(|e| e.to_string())
}

/// Removes the account from the client: detach all sync roots (local data
/// stays), delete access + refresh token, set tray to unauthenticated.
/// Nothing is deleted server-side — re-login remains possible.
#[tauri::command]
pub async fn wipe_account_local(
    app: AppHandle,
    db:  tauri::State<'_, Arc<LocalDb>>,
    api: tauri::State<'_, Arc<RwLock<ApiClient>>>,
) -> Result<(), String> {
    let roots = db.list_sync_roots().map_err(|e| e.to_string())?;
    for root in roots {
        if let Err(e) = db.remove_sync_root(&root.id) {
            // Best-effort: continue; a single failure must not block.
            tracing::warn!("wipe_account_local: remove_sync_root({}) failed: {}", root.id, e);
        }
    }

    api.write().await.clear_token();
    let _ = db.config_set("access_token", "");
    let _ = db.config_set("refresh_token", "");

    let _ = app.emit("sync_status_changed", serde_json::json!({
        "status":         "unauthenticated",
        "pending_count":  0,
        "conflict_count": 0,
    }));
    Ok(())
}

#[tauri::command]
pub async fn get_upload_concurrency(
    db: tauri::State<'_, Arc<LocalDb>>,
) -> Result<u8, String> {
    let v = db.config_get("upload_concurrency")
        .map_err(|e| e.to_string())?
        .and_then(|s| s.parse::<u8>().ok())
        .unwrap_or(3)
        .clamp(1, 8);
    Ok(v)
}

#[tauri::command]
pub async fn set_upload_concurrency(
    db:    tauri::State<'_, Arc<LocalDb>>,
    value: u8,
) -> Result<(), String> {
    let clamped = value.clamp(1, 8);
    db.config_set("upload_concurrency", &clamped.to_string())
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn list_server_sync_roots(
    app: AppHandle,
    db:  tauri::State<'_, Arc<LocalDb>>,
    api: tauri::State<'_, Arc<RwLock<ApiClient>>>,
) -> Result<Vec<crate::api::ServerSyncRoot>, CommandError> {
    let client = api.inner().clone();
    crate::api::with_auth_retry(
        &client, &db,
        || { let _ = app.emit("session_expired", ()); },
        || {
            let client = client.clone();
            async move { client.read().await.list_sync_roots().await }
        },
    )
    .await
    .map_err(CommandError::from)
}

#[tauri::command]
pub async fn create_server_sync_root(
    api:  tauri::State<'_, Arc<RwLock<ApiClient>>>,
    name: String,
) -> Result<crate::api::ServerSyncRoot, CommandError> {
    Ok(api.read().await.create_sync_root(name.trim()).await?)
}

// ─── Autostart ───────────────────────────────────────────────────────────────

#[tauri::command]
pub async fn get_autostart(app: AppHandle) -> Result<bool, String> {
    use tauri_plugin_autostart::ManagerExt;
    app.autolaunch().is_enabled().map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn set_autostart(app: AppHandle, enabled: bool) -> Result<(), String> {
    use tauri_plugin_autostart::ManagerExt;
    if enabled {
        app.autolaunch().enable().map_err(|e| e.to_string())
    } else {
        app.autolaunch().disable().map_err(|e| e.to_string())
    }
}

// ─── Language ─────────────────────────────────────────────────────────────────

#[tauri::command]
pub async fn get_language(
    db: tauri::State<'_, Arc<LocalDb>>,
) -> Result<Option<String>, String> {
    db.config_get("language").map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn set_language(
    app:  AppHandle,
    db:   tauri::State<'_, Arc<LocalDb>>,
    lang: String,
) -> Result<(), String> {
    db.config_set("language", &lang).map_err(|e| e.to_string())?;
    // Rebuild the tray: the listener in main.rs re-reads the language from the
    // database, so re-emitting the last status refreshes menu and tooltip
    // without waiting for a real status change.
    republish_tray_status(&app, &db);
    Ok(())
}

/// Records the language the webview actually resolved at boot (an explicit
/// choice if there is one, otherwise the OS locale). The tray menu is built in
/// Rust and can only read the database, so without this mirror it never learns
/// about an OS-derived language. Deliberately a different key from `language`:
/// that one is the user's explicit pick and must not be overwritten by a boot.
#[tauri::command]
pub async fn set_effective_language(
    app:  AppHandle,
    db:   tauri::State<'_, Arc<LocalDb>>,
    lang: String,
) -> Result<(), String> {
    let previous = db.config_get("language_effective").ok().flatten().unwrap_or_default();
    if previous == lang {
        return Ok(());
    }
    db.config_set("language_effective", &lang).map_err(|e| e.to_string())?;
    republish_tray_status(&app, &db);
    Ok(())
}

/// Re-emits the last known sync status so the tray listener in `main.rs`
/// re-reads the language and refreshes menu labels plus tooltip, without
/// waiting for the next real status change.
fn republish_tray_status(app: &AppHandle, db: &LocalDb) {
    let last_status = db.config_get("tray_last_status")
        .ok().flatten().unwrap_or_else(|| "idle".to_string());
    let all = db.list_pending_all().unwrap_or_default();
    let conflict_count = all.iter().filter(|f| f.sync_status == "conflict").count();
    let pending_count  = all.len();
    let _ = app.emit("sync_status_changed", serde_json::json!({
        "pending_count":  pending_count,
        "conflict_count": conflict_count,
        "status":         last_status,
    }));
}
