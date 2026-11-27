use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;
use anyhow::Result;
use tauri::Emitter;
use tracing::{error, info, warn};

use crate::cloud_files::{self, HydrationCtx};
use crate::db::SyncRootEntry;
use crate::watcher::{start_watcher, FileEvent};

use super::{SyncEngine, POLL_INTERVAL, DEBOUNCE_MS, SAFETY_NET_INTERVAL};
use super::commands::drop_dehydrated_placeholders;

/// Why a sync cycle is running -- decides whether the (potentially
/// expensive, millions-of-files) local full walk runs this cycle. Only
/// `WatcherRestart` and `PeriodicSafetyNet` trigger it; the far more
/// frequent `Incremental` cycles (30s poll, debounced watcher events) only
/// push/pull against the server and process the watcher's own
/// already-staged events (ADR-0037) -- the live watcher is the primary
/// change-detection path, the walk is a backstop, not the routine case.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(super) enum TriggerReason {
    /// The watcher just (re)started — true engine startup, or a restart
    /// after a sync root was added/removed/repathed. Either way there's a
    /// window (the watcher was down) where local changes could have gone
    /// unobserved, so the two cases need the same treatment and share one
    /// variant rather than two that would behave identically.
    WatcherRestart,
    /// `SAFETY_NET_INTERVAL` backstop for OS watcher event loss — e.g. a
    /// kernel event-queue overflow (`ReadDirectoryChangesW`) during a burst.
    PeriodicSafetyNet,
    /// User clicked "Sync Now" (tray item or the `force_sync` command) —
    /// "sync now" reads as "make sure everything is right now", not just
    /// "push what's already staged", so this walks too (see
    /// `SyncEngine::force_walk`).
    UserRequested,
    /// The 30s poll tick or a debounced watcher event: push/pull only, no
    /// local walk.
    Incremental,
}

impl TriggerReason {
    fn triggers_full_walk(self) -> bool {
        matches!(self, Self::WatcherRestart | Self::PeriodicSafetyNet | Self::UserRequested)
    }
}

