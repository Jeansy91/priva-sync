//! The once-per-session placeholder pass: converts every locally present
//! synced file and every directory of a sync root to a Cloud Files
//! placeholder, so Explorer's status column has something to show for files
//! synced in an earlier session. Runs as its own task (ADR-0070).

use std::path::{Path, PathBuf};
use tracing::{info, warn};

use crate::cloud_files::{self, ConvertOutcome};
use crate::db::{LocalDb, SyncRootEntry};

use super::SyncEngine;
use super::paths::relative_path;

impl SyncEngine {
    /// Starts the once-per-session placeholder pass for `root` as its own
    /// task, so Explorer's "Synchronisierungsstatus" column shows values for
    /// files synced in an earlier session. On non-Windows, and on a root that
    /// cannot carry placeholders, this is a no-op.
    ///
    /// **Detached** (ADR-0070), like the full walk (ADR-0056): the pass
    /// touches every entry of the root, and awaiting it held back this root's
    /// uploads and downloads until the last directory was done — on every app
    /// start, minutes on a large root, while there was nothing to convert
    /// because every entry already was a placeholder. Nothing in the
    /// cycle depends on its result; only the root's settled Explorer status
    /// does, which is why [`SyncEngine::placeholder_pass_in_flight`] holds
    /// that back until the pass is through.
    ///
    /// The claim is taken synchronously, before the spawn, so a cycle that
    /// starts while the pass runs finds the root claimed instead of
    /// converting the whole tree a second time.
    pub(super) fn spawn_ensure_placeholders(&self, root: &SyncRootEntry) {
        // Per root, not per platform: a volume root cannot carry placeholders
        // even on Windows, and running the pass there produces one failure per
        // file and nothing else (ADR-0057).
        if !cloud_files::placeholders_available_for(Path::new(&root.local_path)) {
            return;
        }
        if !self.placeholder_passes.lock().unwrap().claim(&root.id) {
            return;
        }

        let db       = self.db.clone();
        let passes   = self.placeholder_passes.clone();
        let sync_now = self.sync_now.clone();
        let root     = root.clone();

        tokio::spawn(async move {
            let outcome = run_placeholder_pass(&db, &root).await;
            passes.lock().unwrap().finish(&root, outcome);
            // The pass may have been all that kept this root from settling;
            // the cycle this nudge starts can now restore its Explorer status.
            sync_now.notify_one();
        });
    }

    /// Whether the placeholder pass is still running for this root. Like a
    /// walk in flight, it makes an empty queue mean nothing: the pass is still
    /// converting directories, so their status is not settled yet.
    pub(super) fn placeholder_pass_in_flight(&self, root_id: &str) -> bool {
        self.placeholder_passes.lock().unwrap().running.contains(root_id)
    }
}

/// Per-root bookkeeping of the once-per-session placeholder pass.
#[derive(Default)]
pub(crate) struct PlaceholderPasses {
    /// Roots whose pass this session has claimed — running or finished.
    claimed:  std::collections::HashSet<String>,
    /// Roots whose pass is running right now.
    running:  std::collections::HashSet<String>,
    /// How often the CF filter has refused a root's pass this session — the
    /// bound on retrying it (see [`PLACEHOLDER_MAX_ATTEMPTS`]).
    refusals: std::collections::HashMap<String, u32>,
}

impl PlaceholderPasses {
    /// Claims the pass for `root_id`; `false` when this session already has.
    fn claim(&mut self, root_id: &str) -> bool {
        if !self.claimed.insert(root_id.to_string()) {
            return false;
        }
        self.running.insert(root_id.to_string());
        true
    }

    /// Records how the pass for `root` ended. A pass that could not run, or
    /// that the CF filter refused, gives its claim back so a later cycle
    /// tries again — the latter bounded, so a root where it can never work
    /// does not retry forever.
    fn finish(&mut self, root: &SyncRootEntry, outcome: PassOutcome) {
        self.running.remove(&root.id);
        match outcome {
            PassOutcome::Done => {}
            PassOutcome::NotRun => {
                self.claimed.remove(&root.id);
            }
            PassOutcome::Refused(refused) => {
                let attempts = self.refusals.entry(root.id.clone()).or_insert(0);
                *attempts += 1;
                if *attempts < PLACEHOLDER_MAX_ATTEMPTS {
                    warn!(
                        "ensure_placeholders('{}'): {refused} entries refused, none converted \
                         — retrying on a later cycle (attempt {attempts}/{PLACEHOLDER_MAX_ATTEMPTS})",
                        root.name,
                    );
                    self.claimed.remove(&root.id);
                } else {
                    warn!(
                        "ensure_placeholders('{}'): still refused after {attempts} attempts — \
                         giving up for this session; files stay without an Explorer sync state",
                        root.name,
                    );
                }
            }
        }
    }
}

