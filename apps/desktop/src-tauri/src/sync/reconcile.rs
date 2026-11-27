//! Server change reconciliation: pull `/changes`, apply them locally, and
//! retry persistent failures from `failed_server_changes`.
//!
//! Anything that turns server-side state into local-disk state lives here:
//! downloads, deletions, conflict copies, and the failure-retry sweep.

use std::path::Path;
use std::time::UNIX_EPOCH;
use anyhow::Result;
use tracing::{debug, info, warn};
use tauri::Emitter;

use crate::cloud_files::{self, PlaceholderInfo};
use crate::db::SyncRootEntry;
use super::{LocalChange, SyncEngine, adopt_server_files, apply_server_dir_entry, hash_file, remove_local_entry, TransferOp, CHANGES_PAGE_LIMIT, RETRY_STAT_BATCH_SIZE};
use super::engine_placeholders::record_dirty_dirs;
use super::predicates::{detect_renames, merge_prefix_moves, RenamePlan};
use super::retry::{compute_next_retry, with_retry};
use super::paths::check_server_path;
use super::failure::{record_failure, materialise_conflict};
use super::download::stage_download;
use super::reconcile_decide::{decide_server_change, LocalState, ServerChangeAction};
use super::reconcile_incoming_delete::{
    decide_incoming_bulk_delete, flush_incoming_deletes, incoming_wave_looks_suspicious, IncomingBulkDeleteDecision,
};
use super::retry_decide::{decide_retry_outcome, RetryOutcome};

impl SyncEngine {
    /// First sync of a (re-)added root: adopt pre-existing local files that
    /// already match the server instead of blind-uploading them. A fresh
    /// `root.id` has an empty file index, so `reconcile_local` would otherwise
    /// stage every on-disk file as a brand-new upload; with no known
    /// server_version those uploads carry no If-Match and the server treats
    /// each blind write to an existing path as a real conflict (loser copy +
    /// notification per file). Runs only on the very first cycle for this root
    /// — `last_sync` absent — and only adds knowledge (synced rows with the
    /// server_version), never touches disk. Best-effort: a failure just falls
    /// back to the historical behaviour.
    pub(super) async fn adopt_on_first_sync(&self, root: &SyncRootEntry) -> Result<()> {
        if !Path::new(&root.local_path).is_dir() {
            return Ok(());
        }

        // One feed for both kinds: the seq cursor lives under
        // `changes_cursor_*` whether the root is owned or mounted.
        if self.db.config_get(&format!("changes_cursor_{}", root.id))?.is_some() {
            return Ok(());
        }

        // The changes cursor only appears once the first cycle reaches its
        // downloads, behind an upload pass that takes hours on a first sync.
        // A start before that runs this again, so it keeps its own position
        // and resumes from the last page it finished instead of re-reading
        // the whole feed.
        let adopt_key = format!("adopt_cursor_{}", root.id);
        let mut cursor: i64 = self.db.config_get(&adopt_key)?
            .and_then(|s| s.parse().ok())
            .unwrap_or(0);
        loop {
            let page = self.api
                .call_client(|c| async move {
                    c.read().await
                        .get_changes_page(&root.server_root_id, cursor, CHANGES_PAGE_LIMIT, root.is_mount())
                        .await
                })
                .await?;
            let (next_cursor, should_continue) = next_pagination_step(cursor, &page);

            if !page.changes.is_empty() {
                // Hashing reads every adopted file in full — blocking I/O
                // that has no business on an executor thread.
                let db       = self.db.clone();
                let root_blk = root.clone();
                let changes  = page.changes;
                let adopted = tokio::task::spawn_blocking(move || {
                    adopt_server_files(&db, &root_blk, &changes)
                })
                .await
                .map_err(|e| anyhow::anyhow!("spawn_blocking: {e}"))??;
                if adopted > 0 {
                    info!("adopted {adopted} pre-existing file(s) for '{}' on first sync \
                           — skipping blind re-upload", root.name);
                }
            }

            cursor = next_cursor;
            self.db.config_set(&adopt_key, &cursor.to_string())?;
            if !should_continue {
                break;
            }
        }
        Ok(())
    }

    pub(super) async fn process_downloads(&self, root: &SyncRootEntry) -> Result<()> {
        // Erst die offenen Retry-Faelle aus failed_server_changes abarbeiten,
        // damit ein vorher fehlgeschlagener Server-Change nicht weiter blockiert.
        // Ergebnis ist best-effort — falls das DB-Setup nicht da ist, weiter.
        if let Err(e) = self.retry_failed_server_changes(root).await {
            warn!("failed_server_changes retry sweep error for '{}': {e:#}", root.name);
        }

        self.process_downloads_paged(root).await
    }

