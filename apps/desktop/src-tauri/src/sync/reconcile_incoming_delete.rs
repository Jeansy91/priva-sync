//! Inbound bulk-delete guard: stage real server-initiated deletions
//! instead of applying them immediately, so a whole pull cycle's true delete
//! count is known — durable, in `local_files.sync_status` — before any of it
//! touches disk. Mirrors the outbound guard's own property (`count_pending_deletes`
//! in `db/files.rs`), and follows the same pure-decision/thin-executor split as
//! `reconcile_decide.rs` (ADR-0024), one boundary further out: everything here
//! that can be pure is pure and unit-tested without a `SyncEngine`.

use std::path::{Path, PathBuf};
use anyhow::Result;
use crate::db::LocalDb;
use super::{mark_server_initiated_remove, remove_local_entry, ServerRemoves};
use super::paths::make_conflict_path;
use super::transfer::bulk_delete_should_trigger;

/// What a pull cycle should do about its currently-staged
/// `pending_incoming_delete` rows, checked after every page: mirrors
/// the outbound guard's own decision (same `bulk_delete_should_trigger`
/// math, reused not duplicated — the guard is direction-agnostic), plus the
/// one-shot confirmation flag that lets a paused wave through exactly once.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(super) enum IncomingBulkDeleteDecision {
    /// Under the threshold, or the user already confirmed this wave — keep
    /// paging/applying normally.
    Proceed,
    /// Over the threshold and not yet confirmed — stop paging, pause the
    /// root, leave everything staged (nothing applied).
    Pause,
}

/// Whether the currently-staged count alone is suspicious enough to ask —
/// independent of whether the user has already confirmed it. Split out so
/// the caller can check this before deciding whether it even needs to read
/// (and thereby consume) the one-shot confirm flag.
pub(super) fn incoming_wave_looks_suspicious(pending_count: usize, total_tracked: usize) -> bool {
    pending_count > 0 && bulk_delete_should_trigger(pending_count, total_tracked)
}

pub(super) fn decide_incoming_bulk_delete(
    pending_count: usize, total_tracked: usize, already_confirmed: bool,
) -> IncomingBulkDeleteDecision {
    if incoming_wave_looks_suspicious(pending_count, total_tracked) && !already_confirmed {
        IncomingBulkDeleteDecision::Pause
    } else {
        IncomingBulkDeleteDecision::Proceed
    }
}

/// Result of [`flush_incoming_deletes`]: every path actually removed (the
/// caller records one transfer-history entry per path, same granularity the
/// old immediate-apply code gave), and every never-uploaded local file a
/// staged directory's removal rescued on the way — the caller (which owns
/// the `AppHandle`) turns each into the same `sync_conflict` notification
/// `apply_server_change` always emitted for this case, now from the flush
/// side instead of the stage side.
pub(super) struct FlushOutcome {
    pub applied_paths: Vec<String>,
    pub rescued:        Vec<(String, PathBuf)>,
}

/// Apply-all half of the guard, once a pull cycle either clears the
/// threshold or the user confirms: for every row staged as
/// `pending_incoming_delete`, suppress the watcher immediately before the
/// real unlink (not at staging time — the gap between staging and this call
/// can be arbitrarily long, e.g. waiting on user confirmation, and the
/// suppression TTL is only 30s), rescue any never-uploaded descendants of a
/// staged directory (its removal is recursive, so it would otherwise take
/// local work nowhere else with it), then remove the entry and its DB row.
pub(super) fn flush_incoming_deletes(
    db:         &LocalDb,
    removes:    &ServerRemoves,
    root_id:    &str,
    local_path: &Path,
) -> Result<FlushOutcome> {
    let staged = db.list_pending_incoming_deletes(root_id)?;
    let mut outcome = FlushOutcome { applied_paths: Vec::new(), rescued: Vec::new() };
    for row in staged {
        mark_server_initiated_remove(removes, root_id, &row.path);
        let abs = local_path.join(row.path.trim_start_matches('/'));
        if row.is_dir {
            outcome.rescued.extend(rescue_unsynced_descendants(db, root_id, local_path, &row.path, &abs)?);
        }
        remove_local_entry(&abs)?;
        db.delete_file_record(root_id, &row.path)?;
        outcome.applied_paths.push(row.path);
    }
    Ok(outcome)
}

