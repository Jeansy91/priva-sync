//! Tauri-exposed commands for sync-root management.
//!
//! Every `#[tauri::command]` belongs here. Sync-engine internals (DB writes,
//! cloud-files registration) stay in their own modules and are called from
//! these commands. Commands nudge the engine via `sync_now.notify_one()`
//! whenever a change should take effect before the next 30 s poll tick.

use std::path::Path;
use std::sync::Arc;
use tracing::{info, warn};

use crate::cloud_files;
use crate::command_error::{codes, CommandError};
use crate::db::LocalDb;

// ─── Add / list / set-mode ───────────────────────────────────────────────────

#[tauri::command]
pub async fn add_sync_root(
    db:       tauri::State<'_, Arc<LocalDb>>,
    sync_now: tauri::State<'_, Arc<tokio::sync::Notify>>,
    id:             String,
    name:           String,
    local_path:     String,
    server_root_id: String,
    download_mode:  String,
) -> Result<(), String> {
    let path = Path::new(&local_path);
    std::fs::create_dir_all(path).map_err(|e| e.to_string())?;

    // A root that cannot carry placeholders cannot be on-demand either, and
    // registering it as a CF provider is what leaves Explorer talking to a
    // provider that never answers (ADR-0057). Both are corrected here rather
    // than trusted from the caller: the UI hides the choice, but the command
    // is the authority.
    let download_mode = if cloud_files::placeholders_available_for(path) {
        // Register as a Cloud Files sync provider so Explorer shows cloud icons.
        if let Err(e) = cloud_files::register(path, &name).await {
            warn!("cloud_files::register failed (non-fatal): {e}");
        }
        download_mode
    } else {
        if download_mode != "eager" {
            info!("'{name}' ({local_path}) kann keine Platzhalter tragen — \
                   Download-Modus auf 'eager' gesetzt");
        }
        "eager".to_string()
    };

    db.upsert_sync_root(&id, &name, &local_path, &server_root_id, &download_mode, false,
            crate::db::ROOT_KIND_OWNED)
        .map_err(|e| e.to_string())?;
    let roots = db.list_sync_roots().map_err(|e| e.to_string())?;
    let namespace_roots = roots.iter()
        .map(|root| (root.name.clone(), root.local_path.clone()))
        .collect();
    if let Err(e) = cloud_files::sync_explorer_namespace(namespace_roots).await {
        warn!("Explorer namespace refresh after adding a root failed: {e:#}");
    }
    // Trigger an immediate sync so server files appear as placeholders right
    // away and the watcher restarts to cover the new directory.
    sync_now.notify_one();
    Ok(())
}

