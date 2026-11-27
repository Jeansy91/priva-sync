use anyhow::Result;
use std::path::Path;
use std::sync::Arc;
use std::time::{Duration, Instant, UNIX_EPOCH};
use tauri::{AppHandle, Emitter};
use tokio::sync::RwLock;
use tracing::warn;

use crate::api::{ApiClient, AuthedApi};
use crate::db::{LocalDb, SyncRootEntry};

pub(crate) mod chunked;
pub mod commands;
pub mod conflict_resolve;
pub(crate) mod delta;
mod download;
mod engine_emit;
mod engine_loop;
mod engine_placeholders;
mod events;
mod failure;
pub(crate) mod ignore;
pub mod mount_commands;
mod notify;
mod paths;
mod placeholder_pass;
mod predicates;
mod reconcile;
mod reconcile_decide;
mod reconcile_incoming_delete;
mod resolve_disappeared;
mod retry;
mod retry_decide;
mod snapshot;
pub(crate) mod snapshot_store;
mod transfer;
mod transfer_decide;
mod upload;
mod walk;

pub(crate) use chunked::{upload_file_chunked, ChunkedUploadRequest};
pub(crate) use predicates::{
    delete_reached_server, match_disappeared_to_staged, mount_read_only_changes,
    root_accepts_local_mutations, root_still_on_server, should_attempt_server_move,
    should_mark_disappeared, should_persist_upload_result, sync_root_name_changes, MoveMatch,
};

// Re-export for the test module + other crates that referenced the old paths.
pub(crate) use paths::remove_local_entry;

const POLL_INTERVAL: Duration = Duration::from_secs(30);
/// Minimum gap between transfer-summary OS notifications. Prevents toast
/// spam when many sync_now bursts land back-to-back.
const TRANSFER_NOTIFY_THROTTLE: Duration = Duration::from_secs(60);
/// How long to wait after the first file event before kicking off a sync.
/// This batches rapid bursts (e.g. saving a file that triggers multiple events).
const DEBOUNCE_MS: u64 = 250;
/// Page size for the owned-root changes-feed (ADR-0036). Matches the
/// server's default limit so a first page never gets silently truncated
/// smaller than what the caller expected.
const CHANGES_PAGE_LIMIT: u32 = 1000;
/// Paths per stat request in the `failed_server_changes` retry sweep
/// (ADR-0062) -- matches the server's own cap (`filesStatMaxPaths` in
/// `apps/server/internal/api/handler/files.go`) so a full sweep never trips
/// the server's 400. A root-wide outage with 12,691 queued retries costs
/// ~26 requests at this size instead of one per row.
pub(super) const RETRY_STAT_BATCH_SIZE: usize = 500;
/// Periodic full local walk (ADR-0037) -- backstop for OS watcher event
/// loss (e.g. a kernel event-queue overflow during a burst). The live
/// watcher is the primary change-detection path; this only catches what it
/// might have missed. Deliberately far less frequent than POLL_INTERVAL --
/// an unmeasured starting value, calibration is a follow-up.
const SAFETY_NET_INTERVAL: Duration = Duration::from_secs(20 * 60);

