//! CF callback registration plus the FETCH_DATA / CANCEL handlers.
//! `connect` returns a [`Connection`] whose Drop tears the registration
//! down again so the lifetime of the provider matches the watcher loop.

use std::ffi::c_void;
use std::path::Path;
use std::sync::Arc;

use anyhow::{Context, Result};
use tracing::{error, info, warn};

use windows::core::PCWSTR;
use windows::Win32::Foundation::NTSTATUS;
use windows::Win32::Storage::CloudFilters::{
    CfConnectSyncRoot, CfExecute, CfUpdateSyncProviderStatus,
    CF_CALLBACK_REGISTRATION,
    CF_CALLBACK_TYPE_CANCEL_FETCH_DATA,
    CF_CALLBACK_TYPE_FETCH_DATA, CF_CALLBACK_TYPE_NONE,
    CF_CONNECT_FLAG_NONE, CF_CONNECTION_KEY,
    CF_OPERATION_INFO, CF_OPERATION_PARAMETERS,
    CF_OPERATION_TYPE_TRANSFER_DATA,
    CF_OPERATION_TRANSFER_DATA_FLAG_NONE,
    CF_PROVIDER_STATUS_IDLE,
};

use super::{Connection, HydrationCtx, to_hstring};
use crate::cloud_files;

pub fn connect(local_path: &Path, ctx: Arc<HydrationCtx>) -> Result<Connection> {
    let path_w  = to_hstring(local_path)?;
    let ctx_ptr = Arc::as_ptr(&ctx) as *const c_void;

    let callbacks: [CF_CALLBACK_REGISTRATION; 3] = [
        CF_CALLBACK_REGISTRATION {
            Type:     CF_CALLBACK_TYPE_FETCH_DATA,
            Callback: Some(fetch_data_callback),
        },
        CF_CALLBACK_REGISTRATION {
            Type:     CF_CALLBACK_TYPE_CANCEL_FETCH_DATA,
            Callback: Some(cancel_fetch_callback),
        },
        CF_CALLBACK_REGISTRATION {
            Type:     CF_CALLBACK_TYPE_NONE,
            Callback: None,
        },
    ];

    let key = unsafe {
        CfConnectSyncRoot(
            PCWSTR(path_w.as_ptr()),
            callbacks.as_ptr(),
            Some(ctx_ptr),
            CF_CONNECT_FLAG_NONE,
        )
        .context("CfConnectSyncRoot")?
    };

    // Switch the provider to IDLE — without this Explorer treats the sync
    // root as connected-but-not-active, suppresses the cloud-folder icon on
    // the root, and leaves the Status column blank for the root entry.
    if let Err(e) = unsafe { CfUpdateSyncProviderStatus(key, CF_PROVIDER_STATUS_IDLE) } {
        warn!("CfUpdateSyncProviderStatus(IDLE) failed for '{}': {e}", local_path.display());
    }

    // NB: the sync-root directory itself is marked in-sync at the end of a
    // settled sync cycle (see SyncEngine::mark_root_in_sync), not here: the
    // CF filter clears a directory's in-sync bit on every descendant change,
    // so a mark set at connect time is wiped by the placeholder-init and
    // transfer passes that follow.

    info!("connected cloud callbacks: {}", local_path.display());
    Ok(Connection::__new(key, ctx))
}

// ─── FETCH_DATA callback ──────────────────────────────────────────────────────

