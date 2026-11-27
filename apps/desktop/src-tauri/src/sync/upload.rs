//! Sending one file's content to the server: snapshot it (ADR-0077), then
//! delta (ADR-0002), chunked or a single PUT, and settle the outcome.

use std::path::Path;
use std::sync::Mutex;
use std::time::SystemTime;

use tauri::{AppHandle, Emitter};
use tokio::sync::Semaphore;
use tracing::{debug, info, warn};

use super::download::stage_download;
use super::failure::{
    leave_pending_on_auth_failure, materialise_conflict, record_failure,
    settle_upload_conflict_if_identical,
};
use super::retry::{with_retry_throttled, IN_USE_RETRY_SECS, REMOTE_QUOTA_RETRY_SECS};
use super::snapshot::{InUse, SnapshotError};
use super::snapshot_store::SnapshotStore;
use super::transfer_decide::{decide_upload_strategy, is_blocked_by_remote_quota};
use super::{
    should_persist_upload_result, upload_file_chunked, ChunkedUploadRequest, TransferOp,
    TransferRecord, DELTA_MAX_LITERAL_BYTES,
};
use crate::api::{is_auth_failure, AuthedApi};
use crate::cloud_files;
use crate::db::{LocalDb, LocalFile};

/// What every upload of one batch shares — borrowed from the batch's task.
#[derive(Clone, Copy)]
pub(super) struct UploadScope<'a> {
    pub api: &'a AuthedApi,
    pub db: &'a LocalDb,
    pub app: &'a AppHandle,
    pub sem: &'a Semaphore,
    pub transfers: &'a Mutex<Vec<TransferRecord>>,
    pub snapshot_store: &'a SnapshotStore,
    pub root_id: &'a str,
    pub root_name: &'a str,
    pub root_server_id: &'a str,
    pub root_is_mount: bool,
}

