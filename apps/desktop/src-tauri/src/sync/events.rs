//! Watcher event → pending-row staging.
//!
//! The watcher fires Modified/Deleted/Renamed events for files inside any sync
//! root. Methods here translate those into rows in `local_files` with the
//! appropriate `sync_status`, so the next sync cycle picks them up.

use std::path::Path;
use std::time::UNIX_EPOCH;
use anyhow::Result;
use tracing::{debug, error, info, warn};

use crate::cloud_files;
use crate::db::{LocalDb, SyncRootEntry};
use crate::watcher::FileEvent;
use super::{SyncEngine, hash_file, should_attempt_server_move, should_mark_disappeared, root_accepts_local_mutations};
use super::ignore::IgnoreMatcher;
use super::paths::{find_root, relative_path};
use super::transfer_decide::{is_staged_for_upload, upload_is_settled};

/// Which staging function a watcher event routes to. Exists so `stage_event`
/// names the four cases once and `stage_one` owns the shared shape —
/// find the root, stage, report failure, enter the lane.
#[derive(Debug, Clone, Copy)]
enum StageKind { Upload, Delete, DirCreated }

impl std::fmt::Display for StageKind {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(match self {
            Self::Upload     => "upload",
            Self::Delete     => "delete",
            Self::DirCreated => "dir-created",
        })
    }
}

impl SyncEngine {
    /// Top-level watcher entry point. Renames inside the same sync root attempt
    /// a server-side move first to preserve history; everything else falls
    /// through to plain staging + a sync nudge.
    pub(super) async fn handle_file_event(&self, roots: &[SyncRootEntry], event: FileEvent) {
        if let FileEvent::Renamed { ref from, ref to } = event {
            if let (Some(root_from), Some(root_to)) =
                (find_root(roots, from), find_root(roots, to))
            {
                // Read-only mounts never push a server-side move; the reconcile
                // cycle restores the server layout instead (ADR-0011).
                if root_from.id == root_to.id && !root_accepts_local_mutations(root_from) {
                    return;
                }
                if root_from.id == root_to.id {
                    if let (Ok(from_rel), Ok(to_rel)) = (
                        relative_path(Path::new(&root_from.local_path), from),
                        relative_path(Path::new(&root_to.local_path), to),
                    ) {
                        // Only attempt a server-side move when the source is
                        // confirmed `synced` server-side. For pending / pending_delete
                        // / unknown sources the upload either never reached the
                        // server or is mid-flight — move_file would 404 and the
                        // fallback delete+upload duplicates the file (phantom
                        // re-appear after copy+rename).
                        if !should_attempt_server_move(&self.db, &root_from.id, &from_rel) {
                            // Source was never confirmed synced, but its upload
                            // may already be on the wire. Keep the from-row as
                            // `pending_delete` so a late upload result is not
                            // persisted and the delete pass removes the orphan
                            //; the destination is staged as a fresh upload.
                            warn!(
                                "rename '{from_rel}' → '{to_rel}': source not synced \
                                 server-side — skipping server move, staging \
                                 destination as fresh upload"
                            );
                            let _ = self.db.mark_pending_delete(&root_from.id, &from_rel);
                            // Only the destination is staged, so this goes
                            // through the same funnel as any other upload
                            // rather than re-implementing it.
                            self.stage_one(roots, to, StageKind::Upload);
                            self.sync_now.notify_one();
                            return;
                        }
                        let server_id = root_from.server_root_id.clone();
                        let root_id   = root_from.id.clone();
                        // Send the last server_version we know so the server can
                        // detect a move-move race.
                        let known_version = self.db
                            .get_file(&root_id, &from_rel)
                            .ok()
                            .flatten()
                            .map(|f| f.server_version)
                            .unwrap_or(-1);
                        let is_mount = root_from.is_mount();
                        let (sid, from, to) = (&server_id, &from_rel, &to_rel);
                        let moved = self.api
                            .call(|snap| async move {
                                snap.move_file(sid, from, to, known_version, is_mount).await
                            })
                            .await;
                        match moved {
                            Ok(crate::api::MoveOutcome::Moved) => {
                                let _ = self.db.rename_file(&root_id, &from_rel, &to_rel);
                                info!("server move: {from_rel} → {to_rel}");
                                self.sync_now.notify_one();
                                return;
                            }
                            Ok(crate::api::MoveOutcome::Conflict { server_version, current_path, conflict_path }) => {
                                // Another client moved or edited the file in parallel.
                                // The server has placed a conflict copy at to_path and
                                // pushed SSE conflict_added; the next sync cycle will
                                // pull the canonical state.
                                warn!("move conflict: '{from_rel}' -> '{to_rel}' \
                                       (server current='{current_path}' v={server_version}, \
                                       conflict_copy={conflict_path:?})");
                                self.sync_now.notify_one();
                                return;
                            }
                            Err(e) => {
                                warn!("server move failed, falling back to delete+upload: {e}");
                            }
                        }
                    }
                }
            }
        }

        self.stage_event(roots, event);
        self.sync_now.notify_one();
    }