/// How a placeholder pass ended, as far as retrying it is concerned.
enum PassOutcome {
    /// Ran through; not run again this session.
    Done,
    /// Could not start (registration failed) — free to try again.
    NotRun,
    /// Converted nothing while this many entries were refused: the CF filter
    /// was not attached to the root yet.
    Refused(u64),
}

/// The placeholder pass itself: converts every locally present synced file
/// and every directory of `root` to a CF placeholder.
///
/// Chunked over the directory tree through `spawn_blocking`, for the same
/// reason the full walk is (ADR-0037): this pass touches every entry of the
/// root, and running it inline froze the engine loop — and with it this
/// provider's CF callbacks — for its whole duration. On a root with millions
/// of files that window is long enough for Explorer to block on the first
/// look into the sync root, waiting on a hydration or status answer the
/// frozen daemon cannot give, and be torn down.
///
/// Memory is bounded by tree depth × directory width, never by root size:
/// the DB side is queried per directory instead of loading every row of the
/// root into one `Vec` up front.
async fn run_placeholder_pass(db: &std::sync::Arc<LocalDb>, root: &SyncRootEntry) -> PassOutcome {
    // Re-register so this pass is self-contained and doesn't depend on the
    // startup loop having run successfully beforehand.
    if let Err(e) = cloud_files::register(Path::new(&root.local_path), &root.name).await {
        warn!("ensure_placeholders: register failed for '{}': {e:#} — skipping", root.name);
        return PassOutcome::NotRun;
    }

    let base    = PathBuf::from(&root.local_path);
    let started = std::time::Instant::now();

    // Depth-first, starting at the sync root. The stack only ever holds the
    // directories still queued along the current path, so it stays small even
    // on a root with very many subdirectories — unlike a breadth-first queue,
    // which would grow to the tree's widest level.
    let mut stack = vec![Pending::Dir(base.clone())];
    let mut stats = PlaceholderStats::default();

    while !stack.is_empty() {
        let db       = db.clone();
        let root_blk = root.clone();
        let base_blk = base.clone();
        let mut work = std::mem::take(&mut stack);

        let joined = tokio::task::spawn_blocking(move || {
            let chunk = convert_chunk(&db, &root_blk, &base_blk, &mut work);
            (chunk, work)
        })
        .await;
        let (chunk, rest) = match joined {
            Ok(done) => done,
            Err(e)   => {
                // A panicked chunk loses its share of the stack; what was
                // converted so far stays converted. Retrying in this session
                // would most likely panic on the same entry again.
                warn!("ensure_placeholders('{}'): chunk aborted: {e}", root.name);
                return PassOutcome::Done;
            }
        };

        stats.merge(chunk);
        stack = rest;
    }

    let elapsed_ms = started.elapsed().as_millis();
    info!(
        "ensure_placeholders('{}'): {} files + {} dirs in {elapsed_ms}ms — \
         files[succeeded={}, already={}, not_under_root={}, failed={}], \
         dirs[succeeded={}, already={}, not_under_root={}, failed={}]",
        root.name,
        stats.files_processed,
        stats.dirs_processed(),
        stats.files_succeeded,
        stats.files_already,
        stats.files_not_under_root,
        stats.files_failed,
        stats.dirs_succeeded,
        stats.dirs_already,
        stats.dirs_not_under_root,
        stats.dirs_failed,
    );

    for (path, err) in &stats.failures {
        warn!("ensure_placeholders[Failed] '{path}': {err}");
    }
    if stats.failures_dropped > 0 {
        warn!(
            "ensure_placeholders('{}'): {} further failure(s) not listed",
            root.name, stats.failures_dropped,
        );
    }

    // A pass that converted nothing while being refused is not a finished
    // pass — the CF filter was not ready for this root yet. Leaving it marked
    // done meant every file stayed a plain file for the rest of the session,
    // with no sync state for Explorer to show, until a restart.
    let refused = stats.files_not_under_root + stats.dirs_not_under_root;
    if refused > 0 && stats.files_succeeded == 0 {
        return PassOutcome::Refused(refused);
    }
    PassOutcome::Done
}