impl SyncEngine {
    pub async fn run(&self) {
        info!("sync engine started");
        loop {
            // Wait until authenticated. Surface this as a distinct tray
            // state so the icon shows the neutral logo (not the green idle
            // checkmark, which would imply "everything is synced").
            if !self.api.is_authenticated().await {
                self.emit_status("unauthenticated");
                tokio::time::sleep(Duration::from_secs(3)).await;
                continue;
            }

            // Drop any local roots that the server no longer knows about
            // (e.g. user deleted the root in the web UI). The return value
            // doubles as a server-reachability probe: Err = unreachable, so
            // we flag the tray "offline" and retry next cycle.
            match self.prune_orphaned_sync_roots().await {
                Ok(_)  => {}
                Err(_) => {
                    self.emit_status("offline");
                    tokio::time::sleep(Duration::from_secs(5)).await;
                    continue;
                }
            }

            let roots = match self.db.list_sync_roots() {
                Ok(r) => r,
                Err(e) => {
                    error!("failed to list sync roots: {e}");
                    tokio::time::sleep(Duration::from_secs(5)).await;
                    continue;
                }
            };

            let namespace_roots: Vec<(String, String)> = roots.iter()
                .map(|root| (root.name.clone(), root.local_path.clone()))
                .collect();
            if let Err(e) = cloud_files::reconcile_explorer_namespace(namespace_roots.clone()).await {
                warn!("Explorer namespace reconciliation failed: {e:#}");
            }

            if roots.is_empty() {
                // No roots configured (or last one just got pruned) — surface
                // as the distinct "empty" state so the tray shows the neutral
                // logo instead of the green "synchronized" idle icon.
                self.emit_status("empty");
                tokio::time::sleep(Duration::from_secs(3)).await;
                continue;
            }

            // Collect watch paths (only existing directories)
            let watch_paths: Vec<PathBuf> = roots.iter()
                .map(|r| PathBuf::from(&r.local_path))
                .filter(|p| p.is_dir())
                .collect();

            let mut event_rx = match start_watcher(watch_paths) {
                Ok(rx) => rx,
                Err(e) => {
                    error!("failed to start file watcher: {e}");
                    tokio::time::sleep(Duration::from_secs(5)).await;
                    continue;
                }
            };

            info!("watching {} sync root(s)", roots.len());

            // Track (id → local_path) for this watcher instance.
            // We restart whenever a root is added, removed, or its local path
            // changes (same server root re-added at a different directory).
            let watched_roots: std::collections::HashMap<String, String> =
                roots.iter().map(|r| (r.id.clone(), r.local_path.clone())).collect();

            // Ensure every root is registered with the CF API before connecting.
            // CfRegisterSyncRoot is idempotent — safe to call on every startup.
            // This re-registers roots whose Windows registry entry was lost
            // (e.g. after OS updates or profile resets), which would otherwise
            // cause CfConnectSyncRoot to fail and leave Explorer showing
            // briefcase fallback icons instead of cloud/checkmark status.
            //
            // A root on a volume root (`D:\`) is skipped: Windows refuses to
            // register one, and registering it anyway left Explorer with a
            // cloud provider for the whole drive that never answers
            // (ADR-0057). Such a root syncs eagerly, without placeholders.
            for root in &roots {
                let path = std::path::Path::new(&root.local_path);
                if !cloud_files::placeholders_available_for(path) {
                    if cloud_files::is_volume_root(path) {
                        info!("'{}' liegt auf einem Laufwerks-Root ({}) — \
                               Synchronisierung ohne Cloud-Files-Platzhalter, \
                               Dateien werden vollstaendig geladen",
                              root.name, root.local_path);
                    }
                    continue;
                }
                if let Err(e) = cloud_files::register(path, &root.name).await {
                    error!("CF re-registration failed for '{}': {e}", root.name);
                }
            }

            if let Err(e) = cloud_files::sync_explorer_namespace(namespace_roots).await {
                warn!("Explorer namespace refresh failed: {e:#}");
            }

            // Connect Cloud Files hydration callbacks for every root.
            // The connections are dropped automatically when the inner loop
            // ends (watcher restart), which calls CfDisconnectSyncRoot.
            let rt_handle = tokio::runtime::Handle::current();
            let _cf_connections: Vec<cloud_files::Connection> = roots.iter()
                .filter(|root| cloud_files::placeholders_available_for(
                    std::path::Path::new(&root.local_path)))
                .filter_map(|root| {
                    let ctx = Arc::new(HydrationCtx {
                        api:            self.api.clone(),
                        db:             self.db.clone(),
                        rt:             rt_handle.clone(),
                        server_root_id: root.server_root_id.clone(),
                        is_mount:       root.is_mount(),
                    });
                    match cloud_files::connect(
                        std::path::Path::new(&root.local_path), ctx)
                    {
                        Ok(conn) => {
                            info!("CF provider connected: '{}' (Explorer Status-Spalte aktiv)", root.name);
                            Some(conn)
                        }
                        Err(e) => {
                            // Without an active CfConnectSyncRoot, Explorer shows a
                            // briefcase icon for dehydrated placeholders instead of
                            // a cloud icon — the Status column is degraded.
                            error!("CF provider connect failed for '{}': {e} \
                                   — Explorer Synchronisierungsstatus-Spalte zeigt Fallback-Icons", root.name);
                            None
                        }
                    }
                })
                .collect();

            // Initial sync for all known roots (sync_cycle handles first-time
            // initial_scan internally when last_sync is absent). The watcher
            // just (re)started, so this is the one case that always walks.
            self.emit_status("syncing");
            self.sync_all_roots_draining(&roots, TriggerReason::WatcherRestart, &mut event_rx).await;
            self.notify_transfer_summary();
            self.emit_status(self.overall_status());

            // Main loop: react to file events + periodic polling + a rare
            // full-walk safety net (ADR-0037).
            let mut poll_timer = tokio::time::interval(POLL_INTERVAL);
            poll_timer.tick().await; // consume immediate first tick
            let mut safety_net_timer = tokio::time::interval(SAFETY_NET_INTERVAL);
            safety_net_timer.tick().await; // consume immediate first tick

            loop {
                tokio::select! {
                    // ── Periodic full sync ────────────────────────────────────
                    _ = poll_timer.tick() => {
                        // Detect server-side deletions before syncing — otherwise
                        // sync_cycle would chase 404s for the gone root.
                        let pruned = self.prune_orphaned_sync_roots().await.unwrap_or(false);
                        if pruned {
                            info!("sync root pruned by server — restarting watcher");
                            break;
                        }

                        self.emit_status("syncing");
                        let roots = self.db.list_sync_roots().unwrap_or_default();
                        self.sync_all_roots_draining(&roots, TriggerReason::Incremental, &mut event_rx).await;
                        self.notify_transfer_summary();
                        self.emit_status(self.overall_status());
                    }

                    // ── Full-walk safety net (ADR-0037) ───────────────────────
                    _ = safety_net_timer.tick() => {
                        self.emit_status("syncing");
                        let roots = self.db.list_sync_roots().unwrap_or_default();
                        self.sync_all_roots_draining(&roots, TriggerReason::PeriodicSafetyNet, &mut event_rx).await;
                        self.notify_transfer_summary();
                        self.emit_status(self.overall_status());
                    }

                    // ── Debounced sync triggered by file events or force_sync ─
                    _ = self.sync_now.notified() => {
                        // Wait a little to let rapid bursts of events settle
                        tokio::time::sleep(Duration::from_millis(DEBOUNCE_MS)).await;

                        // Consumed here regardless of exit path below (restart
                        // or incremental) so a stale request never leaks into
                        // an unrelated later cycle.
                        let user_requested = self.force_walk.swap(false, std::sync::atomic::Ordering::Relaxed);

                        // Drain any file events that arrived during the sleep
                        // so they are staged before we count pending files.
                        let roots_snap = self.db.list_sync_roots().unwrap_or_default();
                        while let Ok(ev) = event_rx.try_recv() {
                            self.stage_event(&roots_snap, ev);
                        }

                        // Restart if any root was added, removed, or had its
                        // local path changed (same server root, new local dir).
                        if watched_roots_changed(&roots_snap, &watched_roots) {
                            info!("sync root change detected — restarting watcher");
                            break;
                        }

                        let reason = if user_requested { TriggerReason::UserRequested } else { TriggerReason::Incremental };
                        self.emit_status("syncing");
                        let roots_snap = self.db.list_sync_roots().unwrap_or_default();
                        self.sync_all_roots_draining(&roots_snap, reason, &mut event_rx).await;
                        self.notify_transfer_summary();
                        self.emit_status(self.overall_status());
                    }

                    // ── File watcher events ───────────────────────────────────
                    event = event_rx.recv() => {
                        match event {
                            Some(ev) => {
                                let roots = self.db.list_sync_roots().unwrap_or_default();
                                self.handle_file_event(&roots, ev).await;
                            }
                            None => {
                                warn!("file watcher channel closed, restarting");
                                break;
                            }
                        }
                    }
                }
            }
        }
    }

