//! Pure decision matrix for the pull/download side of the sync engine
//! (ADR-0024 — counterpart to the upload-side split).
//!
//! [`decide_server_change`] takes no DB/FS/AppHandle — every fact it needs
//! is gathered up front into a [`LocalState`] by the executor
//! (`apply_server_change` in `reconcile.rs`), which then matches on the
//! returned [`ServerChangeAction`] and performs it. Same split as
//! `predicates.rs`, just returning an action instead of a bool.
//!
//! The path-validation and open-conflict guards stay as early returns in the
//! executor rather than variants here: both are cheap, order-sensitive,
//! side-effecting pre-checks (the path check itself does a Windows
//! `read_dir` for case-collision detection, and the path-warning event fires
//! regardless of the outcome) that must run — and can short-circuit —
//! *before* the executor risks a fallible `hash_file` for the rest of the
//! matrix. Folding them into this function would mean gathering `LocalState`
//! unconditionally, including for entries that get skipped before a local
//! hash is ever attempted in the original code.

use crate::api::ServerFileEntry;
use super::LocalChange;

/// Local-side facts the decision needs, gathered by the executor before
/// calling [`decide_server_change`]. Kept separate from `ServerFileEntry` so
/// the decision itself touches no DB/FS.
#[derive(Debug)]
pub(crate) struct LocalState {
    /// Local change relative to the last known server state.
    pub local_change: LocalChange,
    /// The local file's current content already matches `entry.content_hash`
    /// — `false` whenever the local file doesn't exist, the server sent no
    /// hash to compare against, or the content genuinely differs.
    pub content_matches: bool,
    /// This root is a mount the recipient cannot write to (ADR-0011) — a
    /// conflicting local edit is discarded rather than preserved.
    pub is_readonly_mount: bool,
}

/// What to do with one server-reported change, given what the executor found
/// locally. Mirrors the branch matrix that used to live inline in
/// `apply_server_change` (path/open-conflict skips excepted — see module docs).
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) enum ServerChangeAction {
    /// Server deleted the file but the local copy has unsaved changes — keep
    /// local, it goes back to the server on the next push cycle.
    KeepLocalOverDelete,
    /// Both sides deleted — drop the DB row, nothing to do on disk.
    ForgetDeletedRow,
    /// Server deleted the file and local has no pending change — remove the
    /// on-disk copy (if any) and the DB row.
    RemoveLocal,
    /// Server-side directory — create it locally and mark synced.
    CreateLocalDir,
    /// Local edit conflicts with a server update — materialise a conflict
    /// copy, then download the server's version.
    Conflict,
    /// Read-only mount: the local edit is discarded in favour of the server
    /// version — warn/notify, then download.
    DiscardReadonlyEdit,
    /// Local content already matches the server's — record the row as
    /// synced, no bytes move.
    AlreadyInSync,
    /// Download the server's bytes over the local file.
    Download,
}