/// Upload `file`'s current content and record the outcome on its row. Never
/// fails outward: every result, including errors, ends up in the database.
pub(super) async fn upload_one(
    scope: &UploadScope<'_>,
    file: &LocalFile,
    local_path: &Path,
    announce: &(impl Fn() + Sync),
) {
    let UploadScope {
        api,
        db,
        app,
        sem,
        transfers,
        snapshot_store,
        root_id,
        root_name,
        root_server_id,
        root_is_mount,
    } = *scope;

    let resumable_session = match db.chunked_upload_for(root_server_id, &file.path) {
        Ok(session) => session,
        Err(error) => {
            warn!(
                "cannot read resumable upload session for '{}': {error:#}",
                file.path
            );
            None
        }
    };

    // Reuse a persisted snapshot only after hashing both the current source
    // under the write-denying lock and the cached bytes against the ledger.
    let resume_result = if let Some(session) = resumable_session.as_ref() {
        let snapshot_store = snapshot_store.clone();
        let source_path = local_path.to_path_buf();
        let session = session.clone();
        tokio::task::spawn_blocking(move || {
            snapshot_store.resume(
                &source_path,
                &session.snapshot_id,
                &session.content_hash,
                session.file_size,
                session.mtime,
            )
        })
        .await
        .unwrap_or_else(|error| Err(SnapshotError::Io(std::io::Error::other(error))))
    } else {
        Ok(None)
    };

    let (taken, mut resumable_session) = match resume_result {
        Ok(Some(snapshot)) => (Ok(snapshot), resumable_session),
        Ok(None) => {
            if let Some(session) = resumable_session {
                if let Err(error) = db.forget_chunked_upload(root_server_id, &file.path) {
                    warn!(
                        "cannot forget invalid upload session for '{}': {error:#}",
                        file.path
                    );
                }
                snapshot_store.remove_snapshot(&session.snapshot_id);
            }
            let source_path = local_path.to_path_buf();
            let snapshot_store = snapshot_store.clone();
            let result = tokio::task::spawn_blocking(move || snapshot_store.take(&source_path))
                .await
                .unwrap_or_else(|error| Err(SnapshotError::Io(std::io::Error::other(error))));
            (result, None)
        }
        Err(error) => (Err(error), resumable_session),
    };

    match taken {
        Err(SnapshotError::InUse(reason)) => {
            defer_in_use(db, app, root_id, &file.path, reason);
        }
        Err(error) => {
            let detail = error.to_string();
            let e = anyhow::Error::new(error)
                .context(format!("cannot snapshot '{}': {detail}", file.path));
            record_failure(
                "snapshot",
                "upload",
                &file.path,
                &e,
                file.attempt_count + 1,
                |next_retry| {
                    let _ = db.set_failed(root_id, &file.path, &e.to_string(), next_retry);
                },
                |payload| {
                    app.emit("sync_failure", payload).ok();
                },
            );
        }
        // Changed back to the server's content since
        // `process_uploads` hashed it.
        Ok(mut snapshot) if file.server_hash.as_deref() == Some(snapshot.hash.as_slice()) => {
            if resumable_session.is_some() {
                if let Err(error) = db.forget_chunked_upload(root_server_id, &file.path) {
                    warn!(
                        "cannot forget completed upload session for '{}': {error:#}",
                        file.path
                    );
                }
                snapshot.remove_on_drop();
            }
            info!("skip upload (hash match): {}", file.path);
            let _ = db.set_synced(root_id, &file.path, &snapshot.hash);
            let _ = cloud_files::convert_to_placeholder(local_path, &file.path, true);
        }
        Ok(mut snapshot) => {
            let _ = cloud_files::convert_to_placeholder(local_path, &file.path, false);
            let _ = cloud_files::mark_syncing(local_path);

            let path = file.path.clone();
            // Every read below comes from the snapshot (ADR-0077);
            // `local_path` stays the real file for the conflict path.
            let src = snapshot.path().to_path_buf();
            let known_version = file.server_version;
            let file_size = snapshot.size;
            // Mounts have no chunked-upload endpoints (ADR-0011) — large
            // mount files go single-shot PUT. Chunked-for-mounts is a
            // later optimization, not required for correctness. Delta
            // (ADR-0002) and chunked are independent gates, so a
            // large delta-eligible file tries delta first and falls
            // back to chunked (not plain) if delta declines.
            let strategy = decide_upload_strategy(root_is_mount, known_version, file_size);
            if !strategy.falls_back_to_chunked() && resumable_session.take().is_some() {
                if let Err(error) = db.forget_chunked_upload(root_server_id, &path) {
                    warn!(
                        "cannot forget unused upload session for '{}': {error:#}",
                        path
                    );
                }
            }

            // The matcher streams the file in bounded memory, so delta
            // also covers chunk-sized files; on any miss — server
            // declines, payload too large, plan fails, version moved —
            // it yields None and we fall through to strategy's fallback.
            let delta_outcome = if strategy.tries_delta() {
                // Single-shot, no in-cycle retry loop here (delta falls
                // back to chunked/plain on any miss) — still gate on the
                // upload semaphore so it counts against the same
                // concurrency budget as every other network attempt.
                let _permit = sem.acquire().await.expect("semaphore closed");
                try_delta_upload(
                    api,
                    root_server_id,
                    &path,
                    &src,
                    snapshot.mtime,
                    known_version,
                )
                .await
            } else {
                None
            };

            let completed_without_chunked = delta_outcome.is_some();
            let outcome_result = if let Some(out) = delta_outcome {
                Ok(out)
            } else if strategy.falls_back_to_chunked() {
                // Cancellation can drop this future after the session is
                // persisted but before the normal outcome handling runs.
                snapshot.preserve_for_resume();
                // Large file — chunked upload. Each network call inside
                // (init/status/per-chunk/complete) gates on `sem` itself,
                // releasing the slot during any retry backoff.
                //
                // Announced here rather than behind a permit like the
                // other branches: the permits live inside the chunk
                // loop, and a multi-gigabyte file that names itself
                // only once its first chunk is through would leave the
                // label empty for the longest transfers of all.
                announce();
                let mtime = snapshot.mtime;
                // Resume the session a previous attempt opened for
                // this exact file. Without it every retry re-sends
                // the whole file and abandons the old chunks on the
                // server until the session expires.
                let existing_upload_id = resumable_session
                    .as_ref()
                    .map(|session| session.upload_id.clone());
                upload_file_chunked(
                    api,
                    sem,
                    ChunkedUploadRequest {
                        root_server_id,
                        server_path: &path,
                        local_path: &src,
                        mtime,
                        known_server_version: known_version,
                        existing_upload_id,
                        snapshot_id: &snapshot.id,
                        sessions: db,
                        content_hash: &snapshot.hash,
                    },
                )
                .await
            } else {
                let (p, mtime) = (&path, snapshot.mtime);
                let src = src.as_path();
                with_retry_throttled(sem, &path, || {
                    announce();
                    api.call(|snap| async move {
                        snap.upload_file(
                            root_server_id,
                            p,
                            src,
                            mtime,
                            known_version,
                            root_is_mount,
                        )
                        .await
                    })
                })
                .await
            };

            if completed_without_chunked && resumable_session.is_some() {
                if let Err(error) = db.forget_chunked_upload(root_server_id, &path) {
                    warn!(
                        "cannot forget superseded upload session for '{}': {error:#}",
                        path
                    );
                }
            }

            if outcome_result.is_err() && strategy.falls_back_to_chunked() {
                let keep_snapshot = match db.chunked_upload_for(root_server_id, &path) {
                    Ok(session) => {
                        session.is_some_and(|session| session.snapshot_id == snapshot.id)
                    }
                    Err(error) => {
                        warn!("cannot check upload session for '{}': {error:#}", path);
                        true
                    }
                };
                if !keep_snapshot {
                    snapshot.remove_on_drop();
                }
            } else {
                snapshot.remove_on_drop();
            }

            match outcome_result {
                Ok(crate::api::UploadOutcome::Uploaded(resp)) => {
                    // Pre-commit-recheck: if the watcher staged a delete
                    // (e.g. rename-away) while this upload was in flight,
                    // do NOT overwrite that intent with `synced` — the
                    // next cycle's delete pass will reconcile the server.
                    if !should_persist_upload_result(db, root_id, &file.path) {
                        warn!(
                            "upload of '{}' completed but local intent flipped to \
                             pending_delete mid-flight — leaving delete in place \
                             (server will be cleaned up next cycle)",
                            file.path,
                        );
                    } else {
                        let hash = hex::decode(&resp.content_hash).unwrap_or_default();
                        let _ = db.set_synced_with_version(
                            root_id,
                            &file.path,
                            &hash,
                            resp.server_version,
                        );
                        let _ = cloud_files::mark_in_sync(local_path);
                        if let Ok(mut v) = transfers.lock() {
                            v.push(TransferRecord {
                                op: TransferOp::Uploaded,
                                path: file.path.clone(),
                                root_name: root_name.to_string(),
                            });
                        }
                        info!(
                            "uploaded: {} (server_version={})",
                            file.path, resp.server_version
                        );
                    }
                }
                // Same bytes on both sides: only our server_version was
                // stale, so this is no conflict.
                Ok(crate::api::UploadOutcome::Conflict {
                    server_version,
                    ref server_hash,
                    ..
                }) if settle_upload_conflict_if_identical(
                    db,
                    root_id,
                    &path,
                    local_path,
                    server_version,
                    server_hash,
                ) =>
                {
                    let _ = cloud_files::mark_in_sync(local_path);
                    info!(
                        "upload conflict on '{}' had identical content — \
                           adopted server_version={}",
                        file.path, server_version
                    );
                }
                Ok(crate::api::UploadOutcome::Conflict {
                    server_version,
                    server_hash,
                    conflict_path: server_conflict_path,
                }) => {
                    // Server has a newer version — create a conflict copy of
                    // the local file and download the server's version.
                    warn!(
                        "ETag conflict on upload '{}': server_version={}, \
                           server_hash={}",
                        file.path, server_version, server_hash
                    );

                    let p = &path;
                    let winner = api
                        .call(|snap| async move {
                            stage_download(&snap, root_server_id, p, root_is_mount, local_path)
                                .await
                        })
                        .await;
                    match winner {
                        Ok(winner) => {
                            if let Err(e) = materialise_conflict(
                                db,
                                root_id,
                                &path,
                                local_path,
                                winner,
                                server_conflict_path.as_deref(),
                                |hash, _size| {
                                    db.set_synced_with_version(root_id, &path, hash, server_version)
                                },
                                |payload| {
                                    app.emit("sync_conflict", payload).ok();
                                },
                            ) {
                                warn!("materialise_conflict failed for '{}': {e}", path);
                            }
                        }
                        Err(e) => warn!("conflict download failed for '{}': {e}", path),
                    }
                }
                // Giver's quota is full (mount upload only, ADR-0011): a
                // steady poll instead of the exponential pending_failed
                // backoff — the wait is on the giver freeing space, which
                // can happen any time, not on the client fixing something.
                Err(e) if is_blocked_by_remote_quota(root_is_mount, &e) => {
                    let next_retry = chrono::Utc::now().timestamp() + REMOTE_QUOTA_RETRY_SECS;
                    let _ = db.set_blocked_remote_quota(root_id, &file.path, next_retry);
                    warn!("upload blocked by remote quota for '{}': retry in {REMOTE_QUOTA_RETRY_SECS}s", file.path);
                    app.emit(
                        "sync_failure",
                        serde_json::json!({
                            "kind":  "upload",
                            "path":  file.path,
                            "error": e.to_string(),
                        }),
                    )
                    .ok();
                }
                Err(e) if is_auth_failure(&e) => leave_pending_on_auth_failure(&file.path, &e),
                Err(e) => {
                    let next_attempt = file.attempt_count + 1;
                    record_failure(
                        "upload",
                        "upload",
                        &file.path,
                        &e,
                        next_attempt,
                        |next_retry| {
                            let _ = db.set_failed(root_id, &file.path, &e.to_string(), next_retry);
                        },
                        |payload| {
                            app.emit("sync_failure", payload).ok();
                        },
                    );
                }
            }
        }
    }
}