/// If the fraction of pending-delete files in a sync cycle exceeds this value,
/// pause the deletes and ask the user to confirm before proceeding.
pub(super) const BULK_DELETE_THRESHOLD: f64 = 0.30;
/// Don't trigger the bulk-delete guard for tiny roots (avoid false positives).
pub(super) const BULK_DELETE_MIN_FILES: usize = 5;
/// Absolute fallback for the bulk-delete guard (ADR-0064): the 30% ratio
/// scales in the wrong direction — a million-file root needs 300k deletions
/// to trip it, exactly where a false negative costs the most. A raw count
/// past this closes that gap regardless of root size. A rough figure, not a
/// measurement: comfortably above a normal bulk rename/move, comfortably
/// below what a very large root can legitimately delete in a day.
pub(super) const BULK_DELETE_ABSOLUTE_THRESHOLD: usize = 5000;
/// How many file uploads/deletes run concurrently (default; overridden by DB config).
pub(super) const UPLOAD_CONCURRENCY_DEFAULT: usize = 3;
/// How many pending rows one cycle takes on. A first sync stages the whole
/// root as pending — and with the walk now running detached (ADR-0056), it
/// keeps staging while the uploads run — so the queue is read in batches
/// instead of materialised whole. What a cycle leaves behind it re-nudges
/// itself for, so the batch bounds memory, not throughput.
pub(super) const UPLOAD_BATCH_LIMIT: usize = 2_000;
/// How often, in pending rows examined, a running batch looks whether a live
/// change has entered the priority lane since it started (ADR-0059). Serving
/// the lane first only helps at batch boundaries; on a first sync a batch of
/// `UPLOAD_BATCH_LIMIT` files can run for minutes, and a change made in that
/// window would wait it out. This is the granularity at which a batch cuts
/// itself short instead.
pub(super) const PRIORITY_YIELD_EVERY: usize = 100;
/// How many files one `run_uploads` call takes on, so that the same check can
/// also cut the *transfer* half of a batch short (ADR-0059). The pre-pass
/// hashes, the transfer sends; a batch spends real time in both, and a
/// preemption that only worked in the pre-pass would leave a live change
/// waiting behind up to `UPLOAD_BATCH_LIMIT` uploads. `run_uploads` is built
/// for repeated calls — it carries the progress tally across them.
pub(super) const UPLOAD_RUN_CHUNK: usize = 200;
/// Wie oft der Nenner der Fortschrittsanzeige waehrend eines Upload-Batches
/// neu bestimmt wird. Der entkoppelte Walk staged in dieselbe Warteschlange,
/// also waechst sie waehrend des Uploads — ein beim Batch-Start eingefrorener
/// Wert waere sofort kleiner als die offene Menge in der Statuszeile.
pub(super) const TOTAL_REFRESH_INTERVAL: std::time::Duration = std::time::Duration::from_secs(2);
/// Files larger than this are uploaded in chunks instead of a single PUT.
pub(super) const CHUNKED_UPLOAD_THRESHOLD: u64 = 10 * 1024 * 1024; // 10 MB
/// Delta upload bails once its literal (changed) payload exceeds this — beyond
/// it a plain chunked upload is comparable, and the cap also bounds the plan's
/// in-memory/JSON footprint so even huge files are diffed in bounded memory.
pub(super) const DELTA_MAX_LITERAL_BYTES: usize = 10 * 1024 * 1024; // 10 MB
/// Server-initiierte Löschungen erzeugen Disk-`Deleted`-Events, die der Watcher
/// aufschnappt und sonst als `pending_delete` stagen würde. Wir merken uns die
/// betroffenen Pfade kurz, damit `stage_delete` sie überspringt. Das Fenster
/// muss länger sein als typische Watcher-Latenz inkl. Debounce.
pub(super) const SERVER_REMOVE_SUPPRESS_TTL: Duration = Duration::from_secs(30);

/// Describes the local state of a file relative to the last known server version.
#[derive(Debug, PartialEq, Eq)]
pub(super) enum LocalChange {
    /// File exists locally and has been modified (pending upload).
    Modified,
    /// File was deleted locally (pending server delete).
    Deleted,
    /// No local change — file is synced or not yet tracked.
    None,
}

// ─── Sync Engine ─────────────────────────────────────────────────────────────

/// Paths the client itself just removed from disk because the server said they
/// were gone, keyed by `(sync_root_id, relative path)` with the time they were
/// noted (for the TTL sweep). Anything in here must not be read back as a
/// *local* deletion — see [`SyncEngine::server_initiated_removes`].
pub(super) type ServerRemoves =
    std::sync::Mutex<std::collections::HashMap<(String, String), Instant>>;

/// Non-consuming peek at [`ServerRemoves`], for the detached walk: unlike the
/// watcher, the walk is not the one event the note was written for, so it must
/// leave the entry in place for that event to still find it. Prunes expired
/// entries on the way past, like the consuming twin does.
pub(super) fn is_server_initiated_remove(
    removes: &ServerRemoves,
    root_id: &str,
    rel: &str,
) -> bool {
    let Ok(mut map) = removes.lock() else {
        return false;
    };
    let now = Instant::now();
    map.retain(|_, t| now.duration_since(*t) < SERVER_REMOVE_SUPPRESS_TTL);
    map.contains_key(&(root_id.to_string(), rel.to_string()))
}

/// Free-function twin of [`SyncEngine::note_server_initiated_remove`]:
/// the note only ever touches the bare `ServerRemoves` map, so it needs no
/// `SyncEngine` — lets the inbound bulk-delete flush (outside `SyncEngine`)
/// suppress the watcher the same way the pull path always has. The method
/// delegates here rather than duplicating the logic.
pub(super) fn mark_server_initiated_remove(removes: &ServerRemoves, root_id: &str, rel: &str) {
    if let Ok(mut map) = removes.lock() {
        let now = Instant::now();
        map.retain(|_, t| now.duration_since(*t) < SERVER_REMOVE_SUPPRESS_TTL);
        map.insert((root_id.to_string(), rel.to_string()), now);
    }
}

