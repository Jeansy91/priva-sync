//! Local filesystem full walk: chunked, resumable, checkpointed, with a
//! hierarchical per-directory diff (ADR-0037).
//!
//! Compares the local filesystem against the DB index and stages any
//! discrepancies — files/dirs added, modified, or deleted since the last
//! full walk. Runs rarely (`TriggerReason::triggers_full_walk`,
//! engine_loop.rs), not on every sync cycle: the live watcher (events.rs) is
//! the primary change-detection path, this is the backstop for whatever it
//! might have missed while offline or from event loss.
//!
//! Deletion-detection is scoped per directory (`list_immediate_children`)
//! instead of loading the whole root's file list once at the end — memory
//! at any instant is bounded by one directory's width times the current
//! recursion depth, not by the root's total size. This mirrors rclone's
//! "march" algorithm: walk a directory, diff its immediate children against
//! the DB's immediate children for that same path, recurse. No global
//! sort-merge across differently-ordered sources is needed for this (that
//! approach — and why it was rejected in favour of this one — is recorded
//! in ADR-0037): each level's diff is a same-directory name-set comparison,
//! not a merge of two independently-ordered walks.

use std::path::Path;
use std::sync::Arc;
use std::time::UNIX_EPOCH;
use anyhow::Result;
use tracing::{info, warn};

use crate::cloud_files;
use crate::db::{LocalDb, SyncRootEntry};
use super::SyncEngine;
use super::ignore;
use super::paths::relative_path;
use super::{is_server_initiated_remove, ServerRemoves};
use super::hash_file;

impl SyncEngine {
    /// Start comparing the local filesystem against the DB, staging any
    /// discrepancies — and return immediately.
    ///
    /// Called only on a full-walk `TriggerReason` (engine_loop.rs), not on
    /// every sync cycle — this is what picks up files added/deleted while the
    /// client was offline or from another host; routine cycles rely on the
    /// live watcher (events.rs) instead (ADR-0037).
    ///
    /// **Detached** (ADR-0056): the walk runs as its own task and the cycle
    /// goes straight on to `process_uploads`. Awaiting it here meant the first
    /// byte only left the machine once the last directory had been read —
    /// hours on a million-file root, with a full upload queue already staged
    /// and idle. The walk nudges `sync_now` after every completed top-level
    /// subtree, so a fresh cycle picks up what it just staged while it keeps
    /// walking: disk-bound discovery and network-bound upload now overlap
    /// instead of taking turns.
    ///
    /// The overlap guard (`active_walks`) is claimed synchronously, before the
    /// task is spawned, so a trigger arriving in a later cycle finds the root
    /// busy and skips instead of racing a second walk chain against the first.
    /// Callers that need to know whether a root is still being walked ask
    /// [`SyncEngine::walk_in_flight`].
    ///
    /// Chunked by top-level entry via `spawn_blocking`, so a long walk
    /// (millions of files, a slow disk) doesn't occupy its task for the whole
    /// duration either — between chunks, this same root's uploads/downloads/
    /// watcher-event handling can still make progress. A persisted
    /// checkpoint (the last fully completed entry, at any depth — ADR-0058)
    /// means a crash or app close mid-walk resumes by skipping already-done
    /// subtrees entirely, not just their expensive work — safe because each
    /// subtree's deletion-detection already committed when that subtree
    /// finished, not deferred to a global end-of-walk step.
    ///
    /// Uses mtime + size as a cheap fast-path; hashes only when those differ.
    pub(super) fn spawn_reconcile_local(&self, root: &SyncRootEntry) {
        if root.read_only {
            return;
        }
        if !Path::new(&root.local_path).is_dir() {
            return;
        }
        if !self.begin_walk(&root.id) {
            info!("reconcile: walk already in progress for '{}', skipping", root.name);
            return;
        }

        let db           = self.db.clone();
        let removes      = self.server_initiated_removes.clone();
        let active_walks = self.active_walks.clone();
        let sync_now     = self.sync_now.clone();
        let root         = root.clone();

        tokio::spawn(async move {
            if let Err(e) = run_chunked_walk(&db, &removes, &sync_now, &root).await {
                warn!("local reconcile failed for '{}': {e}", root.name);
            }
            // Guard released before the final nudge, so the cycle that nudge
            // triggers already sees the root as walk-free (and may settle its
            // Explorer status).
            active_walks.lock().unwrap().remove(&root.id);
            sync_now.notify_one();
        });
    }

    fn begin_walk(&self, root_id: &str) -> bool {
        self.active_walks.lock().unwrap().insert(root_id.to_string())
    }

    /// Whether a full walk is still staging rows for this root. Cycle steps
    /// that mean "everything settled" — Explorer's directory status above all
    /// — must not fire on the momentary empty queue between two chunks.
    pub(super) fn walk_in_flight(&self, root_id: &str) -> bool {
        self.active_walks.lock().unwrap().contains(root_id)
    }
}

/// How long the walk may go without persisting its position. A checkpoint
/// after every entry would be a config write per file; this bounds the work a
/// restart repeats to a couple of seconds instead.
const CHECKPOINT_INTERVAL: std::time::Duration = std::time::Duration::from_secs(2);

/// What the resume logic wants done with an entry while it is fast-forwarding
/// back to where the previous run stopped.
#[derive(Debug, PartialEq, Eq)]
enum Resume {
    /// Completed by the earlier run — skip it whole, mark it seen.
    Skip,
    /// The previous run stopped *inside* this directory — walk it, but keep
    /// fast-forwarding within it.
    Descend,
    /// At or past the checkpoint: walk normally, and stop fast-forwarding.
    Walk,
}

/// The walk's resumable position, persisted under `reconcile_checkpoint_<root>`.
///
/// The value is the root-relative path of the last **completed** entry — file
/// or directory, at any depth. It used to be the last completed *top-level*
/// entry, which is only a useful resume point when a root has many top-level
/// entries. On a root whose content sits under a single folder (a whole drive
/// with everything in `\Persönlich`, say) that entry never finished before the
/// next restart, so every start re-walked the same subtree from the beginning
/// and the queue only ever drained — never grew — until the walk had caught
/// up with itself again.
///
/// Correctness rests on one invariant: every entry that sorts at or before the
/// checkpoint, at every level of the path, was fully processed. The walk keeps
/// it by visiting siblings in sorted order and recording only on completion.
struct Checkpoint {
    /// `None` for the non-persisting form — nothing to write to.
    db:         Option<std::sync::Arc<LocalDb>>,
    key:        String,
    /// Where the previous run stopped; `None` once this run has caught up.
    resume_at:  Option<String>,
    last_write: std::time::Instant,
}

impl Checkpoint {
    fn load(db: std::sync::Arc<LocalDb>, key: String) -> Self {
        let resume_at = db.config_get(&key).ok().flatten().filter(|s| !s.is_empty());
        Checkpoint { db: Some(db), key, resume_at, last_write: std::time::Instant::now() }
    }

    /// A checkpoint that never resumes and never persists — for the
    /// unchunked convenience form and its tests.
    #[cfg(test)]
    fn none() -> Self {
        Checkpoint {
            db:         None,
            key:        String::new(),
            resume_at:  None,
            last_write: std::time::Instant::now(),
        }
    }

