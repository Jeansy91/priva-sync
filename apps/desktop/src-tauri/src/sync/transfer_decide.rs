//! Pure decision matrix for the push/upload side of the sync engine
//! (ADR-0024 — counterpart to the pull-side split).
//!
//! [`decide_upload_action`] and [`decide_upload_strategy`] take no DB/FS/
//! network access — the executor (`run_uploads` in `transfer.rs`) gathers
//! the handful of already-cheap facts (`is_dir`, `is_delete`, a `metadata()`
//! read for size) and matches on the result. Split into two calls rather
//! than one because the original code only ever computes `file_size` for the
//! upload branch — folding it into a single upfront decision would cost a
//! `metadata()` read for every `CreateDir`/`Delete` row too.
//!
//! Not every branch name the originating issue sketched turns out to be a
//! real *decision*: `Conflict` is a server response the executor only learns
//! after the network call (handled via `materialise_conflict`, not
//! here). [`is_blocked_by_remote_quota`] is the one piece of outcome
//! classification that does belong here — see its own docs.

use super::CHUNKED_UPLOAD_THRESHOLD;
use super::retry::is_remote_quota_error;

/// What a look at the local filesystem says about a staged row's file.
///
/// The distinction that matters is between "gone" and "cannot tell". A
/// `pending` upload whose file is genuinely gone means the file was created
/// and removed again before the cycle reached it, and the right answer is to
/// remove it server-side too. But `Path::exists()` — which this replaces —
/// collapses both cases into `false`: it is defined as `metadata().is_ok()`
/// and answers false just as readily for a permission error on the parent
/// directory, a path too long for the current Windows long-path setting, or a
/// drive that went away for a moment. Reading any of those as "gone" deletes
/// a file server-side that the user never asked to remove, and the deletion
/// is silent — the delete call tolerates a 404, so nothing looks wrong.
#[derive(Debug)]
pub(crate) enum LocalPresence {
    /// The file is there. Carries the two facts the settle check needs
    /// ([`upload_is_settled`]) so it costs no second `metadata()` call.
    Present { size: i64, mtime: i64 },
    /// The file is definitively not there (`ErrorKind::NotFound`).
    Gone,
    /// The filesystem refused to answer. Not a delete — a failure to retry.
    Unreadable(std::io::Error),
}

/// Ask the filesystem about `path`, keeping the reason a negative answer came
/// back. See [`LocalPresence`] for why the reason is the whole point.
pub(crate) fn probe_local(path: &std::path::Path) -> LocalPresence {
    match std::fs::metadata(path) {
        Ok(m) => LocalPresence::Present {
            size:  m.len() as i64,
            mtime: mtime_secs(&m),
        },
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => LocalPresence::Gone,
        Err(e) => LocalPresence::Unreadable(e),
    }
}

/// Modification time as whole seconds since the epoch — the resolution the
/// `local_files` rows store, so both sides of [`upload_is_settled`] are
/// comparable.
pub(crate) fn mtime_secs(meta: &std::fs::Metadata) -> i64 {
    meta.modified()
        .unwrap_or(std::time::UNIX_EPOCH)
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs() as i64
}

/// Whether a row's status already means "this file's current content is queued
/// for upload" — the states [`crate::db::LocalDb::list_pending_batch`] hands
/// the upload path for a file that still exists locally.
///
/// `pending_delete` and `conflict` are deliberately absent: both describe
/// something other than "send the bytes on disk", and a write event on such a
/// path has to go through full staging rather than quietly refreshing the row.
pub(crate) fn is_staged_for_upload(status: &str) -> bool {
    matches!(status, "pending" | "pending_failed" | "blocked_remote_quota" | "in_use")
}

/// Whether a staged file has stopped changing since staging recorded it.
///
/// A file copied into a sync root is visible from its first byte: the create
/// event stages it, and the write events that follow keep changing it. Sending
/// it right away means uploading a fragment, and then uploading it again for
/// every burst that follows — each one a server-side version of a file that
/// was never in that state as far as the user is concerned.
///
/// The criterion is deliberately not a fixed quiet period. Staging already
/// recorded a `(size, mtime)`; if the disk still shows the same pair by the
/// time the upload runs, nothing was written in between and the file is ready.
/// That converges as soon as the writes stop — a small file still uploads one
/// debounce interval after it lands — instead of making every upload wait out
/// a timer sized for the worst case.
pub(crate) fn upload_is_settled(
    staged_size: i64, staged_mtime: i64, size: i64, mtime: i64,
) -> bool {
    staged_size == size && staged_mtime == mtime
}

