use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use serde::Serialize;
use tauri::{AppHandle, Emitter, Manager};
use tauri_plugin_notification::NotificationExt;
use tauri_plugin_updater::{Update, Updater, UpdaterExt};
use tokio::sync::oneshot;
use url::Url;

use crate::db::LocalDb;

#[derive(Debug, Clone, Serialize)]
pub struct UpdateInfo {
    pub version:      String,
    pub current:      String,
    pub notes:        Option<String>,
    pub release_date: Option<String>,
}

/// What the startup auto-update is currently doing. The frontend gates its
/// whole UI — login screen included — on this: nothing but the update screen
/// is shown until it reads `Idle` (ADR-0060).
///
/// `Idle` is both the pre-update and the post-update state; a client with
/// nothing to install passes through it within a moment of starting.
#[derive(Debug, Clone, Default, Serialize)]
#[serde(tag = "stage", rename_all = "snake_case")]
pub enum UpdateStage {
    #[default]
    Idle,
    Checking,
    Downloading { version: String, downloaded: u64, total: Option<u64> },
    Installing  { version: String },
}

/// Backend-side memory of the last known update. Lives independently of the
/// (dismissable) React banner state so the tray item and the once-per-version
/// OS notification survive a banner dismiss — the frontend only mirrors this
/// via the `update_available` event, it never owns the fact.
#[derive(Default)]
pub struct UpdateTracker {
    pub info:     Mutex<Option<UpdateInfo>>,
    notified:     Mutex<Option<String>>,
    stage:        Mutex<UpdateStage>,
    /// Set while a startup auto-update run is in flight, so a second trigger
    /// from the login screen does not start a parallel download.
    running:      AtomicBool,
}

/// Guards against starting a second concurrent 6h-poll loop if the user logs
/// out and back in within the same running process.
#[derive(Default)]
pub struct PollGuard(AtomicBool);

const POLL_INTERVAL: Duration = Duration::from_secs(6 * 60 * 60);

/// Grace period before the session poll's first network call, so it does not
/// race the rest of app startup. The startup auto-update has no such delay —
/// the UI is waiting on it.
const POLL_GRACE: Duration = Duration::from_secs(2);

/// Build an `Updater` whose endpoint is derived from the server the client is
/// currently paired with. `override_url` takes priority (used while the user
/// is still typing the server URL on the login screen, before anything is
/// persisted); falls back to the persisted `server_url`. `tauri.conf.json`
/// deliberately carries no endpoint (ADR-0087): without a known server there
/// is nothing to check against, so `Ok(None)` is returned (fresh install).
fn build_updater(app: &AppHandle, override_url: Option<String>) -> Result<Option<Updater>, String> {
    let builder = app.updater_builder();

    let server_url = match override_url {
        Some(u) if !u.trim().is_empty() => crate::public_url::normalize_public_url(&u),
        _ => {
            let db = app.state::<Arc<LocalDb>>();
            db.config_get("server_url").ok().flatten().unwrap_or_default()
        }
    };

    if server_url.is_empty() {
        return Ok(None);
    }

    let url = update_endpoint(&server_url)?;

    builder
        .endpoints(vec![url])
        .map_err(|e| e.to_string())?
        .build()
        .map(Some)
        .map_err(|e| e.to_string())
}

/// Update feed URL of a server. The `{{target}}`-style placeholders are
/// expanded by tauri-plugin-updater itself.
fn update_endpoint(server_url: &str) -> Result<Url, String> {
    let base = server_url.trim_end_matches('/');
    let endpoint = format!(
        "{base}/updates/{{{{target}}}}-{{{{arch}}}}/{{{{current_version}}}}"
    );
    Url::parse(&endpoint).map_err(|e| format!("invalid server_url {server_url}: {e}"))
}

/// Whether a newly found `incoming` version should trigger a fresh OS
/// notification, given the last version we already notified about. `None`
/// (nothing notified yet) always yields `true` for the first find.
fn should_notify(prev: &Option<String>, incoming: &str) -> bool {
    prev.as_deref() != Some(incoming)
}

