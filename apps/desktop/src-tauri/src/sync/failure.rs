//! Shared failure/conflict outcome handling for the sync engine (ADR-0024
//! Vorstufe).
//!
//! Both helpers here replace copy-pasted choreography that used to live
//! separately in `transfer.rs` (upload path) and `reconcile.rs` (download
//! path): computing the retry backoff and emitting `sync_failure`, and
//! materialising a conflict copy + emitting `sync_conflict`. The event emit
//! itself is injected as a closure (rather than a `tauri::AppHandle`
//! parameter) so both helpers stay unit-testable without a running app.
//! Persisting the actual server-side bookkeeping stays with the caller in
//! both cases, since the two call sites know different things about the
//! write (a 409 response carries an exact `server_version`; a normal pull
//! download only has `mtime`).

use std::path::{Path, PathBuf};
use anyhow::Result;
use tracing::{debug, warn};

use crate::db::LocalDb;
use super::retry::compute_next_retry;
use super::paths::make_conflict_path;
use super::download::StagedDownload;

/// An auth failure says nothing about the file: recording it would
/// back the row off and, after a few rounds, park it until the next client
/// update — for a session that a refresh, or the next login, restores. The
/// row simply stays queued for the next cycle.
pub(super) fn leave_pending_on_auth_failure(path: &str, err: &anyhow::Error) {
    debug!("'{path}' left pending, session not usable: {err}");
}

/// Record a failed sync operation: compute the retry backoff, run the
/// caller's persistence step, warn, and emit `sync_failure`. `attempt` is the
/// attempt number this failure represents (the three upload/delete/dir call
/// sites in `transfer.rs` pass the row's `attempt_count + 1`; the
/// server-change call site in `reconcile.rs` always starts a fresh row at
/// `1`).
pub(super) fn record_failure(
    verb:    &str,
    kind:    &str,
    path:    &str,
    err:     &anyhow::Error,
    attempt: i64,
    persist: impl FnOnce(i64),
    emit:    impl FnOnce(serde_json::Value),
) {
    let now = chrono::Utc::now().timestamp();
    let next_retry = compute_next_retry(now, attempt).unwrap_or(i64::MAX);
    persist(next_retry);
    warn!("{verb} failed for '{path}': {err} (next retry in {}s)",
        if next_retry == i64::MAX { "manual".to_string() } else { (next_retry - now).to_string() });
    emit(serde_json::json!({
        "kind":  kind,
        "path":  path,
        "error": err.to_string(),
    }));
}

/// What an upload's 409 actually means once the server's hash is compared to
/// the local content.
#[derive(Debug, PartialEq, Eq)]
pub(super) enum UploadConflictResolution {
    /// Server already holds our exact bytes; only the local `server_version`
    /// was stale. No conflict copy, no download.
    AlreadyInSync,
    RealConflict,
}

/// Decide whether an upload 409 is a real conflict. `local_hash` must be
/// freshly computed from the file on disk, not the staged row's hash, which
/// can lag behind a write still in progress. Any unparsable `server_hash`
/// counts as a real conflict: the safe direction keeps the loser copy.
pub(super) fn resolve_upload_conflict(local_hash: &[u8], server_hash: &str) -> UploadConflictResolution {
    match hex::decode(server_hash) {
        Ok(server) if !server.is_empty() && server == local_hash => UploadConflictResolution::AlreadyInSync,
        _ => UploadConflictResolution::RealConflict,
    }
}

/// Handle the "not a conflict after all" case of an upload 409: when the
/// server's hash equals the local file's, only the row's `server_version` was
/// stale, so record the new version and report `true`. Returns `false` (leave
/// everything to the conflict path) for a real conflict, an unreadable local
/// file, or a failed DB write.
pub(super) fn settle_upload_conflict_if_identical(
    db:             &LocalDb,
    root_id:        &str,
    path:           &str,
    local_path:     &Path,
    server_version: i64,
    server_hash:    &str,
) -> bool {
    let Ok(local_hash) = super::hash_file(local_path) else { return false };
    if resolve_upload_conflict(&local_hash, server_hash) != UploadConflictResolution::AlreadyInSync {
        return false;
    }
    db.set_synced_with_version(root_id, path, &local_hash, server_version).is_ok()
}

