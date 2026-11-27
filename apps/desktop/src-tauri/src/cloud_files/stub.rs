//! No-op Cloud Files stub for macOS and Linux.
//!
//! All functions succeed immediately.  Files are downloaded eagerly by the
//! sync engine (no placeholder support on these platforms yet).

use std::path::Path;
use std::sync::Arc;
use anyhow::Result;

use super::{ConvertOutcome, HydrationCtx, PlaceholderInfo, PlaceholderOutcome};

/// Placeholder connection handle — nothing to hold on non-Windows.
pub struct Connection;

/// Register a directory as a cloud sync provider (no-op).
pub fn register(_local_path: &Path, _display_name: &str) -> Result<()> {
    Ok(())
}

/// Unregister a cloud sync provider (no-op).
pub fn unregister(_local_path: &Path) -> Result<()> {
    Ok(())
}

/// Connect hydration callbacks for a sync root (no-op).
pub fn connect(
    _local_path: &Path,
    _ctx: Arc<HydrationCtx>,
) -> Result<Connection> {
    Ok(Connection)
}

/// Bring a placeholder in line with the server's state (no-op — there are no
/// placeholders here, so the caller always writes the bytes itself).
pub fn ensure_placeholder(
    _base_path: &Path,
    _info: &PlaceholderInfo<'_>,
) -> Result<PlaceholderOutcome> {
    Ok(PlaceholderOutcome::NeedsFullDownload)
}

/// Mark a file as fully in-sync (no-op).
pub fn mark_in_sync(_path: &Path) -> Result<()> {
    Ok(())
}

/// Mark a file as currently syncing (no-op).
pub fn mark_syncing(_path: &Path) -> Result<()> {
    Ok(())
}

/// Convert a regular file to a CF placeholder (no-op on non-Windows).
pub fn convert_to_placeholder(_path: &Path, _server_path: &str, _in_sync: bool) -> ConvertOutcome {
    ConvertOutcome::Succeeded
}

/// Settle an excluded file in Explorer (no-op on non-Windows).
pub fn mark_excluded(_path: &Path, _identity: &str) -> Result<()> {
    Ok(())
}

/// Hydrate a placeholder file (no-op — files are eagerly downloaded on non-Windows).
pub fn hydrate_file(_path: &Path) -> Result<()> {
    Ok(())
}

/// Dehydrate a placeholder file (no-op on non-Windows — no placeholders exist).
pub fn dehydrate_file(_path: &Path) -> Result<()> {
    Ok(())
}

/// Pin a file so it stays resident (no-op on non-Windows).
pub fn set_pinned(_path: &Path) -> Result<()> {
    Ok(())
}

/// Unpin a file so it may be dehydrated (no-op on non-Windows).
pub fn set_unpinned(_path: &Path) -> Result<()> {
    Ok(())
}

/// Returns `true` if `path` is a dehydrated placeholder.
/// Always `false` on non-Windows — every file is fully on disk.
pub fn is_dehydrated(_path: &Path) -> bool {
    false
}