/// How often per session the pass is retried while the CF filter refuses it.
/// A refusal means the filter is not attached to the root (yet): a later cycle
/// may succeed, but retrying forever would only load a root where it never
/// can.
const PLACEHOLDER_MAX_ATTEMPTS: u32 = 5;

/// How many entries one blocking chunk of [`run_placeholder_pass`]
/// converts before handing control back to the engine loop. Same order of
/// magnitude as the full walk's chunk (ADR-0037): large enough that the
/// per-chunk overhead disappears, small enough that one chunk never holds a
/// blocking thread — and the DB lock it shares with transfers — for long.
const PLACEHOLDER_CHUNK_ENTRIES: usize = 10_000;

/// Upper bound on individually logged conversion failures per root. The
/// failures that matter here are systematic (CF filter not attached, missing
/// permissions) and then hit every single entry — a million warn lines is
/// neither readable nor free (ADR-0050).
const MAX_LOGGED_FAILURES: usize = 50;

/// Aggregated outcome of the placeholder pass, summed across chunks.
#[derive(Default)]
struct PlaceholderStats {
    files_processed:      u64,
    files_succeeded:      u64,
    files_already:        u64,
    files_not_under_root: u64,
    files_failed:         u64,
    dirs_succeeded:       u64,
    dirs_already:         u64,
    dirs_not_under_root:  u64,
    dirs_failed:          u64,
    /// Capped at [`MAX_LOGGED_FAILURES`] entries.
    failures:             Vec<(String, String)>,
    /// Failures beyond that cap, counted rather than kept.
    failures_dropped:     u64,
}

impl PlaceholderStats {
    fn dirs_processed(&self) -> u64 {
        self.dirs_succeeded + self.dirs_already + self.dirs_not_under_root + self.dirs_failed
    }

    fn record_failure(&mut self, path: String, err: String) {
        if self.failures.len() < MAX_LOGGED_FAILURES {
            self.failures.push((path, err));
        } else {
            self.failures_dropped += 1;
        }
    }

    fn merge(&mut self, other: PlaceholderStats) {
        self.files_processed      += other.files_processed;
        self.files_succeeded      += other.files_succeeded;
        self.files_already        += other.files_already;
        self.files_not_under_root += other.files_not_under_root;
        self.files_failed         += other.files_failed;
        self.dirs_succeeded       += other.dirs_succeeded;
        self.dirs_already         += other.dirs_already;
        self.dirs_not_under_root  += other.dirs_not_under_root;
        self.dirs_failed          += other.dirs_failed;
        self.failures_dropped     += other.failures_dropped;
        for (path, err) in other.failures {
            self.record_failure(path, err);
        }
    }
}

/// One unit of pending traversal work.
///
/// The half-finished variant is what makes the chunk budget hold for a single
/// wide directory too: a folder with a million entries directly in it would
/// otherwise be one indivisible blocking stretch, which is exactly the
/// behaviour this pass was rewritten to get rid of. Carrying the live
/// `ReadDir` across the yield resumes precisely where the budget ran out,
/// without re-reading entries or depending on a stable directory order.
enum Pending {
    /// A directory that has not been opened yet.
    Dir(PathBuf),
    /// A directory whose entries are partly done.
    /// Boxed: the stack holds many unopened `Dir`s and few open ones.
    Open(Box<OpenDir>),
}

struct OpenDir {
    entries: std::fs::ReadDir,
    /// Sync status of the tracked files directly inside it — one directory's
    /// worth of rows, never the whole root's.
    tracked: std::collections::HashMap<String, String>,
}