    fn resumed(&self) -> bool {
        self.resume_at.is_some()
    }

    /// What to do with `rel` at the current level.
    fn action(&mut self, rel: &str) -> Resume {
        let Some(cp) = self.resume_at.as_deref() else { return Resume::Walk };
        // The previous run stopped somewhere below `rel`.
        if cp.starts_with(&format!("{rel}/")) {
            return Resume::Descend;
        }
        if rel <= cp {
            return Resume::Skip;
        }
        // Past it — everything from here on is unexplored.
        self.resume_at = None;
        Resume::Walk
    }

    /// Note `rel` as completed. Throttled: a missed write only costs the next
    /// run a little repeated stat-ing, never correctness.
    fn record(&mut self, rel: &str) {
        if self.key.is_empty() || self.last_write.elapsed() < CHECKPOINT_INTERVAL {
            return;
        }
        if let Some(db) = &self.db {
            let _ = db.config_set(&self.key, rel);
        }
        self.last_write = std::time::Instant::now();
    }

    /// A completed walk starts fresh next time.
    fn clear(&mut self) {
        if let Some(db) = &self.db {
            if !self.key.is_empty() {
                let _ = db.config_set(&self.key, "");
            }
        }
        self.resume_at = None;
    }
}

/// Walk `root` until it has been covered *completely and without gaps*, then
/// return.
///
/// Normally that is one pass. It is two when the pass resumed from a
/// checkpoint, and that case is what makes a kill safe (ADR-0056):
///
/// A checkpoint is only ever left behind by a run that did not finish — the
/// process was killed, or the app quit and dropped the task. So the client was
/// *down* for some stretch afterwards, with no watcher running. The resumed
/// pass fast-forwards over the subtrees the earlier run completed, which is
/// what keeps a resume cheap — but a file created, changed or deleted in one
/// of those subtrees while the client was down was seen by nobody: not by the
/// dead watcher, and not by the pass that skipped it.
///
/// So a resumed pass is treated as provisional: it runs first, because it
/// reaches the not-yet-walked part of the root (and gets its uploads moving)
/// fastest, and is then followed by one full pass from the top with the
/// checkpoint cleared. The second pass is cheap for everything already in
/// sync — mtime + size match, nothing is re-hashed — and it is what turns
/// "resumes where it left off" into "no file is ever missed, whatever the app
/// was doing when it died".
async fn run_chunked_walk(
    db:       &Arc<LocalDb>,
    removes:  &Arc<ServerRemoves>,
    sync_now: &tokio::sync::Notify,
    root:     &SyncRootEntry,
) -> Result<()> {
    loop {
        // `false` = this pass started at the top and covered everything.
        if !walk_once(db, removes, sync_now, root).await? {
            return Ok(());
        }
        info!(
            "reconcile: '{}' resumed from a checkpoint — walking it again in full, \
             so changes made in the skipped subtrees while the client was down \
             cannot be missed",
            root.name,
        );
    }
}

/// One pass over the root. Returns whether it resumed from a checkpoint (and
/// therefore skipped subtrees it did not verify itself).
async fn walk_once(
    db:       &Arc<LocalDb>,
    removes:  &Arc<ServerRemoves>,
    sync_now: &tokio::sync::Notify,
    root:     &SyncRootEntry,
) -> Result<bool> {
    let base = Path::new(&root.local_path).to_path_buf();
    let checkpoint_key = format!("reconcile_checkpoint_{}", root.id);

    let mut checkpoint = Checkpoint::load(db.clone(), checkpoint_key);
    let resumed = checkpoint.resumed();
    if resumed {
        info!("reconcile: resuming '{}' from a prior checkpoint", root.name);
    }
    let matcher = ignore::IgnoreMatcher::from_patterns(&db.get_ignore_patterns().unwrap_or_default());

    // Deterministic top-level order so a checkpoint has a stable meaning
    // across runs. Sorted by the same NFC-normalised `rel` string the
    // checkpoint comparison below uses -- sorting by raw OsString
    // file_name() instead could disagree with it for an accented name
    // (NFC vs whatever the OS handed back), making the resume `<=`
    // check non-monotonic.
    let mut top_entries: Vec<(String, std::fs::DirEntry)> = match std::fs::read_dir(&base) {
        Ok(rd) => rd.filter_map(|e| e.ok())
            .filter_map(|e| {
                let rel = relative_path(&base, &e.path()).ok()?;
                Some((rel, e))
            })
            .collect(),
        Err(e) => {
            warn!("reconcile: read_dir failed for '{}': {e}", base.display());
            return Ok(false);
        }
    };
    top_entries.sort_by(|a, b| a.0.cmp(&b.0));

    let mut top_level_seen: std::collections::HashSet<String> = std::collections::HashSet::new();

    for (rel, entry) in top_entries {
        let child_path = entry.path();

        // Fast-forward past an already-completed subtree from a prior partial
        // run. Safe to skip its I/O entirely (not just its expensive work)
        // because that subtree's own deletion-detection already ran and
        // committed before the checkpoint was written — unlike a deferred,
        // whole-tree pass, there is nothing left pending for it to invalidate.
        // `Descend` is the partially-finished case: the previous run stopped
        // somewhere inside, so this one walks it while still skipping the
        // children it already did.
        if checkpoint.action(&rel) == Resume::Skip {
            top_level_seen.insert(rel);
            continue;
        }

        // Same subtree skip as inside `reconcile_directory`, for the top level
        // — where `$RECYCLE.BIN` and `System Volume Information` actually sit
        // when the root is a whole drive. Deliberately before the metadata
        // call: on those two, even `metadata()` can be refused.
        if !matcher.has_negations() && matcher.is_ignored_db_path(&rel, true)
            && child_path.is_dir()
        {
            continue;
        }

        let db_blk      = db.clone();
        let root_blk    = root.clone();
        let matcher_blk = matcher.clone();
        let removes_blk = removes.clone();
        let base_blk    = base.clone();
        let rel_blk     = rel.clone();
        let child_blk   = child_path.clone();

        // The checkpoint travels into the blocking chunk and back out: it is
        // written from inside the traversal (per directory, throttled), and
        // its fast-forward state has to survive to the next top-level entry.
        let (mark_seen, cp_back) = tokio::task::spawn_blocking(
            move || -> (Result<bool>, Checkpoint) {
                let mut cp = checkpoint;
                let outcome = (|| -> Result<bool> {
                    let meta = child_blk.metadata()?;
                    if meta.is_dir() {
                        let scope = WalkScope {
                            db: &db_blk, root: &root_blk, matcher: &matcher_blk,
                            removes: &removes_blk, base: &base_blk,
                        };
                        reconcile_directory(&scope, &mut cp, &rel_blk, &child_blk)
                    } else {
                        reconcile_one_file_entry(&db_blk, &root_blk, &matcher_blk, &base_blk,
                                                 &child_blk, &meta)
                    }
                })();
                (outcome, cp)
            },
        ).await?;
        checkpoint = cp_back;
        let mark_seen = mark_seen?;

        // Mirrors reconcile_directory's own "seen ⇔ not filtered out"
        // rule for its children -- a root-level file/dir that's
        // ignored/tmp/conflict/dehydrated must NOT be marked seen here
        // either, or a later genuine deletion of it would never be
        // detected by the root-scope scan below (it would look
        // "already seen" and get skipped before the exists() check).
        if mark_seen {
            top_level_seen.insert(rel.clone());
        }
        // A finished top-level entry always gets its checkpoint, unthrottled:
        // it is the coarsest and most valuable resume point there is.
        let _ = db.config_set(&checkpoint.key, &rel);
        checkpoint.last_write = std::time::Instant::now();

        // This subtree's rows are committed — wake the engine loop so their
        // upload starts now instead of after the last directory of the root
        // (ADR-0056). `Notify` holds a single permit, so nudging once per
        // subtree costs nothing when a cycle is already running.
        sync_now.notify_one();
    }

    // Root-level deletion-detection, now that every top-level entry has
    // been seen (freshly processed above, or fast-forwarded on resume).
    for db_path in db.list_immediate_children(&root.id, "")? {
        if top_level_seen.contains(&db_path) {
            continue;
        }
        let local_child = base.join(db_path.trim_start_matches('/'));
        if !local_child.exists() {
            // Absent because *we* removed it a moment ago, on the server's
            // orders — the pull writes that note before it deletes. Staging a
            // delete for it would send the removal back to the server as if
            // the user had made it, and for the far side of a rename that is
            // a delete of the file under its new name.
            if is_server_initiated_remove(removes, &root.id, &db_path) {
                continue;
            }
            info!("local deletion detected: {}", db_path);
            db.upsert_file(&root.id, &db_path, 0, 0, &[], "pending_delete", None)?;
        }
    }

    // Clear the checkpoint — a fully completed walk starts fresh next time.
    checkpoint.clear();
    Ok(resumed)
}

