//! Upload path: walk pending rows, push them to the server in parallel
//! batches, surface progress + transfer records, and handle bulk-delete
//! confirmation gates.
//!
//! Everything that turns local-disk state into server-side state lives here:
//! single-shot PUT, chunked uploads, deletes, and the upload-side `with_retry`
//! invocations. Read-only and paused roots are short-circuited at the top.

use anyhow::Result;
use std::path::Path;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use tauri::Emitter;
use tracing::{debug, info, warn};

use super::engine_placeholders::record_dirty_dirs;
use super::failure::{leave_pending_on_auth_failure, record_failure};
use super::retry::with_retry_throttled;
use super::snapshot;
use super::transfer_decide::{
    decide_upload_action, probe_local, upload_is_settled, LocalPresence, UploadAction,
};
use super::upload::{defer_in_use, upload_one, UploadScope};
use super::{
    delete_reached_server, hash_file, SyncEngine, TransferOp, TransferRecord,
    BULK_DELETE_ABSOLUTE_THRESHOLD, BULK_DELETE_MIN_FILES, BULK_DELETE_THRESHOLD,
    PRIORITY_YIELD_EVERY, TOTAL_REFRESH_INTERVAL, UPLOAD_BATCH_LIMIT, UPLOAD_CONCURRENCY_DEFAULT,
    UPLOAD_RUN_CHUNK,
};
use crate::api::is_auth_failure;
use crate::cloud_files;
use crate::db::SyncRootEntry;

