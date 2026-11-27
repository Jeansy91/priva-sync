//! Windows Cloud Files API integration.
//!
//! Implements on-demand placeholder sync using the CF API (Windows 10 1709+).
//! Files appear instantly in Explorer with the cloud icon and are only
//! downloaded when the user (or an app) first opens them.

#![allow(non_snake_case)]

use std::path::Path;
use std::sync::Arc;

use anyhow::{anyhow, Context, Result};
use tracing::warn;

use windows::core::{HSTRING, PCWSTR};
use windows::Win32::Foundation::{CloseHandle, HANDLE};
use windows::Win32::Storage::CloudFilters::{
    CfDisconnectSyncRoot, CfUpdateSyncProviderStatus,
    CF_CONNECTION_KEY, CF_PROVIDER_STATUS_TERMINATED,
};
use windows::Win32::Storage::FileSystem::{
    CreateFileW, FILE_FLAG_BACKUP_SEMANTICS, FILE_SHARE_DELETE,
    FILE_SHARE_READ, FILE_SHARE_WRITE, OPEN_EXISTING,
    FILE_WRITE_ATTRIBUTES,
};

use super::{ConvertOutcome, HydrationCtx, PlaceholderInfo, PlaceholderOutcome};

mod branding;
mod registration;
mod connect;
mod namespace;
mod placeholder;
mod status;

/// True for the two HRESULTs the CF filter driver answers with while it is
/// still attaching to a freshly registered sync root.
///
/// `0x8007017C` is `ERROR_CLOUD_FILE_INVALID_REQUEST` (380), *not*
/// `ERROR_CLOUD_FILE_NOT_UNDER_SYNC_ROOT` (390 → `0x80070186`) — the two are
/// easy to confuse and both show up in this window, so both are matched here
/// rather than spelled out as hex strings at each call site.
fn is_filter_not_ready(code: windows::core::HRESULT) -> bool {
    use windows::Win32::Foundation::{
        ERROR_CLOUD_FILE_INVALID_REQUEST, ERROR_CLOUD_FILE_NOT_UNDER_SYNC_ROOT,
    };
    code == windows::core::HRESULT::from_win32(ERROR_CLOUD_FILE_INVALID_REQUEST.0)
        || code == windows::core::HRESULT::from_win32(ERROR_CLOUD_FILE_NOT_UNDER_SYNC_ROOT.0)
}

pub use registration::{register, unregister};
pub use namespace::{reconcile as reconcile_explorer_namespace, sync as sync_explorer_namespace};
pub use connect::connect;
pub use placeholder::{ensure_placeholder, convert_to_placeholder, hydrate_file, dehydrate_file, mark_excluded};
pub use status::{mark_in_sync, mark_syncing, is_dehydrated, set_pinned, set_unpinned};

// ─── Connection handle ────────────────────────────────────────────────────────

pub struct Connection {
    key:  CF_CONNECTION_KEY,
    _ctx: Arc<HydrationCtx>,
}

impl Connection {
    #[doc(hidden)]
    pub(super) fn __new(key: CF_CONNECTION_KEY, ctx: Arc<HydrationCtx>) -> Self {
        Self { key, _ctx: ctx }
    }
}

impl Drop for Connection {
    fn drop(&mut self) {
        unsafe {
            // Tell Explorer the provider is going away before we drop the
            // connection — without this the cloud icon lingers as if the
            // provider were still active.
            let _ = CfUpdateSyncProviderStatus(self.key, CF_PROVIDER_STATUS_TERMINATED);
            if let Err(e) = CfDisconnectSyncRoot(self.key) {
                warn!("CfDisconnectSyncRoot failed: {e}");
            }
        }
    }
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

pub(super) fn to_hstring(path: &Path) -> Result<HSTRING> {
    let s = path.to_str().ok_or_else(|| anyhow!("non-UTF-8 path"))?;
    Ok(HSTRING::from(s))
}

pub(super) fn unix_to_filetime(unix_secs: i64) -> i64 {
    // FILE_BASIC_INFO uses LARGE_INTEGER (i64) in 100-nanosecond intervals
    // from January 1, 1601.
    let ft = (unix_secs.max(0) as u64) * 10_000_000 + 116_444_736_000_000_000u64;
    ft as i64
}

pub(super) fn with_handle<F>(path: &Path, f: F) -> Result<()>
where
    F: FnOnce(HANDLE) -> Result<()>,
{
    let path_w = to_hstring(path)?;
    let handle = unsafe {
        CreateFileW(
            PCWSTR(path_w.as_ptr()),
            FILE_WRITE_ATTRIBUTES.0,
            FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
            None,
            OPEN_EXISTING,
            FILE_FLAG_BACKUP_SEMANTICS,
            None,
        )
        .context("CreateFileW")?
    };
    let result = f(handle);
    unsafe { let _ = CloseHandle(handle); }
    result
}
