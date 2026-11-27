//! Virtual file placeholder support.
//!
//! On Windows we use the Cloud Files API so files appear in Explorer
//! immediately as placeholders (with the cloud icon) and are downloaded on
//! demand when the user opens them.
//!
//! On macOS and Linux we fall back to eager download: every file is fully
//! written to disk during the sync cycle.  (macOS File Provider requires a
//! sandboxed App Extension; Linux FUSE would need a separate mount point —
//! both are tracked as future work.)

use std::sync::Arc;

use crate::api::AuthedApi;
use crate::db::LocalDb;

// ─── Shared types ─────────────────────────────────────────────────────────────

/// Outcome of a `convert_to_placeholder` call. Captures *how* the conversion
/// resolved so callers (e.g. `ensure_placeholders`) can aggregate stats and
/// reason about why some files fail.
#[must_use]
pub enum ConvertOutcome {
    /// Conversion succeeded.
    Succeeded,
    /// File was already a CF placeholder — treated as success (idempotent).
    AlreadyPlaceholder,
    /// Refused with `0x8007017C` / `0x80070186` on an entry that is not a
    /// placeholder yet. Usually means the CF filter driver still does not see
    /// the file under a registered sync root.
    NotUnderSyncRoot,
    /// Any other error (bad path, access denied, …).
    Failed(anyhow::Error),
}

/// What [`ensure_placeholder`] did with the entry, so the caller knows
/// whether the server's bytes are now represented on disk or still have to be
/// written the hard way.
#[derive(Debug, PartialEq, Eq)]
pub enum PlaceholderOutcome {
    /// A fresh placeholder was created — nothing was there before.
    Created,
    /// An existing entry was re-pointed at the server's current state: new
    /// size and identity, local bytes dropped, so the next read fetches them.
    Updated,
    /// The entry exists but cannot be turned back into a dehydrated
    /// placeholder — the usual reason is a pinned file, which the user asked
    /// to keep on this device. The caller has to write the bytes itself.
    NeedsFullDownload,
}

/// Metadata needed to create a single placeholder entry.
pub struct PlaceholderInfo<'a> {
    /// Path relative to the sync-root, forward-slash separated, leading slash
    /// included (e.g. `/docs/report.pdf`).
    pub relative_path: &'a str,
    /// File size in bytes as reported by the server.
    pub size:  i64,
    /// Last-modified time as a Unix timestamp (seconds since epoch).
    pub mtime: i64,
}

/// Context the hydration callback uses to download a file when Windows asks
/// for its content.
pub struct HydrationCtx {
    pub api:            AuthedApi,
    pub db:             Arc<LocalDb>,
    pub rt:             tokio::runtime::Handle,
    pub server_root_id: String,
    /// Routes the hydration download to /shared-mounts vs /sync-roots (ADR-0011).
    pub is_mount:       bool,
}

// ─── Platform dispatch ────────────────────────────────────────────────────────

#[cfg(target_os = "windows")]
mod windows;
#[cfg(not(target_os = "windows"))]
mod stub;

// Re-export the platform-specific `Connection` type and all public functions.
// `register`/`unregister` are wrapped below instead of re-exported directly —
// see the comment on `register`.
#[cfg(target_os = "windows")]
pub use windows::{Connection, connect,
                  convert_to_placeholder, ensure_placeholder, hydrate_file,
                  dehydrate_file, mark_in_sync, mark_syncing, is_dehydrated,
                  mark_excluded, set_pinned, set_unpinned};
#[cfg(target_os = "windows")]
use windows::{register as platform_register, unregister as platform_unregister};
#[cfg(target_os = "windows")]
use windows::{
    reconcile_explorer_namespace as platform_reconcile_explorer_namespace,
    sync_explorer_namespace as platform_sync_explorer_namespace,
};

#[cfg(not(target_os = "windows"))]
pub use stub::{Connection, connect,
               convert_to_placeholder, ensure_placeholder, hydrate_file,
               dehydrate_file, mark_in_sync, mark_syncing, is_dehydrated,
               mark_excluded, set_pinned, set_unpinned};
#[cfg(not(target_os = "windows"))]
use stub::{register as platform_register, unregister as platform_unregister};

/// Registers `local_path` as a Cloud Files sync root (Windows: WinRT
/// `StorageProviderSyncRootManager`; no-op elsewhere).
///
/// Runs on a blocking thread, never directly on a Tokio executor thread: the
/// underlying WinRT/COM call can initialize the calling OS thread's COM
/// apartment as a side effect, and callers here (`engine_loop`'s periodic
/// re-registration, `placeholder_pass`) run on Tokio's small, reused pool
/// of async worker threads. A native Win32 dialog invoked later and
/// dispatched onto one of those same pooled threads then silently fails its
/// COM-dependent content enumeration (apartment mismatch) while still
/// drawing an empty window — this was diagnosed as the cause of a
/// permanently blank folder-picker dialog.
pub async fn register(local_path: &std::path::Path, display_name: &str) -> anyhow::Result<()> {
    let local_path = local_path.to_path_buf();
    let display_name = display_name.to_string();
    tokio::task::spawn_blocking(move || platform_register(&local_path, &display_name))
        .await
        .map_err(|e| anyhow::anyhow!("spawn_blocking: {e}"))?
}