pub struct SyncEngine {
    pub(super) app: AppHandle,
    pub(super) db: Arc<LocalDb>,
    /// Every request the engine makes goes through here, so none of them can
    /// run on a token that expired since it was read (ADR-0071).
    pub(super) api: AuthedApi,
    pub(super) snapshot_store: snapshot_store::SnapshotStore,
    /// Signalled whenever files are staged and a sync cycle should run soon.
    pub(super) sync_now: Arc<tokio::sync::Notify>,
    /// Set by a user-initiated "Sync Now" (tray item or `force_sync`
    /// command) right before signalling `sync_now`, so the debounce branch
    /// can tell that apart from a routine watcher-event nudge and run a full
    /// local walk (ADR-0037) instead of an Incremental cycle -- "sync now"
    /// implies "make sure everything is right now", not just "push what's
    /// already staged". Consumed (reset to false) by the branch that reads it.
    pub(super) force_walk: Arc<std::sync::atomic::AtomicBool>,
    /// Monotonic count of rows that have *entered* the upload queue's priority
    /// lane (ADR-0059). A running upload batch snapshots it and compares as it
    /// goes: a higher value means the watcher registered a change the batch is
    /// not carrying, which is its cue to cut itself short.
    ///
    /// In-process rather than a lane query, for the same reason `force_walk`
    /// is a flag and not a DB read: the code that fills the lane and the code
    /// that watches it are the same process, and during a first sync the
    /// alternative would take the DB mutex the detached walk is already
    /// hammering. Only genuine arrivals count — see `LocalDb::set_priority`.
    pub(super) lane_arrivals: Arc<std::sync::atomic::AtomicU64>,
    /// Per-root state of the once-per-session placeholder pass: claimed,
    /// running, refused how often. Shared via `Arc` because the pass runs as
    /// its own task (ADR-0070) and only holds a clone.
    pub(super) placeholder_passes: Arc<std::sync::Mutex<placeholder_pass::PlaceholderPasses>>,
    /// Bounds parallelism for eager-mode background hydration. Without this
    /// a sync of N files in eager mode would spawn N concurrent
    /// CfHydratePlaceholder calls — each running our FETCH_DATA download —
    /// and saturate the network instantly.
    pub(super) hydrate_sem: Arc<tokio::sync::Semaphore>,
    /// Per-cycle log of completed transfers. Pushed from upload/download/
    /// delete success paths (incl. spawned tasks, hence Arc<Mutex<…>>),
    /// drained by `notify_transfer_summary` at end of each pass. Tracks
    /// path + root name so the OS notification can name the single file
    /// when only one was touched, or the affected sync root(s) otherwise.
    pub(super) transfers: Arc<std::sync::Mutex<Vec<TransferRecord>>>,
    /// Last time we fired a transfer-summary notification — gate for the
    /// `TRANSFER_NOTIFY_THROTTLE` window.
    pub(super) last_transfer_notify: std::sync::Mutex<Option<Instant>>,
    /// Pfade, die wir gerade selbst (wegen Server-Delete) von der Platte
    /// entfernen. Watcher sieht die Disk-Löschung und würde sonst eine
    /// `pending_delete`-Zeile anlegen → Bulk-Delete-Guard schlägt fälschlich an.
    /// Seit der Walk entkoppelt läuft (ADR-0056) gilt dasselbe für ihn: er
    /// kann ein Verzeichnis genau dann lesen, wenn der Server-Pull daraus
    /// gerade eine Datei entfernt hat. Deshalb `Arc` — der abgesetzte
    /// Walk-Task hält nur einen Clone.
    /// Key: (sync_root_id, relativer Pfad). Wert: Insert-Zeitpunkt (für TTL-GC).
    pub(super) server_initiated_removes: Arc<ServerRemoves>,
    /// Directories whose Cloud-Files IN_SYNC bit this cycle invalidated (ADR-0052).
    ///
    /// The CF filter clears a directory placeholder's IN_SYNC flag whenever a
    /// descendant changes, so every entry we touch dirties its whole ancestor
    /// chain up to the sync root. Collecting the chain here and restoring it
    /// once at the settled end of the cycle keeps the work proportional to
    /// what actually moved, instead of re-walking a million-file root every
    /// cycle (ADR-0037). Shared via `Arc` because the upload tasks are
    /// spawned and only hold clones.
    ///
    /// Key: sync-root id. Value: relative directory paths (`/docs`,
    /// `/docs/2026`), sync root itself excluded — it has its own marker.
    pub(super) dirty_dirs: Arc<engine_placeholders::DirtyDirs>,
    /// Wie viele Dateien der laufende Upload-Durchlauf je Root schon erledigt
    /// hat. Traegt den Fortschrittsbalken ueber die Batch-Grenzen des
    /// gedeckelten Upload-Zyklus hinweg (ADR-0056); der Eintrag wird geloescht,
    /// sobald die Warteschlange des Roots leer ist.
    pub(super) upload_progress: std::sync::Mutex<std::collections::HashMap<String, usize>>,
    /// Root IDs with a chunked full walk currently in flight (ADR-0037) —
    /// the overlap guard. A second trigger for the same root (e.g. the
    /// safety net firing while a startup walk is still running on a huge
    /// root) finds its ID already present and skips instead of racing a
    /// second walk chain against the first.
    pub(super) active_walks: Arc<std::sync::Mutex<std::collections::HashSet<String>>>,
}