    /// Detect sync roots that were deleted on the server (e.g. by the user via
    /// the web UI) and clean them up locally. Hydrated files and folder
    /// structure stay on disk — only DB tracking is dropped. Dehydrated Cloud
    /// Files placeholders are scrubbed first, since their content lives only on
    /// the server and they would otherwise become unopenable zombie reparse
    /// points in Explorer.
    ///
    /// Returns `Ok(true)` if at least one root was pruned (caller should
    /// restart the file watcher). Returns `Err` if the server is unreachable
    /// — caller surfaces that as the "offline" tray state. Always probes the
    /// server (even with zero local roots) so the outer loop knows whether
    /// the server is up before settling on the "empty" idle state.
    async fn prune_orphaned_sync_roots(&self) -> Result<bool> {
        // Pull both namespaces: own roots and incoming Shared Mounts. A mount's
        // server_root_id is a share_id, so it must be matched against the
        // shared-mounts set, never the own-roots set (ADR-0011). Both calls
        // must succeed — a failure means the server is unreachable, and we must
        // not prune on a transient miss.
        let server_roots = self.api
            .call_client(|c| async move { c.read().await.list_sync_roots().await })
            .await?;
        let server_mounts = self.api
            .call_client(|c| async move { c.read().await.list_shared_mounts().await })
            .await?;

        let local = self.db.list_sync_roots().unwrap_or_default();
        if local.is_empty() {
            return Ok(false);
        }

        // Keep the server names keyed by id so the post-prune refresh can
        // mirror an owned-root rename done in the web UI; prune itself
        // only needs the key set.
        let server_root_names: std::collections::HashMap<String, String> =
            server_roots.into_iter().map(|r| (r.id, r.name)).collect();
        let known_roots: std::collections::HashSet<String> =
            server_root_names.keys().cloned().collect();
        // Keep can_write alongside the id: prune only needs presence, but the
        // permission refresh below (ADR-0014) needs the flag too.
        let mount_can_write: std::collections::HashMap<String, bool> =
            server_mounts.into_iter().map(|m| (m.id, m.can_write)).collect();
        let known_mounts: std::collections::HashSet<String> =
            mount_can_write.keys().cloned().collect();

        let mut pruned = false;
        for root in local {
            if super::root_still_on_server(
                root.is_mount(), &root.server_root_id, &known_roots, &known_mounts,
            ) {
                continue;
            }
            info!(
                "sync root '{}' (server_id={}) no longer exists on server — removing locally",
                root.name, root.server_root_id
            );

            // Scrub dehydrated CF placeholders before unregister — afterwards
            // they become orphaned reparse points Explorer cannot open.
            // Hydrated files (the user's actual data) stay on disk.
            let removed = drop_dehydrated_placeholders(&root.local_path);
            if removed > 0 {
                info!("prune '{}': dropped {removed} dehydrated placeholder(s)", root.name);
            }

            if let Err(e) = cloud_files::unregister(Path::new(&root.local_path)).await {
                warn!("prune '{}': cloud_files::unregister failed (non-fatal): {e}", root.name);
            }

            if let Err(e) = self.db.remove_sync_root(&root.id) {
                error!("prune '{}': remove_sync_root in DB failed: {e}", root.name);
                continue;
            }

            self.app.emit("sync:root_orphaned", serde_json::json!({
                "root_id":    root.id,
                "root_name":  root.name,
                "local_path": root.local_path,
                "is_mount":   root.is_mount(),
            })).ok();
            if root.is_mount() {
                // Revocation, expiry, or the giver deleting the root all look
                // the same here: the share left the mount list (ADR-0011).
                self.os_notify(
                    "PrivaSync — Freigabe entzogen",
                    &format!(
                        "Die Freigabe '{}' wurde entzogen. Lokale Kopien bleiben unter '{}' erhalten.",
                        root.name, root.local_path,
                    ),
                );
            } else {
                self.os_notify(
                    "PrivaSync — Sync-Root entfernt",
                    &format!(
                        "'{}' wurde im Web gelöscht. Lokale Dateien bleiben unter '{}' erhalten.",
                        root.name, root.local_path,
                    ),
                );
            }
            pruned = true;
        }

        // Pull-authoritative permission refresh (ADR-0014): a giver may flip
        // can_write WITHOUT revoking — that never trips the prune path, so the
        // mount's persisted read_only would otherwise stay stale forever. Re-
        // list post-prune so we never touch a root we just removed, then apply
        // each read_only change the pull reveals. The engine RO-stage-guard and
        // the reconcile download-wins+toast (ADR-0011) enforce it from there.
        let survivors = self.db.list_sync_roots().unwrap_or_default();
        for (root_id, read_only) in super::mount_read_only_changes(&survivors, &mount_can_write) {
            if let Err(e) = self.db.set_sync_root_read_only(&root_id, read_only) {
                error!("read_only refresh for '{root_id}' failed: {e}");
                continue;
            }
            let name = survivors.iter().find(|r| r.id == root_id)
                .map(|r| r.name.clone()).unwrap_or_else(|| root_id.clone());
            if read_only {
                info!("mount '{name}' is now read-only (can_write revoked)");
                self.os_notify(
                    "PrivaSync — Schreibrecht entzogen",
                    &format!(
                        "Für '{name}' hast du kein Schreibrecht mehr. Lokale \
                         Änderungen werden mit der Server-Version überschrieben.",
                    ),
                );
            } else {
                info!("mount '{name}' is now writable (can_write granted)");
            }
        }

        // Pull-authoritative name refresh: an owned root renamed in the
        // web UI changes only its `name` server-side — that never trips the
        // prune path, so without this the desktop label would stay stale. Only
        // the DB label moves; the local folder path is the user's choice and
        // is left untouched. Emit so the UI re-lists immediately.
        let mut root_names_changed = false;
        for (root_id, new_name) in super::sync_root_name_changes(&survivors, &server_root_names) {
            if let Err(e) = self.db.set_sync_root_name(&root_id, &new_name) {
                error!("name refresh for '{root_id}' failed: {e}");
                continue;
            }
            root_names_changed = true;
            info!("sync root '{root_id}' renamed to '{new_name}' (web rename mirrored)");
            self.app.emit("sync:root_renamed", serde_json::json!({
                "root_id": root_id,
                "name":    new_name,
            })).ok();
        }

        if pruned || root_names_changed {
            if let Ok(roots) = self.db.list_sync_roots() {
                let namespace_roots = roots.iter()
                    .map(|root| (root.name.clone(), root.local_path.clone()))
                    .collect();
                if let Err(e) = cloud_files::sync_explorer_namespace(namespace_roots).await {
                    warn!("Explorer namespace refresh after root changes failed: {e:#}");
                }
            }
        }

        Ok(pruned)
    }