/// Ask the configured endpoint whether a newer version exists. Pure I/O — no
/// tracker, notification or event side effects, so both the user-facing
/// command and the silent startup auto-update can share it.
async fn fetch_update(app: &AppHandle, server_url: Option<String>) -> Result<Option<Update>, String> {
    let Some(updater) = build_updater(app, server_url)? else {
        return Ok(None);
    };
    updater.check().await.map_err(|e| e.to_string())
}

/// Record a found update: fill the shared tracker, optionally fire the
/// "update available" OS notification (at most once per distinct version),
/// re-emit `update_available` (always, so a freshly focused window redisplays
/// the banner even after a previous dismiss) and rebuild the tray menu.
///
/// `notify` is false on the startup path — that one installs right away and
/// posts its own "installing now" notification instead.
fn record_find(app: &AppHandle, tracker: &UpdateTracker, update: &Update, notify: bool) -> UpdateInfo {
    let info = UpdateInfo {
        version:      update.version.clone(),
        current:      app.package_info().version.to_string(),
        notes:        update.body.clone(),
        release_date: update.date.map(|d| d.to_string()),
    };

    *tracker.info.lock().unwrap() = Some(info.clone());

    if notify {
        let fire = {
            let mut notified = tracker.notified.lock().unwrap();
            let fire = should_notify(&notified, &info.version);
            if fire {
                *notified = Some(info.version.clone());
            }
            fire
        };
        if fire {
            notify_user(app, "tray.update.notifyTitle", "tray.update.notifyBody", &info.version);
        }
    }

    let _ = app.emit("update_available", &info);
    let _ = crate::tray::rebuild_menu(app);

    info
}

/// Show an OS notification whose title and body come from the given i18n keys,
/// in the language the tray uses.
fn notify_user(app: &AppHandle, title_key: &str, body_key: &str, version: &str) {
    let lang = app
        .try_state::<Arc<LocalDb>>()
        .map(|db| crate::tray::language(&db))
        .unwrap_or_else(|| "de".to_string());
    let lc = lang.as_str();
    let title = rust_i18n::t!(title_key, locale = lc).into_owned();
    let body  = rust_i18n::t!(body_key, locale = lc, version = version).into_owned();
    let _ = app.notification().builder().title(title).body(body).show();
}

/// Check the configured update endpoint. Returns `Some` when a newer version
/// is available, `None` when the installed version is current.
///
/// The choke point for every user-facing caller: the manual "check now"
/// button, the live server-URL validation on the login screen, and the 6h
/// background poll. The startup path deliberately does not go through here —
/// it installs instead of announcing (see [`start_auto_update`]).
#[tauri::command]
pub async fn check_for_update(
    app:        AppHandle,
    tracker:    tauri::State<'_, Arc<UpdateTracker>>,
    server_url: Option<String>,
) -> Result<Option<UpdateInfo>, String> {
    let update = fetch_update(&app, server_url).await?;
    Ok(update.map(|u| record_find(&app, &tracker, &u, true)))
}

/// Download and install the update. On success the app is restarted; this
/// function therefore does not return normally in the success path on most
/// platforms.
#[tauri::command]
pub async fn install_update(app: AppHandle) -> Result<(), String> {
    let update = fetch_update(&app, None).await?
        .ok_or_else(|| "no update available".to_string())?;

    install(&update).await?;

    app.restart();
}

/// Download and install `update` in place. Shared by the manual install
/// command and the silent startup auto-update; the caller decides whether to
/// restart afterwards.
async fn install(update: &Update) -> Result<(), String> {
    update
        .download_and_install(|_chunk, _total| {}, || {})
        .await
        .map_err(|e| e.to_string())
}