    /// Keyset-paginated feed on a monotonic seq cursor (ADR-0036), for owned
    /// roots and Shared Mounts alike. Pages until the server signals no more
    /// (`next_cursor` absent), persisting the cursor after every page —
    /// including an all-caught-up empty page — so a crash mid-catch-up
    /// resumes instead of restarting, and a fully-synced root doesn't re-run
    /// adopt_on_first_sync forever. A cross-page rename's tombstone can be
    /// staged (`pending_incoming_delete`) before its live counterpart
    /// on a later page is seen; that is safe to advance past because staging
    /// is already durable in the DB and `rename_file` clears the flag if the
    /// row turns out to have moved — nothing here holds the delete in
    /// memory the way the removed cross-page tombstone deferral once did.
    ///
    /// Mounts had their own unpaginated, timestamp-based path until they moved onto the shared seq feed. Its
    /// cursor was a wallclock value, so a backwards clock correction could
    /// hide a change from a client that had already moved past that instant —
    /// silent divergence rather than a visible error.
    async fn process_downloads_paged(&self, root: &SyncRootEntry) -> Result<()> {
        let mut cursor: i64 = self.db
            .config_get(&format!("changes_cursor_{}", root.id))?
            .and_then(|s| s.parse().ok())
            .unwrap_or(0);
        let is_mount = root.is_mount();
        // Denominator for the inbound bulk-delete guard, snapshotted
        // once so a wave being staged over several pages this cycle doesn't
        // shrink its own ratio as it goes — same reasoning as the outbound
        // guard's `total_tracked` (ADR-0064).
        let total_tracked = self.db.count_tracked_files(&root.id).unwrap_or(0);

        loop {
            let page = self.api
                .call_client(|c| async move {
                    c.read().await
                        .get_changes_page(&root.server_root_id, cursor, CHANGES_PAGE_LIMIT, is_mount)
                        .await
                })
                .await?;

            // A page can hold only a directory-move event and no ordinary
            // file changes (ADR-0065: the move's own rows no longer bump
            // seq), so "caught up" must check both sources.
            let caught_up = page.changes.is_empty() && page.moves.is_empty();
            if !caught_up {
                self.apply_changes_batch(root, &page.changes, &page.moves).await?;
            }
            let (next_cursor, should_continue) = next_pagination_step(cursor, &page);
            cursor = next_cursor;

            // Unconditional — do not re-gate this on any future deferral
            // mechanism without re-deriving why. It was gated on a tombstone
            // deferral buffer once (an earlier mechanism, since removed);
            // that gate silently broke per-page crash recovery for the
            // buffer's entire lifetime, since the common case (nothing
            // deferred) never persisted until the very last page.
            let _ = self.db.config_set(
                &format!("changes_cursor_{}", root.id), &cursor.to_string(),
            );

            // Inbound bulk-delete guard: checked after every page
            // against the durable staged count (`RemoveLocal` now stages via
            // `mark_pending_incoming_delete` instead of applying immediately
            // — see that action's arm below), so a wave spanning more pages
            // than `BULK_DELETE_ABSOLUTE_THRESHOLD` can never sneak through
            // in per-page instalments the way the outbound guard does for uploads. The
            // confirm flag is read (and consumed) only when a pause is
            // actually on the table — reading it eagerly on every harmless
            // page would burn a real confirmation before the page that
            // needed it ever arrives.
            let pending = self.db.count_pending_incoming_deletes(&root.id).unwrap_or(0);
            let confirmed = if incoming_wave_looks_suspicious(pending, total_tracked) {
                self.db.take_bulk_delete_confirmed_incoming(&root.id).unwrap_or(false)
            } else {
                false
            };
            match decide_incoming_bulk_delete(pending, total_tracked, confirmed) {
                IncomingBulkDeleteDecision::Pause => {
                    warn!(
                        "inbound bulk-delete guard triggered for '{}': {pending} of {total_tracked} files \
                         staged for deletion — auto-pausing root, awaiting user decision",
                        root.name,
                    );
                    if let Err(e) = self.db.set_sync_root_paused(&root.id, true) {
                        warn!("failed to auto-pause '{}': {e:#}", root.name);
                    }
                    self.app.emit("sync:bulk_delete_warning", serde_json::json!({
                        "root_id":      root.id,
                        "root_name":    root.name,
                        "delete_count": pending,
                        "total_count":  total_tracked,
                        "direction":    "incoming",
                    })).ok();
                    self.os_notify(
                        "PrivaSync — Massenlöschung erkannt",
                        &format!(
                            "{}: {pending} von {total_tracked} Dateien zur Löschung markiert. Sync pausiert — bitte bestätigen.",
                            root.name,
                        ),
                    );
                    // Nothing staged this cycle is applied — the rows stay
                    // `pending_incoming_delete`, untouched on disk, exactly
                    // like the outbound guard leaves its own batch unsent.
                    return Ok(());
                }
                IncomingBulkDeleteDecision::Proceed => {}
            }

            if !should_continue {
                break;
            }
        }

        let outcome = flush_incoming_deletes(
            &self.db, &self.server_initiated_removes, &root.id, Path::new(&root.local_path),
        )?;
        for (rel, conflict_copy) in &outcome.rescued {
            self.app.emit("sync_conflict", serde_json::json!({
                "path":          rel,
                "conflict_copy": conflict_copy.to_string_lossy(),
                "reason":        "parent_deleted_on_server",
            })).ok();
        }
        if !outcome.applied_paths.is_empty() {
            for path in &outcome.applied_paths {
                self.record_transfer(TransferOp::Deleted, path, &root.name);
            }
            info!("applied {} inbound delete(s) for '{}'", outcome.applied_paths.len(), root.name);
        }
        Ok(())
    }

