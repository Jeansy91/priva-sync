//! Daemon side of the Explorer context menu (ADR-0021).
//!
//! [`DaemonBackend`] turns a filesystem path into the menu tree and performs
//! the side effect of each verb, reusing the existing sync/CF building blocks.
//! On Windows [`spawn_pipe_server`] hosts it behind the named pipe the shell
//! extension talks to; the pure protocol + verb logic lives in [`contextmenu`].

use std::collections::HashMap;
use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use tauri::{AppHandle, Emitter, Manager};
use tokio::sync::Notify;
use tracing::{info, warn};

use crate::api::{shares::RootShares, ApiClient, AuthedApi};
use crate::contextmenu::{self, Located, MenuBackend, MenuNode, MenuVerb, PathMenuState, RootRef};
use crate::db::LocalDb;

/// Named pipe the shell extension connects to (per-user, ACL-scoped by Windows).
#[cfg(target_os = "windows")]
const PIPE_NAME: &str = r"\\.\pipe\privasync";

/// How often the background task refreshes each owned root's share snapshot.
const SHARE_REFRESH_INTERVAL: Duration = Duration::from_secs(30);

/// Snapshot the local DB's sync roots in the locator's shape — the one
/// DB↔[`RootRef`] bridge, shared by the pipe backend and the deep-link
/// handler.
pub fn db_root_refs(db: &LocalDb) -> Vec<RootRef> {
    db.list_sync_roots()
        .unwrap_or_default()
        .into_iter()
        .map(|r| {
            let is_mount = r.is_mount();
            RootRef {
                local_path:     r.local_path,
                read_only:      r.read_only,
                is_mount,
                server_root_id: r.server_root_id,
                name:           r.name,
                id:             r.id,
            }
        })
        .collect()
}

/// Daemon-side implementation of the menu contract.
pub struct DaemonBackend {
    db:          Arc<LocalDb>,
    app:         AppHandle,
    sync_now:    Arc<Notify>,
    api:         AuthedApi,
    /// Per-root share snapshot (server_root_id → shares), refreshed off the hot
    /// path by [`spawn_share_refresher`]. Right-clicks only ever read it, so the
    /// Explorer menu never blocks on the network.
    share_cache: Mutex<HashMap<String, RootShares>>,
}

impl DaemonBackend {
    pub fn new(
        db:       Arc<LocalDb>,
        app:      AppHandle,
        sync_now: Arc<Notify>,
        api:      Arc<tokio::sync::RwLock<ApiClient>>,
    ) -> Self {
        let api = {
            let app = app.clone();
            AuthedApi::new(api, db.clone(), move || { app.emit("session_expired", ()).ok(); })
        };
        Self { db, app, sync_now, api, share_cache: Mutex::new(HashMap::new()) }
    }

    /// Share flags (`is_shared`, `has_public_link`) for an entry — a pure read
    /// of the background-refreshed cache. Unknown roots (not fetched yet, or the
    /// server was unreachable) degrade to "not shared" so the menu still works.
    fn share_flags(&self, server_root_id: &str, rel_path: &str) -> (bool, bool) {
        match self.share_cache.lock() {
            Ok(cache) => match cache.get(server_root_id) {
                Some(shares) => crate::api::shares::share_flags(shares, rel_path),
                None => (false, false),
            },
            Err(_) => (false, false),
        }
    }

    /// Fetch every owned root's shares once and update the cache. Per-root
    /// failures keep the previous snapshot rather than clearing it.
    async fn refresh_shares(&self) {
        // Logged out: nothing to ask, and nothing worth a warning per root.
        if !self.api.is_authenticated().await {
            return;
        }
        for root in self.root_refs() {
            if root.is_mount {
                continue;
            }
            let listed = self.api
                .call_client(|c| {
                    let id = &root.server_root_id;
                    async move { c.read().await.list_root_shares(id).await }
                })
                .await;
            match listed {
                Ok(shares) => {
                    if let Ok(mut cache) = self.share_cache.lock() {
                        cache.insert(root.server_root_id.clone(), shares);
                    }
                }
                Err(e) => warn!("share refresh for root {} failed: {e}", root.server_root_id),
            }
        }
    }