    /// Pure staging — writes pending status to DB without triggering a sync.
    /// Used both from `handle_file_event` and from the debounce drain.
    ///
    /// Everything staged here lands in the upload queue's priority lane
    /// (ADR-0059): these are changes the user just made on disk, and they must
    /// not queue behind the millions of rows a full walk may be staging in
    /// parallel (ADR-0056).
    pub(super) fn stage_event(&self, roots: &[SyncRootEntry], event: FileEvent) {
        match event {
            FileEvent::Modified(path)   => self.stage_one(roots, &path, StageKind::Upload),
            FileEvent::Deleted(path)    => self.stage_one(roots, &path, StageKind::Delete),
            FileEvent::DirCreated(path) => self.stage_one(roots, &path, StageKind::DirCreated),
            FileEvent::Renamed { from, to } => {
                self.stage_one(roots, &from, StageKind::Delete);
                self.stage_one(roots, &to,   StageKind::Upload);
            }
        }
    }

    /// Stage one path and, if that wrote a queued row, enter it into the
    /// priority lane. The single place where a watcher event becomes queue
    /// state, so "everything the watcher reports goes into the lane" is one
    /// line of code rather than an invariant spread over four match arms.
    fn stage_one(&self, roots: &[SyncRootEntry], path: &Path, kind: StageKind) {
        let Some(root) = find_root(roots, path) else { return };
        let staged = match kind {
            StageKind::Upload     => stage_upload(&self.db, root, path),
            StageKind::Delete     => self.stage_delete(root, path),
            StageKind::DirCreated => stage_dir_created(&self.db, root, path),
        };
        match staged {
            Ok(Some(rel)) => self.enter_lane(&root.id, &rel),
            // Staging declined to write a row — an ignored path, a dehydrated
            // placeholder, content already matching the server, a file still
            // being written. Nothing to raise, and nothing wrong.
            Ok(None)      => {}
            Err(e) if vanished_before_staging(&e) => {
                debug!("stage {kind} skipped for '{}': gone again before it could be read", path.display());
            }
            Err(e)        => error!("stage {kind} failed for '{}': {e}", path.display()),
        }
    }

    /// Enter a freshly staged row into the upload queue's priority lane
    /// (ADR-0059) and, when that is a genuine arrival, tell any running upload
    /// batch that it now has something worth cutting itself short for.
    pub(super) fn enter_lane(&self, root_id: &str, rel: &str) {
        match self.db.set_priority(root_id, rel) {
            Ok(true)  => { self.lane_arrivals.fetch_add(1, std::sync::atomic::Ordering::Relaxed); }
            Ok(false) => {}
            Err(e)    => warn!("priority lane update failed for '{rel}': {e}"),
        }
    }