    /// `sync_all_roots`, with the watcher branch kept alive for the duration
    /// of the cycle (ADR-0059).
    ///
    /// A cycle is a single branch of the engine loop's `select!`, so for as
    /// long as it is awaited nothing else in that loop is polled. On a first
    /// sync over millions of files that is minutes at a time, and an event
    /// that has not been staged cannot be served by the upload queue's
    /// priority lane: it is not in the queue at all. Staging here is what
    /// makes "registered while the initial walk runs" mean "already on its
    /// way", instead of "seen once the running cycle happens to end".
    ///
    /// Scope worth being honest about: this revives exactly one of the loop's
    /// four branches. The poll tick's orphan check, the safety-net walk and
    /// the watcher-restart decision stay starved for the length of a cycle,
    /// as they were before. The moment a second branch needs servicing
    /// mid-cycle, the answer is not another wrapper — it is spawning the
    /// cycle as its own task and making "a cycle is in flight" explicit loop
    /// state, the way ADR-0056 already did for the walk.
    ///
    /// Events are staged, not fully handled: `stage_event` is synchronous DB
    /// work, while `handle_file_event` would await a server-side move and
    /// stall the very cycle this is running alongside. The debounce drain
    /// makes the same trade for the same reason; a rename observed here goes
    /// out as delete + upload rather than as a move.
    async fn sync_all_roots_draining(
        &self,
        roots:    &[SyncRootEntry],
        reason:   TriggerReason,
        event_rx: &mut tokio::sync::mpsc::Receiver<FileEvent>,
    ) {
        let cycle = self.sync_all_roots(roots, reason);
        tokio::pin!(cycle);
        loop {
            tokio::select! {
                _ = &mut cycle => return,
                event = event_rx.recv() => match event {
                    Some(ev) => {
                        self.stage_event(roots, ev);
                        // The staged row may be all that is left to do once
                        // this cycle ends — ask for the follow-up cycle that
                        // serves it rather than waiting for the poll tick.
                        self.sync_now.notify_one();
                    }
                    // Closed channel: a further `recv()` returns immediately,
                    // so selecting on it again would spin. The outer loop's
                    // own recv branch owns the restart decision — let this
                    // cycle finish and hand back to it.
                    None => { cycle.await; return; }
                },
            }
        }
    }