    /// Snapshot the sync roots in the locator's shape.
    fn root_refs(&self) -> Vec<RootRef> {
        db_root_refs(&self.db)
    }

    /// Classify a path into the state the menu is gated on.
    fn gather_state(&self, path: &Path) -> PathMenuState {
        let roots = self.root_refs();
        let Some(loc) = contextmenu::locate(&roots, path) else {
            // The context menu is only available inside a registered sync root.
            return PathMenuState::default();
        };
        let root = &roots[loc.root_index];
        let is_dir = path.is_dir();

        // Excluded: matched by the global exclusion patterns (root itself can't be).
        let excluded = !loc.relative_path.is_empty() && {
            let pats = self.db.get_ignore_patterns().unwrap_or_default();
            crate::sync::ignore::IgnoreMatcher::from_patterns(&pats)
                .is_ignored_db_path(&loc.relative_path, is_dir)
        };

        // Conflict: a conflict-copy row exists for this path (ADR-0003/0016).
        let in_conflict = !loc.relative_path.is_empty()
            && self
                .db
                .conflict_copy_path(&root.id, &loc.relative_path)
                .ok()
                .flatten()
                .is_some();

        // Share state: server-side, owner-only. Skip for mounts (recipients
        // cannot re-share, ADR-0012); cached + graceful on error.
        let (is_shared, has_public_link) = if root.is_mount {
            (false, false)
        } else {
            self.share_flags(&root.server_root_id, &loc.relative_path)
        };

        PathMenuState {
            in_sync_root: true,
            is_mount:     root.is_mount,
            read_only:    root.read_only,
            excluded,
            in_conflict,
            // "pinned" here means "fully resident": a non-dehydrated entry can
            // be freed up, a dehydrated one can be kept on device.
            pinned:       !crate::cloud_files::is_dehydrated(path),
            // Share state is server-side. Mounts cannot be re-shared (ADR-0012)
            // so we skip the lookup for them; owned roots get a cached probe.
            is_shared,
            has_public_link,
        }
    }

    /// Open a URL in the user's default browser via a detached process — the
    /// same approach the tray uses, since the shell plugin's `open` blocks
    /// while the webview is hidden.
    fn open_url(&self, url: &str) -> Result<(), String> {
        #[cfg(target_os = "windows")]
        let (cmd, args) = ("cmd", vec!["/C".into(), "start".into(), "".into(), url.to_string()]);
        #[cfg(target_os = "macos")]
        let (cmd, args) = ("open", vec![url.to_string()]);
        #[cfg(all(unix, not(target_os = "macos")))]
        let (cmd, args) = ("xdg-open", vec![url.to_string()]);

        std::process::Command::new(cmd)
            .args(&args)
            .spawn()
            .map(|_| ())
            .map_err(|e| format!("open url: {e}"))
    }

    fn server_url(&self) -> Result<String, String> {
        let raw = self
            .db
            .config_get("server_url")
            .map_err(|e| e.to_string())?
            .unwrap_or_default();
        if raw.is_empty() {
            return Err("no server configured".into());
        }
        Ok(raw.trim_end_matches('/').to_string())
    }

    /// Build the web-UI deep link for the share-family verbs. Sharing lives in
    /// the web app, so these verbs route there instead of duplicating share
    /// logic in the desktop client.
    fn web_url(&self, roots: &[RootRef], loc: &Option<Located>, is_dir: bool) -> Result<String, String> {
        let base = self.server_url()?;
        let Some(loc) = loc else { return Ok(format!("{}/files", base.trim_end_matches('/'))) };
        let root = &roots[loc.root_index];
        // Own roots are addressed by name, mounts by share id (= server_root_id).
        let label = if root.is_mount { &root.server_root_id } else { &root.name };
        Ok(contextmenu::web_files_url(&base, label, root.is_mount, &loc.relative_path, is_dir))
    }

