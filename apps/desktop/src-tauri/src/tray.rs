//! Tray icon menu: builds it, keeps it in step with the sync state, and
//! renders every label in the persisted UI language.
//!
//! The pure grouping/sorting rules for the "Ordner oeffnen" submenu live in
//! [`crate::tray_menu`] so they stay testable without a running app; this
//! module owns everything that needs a Tauri handle.

use std::sync::{Arc, Mutex};
use tauri::{
    image::Image,
    menu::{IconMenuItem, IsMenuItem, Menu, MenuItem, PredefinedMenuItem, Submenu},
    AppHandle, Manager, Wry,
};

use crate::db::{LocalDb, SyncRootEntry};
use crate::{tray_menu, updater};

/// Reads the UI language from LocalDb. `language` is the explicit user choice
/// from the settings picker; `language_effective` is what the webview actually
/// resolved at boot (OS locale when nothing was ever picked) and is rewritten
/// on every start. Without the second key the tray stayed English for every
/// user who never opened the picker, because the OS-locale fallback lives in
/// the frontend and never reached the database. Region tags ("de-DE") are
/// reduced to their primary tag.
pub(crate) fn language(db: &LocalDb) -> String {
    let raw = ["language", "language_effective"]
        .into_iter()
        .find_map(|key| {
            db.config_get(key).ok().flatten().filter(|s| !s.trim().is_empty())
        })
        .unwrap_or_else(|| "en".to_string());
    raw.split('-').next().unwrap_or("en").to_string()
}

/// Translates the tooltip label for a sync status into the current UI
/// language. Unknown status strings fall back to `tooltip.ready`.
pub(crate) fn tooltip(status: &str, lang: &str) -> String {
    match status {
        "syncing"         => rust_i18n::t!("tray.tooltip.syncing",         locale = lang).into_owned(),
        "conflict"        => rust_i18n::t!("tray.tooltip.conflict",        locale = lang).into_owned(),
        "offline"         => rust_i18n::t!("tray.tooltip.offline",         locale = lang).into_owned(),
        "unauthenticated" => rust_i18n::t!("tray.tooltip.unauthenticated", locale = lang).into_owned(),
        "empty"           => rust_i18n::t!("tray.tooltip.empty",           locale = lang).into_owned(),
        _                 => rust_i18n::t!("tray.tooltip.ready",           locale = lang).into_owned(),
    }
}

/// Handles retained from the last full tray-menu build so a mere status change
/// can be patched into the existing menu instead of building a new one.
///
/// Rebuilding is not free on Windows: muda creates a fresh `HBITMAP` for every
/// `IconMenuItem` it appends and never destroys it — `DestroyMenu` does not
/// touch `hbmpItem`. The sync engine re-emits `sync_status_changed` every three
/// seconds while unauthenticated, empty or offline, so one rebuild per event
/// burned through the process-wide GDI object quota (10 000) within hours.
/// Past that point `TrackPopupMenu` can no longer show the popup and every
/// tray menu click silently did nothing. `set_text` writes `MIIM_STRING` only
/// and allocates no bitmap, so the patch path is leak-free.
struct MenuHandles {
    /// Everything a rebuild would change structurally — see `signature`.
    signature: String,
    status:    IconMenuItem<Wry>,
    conflicts: IconMenuItem<Wry>,
}

/// Managed by [`install`], filled by the first [`rebuild_menu`] call.
type MenuCell = Arc<Mutex<Option<MenuHandles>>>;

/// Registers the state the menu builder keeps between rebuilds. Call once,
/// before the first [`rebuild_menu`].
pub(crate) fn install<M: Manager<Wry>>(manager: &M) {
    manager.manage(MenuCell::default());
}

/// Identifies a tray menu layout: language decides every label, the sync-root
/// set decides the "Ordner oeffnen" submenu, and the update entry only exists
/// once a check found a newer version. Status text and conflict count are
/// deliberately absent — those are exactly what the patch path updates.
fn signature(lang: &str, roots: &[SyncRootEntry], update_version: Option<&str>) -> String {
    let mut sig = format!("{lang}|{}|", update_version.unwrap_or(""));
    for r in roots {
        sig.push_str(&r.id);
        sig.push(':');
        sig.push_str(&r.name);
        sig.push(':');
        sig.push_str(&r.kind);
        sig.push(';');
    }
    sig
}