/// Unregisters a Cloud Files sync root. See [`register`] for why this runs
/// on a blocking thread.
pub async fn unregister(local_path: &std::path::Path) -> anyhow::Result<()> {
    let local_path = local_path.to_path_buf();
    tokio::task::spawn_blocking(move || platform_unregister(&local_path))
        .await
        .map_err(|e| anyhow::anyhow!("spawn_blocking: {e}"))?
}

/// Reconcile stale PrivaSync roots and refresh the Explorer namespace.
#[cfg(target_os = "windows")]
pub async fn reconcile_explorer_namespace(roots: Vec<(String, String)>) -> anyhow::Result<()> {
    tokio::task::spawn_blocking(move || platform_reconcile_explorer_namespace(&roots))
        .await
        .map_err(|e| anyhow::anyhow!("spawn_blocking: {e}"))?
}

/// Refresh the Explorer namespace from the current roots.
#[cfg(target_os = "windows")]
pub async fn sync_explorer_namespace(roots: Vec<(String, String)>) -> anyhow::Result<()> {
    tokio::task::spawn_blocking(move || platform_sync_explorer_namespace(&roots))
        .await
        .map_err(|e| anyhow::anyhow!("spawn_blocking: {e}"))?
}

#[cfg(not(target_os = "windows"))]
pub async fn reconcile_explorer_namespace(_roots: Vec<(String, String)>) -> anyhow::Result<()> {
    Ok(())
}

#[cfg(not(target_os = "windows"))]
pub async fn sync_explorer_namespace(_roots: Vec<(String, String)>) -> anyhow::Result<()> {
    Ok(())
}

/// Returns `true` on platforms that support on-demand placeholder files.
/// On other platforms all files are downloaded eagerly.
pub fn supports_placeholders() -> bool {
    cfg!(target_os = "windows")
}

/// A path that is the root of its own volume — `D:\`, `C:\`, `/`, or a bare
/// UNC share — as opposed to a directory inside one.
///
/// Spelled out rather than delegated to `Path::parent().is_none()`. That
/// delegation was correct on Windows and meaningless anywhere else: to a Unix
/// path parser `D:\` is one ordinary filename, so `parent()` answers `Some("")`
/// and the rule silently inverted. CI runs on Linux, which is where it
/// surfaced — the Windows rule has to be expressible (and testable) on the
/// platform that checks it.
pub fn is_volume_root(local_path: &std::path::Path) -> bool {
    let raw = local_path.to_string_lossy();
    let path = raw.trim();
    if path.is_empty() {
        return false;
    }
    if path == "/" {
        return true;
    }

    // Windows drive root: `D:`, `D:\`, `D:/`.
    let b = path.as_bytes();
    if b.len() >= 2 && b[0].is_ascii_alphabetic() && b[1] == b':' {
        return match b.len() {
            2 => true,
            3 => b[2] == b'\\' || b[2] == b'/',
            _ => false,
        };
    }

    // Bare UNC share: `\\server\share`, with nothing below it.
    if let Some(rest) = path.strip_prefix(r"\\") {
        let parts: Vec<&str> = rest.trim_end_matches('\\').split('\\').collect();
        return parts.len() == 2 && parts.iter().all(|p| !p.is_empty());
    }

    false
}

/// Whether *this* sync root can carry Cloud Files placeholders.
///
/// Platform support is only half the question. The Cloud Filter API refuses a
/// volume root outright: `StorageProviderSyncRootManager::Register` answers
/// `0x8007017C` (`ERROR_CLOUD_FILE_INVALID_REQUEST`) for `D:\`, the filter
/// never attaches, and `CfConnectSyncRoot` then fails too. A root registered
/// that way is the worst of both worlds — Explorer sees a cloud provider for
/// the entire drive that never answers, and every placeholder conversion
/// fails one by one (a field log showed 23925 failures out of 26358 files).
///
/// Such a root is still perfectly syncable, just without the on-demand
/// hydration and the Explorer status column: files are transferred in full,
/// like on a platform with no placeholder support at all. Every CF-touching
/// path asks this instead of [`supports_placeholders`] so that fallback is
/// the same one non-Windows already takes (ADR-0057).
pub fn placeholders_available_for(local_path: &std::path::Path) -> bool {
    supports_placeholders() && !is_volume_root(local_path)
}

/// Make a path either fully resident on this device (pin + hydrate) or
/// reclaimable (unpin + dehydrate). Backs the "always keep on device" /
/// "free up space" context-menu verbs (ADR-0021). On non-Windows this is a
/// no-op — every file is already fully on disk.
pub fn set_resident(path: &std::path::Path, resident: bool) -> anyhow::Result<()> {
    if resident {
        set_pinned(path)?;
        hydrate_file(path)
    } else {
        set_unpinned(path)?;
        dehydrate_file(path)
    }
}