    /// Apply one batch of server changes: rename detection, then per-entry
    /// download/delete/conflict with failure recording and progress emits.
    /// Shared by every page of the changes feed, owned root or Shared Mount
    /// alike (ADR-0036).
    async fn apply_changes_batch(
        &self,
        root:     &SyncRootEntry,
        changes:  &[crate::api::ServerFileEntry],
        moves:    &[crate::api::PrefixMoveEvent],
    ) -> Result<()> {
        let total = changes.len();
        info!("processing {} server change(s) for '{}'", total, root.name);

        // Recognise server-side renames by file id before the per-entry loop:
        // a moved row keeps its id at the new path while a tombstone marks the
        // old one. Moving the local file in place (instead of download-new +
        // delete-old) avoids re-transferring the bytes and the historical
        // duplicate-on-rename. Detection degrades gracefully — rows
        // without a backfilled server id simply fall through to download.
        //
        // ADR-0065 directory-move events fold into the same plan: they need
        // no id lookup (old_prefix/new_prefix already name both sides), so
        // they run through the identical apply_renames loop without any
        // separate client-side apply path.
        let plan = match self.db.list_server_file_ids(&root.id) {
            Ok(local) => detect_renames(&local, changes),
            Err(e) => {
                warn!("rename detection skipped for '{}': {e:#}", root.name);
                RenamePlan::default()
            }
        };
        let plan = merge_prefix_moves(plan, moves);
        self.apply_renames(root, &plan);

        for (i, entry) in changes.iter().enumerate() {
            // Skip the two sides of an already-applied rename: the tombstone at
            // the vacated path (the file moved, was not deleted) and the live
            // entry at the new path (the local move already placed it).
            if entry.is_deleted && plan.consumed_old.contains(&entry.path) {
                continue;
            }
            if !entry.is_deleted && plan.rename_targets.contains(&entry.path) {
                let _ = self.db.set_server_file_id(&root.id, &entry.path, &entry.id);
                continue;
            }

            let action = if entry.is_deleted { "delete" } else { "download" };
            self.emit_progress(&root.id, i + 1, total, &entry.path, action);
            self.apply_one_change(root, entry).await;
            // Per-file Done — UI raeumt den aktiven Slot auf.
            self.emit_progress(&root.id, i + 1, total, &entry.path, "file_done");
        }

        self.emit_progress_done(&root.id);
        Ok(())
    }

    /// Apply a single feed entry, recording a retryable failure instead of
    /// propagating one: the cursor moves on regardless, so a lost entry would
    /// never come back on its own.
    async fn apply_one_change(&self, root: &SyncRootEntry, entry: &crate::api::ServerFileEntry) {
        // Every server change touches the entry's parent chain on disk —
        // creating, replacing or removing something inside it — which is
        // exactly what clears those directories' Explorer status. Recorded up
        // front so a partially-applied change still gets its folders settled.
        self.mark_dirs_dirty(&root.id, &entry.path, entry.is_dir && !entry.is_deleted);

        if let Err(e) = self.apply_server_change(root, entry).await {
            record_failure(
                "apply change", "server_change", &entry.path, &e, 1,
                |next_retry| {
                    let _ = self.db.record_failed_server_change(
                        &root.id, &entry.path, &e.to_string(), next_retry,
                    );
                },
                |payload| { self.app.emit("sync_failure", payload).ok(); },
            );
        } else if !entry.is_deleted {
            // Backfill the server id so future renames of this row are
            // detected by identity. Cheap no-op when already set.
            let _ = self.db.set_server_file_id(&root.id, &entry.path, &entry.id);
        }
    }