/// One blocking chunk of the placeholder pass: takes work off `stack` and
/// converts entries — each directory plus the tracked files directly inside
/// it — until [`PLACEHOLDER_CHUNK_ENTRIES`] entries have been touched.
/// Whatever is left, including a half-read directory, goes back onto `stack`,
/// so the caller resumes exactly where this returned.
///
/// Pre-order and iterative: a directory is converted when it is first popped,
/// before any of its children, and nesting depth lives on `stack` rather than
/// on the thread's stack.
///
/// The sync root itself is descended into but never converted:
/// `CfConvertToPlaceholder` answers `NotUnderSyncRoot` on the registered root
///. It gets its in-sync mark at the end of a settled cycle instead.
///
/// Two failure buckets per entry: `NotUnderSyncRoot` is a known cosmetic
/// limitation (the entry stays a regular file — openable and syncable, just
/// without the ✓ in Explorer's Status column). Real errors stay loud.
fn convert_chunk(
    db:    &LocalDb,
    root:  &SyncRootEntry,
    base:  &Path,
    stack: &mut Vec<Pending>,
) -> PlaceholderStats {
    let mut stats   = PlaceholderStats::default();
    let mut touched = 0usize;

    while touched < PLACEHOLDER_CHUNK_ENTRIES {
        let Some(item) = stack.pop() else { break };

        let mut open = match item {
            Pending::Open(open) => *open,
            Pending::Dir(dir)   => {
                match open_directory(db, root, base, &dir, &mut stats, &mut touched) {
                    Some(open) => open,
                    None       => continue,
                }
            }
        };

        // Drain this directory until it ends or the budget does.
        let mut exhausted = false;
        while touched < PLACEHOLDER_CHUNK_ENTRIES {
            let Some(entry) = open.entries.next() else { exhausted = true; break };
            let Ok(entry)   = entry else { continue };
            let Ok(file_type) = entry.file_type() else { continue };
            let path = entry.path();

            // Symlinks are neither followed nor converted — same as the
            // `walkdir` default this replaced.
            if file_type.is_dir() {
                stack.push(Pending::Dir(path));
                continue;
            }
            if !file_type.is_file() {
                continue;
            }

            let Ok(rel) = relative_path(base, &path) else { continue };
            // An untracked file (never synced, excluded, a stray temp copy)
            // has no server state to point a placeholder at. Reconcile stages
            // it, and the cycle that uploads it converts it.
            let Some(status) = open.tracked.get(&rel) else { continue };

            touched += 1;
            stats.files_processed += 1;
            match cloud_files::convert_to_placeholder(&path, &rel, status == "synced") {
                ConvertOutcome::Succeeded          => stats.files_succeeded += 1,
                ConvertOutcome::AlreadyPlaceholder => stats.files_already += 1,
                ConvertOutcome::NotUnderSyncRoot   => stats.files_not_under_root += 1,
                ConvertOutcome::Failed(e) => {
                    stats.files_failed += 1;
                    stats.record_failure(rel, format!("{e:#}"));
                }
            }
        }
        if !exhausted {
            // Pushed last so the next chunk finishes this directory before
            // descending into the children it just queued.
            stack.push(Pending::Open(Box::new(open)));
        }
    }

    stats
}