#[tauri::command]
pub async fn list_sync_roots(
    db: tauri::State<'_, Arc<LocalDb>>,
) -> Result<Vec<crate::db::SyncRootEntry>, String> {
    db.list_sync_roots().map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn set_download_mode(
    db:       tauri::State<'_, Arc<LocalDb>>,
    sync_now: tauri::State<'_, Arc<tokio::sync::Notify>>,
    id:   String,
    mode: String,
) -> Result<(), CommandError> {
    // On-demand needs Cloud Files placeholders. A root that cannot carry them
    // — a volume root such as `D:\`, or any root on a platform without CF —
    // would silently keep downloading everything in full, so the request is
    // refused with a reason instead of being accepted and ignored (ADR-0057).
    if mode != "eager" {
        let root = db.get_sync_root(&id)?
            .ok_or_else(|| CommandError::coded(codes::SYNC_ROOT_NOT_FOUND, "sync root not found"))?;
        if !cloud_files::placeholders_available_for(Path::new(&root.local_path)) {
            return Err(CommandError::coded(
                codes::ON_DEMAND_ON_DRIVE_ROOT,
                format!("'{}' is a drive root; on-demand is unavailable there", root.local_path),
            ));
        }
    }
    db.set_download_mode(&id, &mode).map_err(|e| e.to_string())?;
    // Trigger a sync so the new mode takes effect immediately.
    sync_now.notify_one();
    Ok(())
}

/// Read the global exclusion patterns, applied to every sync root.
/// Returns the built-in defaults when the user has never saved a value.
#[tauri::command]
pub async fn get_ignore_patterns(
    db: tauri::State<'_, Arc<LocalDb>>,
) -> Result<String, String> {
    db.get_ignore_patterns().map_err(|e| e.to_string())
}

/// The built-in default exclusion list — used by the "restore defaults"
/// action in Settings. Kept server-side so Rust stays the single source.
#[tauri::command]
pub async fn get_default_ignore_patterns() -> Result<String, String> {
    Ok(crate::db::DEFAULT_IGNORE_PATTERNS.to_string())
}

/// Validate exclusion patterns against the real gitignore engine so the
/// editor can flag malformed lines before they are saved. Returns one entry per
/// rejected line (1-based) — an empty list means the whole text is valid.
#[tauri::command]
pub async fn validate_ignore_patterns(
    patterns: String,
) -> Result<Vec<super::ignore::PatternError>, String> {
    Ok(super::ignore::validate_patterns(&patterns))
}

/// Count how many files the saved patterns currently exclude across all
/// writable roots, with a capped sample for display. Read-only roots are
/// skipped — they never upload, so exclusions have no effect there.
#[tauri::command]
pub async fn count_excluded_files(
    db: tauri::State<'_, Arc<LocalDb>>,
) -> Result<super::ignore::ExcludedPreview, String> {
    let patterns = db.get_ignore_patterns().map_err(|e| e.to_string())?;
    let matcher  = super::ignore::IgnoreMatcher::from_patterns(&patterns);
    let roots: Vec<(String, std::path::PathBuf)> = db
        .list_sync_roots().map_err(|e| e.to_string())?
        .into_iter()
        .filter(|r| !r.read_only)
        .map(|r| (r.name, std::path::PathBuf::from(r.local_path)))
        .collect();
    Ok(super::ignore::preview_excluded(&roots, &matcher, 100))
}

/// Replace the global exclusion patterns and trigger a sync so the new
/// rules take effect immediately — newly-ignored files stop syncing, and files
/// no longer matched are picked up on the next reconcile.
#[tauri::command]
pub async fn set_ignore_patterns(
    db:       tauri::State<'_, Arc<LocalDb>>,
    sync_now: tauri::State<'_, Arc<tokio::sync::Notify>>,
    patterns: String,
) -> Result<(), String> {
    db.set_ignore_patterns(&patterns).map_err(|e| e.to_string())?;
    sync_now.notify_one();
    Ok(())
}

// ─── Remove sync root ────────────────────────────────────────────────────────

/// Stats for the remove-sync-root confirmation dialog. `dehydrated_*` count
/// the on-demand placeholders whose content lives only on the server — those
/// turn into broken zombie files if the provider is unregistered without
/// cleaning them up first.
#[derive(serde::Serialize)]
pub struct RemoveSyncRootOptions {
    pub dehydrated_count: u64,
    pub dehydrated_bytes: u64,
}

#[tauri::command]
pub async fn get_remove_sync_root_options(
    db: tauri::State<'_, Arc<LocalDb>>,
    id: String,
) -> Result<RemoveSyncRootOptions, String> {
    let root = match db.get_sync_root(&id).map_err(|e| e.to_string())? {
        Some(r) => r,
        None    => return Ok(RemoveSyncRootOptions { dehydrated_count: 0, dehydrated_bytes: 0 }),
    };

    // A root that cannot carry placeholders has none to count, and the answer
    // is known without touching the disk. Walking anyway meant the "Prüfe
    // lokale Dateien…" step of the remove dialog traversed a whole drive —
    // `$RECYCLE.BIN` and `System Volume Information` included — for several
    // minutes to arrive at zero (ADR-0057).
    if !cloud_files::placeholders_available_for(Path::new(&root.local_path)) {
        return Ok(RemoveSyncRootOptions { dehydrated_count: 0, dehydrated_bytes: 0 });
    }

    // Walk the local tree once and tally placeholders that have no local
    // payload yet. metadata().len() on a CF placeholder reports the expected
    // (server) size, which is what we want to surface as "would-need-to-
    // download bytes" to the user.
    //
    // On a blocking thread: this is a full traversal, and a command that runs
    // it inline occupies a Tokio worker for its whole duration.
    let local_path = root.local_path.clone();
    tokio::task::spawn_blocking(move || {
        let mut count: u64 = 0;
        let mut bytes: u64 = 0;
        for entry in walkdir::WalkDir::new(&local_path)
            .into_iter()
            .filter_map(|e| e.ok())
            .filter(|e| e.file_type().is_file())
        {
            if cloud_files::is_dehydrated(entry.path()) {
                count += 1;
                if let Ok(meta) = entry.metadata() {
                    bytes = bytes.saturating_add(meta.len());
                }
            }
        }
        RemoveSyncRootOptions { dehydrated_count: count, dehydrated_bytes: bytes }
    })
    .await
    .map_err(|e| format!("spawn_blocking: {e}"))
}

/// Remove a sync root. Always scrubs dehydrated CF placeholders from the
/// local tree before unregistering — leaving them behind would create zombie
/// files (visible in Explorer with name + size, but unopenable because the
/// provider is gone). Files the user actually downloaded stay on disk.
#[tauri::command]
pub async fn remove_sync_root(
    db:       tauri::State<'_, Arc<LocalDb>>,
    sync_now: tauri::State<'_, Arc<tokio::sync::Notify>>,
    id: String,
) -> Result<(), String> {
    if let Ok(Some(root)) = db.get_sync_root(&id) {
        // Scrub dehydrated placeholders BEFORE unregister — afterwards they
        // become orphaned reparse points that Explorer still shows but
        // cannot open.
        //
        // Skipped entirely where placeholders were never possible: the sweep
        // is a full traversal of the root, and on a drive root it would spend
        // minutes finding nothing (ADR-0057). On a blocking thread otherwise,
        // for the same reason the dialog's pre-count is.
        if cloud_files::placeholders_available_for(Path::new(&root.local_path)) {
            let path = root.local_path.clone();
            let removed = tokio::task::spawn_blocking(move || drop_dehydrated_placeholders(&path))
                .await
                .unwrap_or(0);
            if removed > 0 {
                info!(
                    "remove_sync_root('{}'): dropped {removed} dehydrated placeholder(s)",
                    root.name
                );
            }
        }

        if let Err(e) = cloud_files::unregister(Path::new(&root.local_path)).await {
            warn!("cloud_files::unregister failed (non-fatal): {e}");
        }
    }
    db.remove_sync_root(&id).map_err(|e| e.to_string())?;
    if let Ok(roots) = db.list_sync_roots() {
        let namespace_roots = roots.iter()
            .map(|root| (root.name.clone(), root.local_path.clone()))
            .collect();
        if let Err(e) = cloud_files::sync_explorer_namespace(namespace_roots).await {
            warn!("Explorer namespace refresh after removing a root failed: {e:#}");
        }
    }
    // Kick the sync engine so its inner loop re-fetches the roots list,
    // detects the removal, and restarts the file watcher without the now-
    // orphaned path. Without this notify the watcher would keep watching
    // the removed directory until the next 30s poll tick.
    sync_now.notify_one();
    Ok(())
}

/// Walks `base` and removes every dehydrated CF placeholder. Best-effort —
/// individual delete failures are logged but do not abort the sweep.
/// Returns the count of files actually removed.
pub(super) fn drop_dehydrated_placeholders(base: &str) -> u64 {
    let mut removed: u64 = 0;
    for entry in walkdir::WalkDir::new(base)
        .into_iter()
        .filter_map(|e| e.ok())
        .filter(|e| e.file_type().is_file())
    {
        let path = entry.path();
        if !cloud_files::is_dehydrated(path) {
            continue;
        }
        match std::fs::remove_file(path) {
            Ok(()) => removed += 1,
            Err(e) => warn!("failed to drop placeholder '{}': {e}", path.display()),
        }
    }
    removed
}

// ─── Sync control (force, pause, resume, bulk-delete) ────────────────────────

#[tauri::command]
pub async fn force_sync(
    sync_now:   tauri::State<'_, Arc<tokio::sync::Notify>>,
    force_walk: tauri::State<'_, Arc<std::sync::atomic::AtomicBool>>,
) -> Result<(), String> {
    // "Sync now" implies a full local walk, not just pushing what's already
    // staged (ADR-0037) -- see SyncEngine::force_walk.
    force_walk.store(true, std::sync::atomic::Ordering::Relaxed);
    sync_now.notify_one();
    Ok(())
}

/// User confirmed that the pending bulk-delete for this root should proceed.
/// Sets a one-shot flag in the DB so the next sync cycle skips the guard,
/// resumes the root (which the guard auto-paused), and triggers an immediate
/// sync. `direction` selects which of the two independent guards
/// this confirms — "incoming" (server-initiated deletions the client is
/// about to apply) or anything else, treated as the original outbound guard
/// (client-initiated deletions about to be pushed). Two separate flags on
/// purpose: both can legitimately be paused on the same root at once, and
/// confirming one must never also wave the other's unrelated wave through.
#[tauri::command]
pub async fn confirm_bulk_delete(
    db:       tauri::State<'_, Arc<LocalDb>>,
    sync_now: tauri::State<'_, Arc<tokio::sync::Notify>>,
    root_id: String,
    direction: String,
) -> Result<(), String> {
    if direction == "incoming" {
        db.set_bulk_delete_confirmed_incoming(&root_id)
    } else {
        db.set_bulk_delete_confirmed(&root_id)
    }.map_err(|e| e.to_string())?;
    db.set_sync_root_paused(&root_id, false)
        .map_err(|e| e.to_string())?;
    sync_now.notify_one();
    Ok(())
}

/// Manually pause this sync root. The cycle will skip it until the user
/// resumes it. Does not touch any pending state — uploads/deletes will
/// resume from where they were.
#[tauri::command]
pub async fn pause_sync_root(
    db: tauri::State<'_, Arc<LocalDb>>,
    root_id: String,
) -> Result<(), String> {
    db.set_sync_root_paused(&root_id, true)
        .map_err(|e| e.to_string())
}

/// Resume a previously paused sync root and trigger an immediate sync.
#[tauri::command]
pub async fn resume_sync_root(
    db:       tauri::State<'_, Arc<LocalDb>>,
    sync_now: tauri::State<'_, Arc<tokio::sync::Notify>>,
    root_id: String,
) -> Result<(), String> {
    db.set_sync_root_paused(&root_id, false)
        .map_err(|e| e.to_string())?;
    sync_now.notify_one();
    Ok(())
}

/// Bulk-delete "restore locally" path: drop the staged pending_delete rows,
/// reset the server change cursor so the next download cycle re-fetches the
/// full history, then resume the root and trigger a sync. Used when the user
/// wants to undo a local mass deletion by pulling the server copies back down.
#[tauri::command]
pub async fn restore_local_files(
    db:       tauri::State<'_, Arc<LocalDb>>,
    sync_now: tauri::State<'_, Arc<tokio::sync::Notify>>,
    root_id: String,
) -> Result<(), String> {
    let cleared = db.clear_pending_deletes(&root_id)
        .map_err(|e| e.to_string())?;
    db.clear_last_sync(&root_id).map_err(|e| e.to_string())?;
    db.set_sync_root_paused(&root_id, false)
        .map_err(|e| e.to_string())?;
    info!("restore_local_files: cleared {cleared} pending_delete rows for {root_id}");
    sync_now.notify_one();
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    // A directory with only regular (non-CF-placeholder) files must be left
    // completely untouched: nothing is dehydrated, so nothing is removed.
    // `is_dehydrated` is a hard `false` on non-Windows and, on Windows, false
    // for a plain file with no reparse point — either way this must hold.
    #[test]
    fn drop_dehydrated_placeholders_leaves_regular_files_alone() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join("a.txt"), b"hello").unwrap();
        std::fs::create_dir(dir.path().join("sub")).unwrap();
        std::fs::write(dir.path().join("sub/b.txt"), b"world").unwrap();

        let removed = drop_dehydrated_placeholders(&dir.path().to_string_lossy());

        assert_eq!(removed, 0, "no placeholder present -- nothing should be removed");
        assert!(dir.path().join("a.txt").exists());
        assert!(dir.path().join("sub/b.txt").exists());
    }

    // A missing/non-existent base path must not panic — walkdir yields no
    // entries and the sweep is a no-op.
    #[test]
    fn drop_dehydrated_placeholders_handles_missing_base_gracefully() {
        let removed = drop_dehydrated_placeholders("C:/this/path/does/not/exist-priva-sync-test");
        assert_eq!(removed, 0);
    }
}