    /// Mirror server-side renames onto the local disk + DB. For each
    /// `(old, new)` (parent-first), move the file when its source still exists
    /// and the destination is free; a child whose ancestor already relocated
    /// the subtree is then a no-op on both sides — the filesystem move took
    /// its bytes and `rename_file` took its row, since that rewrites the whole
    /// subtree rather than the named row alone. The vacated path is marked
    /// server-initiated so the watcher's Deleted event is suppressed (the
    /// created side is suppressed by the server_hash check in stage_upload,
    /// since the DB row already carries the path + hash). Failures are logged
    /// and left for the normal download+tombstone path on the next cycle.
    fn apply_renames(&self, root: &SyncRootEntry, plan: &RenamePlan) {
        let base = Path::new(&root.local_path);
        for (old, new) in &plan.renames {
            let old_abs = base.join(old.trim_start_matches('/'));
            let new_abs = base.join(new.trim_start_matches('/'));

            // Suppress the watcher delete for the path we are vacating.
            self.note_server_initiated_remove(&root.id, old);

            if old_abs.exists() && !new_abs.exists() {
                if let Some(parent) = new_abs.parent() {
                    if let Err(e) = std::fs::create_dir_all(parent) {
                        warn!("rename '{old}'->'{new}': mkdir parent failed: {e:#}");
                        continue;
                    }
                }
                if let Err(e) = std::fs::rename(&old_abs, &new_abs) {
                    warn!("rename '{old}'->'{new}': fs move failed: {e:#}");
                    continue;
                }
            }

            if let Err(e) = self.db.rename_file(&root.id, old, new) {
                warn!("rename '{old}'->'{new}': db update failed: {e:#}");
                continue;
            }
            self.record_transfer(TransferOp::Renamed, new, &root.name);
            // Both ends of the move lost a child resp. gained one.
            self.mark_dirs_dirty(&root.id, old, false);
            self.mark_dirs_dirty(&root.id, new, false);
            info!("renamed locally (server move): {old} -> {new}");
        }
    }

    /// Drain `failed_server_changes` rows for this root whose backoff window
    /// has passed and re-attempt the apply. Unlike the original design
    /// (replaying the serialized feed entry the row failed with), each due
    /// row's *current* server state is re-fetched through the batched stat
    /// endpoint before anything is applied — ADR-0062: a stale snapshot
    /// applied minutes late could resurrect a path the server had since
    /// deleted, or overwrite a later server-side change with a frozen one.
    /// Paths are batched into blocks of [`RETRY_STAT_BATCH_SIZE`] so a
    /// root-wide outage that queues thousands of rows costs a handful of
    /// requests, not one per row. Successful entries are removed; failed
    /// entries get bumped (attempt_count + new next_retry_at); a row whose
    /// path the server has no record of at all is dropped outright — see
    /// [`decide_retry_outcome`].
    async fn retry_failed_server_changes(&self, root: &SyncRootEntry) -> Result<()> {
        let now = chrono::Utc::now().timestamp();
        let due = self.db.list_due_failed_server_changes(now)?;
        let due: Vec<_> = due.into_iter().filter(|r| r.sync_root_id == root.id).collect();
        if due.is_empty() {
            return Ok(());
        }
        info!("retrying {} failed server change(s) for '{}'", due.len(), root.name);

        for chunk in due.chunks(RETRY_STAT_BATCH_SIZE) {
            let paths: Vec<String> = chunk.iter().map(|row| row.server_path.clone()).collect();
            let stats = match self.api
                .call_client(|c| {
                    let paths = &paths;
                    async move { c.read().await.stat_files(&root.server_root_id, root.is_mount(), paths).await }
                })
                .await
            {
                Ok(s) => s,
                Err(e) => {
                    // Best-effort: leave this chunk's rows due, they are
                    // retried on the next cycle instead of losing the sweep
                    // entirely over one failed stat call.
                    warn!("stat sweep failed for '{}' ({} path(s)): {e:#}", root.name, paths.len());
                    continue;
                }
            };
            let by_path: std::collections::HashMap<&str, &crate::api::FileStatEntry> =
                stats.iter().map(|s| (s.path.as_str(), s)).collect();

            for row in chunk {
                let Some(stat) = by_path.get(row.server_path.as_str()) else {
                    // The server always answers one entry per requested
                    // path; a missing one would be a contract break. Drop
                    // rather than spin retrying something the server never
                    // acknowledges asking about.
                    warn!("stat sweep: no result for '{}' — dropping retry row", row.server_path);
                    let _ = self.db.delete_failed_server_change(row.id);
                    continue;
                };
                match decide_retry_outcome(stat) {
                    RetryOutcome::Drop => {
                        info!("retry dropped for '{}' — server has no record of it", row.server_path);
                        let _ = self.db.delete_failed_server_change(row.id);
                    }
                    RetryOutcome::Apply(entry) => match self.apply_server_change(root, &entry).await {
                        Ok(()) => {
                            info!("retry succeeded for '{}'", entry.path);
                            let _ = self.db.delete_failed_server_change(row.id);
                        }
                        Err(e) => {
                            let next_attempt = row.attempt_count + 1;
                            let next_retry = compute_next_retry(now, next_attempt);
                            let next_retry_val = next_retry.unwrap_or(i64::MAX); // MAX = manual-only
                            let _ = self.db.bump_failed_server_change(
                                row.id, &format!("{e:#}"), next_retry_val,
                            );
                            warn!("retry failed for '{}': {e:#} (next attempt {})",
                                entry.path,
                                next_retry.map_or("manual".to_string(), |t| (t - now).to_string() + "s"),
                            );
                        }
                    },
                }
            }
        }
        Ok(())
    }