    /// Runs `sync_cycle` for every root concurrently instead of one after
    /// another — with several roots configured, a slow/large root no longer
    /// blocks the others from making progress. Each root's API calls go
    /// through `self.api`'s read lock, so they run genuinely in parallel;
    /// only the rare token refresh briefly serialises everyone.
    pub(super) async fn sync_all_roots(&self, roots: &[SyncRootEntry], reason: TriggerReason) {
        futures_util::future::join_all(roots.iter().map(|root| async move {
            if let Err(e) = self.sync_cycle(root, reason).await {
                warn!("sync cycle failed for '{}': {e}", root.name);
            }
        })).await;
    }

    pub(super) async fn sync_cycle(&self, root: &SyncRootEntry, reason: TriggerReason) -> Result<()> {
        // If the local sync directory has been deleted, skip this root entirely.
        // We do NOT recreate the folder — the user may have intentionally removed
        // it.  They need to manually re-add the root or restore the directory.
        if !Path::new(&root.local_path).is_dir() {
            warn!("sync root directory missing, skipping: '{}'", root.name);
            return Ok(());
        }

        // Skip paused or just-removed roots entirely. Re-read live state so a
        // pause OR remove that landed since the caller snapshotted the roots
        // list takes effect immediately.
        if self.should_skip(&root.id) {
            return Ok(());
        }

        // On the very first cycle of a (re-)added root, adopt pre-existing
        // local files that already match the server before reconcile stages
        // them. Without this, a re-add over existing content blind-uploads
        // every file and the server flags each as a conflict.
        if let Err(e) = self.adopt_on_first_sync(root).await {
            warn!("adopt-on-first-sync failed for '{}': {e}", root.name);
        }

        // Reconcile the local filesystem against the DB — only on a full-walk
        // trigger (ADR-0037). This is what picks up files added/deleted while
        // offline or from another host; the far more frequent Incremental
        // cycles rely on the live watcher (events.rs) to have already staged
        // whatever changed, and skip the (potentially expensive, millions-of-
        // files) walk entirely.
        //
        // Started, not awaited (ADR-0056): the rest of this cycle — and every
        // cycle the walk nudges while it runs — uploads what it has staged so
        // far, so discovery and transfer overlap instead of taking turns.
        if reason.triggers_full_walk() {
            self.spawn_reconcile_local(root);
        }

        // Convert any pre-existing synced files to CF placeholders so Explorer
        // shows the "Synchronisierungsstatus" column values. Once per root per
        // session, and started rather than awaited (ADR-0070): this root's
        // transfers do not wait for a pass over every entry it holds.
        self.spawn_ensure_placeholders(root);

        // Emit after reconcile so the UI shows the freshly-staged pending count
        // immediately, before any upload starts.
        self.emit_status("syncing");

        // resolve_disappeared runs inside process_uploads, immediately before
        // it snapshots the pending batch (ADR-0063) -- not here. The
        // detached walk (spawn_reconcile_local above) keeps staging rows
        // concurrently with this cycle (ADR-0056), so a call here could still
        // leave a race: the walk stages the far side of a folder rename
        // after this point but before process_uploads reads its batch, and
        // that batch would upload it as a plain new file before any resolve
        // pass ever saw it as a join candidate.
        self.process_uploads(root).await?;
        // The bulk-delete guard inside process_uploads may have flipped this
        // root to paused, or the user may have removed the root entirely
        // while we were uploading — bail before doing any further server I/O.
        if self.should_skip(&root.id) {
            return Ok(());
        }
        self.process_downloads(root).await?;
        if self.should_skip(&root.id) {
            return Ok(());
        }
        self.hydrate_pending_placeholders(root).await?;

        // Once nothing is pending for this root, every directory this cycle
        // touched — and the root itself — is in sync, so Explorer's Status
        // column gets its value back. The CF filter clears these bits on
        // every descendant change, so it is applied here, at the settled end
        // of each cycle, rather than once up front.
        //
        // A walk still in flight makes an empty queue meaningless — it is the
        // gap between two chunks, not the end of the work — so the root keeps
        // its "syncing" status until the walk that is still staging rows
        // finishes (ADR-0056). The placeholder pass likewise: it is still
        // converting directories whose status this would settle (ADR-0070).
        let settled = !self.walk_in_flight(&root.id)
            && !self.placeholder_pass_in_flight(&root.id)
            && !self.db.has_pending(&root.id).unwrap_or(true);
        if settled {
            self.settle_directory_status(root);
        }
        Ok(())
    }