/// Copy every not-yet-uploaded file under `dir_path` out of the way before
/// the directory is removed. Extracted from the executor's own
/// `rescue_unsynced_descendants` — same rescue-root convention (a
/// `.conflict.<ts>` sibling of the doomed directory, keeping the subtree
/// layout inside it), just DB/FS-only so the flush step needs no
/// `SyncEngine`/`AppHandle`. Best-effort per file: one failed copy is
/// skipped, not fatal to the rest of the removal.
fn rescue_unsynced_descendants(
    db: &LocalDb, root_id: &str, root_local_path: &Path, dir_path: &str, dir_abs: &Path,
) -> Result<Vec<(String, PathBuf)>> {
    let unsynced = db.list_unsynced_under(root_id, dir_path)?;
    if unsynced.is_empty() {
        return Ok(Vec::new());
    }

    let rescue_root = make_conflict_path(dir_abs);
    let mut rescued = Vec::new();
    for rel in &unsynced {
        // `rel` is rooted at the sync root; the part below `dir_path` is what
        // gets recreated inside the rescue directory.
        let Some(below) = rel.strip_prefix(dir_path).map(|s| s.trim_start_matches('/')) else {
            continue;
        };
        let src = root_local_path.join(rel.trim_start_matches('/'));
        if !src.is_file() {
            continue;
        }
        let dest = rescue_root.join(below);
        if let Some(parent) = dest.parent() {
            if std::fs::create_dir_all(parent).is_err() {
                continue;
            }
        }
        if std::fs::copy(&src, &dest).is_ok() {
            rescued.push((rel.clone(), dest));
        }
    }
    Ok(rescued)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::sync::is_server_initiated_remove;

    struct TempDir(std::path::PathBuf);
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

    #[test]
    fn decide_incoming_bulk_delete_pauses_over_threshold_unconfirmed() {
        assert_eq!(
            decide_incoming_bulk_delete(6000, 10_000, false),
            IncomingBulkDeleteDecision::Pause,
        );
    }

    #[test]
    fn decide_incoming_bulk_delete_proceeds_under_threshold() {
        assert_eq!(
            decide_incoming_bulk_delete(3, 10_000, false),
            IncomingBulkDeleteDecision::Proceed,
        );
    }

    /// The AC's confirm path: once the user has signed off, the same wave
    /// that paused the root a moment ago must proceed.
    #[test]
    fn decide_incoming_bulk_delete_proceeds_over_threshold_once_confirmed() {
        assert_eq!(
            decide_incoming_bulk_delete(6000, 10_000, true),
            IncomingBulkDeleteDecision::Proceed,
        );
    }

    /// The flush step is what finally removes bytes + row, and — the bug the
    /// advisor caught — suppresses the watcher right here, not back when the
    /// row was staged (staging can be arbitrarily long before flush, e.g.
    /// waiting on user confirmation; the suppression TTL is only 30s).
    #[test]
    fn flush_incoming_deletes_removes_files_rows_and_suppresses_watcher() {
        let tmp = TempDir::new();
        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        let file_abs = tmp.path().join("a.txt");
        std::fs::write(&file_abs, b"bytes").unwrap();
        db.upsert_file("r1", "/a.txt", 5, 1, b"h", "pending_incoming_delete", None).unwrap();

        let removes: ServerRemoves = std::sync::Mutex::new(std::collections::HashMap::new());
        assert!(!is_server_initiated_remove(&removes, "r1", "/a.txt"), "nothing suppressed before flush");

        let outcome = flush_incoming_deletes(&db, &removes, "r1", tmp.path()).unwrap();

        assert_eq!(outcome.applied_paths, vec!["/a.txt".to_string()]);
        assert!(!file_abs.exists(), "flush must remove the file from disk");
        assert!(db.get_file("r1", "/a.txt").unwrap().is_none(), "flush must remove the DB row");
        assert!(is_server_initiated_remove(&removes, "r1", "/a.txt"),
            "flush must suppress the watcher for the path it just removed");
    }

    /// A staged directory takes local work inside its subtree with it
    /// (recursive removal) — a file queued for upload but never sent would
    /// otherwise lose its only copy. Same rescue `apply_server_change` always
    /// did for a live server-deleted directory, moved to flush time with the
    /// rest of the removal.
    #[test]
    fn flush_incoming_deletes_rescues_unsynced_descendants_of_a_staged_directory() {
        let tmp = TempDir::new();
        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        let dir_abs = tmp.path().join("docs");
        std::fs::create_dir_all(&dir_abs).unwrap();
        std::fs::write(dir_abs.join("never-uploaded.txt"), b"local only").unwrap();

        db.upsert_dir("r1", "/docs", "pending_incoming_delete").unwrap();
        db.upsert_file("r1", "/docs/never-uploaded.txt", 10, 1, b"h", "pending", None).unwrap();

        let removes: ServerRemoves = std::sync::Mutex::new(std::collections::HashMap::new());
        let outcome = flush_incoming_deletes(&db, &removes, "r1", tmp.path()).unwrap();

        assert!(!dir_abs.exists(), "the directory itself must still be removed");
        assert_eq!(outcome.rescued.len(), 1, "the never-uploaded descendant must be rescued, not lost");
        let (rel, conflict_copy) = &outcome.rescued[0];
        assert_eq!(rel, "/docs/never-uploaded.txt");
        assert!(conflict_copy.exists(), "rescued bytes must actually land on disk");
        assert_eq!(std::fs::read(conflict_copy).unwrap(), b"local only");
    }
}
