#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod api;
mod app_id;
mod client_id;
mod cloud_files;
mod command_error;
mod contextmenu;
mod contextmenu_pipe;
mod db;
mod deeplink;
mod identity;
mod instance_check;
mod logging;
mod public_url;
mod sse;
mod state;
mod sync;
mod tray;
mod tray_menu;
mod updater;
mod watcher;

use std::sync::{Arc, Mutex};
use tauri::{
    menu::{Menu, MenuItem},
    tray::TrayIconBuilder,
    Emitter, Listener, Manager, WindowEvent,
};
use tokio::sync::RwLock;

use api::{ApiClient, TransferStats};
use db::LocalDb;
use sync::SyncEngine;

rust_i18n::i18n!("locales", fallback = "en");

fn main() {
    logging::init();

    tauri::Builder::default()
        // Single instance must be the first registered plugin so a second
        // launch (e.g. the OS starting us for a privasync:// URL while we
        // already run) hands its argv to this instance and exits. The
        // "deep-link" feature forwards any URL in argv to the deep-link
        // plugin's on_open_url handler automatically. Deep-link relaunches
        // exist only to hand over the URL — showing the main window would
        // steal focus from the Explorer window the reveal is about to open,
        // so only a plain double-click relaunch brings the window up.
        .plugin(tauri_plugin_single_instance::init(|app, argv, _cwd| {
            if deeplink::has_reveal_url(&argv) {
                return;
            }
            if let Some(win) = app.get_webview_window("main") {
                let _ = win.show();
                let _ = win.set_focus();
            }
        }))
        .plugin(tauri_plugin_deep_link::init())
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        // Autostart: writes HKCU\Software\Microsoft\Windows\CurrentVersion\Run
        // on Windows / a LaunchAgent on macOS / a .desktop file on Linux when
        // toggled on via SettingsTab. No args — we want the app to come up the
        // same way it would from a manual launch (the close-to-tray handler
        // above takes care of starting hidden if the user doesn't open it).
        .plugin(tauri_plugin_autostart::init(
            tauri_plugin_autostart::MacosLauncher::LaunchAgent,
            None,
        ))
        // Intercept window close: minimize to tray instead of quitting.
        // The app exits only via the tray "Quit" entry.
        .on_window_event(|window, event| {
            if window.label() == "main" {
                if let WindowEvent::CloseRequested { api, .. } = event {
                    api.prevent_close();
                    let _ = window.hide();
                }
            }
        })
        .setup(|app| {
            // --- Persistent state ---------------------------------------------
            let db = Arc::new(
                LocalDb::open(app.path().app_data_dir()?.join("privasync.db"))
                    .expect("failed to open local database"),
            );
            let retained_snapshot_ids = match db.chunked_upload_snapshot_ids() {
                Ok(ids) => Some(ids),
                Err(error) => {
                    tracing::warn!(
                        "cannot read resumable upload snapshots; skipping startup cleanup: {error:#}"
                    );
                    None
                }
            };
            let snapshot_store = sync::snapshot_store::SnapshotStore::new(
                app.path().app_cache_dir()?.join("upload-snapshots"),
                retained_snapshot_ids,
            )?;
            // Before the engine starts, so its first cycle already sees the
            // unparked rows as due.
            match db.unpark_failures_on_version_change(
                env!("CARGO_PKG_VERSION"), chrono::Utc::now().timestamp(),
            ) {
                Ok(0)  => {}
                Ok(n)  => tracing::info!("new client version: {n} parked sync failure(s) due again"),
                Err(e) => tracing::warn!("unparking sync failures after update failed: {e:#}"),
            }

            // Restore API client from stored credentials
            let server_url = db.config_get("server_url")
                .ok().flatten().unwrap_or_default();
            let token = db.config_get("access_token")
                .ok().flatten().unwrap_or_default();

            let refresh_token = db.config_get("refresh_token")
                .ok().flatten().unwrap_or_default();

            let stats = Arc::new(TransferStats::default());

            let mut client = ApiClient::new(
                if server_url.is_empty() {
                    "http://localhost:8080".to_string()
                } else {
                    server_url
                },
                stats.clone(),
            );
            let is_logged_in = !token.is_empty();
            if is_logged_in {
                client.set_token(token);
            }

            let api = Arc::new(RwLock::new(client));

            // Sampler: derive throughput from the cumulative byte counters
            // once a second and emit it to the frontend so the bottom status
            // bar can show live up/down speeds. Cheap relaxed atomics, no
            // allocation in steady state when both rates are zero.
            {
                let stats_sampler = stats.clone();
                let app_sampler   = app.handle().clone();
                tauri::async_runtime::spawn(async move {
                    tracing::info!("transfer_speed sampler started");
                    let mut prev = stats_sampler.snapshot();
                    let mut interval = tokio::time::interval(std::time::Duration::from_secs(1));
                    interval.tick().await; // consume immediate first tick
                    loop {
                        interval.tick().await;
                        let now = stats_sampler.snapshot();
                        let up_bps   = now.0.saturating_sub(prev.0);
                        let down_bps = now.1.saturating_sub(prev.1);
                        prev = now;
                        if up_bps > 0 || down_bps > 0 {
                            tracing::debug!(
                                "transfer_speed: ↑{up_bps}B/s ↓{down_bps}B/s (cum ↑{} ↓{})",
                                now.0, now.1,
                            );
                        }
                        if let Err(e) = app_sampler.emit("transfer_speed", serde_json::json!({
                            "upload_bps":   up_bps,
                            "download_bps": down_bps,
                        })) {
                            tracing::warn!("transfer_speed emit failed: {e}");
                        }
                    }
                });
            }

            // On startup: silently refresh the token in the background.
            if !refresh_token.is_empty() {
                let api_startup   = api.clone();
                let db_startup    = db.clone();
                let rt_startup    = refresh_token.clone();
                let ident_startup = crate::client_id::current(&db);
                tauri::async_runtime::spawn(async move {
                    let mut client = api_startup.write().await;
                    if let Ok(new_rt) = client.refresh_token(Some(&rt_startup), Some(&ident_startup)).await {
                        let new_token = client.access_token.clone().unwrap_or_default();
                        let _ = db_startup.config_set("access_token", &new_token);
                        if let Some(rt) = new_rt {
                            let _ = db_startup.config_set("refresh_token", &rt);
                        }
                    }
                });
            }

            let sync_notify: Arc<tokio::sync::Notify> = Arc::new(tokio::sync::Notify::new());
            // Set by a user-initiated "Sync Now" right before sync_notify, so
            // the engine can tell that apart from a routine watcher-event
            // nudge and run a full local walk instead of an Incremental
            // cycle (ADR-0037) -- see SyncEngine::force_walk.
            let force_walk: Arc<std::sync::atomic::AtomicBool> =
                Arc::new(std::sync::atomic::AtomicBool::new(false));

            app.manage(db.clone());
            app.manage(api.clone());
            app.manage(sync_notify.clone());
            app.manage(force_walk.clone());
            app.manage(Arc::new(updater::UpdateTracker::default()));
            app.manage(Arc::new(updater::PollGuard::default()));
            // Holds the tray menu handles between rebuilds so a status change
            // patches the existing menu instead of building a new one.
            tray::install(app);

            // --- privasync:// deep link ---------------------------------
            // "Lokal anzeigen" from the web UI. The bundler registers the scheme
            // at install time; register_all also points the HKCU/.desktop
            // entry at the current executable so dev builds work too.
            let mut cold_start_is_reveal = false;
            {
                use tauri_plugin_deep_link::DeepLinkExt;
                #[cfg(any(target_os = "windows", target_os = "linux"))]
                {
                    if let Err(e) = app.deep_link().register_all() {
                        tracing::warn!("deep link runtime registration failed: {e}");
                    }
                }
                let db_deeplink = db.clone();
                app.deep_link().on_open_url(move |event| {
                    for url in event.urls() {
                        deeplink::handle_url(&db_deeplink, url.as_str());
                    }
                });
                // Cold start via URL: the triggering URL is in argv, not in a
                // later on_open_url event — consume it now.
                if let Ok(Some(urls)) = app.deep_link().get_current() {
                    cold_start_is_reveal = !urls.is_empty();
                    for url in urls {
                        deeplink::handle_url(&db, url.as_str());
                    }
                }
            }

            // Not logged in: open the login window right away instead of
            // leaving the app hidden in the tray, unless this launch only
            // exists to hand over a reveal URL (see single-instance handler
            // above — that case must stay silent).
            if !is_logged_in && !cold_start_is_reveal {
                if let Some(win) = app.get_webview_window("main") {
                    let _ = win.show();
                    let _ = win.set_focus();
                }
            }

            // --- Explorer context-menu pipe server (ADR-0021) -----------------
            // Serves the shell extension: classifies paths into menu trees and
            // performs verb side effects. No-op transport on non-Windows.
            {
                let backend = Arc::new(contextmenu_pipe::DaemonBackend::new(
                    db.clone(),
                    app.handle().clone(),
                    sync_notify.clone(),
                    api.clone(),
                ));
                contextmenu_pipe::spawn_share_refresher(backend.clone());
                contextmenu_pipe::spawn_pipe_server(backend);
            }

            // --- System Tray --------------------------------------------------
            // Initial placeholder menu — replaced below via rebuild_tray_menu
            // with the real DB state as soon as the tray is built.
            let placeholder_status = MenuItem::with_id(app, "status", "PrivaSync", false, None::<&str>)?;
            let placeholder_quit   = MenuItem::with_id(app, "quit",   "Beenden",   true,  None::<&str>)?;
            let menu = Menu::with_items(app, &[&placeholder_status, &placeholder_quit])?;

            let mut tray_builder = TrayIconBuilder::with_id("main")
                .menu(&menu)
                .show_menu_on_left_click(false)
                .tooltip("PrivaSync")
                .on_menu_event(|app, event| {
                    let id = event.id.as_ref();
                    match id {
                        "quit" => app.exit(0),
                        "show" => {
                            if let Some(win) = app.get_webview_window("main") {
                                let _ = win.show();
                                let _ = win.set_focus();
                                let _ = win.unminimize();
                            }
                        }
                        "sync_now" => {
                            if let Some(force_walk) = app.try_state::<Arc<std::sync::atomic::AtomicBool>>() {
                                force_walk.store(true, std::sync::atomic::Ordering::Relaxed);
                            }
                            if let Some(notify) = app.try_state::<Arc<tokio::sync::Notify>>() {
                                notify.notify_one();
                            }
                        }
                        "conflicts" => {
                            if let Some(win) = app.get_webview_window("main") {
                                let _ = win.show();
                                let _ = win.set_focus();
                                let _ = win.unminimize();
                            }
                            let _ = app.emit("tray_navigate", "conflicts");
                        }
                        "settings" => {
                            if let Some(win) = app.get_webview_window("main") {
                                let _ = win.show();
                                let _ = win.set_focus();
                                let _ = win.unminimize();
                            }
                            let _ = app.emit("tray_navigate", "settings");
                        }
                        "update_available" => {
                            if let Some(win) = app.get_webview_window("main") {
                                let _ = win.show();
                                let _ = win.set_focus();
                                let _ = win.unminimize();
                            }
                            // Re-emit so the banner reappears even if it was
                            // dismissed earlier — the tracker outlives that.
                            if let Some(tracker) = app.try_state::<Arc<updater::UpdateTracker>>() {
                                if let Some(info) = tracker.info.lock().unwrap().clone() {
                                    let _ = app.emit("update_available", info);
                                }
                            }
                        }
                        _ if id.starts_with("open_root_") => {
                            let root_id = &id["open_root_".len()..];
                            if let Some(db) = app.try_state::<Arc<LocalDb>>() {
                                if let Ok(Some(root)) = db.get_sync_root(root_id) {
                                    let (program, args) = deeplink::explorer_invocation(&root.local_path);
                                    if let Err(e) = std::process::Command::new(program)
                                        .args(&args)
                                        .spawn()
                                    {
                                        tracing::warn!("tray open_root spawn failed: {e}");
                                    }
                                }
                            }
                        }
                        _ => {}
                    }
                })
                .on_tray_icon_event(|tray, event| {
                    if let tauri::tray::TrayIconEvent::Click {
                        button: tauri::tray::MouseButton::Left,
                        button_state: tauri::tray::MouseButtonState::Up,
                        ..
                    } = event
                    {
                        let app = tray.app_handle();
                        if let Some(win) = app.get_webview_window("main") {
                            let _ = win.show();
                            let _ = win.set_focus();
                        }
                    }
                });

            // Use tray icons if available — start with the neutral logo until
            // the sync engine emits a real status (no roots / not logged in
            // both render as the logo, so this matches steady state too).
            let icon_bytes = include_bytes!("../icons/tray-logo.png");
            if let Ok(icon) = tauri::image::Image::from_bytes(icon_bytes) {
                tray_builder = tray_builder.icon(icon);
            } else if let Some(icon) = app.default_window_icon() {
                tray_builder = tray_builder.icon(icon.clone());
            }

            tray_builder.build(app)?;

            // Now set the real menu with the current DB state.
            let _ = tray::rebuild_menu(app.handle());

            // --- Sync status -> tray label + icon -----------------------------
            let icon_idle_bytes:     &'static [u8] = include_bytes!("../icons/tray-icon.png");
            let icon_logo_bytes:     &'static [u8] = include_bytes!("../icons/tray-logo.png");
            let icon_syncing_bytes:  &'static [u8] = include_bytes!("../icons/tray-syncing.png");
            let icon_conflict_bytes: &'static [u8] = include_bytes!("../icons/tray-conflict.png");
            let icon_offline_bytes:  &'static [u8] = include_bytes!("../icons/tray-offline.png");

            let app_handle = app.handle().clone();
            // Tray icon and tooltip depend on nothing but the status string and
            // the UI language, and the engine re-sends the same status every few
            // seconds while idle, offline or signed out. Skipping the identical
            // repeats saves a PNG decode and a SQLite write per tick. The
            // language belongs in the key because `set_language` refreshes the
            // tooltip by re-emitting the *unchanged* status. `None` means "not
            // seen yet", so the first event of a session always applies.
            let last_render: Mutex<Option<(String, String)>> = Mutex::new(None);
            app.listen("sync_status_changed", move |event: tauri::Event| {
                let status = serde_json::from_str::<serde_json::Value>(event.payload())
                    .ok()
                    .and_then(|v| v.get("status").and_then(|s| s.as_str()).map(|s| s.to_string()))
                    .unwrap_or_else(|| event.payload().trim_matches('"').to_string());
                let lang = app_handle
                    .try_state::<Arc<LocalDb>>()
                    .map(|db| tray::language(&db))
                    .unwrap_or_else(|| "en".to_string());

                let changed = {
                    let key = (status.clone(), lang.clone());
                    let mut last = last_render.lock().unwrap();
                    let changed = last.as_ref() != Some(&key);
                    if changed {
                        *last = Some(key);
                    }
                    changed
                };

                if changed {
                    let bytes = match status.as_str() {
                        "syncing"         => icon_syncing_bytes,
                        "conflict"        => icon_conflict_bytes,
                        "offline"         => icon_offline_bytes,
                        "unauthenticated" => icon_logo_bytes,
                        "empty"           => icon_logo_bytes,
                        _                 => icon_idle_bytes,
                    };
                    // Persist the last status so set_language can refresh the
                    // tooltip without a new sync_status_changed event.
                    let _ = app_handle
                        .try_state::<Arc<LocalDb>>()
                        .map(|db| db.config_set("tray_last_status", status.as_str()));
                    if let Some(tray) = app_handle.tray_by_id("main") {
                        let _ = tray.set_tooltip(Some(tray::tooltip(status.as_str(), &lang)));
                        if let Ok(image) = tauri::image::Image::from_bytes(bytes) {
                            let _ = tray.set_icon(Some(image));
                        }
                    }
                }

                // Always: the status label and conflict counter track the
                // pending-file count, which moves without the status string
                // changing. Cheap now — this patches the existing menu
                // unless the language or the sync-root set changed.
                let _ = tray::rebuild_menu(&app_handle);
            });

            // --- SSE subscriber (server push for conflict_added etc.) --------
            sse::spawn_subscription(app.handle().clone(), api.clone());

            // --- Auto update ---------------------------------------------------
            // Runs before the login screen and independently of it: the update
            // feed is public, so a client updates itself even if nobody ever
            // signs in on it (ADR-0060).
            let update_done = updater::start_auto_update(app.handle().clone(), None);

            // --- Sync engine --------------------------------------------------
            // Waits for the update question to be settled: syncing against a
            // server the client may be about to be replaced for is wasted work,
            // and the engine's first walk would compete with the download. The
            // signal also arrives when there is nothing to update and when the
            // check fails, so this never deadlocks the engine.
            let engine = SyncEngine::new(
                app.handle().clone(),
                db,
                api,
                snapshot_store,
                sync_notify,
                force_walk,
            );
            tauri::async_runtime::spawn(async move {
                let _ = update_done.await;
                engine.run().await;
            });

            // The recurring announce-poll needs a session. A fresh login
            // (state::login / verify_totp) starts it itself once it persists a
            // token — this branch only covers auto-login at startup with an
            // already-valid token, where the check above just ran.
            if is_logged_in {
                updater::start_polling(app.handle().clone(), None, false);
            }

            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            // Status
            state::get_sync_status,
            state::get_conflicts,
            state::resolve_conflict,
            state::conflict_diff,
            state::conflict_versions,
            state::conflict_preview,
            state::list_sync_failures,
            state::retry_sync_failure,
            state::get_config,
            // Auth
            state::login,
            state::verify_totp,
            state::logout,
            state::fetch_my_avatar,
            state::wipe_account_local,
            // Settings
            state::get_upload_concurrency,
            state::set_upload_concurrency,
            state::get_autostart,
            state::set_autostart,
            state::get_language,
            state::set_language,
            state::set_effective_language,
            // Sync roots
            state::list_server_sync_roots,
            state::create_server_sync_root,
            sync::commands::add_sync_root,
            sync::commands::remove_sync_root,
            sync::commands::get_remove_sync_root_options,
            sync::commands::list_sync_roots,
            sync::commands::force_sync,
            sync::commands::set_download_mode,
            sync::commands::get_ignore_patterns,
            sync::commands::get_default_ignore_patterns,
            sync::commands::validate_ignore_patterns,
            sync::commands::count_excluded_files,
            sync::commands::set_ignore_patterns,
            sync::commands::confirm_bulk_delete,
            sync::commands::pause_sync_root,
            sync::commands::resume_sync_root,
            sync::commands::restore_local_files,
            sync::mount_commands::list_available_mounts,
            sync::mount_commands::activate_mount,
            sync::mount_commands::remove_mount,
            // Updater
            updater::check_for_update,
            updater::install_update,
            updater::get_update_stage,
            updater::start_auto_update_for,
            instance_check::verify_instance,
        ])
        .run(tauri::generate_context!())
        .expect("error while running privasync desktop");
}