impl SyncEngine {
    pub(super) async fn process_uploads(&self, root: &SyncRootEntry) -> Result<()> {
        // Read-only roots never push anything to the server.
        if root.read_only {
            return Ok(());
        }

        // Join disappeared rows against freshly staged files (ADR-0063)
        // immediately before the batch below is snapshotted -- not earlier
        // in sync_cycle. The detached walk (ADR-0056) keeps staging rows
        // concurrently with this cycle, so resolving any later than this
        // would still let a walk-staged rename target slip into a batch and
        // upload as a plain new file before any resolve pass saw it.
        if let Err(e) = self.resolve_disappeared(root).await {
            warn!("resolve_disappeared failed for '{}': {e}", root.name);
        }

        // One batch per cycle, not the whole queue (ADR-0056): during a first
        // sync the detached walk keeps staging rows while these upload, and
        // reading the full pending set each cycle would grow with the root.
        let pending = self.db.list_pending_batch(&root.id, UPLOAD_BATCH_LIMIT)?;
        if pending.is_empty() {
            // The queue really is empty — this drain is over. Reset the
            // cross-batch tally and clear the UI's progress row; the "done"
            // signal belongs here rather than at the end of every batch.
            if self
                .upload_progress
                .lock()
                .unwrap()
                .remove(&root.id)
                .is_some()
            {
                self.emit_progress_done(&root.id);
            }
            return Ok(());
        }
        // Both mean the same thing to the caller — there is more queue behind
        // this batch, so ask for another cycle rather than waiting for the
        // poll tick. A full batch says so by its size; a preempted one by
        // having stopped early.
        let mut more_waiting = pending.len() == UPLOAD_BATCH_LIMIT;

        // The lane as it stood when this batch was read (ADR-0059). The batch
        // already carries whatever was in it; what the checks below watch for
        // is an *arrival* since — a change the user made while this batch was
        // running. Counting arrivals rather than asking "is the lane
        // non-empty" is what keeps a deferred, still-growing file from making
        // every batch preempt itself.
        let arrivals_at_start = self.lane_arrivals.load(Ordering::Relaxed);

        // Re-evaluate each pending file right before uploading:
        // hash the current local content and compare against the last known
        // server hash.  This catches cases where:
        //  - the file was reverted to its server version between staging and now
        //  - download_file wrote the file and the watcher re-staged it before
        //    the DB was updated
        let mut to_upload: Vec<crate::db::LocalFile> = Vec::new();
        for (examined, file) in pending.iter().enumerate() {
            // Cut the batch short once a live change has entered the lane
            // since we started (ADR-0059) — what is collected so far still
            // goes out, and the nudge at the end of this function brings up
            // the next cycle, which reads the lane first. Everything left in
            // `pending` simply stays pending; the batch is a window on the
            // queue, not a claim on it.
            if examined > 0
                && examined % PRIORITY_YIELD_EVERY == 0
                && self.lane_arrived_since(arrivals_at_start)
            {
                debug!(
                    "upload batch for '{}' preempted after {examined} rows — \
                     live change waiting in the priority lane",
                    root.name,
                );
                more_waiting = true;
                break;
            }

            let local_path = Path::new(&root.local_path).join(file.path.trim_start_matches('/'));

            if file.sync_status == "pending_delete" {
                to_upload.push(file.clone());
                continue;
            }

            match probe_local(&local_path) {
                LocalPresence::Gone => {
                    // Created and removed again before this cycle reached it —
                    // the server copy goes too.
                    to_upload.push(file.clone());
                    continue;
                }
                LocalPresence::Unreadable(e) => {
                    // Not a deletion: the filesystem simply would not answer.
                    // Surface it and retry rather than removing the file
                    // server-side on the strength of an unreadable stat.
                    let err = anyhow::Error::new(e).context(format!("cannot stat '{}'", file.path));
                    record_failure(
                        "stat",
                        "upload",
                        &file.path,
                        &err,
                        file.attempt_count + 1,
                        |next_retry| {
                            let _ = self.db.set_failed(
                                &root.id,
                                &file.path,
                                &err.to_string(),
                                next_retry,
                            );
                        },
                        |payload| {
                            self.app.emit("sync_failure", payload).ok();
                        },
                    );
                    continue;
                }
                LocalPresence::Present { size, mtime } => {
                    // Still being written? Then this is a fragment, not the
                    // file the user copied in. Refresh the row's size/mtime so
                    // the next cycle compares against something current — that
                    // also carries the settle forward when the watcher event
                    // for the final write never arrives — and leave it pending.
                    // Deliberately before `hash_file`: re-reading a growing
                    // multi-gigabyte file every cycle is the expensive half.
                    if !file.is_dir && !upload_is_settled(file.size, file.mtime, size, mtime) {
                        debug!("upload deferred, '{}' still being written", file.path);
                        let _ = self
                            .db
                            .set_staged_metadata(&root.id, &file.path, size, mtime);
                        continue;
                    }
                }
            }

            // is_dir entries have no content to hash — forward them as-is.
            // run_uploads has a dedicated branch (create_directory instead
            // of upload_file). Otherwise File::open on a directory fails
            // with "permission denied".
            if file.is_dir {
                to_upload.push(file.clone());
                continue;
            }

            // Cheap before the full read below: an open database would
            // otherwise be hashed in full every cycle only for the snapshot
            // to refuse it (ADR-0077).
            if let Some(reason) = snapshot::check_in_use(&local_path) {
                defer_in_use(&self.db, &self.app, &root.id, &file.path, reason);
                continue;
            }

            match hash_file(&local_path) {
                Ok(current_hash) => {
                    // server_hash is what the server last acknowledged
                    let already_synced = file
                        .server_hash
                        .as_deref()
                        .map(|sh| sh == current_hash.as_slice())
                        .unwrap_or(false);

                    if already_synced {
                        if let Err(error) = self.snapshot_store.discard_session(
                            &self.db,
                            &root.server_root_id,
                            &file.path,
                        ) {
                            warn!("cannot discard upload session for '{}': {error:#}", file.path);
                        }
                        // Local file is identical to server — just fix the status.
                        info!("skip upload (hash match): {}", file.path);
                        let _ = self.db.set_synced(&root.id, &file.path, &current_hash);
                        // Convert to an in-sync placeholder so the Status column
                        // shows "Available on this device" instead of no info.
                        let _ = cloud_files::convert_to_placeholder(&local_path, &file.path, true);
                    } else {
                        // Carry the hash that was just computed, not the one
                        // the database happens to hold: the upload states it
                        // to the server, which has no other way to verify what
                        // it received.
                        let mut queued = file.clone();
                        queued.content_hash = current_hash;
                        to_upload.push(queued);
                    }
                }
                Err(e) => {
                    warn!("cannot hash '{}', skipping: {e}", file.path);
                }
            }
        }

        if to_upload.is_empty() {
            // A full batch that filtered down to nothing (everything in it
            // still being written, say) still means there is more queue behind
            // it — ask for the next cycle rather than stalling the drain until
            // the poll tick.
            if more_waiting {
                self.sync_now.notify_one();
            }
            return Ok(());
        }

        // ── Bulk-delete guard ─────────────────────────────────────────────────
        // If >30% of tracked files are staged for deletion, or an absolute
        // count past BULK_DELETE_ABSOLUTE_THRESHOLD is reached (and the root
        // has at least BULK_DELETE_MIN_FILES tracked), this is likely an
        // accident (e.g. the user accidentally cleared a folder). Pause all
        // deletes and ask for confirmation before proceeding. See ADR-0064
        // for why the ratio alone isn't enough on a very large root.
        //
        // Both numbers come from the database, never from `to_upload`: that is
        // one capped batch of the queue, so a wave of 400k deletions would show
        // up as UPLOAD_BATCH_LIMIT rows against the whole root and never reach
        // the threshold — the guard would delete the root in quiet instalments.
        let delete_count = self.db.count_pending_deletes(&root.id).unwrap_or(0);

        if delete_count >= BULK_DELETE_MIN_FILES {
            let total_tracked = self.db.count_tracked_files(&root.id).unwrap_or(0);

            if bulk_delete_should_trigger(delete_count, total_tracked) {
                // Check if the user already confirmed this wave.
                let confirmed = self
                    .db
                    .take_bulk_delete_confirmed(&root.id)
                    .unwrap_or(false);

                if !confirmed {
                    warn!(
                        "bulk-delete guard triggered for '{}': {} of {} files \
                         staged for deletion — auto-pausing root, awaiting user decision",
                        root.name, delete_count, total_tracked,
                    );
                    // Auto-pause so the next cycle skips entirely. Without
                    // this the guard would re-fire every cycle as long as
                    // pending_delete rows exist — endless dialog loop.
                    if let Err(e) = self.db.set_sync_root_paused(&root.id, true) {
                        warn!("failed to auto-pause '{}': {e}", root.name);
                    }
                    self.app
                        .emit(
                            "sync:bulk_delete_warning",
                            serde_json::json!({
                                "root_id":      root.id,
                                "root_name":    root.name,
                                "delete_count": delete_count,
                                "total_count":  total_tracked,
                                "direction":    "outgoing",
                            }),
                        )
                        .ok();
                    self.os_notify(
                        "PrivaSync — Massenlöschung erkannt",
                        &format!(
                            "{}: {} von {} Dateien zur Löschung markiert. Sync pausiert — bitte bestätigen.",
                            root.name, delete_count, total_tracked,
                        ),
                    );
                    // Don't run any further work this cycle — the root is now
                    // paused and the user owns the next decision.
                    return Ok(());
                }
                info!("bulk-delete confirmed for '{}' — proceeding", root.name);
            }
        }

        // Send in chunks rather than as one 2000-file run, so the same check
        // can cut the transfer half short too (ADR-0059). `run_uploads`
        // carries its progress tally across calls, so a chunked drain shows
        // the same climbing bar as a single run.
        let mut result = Ok(());
        for chunk in to_upload.chunks(UPLOAD_RUN_CHUNK) {
            result = self.run_uploads(root, chunk.to_vec()).await;
            if result.is_err() {
                break;
            }
            if self.lane_arrived_since(arrivals_at_start) {
                debug!(
                    "upload batch for '{}' cut short — live change waiting in the priority lane",
                    root.name,
                );
                more_waiting = true;
                break;
            }
        }

        // More is waiting: either this batch filled up, or it stopped early
        // for a live change. Ask for another cycle now rather than letting the
        // queue idle until the 30s poll — during a first sync that difference
        // is hours (ADR-0056).
        if more_waiting {
            self.sync_now.notify_one();
        }
        result
    }

