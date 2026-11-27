//! Status helpers that flip a placeholder's IN_SYNC flag so Explorer
//! shows the correct overlay icon, plus the dehydrated-check used by
//! the eager-hydration sweep.

use std::path::Path;

use anyhow::{Context, Result};

use windows::core::PCWSTR;
use windows::Win32::Storage::CloudFilters::{
    CfSetInSyncState, CfSetPinState,
    CF_IN_SYNC_STATE_IN_SYNC, CF_IN_SYNC_STATE_NOT_IN_SYNC,
    CF_PIN_STATE_PINNED, CF_PIN_STATE_UNPINNED,
    CF_SET_IN_SYNC_FLAG_NONE, CF_SET_PIN_FLAG_NONE,
};
use windows::Win32::Storage::FileSystem::{
    GetFileAttributesW, FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS,
};

use super::{to_hstring, with_handle};

// CfSetInSyncState (not CfUpdatePlaceholder) is the canonical API for
// flipping the in-sync bit: it also works on the sync-root directory
// itself, where CfUpdatePlaceholder fails with
// ERROR_CLOUD_FILE_ACCESS_DENIED.

pub fn mark_in_sync(path: &Path) -> Result<()> {
    with_handle(path, |h| unsafe {
        CfSetInSyncState(h, CF_IN_SYNC_STATE_IN_SYNC, CF_SET_IN_SYNC_FLAG_NONE, None)
            .context("CfSetInSyncState(IN_SYNC)")
    })
}

pub fn mark_syncing(path: &Path) -> Result<()> {
    // Clear the in-sync bit so Explorer shows the sync-pending overlay.
    with_handle(path, |h| unsafe {
        CfSetInSyncState(h, CF_IN_SYNC_STATE_NOT_IN_SYNC, CF_SET_IN_SYNC_FLAG_NONE, None)
            .context("CfSetInSyncState(NOT_IN_SYNC)")
    })
}

/// Pin a placeholder so Windows never dehydrates it. Used for excluded files
///: they have no server copy, so a dehydrated excluded file could never
/// be recalled — pinning keeps the content permanently on disk.
pub fn set_pinned(path: &Path) -> Result<()> {
    with_handle(path, |h| unsafe {
        CfSetPinState(h, CF_PIN_STATE_PINNED, CF_SET_PIN_FLAG_NONE, None)
            .context("CfSetPinState(PINNED)")
    })
}

/// Unpin a placeholder so Windows is allowed to dehydrate it again — the
/// inverse of [`set_pinned`], used by the "free up space" context-menu verb.
pub fn set_unpinned(path: &Path) -> Result<()> {
    with_handle(path, |h| unsafe {
        CfSetPinState(h, CF_PIN_STATE_UNPINNED, CF_SET_PIN_FLAG_NONE, None)
            .context("CfSetPinState(UNPINNED)")
    })
}

pub fn is_dehydrated(path: &Path) -> bool {
    let Ok(path_w) = to_hstring(path) else { return false };
    let attrs = unsafe { GetFileAttributesW(PCWSTR(path_w.as_ptr())) };
    attrs != u32::MAX && (attrs & FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS.0) != 0
}