/// Download and install `update`, publishing [`UpdateStage`] as it goes so the
/// gate screen can show a real progress bar instead of a spinner.
///
/// Byte counts are pushed at most once per whole percent (and, when the server
/// sends no `Content-Length`, once per megabyte) — the chunk callback fires far
/// too often to emit an event each time.
async fn install_with_progress(app: &AppHandle, update: &Update, version: &str) -> Result<(), String> {
    let mut downloaded: u64 = 0;
    let mut last_emitted: u64 = 0;

    update
        .download_and_install(
            |chunk, total| {
                downloaded += chunk as u64;
                let step = match total {
                    Some(t) if t > 0 => t / 100,
                    _ => 1024 * 1024,
                };
                if downloaded - last_emitted < step.max(1) && downloaded < total.unwrap_or(u64::MAX) {
                    return;
                }
                last_emitted = downloaded;
                set_stage(app, UpdateStage::Downloading {
                    version: version.to_string(),
                    downloaded,
                    total,
                });
            },
            || set_stage(app, UpdateStage::Installing { version: version.to_string() }),
        )
        .await
        .map_err(|e| e.to_string())
}

/// Update the client to the current version, before anything else happens —
/// before the login screen, before the sync engine. Runs on every app start,
/// logged in or not: the update feed (`GET /updates/…`) is public.
///
/// A find is installed without asking, then the app restarts. Right after
/// start is the one moment where that costs the user nothing — and a client
/// that reaches its login screen on an old protocol version is exactly what
/// this is meant to prevent.
///
/// The endpoint is `server_url` if given (the login screen passes the URL the
/// user just typed), else the persisted one. A fresh install knows no server
/// yet and has nothing to check against — it skips straight to `Idle`, so the
/// login screen with the server field comes up; `SetupPage` calls back in here
/// through [`start_auto_update_for`] once that URL verifies.
///
/// The stage is switched to `Checking` **synchronously**, before the work is
/// spawned: the frontend gates its first render on it, so it must already be
/// set by the time the webview can ask.
///
/// The returned receiver fires when the client is done updating — including
/// the skip and failure paths, so it is always safe to wait on. It never fires
/// on the success path, which restarts the app instead.
pub fn start_auto_update(app: AppHandle, server_url: Option<String>) -> oneshot::Receiver<()> {
    let (done, wait) = oneshot::channel();

    let url = match server_url {
        Some(u) if !u.trim().is_empty() => Some(u),
        _ => {
            let db = app.state::<Arc<LocalDb>>();
            db.config_get("server_url").ok().flatten().filter(|u| !u.trim().is_empty())
        }
    };

    let Some(url) = url else {
        let _ = done.send(());
        return wait;
    };

    // The login screen re-triggers this per verified URL; one run at a time.
    if app.state::<Arc<UpdateTracker>>().running.swap(true, Ordering::SeqCst) {
        let _ = done.send(());
        return wait;
    }

    set_stage(&app, UpdateStage::Checking);
    tauri::async_runtime::spawn(async move {
        auto_update_on_start(&app, Some(url)).await;
        // Reached only when nothing was installed — the success path restarts.
        app.state::<Arc<UpdateTracker>>().running.store(false, Ordering::SeqCst);
        set_stage(&app, UpdateStage::Idle);
        let _ = done.send(());
    });

    wait
}

/// The login screen's entry into [`start_auto_update`]: check the server the
/// user just typed, before they hand it any credentials. Fire-and-forget — the
/// frontend follows the run through the `update_stage` event.
#[tauri::command]
pub fn start_auto_update_for(app: AppHandle, server_url: String) {
    // The receiver only matters to startup, which waits on the run.
    drop(start_auto_update(app, Some(server_url)));
}

/// Publish the current startup-update stage: store it for late askers
/// ([`get_update_stage`]) and push it to an already-running frontend.
fn set_stage(app: &AppHandle, stage: UpdateStage) {
    let tracker = app.state::<Arc<UpdateTracker>>();
    *tracker.stage.lock().unwrap() = stage.clone();
    let _ = app.emit("update_stage", stage);
}