unsafe extern "system" fn fetch_data_callback(
    info:   *const windows::Win32::Storage::CloudFilters::CF_CALLBACK_INFO,
    params: *const windows::Win32::Storage::CloudFilters::CF_CALLBACK_PARAMETERS,
) {
    let info   = &*info;
    let params = &*params;

    let ctx = &*(info.CallbackContext as *const HydrationCtx);

    // File identity bytes hold the server-relative path we stored at
    // placeholder creation time.
    let server_path = if info.FileIdentityLength > 0 {
        std::str::from_utf8(std::slice::from_raw_parts(
            info.FileIdentity as *const u8,
            info.FileIdentityLength as usize,
        ))
        .unwrap_or("")
        .to_string()
    } else {
        PCWSTR(info.NormalizedPath.0)
            .to_string()
            .unwrap_or_default()
            .replace('\\', "/")
    };

    let required_offset = params.Anonymous.FetchData.RequiredFileOffset;
    let required_length = params.Anonymous.FetchData.RequiredLength;
    let connection_key  = info.ConnectionKey;
    let transfer_key    = info.TransferKey;
    let request_key     = info.RequestKey;

    // Read the placeholder's row before the transfer, not after: this takes
    // the SQLite mutex, and doing it afterwards would queue it behind whatever
    // else the process is transferring.
    let placeholder_version = ctx.db
        .get_file_by_server_path(&ctx.server_root_id, &server_path)
        .ok()
        .flatten()
        .and_then(|rec| cloud_files::pinnable_version(rec.server_version));

    // Ask for the window Explorer touched, not the whole file. Downloading the
    // whole file per FETCH_DATA meant a sequential read of a large file pulled
    // it across the wire once per window.
    //
    // Safe to block_on here because Windows thread-pool threads are not Tokio
    // executor threads.
    let result = ctx.rt.block_on(async {
        let range = Some((required_offset.max(0) as u64, required_length.max(0) as u64));
        let stream = ctx.api
            .call_client(|c| {
                let server_path = &server_path;
                async move {
                    c.read().await
                        .download_stream(&ctx.server_root_id, server_path, ctx.is_mount, range)
                        .await
                }
            })
            .await?;
        let is_partial     = stream.is_partial;
        let server_version = stream.server_version;
        // Bounded by the requested window, so this stays in memory: it has to,
        // CfExecute takes a slice.
        let data = stream.collect().await?;
        anyhow::Ok((data, is_partial, server_version))
    });

    match result {
        Ok((data, is_partial, server_version)) => {
            if !cloud_files::hydration_version_matches(placeholder_version, server_version) {
                warn!(
                    "hydration of '{server_path}' aborted: placeholder is version {placeholder_version:?}, server answered {server_version:?}"
                );
                // STATUS_CLOUD_FILE_NOT_IN_SYNC = 0xC000_0392. Windows fails
                // the read; the next reconcile cycle refreshes the placeholder
                // and a retry then hydrates from one consistent version.
                do_transfer(connection_key, transfer_key, request_key,
                            required_offset, &[],
                            NTSTATUS(0xC000_0392_u32 as i32));
                return;
            }

            let window = cloud_files::hydration_window(
                &data, is_partial, required_offset, required_length,
            );
            do_transfer(connection_key, transfer_key, request_key,
                        required_offset, window, NTSTATUS(0));

            // Deliberately no DB write here. The placeholder's row already
            // carries the server's hash and version from the moment it was
            // created (see `SyncEngine::apply_server_file_entry`), so there is
            // nothing to learn from a window — and the write that used to
            // happen here was unconditional, which reset an open `conflict`
            // row to `synced` and orphaned its `.conflict.` copy.
        }
        Err(e) => {
            error!("hydration failed for '{server_path}': {e}");
            // STATUS_CLOUD_FILE_NETWORK_UNAVAILABLE = 0xC000_00B5
            do_transfer(connection_key, transfer_key, request_key,
                        required_offset, &[],
                        NTSTATUS(0xC000_00B5_u32 as i32));
        }
    }
}

unsafe extern "system" fn cancel_fetch_callback(
    _info:   *const windows::Win32::Storage::CloudFilters::CF_CALLBACK_INFO,
    _params: *const windows::Win32::Storage::CloudFilters::CF_CALLBACK_PARAMETERS,
) {}

// ─── CfExecute helper ─────────────────────────────────────────────────────────

fn do_transfer(
    connection_key: CF_CONNECTION_KEY,
    transfer_key:   i64,
    request_key:    i64,
    offset:         i64,
    data:           &[u8],
    status:         NTSTATUS,
) {
    let op_info = CF_OPERATION_INFO {
        StructSize:        std::mem::size_of::<CF_OPERATION_INFO>() as u32,
        Type:              CF_OPERATION_TYPE_TRANSFER_DATA,
        ConnectionKey:     connection_key,
        TransferKey:       transfer_key,
        CorrelationVector: std::ptr::null(),
        SyncStatus:        std::ptr::null(),
        RequestKey:        request_key,
    };

    // Build the operation parameters using zeroed memory to handle any
    // padding or version differences safely.
    let mut op_params: CF_OPERATION_PARAMETERS = unsafe { std::mem::zeroed() };
    op_params.ParamSize = std::mem::size_of::<CF_OPERATION_PARAMETERS>() as u32;
    unsafe {
        let td = &mut op_params.Anonymous.TransferData;
        td.Flags            = CF_OPERATION_TRANSFER_DATA_FLAG_NONE;
        td.CompletionStatus = status;
        td.Offset           = offset;
        td.Buffer           = data.as_ptr() as *const c_void;
        td.Length           = data.len() as i64;
    }

    unsafe {
        if let Err(e) = CfExecute(&op_info, &mut op_params) {
            warn!("CfExecute failed: {e}");
        }
    }
}