#[derive(Debug, Clone)]
pub(super) enum TransferOp {
    Uploaded,
    Downloaded,
    Deleted,
    Renamed,
}

#[derive(Debug, Clone)]
pub(super) struct TransferRecord {
    pub(super) op: TransferOp,
    pub(super) path: String,
    pub(super) root_name: String,
}

impl SyncEngine {
    pub fn new(
        app: AppHandle,
        db: Arc<LocalDb>,
        api: Arc<RwLock<ApiClient>>,
        snapshot_store: snapshot_store::SnapshotStore,
        sync_now: Arc<tokio::sync::Notify>,
        force_walk: Arc<std::sync::atomic::AtomicBool>,
    ) -> Self {
        let api = {
            let app = app.clone();
            AuthedApi::new(api, db.clone(), move || {
                app.emit("session_expired", ()).ok();
            })
        };
        Self {
            app,
            db,
            api,
            snapshot_store,
            sync_now,
            force_walk,
            lane_arrivals: Arc::new(std::sync::atomic::AtomicU64::new(0)),
            placeholder_passes: Arc::new(std::sync::Mutex::new(Default::default())),
            hydrate_sem: Arc::new(tokio::sync::Semaphore::new(UPLOAD_CONCURRENCY_DEFAULT)),
            transfers: Arc::new(std::sync::Mutex::new(Vec::new())),
            last_transfer_notify: std::sync::Mutex::new(None),
            server_initiated_removes: Arc::new(std::sync::Mutex::new(
                std::collections::HashMap::new(),
            )),
            dirty_dirs: Arc::new(std::sync::Mutex::new(std::collections::HashMap::new())),
            upload_progress: std::sync::Mutex::new(std::collections::HashMap::new()),
            active_walks: Arc::new(std::sync::Mutex::new(std::collections::HashSet::new())),
        }
    }

    /// Markiert `(root_id, rel)` als gerade vom Server-Pull gelöscht, damit der
    /// nachfolgende Watcher-`Deleted`-Event nicht in `pending_delete` landet.
    /// Räumt dabei abgelaufene Einträge auf (kein eigener Timer nötig).
    pub(super) fn note_server_initiated_remove(&self, root_id: &str, rel: &str) {
        mark_server_initiated_remove(&self.server_initiated_removes, root_id, rel);
    }

    /// Liefert true, wenn der Pfad gerade durch einen Server-Delete entfernt
    /// wurde — der Watcher-Delete soll dann ignoriert werden. Konsumiert den
    /// Eintrag, damit ein späterer echter lokaler Delete wieder durchgeht.
    pub(super) fn consume_server_initiated_remove(&self, root_id: &str, rel: &str) -> bool {
        if let Ok(mut map) = self.server_initiated_removes.lock() {
            let now = Instant::now();
            map.retain(|_, t| now.duration_since(*t) < SERVER_REMOVE_SUPPRESS_TTL);
            return map
                .remove(&(root_id.to_string(), rel.to_string()))
                .is_some();
        }
        false
    }

    pub(super) fn record_transfer(&self, op: TransferOp, path: &str, root_name: &str) {
        if let Ok(mut v) = self.transfers.lock() {
            v.push(TransferRecord {
                op,
                path: path.to_string(),
                root_name: root_name.to_string(),
            });
        }
    }

    /// Classify a file relative to the last state we synced with the server.
    ///
    /// When the file still exists locally (`current_hash` is `Some`), the
    /// classification is hash-based: compare against the DB row's
    /// `server_hash` (the last content we know the server acknowledged),
    /// not the row's `sync_status` flag. `sync_status` is set by
    /// the walk, which the cycle only *starts* (ADR-0056) — so the flag is
    /// stale by construction here: a file the user (or e.g. a running game)
    /// writes to after the walk passed over it, or while the walk is still
    /// somewhere else in the tree, still shows a non-`pending` status.
    /// Trusting that flag would classify a
    /// genuinely-changed file as unmodified, and `decide_server_change`
    /// would then silently overwrite it with the server's version instead of
    /// routing it through the conflict path (which backs the local content
    /// up as `.conflict` before downloading). Hashing right now is race-free
    /// by construction: there is no window between "check" and "act".
    ///
    /// When there is no local content to hash (`current_hash` is `None`) —
    /// the file is gone, or it is a dehydrated placeholder holding no local
    /// bytes — fall back to `sync_status` to distinguish "we deleted it too"
    /// from "no local record of a delete". A placeholder cannot carry an
    /// unsynced local edit by construction, so `synced` → `None` is the right
    /// answer for it too.
    pub(super) fn is_locally_modified(
        &self,
        root: &SyncRootEntry,
        path: &str,
        current_hash: Option<&[u8]>,
    ) -> Result<LocalChange> {
        let Some(f) = self.db.get_file(&root.id, path)? else {
            return Ok(LocalChange::None);
        };
        match current_hash {
            Some(hash) => {
                let unchanged = f.server_hash.as_deref() == Some(hash);
                Ok(if unchanged {
                    LocalChange::None
                } else {
                    LocalChange::Modified
                })
            }
            None => Ok(match f.sync_status.as_str() {
                "pending_delete" => LocalChange::Deleted,
                _ => LocalChange::None,
            }),
        }
    }
}