// ─── Hydration decisions ──────────────────────────────────────────────────────
//
// Explorer asks for a placeholder's bytes one window at a time, so a single
// file is hydrated by many independent HTTP requests. The two decisions below
// are what keeps that safe and cheap; they live here rather than in the
// Windows callback because they are ordinary logic, testable on any platform.

/// Whether a hydration response may be served, given the server version the
/// local placeholder row was created for and the version the response came
/// from.
///
/// Each window is its own request, so a file rewritten on the server midway
/// through hydration would otherwise be stitched together from two different
/// versions — a torn file that the sync engine would then consider `synced`.
/// A missing version on either side means there is nothing to pin against
/// (Shared Mounts do not carry one, and neither do older servers); the
/// hydration proceeds rather than failing on a check that cannot be made.
pub(crate) fn hydration_version_matches(placeholder: Option<i64>, response: Option<i64>) -> bool {
    match (placeholder, response) {
        (Some(a), Some(b)) => a == b,
        _ => true,
    }
}

/// The version to pin a hydration against, out of a local row's stored
/// `server_version`. That column carries `-1` until a version is actually
/// known, which means "nothing to pin against" rather than version minus one.
pub(crate) fn pinnable_version(stored: i64) -> Option<i64> {
    (stored >= 0).then_some(stored)
}

/// The bytes to hand back for a hydration request, out of what actually
/// arrived.
///
/// A server that honoured the `Range` sent exactly the requested window, and
/// `data` is already the answer. A server that ignored it answered 200 with
/// the whole file, and the window has to be cut out here — correct, just not
/// cheap, which is why the range request exists in the first place.
/// A window starting at or past the end yields nothing rather than panicking.
pub(crate) fn hydration_window(data: &[u8], is_partial: bool, offset: i64, length: i64) -> &[u8] {
    if is_partial {
        return data;
    }
    let start = (offset.max(0) as usize).min(data.len());
    let end   = start.saturating_add(length.max(0) as usize).min(data.len());
    &data[start..end]
}

#[cfg(test)]
mod hydration_tests {
    use super::*;

    #[test]
    fn a_response_from_the_placeholders_own_version_is_served() {
        assert!(hydration_version_matches(Some(7), Some(7)));
    }

    #[test]
    fn a_response_from_a_different_version_is_refused() {
        assert!(
            !hydration_version_matches(Some(7), Some(8)),
            "serving this window would stitch the file together from two server versions"
        );
    }

    #[test]
    fn an_unpinnable_hydration_is_served_rather_than_refused() {
        assert!(hydration_version_matches(None, Some(8)), "placeholder predates version tracking");
        assert!(hydration_version_matches(Some(7), None), "endpoint sends no version (Shared Mount)");
    }

    #[test]
    fn an_unknown_stored_version_is_nothing_to_pin_against() {
        assert_eq!(pinnable_version(-1), None, "-1 is the not-yet-known sentinel, not a version");
        assert_eq!(pinnable_version(0), Some(0));
        assert_eq!(pinnable_version(7), Some(7));
    }

    #[test]
    fn a_honoured_range_is_already_exactly_the_window() {
        assert_eq!(hydration_window(b"2345", true, 2, 4), b"2345");
    }

    #[test]
    fn an_ignored_range_is_cut_down_to_the_window_locally() {
        assert_eq!(
            hydration_window(b"0123456789", false, 2, 4),
            b"2345",
            "a server that answers 200 sent the whole file, so the window has to be cut here"
        );
    }

    #[test]
    fn a_window_running_past_the_end_stops_at_the_end() {
        assert_eq!(hydration_window(b"0123456789", false, 8, 100), b"89");
        assert_eq!(hydration_window(b"0123456789", false, 10, 4), b"");
        assert_eq!(hydration_window(b"0123456789", false, 99, 4), b"");
    }

    /// The Cloud Filter API refuses a volume root — registering one anyway is
    /// what left Explorer with a provider for the whole drive that never
    /// answers, and every placeholder conversion failing (ADR-0057).
    #[test]
    fn a_volume_root_carries_no_placeholders() {
        use std::path::Path;
        // Every case runs on every platform — the rule is about the *shape* of
        // the path, not about how the host's parser happens to read it.
        for root in [r"D:\", r"C:\", "D:/", "d:", "/", r"\\server\share", r"\\server\share\"] {
            assert!(is_volume_root(Path::new(root)), "{root} is a volume root");
        }
        for inside in [
            r"D:\PrivaSync", r"D:\Backup\Fotos", "C:/Users/user/Sync",
            r"\\server\share\unterordner", "/home/user/sync", "", "   ",
        ] {
            assert!(!is_volume_root(Path::new(inside)), "{inside} is not a volume root");
        }

        assert!(!placeholders_available_for(Path::new(r"D:\")),
            "a drive root falls back to full downloads, like a platform without CF");
        assert_eq!(
            placeholders_available_for(Path::new(r"D:\PrivaSync")),
            supports_placeholders(),
            "a directory inside the volume is limited only by the platform",
        );
    }
}