/// What to do with one staged row before any network call is made.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum UploadAction {
    /// Empty directory — `POST /directories`, no content.
    CreateDir,
    /// Row is staged for deletion (or its local file is already gone).
    Delete,
    /// A real file upload — see [`UploadStrategy`] for which wire protocol.
    Upload,
}

/// Decide the top-level action for one staged row. Pure — mirrors the
/// `if file.is_dir && !is_delete { .. } else if is_delete { .. } else { .. }`
/// dispatch that used to open `run_uploads`'s per-file task.
pub(crate) fn decide_upload_action(is_dir: bool, is_delete: bool) -> UploadAction {
    if is_dir && !is_delete {
        UploadAction::CreateDir
    } else if is_delete {
        UploadAction::Delete
    } else {
        UploadAction::Upload
    }
}

/// Which wire protocol(s) to try for an [`UploadAction::Upload`] row, in
/// order. Delta and chunked are independent gates in the original code (a
/// large file that's also delta-eligible tries delta *then* falls back to
/// chunked, not plain) — the four variants name every combination instead of
/// collapsing them into a single either/or choice.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum UploadStrategy {
    Plain,
    Chunked,
    DeltaThenPlain,
    DeltaThenChunked,
}

impl UploadStrategy {
    /// Whether delta should be attempted first (before any fallback).
    pub(crate) fn tries_delta(self) -> bool {
        matches!(self, UploadStrategy::DeltaThenPlain | UploadStrategy::DeltaThenChunked)
    }

    /// Whether the fallback — used when delta wasn't attempted, or was
    /// attempted and declined — is the chunked protocol rather than plain.
    pub(crate) fn falls_back_to_chunked(self) -> bool {
        matches!(self, UploadStrategy::Chunked | UploadStrategy::DeltaThenChunked)
    }
}

/// Decide the upload strategy for a file whose size is now known. Pure —
/// wraps the two independent eligibility gates that already existed
/// (`should_attempt_delta_upload`, `should_use_chunked_upload`) into one
/// result the executor matches on.
pub(crate) fn decide_upload_strategy(is_mount: bool, known_version: i64, file_size: u64) -> UploadStrategy {
    let try_delta   = should_attempt_delta_upload(is_mount, known_version, file_size);
    let use_chunked = should_use_chunked_upload(file_size, is_mount);
    match (try_delta, use_chunked) {
        (true,  true)  => UploadStrategy::DeltaThenChunked,
        (true,  false) => UploadStrategy::DeltaThenPlain,
        (false, true)  => UploadStrategy::Chunked,
        (false, false) => UploadStrategy::Plain,
    }
}

/// Pure routing decision: a file above [`super::CHUNKED_UPLOAD_THRESHOLD`]
/// goes through the multi-part protocol instead of a single PUT — except on
/// a mount, which has no chunked-upload endpoint (ADR-0011).
fn should_use_chunked_upload(file_size: u64, is_mount: bool) -> bool {
    file_size >= CHUNKED_UPLOAD_THRESHOLD && !is_mount
}

/// Pure routing decision: delta-sync (ADR-0002) is only attempted for
/// an owned-root file (mounts have no delta endpoints) that the client
/// already has a known server version for, and whose size clears the block
/// threshold — below that a full upload is cheaper than the round-trip for
/// signatures.
fn should_attempt_delta_upload(is_mount: bool, known_version: i64, file_size: u64) -> bool {
    !is_mount && known_version >= 0 && file_size > crate::sync::delta::BLOCK_SIZE as u64
}

/// True when an upload failure is the giver's quota being full on a Shared
/// Mount (HTTP 507, ADR-0011) — the one case where the executor routes to
/// `blocked_remote_quota` (steady poll) instead of the exponential
/// `pending_failed` backoff. Gated on `is_mount`: an owned root hitting 507
/// is the user's *own* quota, which doesn't get the "wait for someone else"
/// treatment — that keeps failing until the user acts, so it stays on the
/// normal backoff.
pub(crate) fn is_blocked_by_remote_quota(is_mount: bool, err: &anyhow::Error) -> bool {
    is_mount && is_remote_quota_error(err)
}

#[cfg(test)]
mod probe_tests {
    use super::*;