/// Convert `dir` itself (unless it is the sync root) and open it for
/// iteration. `None` when the directory cannot be relativised, queried or
/// read — all three are logged and skipped, never fatal for the pass.
fn open_directory(
    db:      &LocalDb,
    root:    &SyncRootEntry,
    base:    &Path,
    dir:     &Path,
    stats:   &mut PlaceholderStats,
    touched: &mut usize,
) -> Option<OpenDir> {
    let rel = if dir == base {
        String::new()
    } else {
        relative_path(base, dir).ok()?
    };

    if !rel.is_empty() {
        *touched += 1;
        // In-sync only when nothing below it is still waiting. Asserting it
        // unconditionally is what put a green checkmark on folders whose
        // contents had not been uploaded yet: Explorer told the user the data
        // was safe while it sat in the upload queue. A directory that still
        // has work below it becomes a placeholder all the same — that is what
        // lets the CF filter track it at all — just not an in-sync one.
        let settled = !db.has_unsynced_under(&root.id, &rel).unwrap_or(true);
        match cloud_files::convert_to_placeholder(dir, &rel, settled) {
            ConvertOutcome::Succeeded          => stats.dirs_succeeded += 1,
            ConvertOutcome::AlreadyPlaceholder => {
                // Conversion is a no-op on an existing placeholder, so a
                // directory left NOT_IN_SYNC by the previous session (its
                // last cycle never settled) would stay that way forever —
                // but only re-assert it when the subtree really is settled.
                if settled {
                    let _ = cloud_files::mark_in_sync(dir);
                }
                stats.dirs_already += 1;
            }
            ConvertOutcome::NotUnderSyncRoot   => stats.dirs_not_under_root += 1,
            ConvertOutcome::Failed(e) => {
                stats.dirs_failed += 1;
                stats.record_failure(rel.clone(), format!("{e:#}"));
            }
        }
    }

    // Only this one directory's rows — the whole point of walking the tree
    // instead of listing the root's files up front.
    let tracked = match db.file_statuses_in_dir(&root.id, &rel) {
        Ok(rows) => rows,
        Err(e)   => {
            warn!("ensure_placeholders: status lookup for '{rel}' failed: {e:#}");
            return None;
        }
    };
    let entries = match std::fs::read_dir(dir) {
        Ok(entries) => entries,
        Err(e)      => {
            warn!("ensure_placeholders: read_dir '{}' failed: {e}", dir.display());
            return None;
        }
    };

    Some(OpenDir { entries, tracked })
}

#[cfg(test)]
mod tests {
    // ── PlaceholderPasses ────────────────────────────────────────────────────

    use super::{PassOutcome, PlaceholderPasses, PLACEHOLDER_MAX_ATTEMPTS};

    #[test]
    fn a_claimed_pass_runs_until_finished_and_is_not_claimed_twice() {
        let mut passes = PlaceholderPasses::default();
        let root = test_root("r1", Path::new("/r"));

        assert!(passes.claim("r1"));
        assert!(passes.running.contains("r1"), "a claimed pass counts as in flight");
        assert!(!passes.claim("r1"), "a cycle starting mid-pass must not start a second one");

        passes.finish(&root, PassOutcome::Done);
        assert!(!passes.running.contains("r1"), "finishing releases the root for settling");
        assert!(!passes.claim("r1"), "a finished pass is not repeated this session");
    }

    #[test]
    fn a_pass_that_could_not_run_is_claimable_again() {
        let mut passes = PlaceholderPasses::default();
        let root = test_root("r1", Path::new("/r"));

        assert!(passes.claim("r1"));
        passes.finish(&root, PassOutcome::NotRun);
        assert!(passes.claim("r1"), "a failed registration must not cost the root its pass");
    }

    #[test]
    fn a_refused_pass_is_retried_a_bounded_number_of_times() {
        let mut passes = PlaceholderPasses::default();
        let root = test_root("r1", Path::new("/r"));

        for _ in 0..PLACEHOLDER_MAX_ATTEMPTS {
            assert!(passes.claim("r1"));
            passes.finish(&root, PassOutcome::Refused(7));
        }
        assert!(!passes.claim("r1"), "after the last attempt the root gives up for the session");
        assert!(!passes.running.contains("r1"));
    }

    // ── convert_chunk ────────────────────────────────────────────────────────
    //
    // The CF conversions themselves are not asserted on: outside a registered
    // sync root the platform refuses every one of them, and on non-Windows the
    // call is a stub. What these cover is the traversal contract — which
    // entries the pass visits, which it leaves alone, and that it stays within
    // its memory and stack budget on the shapes that used to break it.

    use super::{convert_chunk, Pending, PlaceholderStats};
    use crate::db::{LocalDb, SyncRootEntry};
    use std::path::{Path, PathBuf};

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

    /// Run `stack` to completion the way `ensure_placeholders` does, one chunk
    /// at a time, and report how many chunks it took.
    fn drain_stack(
        db:    &LocalDb,
        root:  &SyncRootEntry,
        base:  &Path,
        stack: &mut Vec<Pending>,
    ) -> (PlaceholderStats, u32) {
        let mut stats  = PlaceholderStats::default();
        let mut chunks = 0;
        while !stack.is_empty() {
            stats.merge(convert_chunk(db, root, base, stack));
            chunks += 1;
        }
        (stats, chunks)
    }