    /// Append a path-anchored exclusion pattern to the global exclusion list.
    fn add_exclusion(&self, loc: &Option<Located>, is_dir: bool) -> Result<(), String> {
        let loc = loc.as_ref().ok_or("path is not in a sync root")?;
        if loc.relative_path.is_empty() {
            return Err("cannot exclude the sync root itself".into());
        }
        let pattern = if is_dir { format!("{}/", loc.relative_path) } else { loc.relative_path.clone() };
        let mut pats = self.db.get_ignore_patterns().map_err(|e| e.to_string())?;
        if pats.lines().any(|l| l.trim() == pattern) {
            return Ok(()); // already excluded — idempotent
        }
        if !pats.is_empty() && !pats.ends_with('\n') {
            pats.push('\n');
        }
        pats.push_str(&pattern);
        pats.push('\n');
        self.db.set_ignore_patterns(&pats).map_err(|e| e.to_string())?;
        self.sync_now.notify_one();
        Ok(())
    }

    /// Remove the path's exclusion pattern, re-including it in sync.
    fn remove_exclusion(&self, loc: &Option<Located>, is_dir: bool) -> Result<(), String> {
        let loc = loc.as_ref().ok_or("path is not in a sync root")?;
        let with_slash = format!("{}/", loc.relative_path);
        let pats = self.db.get_ignore_patterns().map_err(|e| e.to_string())?;
        let kept: Vec<&str> = pats
            .lines()
            .filter(|l| {
                let t = l.trim();
                t != loc.relative_path && t != with_slash
            })
            .collect();
        let _ = is_dir; // both forms are filtered regardless of current kind
        let next = kept.join("\n");
        self.db.set_ignore_patterns(&next).map_err(|e| e.to_string())?;
        self.sync_now.notify_one();
        Ok(())
    }

    /// Show the desktop window and route it to a known view.
    fn navigate(&self, view: &str) -> Result<(), String> {
        if let Some(win) = self.app.get_webview_window("main") {
            let _ = win.show();
            let _ = win.set_focus();
            let _ = win.unminimize();
        }
        self.app.emit("tray_navigate", view).map_err(|e| e.to_string())
    }

    /// Ask the desktop UI to start the add-sync-root flow for this folder.
    fn start_add_root(&self, path: &Path) -> Result<(), String> {
        if let Some(win) = self.app.get_webview_window("main") {
            let _ = win.show();
            let _ = win.set_focus();
            let _ = win.unminimize();
        }
        self.app
            .emit("contextmenu_add_root", path.to_string_lossy().to_string())
            .map_err(|e| e.to_string())
    }
}

impl DaemonBackend {
    /// The user's UI language (primary tag), falling back to English — same
    /// resolution the tray uses. Drives the context-menu label localization.
    fn locale(&self) -> String {
        self.db
            .config_get("language")
            .ok()
            .flatten()
            .map(|l| l.split('-').next().unwrap_or("en").to_string())
            .filter(|l| !l.is_empty())
            .unwrap_or_else(|| "en".to_string())
    }
}

impl MenuBackend for DaemonBackend {
    fn menu_for(&self, path: &Path) -> Vec<MenuNode> {
        let items = contextmenu::items(&self.gather_state(path));
        let lc = self.locale();
        contextmenu::decorate(&items, &|key| rust_i18n::t!(key, locale = lc.as_str()).to_string())
    }