    async fn apply_server_change(
        &self,
        root: &SyncRootEntry,
        entry: &crate::api::ServerFileEntry,
    ) -> Result<()> {
        let base = Path::new(&root.local_path);

        // Validate before touching the filesystem. Blocking issues (illegal
        // characters, case-collision) skip the file; a non-blocking length
        // warning still falls through and is synced — it may succeed with the
        // Windows Long Path option enabled. Stays an early guard
        // in the executor (not part of `decide_server_change`) so a blocked
        // entry never reaches the fallible local hash below — see
        // reconcile_decide.rs module docs.
        if !entry.is_deleted {
            if let Some(issue) = check_server_path(base, &entry.path) {
                warn!(
                    "path warning '{}': {} (blocking={})",
                    entry.path, issue.code(), issue.blocking(),
                );
                self.app.emit("sync:path_warning", serde_json::json!({
                    "root_id":  root.id,
                    "path":     entry.path,
                    "code":     issue.code(),
                    "params":   issue.params(),
                    "blocking": issue.blocking(),
                })).ok();
                if issue.blocking() {
                    return Ok(());
                }
            }
        }

        let local_path = base.join(entry.path.trim_start_matches('/'));

        // An open conflict is the user's to resolve: skip the normal download /
        // "already in sync" handling, which would otherwise re-mark the row
        // 'synced' and orphan its .conflict copy. Tombstones still fall through
        // so a server-side delete can clean up.
        if !entry.is_deleted {
            if let Ok(Some(f)) = self.db.get_file(&root.id, &entry.path) {
                if f.sync_status == "conflict" {
                    return Ok(());
                }
            }
        }

        // Gather the facts the pure decision needs. `hash_file` only runs
        // when a local file actually exists (same gating as the original
        // inline checks), and its result feeds `is_locally_modified` so the
        // Modified/None call is based on the file's *current* bytes rather
        // than a `sync_status` flag that `reconcile_local` may not have
        // caught up on yet within this same cycle — see that function's doc
        // comment for the race it closes.
        // A dehydrated placeholder is a file on disk with no local bytes
        // behind it. Hashing one recalls the entire file over the network —
        // and mid-update it cannot even succeed: the hydration callback
        // refuses when the placeholder's recorded version no longer matches
        // what the server answers (cloud_files/windows/connect.rs), so the
        // read fails and takes this whole apply with it. Its DB row already
        // records the server hash the placeholder stands for, which is
        // exactly what the comparison below needs.
        let is_placeholder_only = !entry.is_deleted
            && !entry.is_dir
            && local_path.exists()
            && cloud_files::is_dehydrated(&local_path);

        let local_hash = if !entry.is_deleted && !entry.is_dir && local_path.exists()
            && !is_placeholder_only
        {
            Some(hash_file(&local_path)?)
        } else {
            None
        };
        let local_change = if entry.is_deleted || !entry.is_dir {
            self.is_locally_modified(root, &entry.path, local_hash.as_deref())?
        } else {
            LocalChange::None
        };
        let content_matches = match (&entry.content_hash, &local_hash) {
            (Some(server_hex), Some(h)) => hex::encode(h) == *server_hex,
            // No local bytes to compare — the row's recorded server hash
            // stands in for them.
            (Some(server_hex), None) if is_placeholder_only => self.db
                .get_file(&root.id, &entry.path)
                .ok()
                .flatten()
                .and_then(|f| f.server_hash)
                .is_some_and(|h| hex::encode(h) == *server_hex),
            _ => false,
        };

        let state = LocalState {
            local_change,
            content_matches,
            is_readonly_mount: root.is_mount() && root.read_only,
        };

        match decide_server_change(entry, &state) {
            ServerChangeAction::KeepLocalOverDelete => {
                // Local has unsaved changes — keep local, stage for re-upload.
                warn!("conflict: server deleted '{}' but local modified — keeping local", entry.path);
                Ok(())
            }
            ServerChangeAction::ForgetDeletedRow => {
                // Both sides deleted — clean up DB record, nothing to do on disk.
                let _ = self.db.delete_file_record(&root.id, &entry.path);
                Ok(())
            }
            ServerChangeAction::RemoveLocal => {
                // Stage only — nothing is removed here. The inbound
                // bulk-delete guard decides at cycle end whether to flush
                // (apply every staged row via `flush_incoming_deletes`) or
                // leave the root paused for confirmation. Watcher suppression
                // and directory-rescue both move to flush time with it: the
                // gap between staging and flush can be arbitrarily long
                // (waiting on user confirmation), and the suppression TTL is
                // only 30s — noting it here would have long expired by the
                // time the real unlink happens.
                //
                // A path with no tracked row isn't part of what the guard
                // counts (there is nothing for the outbound watcher path to
                // stage against either, so no suppression is needed) — clean
                // up any stray disk copy directly, same as before this split.
                if self.db.get_file(&root.id, &entry.path)?.is_some() {
                    if let Err(e) = self.db.mark_pending_incoming_delete(&root.id, &entry.path) {
                        warn!("failed to stage incoming delete for '{}': {e:#}", entry.path);
                    }
                } else {
                    let _ = remove_local_entry(&local_path);
                }
                Ok(())
            }
            ServerChangeAction::CreateLocalDir => apply_server_dir_entry(&self.db, root, entry),
            ServerChangeAction::Conflict => {
                // Covers both sub-cases the original code logged separately
                // (local modified vs. local deleted while the server moved
                // on) — the resulting action is identical either way.
                if state.local_change == LocalChange::Deleted {
                    warn!("conflict: local deleted '{}' but server has update — re-downloading", entry.path);
                }
                self.handle_conflict(root, entry, &local_path).await
            }
            ServerChangeAction::DiscardReadonlyEdit => {
                // Read-only mount: the recipient has no write right, so there
                // is no conflict to resolve — the server wins and the local
                // edit is discarded. Warn the user (their change is lost) and
                // fall through to the download below. ADR-0011.
                warn!("read-only mount '{}': discarding local edit of '{}' — server version wins",
                    root.name, entry.path);
                self.app.emit("sync_readonly_discard", serde_json::json!({
                    "root_id": root.id,
                    "path":    entry.path,
                })).ok();
                self.os_notify(
                    "PrivaSync — Änderung verworfen",
                    &format!(
                        "'{}' ist schreibgeschützt — deine lokale Änderung wurde verworfen, der Serverstand wird geladen.",
                        entry.path,
                    ),
                );
                self.download_server_entry(root, entry, &local_path).await
            }
            ServerChangeAction::AlreadyInSync => {
                // Already in sync — just make sure the DB reflects it.
                if let Some(hash) = &local_hash {
                    let mtime = local_path.metadata()
                        .and_then(|m| m.modified())
                        .unwrap_or(UNIX_EPOCH)
                        .duration_since(UNIX_EPOCH)
                        .unwrap_or_default()
                        .as_secs() as i64;
                    let size = local_path.metadata().map(|m| m.len() as i64).unwrap_or(entry.size);
                    let _ = self.db.upsert_file(
                        &root.id, &entry.path,
                        size, mtime, hash,
                        "synced", Some(hash),
                    );
                }
                Ok(())
            }
            ServerChangeAction::Download => self.download_server_entry(root, entry, &local_path).await,
        }
    }