    /// Returns `true` if this root should not be touched right now: either it
    /// has been removed from the DB (live re-fetch returned None) or it is
    /// flagged as paused. Used both at the start of a cycle and between phases
    /// so a remove/pause that lands mid-cycle takes effect immediately.
    pub(super) fn should_skip(&self, root_id: &str) -> bool {
        match self.db.get_sync_root(root_id).ok().flatten() {
            None      => true,        // root was removed mid-cycle
            Some(r)   => r.paused,
        }
    }
}

/// True when the current sync-root set no longer matches what the file
/// watcher was started with: a root was added, removed, or an existing root's
/// `local_path` changed (same server root re-added at a different directory).
/// Drives the watcher-restart decision in the debounced-sync branch of `run`.
/// Pure over its inputs so it is unit-testable without a `SyncEngine`.
fn watched_roots_changed(
    current: &[SyncRootEntry],
    watched: &std::collections::HashMap<String, String>,
) -> bool {
    current.len() != watched.len()
        || current.iter().any(|r| {
            watched.get(&r.id)
                .map(|p| p != &r.local_path)
                .unwrap_or(true)
        })
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;

    // Trigger matrix (ADR-0037): exactly the rare/expensive triggers walk;
    // everything routine relies on the live watcher having already staged
    // whatever changed.
    #[test]
    fn trigger_reason_walk_matrix() {
        let cases = [
            (TriggerReason::WatcherRestart,    true),
            (TriggerReason::PeriodicSafetyNet, true),
            (TriggerReason::UserRequested,     true),
            (TriggerReason::Incremental,       false),
        ];
        for (reason, want_walk) in cases {
            assert_eq!(
                reason.triggers_full_walk(), want_walk,
                "{reason:?}.triggers_full_walk() should be {want_walk}",
            );
        }
    }

    fn root(id: &str, local_path: &str) -> SyncRootEntry {
        SyncRootEntry {
            id:             id.to_string(),
            name:           "n".to_string(),
            local_path:     local_path.to_string(),
            server_root_id: "srv".to_string(),
            download_mode:  "on_demand".to_string(),
            read_only:      false,
            paused:         false,
            kind:           crate::db::ROOT_KIND_OWNED.to_string(),
        }
    }

    #[test]
    fn unchanged_root_set_does_not_trigger_restart() {
        let watched: HashMap<String, String> =
            [("r1".to_string(), "/a".to_string())].into_iter().collect();
        let current = vec![root("r1", "/a")];
        assert!(!watched_roots_changed(&current, &watched));
    }

    #[test]
    fn added_root_triggers_restart() {
        let watched: HashMap<String, String> =
            [("r1".to_string(), "/a".to_string())].into_iter().collect();
        let current = vec![root("r1", "/a"), root("r2", "/b")];
        assert!(watched_roots_changed(&current, &watched), "new root count must trigger a restart");
    }

    #[test]
    fn removed_root_triggers_restart() {
        let watched: HashMap<String, String> = [
            ("r1".to_string(), "/a".to_string()),
            ("r2".to_string(), "/b".to_string()),
        ].into_iter().collect();
        let current = vec![root("r1", "/a")];
        assert!(watched_roots_changed(&current, &watched));
    }

    #[test]
    fn changed_local_path_triggers_restart_even_with_same_count() {
        let watched: HashMap<String, String> =
            [("r1".to_string(), "/old-path".to_string())].into_iter().collect();
        let current = vec![root("r1", "/new-path")];
        assert!(watched_roots_changed(&current, &watched),
            "same root id at a different local_path must still restart the watcher");
    }
}