    /// Stage a local deletion — DB only, no network call. Skipped when the
    /// entire sync-root directory is gone: that's not a per-file delete but a
    /// folder removal, handled by the sync cycle's missing-root check.
    ///
    /// Returns the staged row's root-relative path, or `None` for each of the
    /// reasons this declines to stage. The caller needs those apart: only a
    /// row that was actually written can enter the priority lane, and the
    /// path it returns is the one this already computed.
    fn stage_delete(&self, root: &SyncRootEntry, path: &Path) -> Result<Option<String>> {
        // Read-only mounts never delete server-side; reconcile re-downloads
        // anything removed locally (ADR-0011).
        if !root_accepts_local_mutations(root) {
            return Ok(None);
        }
        if !Path::new(&root.local_path).is_dir() {
            return Ok(None);
        }
        let rel = relative_path(Path::new(&root.local_path), path)?;
        // Global exclusions: an ignored path is never synced, so there is
        // no server-side copy to delete — drop the watcher delete event.
        if IgnoreMatcher::from_patterns(&self.db.get_ignore_patterns().unwrap_or_default())
            .is_ignored_db_path(&rel, false)
        {
            return Ok(None);
        }
        // Wenn reconcile die Datei gerade selbst entfernt hat (Server-Delete),
        // ist dies kein echter lokaler Löschwunsch — Watcher-Event ignorieren.
        if self.consume_server_initiated_remove(&root.id, &rel) {
            info!("stage_delete: skip '{rel}' (server-initiated remove)");
            return Ok(None);
        }
        // A row with a known identity might be the near side of a rename
        // (ADR-0063): don't tell the server anything yet, just mark it gone
        // from this path. The next sync cycle's join resolves it against a
        // freshly staged path, or -- surviving a cycle without a partner --
        // falls back to `pending_delete` itself.
        if should_mark_disappeared(&self.db, &root.id, &rel) {
            self.db.mark_disappeared(&root.id, &rel)?;
        } else {
            self.db.upsert_file(&root.id, &rel, 0, 0, &[], "pending_delete", None)?;
        }
        Ok(Some(rel))
    }
}

/// Whether staging failed only because the file was removed between the
/// watcher's event and the read — a lock or scratch file that lives for
/// milliseconds. Nothing is lost: the removal arrives as its own event.
fn vanished_before_staging(e: &anyhow::Error) -> bool {
    e.chain().any(|cause| {
        cause.downcast_ref::<std::io::Error>()
            .is_some_and(|io| io.kind() == std::io::ErrorKind::NotFound)
    })
}