/// The stage as of right now. The frontend calls this once on mount, because
/// the first `update_stage` events fire before it can listen.
#[tauri::command]
pub fn get_update_stage(tracker: tauri::State<'_, Arc<UpdateTracker>>) -> UpdateStage {
    tracker.stage.lock().unwrap().clone()
}

/// Start the recurring update check for a session that has just become
/// authenticated (fresh login or 2FA completion).
///
/// The recurring check every [`POLL_INTERVAL`] deliberately only announces —
/// a session that has been running for hours must not restart under the
/// user's hands. Only [`start_auto_update`] installs.
///
/// `immediate` runs one announcing check up front, against
/// `initial_server_url` (falls back to the persisted `server_url` if `None`).
/// It is false on the auto-login path at startup, where
/// [`start_auto_update`] has just checked the same endpoint anyway.
///
/// The loop is started at most once per process — logging out and back in
/// within the same running app re-runs the immediate check but does not spawn
/// a second parallel loop.
pub fn start_polling(app: AppHandle, initial_server_url: Option<String>, immediate: bool) {
    let app_for_loop = app.clone();
    tauri::async_runtime::spawn(async move {
        tokio::time::sleep(POLL_GRACE).await;

        if immediate {
            let tracker = app_for_loop.state::<Arc<UpdateTracker>>();
            if let Err(e) = check_for_update(app_for_loop.clone(), tracker, initial_server_url).await {
                tracing::warn!(error = %e, "initial update check failed");
            }
        }

        let guard = app_for_loop.state::<Arc<PollGuard>>();
        if guard.0.swap(true, Ordering::SeqCst) {
            // A recurring loop is already running from an earlier login in
            // this same process — the immediate check above already ran,
            // nothing more to do here.
            return;
        }

        let mut interval = tokio::time::interval(POLL_INTERVAL);
        interval.tick().await; // consume the immediate first tick
        loop {
            interval.tick().await;
            let tracker = app_for_loop.state::<Arc<UpdateTracker>>();
            if let Err(e) = check_for_update(app_for_loop.clone(), tracker, None).await {
                tracing::warn!(error = %e, "periodic update check failed");
            }
        }
    });
}

/// Check, and on a find install right away and restart.
///
/// Every failure is non-fatal — a client that cannot reach its server or
/// cannot write the installer keeps running on the old version, with the
/// banner and tray entry already in place from [`record_find`] so the user
/// can retry manually once logged in. The caller returns the stage to `Idle`,
/// which releases the frontend into the normal UI.
async fn auto_update_on_start(app: &AppHandle, server_url: Option<String>) {
    let update = match fetch_update(app, server_url).await {
        Ok(Some(u)) => u,
        Ok(None) => return,
        Err(e) => {
            tracing::warn!(error = %e, "startup update check failed");
            return;
        }
    };

    let info = {
        let tracker = app.state::<Arc<UpdateTracker>>();
        record_find(app, &tracker, &update, false)
    };
    notify_user(app, "tray.update.autoTitle", "tray.update.autoBody", &info.version);

    match install_with_progress(app, &update, &info.version).await {
        Ok(()) => {
            tracing::info!(version = %info.version, "auto-update installed, restarting");
            app.restart();
        }
        Err(e) => tracing::warn!(error = %e, version = %info.version, "auto-update install failed"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn first_find_always_notifies() {
        assert!(should_notify(&None, "1.2.0"));
    }

    #[test]
    fn same_version_does_not_notify_again() {
        assert!(!should_notify(&Some("1.2.0".to_string()), "1.2.0"));
    }

    #[test]
    fn new_version_notifies_again() {
        assert!(should_notify(&Some("1.2.0".to_string()), "1.3.0"));
    }

    #[test]
    fn endpoint_is_derived_from_server_url() {
        let url = update_endpoint("https://sync.example.org/").unwrap();
        assert_eq!(url.host_str(), Some("sync.example.org"));
        assert!(url.path().starts_with("/updates/"));
        assert!(url.as_str().contains("current_version"));
    }
}