/// Park an upload whose file another program has open (ADR-0077): a steady
/// poll, not a failure. The problems list is refreshed only when the row
/// enters the state, so a database open all day does not emit every minute.
pub(super) fn defer_in_use(
    db: &LocalDb,
    app: &AppHandle,
    root_id: &str,
    path: &str,
    reason: InUse,
) {
    let next_retry = chrono::Utc::now().timestamp() + IN_USE_RETRY_SECS;
    debug!("upload deferred, '{path}' is in use ({reason:?}): retry in {IN_USE_RETRY_SECS}s");
    match db.set_in_use(root_id, path, reason.message(), next_retry) {
        Ok(true) => {
            info!("upload of '{path}' waits until the file is closed ({reason:?})");
            app.emit(
                "sync_failure",
                serde_json::json!({
                    "kind":  "upload",
                    "path":  path,
                    "error": reason.message(),
                }),
            )
            .ok();
        }
        Ok(false) => {}
        Err(e) => warn!("set_in_use failed for '{path}': {e}"),
    }
}

/// Attempt a delta upload: fetch the server's block signatures, diff the local
/// file against them, and send only the changed blocks (ADR-0002).
///
/// Returns `Some(outcome)` only when the server gave a definitive answer —
/// `Uploaded` on success or `Conflict` on a version race (handled exactly like a
/// full-upload conflict). `None` means "delta not usable here" (server declined,
/// I/O error, or the apply failed) and the caller must fall back to a normal
/// full or chunked upload. Correctness never depends on delta: the server
/// verifies the reconstructed hash before committing.
async fn try_delta_upload(
    api: &AuthedApi,
    root_server_id: &str,
    path: &str,
    local_path: &Path,
    mtime: SystemTime,
    known_version: i64,
) -> Option<crate::api::UploadOutcome> {
    use crate::api::delta::DeltaAvailability;
    use crate::sync::delta;

    let requested = api
        .call(|snap| async move { snap.delta_request(root_server_id, path).await })
        .await;
    let (block_size, sigs) = match requested {
        Ok(DeltaAvailability::Available {
            block_size,
            signatures,
            ..
        }) => (block_size, signatures),
        Ok(DeltaAvailability::NotAvailable) => return None,
        Err(e) => {
            warn!("delta request failed for '{path}': {e} — falling back to full upload");
            return None;
        }
    };

    // Stream the local file through the matcher (bounded memory) rather than
    // loading it whole, so delta works for arbitrarily large files. If the
    // changed payload would rival a chunked upload anyway, the matcher bails and
    // we fall back. The hash comes out of the same pass.
    let file = match std::fs::File::open(local_path) {
        Ok(f) => f,
        Err(e) => {
            warn!("delta open failed for '{path}': {e} — falling back to full upload");
            return None;
        }
    };
    let (plan, expected_hash) = match delta::compute_plan_reader(
        std::io::BufReader::new(file),
        &sigs,
        block_size,
        DELTA_MAX_LITERAL_BYTES,
    ) {
        Ok(delta::StreamingPlan::Delta { ops, hash }) => (ops, hash),
        Ok(delta::StreamingPlan::NotWorthwhile) => return None,
        Err(e) => {
            warn!("delta read failed for '{path}': {e} — falling back to full upload");
            return None;
        }
    };

    // If-Match carries the client's *known* server version (not the just-fetched
    // one) so a server that moved on under us loses the optimistic lock → 409 →
    // the same conflict path a full upload would take.
    let (plan, expected_hash) = (&plan, expected_hash.as_bytes());
    let applied = api
        .call(|snap| async move {
            snap.delta_apply(
                root_server_id,
                path,
                plan,
                expected_hash,
                mtime,
                known_version,
            )
            .await
        })
        .await;
    match applied {
        Ok(outcome) => Some(outcome),
        Err(e) => {
            warn!("delta apply failed for '{path}': {e} — falling back to full upload");
            None
        }
    }
}