/// Stage a local file for upload — DB only, no network call.
/// Skips atomic-download temp files, our own conflict copies, dehydrated
/// placeholders, and content whose hash already matches the known
/// server-side hash.
///
/// A free function over `&LocalDb` (not a `SyncEngine` method) for the same
/// reason as [`stage_dir_created`]: it needs nothing else off the engine, and
/// this way its filter order — which decides what gets read off disk — is
/// unit-testable without constructing an `AppHandle` and an API client.
pub(super) fn stage_upload(db: &LocalDb, root: &SyncRootEntry, path: &Path) -> Result<Option<String>> {
    // Read-only mounts produce no pending uploads — a local edit on a
    // can_write=false mount is discarded by the next reconcile (ADR-0011).
    if !root_accepts_local_mutations(root) {
        return Ok(None);
    }
    if path.extension().and_then(|e| e.to_str()) == Some("tmp") {
        return Ok(None);
    }
    if path.to_string_lossy().contains(".conflict.") {
        return Ok(None);
    }
    // A dehydrated placeholder holds no local bytes, so there is nothing
    // to stage — and reading it to find that out would recall the whole
    // file over the network. Same guard, same position in the filter
    // order, as the walk's `reconcile_one_file_entry` (walk.rs).
    if cloud_files::is_dehydrated(path) {
        return Ok(None);
    }
    let rel  = relative_path(Path::new(&root.local_path), path)?;
    // Global exclusions: never stage a matching file for upload.
    if IgnoreMatcher::from_patterns(&db.get_ignore_patterns().unwrap_or_default())
        .is_ignored_db_path(&rel, false)
    {
        return Ok(None);
    }
    let meta = path.metadata()?;
    let size = meta.len() as i64;
    let mtime = meta.modified()
        .unwrap_or(UNIX_EPOCH)
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs() as i64;

    // Cheap fast-path before the expensive one, mirroring the walk: a row
    // already `synced` whose size and mtime still match cannot have
    // changed content, so it needs no hash. Ordering matters — the
    // hash-based check below used to run *after* `hash_file`, which meant
    // every watcher event paid a full read of the file even when the
    // answer was "nothing to do".
    if let Ok(Some(known)) = db.get_file(&root.id, &rel) {
        if known.sync_status == "synced" && known.size == size && known.mtime == mtime {
            return Ok(None);
        }
        // Already staged and the file has moved on since — it is still being
        // written. The row already says "pending", so nothing is learned by
        // hashing a fragment that the next write invalidates; a copy of a large
        // file emits write events throughout, and hashing on each of them reads
        // the growing file over and over. Refresh only the two fields the
        // settle check in `process_uploads` compares against.
        if is_staged_for_upload(&known.sync_status)
            && !upload_is_settled(known.size, known.mtime, size, mtime)
        {
            db.set_staged_metadata(&root.id, &rel, size, mtime)?;
            // Still a queued live change, so it still belongs in the lane —
            // a file the walk staged first would otherwise stay in the bulk
            // queue for the whole copy. Re-entering an already-raised row is
            // a no-op that reports no arrival, so this cannot make a running
            // batch preempt itself on every write event.
            return Ok(Some(rel));
        }
    }

    let hash = hash_file(path)?;

    // Re-read rather than reuse the row above: hashing a large file takes
    // long enough for a download to have landed meanwhile, and that write
    // is exactly what this check is meant to notice.
    if let Ok(Some(known)) = db.get_file(&root.id, &rel) {
        if known.server_hash.as_deref() == Some(hash.as_slice()) {
            return Ok(None);
        }
    }

    db.upsert_file(&root.id, &rel, size, mtime, &hash, "pending", None)?;
    // Lazily fill the identity that later lets a disappeared row be
    // recognised as this file's move source (ADR-0063). Best-effort: a
    // filesystem that gives none just leaves today's behaviour in place.
    if let Ok(identity) = crate::identity::read(path) {
        let _ = db.set_file_identity(&root.id, &rel, &identity);
    }
    Ok(Some(rel))
}

/// Stage a newly created, currently-empty directory — DB only, no network
/// call. Only truly empty directories get an explicit row: one with content
/// syncs implicitly via its files' own upload events, mirroring
/// `reconcile_local`'s dir-pass (mod.rs) and closing the watcher-side gap
/// that pass covered alone before ADR-0037 made the periodic walk rare.
///
/// A free function (not a `SyncEngine` method) taking `&LocalDb` directly,
/// like `reconcile_local_inner` (sync/walk.rs) / `adopt_server_files`
/// (mod.rs) — so it's unit-testable without constructing a full
/// `SyncEngine` (AppHandle, API client, …), which nothing in this crate's
/// test suite does today.
pub(super) fn stage_dir_created(db: &LocalDb, root: &SyncRootEntry, path: &Path) -> Result<Option<String>> {
    if !root_accepts_local_mutations(root) {
        return Ok(None);
    }
    let rel = relative_path(Path::new(&root.local_path), path)?;
    // Global exclusions: never stage a matching directory for upload.
    if IgnoreMatcher::from_patterns(&db.get_ignore_patterns().unwrap_or_default())
        .is_ignored_db_path(&rel, true)
    {
        return Ok(None);
    }
    let is_empty = std::fs::read_dir(path)
        .map(|mut entries| entries.next().is_none())
        .unwrap_or(false);
    if !is_empty {
        return Ok(None);
    }
    // A directory the pull side just created is already known — staging it
    // would push it straight back to the server it came from. The walk
    // (`reconcile_directory`) has always made this check; the watcher path
    // did not, so every server-side directory arrived, fired DirCreated
    // while still empty, and went out again as a `create_directory` upload.
    // Those uploads are also what made a pure download cycle's toast read
    // "hochgeladen".
    if let Ok(Some(known)) = db.get_file(&root.id, &rel) {
        if known.is_dir && matches!(known.sync_status.as_str(), "synced" | "pending") {
            return Ok(None);
        }
    }
    db.upsert_dir(&root.id, &rel, "pending")?;
    // Lazily fill the identity that later lets a disappeared directory row
    // be recognised as this folder's move source (ADR-0063).
    if let Ok(identity) = crate::identity::read(path) {
        let _ = db.set_file_identity(&root.id, &rel, &identity);
    }
    Ok(Some(rel))
}