/// Run the whole walk to completion in one call, in-process — the
/// unresumable, unchunked convenience form. Pure over `(db, root)` so it can
/// be unit-tested without spinning up a `SyncEngine` (which the chunked
/// `spawn_blocking` driver in `SyncEngine::reconcile_local` needs — see
/// ADR-0037). Delegates straight to `reconcile_directory` at the root, which
/// carries both the per-entry classification and the per-directory
/// deletion-detection driving the chunked path too. Test-only: production
/// always goes through the chunked driver.
#[cfg(test)]
pub(crate) fn reconcile_local_inner(db: &LocalDb, root: &SyncRootEntry) -> Result<()> {
    if root.read_only {
        return Ok(());
    }
    let base = Path::new(&root.local_path);
    if !base.is_dir() {
        return Ok(());
    }
    let matcher = ignore::IgnoreMatcher::from_patterns(&db.get_ignore_patterns().unwrap_or_default());
    let removes = ServerRemoves::default();
    let scope = WalkScope { db, root, matcher: &matcher, removes: &removes, base };
    reconcile_directory(&scope, &mut Checkpoint::none(), "", base)?;
    Ok(())
}

/// What one walk of a root reads at every level but never changes — passed
/// down the recursion as one value instead of five.
#[derive(Clone, Copy)]
struct WalkScope<'a> {
    db:      &'a LocalDb,
    root:    &'a SyncRootEntry,
    matcher: &'a ignore::IgnoreMatcher,
    removes: &'a ServerRemoves,
    base:    &'a Path,
}

/// Recursively reconcile one directory: classify each immediate child (file
/// or subdirectory) via a single `metadata()` call — walkdir-style
/// `file_type()` can misreport reparse points on Windows, `metadata()` is
/// the one source of truth — stage the resulting DB change, recurse into
/// subdirectories, and once every immediate child has been seen, run
/// deletion-detection scoped to exactly this directory (`dir_path`) against
/// `LocalDb::list_immediate_children`. Descends into every subdirectory
/// regardless of its own ignore status, matching the non-subtree-skipping
/// semantics files already had — an ignored *directory* is simply never
/// itself staged or marked seen, same as an ignored file.
///
/// `dir_path` is `dir`'s own server-relative path (`""` for the sync root
/// itself, which is never staged as an entry — mirrors the old walk's
/// `min_depth(1)`). Returns whether the *caller* should mark `dir_path` seen
/// in its own scope: true only for a directory that turned out to be empty
/// and not ignored (matches the file-branch's "seen ⇔ not filtered out").
fn reconcile_directory(
    scope:      &WalkScope,
    checkpoint: &mut Checkpoint,
    dir_path:   &str,
    dir:        &Path,
) -> Result<bool> {
    let WalkScope { db, root, matcher, removes, base } = *scope;
    let mut seen_names = std::collections::HashSet::new();

    let entries = match std::fs::read_dir(dir) {
        Ok(rd) => rd,
        Err(e) => {
            warn!("reconcile: read_dir failed for {}: {e}", dir.display());
            return Ok(false);
        }
    };

    // Sorted, by the same NFC `rel` the checkpoint is compared against. Every
    // level needs a deterministic order, not just the top one: the resume
    // point is now a path at any depth, and "everything sorting before it is
    // done" is only true if each level is walked in that order. Memory stays
    // at one directory's width — the bound this walk already had.
    let mut children: Vec<(String, std::path::PathBuf)> = entries
        .filter_map(|e| e.ok())
        .filter_map(|e| {
            let path = e.path();
            let rel = relative_path(base, &path).ok()?;
            Some((rel, path))
        })
        .collect();
    children.sort_by(|a, b| a.0.cmp(&b.0));
    let any_entries = !children.is_empty();

    for (rel, child_path) in children {
        // Already done by an earlier run: skip whole, but mark seen so this
        // directory's deletion scan does not read it as gone.
        if checkpoint.action(&rel) == Resume::Skip {
            seen_names.insert(rel);
            continue;
        }

        let meta = match child_path.metadata() {
            Ok(m) => m,
            Err(e) => {
                warn!("reconcile: metadata failed for {}: {e}", child_path.display());
                continue;
            }
        };

        // An ignored directory is not descended into when no negation pattern
        // could rescue anything inside it. `$RECYCLE.BIN/` and `System Volume
        // Information/` ship in the defaults and are unreadable anyway, so a
        // root on a whole drive spent its walk on access-denied warnings for
        // trees whose every entry would have been discarded.
        //
        // Not marked seen, exactly as before — that is what keeps the parent's
        // deletion scan from reading the directory as gone. And skipping the
        // subtree also skips staging deletes *inside* it, which matches what
        // an exclusion means (ADR-0020): local→server staging is suppressed,
        // uploads and deletes alike.
        if meta.is_dir() && !matcher.has_negations() && matcher.is_ignored_db_path(&rel, true) {
            continue;
        }

        let mark_seen = if meta.is_dir() {
            reconcile_directory(scope, checkpoint, &rel, &child_path)?
        } else {
            reconcile_one_file_entry(db, root, matcher, base, &child_path, &meta)?
        };
        // Completed — this is the resume point a restart picks up from, at
        // whatever depth it happens to be (throttled, see `record`).
        checkpoint.record(&rel);
        if mark_seen {
            seen_names.insert(rel);
        }
    }

    // Scoped deletion-detection: any DB row that's an immediate child of
    // this directory but wasn't just seen, and no longer exists on disk, is
    // a local deletion. Re-stat right before acting — a bug in the diff
    // above must never be the sole gate for deleting real data.
    for db_path in db.list_immediate_children(&root.id, dir_path)? {
        if seen_names.contains(&db_path) {
            continue;
        }
        let local_child = base.join(db_path.trim_start_matches('/'));
        if !local_child.exists() {
            // See the identical check in `walk_once`: a path the pull just
            // removed on the server's orders is not a local deletion, and
            // since the walk runs alongside the pull (ADR-0056) it can now
            // read a directory in exactly that moment.
            if is_server_initiated_remove(removes, &root.id, &db_path) {
                continue;
            }
            info!("local deletion detected: {}", db_path);
            db.upsert_file(&root.id, &db_path, 0, 0, &[], "pending_delete", None)?;
        }
    }

    if any_entries {
        return Ok(false); // non-empty: never independently staged (server-side implicit via children)
    }
    if dir_path.is_empty() {
        return Ok(false); // the sync root itself is never staged as an entry
    }
    if matcher.is_ignored_db_path(dir_path, true) {
        return Ok(false);
    }

    if let Ok(Some(known)) = db.get_file(&root.id, dir_path) {
        if known.is_dir && known.sync_status == "synced" {
            return Ok(true);
        }
        if known.is_dir && known.sync_status == "pending" {
            info!("reconcile: dir already pending: {}", dir_path);
            return Ok(true);
        }
    }
    info!("reconcile: staging empty dir for upload: {}", dir_path);
    db.upsert_dir(&root.id, dir_path, "pending")?;
    Ok(true)
}