    /// Whether a row entered the priority lane since `mark` was taken — the
    /// preemption signal (ADR-0059). A relaxed load: the count only ever
    /// grows, and reacting one check later costs nothing.
    fn lane_arrived_since(&self, mark: u64) -> bool {
        self.lane_arrivals.load(Ordering::Relaxed) > mark
    }

    /// Inner upload loop — called after the bulk-delete guard has run.
    /// Runs up to `UPLOAD_CONCURRENCY_DEFAULT` (or configured) transfers in parallel.
    async fn run_uploads(
        &self,
        root: &SyncRootEntry,
        to_upload: Vec<crate::db::LocalFile>,
    ) -> Result<()> {
        if to_upload.is_empty() {
            return Ok(());
        }

        // What the progress bar counts against is the root's whole outstanding
        // queue, not this batch (ADR-0056). `to_upload.len()` is capped at
        // UPLOAD_BATCH_LIMIT, so using it showed "1999 von 1999" while tens of
        // thousands of files were still waiting — and it reset with every
        // batch instead of moving on.
        //
        // `done_before` carries the files this drain already finished in
        // earlier batches, so the bar keeps climbing across batch boundaries
        // and only resets once the root actually runs dry (in
        // `process_uploads`).
        let done_before = self
            .upload_progress
            .lock()
            .unwrap()
            .get(&root.id)
            .copied()
            .unwrap_or(0);
        let outstanding = self.db.count_pending(&root.id).unwrap_or(to_upload.len());
        // Shared and refreshed while the batch runs (see the join loop below).
        // Read once at batch start, it went stale the moment the detached walk
        // staged the next directory: the per-root "x von y" then showed a `y`
        // smaller than the "(z ausstehend)" in the status line, which is a
        // contradiction — `y` counts the same queue plus what is already done.
        let total = Arc::new(AtomicUsize::new(done_before + outstanding));

        let concurrency = self
            .db
            .config_get("upload_concurrency")
            .ok()
            .flatten()
            .and_then(|v| v.parse::<usize>().ok())
            .unwrap_or(UPLOAD_CONCURRENCY_DEFAULT)
            .clamp(1, 8);
        info!(
            "uploading {} file(s) of {} outstanding for '{}' (concurrency {})",
            to_upload.len(),
            outstanding,
            root.name,
            concurrency
        );

        // Not one snapshot for the batch: a batch outlives the access token, and
        // every request after its expiry came back 401. Each request
        // takes the current token through `AuthedApi::call` instead.
        if !self.api.is_authenticated().await {
            return Err(anyhow::Error::new(crate::api::NotAuthenticated));
        }
        let api = self.api.clone();
        let db = self.db.clone();
        let app = self.app.clone();
        let sem = Arc::new(tokio::sync::Semaphore::new(concurrency));
        // Atomic counter so progress increments correctly across parallel tasks.
        let done = Arc::new(AtomicUsize::new(0));
        // Per-cycle transfer log for the OS notification summary.
        let transfers = self.transfers.clone();
        // Directories whose Explorer status these uploads invalidate.
        let dirty = self.dirty_dirs.clone();
        let snapshot_store = self.snapshot_store.clone();

        // Owned root fields needed inside 'static tasks.
        let root_id = root.id.clone();
        let root_name = root.name.clone();
        let root_local = root.local_path.clone();
        let root_server_id = root.server_root_id.clone();
        // Copy: routes each REST call to /shared-mounts vs /sync-roots (ADR-0011).
        let root_is_mount = root.is_mount();

        let mut join_set = tokio::task::JoinSet::new();

        for file in to_upload {
            let api = api.clone();
            let db = db.clone();
            let app = app.clone();
            let sem = sem.clone();
            let done = done.clone();
            let total = total.clone();
            let transfers = transfers.clone();
            let dirty = dirty.clone();
            let snapshot_store = snapshot_store.clone();
            let root_id = root_id.clone();
            let root_name = root_name.clone();
            let root_server_id = root_server_id.clone();
            let root_local = root_local.clone();

            join_set.spawn(async move {
                // No task-wide permit here: each network attempt below
                // acquires its own slot via `with_retry_throttled` (or an
                // inline acquire for the delta path), and releases it while
                // backing off from a transient failure. Holding one permit
                // for the whole task — including retry sleeps — would let a
                // few persistently-failing files occupy every concurrency
                // slot for the whole backoff window and starve the rest of
                // the queue.
                let local_path = Path::new(&root_local)
                    .join(file.path.trim_start_matches('/'));

                // Re-probed here because `process_uploads` ran before the queue
                // did: the file may have vanished since. `Unreadable` stays out
                // of the delete branch on purpose — it falls through to Upload,
                // where opening the file fails and records a retryable failure.
                let is_delete = file.sync_status == "pending_delete"
                    || matches!(probe_local(&local_path), LocalPresence::Gone);
                let action    = if is_delete { "delete" } else { "upload" };

                // Uploading, deleting or creating an entry clears the Explorer
                // status of every directory above it — recorded here so the
                // settled end of the cycle can put it back.
                record_dirty_dirs(&dirty, &root_id, &file.path, file.is_dir && !is_delete);

                // "This file is transferring now" — emitted from inside the
                // retry closures below, which run *after* the concurrency
                // permit is acquired, never here.
                //
                // Announcing it at spawn time was wrong twice over. Every task
                // of the batch is spawned up front and only then queues for a
                // permit, so the UI's set of in-flight files was the whole
                // batch: it named a file that had not started, and the counter
                // it carried was the one captured at spawn — which is why the
                // number sat still while 6 MB/s were going out. The counter
                // itself advances on completion (further down), so both halves
                // now describe work that is actually happening.
                let announce = {
                    let app     = app.clone();
                    let root_id = root_id.clone();
                    let path    = file.path.clone();
                    let done    = done.clone();
                    let total   = total.clone();
                    move || {
                        app.emit("sync_progress", serde_json::json!({
                            "root_id": root_id,
                            "current": done_before + done.load(std::sync::atomic::Ordering::Relaxed),
                            "total":   total.load(std::sync::atomic::Ordering::Relaxed),
                            "file":    path,
                            "action":  action,
                        })).ok();
                    }
                };

                match decide_upload_action(file.is_dir, is_delete) {
                    UploadAction::CreateDir => {
                        // Leerer Ordner: kein File-Upload, sondern POST /directories.
                        let path = file.path.clone();
                        let (rsid, p) = (&root_server_id, &path);
                        match with_retry_throttled(&sem, &path, || {
                            announce();
                            api.call(|snap| async move { snap.create_directory(rsid, p, root_is_mount).await })
                        }).await {
                            Ok(server_id) => {
                                // Synced ohne Server-Hash — Dirs haben keinen Inhalt.
                                let _ = db.set_synced_with_version(
                                    &root_id, &file.path, &[], 0,
                                );
                                // Persist the server file id right away so a later
                                // move of this (possibly empty) directory is
                                // recognised as a rename instead of delete+create —
                                // without it an empty-dir move toasts as "gelöscht"
                                //. Skipped when the endpoint returned no id.
                                if !server_id.is_empty() {
                                    let _ = db.set_server_file_id(&root_id, &file.path, &server_id);
                                }
                                if let Ok(mut v) = transfers.lock() {
                                    v.push(TransferRecord {
                                        op: TransferOp::Uploaded,
                                        path: file.path.clone(),
                                        root_name: root_name.clone(),
                                    });
                                }
                                info!("created remote directory: {}", file.path);
                            }
                            Err(e) if is_auth_failure(&e) => leave_pending_on_auth_failure(&file.path, &e),
                            Err(e) => {
                                let next_attempt = file.attempt_count + 1;
                                record_failure(
                                    "create_directory", "upload", &file.path, &e, next_attempt,
                                    |next_retry| { let _ = db.set_failed(&root_id, &file.path, &e.to_string(), next_retry); },
                                    |payload| { app.emit("sync_failure", payload).ok(); },
                                );
                            }
                        }
                    }
                    UploadAction::Delete => {
                        if let Err(error) = snapshot_store.discard_session(
                            &db, &root_server_id, &file.path,
                        ) {
                            warn!("cannot discard upload session for '{}': {error:#}", file.path);
                        }
                        let path = file.path.clone();
                        let known_version = file.server_version;
                        let (rsid, p) = (&root_server_id, &path);
                        match with_retry_throttled(&sem, &path, || {
                            announce();
                            api.call(|snap| async move { snap.delete_file(rsid, p, known_version, root_is_mount).await })
                        }).await {
                            Ok(crate::api::DeleteOutcome::Deleted) => {
                                let _ = db.delete_file_record(&root_id, &file.path);
                                // A row the server never held (retired rename
                                // source) deleted nothing visible.
                                if delete_reached_server(&file) {
                                    if let Ok(mut v) = transfers.lock() {
                                        v.push(TransferRecord {
                                            op: TransferOp::Deleted,
                                            path: file.path.clone(),
                                            root_name: root_name.clone(),
                                        });
                                    }
                                }
                            }
                            Ok(crate::api::DeleteOutcome::Conflict { server_version }) => {
                                // Someone wrote a newer version than the one
                                // this delete was decided against. Their change
                                // wins over our removal: drop the pending row so
                                // the delete is not retried, and let the pull
                                // bring the new version down. Without the
                                // If-Match the delete would simply have gone
                                // through and their edit would be gone.
                                warn!(
                                    "delete of '{}' rejected: server moved to version {server_version} \
                                     — keeping the server's copy, dropping the local delete",
                                    file.path,
                                );
                                let _ = db.delete_file_record(&root_id, &file.path);
                                app.emit("sync_conflict", serde_json::json!({
                                    "path":   file.path,
                                    "reason": "delete_version_conflict",
                                })).ok();
                            }
                            Err(e) if is_auth_failure(&e) => leave_pending_on_auth_failure(&file.path, &e),
                            Err(e) => {
                                let next_attempt = file.attempt_count + 1;
                                record_failure(
                                    "delete", "delete", &file.path, &e, next_attempt,
                                    |next_retry| { let _ = db.set_failed(&root_id, &file.path, &e.to_string(), next_retry); },
                                    |payload| { app.emit("sync_failure", payload).ok(); },
                                );
                            }
                        }
                    }
                    UploadAction::Upload => {
                        let scope = UploadScope {
                            api: &api, db: &db, app: &app, sem: &sem, transfers: &transfers,
                            snapshot_store: &snapshot_store,
                            root_id: &root_id, root_name: &root_name,
                            root_server_id: &root_server_id, root_is_mount,
                        };
                        upload_one(&scope, &file, &local_path, &announce).await;
                    }
                }

                // Per-file Done — damit das UI parallele Uploads einzeln
                // verfolgen kann. Hier, nach dem Transfer, wird auch
                // hochgezählt: `current` zählt erledigte Dateien, nicht
                // gestartete Tasks.
                let current = done_before
                    + done.fetch_add(1, std::sync::atomic::Ordering::Relaxed) + 1;
                app.emit("sync_progress", serde_json::json!({
                    "root_id": root_id,
                    "current": current,
                    "total":   total.load(std::sync::atomic::Ordering::Relaxed),
                    "file":    file.path,
                    "action":  "file_done",
                })).ok();

                // Update pending status display after each file. Derive the
                // status from the live counts — hardcoding "syncing" here
                // races the engine's final overall_status emit: if this
                // last per-file event lands at the listener AFTER the "idle"
                // emit, the tray gets stuck on Synchronisiert… until the
                // user kicks it manually.
                //
                // Counted, not listed: this runs after every single file, and
                // `list_pending_all` materialised every pending row of the
                // system each time — 22k rows per uploaded photo during a
                // first sync.
                let (pending_count, conflict_count) =
                    db.pending_counts_all().unwrap_or((0, 0));
                let derived = if conflict_count > 0 { "conflict" }
                              else if pending_count > 0 { "syncing" }
                              else { "idle" };
                app.emit("sync_status_changed", serde_json::json!({
                    "pending_count":  pending_count,
                    "conflict_count": conflict_count,
                    "status":         derived,
                })).ok();
            });
        }

        // Wait for all tasks, refreshing the denominator as they finish. The
        // walk is staging into the same queue while this runs (ADR-0056), so a
        // total fixed at batch start would be overtaken by the status line's
        // outstanding count within seconds. Throttled: this is one COUNT, but
        // it would otherwise run per completed file.
        let mut last_total_refresh = std::time::Instant::now();
        while let Some(result) = join_set.join_next().await {
            if let Err(e) = result {
                warn!("upload task panicked: {e}");
            }
            if last_total_refresh.elapsed() >= TOTAL_REFRESH_INTERVAL {
                if let Ok(outstanding) = self.db.count_pending(&root.id) {
                    let done_now = done.load(std::sync::atomic::Ordering::Relaxed);
                    total.store(
                        done_before + done_now + outstanding,
                        std::sync::atomic::Ordering::Relaxed,
                    );
                }
                last_total_refresh = std::time::Instant::now();
            }
        }

        // Carry this batch's tally into the next one, so the bar continues
        // instead of restarting. Cleared by `process_uploads` once the root's
        // queue is empty — that, not the end of a batch, is the end of a drain.
        self.upload_progress.lock().unwrap().insert(
            root.id.clone(),
            done_before + done.load(std::sync::atomic::Ordering::Relaxed),
        );

        // No progress-done here: with a batched queue the end of a batch is not
        // the end of the work, and clearing the UI's progress row between
        // batches made the transfer look finished every 2000 files.

        Ok(())
    }
}