    /// Deliver the server's current bytes for `entry` onto `local_path` — the
    /// shared tail of both the plain-download and discard-readonly-edit
    /// branches (they only differ in what happens *before* this runs).
    async fn download_server_entry(
        &self,
        root: &SyncRootEntry,
        entry: &crate::api::ServerFileEntry,
        local_path: &Path,
    ) -> Result<()> {
        // On Windows (CF API): always create a placeholder first so the file
        // appears in File Explorer immediately, even before it is downloaded.
        // The Status column will show the cloud icon straight away.
        //
        // For eager mode: immediately hydrate the placeholder by calling
        // CfHydratePlaceholder in a blocking thread.  This triggers our
        // FETCH_DATA callback which downloads the file and updates the DB.
        //
        // For on-demand mode: leave the placeholder dehydrated.  Windows will
        // call FETCH_DATA the first time the user opens the file.
        //
        // On non-Windows — and on a volume root, which Windows refuses to
        // register as a sync root (ADR-0057) — download the full file directly.
        if cloud_files::placeholders_available_for(Path::new(&root.local_path)) {
            let mtime = chrono::DateTime::parse_from_rfc3339(entry.mtime.as_str())
                .map(|dt| dt.timestamp())
                .unwrap_or(0);

            let outcome = cloud_files::ensure_placeholder(
                Path::new(&root.local_path),
                &PlaceholderInfo {
                    relative_path: &entry.path,
                    size:          entry.size,
                    mtime,
                },
            )?;

            // A pinned file may not have its bytes dropped, so no placeholder
            // can stand in for the server's new content — write it out for
            // real. `download_file` records the row and the transfer itself,
            // so this branch is done once it returns.
            // Per-file and cheap, but the only place that records whether an
            // entry was newly created or re-pointed at a newer server version
            // — which is exactly what a "my file is stale" report needs.
            debug!("placeholder for '{}': {outcome:?}", entry.path);

            if outcome == cloud_files::PlaceholderOutcome::NeedsFullDownload {
                let ep = entry.path.clone();
                let lp = local_path.to_path_buf();
                with_retry(&ep, || self.download_file(root, &ep, &lp)).await?;
                return Ok(());
            }

            // Record the server-side state in the DB immediately. The
            // placeholder represents "in sync with server" semantically — the
            // bytes-on-disk part is just a hydration detail. Without this row
            // a future reconcile_local could see "file on disk, no DB record"
            // and stage the placeholder for upload (it would skip via
            // is_dehydrated, but better to set the truth explicitly).
            // The version travels with the row: each Cloud-Filter hydration is
            // its own request, and without a version on the placeholder there
            // is nothing to notice a server-side rewrite against.
            let server_hash = hex::decode(entry.content_hash.as_deref().unwrap_or(""))
                .unwrap_or_default();
            self.db.upsert_synced_with_version(
                &root.id, &entry.path,
                entry.size, mtime, &server_hash,
                entry.server_version.unwrap_or(-1),
            )?;

            // Count the placeholder as the delivered download. This is the
            // *only* pull path on Windows, so without it the per-cycle
            // summary sees no `Downloaded` records at all and a pure pull
            // cycle can never read "heruntergeladen" — the toast then
            // reports whatever uploads happened to share the cycle.
            // On-demand mode included: the file is what the user sees
            // arrive, and hydration is a byte-fetch detail behind it.
            self.record_transfer(TransferOp::Downloaded, &entry.path, &root.name);

            if root.download_mode == "eager" {
                // Fire-and-forget background hydration. The user sees the
                // placeholder appear in Explorer immediately (with the cloud
                // icon), then it transitions to ✓ once the bytes have been
                // downloaded. Hydrations are bounded by `hydrate_sem` so we
                // don't saturate the network on a fresh add of a large root.
                let lp           = local_path.to_path_buf();
                let path_for_log = entry.path.clone();
                let sem          = self.hydrate_sem.clone();
                let dirty        = self.dirty_dirs.clone();
                let dirty_root   = root.id.clone();
                tokio::spawn(async move {
                    let _permit = match sem.acquire_owned().await {
                        Ok(p)  => p,
                        Err(_) => return,    // semaphore closed (shutdown)
                    };
                    let lp2 = lp.clone();
                    let res = tokio::task::spawn_blocking(move || cloud_files::hydrate_file(&lp2))
                        .await;
                    match res {
                        Ok(Ok(()))   => {
                            let _ = cloud_files::mark_in_sync(&lp);
                            // This lands after the cycle that spawned it may
                            // already have settled; the next cycle picks the
                            // parent chain up.
                            record_dirty_dirs(&dirty, &dirty_root, &path_for_log, false);
                        }
                        Ok(Err(e))   => warn!("eager hydrate failed for '{}': {e:#}", path_for_log),
                        Err(e)       => warn!("eager hydrate task panicked for '{}': {e:#}", path_for_log),
                    }
                });
            }
        } else {
            let ep = entry.path.clone();
            let lp = local_path.to_path_buf();
            with_retry(&ep, || self.download_file(root, &ep, &lp)).await?;
        }
        Ok(())
    }