    /// The whole tree, from the sync root down.
    fn drain(db: &LocalDb, root: &SyncRootEntry, base: &Path) -> (PlaceholderStats, u32) {
        let mut stack = vec![Pending::Dir(base.to_path_buf())];
        drain_stack(db, root, base, &mut stack)
    }

    #[test]
    fn convert_chunk_visits_every_directory_and_only_tracked_files() {
        let tmp  = TempDir::new();
        let base = tmp.path().join("root");
        std::fs::create_dir_all(base.join("docs/deep")).unwrap();
        std::fs::write(base.join("a.txt"), b"a").unwrap();
        std::fs::write(base.join("docs/b.txt"), b"b").unwrap();
        std::fs::write(base.join("docs/deep/c.txt"), b"c").unwrap();
        // Never synced, so there is no server state to point a placeholder at.
        std::fs::write(base.join("docs/untracked.tmp"), b"x").unwrap();

        let db   = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        let root = test_root("root-1", &base);
        for path in ["/a.txt", "/docs/b.txt", "/docs/deep/c.txt"] {
            db.upsert_file(&root.id, path, 1, 1, b"h", "synced", None).unwrap();
        }

        let (stats, _) = drain(&db, &root, &base);

        assert_eq!(stats.files_processed, 3, "the untracked file must be left alone");
        assert_eq!(stats.dirs_processed(), 2, "docs + docs/deep, never the sync root itself");
    }

    /// The pass used to hold every row of the root in one `Vec` and walk the
    /// whole tree in a single blocking stretch. Both are gone: a chunk stops
    /// at its entry budget and hands the rest back on the stack, which is what
    /// lets the engine loop — and with it this provider's CF callbacks —
    /// breathe between chunks.
    #[test]
    fn convert_chunk_stops_at_its_budget_and_resumes_from_the_stack() {
        let tmp  = TempDir::new();
        let base = tmp.path().join("root");
        std::fs::create_dir_all(&base).unwrap();

        let db   = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        let root = test_root("root-1", &base);
        let total = super::PLACEHOLDER_CHUNK_ENTRIES + 5;
        for i in 0..total {
            let name = format!("f{i}.bin");
            std::fs::write(base.join(&name), b"x").unwrap();
            db.upsert_file(&root.id, &format!("/{name}"), 1, 1, b"h", "synced", None).unwrap();
        }

        let mut stack = vec![Pending::Dir(base.clone())];
        let first = convert_chunk(&db, &root, &base, &mut stack);
        assert!(
            first.files_processed >= super::PLACEHOLDER_CHUNK_ENTRIES as u64,
            "a chunk runs to its budget before yielding",
        );
        assert!(
            first.files_processed < total as u64,
            "and stops there rather than draining the directory in one go",
        );

        // Continues from the half-read directory the first chunk handed back,
        // rather than starting the traversal over.
        let (rest, _) = drain_stack(&db, &root, &base, &mut stack);
        assert_eq!(
            first.files_processed + rest.files_processed,
            total as u64,
            "every file is converted exactly once across the chunks",
        );
    }

    /// Traversal is an explicit stack, not recursion — a deep tree must not
    /// blow the thread's stack (mirrors the same guarantee for the full walk).
    #[test]
    fn convert_chunk_survives_deep_nesting_without_stack_overflow() {
        let tmp  = TempDir::new();
        let base = tmp.path().join("root");
        let mut deep = base.clone();
        for _ in 0..500 {
            deep = deep.join("n");
        }
        std::fs::create_dir_all(&deep).unwrap();

        let db   = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        let root = test_root("root-1", &base);

        let (stats, _) = drain(&db, &root, &base);
        assert_eq!(stats.dirs_processed(), 500);
    }

    /// Failure logging is capped: a systematic refusal hits every entry, and
    /// the summary must not grow with the size of the root.
    #[test]
    fn stats_cap_the_failures_they_keep() {
        let mut stats = PlaceholderStats::default();
        for i in 0..(super::MAX_LOGGED_FAILURES + 20) {
            stats.record_failure(format!("/f{i}"), "boom".to_string());
        }
        assert_eq!(stats.failures.len(), super::MAX_LOGGED_FAILURES);
        assert_eq!(stats.failures_dropped, 20);
    }
}