/// Pure bulk-delete guard math (see [`BULK_DELETE_THRESHOLD`]): the deleted
/// share of tracked files, compared by the caller against the threshold and
/// also reused for the warn/notify message. Split out from `process_uploads`
/// so the guard's math is unit-testable without a `SyncEngine`. Callers still
/// gate on [`BULK_DELETE_MIN_FILES`] before computing `total_tracked` — tiny
/// roots never trigger this check.
fn bulk_delete_ratio(delete_count: usize, total_tracked: usize) -> f64 {
    delete_count as f64 / total_tracked.max(1) as f64
}

/// Whether the bulk-delete guard should pause and ask (ADR-0064): the ratio
/// alone scales in the wrong direction on a huge root — a million-file root
/// needs 300k deletions to trip 30% — so a raw count past
/// [`BULK_DELETE_ABSOLUTE_THRESHOLD`] trips it too, independent of ratio.
pub(super) fn bulk_delete_should_trigger(delete_count: usize, total_tracked: usize) -> bool {
    bulk_delete_ratio(delete_count, total_tracked) > BULK_DELETE_THRESHOLD
        || delete_count > BULK_DELETE_ABSOLUTE_THRESHOLD
}

#[cfg(test)]
mod tests {
    use super::*;

    // The ratio alone never trips on a huge root: 5000 deletions out of 2M
    // tracked files is 0.25%, nowhere near 30% — exactly the case
    // BULK_DELETE_ABSOLUTE_THRESHOLD exists to catch (ADR-0064).
    #[test]
    fn bulk_delete_should_trigger_fires_on_absolute_count_even_with_tiny_ratio() {
        assert!(bulk_delete_should_trigger(
            BULK_DELETE_ABSOLUTE_THRESHOLD + 1,
            2_000_000
        ));
        assert!(
            !bulk_delete_should_trigger(BULK_DELETE_ABSOLUTE_THRESHOLD, 2_000_000),
            "boundary is exclusive, like the ratio"
        );
    }