    async fn download_file(
        &self,
        root: &SyncRootEntry,
        server_path: &str,
        local_path: &Path,
    ) -> Result<()> {
        // A snapshot rather than the locked client: the transfer can run for
        // minutes, and nothing else should have to wait behind it for the API
        // lock.
        let staged = self.api
            .call(|snap| async move {
                stage_download(&snap, &root.server_root_id, server_path, root.is_mount(), local_path).await
            })
            .await?;

        // The staged file already carries its own hash — the download wrote
        // the bytes and hashed them in the same pass. Publishing it is the
        // rename, which stays atomic exactly as before.
        let (hash, size) = (staged.hash.clone(), staged.size);
        staged.publish(local_path)?;

        let mtime = local_path.metadata()?
            .modified()
            .unwrap_or(UNIX_EPOCH)
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .as_secs() as i64;

        self.db.upsert_file(
            &root.id, server_path,
            size, mtime, &hash,
            "synced", Some(&hash),
        )?;

        self.record_transfer(TransferOp::Downloaded, server_path, &root.name);
        info!("downloaded: {server_path}");
        Ok(())
    }

    async fn handle_conflict(
        &self,
        root: &SyncRootEntry,
        server_entry: &crate::api::ServerFileEntry,
        local_path: &Path,
    ) -> Result<()> {
        let winner = self.api
            .call(|snap| async move {
                stage_download(&snap, &root.server_root_id, &server_entry.path, root.is_mount(), local_path).await
            })
            .await?;

        let conflict_path = materialise_conflict(
            &self.db, &root.id, &server_entry.path, local_path, winner, None,
            |hash, size| {
                let mtime = local_path.metadata()
                    .and_then(|m| m.modified())
                    .unwrap_or(UNIX_EPOCH)
                    .duration_since(UNIX_EPOCH)
                    .unwrap_or_default()
                    .as_secs() as i64;
                self.db.upsert_file(
                    &root.id, &server_entry.path,
                    size, mtime, hash,
                    "synced", Some(hash),
                )?;
                self.record_transfer(TransferOp::Downloaded, &server_entry.path, &root.name);
                Ok(())
            },
            |payload| { self.app.emit("sync_conflict", payload).ok(); },
        )?;

        warn!("conflict resolved for '{}' — local copy saved as '{}'",
            server_entry.path,
            conflict_path.display()
        );

        Ok(())
    }
}