    #[test]
    fn probe_local_reports_an_existing_file_as_present() {
        let dir  = tempfile::tempdir().unwrap();
        let file = dir.path().join("a.txt");
        std::fs::write(&file, b"x").unwrap();

        assert!(matches!(probe_local(&file), LocalPresence::Present { size: 1, .. }));
    }

    #[test]
    fn probe_local_reports_a_missing_file_as_gone() {
        let dir = tempfile::tempdir().unwrap();

        assert!(matches!(probe_local(&dir.path().join("nope.txt")), LocalPresence::Gone));
    }

    /// A missing *parent* is still a genuine NotFound — the file really is
    /// not there, so this must not be mistaken for an unreadable stat.
    #[test]
    fn probe_local_reports_a_missing_parent_as_gone() {
        let dir = tempfile::tempdir().unwrap();

        assert!(matches!(
            probe_local(&dir.path().join("no-such-dir").join("a.txt")),
            LocalPresence::Gone,
        ));
    }

    /// The distinction the whole type exists for: an error that is *not*
    /// NotFound must never read as "gone", because "gone" deletes the file
    /// server-side. A NUL byte makes the OS reject the path outright, which
    /// is a portable way to get a non-NotFound error out of `metadata()`.
    #[test]
    fn probe_local_never_reports_a_non_notfound_error_as_gone() {
        let probed = probe_local(std::path::Path::new("invalid\0path"));

        match probed {
            LocalPresence::Unreadable(e) => {
                assert_ne!(e.kind(), std::io::ErrorKind::NotFound);
            }
            other => panic!("expected Unreadable, got {other:?}"),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// `list_pending_batch` hands the upload path retry-due failures alongside plain
    /// pending rows. Leaving those out of the settle check's eligible set
    /// would skip such a row on every cycle without ever refreshing what it
    /// compares against — it would never upload again.
    #[test]
    fn every_retryable_upload_state_counts_as_staged() {
        assert!(is_staged_for_upload("pending"));
        assert!(is_staged_for_upload("pending_failed"));
        assert!(is_staged_for_upload("blocked_remote_quota"));
        assert!(is_staged_for_upload("in_use"));
    }

    #[test]
    fn delete_conflict_and_synced_are_not_staged_uploads() {
        assert!(!is_staged_for_upload("pending_delete"));
        assert!(!is_staged_for_upload("conflict"));
        assert!(!is_staged_for_upload("synced"));
    }

    #[test]
    fn unchanged_size_and_mtime_means_settled() {
        assert!(upload_is_settled(4096, 1_700_000_000, 4096, 1_700_000_000));
    }

    /// The copy-in-progress case: the row was staged at a fragment, the file
    /// has grown since. Uploading now would publish a partial file.
    #[test]
    fn a_grown_file_is_not_settled() {
        assert!(!upload_is_settled(4096, 1_700_000_000, 8192, 1_700_000_000));
    }

    /// Rewritten in place with the same length — size alone would call this
    /// settled, which is why mtime is part of the criterion.
    #[test]
    fn a_rewrite_of_the_same_length_is_not_settled() {
        assert!(!upload_is_settled(4096, 1_700_000_000, 4096, 1_700_000_042));
    }

    /// Truncating counts too: the file is being rewritten, not shrunk once.
    #[test]
    fn a_shrunk_file_is_not_settled() {
        assert!(!upload_is_settled(8192, 1_700_000_000, 0, 1_700_000_000));
    }

    #[test]
    fn remote_quota_507_on_a_mount_is_blocked_by_remote_quota() {
        let e = anyhow::anyhow!("upload file: HTTP 507 Insufficient Storage");
        assert!(is_blocked_by_remote_quota(true, &e));
    }

    // An owned root hitting 507 is the user's own quota, not the giver's —
    // that stays on the normal exponential backoff, not the steady poll.
    #[test]
    fn remote_quota_507_on_an_owned_root_is_not_blocked_by_remote_quota() {
        let e = anyhow::anyhow!("upload file: HTTP 507 Insufficient Storage");
        assert!(!is_blocked_by_remote_quota(false, &e));
    }

    #[test]
    fn non_507_error_on_a_mount_is_not_blocked_by_remote_quota() {
        let e = anyhow::anyhow!("upload file: HTTP 500 internal server error");
        assert!(!is_blocked_by_remote_quota(true, &e));
    }

    #[test]
    fn live_directory_creates_dir() {
        assert_eq!(decide_upload_action(true, false), UploadAction::CreateDir);
    }

    // A directory staged for deletion is a delete, not a dir-create — the
    // is_dir flag only matters for live (non-delete) rows.
    #[test]
    fn deleted_directory_is_a_delete() {
        assert_eq!(decide_upload_action(true, true), UploadAction::Delete);
    }

    #[test]
    fn deleted_file_is_a_delete() {
        assert_eq!(decide_upload_action(false, true), UploadAction::Delete);
    }

    #[test]
    fn live_file_uploads() {
        assert_eq!(decide_upload_action(false, false), UploadAction::Upload);
    }

    #[test]
    fn chunked_upload_routing_respects_size_and_mount() {
        assert!(should_use_chunked_upload(CHUNKED_UPLOAD_THRESHOLD, false));
        assert!(should_use_chunked_upload(CHUNKED_UPLOAD_THRESHOLD + 1, false));
        assert!(!should_use_chunked_upload(CHUNKED_UPLOAD_THRESHOLD - 1, false),
            "just under the threshold must stay single-shot");
        assert!(!should_use_chunked_upload(CHUNKED_UPLOAD_THRESHOLD * 10, true),
            "mounts never use chunked upload regardless of size");
    }

    // Delta upload is only attempted for an owned root with a known server
    // version above the block-size floor; any of the three gates failing
    // falls back to a normal upload.
    #[test]
    fn delta_upload_eligibility_requires_owned_known_version_and_size() {
        let big = crate::sync::delta::BLOCK_SIZE as u64 + 1;
        assert!(should_attempt_delta_upload(false, 3, big), "owned root, known version, big enough file");
        assert!(!should_attempt_delta_upload(true, 3, big), "mounts have no delta endpoint");
        assert!(!should_attempt_delta_upload(false, -1, big), "unknown server_version can't If-Match");
        assert!(!should_attempt_delta_upload(false, 3, crate::sync::delta::BLOCK_SIZE as u64),
            "file at exactly one block is not worth a delta round-trip");
    }

    #[test]
    fn small_owned_file_with_no_known_version_is_plain() {
        assert_eq!(decide_upload_strategy(false, -1, 100), UploadStrategy::Plain);
    }

    #[test]
    fn small_mount_file_is_plain() {
        assert_eq!(decide_upload_strategy(true, 3, 100), UploadStrategy::Plain);
    }

    // Mounts have no chunked-upload endpoint either (ADR-0011) — a large
    // mount file falls all the way back to plain, single-shot PUT.
    #[test]
    fn large_mount_file_is_plain_no_chunked_no_delta() {
        let big = CHUNKED_UPLOAD_THRESHOLD * 10;
        let strategy = decide_upload_strategy(true, 3, big);
        assert_eq!(strategy, UploadStrategy::Plain);
        assert!(!strategy.tries_delta());
        assert!(!strategy.falls_back_to_chunked());
    }

    // A large owned-root file with no known server_version can't try delta
    // (no If-Match target) but is still large enough for chunked.
    #[test]
    fn large_owned_file_with_no_known_version_uses_chunked_not_delta() {
        let big = CHUNKED_UPLOAD_THRESHOLD * 10;
        let strategy = decide_upload_strategy(false, -1, big);
        assert_eq!(strategy, UploadStrategy::Chunked);
        assert!(!strategy.tries_delta());
        assert!(strategy.falls_back_to_chunked());
    }

    // The key case the upload-side split exists for: a large, delta-eligible owned file must
    // try delta first but fall back to *chunked*, not plain, if delta
    // declines — the two gates are independent, not either/or.
    #[test]
    fn large_delta_eligible_owned_file_tries_delta_then_falls_back_to_chunked() {
        let big = CHUNKED_UPLOAD_THRESHOLD * 10;
        let strategy = decide_upload_strategy(false, 3, big);
        assert_eq!(strategy, UploadStrategy::DeltaThenChunked);
        assert!(strategy.tries_delta());
        assert!(strategy.falls_back_to_chunked());
    }

    #[test]
    fn small_delta_eligible_owned_file_tries_delta_then_falls_back_to_plain() {
        let size = crate::sync::delta::BLOCK_SIZE as u64 + 1;
        let strategy = decide_upload_strategy(false, 3, size);
        assert_eq!(strategy, UploadStrategy::DeltaThenPlain);
        assert!(strategy.tries_delta());
        assert!(!strategy.falls_back_to_chunked());
    }
}
