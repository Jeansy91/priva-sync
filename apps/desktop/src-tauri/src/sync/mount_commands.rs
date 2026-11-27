//! Tauri commands for Shared Mounts (ADR-0011): discover available mounts,
//! activate one at a recipient-chosen local path, and remove it locally.
//!
//! Activated mounts live in the same `sync_roots` table as own roots, with
//! `kind='mount'` and `server_root_id == share_id`. From there the normal
//! sync engine handles them — only the REST namespace and the read-only guard
//! differ (Slices 2c/3). Available-but-not-activated mounts are derived purely
//! from the server pull and never persisted locally.

use std::path::Path;
use std::sync::Arc;
use tokio::sync::RwLock;
use tracing::{info, warn};

use crate::api::ApiClient;
use crate::cloud_files;
use crate::command_error::CommandError;
use crate::db::{LocalDb, ROOT_KIND_MOUNT};

/// A mount the recipient could activate but hasn't yet — a server-side Shared
/// Mount with no local sync-root row. Directory shares only; file mounts are
/// web-only on the desktop (ADR-0011), so they are filtered out here.
#[derive(serde::Serialize)]
pub struct AvailableMount {
    pub share_id:       String,
    pub name:           String,
    pub path:           String,
    pub can_write:      bool,
    pub owner_username: String,
}

/// Pull the recipient's Shared Mounts and return only those not yet activated
/// locally and not file-mounts. Powers the tray "available, not activated"
/// section. The server already drops expired and public shares.
#[tauri::command]
pub async fn list_available_mounts(
    api: tauri::State<'_, Arc<RwLock<ApiClient>>>,
    db:  tauri::State<'_, Arc<LocalDb>>,
) -> Result<Vec<AvailableMount>, CommandError> {
    let mounts = api.read().await.list_shared_mounts().await?;
    let local = db.list_sync_roots().map_err(|e| e.to_string())?;
    // Activated mounts are keyed by share_id (= server_root_id) on kind='mount' rows.
    let activated: std::collections::HashSet<String> = local.iter()
        .filter(|r| r.is_mount())
        .map(|r| r.server_root_id.clone())
        .collect();

    Ok(mounts.into_iter()
        .filter(|m| !m.is_file)              // file mounts are web-only (ADR-0011)
        .filter(|m| !activated.contains(&m.id))
        .map(|m| AvailableMount {
            share_id:      m.id,
            name:          m.name,
            path:          m.path,
            can_write:     m.can_write,
            owner_username: m.owner.username,
        })
        .collect())
}

/// Activate a Shared Mount at a recipient-chosen local path. Creates the
/// directory, registers a Cloud Files provider (display name carries the giver
/// so Explorer shows the foreign ownership), and inserts the `kind='mount'`
/// sync-root row. `read_only` mirrors `!can_write`; the engine RO-guard
/// (Slice 3) enforces it. id == server_root_id == share_id keeps the mount
/// single-keyed. ADR-0011.
#[tauri::command]
pub async fn activate_mount(
    db:       tauri::State<'_, Arc<LocalDb>>,
    sync_now: tauri::State<'_, Arc<tokio::sync::Notify>>,
    share_id:       String,
    name:           String,
    local_path:     String,
    can_write:      bool,
    owner_username: String,
) -> Result<(), String> {
    let path = Path::new(&local_path);
    std::fs::create_dir_all(path).map_err(|e| e.to_string())?;

    let display = format!("{name} ({owner_username})");
    if let Err(e) = cloud_files::register(path, &display).await {
        warn!("cloud_files::register (mount) failed (non-fatal): {e}");
    }

    db.upsert_sync_root(&share_id, &name, &local_path, &share_id, "on_demand", !can_write, ROOT_KIND_MOUNT)
        .map_err(|e| e.to_string())?;
    let roots = db.list_sync_roots().map_err(|e| e.to_string())?;
    let namespace_roots = roots.iter()
        .map(|root| (root.name.clone(), root.local_path.clone()))
        .collect();
    if let Err(e) = cloud_files::sync_explorer_namespace(namespace_roots).await {
        warn!("Explorer namespace refresh after activating a mount failed: {e:#}");
    }
    info!("activated mount '{name}' from {owner_username} at {local_path}");
    sync_now.notify_one();
    Ok(())
}

/// Remove an activated mount locally only: drop the sync-root row and the CF
/// provider, but leave the server `shares` row untouched so the mount
/// reappears as "available". Local bytes stay on disk. This is the
/// recipient-initiated counterpart to a server-side revocation, which the
/// prune path handles instead. ADR-0011.
#[tauri::command]
pub async fn remove_mount(
    db:       tauri::State<'_, Arc<LocalDb>>,
    sync_now: tauri::State<'_, Arc<tokio::sync::Notify>>,
    share_id: String,
) -> Result<(), String> {
    if let Ok(Some(root)) = db.get_sync_root(&share_id) {
        // Scrub dehydrated placeholders before unregister — otherwise they
        // become orphaned reparse points Explorer cannot open.
        let removed = super::commands::drop_dehydrated_placeholders(&root.local_path);
        if removed > 0 {
            info!("remove_mount('{}'): dropped {removed} dehydrated placeholder(s)", root.name);
        }
        if let Err(e) = cloud_files::unregister(Path::new(&root.local_path)).await {
            warn!("cloud_files::unregister (mount) failed (non-fatal): {e}");
        }
    }
    db.remove_sync_root(&share_id).map_err(|e| e.to_string())?;
    if let Ok(roots) = db.list_sync_roots() {
        let namespace_roots = roots.iter()
            .map(|root| (root.name.clone(), root.local_path.clone()))
            .collect();
        if let Err(e) = cloud_files::sync_explorer_namespace(namespace_roots).await {
            warn!("Explorer namespace refresh after removing a mount failed: {e:#}");
        }
    }
    sync_now.notify_one();
    Ok(())
}