/// Given the current cursor and a freshly fetched page, compute the next
/// cursor and whether pagination should continue. Pure over its inputs so
/// the actual gap/duplicate-safety property (cursor only ever advances to
/// the last entry's own seq, pagination stops exactly when the server has
/// no more or sent nothing) is unit-testable without a live `SyncEngine` —
/// the executor methods around this remain a live-only surface per
/// ADR-0024, same as the rest of this module.
///
/// Takes the higher seq across `changes` and `moves` (ADR-0065): a directory
/// move can be the only thing in a page, and `changes` alone can no longer
/// be trusted to hold the page's true last seq.
fn next_pagination_step(cursor: i64, page: &crate::api::ChangesPage) -> (i64, bool) {
    let last_change = page.changes.last().map(|e| e.seq);
    let last_move = page.moves.last().map(|m| m.seq);
    let next_cursor = last_change.into_iter().chain(last_move).max().unwrap_or(cursor);
    let should_continue = (!page.changes.is_empty() || !page.moves.is_empty()) && page.next_cursor.is_some();
    (next_cursor, should_continue)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::api::{ChangesPage, PrefixMoveEvent, ServerFileEntry};

    fn move_event(seq: i64) -> PrefixMoveEvent {
        PrefixMoveEvent { old_prefix: format!("/old{seq}"), new_prefix: format!("/new{seq}"), seq }
    }

    fn entry(seq: i64) -> ServerFileEntry {
        ServerFileEntry {
            id:             format!("f{seq}"),
            path:           format!("/f{seq}.txt"),
            size:           1,
            mtime:          "2026-01-01T00:00:00Z".to_string(),
            content_hash:   None,
            is_deleted:     false,
            updated_at:     "2026-01-01T00:00:00Z".to_string(),
            server_version: Some(1),
            is_dir:         false,
            seq,
        }
    }

    #[test]
    fn next_pagination_step_advances_to_last_entrys_seq_and_continues_on_full_page() {
        let page = ChangesPage { changes: vec![entry(5), entry(6), entry(9)], moves: vec![], next_cursor: Some(9) };
        let (cursor, should_continue) = next_pagination_step(0, &page);
        assert_eq!(cursor, 9, "cursor must advance to the last entry's own seq");
        assert!(should_continue, "a page carrying next_cursor must continue");
    }

    #[test]
    fn next_pagination_step_stops_on_a_short_page_but_still_advances() {
        let page = ChangesPage { changes: vec![entry(11)], moves: vec![], next_cursor: None };
        let (cursor, should_continue) = next_pagination_step(9, &page);
        assert_eq!(cursor, 11, "a short (final) page's entries must still be committed to the cursor");
        assert!(!should_continue, "next_cursor absent must stop pagination");
    }

    // ADR-0065: a directory move can be the only thing in a page (no ordinary
    // file changes alongside it, since the move's own rows no longer bump
    // seq). Pagination must still advance past it and must not mistake the
    // page for "caught up" -- both bugs would silently drop the move.
    #[test]
    fn next_pagination_step_advances_and_continues_on_a_moves_only_page() {
        let page = ChangesPage { changes: vec![], moves: vec![move_event(9)], next_cursor: Some(9) };
        let (cursor, should_continue) = next_pagination_step(0, &page);
        assert_eq!(cursor, 9, "cursor must advance to the move event's own seq");
        assert!(should_continue, "a moves-only page carrying next_cursor must continue");
    }

    // A page's true last seq can come from either source -- whichever one
    // actually holds the highest seq, not just whichever field happens to be
    // checked first.
    #[test]
    fn next_pagination_step_takes_the_higher_seq_across_both_sources() {
        let page = ChangesPage { changes: vec![entry(5)], moves: vec![move_event(9)], next_cursor: Some(9) };
        let (cursor, _) = next_pagination_step(0, &page);
        assert_eq!(cursor, 9, "the move's seq is higher than the file entry's and must win");

        let page = ChangesPage { changes: vec![entry(9)], moves: vec![move_event(5)], next_cursor: Some(9) };
        let (cursor, _) = next_pagination_step(0, &page);
        assert_eq!(cursor, 9, "the file entry's seq is higher than the move's and must win");
    }

    #[test]
    fn next_pagination_step_leaves_cursor_untouched_on_an_empty_page() {
        let page = ChangesPage { changes: vec![], moves: vec![], next_cursor: None };
        let (cursor, should_continue) = next_pagination_step(9, &page);
        assert_eq!(cursor, 9, "an empty page has nothing to advance to");
        assert!(!should_continue, "an empty page must never continue, even if next_cursor were set");
    }
}