/// After a conflict is detected (an upload's 409, or a modified-local vs.
/// changed-server pull), save the local "loser" as a `.conflict.<ts>` copy,
/// publish the server's `winner` over `local_path`, mark the row `conflict`
/// in the DB, and emit `sync_conflict`. `server_conflict_path` carries the
/// server's own loser-copy path when the caller has one (only the upload/409
/// path does). `persist_server_state` receives the winner's hash and size and
/// records whatever server-side bookkeeping the caller has for the write (see
/// module docs).
///
/// The winner arrives already staged on disk rather than as a byte slice, so
/// resolving a conflict over a large file costs no more memory than any other
/// download. The ordering that matters is unaffected: the loser copy
/// is taken before `local_path` is overwritten, because the overwrite is the
/// rename inside [`StagedDownload::publish`], not the filling of the temp
/// file.
///
/// Every step here is real state (disk write, DB row, event) so a failure at
/// any point aborts the rest rather than emitting `sync_conflict` for a
/// write that didn't actually land.
#[expect(clippy::too_many_arguments, reason = "the two closures are the per-caller seams described in the module docs")]
pub(super) fn materialise_conflict(
    db:                   &LocalDb,
    root_id:              &str,
    path:                 &str,
    local_path:           &Path,
    winner:               StagedDownload,
    server_conflict_path: Option<&str>,
    persist_server_state: impl FnOnce(&[u8], i64) -> Result<()>,
    emit:                 impl FnOnce(serde_json::Value),
) -> Result<PathBuf> {
    let conflict_path = make_conflict_path(local_path);
    if local_path.exists() {
        std::fs::copy(local_path, &conflict_path)?;
    }

    let (hash, size) = (winner.hash.clone(), winner.size);
    winner.publish(local_path)?;
    persist_server_state(&hash, size)?;
    db.set_conflict(root_id, path, &conflict_path.to_string_lossy())?;

    let mut payload = serde_json::json!({
        "path":          path,
        "conflict_copy": conflict_path.to_string_lossy(),
    });
    if let Some(scp) = server_conflict_path {
        payload["server_conflict_path"] = serde_json::json!(scp);
    }
    emit(payload);

    Ok(conflict_path)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::cell::Cell;

    fn test_db() -> LocalDb {
        let tmp = tempfile::tempdir().unwrap();
        LocalDb::open(tmp.path().join("db.sqlite")).unwrap()
    }

    /// The winner as it reaches this module in production: already on disk in
    /// a temp file next to its destination, already hashed.
    fn winner(dest: &Path, data: &[u8]) -> StagedDownload {
        StagedDownload::staged_for_test(dest, data)
    }

    #[test]
    fn upload_conflict_with_matching_hash_is_already_in_sync() {
        let local = blake3::hash(b"same content").as_bytes().to_vec();
        let server = hex::encode(&local);
        assert_eq!(
            resolve_upload_conflict(&local, &server),
            UploadConflictResolution::AlreadyInSync,
        );
    }

    #[test]
    fn upload_conflict_with_different_hash_is_a_real_conflict() {
        let local = blake3::hash(b"mine").as_bytes().to_vec();
        let server = hex::encode(blake3::hash(b"theirs").as_bytes());
        assert_eq!(resolve_upload_conflict(&local, &server), UploadConflictResolution::RealConflict);
    }

    #[test]
    fn upload_conflict_hash_comparison_ignores_hex_case() {
        let local = blake3::hash(b"same content").as_bytes().to_vec();
        let server = hex::encode(&local).to_uppercase();
        assert_eq!(resolve_upload_conflict(&local, &server), UploadConflictResolution::AlreadyInSync);
    }

    #[test]
    fn upload_conflict_with_unusable_server_hash_is_a_real_conflict() {
        let local = blake3::hash(b"x").as_bytes().to_vec();
        for bad in ["", "not-hex", "abc"] {
            assert_eq!(resolve_upload_conflict(&local, bad), UploadConflictResolution::RealConflict, "{bad:?}");
        }
        // An empty local hash must never match an empty server hash.
        assert_eq!(resolve_upload_conflict(&[], ""), UploadConflictResolution::RealConflict);
    }

    #[test]
    fn settle_upload_conflict_marks_row_synced_when_server_already_has_the_bytes() {
        let dir = tempfile::tempdir().unwrap();
        let local_path = dir.path().join("a.txt");
        std::fs::write(&local_path, b"same content").unwrap();
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 12, 1, b"stale", "pending", None).unwrap();
        let server_hash = hex::encode(blake3::hash(b"same content").as_bytes());

        let settled = settle_upload_conflict_if_identical(&db, "r1", "/a.txt", &local_path, 9, &server_hash);

        assert!(settled);
        let row = db.get_file("r1", "/a.txt").unwrap().unwrap();
        assert_eq!(row.sync_status, "synced");
        assert_eq!(row.server_version, 9);
        assert!(!make_conflict_path(&local_path).exists());
    }

    #[test]
    fn settle_upload_conflict_leaves_row_alone_on_real_conflict() {
        let dir = tempfile::tempdir().unwrap();
        let local_path = dir.path().join("a.txt");
        std::fs::write(&local_path, b"mine").unwrap();
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 4, 1, b"h", "pending", None).unwrap();
        let server_hash = hex::encode(blake3::hash(b"theirs").as_bytes());

        assert!(!settle_upload_conflict_if_identical(&db, "r1", "/a.txt", &local_path, 9, &server_hash));
        assert_eq!(db.get_file("r1", "/a.txt").unwrap().unwrap().sync_status, "pending");
    }

    #[test]
    fn settle_upload_conflict_treats_unreadable_local_file_as_real_conflict() {
        let dir = tempfile::tempdir().unwrap();
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 4, 1, b"h", "pending", None).unwrap();
        let server_hash = hex::encode(blake3::hash(b"x").as_bytes());

        assert!(!settle_upload_conflict_if_identical(
            &db, "r1", "/a.txt", &dir.path().join("missing.txt"), 9, &server_hash));
    }

    // Attempt 1 always lands on the first backoff bucket (60s) — this is the
    // fixed attempt reconcile.rs's server-change failure path passes, so its
    // retry timing must stay deterministic regardless of `now`.
    #[test]
    fn record_failure_persists_first_bucket_retry_for_attempt_one() {
        let persisted = Cell::new(-1i64);
        let emitted = Cell::new(false);
        let err = anyhow::anyhow!("boom");
        record_failure("op", "kind", "/a.txt", &err, 1,
            |next_retry| persisted.set(next_retry),
            |_payload| emitted.set(true),
        );
        let now = chrono::Utc::now().timestamp();
        let got = persisted.get();
        assert!(got > now, "next_retry must be in the future");
        assert!(got <= now + 61, "attempt 1 must land on the ~60s bucket, got {}", got - now);
        assert!(emitted.get(), "sync_failure must always emit");
    }

    // Once the backoff schedule is exhausted (attempt beyond the last
    // bucket), the row falls back to manual-only retry — the sentinel used
    // by every call site to mean "no more automatic retries".
    #[test]
    fn record_failure_falls_back_to_manual_after_schedule_exhausted() {
        let persisted = Cell::new(-1i64);
        let err = anyhow::anyhow!("boom");
        record_failure("op", "kind", "/a.txt", &err, 999,
            |next_retry| persisted.set(next_retry),
            |_payload| {},
        );
        assert_eq!(persisted.get(), i64::MAX);
    }

    // The emitted payload must carry the caller's kind/path/error regardless
    // of the persist step — the sync_failure shape all callers rely on.
    #[test]
    fn record_failure_emit_payload_carries_kind_path_error() {
        let captured: Cell<Option<serde_json::Value>> = Cell::new(None);
        let err = anyhow::anyhow!("network unreachable");
        record_failure("upload", "upload", "/docs/a.txt", &err, 1,
            |_| {},
            |payload| captured.set(Some(payload)),
        );
        let payload = captured.into_inner().expect("must emit");
        assert_eq!(payload["kind"], "upload");
        assert_eq!(payload["path"], "/docs/a.txt");
        assert_eq!(payload["error"], "network unreachable");
    }

    // materialise_conflict must pick the same conflict-suffix naming as
    // make_conflict_path and actually write the winning bytes to disk.
    #[test]
    fn materialise_conflict_writes_winner_bytes_and_names_loser_copy() {
        let dir = tempfile::tempdir().unwrap();
        let local_path = dir.path().join("notes.txt");
        std::fs::write(&local_path, b"local version").unwrap();

        let db = test_db();
        let data = b"server version";

        let conflict_path = materialise_conflict(
            &db, "root1", "/notes.txt", &local_path, winner(&local_path, data), None,
            |_hash, _size| Ok(()),
            |_payload| {},
        ).unwrap();

        assert!(conflict_path.file_name().unwrap().to_string_lossy().starts_with("notes.conflict."));
        assert_eq!(std::fs::read(&local_path).unwrap(), data, "local_path must now hold the server bytes");
        assert_eq!(std::fs::read(&conflict_path).unwrap(), b"local version", "conflict copy must hold the loser bytes");
    }

    // No prior local file (e.g. server change arriving before any local
    // write ever landed): the loser-copy step is a no-op, but the winner
    // bytes must still land and the conflict must still be recorded.
    #[test]
    fn materialise_conflict_without_prior_local_file_still_writes_winner() {
        let dir = tempfile::tempdir().unwrap();
        let local_path = dir.path().join("new.txt");

        let db = test_db();
        let data = b"server version";

        let conflict_path = materialise_conflict(
            &db, "root1", "/new.txt", &local_path, winner(&local_path, data), Some("server/loser/path"),
            |_hash, _size| Ok(()),
            |_payload| {},
        ).unwrap();

        assert!(!conflict_path.exists(), "no loser file existed, so no copy should be written");
        assert_eq!(std::fs::read(&local_path).unwrap(), data);
    }

    // A persist_server_state failure must abort before set_conflict/emit —
    // no half-applied conflict row for a write the caller couldn't record.
    #[test]
    fn materialise_conflict_propagates_persist_failure() {
        let dir = tempfile::tempdir().unwrap();
        let local_path = dir.path().join("notes.txt");
        std::fs::write(&local_path, b"local version").unwrap();

        let db = test_db();
        let emitted = Cell::new(false);

        let result = materialise_conflict(
            &db, "root1", "/notes.txt", &local_path, winner(&local_path, b"server version"), None,
            |_hash, _size| Err(anyhow::anyhow!("persist failed")),
            |_payload| emitted.set(true),
        );

        assert!(result.is_err());
        assert!(!emitted.get(), "must not emit sync_conflict when persistence failed");
        assert!(db.get_file("root1", "/notes.txt").unwrap().is_none(), "row must stay untouched on persist failure");
    }

    // The caller records the winner's hash and size without ever holding its
    // bytes — that is what lets a conflict over a multi-GB file resolve
    // without a multi-GB buffer.
    #[test]
    fn materialise_conflict_hands_the_caller_the_winners_hash_and_size() {
        let dir = tempfile::tempdir().unwrap();
        let local_path = dir.path().join("notes.txt");
        std::fs::write(&local_path, b"local version").unwrap();

        let db = test_db();
        let data = b"server version";
        let seen: Cell<Option<(Vec<u8>, i64)>> = Cell::new(None);

        materialise_conflict(
            &db, "root1", "/notes.txt", &local_path, winner(&local_path, data), None,
            |hash, size| { seen.set(Some((hash.to_vec(), size))); Ok(()) },
            |_payload| {},
        ).unwrap();

        let (hash, size) = seen.into_inner().expect("persist must run");
        assert_eq!(hash, blake3::hash(data).as_bytes().to_vec());
        assert_eq!(size, data.len() as i64);
    }
}