    #[test]
    fn bulk_delete_should_trigger_still_fires_on_ratio_alone() {
        assert!(
            bulk_delete_should_trigger(4, 10),
            "40% of a small root must still trigger"
        );
        assert!(
            !bulk_delete_should_trigger(1, 10),
            "10% of a small root must not trigger"
        );
    }

    // Below BULK_DELETE_MIN_FILES the caller never even calls this (guarded
    // inline), but the pure ratio math itself must still hold: a 2-of-3 batch
    // (66%) clears the 30% threshold regardless of the absolute count.
    #[test]
    fn bulk_delete_ratio_flags_high_share() {
        assert!(
            bulk_delete_ratio(2, 3) > BULK_DELETE_THRESHOLD,
            "2/3 = 66% must exceed 30%"
        );
        assert!(
            bulk_delete_ratio(1, 10) <= BULK_DELETE_THRESHOLD,
            "1/10 = 10% must stay under 30%"
        );
    }

    // Exactly at the threshold must NOT trigger — the guard is "greater than",
    // not "greater than or equal", so a clean 30% batch is not flagged.
    #[test]
    fn bulk_delete_ratio_boundary_is_exclusive() {
        assert_eq!(
            bulk_delete_ratio(3, 10),
            BULK_DELETE_THRESHOLD,
            "exactly 30%"
        );
        assert!(
            bulk_delete_ratio(3, 10) <= BULK_DELETE_THRESHOLD,
            "exactly 30% must not exceed"
        );
        assert!(
            bulk_delete_ratio(31, 100) > BULK_DELETE_THRESHOLD,
            "30.001% must exceed"
        );
    }

    // total_tracked=0 must not divide by zero or panic — the guard degrades to
    // "every delete looks like 100%", handled via total_tracked.max(1).
    #[test]
    fn bulk_delete_ratio_handles_zero_total_tracked() {
        assert_eq!(bulk_delete_ratio(5, 0), 5.0);
    }
}