#[cfg(test)]
mod tests {
    use super::*;

    struct TempDir(std::path::PathBuf);
    impl TempDir {
        fn new() -> Self {
            let p = std::env::temp_dir()
                .join(format!("priva-sync-events-test-{}", uuid::Uuid::new_v4()));
            std::fs::create_dir_all(&p).unwrap();
            Self(p)
        }
    }
    impl Drop for TempDir {
        fn drop(&mut self) { let _ = std::fs::remove_dir_all(&self.0); }
    }

    // A file that is gone again before staging reads it is not a
    // failure. The watcher reports its removal on its own. The name must not
    // match a default ignore pattern (e.g. `*.kil`, `*.ldb`): ignored paths
    // return early with `Ok(None)` before the file is ever touched.
    #[test]
    fn a_file_removed_before_staging_counts_as_vanished_not_failed() {
        let tmp = TempDir::new();
        let db = crate::db::LocalDb::open(tmp.0.join("db.sqlite")).unwrap();
        let root = test_root(&tmp.0);

        let err = stage_upload(&db, &root, &tmp.0.join("gone.dat"))
            .expect_err("nothing to read");
        assert!(vanished_before_staging(&err), "{err:#}");

        let denied = anyhow::Error::new(std::io::Error::from(std::io::ErrorKind::PermissionDenied));
        assert!(!vanished_before_staging(&denied), "only a vanished file is benign");
    }

    fn test_root(local_path: &Path) -> SyncRootEntry {
        SyncRootEntry {
            id:             "root-1".to_string(),
            name:           "test".to_string(),
            local_path:     local_path.to_string_lossy().to_string(),
            server_root_id: "srv-1".to_string(),
            download_mode:  "on_demand".to_string(),
            read_only:      false,
            paused:         false,
            kind:           crate::db::ROOT_KIND_OWNED.to_string(),
        }
    }

    #[test]
    fn stages_an_empty_directory_as_pending() {
        let tmp = TempDir::new();
        let db = crate::db::LocalDb::open(tmp.0.join("db.sqlite")).unwrap();
        let root = test_root(&tmp.0);
        db.upsert_sync_root(
            &root.id, &root.name, &root.local_path,
            &root.server_root_id, &root.download_mode, root.read_only, &root.kind,
        ).unwrap();

        let dir = tmp.0.join("empty-sub");
        std::fs::create_dir_all(&dir).unwrap();

        stage_dir_created(&db, &root, &dir).expect("stage_dir_created ok");

        let rec = db.get_file(&root.id, "/empty-sub").unwrap().expect("db row");
        assert!(rec.is_dir, "row must be marked is_dir");
        assert_eq!(rec.sync_status, "pending");
    }

    /// A directory that already has content by the time we look must NOT get
    /// an explicit row — it syncs implicitly via its files' own events, and a
    /// redundant dir row would duplicate what reconcile_local's dir-pass
    /// already deliberately excludes for non-empty directories.
    #[test]
    fn skips_a_directory_that_already_has_content() {
        let tmp = TempDir::new();
        let db = crate::db::LocalDb::open(tmp.0.join("db.sqlite")).unwrap();
        let root = test_root(&tmp.0);
        db.upsert_sync_root(
            &root.id, &root.name, &root.local_path,
            &root.server_root_id, &root.download_mode, root.read_only, &root.kind,
        ).unwrap();

        let dir = tmp.0.join("filled-sub");
        std::fs::create_dir_all(&dir).unwrap();
        std::fs::write(dir.join("a.txt"), b"x").unwrap();

        stage_dir_created(&db, &root, &dir).expect("stage_dir_created ok");

        assert!(db.get_file(&root.id, "/filled-sub").unwrap().is_none(),
            "a non-empty directory must not get an explicit row");
    }