    fn invoke(&self, verb: MenuVerb, path: &Path) -> Result<(), String> {
        let roots = self.root_refs();
        let loc = contextmenu::locate(&roots, path);
        let is_dir = path.is_dir();
        match verb {
            MenuVerb::OpenWeb
            | MenuVerb::ShareWithUser
            | MenuVerb::CreatePublicLink
            | MenuVerb::CopyPublicLink
            | MenuVerb::ManageShares => {
                let url = self.web_url(&roots, &loc, is_dir)?;
                self.open_url(&url)
            }
            MenuVerb::ExcludeFromSync => self.add_exclusion(&loc, is_dir),
            MenuVerb::IncludeInSync   => self.remove_exclusion(&loc, is_dir),
            MenuVerb::KeepOnDevice    => crate::cloud_files::set_resident(path, true).map_err(|e| e.to_string()),
            MenuVerb::FreeUpSpace     => crate::cloud_files::set_resident(path, false).map_err(|e| e.to_string()),
            MenuVerb::ResolveConflict => self.navigate("conflicts"),
            MenuVerb::Synchronize     => self.start_add_root(path),
        }
    }
}

/// Start the background loop that keeps each owned root's share snapshot fresh,
/// so [`DaemonBackend::share_flags`] can answer right-clicks from memory. Runs
/// an immediate pass on startup, then every [`SHARE_REFRESH_INTERVAL`].
pub fn spawn_share_refresher(backend: Arc<DaemonBackend>) {
    tauri::async_runtime::spawn(async move {
        loop {
            backend.refresh_shares().await;
            tokio::time::sleep(SHARE_REFRESH_INTERVAL).await;
        }
    });
}

// ─── Named-pipe transport (Windows only) ───────────────────────────────────────

/// Start the named-pipe server that serves the shell extension. No-op on
/// platforms without the Explorer shell extension (ADR-0021: transport per
/// `cfg`, the cross-platform core is [`DaemonBackend`]).
#[cfg(not(target_os = "windows"))]
pub fn spawn_pipe_server(_backend: Arc<DaemonBackend>) {}

#[cfg(target_os = "windows")]
pub fn spawn_pipe_server(backend: Arc<DaemonBackend>) {
    use tokio::net::windows::named_pipe::ServerOptions;

    tauri::async_runtime::spawn(async move {
        info!("context-menu pipe server listening on {PIPE_NAME}");
        loop {
            let server = match ServerOptions::new().create(PIPE_NAME) {
                Ok(s) => s,
                Err(e) => {
                    warn!("named pipe create failed: {e}; retrying shortly");
                    tokio::time::sleep(std::time::Duration::from_secs(2)).await;
                    continue;
                }
            };
            // Block until a client (the shell extension) connects.
            if let Err(e) = server.connect().await {
                warn!("named pipe connect failed: {e}");
                continue;
            }
            let backend = backend.clone();
            tauri::async_runtime::spawn(async move { serve_connection(server, backend).await });
        }
    });
}

#[cfg(target_os = "windows")]
async fn serve_connection(
    pipe: tokio::net::windows::named_pipe::NamedPipeServer,
    backend: Arc<DaemonBackend>,
) {
    use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};

    let (reader, mut writer) = tokio::io::split(pipe);
    let mut lines = BufReader::new(reader).lines();
    loop {
        let line = match lines.next_line().await {
            Ok(Some(l)) => l,
            Ok(None) => break,           // client closed
            Err(e) => { warn!("pipe read: {e}"); break; }
        };
        if line.trim().is_empty() {
            continue;
        }
        let resp = match serde_json::from_str::<contextmenu::Request>(&line) {
            Ok(req) => {
                let b = backend.clone();
                // CF hydrate/dehydrate (and Command spawns) can block — keep
                // them off the async executor threads.
                tokio::task::spawn_blocking(move || contextmenu::handle(req, b.as_ref()))
                    .await
                    .unwrap_or_else(|e| contextmenu::Response::Error { error: format!("worker panicked: {e}") })
            }
            Err(e) => contextmenu::Response::Error { error: format!("bad request: {e}") },
        };
        let mut bytes = match serde_json::to_vec(&resp) {
            Ok(b) => b,
            Err(e) => { warn!("encode response: {e}"); break; }
        };
        bytes.push(b'\n');
        if let Err(e) = writer.write_all(&bytes).await {
            warn!("pipe write: {e}");
            break;
        }
    }
}
