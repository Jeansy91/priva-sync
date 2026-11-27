//! Pure decision for the `failed_server_changes` retry sweep (ADR-0062).
//!
//! [`decide_retry_outcome`] takes no DB/HTTP — the executor
//! (`retry_failed_server_changes` in `reconcile.rs`) batches the due rows'
//! paths through the stat endpoint and matches each result through this.
//! Same split as `reconcile_decide.rs` (ADR-0024): the correctness property
//! that matters here — a retry must never resurrect a path the server has
//! since deleted, and must always act on the server's *current* state, not
//! the stale snapshot the row failed with — is exactly what this function
//! encodes, so it is the one piece worth testing without a live SyncEngine.

use crate::api::{FileStatEntry, ServerFileEntry};

/// What the retry sweep should do with one due row, given the server's
/// current state for its path.
#[derive(Debug)]
pub(crate) enum RetryOutcome {
    /// The server has no record of this path at all — either nothing was
    /// ever there, or it was and the row simply outlived its own tombstone
    /// (the tombstone already applied on a prior cycle). Either way there is
    /// nothing to apply; drop the retry row.
    Drop,
    /// The server's current state for this path, live or tombstoned. Apply
    /// exactly this, never the stale payload the row originally failed with.
    Apply(ServerFileEntry),
}

/// Decide the outcome for one stat result. Pure — unit-testable without
/// DB/HTTP.
pub(crate) fn decide_retry_outcome(stat: &FileStatEntry) -> RetryOutcome {
    if !stat.found {
        return RetryOutcome::Drop;
    }
    RetryOutcome::Apply(ServerFileEntry {
        id:             stat.id.clone(),
        path:           stat.path.clone(),
        size:           stat.size,
        mtime:          stat.mtime.clone(),
        content_hash:   if stat.content_hash.is_empty() { None } else { Some(stat.content_hash.clone()) },
        is_deleted:     stat.is_deleted,
        updated_at:     stat.updated_at.clone(),
        server_version: Some(stat.server_version),
        is_dir:         stat.is_dir,
        seq:            0,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn found(is_deleted: bool) -> FileStatEntry {
        FileStatEntry {
            path:           "/a.txt".to_string(),
            found:          true,
            id:             "f1".to_string(),
            size:           5,
            mtime:          "2026-01-01T00:00:00Z".to_string(),
            content_hash:   "abcd".to_string(),
            is_deleted,
            is_dir:         false,
            updated_at:     "2026-01-01T00:00:00Z".to_string(),
            server_version: 3,
        }
    }

    // The whole point of ADR-0062: a path the server never had (or no longer
    // has any record of) must not be resurrected -- Drop, never Apply. This
    // is the regression the incident needed: replaying a stale
    // payload for an already-deleted path is exactly what brought the file
    // back.
    #[test]
    fn not_found_drops_the_retry_row() {
        let stat = FileStatEntry { path: "/gone.txt".to_string(), found: false, ..Default::default() };
        assert!(matches!(decide_retry_outcome(&stat), RetryOutcome::Drop));
    }

    // Found-and-deleted must still Apply (the tombstone), not Drop -- the
    // apply path is what actually removes whatever the failed attempt left
    // behind locally.
    #[test]
    fn found_and_deleted_applies_the_tombstone() {
        match decide_retry_outcome(&found(true)) {
            RetryOutcome::Apply(entry) => assert!(entry.is_deleted),
            other => panic!("want Apply(is_deleted=true), got {other:?}"),
        }
    }

    // Found-and-live must carry the *current* server_version/content_hash
    // through -- a changed path must not be re-applied with whatever it
    // looked like when the retry row was queued.
    #[test]
    fn found_and_live_applies_current_state() {
        match decide_retry_outcome(&found(false)) {
            RetryOutcome::Apply(entry) => {
                assert!(!entry.is_deleted);
                assert_eq!(entry.server_version, Some(3));
                assert_eq!(entry.content_hash, Some("abcd".to_string()));
            }
            other => panic!("want Apply(is_deleted=false), got {other:?}"),
        }
    }

    // An empty content_hash (directories never carry one) must map to None,
    // not Some("") -- callers downstream (e.g. download_server_entry's hex
    // decode) treat the two very differently.
    #[test]
    fn empty_content_hash_becomes_none() {
        let stat = FileStatEntry {
            path: "/dir".to_string(), found: true, is_dir: true, ..Default::default()
        };
        match decide_retry_outcome(&stat) {
            RetryOutcome::Apply(entry) => assert_eq!(entry.content_hash, None),
            other => panic!("want Apply, got {other:?}"),
        }
    }
}