    /// Regression: the pull side creates a server-side directory locally and
    /// records it as `synced`; the watcher then fires DirCreated for that same
    /// directory while it is still empty. Staging it would push it straight
    /// back to the server it just came from — and those spurious
    /// `create_directory` uploads are what made a pure download cycle's OS
    /// notification read "hochgeladen".
    #[test]
    fn skips_a_directory_the_pull_side_already_recorded() {
        let tmp = TempDir::new();
        let db = crate::db::LocalDb::open(tmp.0.join("db.sqlite")).unwrap();
        let root = test_root(&tmp.0);
        db.upsert_sync_root(
            &root.id, &root.name, &root.local_path,
            &root.server_root_id, &root.download_mode, root.read_only, &root.kind,
        ).unwrap();

        let dir = tmp.0.join("from-server");
        std::fs::create_dir_all(&dir).unwrap();
        db.upsert_dir(&root.id, "/from-server", "synced").unwrap();

        stage_dir_created(&db, &root, &dir).expect("stage_dir_created ok");

        let rec = db.get_file(&root.id, "/from-server").unwrap().expect("db row");
        assert_eq!(rec.sync_status, "synced",
            "a directory already known as synced must not be re-staged for upload");
    }

    /// A dir already queued for upload stays queued — re-staging it would
    /// only churn the row, and the guard must not accidentally clear it.
    #[test]
    fn leaves_an_already_pending_directory_alone() {
        let tmp = TempDir::new();
        let db = crate::db::LocalDb::open(tmp.0.join("db.sqlite")).unwrap();
        let root = test_root(&tmp.0);
        db.upsert_sync_root(
            &root.id, &root.name, &root.local_path,
            &root.server_root_id, &root.download_mode, root.read_only, &root.kind,
        ).unwrap();

        let dir = tmp.0.join("queued");
        std::fs::create_dir_all(&dir).unwrap();
        db.upsert_dir(&root.id, "/queued", "pending").unwrap();

        stage_dir_created(&db, &root, &dir).expect("stage_dir_created ok");

        let rec = db.get_file(&root.id, "/queued").unwrap().expect("db row");
        assert_eq!(rec.sync_status, "pending");
        assert!(rec.is_dir);
    }

    /// A renamed folder's `To` half needs its OS identity on record before
    /// the next cycle's join can recognise it (ADR-0063), the same as a
    /// freshly staged file.
    #[test]
    fn stages_an_empty_directory_with_its_identity() {
        let tmp = TempDir::new();
        let db = crate::db::LocalDb::open(tmp.0.join("db.sqlite")).unwrap();
        let root = test_root(&tmp.0);
        db.upsert_sync_root(
            &root.id, &root.name, &root.local_path,
            &root.server_root_id, &root.download_mode, root.read_only, &root.kind,
        ).unwrap();

        let dir = tmp.0.join("empty-sub");
        std::fs::create_dir_all(&dir).unwrap();

        stage_dir_created(&db, &root, &dir).expect("stage_dir_created ok");

        let rec = db.get_file(&root.id, "/empty-sub").unwrap().expect("db row");
        assert_eq!(rec.file_identity, Some(crate::identity::read(&dir).unwrap()));
    }