// ─── Free functions ──────────────────────────────────────────────────────────
// Path manipulation/validation helpers moved to `paths` submodule.

/// Lege ein vom Server kommendes Verzeichnis lokal an und markiere es im
/// DB-Index als synced. Pure Funktion, damit ohne SyncEngine testbar.
/// `entry.is_dir` muss true sein.
pub(crate) fn apply_server_dir_entry(
    db: &LocalDb,
    root: &SyncRootEntry,
    entry: &crate::api::ServerFileEntry,
) -> Result<()> {
    let base = Path::new(&root.local_path);
    let local_path = base.join(entry.path.trim_start_matches('/'));

    std::fs::create_dir_all(&local_path)?;

    db.upsert_dir(&root.id, &entry.path, "synced")?;
    Ok(())
}

/// Adopt pre-existing local files that already match the server, so a freshly
/// (re-)added root does not blind-upload every file. A fresh `root.id` knows no
/// server_version, so its uploads carry no If-Match; the server then treats
/// every blind write to an already-existing path as a *real* conflict and
/// materialises a loser copy + notification for each file. For every
/// non-deleted file entry whose local file exists and whose BLAKE3 matches the
/// server's `content_hash`, we record a synced row carrying the entry's
/// `server_version`. Divergent files (same path, different bytes) and files
/// missing on disk are left untouched: the normal reconcile→upload path then
/// surfaces a genuine conflict or downloads them (ADR-0003). Entries whose
/// synced row already carries their `server_version` are skipped unhashed,
/// which is what makes a resumed first sync cheap.
///
/// Pure over `(db, root, entries)` plus the local filesystem, so it is unit-
/// testable without a `SyncEngine`. Returns the number of files adopted.
pub(crate) fn adopt_server_files(
    db: &LocalDb,
    root: &SyncRootEntry,
    entries: &[crate::api::ServerFileEntry],
) -> Result<usize> {
    let base = Path::new(&root.local_path);
    let mut adopted = 0usize;

    for entry in entries {
        if entry.is_deleted || entry.is_dir {
            continue;
        }
        let server_hash_hex = match entry.content_hash.as_deref() {
            Some(h) if !h.is_empty() => h,
            _ => continue,
        };

        // Adopted by an earlier run of this same first sync, cut short before
        // the root got its changes cursor: the row already records exactly
        // this server version, and hashing the file again would only re-read
        // every byte to re-derive it — the whole root, on every restart.
        let known = db.get_file(&root.id, &entry.path)?;
        if known.is_some_and(|f| {
            f.sync_status == "synced" && Some(f.server_version) == entry.server_version
        }) {
            continue;
        }

        let local_path = base.join(entry.path.trim_start_matches('/'));
        if !local_path.is_file() {
            continue;
        }

        let local_hash = match hash_file(&local_path) {
            Ok(h) => h,
            Err(e) => {
                warn!("adopt: cannot hash '{}': {e}", entry.path);
                continue;
            }
        };
        if hex::encode(&local_hash) != server_hash_hex {
            // Divergent — leave it for the reconcile→upload→conflict path.
            continue;
        }

        let meta = local_path.metadata()?;
        let size = meta.len() as i64;
        let mtime = meta
            .modified()
            .unwrap_or(UNIX_EPOCH)
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .as_secs() as i64;

        db.upsert_synced_with_version(
            &root.id,
            &entry.path,
            size,
            mtime,
            &local_hash,
            entry.server_version.unwrap_or(-1),
        )?;
        adopted += 1;
    }

    Ok(adopted)
}