/// Decide what a pull cycle should do with one server change, given the
/// entry itself is not a blocking path problem and not an open conflict (the
/// executor has already ruled those out — see module docs). Pure — no
/// DB/FS access, so it is unit-testable without a `SyncEngine`.
pub(crate) fn decide_server_change(entry: &ServerFileEntry, local: &LocalState) -> ServerChangeAction {
    if entry.is_deleted {
        return match local.local_change {
            LocalChange::Modified => ServerChangeAction::KeepLocalOverDelete,
            LocalChange::Deleted  => ServerChangeAction::ForgetDeletedRow,
            LocalChange::None     => ServerChangeAction::RemoveLocal,
        };
    }

    if entry.is_dir {
        return ServerChangeAction::CreateLocalDir;
    }

    match local.local_change {
        // Only a real divergence (content differs) is a conflict — a
        // `Modified` row whose bytes already match the server's falls
        // through to the same already-in-sync check as `LocalChange::None`.
        LocalChange::Modified if !local.content_matches => {
            if local.is_readonly_mount {
                ServerChangeAction::DiscardReadonlyEdit
            } else {
                ServerChangeAction::Conflict
            }
        }
        // Local deleted, server updated: always re-download, regardless of
        // content — there is no local file left to compare against.
        LocalChange::Deleted => ServerChangeAction::Conflict,
        _ => {
            if local.content_matches {
                ServerChangeAction::AlreadyInSync
            } else {
                ServerChangeAction::Download
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn entry(is_deleted: bool, is_dir: bool) -> ServerFileEntry {
        ServerFileEntry {
            id:             "id1".to_string(),
            path:           "/a.txt".to_string(),
            is_deleted,
            is_dir,
            content_hash:   Some("abc".to_string()),
            size:           3,
            mtime:          "2024-01-01T00:00:00.000Z".to_string(),
            updated_at:     "2024-01-01T00:00:00.000Z".to_string(),
            server_version: Some(1),
            seq:            0,
        }
    }

    fn local(local_change: LocalChange) -> LocalState {
        LocalState {
            local_change,
            content_matches: false,
            is_readonly_mount: false,
        }
    }

    #[test]
    fn deleted_entry_with_local_modification_keeps_local() {
        let l = local(LocalChange::Modified);
        assert_eq!(decide_server_change(&entry(true, false), &l), ServerChangeAction::KeepLocalOverDelete);
    }

    #[test]
    fn deleted_entry_with_local_also_deleted_forgets_row() {
        let l = local(LocalChange::Deleted);
        assert_eq!(decide_server_change(&entry(true, false), &l), ServerChangeAction::ForgetDeletedRow);
    }

    #[test]
    fn deleted_entry_with_no_local_change_removes_local() {
        let l = local(LocalChange::None);
        assert_eq!(decide_server_change(&entry(true, false), &l), ServerChangeAction::RemoveLocal);
    }

    // A deleted directory entry follows the same delete sub-matrix as a file
    // — is_dir is only consulted for *live* entries.
    #[test]
    fn deleted_directory_entry_follows_the_delete_matrix_too() {
        let l = local(LocalChange::None);
        assert_eq!(decide_server_change(&entry(true, true), &l), ServerChangeAction::RemoveLocal);
    }

    #[test]
    fn server_directory_creates_local_dir_regardless_of_local_change() {
        let l = local(LocalChange::Modified);
        assert_eq!(decide_server_change(&entry(false, true), &l), ServerChangeAction::CreateLocalDir);
    }

    #[test]
    fn modified_with_differing_content_conflicts_on_a_normal_root() {
        let mut l = local(LocalChange::Modified);
        l.content_matches = false;
        assert_eq!(decide_server_change(&entry(false, false), &l), ServerChangeAction::Conflict);
    }

    #[test]
    fn modified_with_differing_content_discards_edit_on_readonly_mount() {
        let mut l = local(LocalChange::Modified);
        l.content_matches = false;
        l.is_readonly_mount = true;
        assert_eq!(decide_server_change(&entry(false, false), &l), ServerChangeAction::DiscardReadonlyEdit);
    }

    // A `Modified` row whose bytes already match the server's is not a real
    // conflict — even on a read-only mount, nothing needs discarding.
    #[test]
    fn modified_with_matching_content_is_already_in_sync_even_readonly() {
        let mut l = local(LocalChange::Modified);
        l.content_matches = true;
        l.is_readonly_mount = true;
        assert_eq!(decide_server_change(&entry(false, false), &l), ServerChangeAction::AlreadyInSync);
    }

    #[test]
    fn local_deleted_but_server_updated_always_conflicts() {
        let mut l = local(LocalChange::Deleted);
        l.content_matches = true; // must not matter — no local file to compare
        assert_eq!(decide_server_change(&entry(false, false), &l), ServerChangeAction::Conflict);
    }

    #[test]
    fn no_local_change_with_matching_content_is_already_in_sync() {
        let mut l = local(LocalChange::None);
        l.content_matches = true;
        assert_eq!(decide_server_change(&entry(false, false), &l), ServerChangeAction::AlreadyInSync);
    }

    #[test]
    fn no_local_change_with_differing_content_downloads() {
        let mut l = local(LocalChange::None);
        l.content_matches = false;
        assert_eq!(decide_server_change(&entry(false, false), &l), ServerChangeAction::Download);
    }
}