    #[test]
    fn read_only_mount_never_stages_a_dir() {
        let tmp = TempDir::new();
        let db = crate::db::LocalDb::open(tmp.0.join("db.sqlite")).unwrap();
        let mut root = test_root(&tmp.0);
        root.read_only = true;
        root.kind = crate::db::ROOT_KIND_MOUNT.to_string();
        db.upsert_sync_root(
            &root.id, &root.name, &root.local_path,
            &root.server_root_id, &root.download_mode, root.read_only, &root.kind,
        ).unwrap();

        let dir = tmp.0.join("sub");
        std::fs::create_dir_all(&dir).unwrap();

        stage_dir_created(&db, &root, &dir).expect("stage_dir_created ok");

        assert!(db.get_file(&root.id, "/sub").unwrap().is_none());
    }
    /// The whole point of the reordering: a row that is already `synced` and
    /// whose size + mtime are unchanged must be answered from the DB alone.
    /// The on-disk bytes here deliberately do NOT match the recorded hash --
    /// if the function still hashed the file first, it would see a mismatch
    /// and stage an upload. Staying `synced` is the proof that it did not
    /// read the file, which on Windows is what keeps a dehydrated
    /// placeholder from being recalled over the network.
    #[test]
    fn a_synced_row_with_unchanged_size_and_mtime_is_answered_without_hashing() {
        let tmp = TempDir::new();
        let db = crate::db::LocalDb::open(tmp.0.join("db.sqlite")).unwrap();
        let root = test_root(&tmp.0);
        db.upsert_sync_root(
            &root.id, &root.name, &root.local_path,
            &root.server_root_id, &root.download_mode, root.read_only, &root.kind,
        ).unwrap();

        let file = tmp.0.join("a.txt");
        std::fs::write(&file, b"on-disk bytes").unwrap();
        let meta  = file.metadata().unwrap();
        let mtime = meta.modified().unwrap()
            .duration_since(UNIX_EPOCH).unwrap().as_secs() as i64;

        db.upsert_file(
            &root.id, "/a.txt", meta.len() as i64, mtime,
            b"not-the-real-hash", "synced", Some(b"not-the-real-hash"),
        ).unwrap();

        stage_upload(&db, &root, &file).expect("stage_upload ok");

        assert_eq!(
            db.get_file(&root.id, "/a.txt").unwrap().unwrap().sync_status,
            "synced",
            "the size/mtime fast-path must short-circuit before the hash",
        );
    }

    /// A changed file still has to be staged -- the fast-path must not
    /// swallow real edits. mtime is forced apart from the recorded one so the
    /// check cannot pass by coincidence on a fast filesystem.
    #[test]
    fn a_file_whose_size_changed_is_staged_as_pending() {
        let tmp = TempDir::new();
        let db = crate::db::LocalDb::open(tmp.0.join("db.sqlite")).unwrap();
        let root = test_root(&tmp.0);
        db.upsert_sync_root(
            &root.id, &root.name, &root.local_path,
            &root.server_root_id, &root.download_mode, root.read_only, &root.kind,
        ).unwrap();

        let file = tmp.0.join("b.txt");
        std::fs::write(&file, b"much longer content than before").unwrap();
        db.upsert_file(&root.id, "/b.txt", 3, 0, b"old", "synced", Some(b"old")).unwrap();

        stage_upload(&db, &root, &file).expect("stage_upload ok");

        let rec = db.get_file(&root.id, "/b.txt").unwrap().unwrap();
        assert_eq!(rec.sync_status, "pending");
        assert_eq!(rec.size, "much longer content than before".len() as i64);
    }

    /// Content reverted to exactly what the server holds: size/mtime differ,
    /// so the fast-path does not fire, but the hash comparison behind it
    /// still recognises there is nothing to upload.
    #[test]
    fn content_matching_the_server_hash_is_not_staged() {
        let tmp = TempDir::new();
        let db = crate::db::LocalDb::open(tmp.0.join("db.sqlite")).unwrap();
        let root = test_root(&tmp.0);
        db.upsert_sync_root(
            &root.id, &root.name, &root.local_path,
            &root.server_root_id, &root.download_mode, root.read_only, &root.kind,
        ).unwrap();

        let file = tmp.0.join("c.txt");
        std::fs::write(&file, b"server content").unwrap();
        let server_hash = super::super::hash_file(&file).unwrap();
        db.upsert_file(&root.id, "/c.txt", 1, 1, &server_hash, "synced", Some(&server_hash)).unwrap();

        stage_upload(&db, &root, &file).expect("stage_upload ok");

        assert_eq!(
            db.get_file(&root.id, "/c.txt").unwrap().unwrap().sync_status,
            "synced",
            "matching the server hash must not queue an upload",
        );
    }