/// Compute the BLAKE3 hash of a file by streaming it through a 64 KiB buffer.
pub fn hash_file(path: &Path) -> Result<Vec<u8>> {
    use std::io::Read;
    let mut file = std::fs::File::open(path)?;
    let mut hasher = blake3::Hasher::new();
    let mut buf = vec![0u8; 64 * 1024];
    loop {
        let n = file.read(&mut buf)?;
        if n == 0 {
            break;
        }
        hasher.update(&buf[..n]);
    }
    Ok(hasher.finalize().as_bytes().to_vec())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::{LocalDb, SyncRootEntry};
    // This module's own tests are about adopt_server_files, but one checks
    // its interaction with the walk (an adopted file must not get re-staged
    // as pending) -- reconcile_local_inner now lives in walk.rs.
    use super::walk::reconcile_local_inner;
    use std::path::PathBuf;

    struct TempDir(PathBuf);
    impl TempDir {
        fn new() -> Self {
            let p = std::env::temp_dir().join(format!("priva-sync-test-{}", uuid::Uuid::new_v4()));
            std::fs::create_dir_all(&p).unwrap();
            Self(p)
        }
        fn path(&self) -> &Path {
            &self.0
        }
    }
    impl Drop for TempDir {
        fn drop(&mut self) {
            let _ = std::fs::remove_dir_all(&self.0);
        }
    }

    fn test_root(id: &str, local_path: &Path) -> SyncRootEntry {
        SyncRootEntry {
            id: id.to_string(),
            name: "test".to_string(),
            local_path: local_path.to_string_lossy().to_string(),
            server_root_id: "srv-1".to_string(),
            download_mode: "on_demand".to_string(),
            read_only: false,
            paused: false,
            kind: crate::db::ROOT_KIND_OWNED.to_string(),
        }
    }

    /// A blocked_remote_quota row (ADR-0011) must be picked up by
    /// list_pending_batch once its fixed retry-window has passed, exactly
    /// like a due pending_failed row — so the 60s poll re-attempts the mount
    /// upload.
    #[test]
    fn blocked_remote_quota_row_is_retried_when_due() {
        let tmp = TempDir::new();
        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();

        db.upsert_file("m1", "/a.txt", 5, 0, &[1, 2, 3], "pending", None)
            .unwrap();
        let past = chrono::Utc::now().timestamp() - 10;
        db.set_blocked_remote_quota("m1", "/a.txt", past).unwrap();

        let due = db.list_pending_batch("m1", 10).unwrap();
        assert!(
            due.iter().any(|f| f.path == "/a.txt"),
            "due blocked_remote_quota row should be retried"
        );

        // A not-yet-due block must stay out of the pending set.
        db.upsert_file("m1", "/b.txt", 5, 0, &[4, 5, 6], "pending", None)
            .unwrap();
        let future = chrono::Utc::now().timestamp() + 3600;
        db.set_blocked_remote_quota("m1", "/b.txt", future).unwrap();
        let due2 = db.list_pending_batch("m1", 10).unwrap();
        assert!(
            !due2.iter().any(|f| f.path == "/b.txt"),
            "not-yet-due block must not be retried early"
        );
    }

    /// A mount root (kind='mount', ADR-0011) must round-trip through the DB:
    /// upsert with the mount kind, read it back, and have is_mount() == true,
    /// while an owned root reads back as is_mount() == false. Guards the
    /// migration-11 column + from_row mapping.
    #[test]
    fn mount_kind_round_trips_through_db() {
        let tmp = TempDir::new();
        let db_path = tmp.path().join("db.sqlite");
        let db = LocalDb::open(&db_path).unwrap();

        db.upsert_sync_root(
            "m1",
            "alice-photos",
            "/tmp/m1",
            "share-1",
            "on_demand",
            true,
            crate::db::ROOT_KIND_MOUNT,
        )
        .unwrap();
        db.upsert_sync_root(
            "o1",
            "own",
            "/tmp/o1",
            "root-1",
            "on_demand",
            false,
            crate::db::ROOT_KIND_OWNED,
        )
        .unwrap();

        let mount = db.get_sync_root("m1").unwrap().expect("mount row");
        assert!(mount.is_mount(), "kind='mount' should report is_mount()");
        assert_eq!(mount.server_root_id, "share-1", "mount carries share_id");

        let owned = db.get_sync_root("o1").unwrap().expect("owned row");
        assert!(!owned.is_mount(), "kind='owned' should not be a mount");

        // list_sync_roots must surface the kind too (lenient mapper path).
        let all = db.list_sync_roots().unwrap();
        let listed = all.iter().find(|r| r.id == "m1").expect("mount listed");
        assert!(listed.is_mount(), "list_sync_roots must preserve kind");
    }

    /// A server-side can_write change (no revoke) must flip the activated
    /// mount's persisted read_only on the next pull (ADR-0014): the pure
    /// predicate spots the diff, the DB setter applies it, and list_sync_roots
    /// reflects the new value so the engine RO-guard honours it. Both
    /// directions and persistence are exercised.
    #[test]
    fn pull_refresh_flips_mount_read_only_both_ways() {
        use super::predicates::mount_read_only_changes;
        use std::collections::HashMap;

        let tmp = TempDir::new();
        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();

        // A writable mount and a read-only mount, both activated.
        db.upsert_sync_root(
            "m-rw",
            "rw",
            "/tmp/rw",
            "share-rw",
            "on_demand",
            false,
            crate::db::ROOT_KIND_MOUNT,
        )
        .unwrap();
        db.upsert_sync_root(
            "m-ro",
            "ro",
            "/tmp/ro",
            "share-ro",
            "on_demand",
            true,
            crate::db::ROOT_KIND_MOUNT,
        )
        .unwrap();

        // Server now revokes write on rw and grants it on ro.
        let mut can_write = HashMap::new();
        can_write.insert("share-rw".to_string(), false);
        can_write.insert("share-ro".to_string(), true);

        let local = db.list_sync_roots().unwrap();
        for (id, ro) in mount_read_only_changes(&local, &can_write) {
            db.set_sync_root_read_only(&id, ro).unwrap();
        }

        let after = db.list_sync_roots().unwrap();
        let rw = after.iter().find(|r| r.id == "m-rw").unwrap();
        let ro = after.iter().find(|r| r.id == "m-ro").unwrap();
        assert!(
            rw.read_only,
            "revoked write right must persist as read_only=true"
        );
        assert!(
            !ro.read_only,
            "granted write right must persist as read_only=false"
        );

        // Idempotent: a second pull with the same can_write yields no changes.
        let local2 = db.list_sync_roots().unwrap();
        assert!(
            mount_read_only_changes(&local2, &can_write).is_empty(),
            "stable can_write must produce no further flips"
        );
    }

    /// Server-seitiges Loeschen eines leeren Verzeichnisses muss das lokale
    /// Verzeichnis von der Platte entfernen. Vorher: remove_file schlug auf
    /// Verzeichnis fehl und der Fehler wurde stillgeschluckt.
    #[test]
    fn remove_local_entry_removes_directory() {
        let tmp = TempDir::new();
        let dir = tmp.path().join("victim");
        std::fs::create_dir_all(&dir).unwrap();
        assert!(dir.is_dir());

        let removed = remove_local_entry(&dir).expect("remove_local_entry ok");

        assert!(removed, "remove_local_entry sollte true zurueckgeben");
        assert!(!dir.exists(), "Verzeichnis sollte physisch entfernt sein");
    }

    /// Free-function twin of [`SyncEngine::note_server_initiated_remove`],
    /// extracted so the inbound bulk-delete flush can suppress the
    /// watcher without needing a whole `SyncEngine`. Same map, same logic —
    /// the method now just delegates to this.
    #[test]
    fn mark_server_initiated_remove_makes_the_path_visible_as_suppressed() {
        let removes: ServerRemoves = std::sync::Mutex::new(std::collections::HashMap::new());

        assert!(
            !is_server_initiated_remove(&removes, "r1", "/a.txt"),
            "nothing noted yet"
        );

        mark_server_initiated_remove(&removes, "r1", "/a.txt");

        assert!(is_server_initiated_remove(&removes, "r1", "/a.txt"));
    }

    /// Server delivers a directory entry (is_dir=true): after the apply, the
    /// directory must exist locally and live in the DB index as a synced
    /// is_dir row.
    #[test]
    fn applying_server_dir_entry_creates_local_directory_and_marks_synced() {
        let tmp = TempDir::new();
        let db_path = tmp.path().join("db.sqlite");
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(&root_path).unwrap();

        let db = LocalDb::open(&db_path).unwrap();
        let root = test_root("root-2", &root_path);
        db.upsert_sync_root(
            &root.id,
            &root.name,
            &root.local_path,
            &root.server_root_id,
            &root.download_mode,
            root.read_only,
            &root.kind,
        )
        .unwrap();

        let entry = crate::api::ServerFileEntry {
            id: "dir-1".to_string(),
            path: "/from-server".to_string(),
            size: 0,
            mtime: "2026-05-15T12:00:00Z".to_string(),
            content_hash: None,
            is_deleted: false,
            updated_at: "2026-05-15T12:00:00Z".to_string(),
            server_version: Some(1),
            is_dir: true,
            seq: 0,
        };

        apply_server_dir_entry(&db, &root, &entry)
            .expect("apply_server_dir_entry sollte Ok zurueckgeben");

        assert!(
            root_path.join("from-server").is_dir(),
            "Verzeichnis sollte lokal angelegt sein",
        );

        let rec = db
            .get_file(&root.id, "/from-server")
            .unwrap()
            .expect("DB sollte Eintrag haben");
        assert!(rec.is_dir, "is_dir muss true sein");
        assert_eq!(rec.sync_status, "synced");
    }

    /// Re-adding a root over pre-existing local files must NOT blind-upload
    /// them. A fresh root knows no server_version, so its uploads carry no
    /// If-Match and the server treats every blind write to an existing path as
    /// a real conflict (loser copy + notification per file). The adopt pass
    /// recognises a local file whose BLAKE3 matches the server entry and writes
    /// a synced row carrying the server_version, so the upload pass skips it.
    #[test]
    fn adopt_marks_matching_local_file_synced_with_server_version() {
        let tmp = TempDir::new();
        let db_path = tmp.path().join("db.sqlite");
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(&root_path).unwrap();
        std::fs::write(root_path.join("a.txt"), b"hello").unwrap();

        let db = LocalDb::open(&db_path).unwrap();
        let root = test_root("root-1", &root_path);

        let server_hash = hex::encode(hash_file(&root_path.join("a.txt")).unwrap());
        let entries = vec![crate::api::ServerFileEntry {
            id: "f-1".to_string(),
            path: "/a.txt".to_string(),
            size: 5,
            mtime: "2026-05-15T12:00:00Z".to_string(),
            content_hash: Some(server_hash),
            is_deleted: false,
            updated_at: "2026-05-15T12:00:00Z".to_string(),
            server_version: Some(7),
            is_dir: false,
            seq: 0,
        }];

        let adopted = adopt_server_files(&db, &root, &entries).unwrap();
        assert_eq!(adopted, 1, "matching local file should be adopted");

        let rec = db.get_file(&root.id, "/a.txt").unwrap().expect("db row");
        assert_eq!(
            rec.sync_status, "synced",
            "adopted file must be synced, not pending"
        );
        assert_eq!(
            rec.server_version, 7,
            "adopted file must carry server_version"
        );

        // reconcile must then leave it alone — no pending upload staged.
        reconcile_local_inner(&db, &root).unwrap();
        let rec2 = db.get_file(&root.id, "/a.txt").unwrap().unwrap();
        assert_eq!(
            rec2.sync_status, "synced",
            "reconcile must not re-stage an adopted file as pending"
        );
    }

    /// The adopt pass must not swallow real differences: a local file whose
    /// bytes differ from the server entry (same path) is left unadopted so the
    /// normal reconcile→upload path can surface a real conflict (ADR-0003). A
    /// server file missing on disk is likewise skipped (it gets downloaded).
    #[test]
    fn adopt_skips_divergent_and_missing_files() {
        let tmp = TempDir::new();
        let db_path = tmp.path().join("db.sqlite");
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(&root_path).unwrap();
        std::fs::write(root_path.join("changed.txt"), b"local-edit").unwrap();
        // gone.txt exists only on the server — no local file.

        let db = LocalDb::open(&db_path).unwrap();
        let root = test_root("root-1", &root_path);

        let entries = vec![
            crate::api::ServerFileEntry {
                id: "f-1".to_string(),
                path: "/changed.txt".to_string(),
                size: 14,
                mtime: "2026-05-15T12:00:00Z".to_string(),
                content_hash: Some(hex::encode(blake3::hash(b"server-version").as_bytes())),
                is_deleted: false,
                updated_at: "2026-05-15T12:00:00Z".to_string(),
                server_version: Some(3),
                is_dir: false,
                seq: 0,
            },
            crate::api::ServerFileEntry {
                id: "f-2".to_string(),
                path: "/gone.txt".to_string(),
                size: 4,
                mtime: "2026-05-15T12:00:00Z".to_string(),
                content_hash: Some(hex::encode(blake3::hash(b"data").as_bytes())),
                is_deleted: false,
                updated_at: "2026-05-15T12:00:00Z".to_string(),
                server_version: Some(4),
                is_dir: false,
                seq: 0,
            },
        ];

        let adopted = adopt_server_files(&db, &root, &entries).unwrap();
        assert_eq!(adopted, 0, "divergent + missing files must not be adopted");
        assert!(
            db.get_file(&root.id, "/changed.txt").unwrap().is_none(),
            "divergent file must stay unrecorded so reconcile stages it for upload"
        );
        assert!(
            db.get_file(&root.id, "/gone.txt").unwrap().is_none(),
            "missing file must stay unrecorded so it gets downloaded"
        );
    }

    /// A first sync cut short before the root got its changes cursor runs the
    /// adopt pass again on the next start. What the first run adopted must not
    /// be hashed again — on a large root that is every byte, every restart
    ///.
    #[test]
    fn adopt_skips_files_an_earlier_run_already_adopted() {
        let tmp = TempDir::new();
        let root_path = tmp.path().join("root");
        std::fs::create_dir_all(&root_path).unwrap();
        std::fs::write(root_path.join("a.txt"), b"hello").unwrap();

        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        let root = test_root("root-1", &root_path);
        let entry = |version: i64| crate::api::ServerFileEntry {
            id: "f-1".to_string(),
            path: "/a.txt".to_string(),
            size: 5,
            mtime: "2026-05-15T12:00:00Z".to_string(),
            content_hash: Some(hex::encode(blake3::hash(b"hello").as_bytes())),
            is_deleted: false,
            updated_at: "2026-05-15T12:00:00Z".to_string(),
            server_version: Some(version),
            is_dir: false,
            seq: 0,
        };

        assert_eq!(adopt_server_files(&db, &root, &[entry(7)]).unwrap(), 1);
        assert_eq!(
            adopt_server_files(&db, &root, &[entry(7)]).unwrap(),
            0,
            "a row that already records this server version is not hashed again"
        );
        assert_eq!(
            adopt_server_files(&db, &root, &[entry(8)]).unwrap(),
            1,
            "a newer server version is checked against the bytes again"
        );
        assert_eq!(
            db.get_file(&root.id, "/a.txt")
                .unwrap()
                .unwrap()
                .server_version,
            8
        );
    }
}