/// Classify one file entry and stage the resulting DB change. Returns
/// whether the caller should mark it seen (false for a tmp/`.conflict.`/
/// dehydrated/ignored file — mirrors the pre-ADR-0037 walk's "only insert
/// into seen_paths once past every filter" order exactly).
fn reconcile_one_file_entry(
    db:         &LocalDb,
    root:       &SyncRootEntry,
    matcher:    &ignore::IgnoreMatcher,
    base:       &Path,
    local_path: &Path,
    meta:       &std::fs::Metadata,
) -> Result<bool> {
    if local_path.extension().and_then(|e| e.to_str()) == Some("tmp") {
        return Ok(false);
    }
    if local_path.to_string_lossy().contains(".conflict.") {
        return Ok(false);
    }
    if cloud_files::is_dehydrated(local_path) {
        return Ok(false);
    }

    let rel = match relative_path(base, local_path) {
        Ok(r) => r,
        Err(_) => return Ok(false),
    };
    if matcher.is_ignored_db_path(&rel, false) {
        // Settle excluded files in Explorer: mark them in-sync + pin
        // so the Status column shows "available" instead of the misleading
        // "sync pending". Best-effort, Windows-only, idempotent.
        if let Err(e) = cloud_files::mark_excluded(local_path, &rel) {
            warn!("mark_excluded failed for {}: {e:#}", local_path.display());
        }
        return Ok(false);
    }

    // Leave open conflicts untouched: only an explicit resolve closes them.
    // Otherwise this pass would re-mark the row 'synced' once the on-disk
    // file matches the server, silently dropping the conflict (and orphaning
    // its .conflict copy).
    if let Ok(Some(known)) = db.get_file(&root.id, &rel) {
        if known.sync_status == "conflict" {
            return Ok(true);
        }
    }

    let size  = meta.len() as i64;
    let mtime = meta.modified()
        .unwrap_or(UNIX_EPOCH)
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs() as i64;

    if let Ok(Some(known)) = db.get_file(&root.id, &rel) {
        if known.sync_status == "synced"
            && known.size  == size
            && known.mtime == mtime
        {
            return Ok(true);
        }
    }

    let hash = match hash_file(local_path) {
        Ok(h) => h,
        Err(e) => {
            let ft = meta.file_type();
            warn!(
                "cannot hash '{rel}': {e} \
                 (meta is_file={}, is_dir={}, is_symlink={}, len={}, readonly={})",
                ft.is_file(), ft.is_dir(), ft.is_symlink(),
                meta.len(), meta.permissions().readonly()
            );
            return Ok(true);
        }
    };

    if let Ok(Some(known)) = db.get_file(&root.id, &rel) {
        if known.server_hash.as_deref() == Some(hash.as_slice()) {
            let _ = db.upsert_file(
                &root.id, &rel, size, mtime, &hash, "synced",
                known.server_hash.as_deref(),
            );
            return Ok(true);
        }
    }

    db.upsert_file(&root.id, &rel, size, mtime, &hash, "pending", None)?;
    Ok(true)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::{LocalDb, SyncRootEntry};
    use std::path::PathBuf;

    struct TempDir(PathBuf);
    impl TempDir {
        fn new() -> Self {
            let p = std::env::temp_dir()
                .join(format!("priva-sync-test-{}", uuid::Uuid::new_v4()));
            std::fs::create_dir_all(&p).unwrap();
            Self(p)
        }
        fn path(&self) -> &Path { &self.0 }
    }
    impl Drop for TempDir {
        fn drop(&mut self) { let _ = std::fs::remove_dir_all(&self.0); }
    }

    fn test_root(id: &str, local_path: &Path) -> SyncRootEntry {
        SyncRootEntry {
            id:             id.to_string(),
            name:           "test".to_string(),
            local_path:     local_path.to_string_lossy().to_string(),
            server_root_id: "srv-1".to_string(),
            download_mode:  "on_demand".to_string(),
            read_only:      false,
            paused:         false,
            kind:           crate::db::ROOT_KIND_OWNED.to_string(),
        }
    }

    /// Regression for a bug an advisor-agent review caught before this
    /// shipped: `run_chunked_walk`'s top-level loop used to discard
    /// `reconcile_directory`/`reconcile_one_file_entry`'s `bool` return and
    /// mark every top-level entry "seen" unconditionally.
    ///
    /// The exploit window is *intra-pass*, not across separate walks: each
    /// top-level entry is processed in its own `spawn_blocking` call, and
    /// the root-scope deletion-scan only runs once every entry in that
    /// single `run_chunked_walk` call has gone through the loop -- which,
    /// for a large root, is real wall-clock time later. If an *ignored*
    /// top-level file is processed early and then genuinely deleted before
    /// the scan runs (still within the same pass), the buggy code's
    /// unconditional "seen" would hide it forever. A naive test that calls
    /// the loop, deletes the file, then calls the loop *again* would not
    /// catch this: a second call's fresh `read_dir` simply wouldn't
    /// enumerate an already-deleted file either way, bug or no bug -- so
    /// this reuses the *same* `top_level_seen` across a simulated
    /// process-then-delete-then-scan sequence, exactly mirroring the real
    /// single-pass timeline.
    ///
    /// Nested levels never had this bug (`reconcile_directory` already
    /// respected the bool for its own children -- see
    /// `deletion_check_never_deletes_an_unseen_file_that_still_exists`);
    /// this was a top-level-only asymmetry. `run_chunked_walk` itself is a
    /// `SyncEngine` method and untestable without a live engine (ADR-0024).
    #[test]
    fn root_level_ignored_file_deleted_mid_pass_is_still_detected() {
        let tmp = TempDir::new();
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(&root_path).unwrap();
        let file_path = root_path.join("scratch.bak");
        std::fs::write(&file_path, b"junk").unwrap();

        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        db.set_ignore_patterns("*.bak").unwrap();
        let root = test_root("root-1", &root_path);
        let matcher = ignore::IgnoreMatcher::from_patterns(&db.get_ignore_patterns().unwrap());

        // Row predates the ignore pattern, as if synced before the user
        // excluded it.
        db.upsert_file(&root.id, "/scratch.bak", 4, 1, b"h", "synced", Some(b"h")).unwrap();

        // Phase 1 -- mirrors the "process now" branch while the file still
        // exists. An ignored file must not be marked seen.
        let meta = file_path.metadata().unwrap();
        let mark_seen = reconcile_one_file_entry(&db, &root, &matcher, &root_path, &file_path, &meta).unwrap();
        assert!(!mark_seen, "an ignored file must not be marked seen");
        let mut top_level_seen = std::collections::HashSet::new();
        if mark_seen {
            top_level_seen.insert("/scratch.bak".to_string());
        }

        // Phase 2 -- the race: something deletes the file after it was
        // processed but before the deletion-scan runs.
        std::fs::remove_file(&file_path).unwrap();

        // Phase 3 -- mirrors the root-scope deletion-scan, reusing THIS
        // pass's top_level_seen (not a freshly rebuilt one).
        for db_path in db.list_immediate_children(&root.id, "").unwrap() {
            if top_level_seen.contains(&db_path) { continue; }
            let local_child = root_path.join(db_path.trim_start_matches('/'));
            if !local_child.exists() {
                db.upsert_file(&root.id, &db_path, 0, 0, &[], "pending_delete", None).unwrap();
            }
        }

        let row = db.get_file(&root.id, "/scratch.bak").unwrap().unwrap();
        assert_eq!(row.sync_status, "pending_delete",
            "an ignored file deleted mid-pass must still be detected -- the bug this guards \
             against: unconditionally marking it \"seen\" when processed would make this \
             permanently invisible even though it correctly reflects real on-disk state by \
             the time the scan runs");
    }

    /// Tracer bullet: an empty directory on disk must be captured by
    /// reconcile_local as a pending-upload entry with is_dir=true.
    #[test]
    fn reconcile_local_stages_empty_directory_as_pending_upload() {
        let tmp = TempDir::new();
        let db_path   = tmp.path().join("db.sqlite");
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(&root_path).unwrap();
        std::fs::create_dir_all(root_path.join("empty-folder")).unwrap();

        let db = LocalDb::open(&db_path).unwrap();
        let root = test_root("root-1", &root_path);
        db.upsert_sync_root(
            &root.id, &root.name, &root.local_path,
            &root.server_root_id, &root.download_mode, root.read_only, &root.kind,
        ).unwrap();

        reconcile_local_inner(&db, &root).unwrap();

        let files = db.list_all_files(&root.id).unwrap();
        let dir = files.iter().find(|f| f.path == "/empty-folder")
            .expect("leeres Verzeichnis sollte im DB-Index erscheinen");
        assert!(dir.is_dir, "is_dir muss true sein fuer ein Verzeichnis");
        assert_eq!(dir.sync_status, "pending");
    }

    /// The core reason for the hierarchical redesign (ADR-0037): a deletion
    /// nested two levels deep must be found by the per-directory scoped
    /// diff, without ever loading the whole root's file list.
    #[test]
    fn reconcile_local_detects_a_nested_deletion() {
        let tmp = TempDir::new();
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(root_path.join("a/b")).unwrap();
        std::fs::write(root_path.join("a/b/keep.txt"), b"keep").unwrap();

        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        let root = test_root("root-1", &root_path);

        // First pass adopts the current tree, including the row that will
        // then be deleted from disk.
        reconcile_local_inner(&db, &root).unwrap();
        db.upsert_file(&root.id, "/a/b/gone.txt", 4, 1, b"h", "synced", Some(b"h")).unwrap();

        reconcile_local_inner(&db, &root).unwrap();

        let gone = db.get_file(&root.id, "/a/b/gone.txt").unwrap().unwrap();
        assert_eq!(gone.sync_status, "pending_delete",
            "a file removed two levels deep must still be detected as deleted");
        let keep = db.get_file(&root.id, "/a/b/keep.txt").unwrap().unwrap();
        assert_ne!(keep.sync_status, "pending_delete", "the sibling that's still on disk must be untouched");
    }

    /// A directory that goes from "has content" to "empty" (its only file was
    /// removed) must still get a re-scan: the just-vacated directory's own
    /// deletion-check runs on this pass and finds no immediate children left,
    /// so it becomes eligible to be staged as an empty dir on the next one.
    /// Guards the recursion boundary between a directory's own emptiness
    /// check and its deletion-scan of former children.
    #[test]
    fn reconcile_local_handles_a_directory_that_became_empty() {
        let tmp = TempDir::new();
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(root_path.join("sub")).unwrap();
        std::fs::write(root_path.join("sub/only.txt"), b"x").unwrap();

        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        let root = test_root("root-1", &root_path);
        reconcile_local_inner(&db, &root).unwrap();
        assert!(db.get_file(&root.id, "/sub/only.txt").unwrap().is_some());

        std::fs::remove_file(root_path.join("sub/only.txt")).unwrap();
        reconcile_local_inner(&db, &root).unwrap();

        let only = db.get_file(&root.id, "/sub/only.txt").unwrap().unwrap();
        assert_eq!(only.sync_status, "pending_delete");
        // /sub itself is now empty on disk -- this pass must stage it.
        let sub = db.get_file(&root.id, "/sub").unwrap()
            .expect("now-empty directory must be staged");
        assert!(sub.is_dir);
        assert_eq!(sub.sync_status, "pending");
    }

    /// Nothing under an ignored directory is ever staged. With no negation
    /// pattern in the set the walk does not even descend into it any more
    /// (see the test below); with one, it descends and filters per entry.
    #[test]
    fn a_file_under_an_ignored_directory_is_never_staged() {
        let tmp = TempDir::new();
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(root_path.join("build")).unwrap();
        std::fs::write(root_path.join("build/output.bin"), b"data").unwrap();

        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        db.set_ignore_patterns("build/").unwrap();
        let root = test_root("root-1", &root_path);

        reconcile_local_inner(&db, &root).unwrap();

        assert!(
            db.get_file(&root.id, "/build/output.bin").unwrap().is_none(),
            "a file under an ignored directory must not be staged",
        );
    }

    /// The skip is not just an optimisation, it changes one observable thing:
    /// a DB row inside an ignored directory is no longer read as a local
    /// deletion. That matches what an exclusion means (ADR-0020) — staging
    /// local→server is suppressed for uploads *and* deletes — and it is what
    /// keeps a drive root from spending its walk inside `$RECYCLE.BIN`.
    #[test]
    fn an_ignored_subtree_is_not_scanned_for_deletions() {
        let tmp = TempDir::new();
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(root_path.join("build")).unwrap();

        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        db.set_ignore_patterns("build/").unwrap();
        let root = test_root("root-1", &root_path);
        // Synced before the pattern existed, and gone from disk since.
        db.upsert_file(&root.id, "/build/old.bin", 1, 1, b"h", "synced", None).unwrap();

        reconcile_local_inner(&db, &root).unwrap();

        assert_ne!(
            db.get_file(&root.id, "/build/old.bin").unwrap().unwrap().sync_status,
            "pending_delete",
            "an excluded subtree stages nothing at all, deletions included",
        );
    }

    /// Simulates a crash mid-walk: only the first (sorted) top-level entry
    /// is processed before "crashing"; a "restarted" walk resumes from that
    /// checkpoint, skipping the completed subtree's I/O entirely rather than
    /// just its expensive work -- safe here because each subtree's own
    /// deletion-detection already committed when it finished (ADR-0037),
    /// unlike a deferred whole-tree pass.
    #[test]
    fn resumed_walk_skips_the_completed_top_level_subtree_but_stays_correct() {
        let tmp = TempDir::new();
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(&root_path).unwrap();
        std::fs::write(root_path.join("a.txt"), b"hello").unwrap(); // sorts first
        std::fs::create_dir_all(root_path.join("b_dir")).unwrap();
        std::fs::write(root_path.join("b_dir/nested.txt"), b"hello").unwrap();
        std::fs::write(root_path.join("c.txt"), b"hello").unwrap();

        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        let root = test_root("root-1", &root_path);

        // Simulate "crashed after completing the first top-level entry":
        // persist a checkpoint for /a.txt without ever running the real walk.
        db.config_set(&format!("reconcile_checkpoint_{}", root.id), "/a.txt").unwrap();
        db.upsert_file(&root.id, "/a.txt", 5, 1, b"h", "synced", Some(b"h")).unwrap();

        // A resumed run must still process b_dir/ and c.txt to completion.
        // reconcile_local_inner doesn't resume (it's the unchunked form), so
        // this exercises the resume path directly via the same primitives
        // run_chunked_walk uses, without needing a live SyncEngine (ADR-0024).
        let matcher = ignore::IgnoreMatcher::from_patterns("");
        let checkpoint = db.config_get(&format!("reconcile_checkpoint_{}", root.id)).unwrap();
        assert_eq!(checkpoint.as_deref(), Some("/a.txt"));

        // Everything at or before the checkpoint is skipped; everything
        // after is processed normally -- mirrors run_chunked_walk's loop.
        let mut entries: Vec<_> = std::fs::read_dir(&root_path).unwrap().filter_map(|e| e.ok()).collect();
        entries.sort_by_key(std::fs::DirEntry::file_name);
        let mut skip_until = checkpoint;
        for entry in entries {
            let path = entry.path();
            let rel = relative_path(&root_path, &path).unwrap();
            if let Some(cp) = skip_until.as_deref() {
                if rel.as_str() <= cp { continue; }
                skip_until = None;
            }
            let meta = path.metadata().unwrap();
            if meta.is_dir() {
                let removes = ServerRemoves::default();
                let scope = WalkScope {
                    db: &db, root: &root, matcher: &matcher, removes: &removes, base: &root_path,
                };
                reconcile_directory(&scope, &mut Checkpoint::none(), &rel, &path).unwrap();
            } else {
                reconcile_one_file_entry(&db, &root, &matcher, &root_path, &path, &meta).unwrap();
            }
        }

        assert!(db.get_file(&root.id, "/b_dir/nested.txt").unwrap().is_some(),
            "the directory after the checkpoint must still be walked");
        assert!(db.get_file(&root.id, "/c.txt").unwrap().is_some(),
            "the file after the checkpoint must still be staged");
        // The skipped entry must be untouched, not re-hashed or re-marked.
        let a = db.get_file(&root.id, "/a.txt").unwrap().unwrap();
        assert_eq!(a.sync_status, "synced", "the fast-forwarded entry must not be reprocessed");
    }

    /// The actual backstop against a diff/scoping bug: a DB row that's never
    /// marked seen must still not be deleted while the file genuinely exists
    /// on disk. Demonstrated via a real ignored file rather than an
    /// artificial injection -- an ignored file is the one case the normal
    /// walk *already* produces "not seen but exists" for (its row predates
    /// the ignore pattern, as if synced before the user excluded it), so
    /// this proves the re-stat guard is what's actually preventing deletion
    /// here, not happenstance in the diff.
    #[test]
    fn deletion_check_never_deletes_an_unseen_file_that_still_exists() {
        let tmp = TempDir::new();
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(&root_path).unwrap();
        std::fs::write(root_path.join("old.bak"), b"still here").unwrap();

        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        let root = test_root("root-1", &root_path);
        db.upsert_file(&root.id, "/old.bak", 10, 1, b"h", "synced", Some(b"h")).unwrap();
        db.set_ignore_patterns("*.bak").unwrap();

        reconcile_local_inner(&db, &root).unwrap();

        let row = db.get_file(&root.id, "/old.bak").unwrap().unwrap();
        assert_ne!(row.sync_status, "pending_delete",
            "a file the diff never marks seen must still survive if it's actually still on \
             disk -- this is the re-stat guard, the real backstop against a scoping/diff bug");
    }

    /// A file whose on-disk name is NFD-decomposed (as macOS/APFS's readdir
    /// returns) must reconcile against an NFC-stored DB row as the *same*
    /// file, not as a new file plus a phantom deletion of a row that never
    /// existed on disk in that form. `relative_path()`'s NFC normalisation
    /// (paths.rs, pre-dating this session) is what makes this work; this
    /// test exercises it through the actual walk rather than trusting it in
    /// isolation.
    #[test]
    fn reconcile_local_normalises_nfd_filenames_to_match_nfc_db_rows() {
        let tmp = TempDir::new();
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(&root_path).unwrap();

        // "café.txt" -- NFD form: e + combining acute accent (U+0065
        // U+0301), vs. NFC's single precomposed U+00E9. Both render
        // identically; only the byte sequence differs.
        let nfd_name = "cafe\u{0301}.txt";
        let nfc_name = "caf\u{00E9}.txt";
        assert_ne!(nfd_name, nfc_name, "sanity check: these really are different byte sequences");

        std::fs::write(root_path.join(nfd_name), b"hello").unwrap();

        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        let root = test_root("root-1", &root_path);

        reconcile_local_inner(&db, &root).unwrap();

        let expected_path = format!("/{nfc_name}");
        assert!(db.get_file(&root.id, &expected_path).unwrap().is_some(),
            "walked file must be stored under its NFC-normalised path");

        // A second pass (simulating the safety-net re-walk) must not treat
        // the same on-disk file as a phantom deletion just because its NFD
        // path string doesn't literally match the stored NFC one.
        reconcile_local_inner(&db, &root).unwrap();
        let row = db.get_file(&root.id, &expected_path).unwrap().unwrap();
        assert_ne!(row.sync_status, "pending_delete",
            "NFD-vs-NFC mismatch must not manifest as a phantom deletion");
    }

    /// Empirical scale check for `list_immediate_children`'s scoping query: a
    /// directory with many siblings must all be correctly diffed, and one
    /// genuine deletion among many must still be found -- not proof of
    /// million-file behavior, but a real bump from the handful-of-files
    /// fixtures used elsewhere in this module.
    #[test]
    fn reconcile_local_handles_a_wide_directory_correctly() {
        let tmp = TempDir::new();
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(&root_path).unwrap();
        const N: usize = 2000;
        for i in 0..N {
            std::fs::write(root_path.join(format!("f{i:05}.txt")), b"x").unwrap();
        }

        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        let root = test_root("root-1", &root_path);
        reconcile_local_inner(&db, &root).unwrap();

        let files = db.list_all_files(&root.id).unwrap();
        assert_eq!(files.len(), N, "every file in a wide directory must be staged");

        // Remove one, in the middle of the range, and confirm the next pass
        // finds exactly that one deletion among 2000 siblings.
        std::fs::remove_file(root_path.join("f01000.txt")).unwrap();
        reconcile_local_inner(&db, &root).unwrap();

        let gone = db.get_file(&root.id, "/f01000.txt").unwrap().unwrap();
        assert_eq!(gone.sync_status, "pending_delete");
        let deleted_count = db.list_all_files(&root.id).unwrap()
            .iter().filter(|f| f.sync_status == "pending_delete").count();
        assert_eq!(deleted_count, 1,
            "exactly one deletion among 2000 siblings must be detected, no false positives");
    }

    /// The recursive per-directory design (ADR-0037) uses real Rust
    /// call-stack recursion, unlike the old flat walkdir iterator (which
    /// manages its own stack internally) -- a pathologically deep tree
    /// could in principle overflow the stack. Path-length limits bound
    /// realistic nesting long before stack depth would in practice, but
    /// this empirically checks a depth well beyond anything a real sync
    /// root would have. Kept short enough (numeric single/double-digit
    /// segment names) to stay well under Windows' classic MAX_PATH, so a
    /// failure here means the recursion itself, not an unrelated OS limit.
    #[test]
    fn reconcile_local_survives_deep_nesting_without_stack_overflow() {
        let tmp = TempDir::new();
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(&root_path).unwrap();

        const DEPTH: usize = 40;
        let mut leaf = root_path.clone();
        for i in 0..DEPTH {
            leaf = leaf.join(i.to_string());
        }
        std::fs::create_dir_all(&leaf).unwrap();
        std::fs::write(leaf.join("deep.txt"), b"x").unwrap();

        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        let root = test_root("root-1", &root_path);

        reconcile_local_inner(&db, &root).unwrap();

        let rel = "/".to_string()
            + &(0..DEPTH).map(|i| i.to_string()).collect::<Vec<_>>().join("/")
            + "/deep.txt";
        assert!(db.get_file(&root.id, &rel).unwrap().is_some(),
            "a file {DEPTH} directories deep must still be reconciled");
    }

    /// A file matching the global ignore patterns is never staged for upload,
    /// while a sibling that does not match is staged as usual.
    #[test]
    fn reconcile_local_skips_ignored_files() {
        let tmp = TempDir::new();
        let db_path   = tmp.path().join("db.sqlite");
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(&root_path).unwrap();
        std::fs::write(root_path.join("scratch.bak"), b"junk").unwrap();
        std::fs::write(root_path.join("doc.txt"), b"keep").unwrap();

        let db = LocalDb::open(&db_path).unwrap();
        db.set_ignore_patterns("*.bak").unwrap();
        let root = test_root("root-1", &root_path);

        reconcile_local_inner(&db, &root).unwrap();

        let files = db.list_all_files(&root.id).unwrap();
        assert!(
            files.iter().all(|f| f.path != "/scratch.bak"),
            "ignored *.bak file must not be staged, got {:?}",
            files.iter().map(|f| &f.path).collect::<Vec<_>>()
        );
        let doc = files.iter().find(|f| f.path == "/doc.txt")
            .expect("non-ignored file must still be staged");
        assert_eq!(doc.sync_status, "pending");
    }

    /// On a fresh client (no saved patterns) the built-in default seed is
    /// active: a known OS-junk file (Thumbs.db) is not staged, while a normal
    /// file is. Guards against the default ever silently going empty.
    #[test]
    fn reconcile_local_applies_default_patterns_when_unset() {
        let tmp = TempDir::new();
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(&root_path).unwrap();
        std::fs::write(root_path.join("Thumbs.db"), b"junk").unwrap();
        std::fs::write(root_path.join("doc.txt"), b"keep").unwrap();

        // No set_ignore_patterns call → defaults must apply.
        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        let root = test_root("root-1", &root_path);

        reconcile_local_inner(&db, &root).unwrap();

        let files = db.list_all_files(&root.id).unwrap();
        assert!(
            files.iter().all(|f| f.path != "/Thumbs.db"),
            "default-excluded Thumbs.db must not be staged, got {:?}",
            files.iter().map(|f| &f.path).collect::<Vec<_>>()
        );
        assert!(files.iter().any(|f| f.path == "/doc.txt"),
            "a normal file must still be staged under the defaults");
    }

    /// An open conflict row must survive a reconcile pass untouched. The normal
    /// sync path used to clobber it to 'synced' (dropping the .conflict copy
    /// reference) the moment the on-disk file matched the server — which made
    /// every open conflict silently vanish on the next cycle while the
    /// `.conflict.` files were left orphaned on disk.
    #[test]
    fn reconcile_local_preserves_open_conflict() {
        let tmp = TempDir::new();
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(&root_path).unwrap();
        let content = b"server-version";
        let fpath = root_path.join("c.txt");
        std::fs::write(&fpath, content).unwrap();

        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        let root = test_root("root-1", &root_path);

        // Simulate a conflict whose on-disk file already holds the server
        // version (as handle_conflict leaves it): server_hash == content_hash,
        // row marked 'conflict' with a stored loser-copy path.
        let hash  = hash_file(&fpath).unwrap();
        let meta  = std::fs::metadata(&fpath).unwrap();
        let mtime = meta.modified().unwrap().duration_since(UNIX_EPOCH).unwrap().as_secs() as i64;
        db.upsert_file("root-1", "/c.txt", content.len() as i64, mtime, &hash, "synced", Some(&hash)).unwrap();
        db.set_conflict("root-1", "/c.txt", "E:/x/c.conflict.20260101_000000.txt").unwrap();

        reconcile_local_inner(&db, &root).unwrap();

        let f = db.get_file("root-1", "/c.txt").unwrap().unwrap();
        assert_eq!(f.sync_status, "conflict", "open conflict must not be auto-cleared");
        assert_eq!(
            db.conflict_copy_path("root-1", "/c.txt").unwrap().as_deref(),
            Some("E:/x/c.conflict.20260101_000000.txt"),
            "conflict-copy reference must survive a reconcile pass",
        );
    }

    // ── Crash safety (ADR-0056) ──────────────────────────────────────────────

    /// The guarantee behind the detached walk: kill the client mid-walk, and
    /// the next run still finds *everything* — including changes made in the
    /// subtrees the earlier run had already finished, while nobody was
    /// watching.
    ///
    /// Simulates exactly that timeline: a checkpoint left behind by a killed
    /// run, then a file appearing inside the completed subtree before the
    /// client comes back. The resumed pass skips that subtree by design (that
    /// is what makes a resume cheap), so only the full pass that follows it
    /// can catch the file.
    #[tokio::test]
    async fn a_file_added_while_the_client_was_dead_is_found_after_a_resumed_walk() {
        let tmp = TempDir::new();
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(root_path.join("done")).unwrap();
        std::fs::create_dir_all(root_path.join("todo")).unwrap();
        std::fs::write(root_path.join("todo/later.txt"), b"later").unwrap();

        let db   = Arc::new(LocalDb::open(tmp.path().join("db.sqlite")).unwrap());
        let root = test_root("root-1", &root_path);

        // The killed run had finished '/done' and written its checkpoint.
        db.config_set(&format!("reconcile_checkpoint_{}", root.id), "/done").unwrap();

        // …and then, with the client down, a file landed inside it.
        std::fs::write(root_path.join("done/added-while-down.txt"), b"new").unwrap();

        let removes  = Arc::new(ServerRemoves::default());
        let sync_now = tokio::sync::Notify::new();
        run_chunked_walk(&db, &removes, &sync_now, &root).await.unwrap();

        assert!(
            db.get_file(&root.id, "/done/added-while-down.txt").unwrap().is_some(),
            "the file in the skipped subtree must be staged by the full pass \
             that follows a resumed one",
        );
        assert!(
            db.get_file(&root.id, "/todo/later.txt").unwrap().is_some(),
            "and the part the resumed pass itself walked is staged too",
        );
        assert_eq!(
            db.config_get(&format!("reconcile_checkpoint_{}", root.id)).unwrap().as_deref(),
            Some(""),
            "a completed walk leaves no checkpoint behind",
        );
    }

    /// The second pass only exists to close the resume gap — a walk that
    /// started at the top has no gap, and must not walk the root twice.
    #[tokio::test]
    async fn a_walk_without_a_checkpoint_runs_exactly_once() {
        let tmp = TempDir::new();
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(&root_path).unwrap();
        std::fs::write(root_path.join("a.txt"), b"a").unwrap();

        let db   = Arc::new(LocalDb::open(tmp.path().join("db.sqlite")).unwrap());
        let root = test_root("root-1", &root_path);

        let removes  = Arc::new(ServerRemoves::default());
        let sync_now = tokio::sync::Notify::new();
        // `walk_once` reports whether it resumed; a fresh walk must say no,
        // which is what stops `run_chunked_walk` after one pass.
        let resumed = walk_once(&db, &removes, &sync_now, &root).await.unwrap();
        assert!(!resumed);
    }

    /// A path the server-pull just removed is not a local deletion. The walk
    /// runs alongside the pull now, so it can read a directory in exactly the
    /// window between the note and the pull's own DB update.
    #[tokio::test]
    async fn a_server_initiated_removal_is_not_staged_as_a_local_delete() {
        let tmp = TempDir::new();
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(root_path.join("docs")).unwrap();

        let db   = Arc::new(LocalDb::open(tmp.path().join("db.sqlite")).unwrap());
        let root = test_root("root-1", &root_path);
        // Both rows are tracked; neither file is on disk any more.
        db.upsert_file(&root.id, "/docs/pulled-away.txt", 1, 1, b"h", "synced", None).unwrap();
        db.upsert_file(&root.id, "/docs/really-gone.txt", 1, 1, b"h", "synced", None).unwrap();

        let removes = Arc::new(ServerRemoves::default());
        removes.lock().unwrap().insert(
            (root.id.clone(), "/docs/pulled-away.txt".to_string()),
            std::time::Instant::now(),
        );

        let sync_now = tokio::sync::Notify::new();
        walk_once(&db, &removes, &sync_now, &root).await.unwrap();

        assert_ne!(
            db.get_file(&root.id, "/docs/pulled-away.txt").unwrap().unwrap().sync_status,
            "pending_delete",
            "the pull removed this one — sending the deletion back would delete \
             the server's copy (or, after a rename, the file under its new name)",
        );
        assert_eq!(
            db.get_file(&root.id, "/docs/really-gone.txt").unwrap().unwrap().sync_status,
            "pending_delete",
            "a genuine local deletion right next to it still goes through",
        );
    }

    // ── Checkpoint-Auflösung ────────────────────────────────────────────────

    /// The resume rule, on its own. `Descend` is the case the old per-top-level
    /// checkpoint had no answer for: a root whose content lives under one
    /// folder never completed that folder, so every restart began it again.
    #[test]
    fn resume_skips_finished_siblings_and_descends_into_the_partial_branch() {
        let mut cp = Checkpoint::none();
        cp.resume_at = Some("/Persoenlich/Bilder/2006".to_string());

        // The branch the previous run stopped inside: walk it, keep skipping.
        assert_eq!(cp.action("/Persoenlich"), Resume::Descend);
        assert_eq!(cp.action("/Persoenlich/Bilder"), Resume::Descend);
        // Done before the stop: skipped whole.
        assert_eq!(cp.action("/Persoenlich/Bilder/2005"), Resume::Skip);
        assert_eq!(cp.action("/Persoenlich/Bilder/2006"), Resume::Skip);
        // First entry past it — and fast-forwarding ends here.
        assert_eq!(cp.action("/Persoenlich/Bilder/2007"), Resume::Walk);
        assert!(cp.resume_at.is_none(), "past the checkpoint, nothing is skipped again");
        assert_eq!(cp.action("/Persoenlich/Bilder/2005"), Resume::Walk);
    }

    /// A sibling of the partial branch that sorts after it has not been seen
    /// yet — `/a` being partially done says nothing about `/aa`.
    #[test]
    fn resume_does_not_skip_a_sibling_that_only_looks_like_a_prefix() {
        let mut cp = Checkpoint::none();
        cp.resume_at = Some("/a/b".to_string());

        assert_eq!(cp.action("/a"), Resume::Descend);
        assert_eq!(cp.action("/aa"), Resume::Walk, "'/aa' is not inside '/a'");
    }

    /// Without a stored position everything is walked — the ordinary case.
    #[test]
    fn a_fresh_checkpoint_walks_everything() {
        let mut cp = Checkpoint::none();
        assert_eq!(cp.action("/whatever"), Resume::Walk);
        assert_eq!(cp.action("/aaa"), Resume::Walk);
    }

    /// End to end through the real walk: a run that stopped inside a subtree
    /// resumes there and stages what it had not reached, instead of starting
    /// the subtree over.
    #[tokio::test]
    async fn a_resumed_walk_reaches_unexplored_files_without_redoing_the_subtree() {
        let tmp = TempDir::new();
        let root_path = tmp.path().join("root");
        // One dominant top-level entry — the shape that made the old
        // checkpoint useless on a drive root.
        std::fs::create_dir_all(root_path.join("data/a")).unwrap();
        std::fs::create_dir_all(root_path.join("data/b")).unwrap();
        std::fs::write(root_path.join("data/a/done.txt"), b"done").unwrap();
        std::fs::write(root_path.join("data/b/new.txt"), b"new").unwrap();

        let db   = Arc::new(LocalDb::open(tmp.path().join("db.sqlite")).unwrap());
        let root = test_root("root-1", &root_path);
        // The killed run got as far as /data/a.
        db.config_set(&format!("reconcile_checkpoint_{}", root.id), "/data/a").unwrap();

        let removes  = Arc::new(ServerRemoves::default());
        let sync_now = tokio::sync::Notify::new();
        let resumed  = walk_once(&db, &removes, &sync_now, &root).await.unwrap();

        assert!(resumed, "a stored position means this pass resumed");
        assert!(
            db.get_file(&root.id, "/data/b/new.txt").unwrap().is_some(),
            "the part beyond the checkpoint is staged",
        );
        assert!(
            db.get_file(&root.id, "/data/a/done.txt").unwrap().is_none(),
            "the part before it is skipped, not re-staged — that is what makes \
             a resume cheap; the full pass that follows covers it",
        );
    }
}