    #[test]
    fn read_only_mount_never_stages_a_file() {
        let tmp = TempDir::new();
        let db = crate::db::LocalDb::open(tmp.0.join("db.sqlite")).unwrap();
        let mut root = test_root(&tmp.0);
        root.read_only = true;
        root.kind = crate::db::ROOT_KIND_MOUNT.to_string();
        db.upsert_sync_root(
            &root.id, &root.name, &root.local_path,
            &root.server_root_id, &root.download_mode, root.read_only, &root.kind,
        ).unwrap();

        let file = tmp.0.join("d.txt");
        std::fs::write(&file, b"x").unwrap();

        stage_upload(&db, &root, &file).expect("stage_upload ok");

        assert!(db.get_file(&root.id, "/d.txt").unwrap().is_none());
    }

    /// A file that is still being copied in emits a write event per burst.
    /// The row is already pending, so the only thing worth recording is the
    /// new size/mtime — re-reading the growing file to hash a fragment that
    /// the next write invalidates is pure waste.
    #[test]
    fn a_pending_file_that_grew_is_refreshed_without_rehashing() {
        let tmp = TempDir::new();
        let db = crate::db::LocalDb::open(tmp.0.join("db.sqlite")).unwrap();
        let root = test_root(&tmp.0);
        db.upsert_sync_root(
            &root.id, &root.name, &root.local_path,
            &root.server_root_id, &root.download_mode, root.read_only, &root.kind,
        ).unwrap();

        let file = tmp.0.join("big.bin");
        std::fs::write(&file, b"partial").unwrap();
        stage_upload(&db, &root, &file).expect("stage_upload ok");

        let staged = db.get_file(&root.id, "/big.bin").unwrap().expect("db row");
        assert_eq!(staged.sync_status, "pending");
        let staged_hash = staged.content_hash.clone();

        std::fs::write(&file, b"partial-plus-a-lot-more").unwrap();
        stage_upload(&db, &root, &file).expect("stage_upload ok");

        let grown = db.get_file(&root.id, "/big.bin").unwrap().expect("db row");
        assert_eq!(grown.sync_status, "pending");
        assert_eq!(grown.size, 23, "size must track the file so the settle check can compare");
        assert_eq!(grown.content_hash, staged_hash,
            "the hash is deliberately left stale — the upload path re-hashes");
    }

    /// The plain case must keep its fast path: a first sighting stages a real
    /// hash, so a file that lands in one write is ready to upload immediately.
    #[test]
    fn a_first_sighting_stages_the_real_hash() {
        let tmp = TempDir::new();
        let db = crate::db::LocalDb::open(tmp.0.join("db.sqlite")).unwrap();
        let root = test_root(&tmp.0);
        db.upsert_sync_root(
            &root.id, &root.name, &root.local_path,
            &root.server_root_id, &root.download_mode, root.read_only, &root.kind,
        ).unwrap();

        let file = tmp.0.join("small.txt");
        std::fs::write(&file, b"done").unwrap();
        stage_upload(&db, &root, &file).expect("stage_upload ok");

        let rec = db.get_file(&root.id, "/small.txt").unwrap().expect("db row");
        assert_eq!(rec.sync_status, "pending");
        assert_eq!(rec.content_hash, blake3::hash(b"done").as_bytes().to_vec());
    }

    /// The join a later sync cycle runs (ADR-0063) needs a freshly staged
    /// path to carry its OS file identity, so a disappeared row can be
    /// matched against it. Staging is the "touch" that fills it in.
    #[test]
    fn a_first_sighting_records_the_files_identity() {
        let tmp = TempDir::new();
        let db = crate::db::LocalDb::open(tmp.0.join("db.sqlite")).unwrap();
        let root = test_root(&tmp.0);
        db.upsert_sync_root(
            &root.id, &root.name, &root.local_path,
            &root.server_root_id, &root.download_mode, root.read_only, &root.kind,
        ).unwrap();

        let file = tmp.0.join("small.txt");
        std::fs::write(&file, b"done").unwrap();
        stage_upload(&db, &root, &file).expect("stage_upload ok");

        let rec = db.get_file(&root.id, "/small.txt").unwrap().expect("db row");
        assert_eq!(rec.file_identity, Some(crate::identity::read(&file).unwrap()));
    }
}