/// Renders the status line and the conflict entry for the current DB state.
/// Returns `(status_label, conflicts_label, conflict_count)`.
fn labels(db: &LocalDb, lang: &str) -> (String, String, usize) {
    let pending = db.list_pending_all().unwrap_or_default();
    let conflict_count = pending.iter().filter(|f| f.sync_status == "conflict").count();
    let pending_count  = pending.len() - conflict_count;

    let status_label = if conflict_count > 0 {
        rust_i18n::t!("tray.status.conflicts", locale = lang, count = conflict_count).into_owned()
    } else if pending_count > 0 {
        rust_i18n::t!("tray.status.syncing",   locale = lang, count = pending_count).into_owned()
    } else {
        rust_i18n::t!("tray.status.ready",     locale = lang).into_owned()
    };
    let conflicts_label = if conflict_count > 0 {
        rust_i18n::t!("tray.conflicts.show", locale = lang, count = conflict_count).into_owned()
    } else {
        rust_i18n::t!("tray.conflicts.none", locale = lang).into_owned()
    };
    (status_label, conflicts_label, conflict_count)
}

/// Brings the tray context menu in line with the current DB state. Called at
/// startup and on every `sync_status_changed` event.
///
/// Only a structural change (language, sync roots, update availability) builds
/// a new menu; a plain status change patches the retained handles in place.
pub(crate) fn rebuild_menu(app: &AppHandle) -> tauri::Result<()> {
    let db   = app.state::<Arc<LocalDb>>();
    let lang = language(&db);
    let lc   = lang.as_str();

    let (status_label, conflicts_label, conflict_count) = labels(&db, lc);
    let roots = db.list_sync_roots().unwrap_or_default();

    // Update item only appears once a check found a newer version — it
    // survives a banner dismiss because it reads the backend-side
    // UpdateTracker, not any (dismissable) frontend state.
    let update_info = app
        .try_state::<Arc<updater::UpdateTracker>>()
        .and_then(|t| t.info.lock().unwrap().clone());
    let signature = signature(lc, &roots, update_info.as_ref().map(|i| i.version.as_str()));

    let cell: Option<MenuCell> = app.try_state::<MenuCell>().map(|c| c.inner().clone());

    // ── Patch path ────────────────────────────────────────────────────────
    // Clone the handles out before touching them: `set_text` dispatches to the
    // main thread and blocks there, and holding the cell lock across that
    // would serialise every other caller behind it for no reason.
    if let Some(cell) = &cell {
        let retained = cell.lock().unwrap().as_ref().and_then(|h| {
            (h.signature == signature).then(|| (h.status.clone(), h.conflicts.clone()))
        });
        if let Some((status, conflicts)) = retained {
            status.set_text(&status_label)?;
            conflicts.set_text(&conflicts_label)?;
            conflicts.set_enabled(conflict_count > 0)?;
            return Ok(());
        }
    }

    // ── Full build ────────────────────────────────────────────────────────
    // Icons for the context menu. Reuse the existing tray icons (small
    // enough) and decode once per rebuild — no cache needed now that a
    // rebuild only happens on a structural change.
    let icon_idle     = Image::from_bytes(include_bytes!("../icons/tray-icon.png"))     .ok();
    let icon_syncing  = Image::from_bytes(include_bytes!("../icons/tray-syncing.png"))  .ok();
    let icon_conflict = Image::from_bytes(include_bytes!("../icons/tray-conflict.png")) .ok();

    let label_open      = rust_i18n::t!("tray.open",       locale = lc).into_owned();
    let label_sync_now  = rust_i18n::t!("tray.syncNow",    locale = lc).into_owned();
    let label_settings  = rust_i18n::t!("tray.settings",   locale = lc).into_owned();
    let label_open_root    = rust_i18n::t!("tray.openRoots",   locale = lc).into_owned();
    let label_group_owned  = rust_i18n::t!("tray.groupOwned",  locale = lc).into_owned();
    let label_group_shared = rust_i18n::t!("tray.groupShared", locale = lc).into_owned();
    let label_quit         = rust_i18n::t!("tray.quit",        locale = lc).into_owned();

    let update_item = update_info.map(|info| {
        let label = rust_i18n::t!("tray.update.label", locale = lc, version = info.version).into_owned();
        MenuItem::with_id(app, "update_available", &label, true, None::<&str>)
    }).transpose()?;

    let status_item = IconMenuItem::with_id(
        app, "status", &status_label, false, icon_idle.clone(), None::<&str>,
    )?;
    let show_item     = MenuItem::with_id(app, "show",     &label_open,     true, None::<&str>)?;
    let sync_now      = IconMenuItem::with_id(
        app, "sync_now", &label_sync_now, true, icon_syncing.clone(), None::<&str>,
    )?;
    let settings_item = MenuItem::with_id(app, "settings", &label_settings, true, None::<&str>)?;

    // Submenu of all sync roots, grouped into own folders and folders shared
    // with the user, each alphabetically sorted (see `tray_menu`). Group
    // headers are disabled label items; openable roots carry id open_root_<uuid>.
    let rows = tray_menu::build_open_rows(&roots);
    let mut open_items: Vec<Box<dyn IsMenuItem<Wry>>> = Vec::new();
    for row in &rows {
        match row {
            tray_menu::OpenRow::OwnedHeader => open_items.push(Box::new(
                MenuItem::with_id(app, "grp_owned", &label_group_owned, false, None::<&str>)?,
            )),
            tray_menu::OpenRow::SharedHeader => open_items.push(Box::new(
                MenuItem::with_id(app, "grp_shared", &label_group_shared, false, None::<&str>)?,
            )),
            tray_menu::OpenRow::Separator => {
                open_items.push(Box::new(PredefinedMenuItem::separator(app)?))
            }
            tray_menu::OpenRow::Root { id, name } => {
                let mid = format!("open_root_{id}");
                open_items.push(Box::new(
                    MenuItem::with_id(app, &mid, name, true, None::<&str>)?,
                ));
            }
        }
    }
    let root_refs: Vec<&dyn IsMenuItem<Wry>> =
        open_items.iter().map(|b| b.as_ref()).collect();
    let roots_submenu = Submenu::with_items(
        app, &label_open_root, !rows.is_empty(), &root_refs,
    )?;

    let conflicts_item = IconMenuItem::with_id(
        app, "conflicts", &conflicts_label, conflict_count > 0,
        icon_conflict.clone(), None::<&str>,
    )?;

    let quit_item = MenuItem::with_id(app, "quit", &label_quit, true, None::<&str>)?;
    let sep1 = PredefinedMenuItem::separator(app)?;
    let sep2 = PredefinedMenuItem::separator(app)?;

    let mut menu_items: Vec<&dyn IsMenuItem<Wry>> = vec![&status_item];
    if let Some(update_item) = &update_item {
        menu_items.push(update_item);
    }
    menu_items.extend([
        &sep1 as &dyn IsMenuItem<Wry>,
        &show_item,
        &sync_now,
        &roots_submenu,
        &conflicts_item,
        &settings_item,
        &sep2,
        &quit_item,
    ]);
    let menu = Menu::with_items(app, &menu_items)?;

    if let Some(tray) = app.tray_by_id("main") {
        tray.set_menu(Some(menu))?;
    }
    if let Some(cell) = &cell {
        *cell.lock().unwrap() = Some(MenuHandles {
            signature,
            status:    status_item,
            conflicts: conflicts_item,
        });
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::{SyncRootEntry, ROOT_KIND_MOUNT, ROOT_KIND_OWNED};

    fn root(id: &str, name: &str, kind: &str) -> SyncRootEntry {
        SyncRootEntry {
            id:             id.to_string(),
            name:           name.to_string(),
            local_path:     format!("C:/sync/{id}"),
            server_root_id: id.to_string(),
            download_mode:  "on_demand".to_string(),
            read_only:      false,
            paused:         false,
            kind:           kind.to_string(),
        }
    }

    #[test]
    fn signature_is_stable_across_identical_input() {
        let roots = vec![root("a", "Fotos", ROOT_KIND_OWNED)];
        assert_eq!(
            signature("de", &roots, None),
            signature("de", &roots, None),
        );
    }

    #[test]
    fn language_change_invalidates_the_signature() {
        let roots = vec![root("a", "Fotos", ROOT_KIND_OWNED)];
        assert_ne!(
            signature("de", &roots, None),
            signature("en", &roots, None),
        );
    }

    #[test]
    fn root_set_change_invalidates_the_signature() {
        let before = vec![root("a", "Fotos", ROOT_KIND_OWNED)];
        let after  = vec![
            root("a", "Fotos", ROOT_KIND_OWNED),
            root("m", "Team",  ROOT_KIND_MOUNT),
        ];
        assert_ne!(signature("de", &before, None), signature("de", &after, None));
    }

    #[test]
    fn renaming_a_root_invalidates_the_signature() {
        let before = vec![root("a", "Fotos", ROOT_KIND_OWNED)];
        let after  = vec![root("a", "Bilder", ROOT_KIND_OWNED)];
        assert_ne!(signature("de", &before, None), signature("de", &after, None));
    }

    #[test]
    fn an_appearing_update_invalidates_the_signature() {
        let roots = vec![root("a", "Fotos", ROOT_KIND_OWNED)];
        assert_ne!(
            signature("de", &roots, None),
            signature("de", &roots, Some("0.6.0")),
        );
    }
}
