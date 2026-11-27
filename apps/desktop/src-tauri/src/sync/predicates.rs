//! Pure DB predicates used by the sync engine.
//!
//! These functions only read from `LocalDb` and return a boolean — they have
//! no side effects and no async dependencies, which makes them trivially unit-
//! testable. Race-guard logic against the watcher belongs here.

use crate::api::{PrefixMoveEvent, ServerFileEntry};
use crate::db::{LocalDb, LocalFile, SyncRootEntry};
use std::collections::{HashMap, HashSet};

/// A server-side rename recognised by file-id identity: the same server file
/// id now sits at a different path than the local copy. Returned by
/// [`detect_renames`] so the pull can move the local file in place instead of
/// download-new + delete-old — the latter re-transfers the bytes and, before
/// the server emitted tombstones, duplicated the entry on disk.
#[derive(Debug, Default, PartialEq)]
pub(crate) struct RenamePlan {
    /// `(old_path, new_path)`, shallowest-first so a parent-directory rename is
    /// applied before its children: the filesystem move of the parent already
    /// relocates the whole subtree, and the child entries then only fix up the
    /// DB rows (their on-disk source is already gone).
    pub renames: Vec<(String, String)>,
    /// Old paths vacated by a rename. Tombstone feed entries at these paths
    /// must be skipped — the file moved, it was not deleted.
    pub consumed_old: HashSet<String>,
    /// New paths that are rename targets. Live feed entries at these need no
    /// download — the local rename already put the bytes in place.
    pub rename_targets: HashSet<String>,
}

/// Match live feed entries to local rows by server file id. An id known
/// locally at path A but reported live at path B is a rename A→B. Rows whose
/// id is unknown locally (not yet backfilled, or genuinely new) fall through
/// to the normal download path. Pure over its inputs so it is unit-testable
/// without a SyncEngine.
pub(crate) fn detect_renames(
    local: &[(String, String, bool)], // (path, server_file_id, is_dir)
    feed: &[ServerFileEntry],
) -> RenamePlan {
    let by_id: HashMap<&str, &str> = local
        .iter()
        .map(|(path, id, _)| (id.as_str(), path.as_str()))
        .collect();

    let mut plan = RenamePlan::default();
    for e in feed {
        if e.is_deleted || e.id.is_empty() {
            continue;
        }
        if let Some(&old) = by_id.get(e.id.as_str()) {
            if old != e.path {
                plan.renames.push((old.to_string(), e.path.clone()));
                plan.consumed_old.insert(old.to_string());
                plan.rename_targets.insert(e.path.clone());
            }
        }
    }
    // Parent before child: a shallower old path (fewer separators) is moved
    // first so the subtree relocation is a single filesystem rename.
    plan.renames.sort_by(|a, b| {
        a.0.matches('/').count()
            .cmp(&b.0.matches('/').count())
            .then_with(|| a.0.cmp(&b.0))
    });
    plan
}

/// Fold ADR-0065 prefix-move events into an id-detected [`RenamePlan`], so
/// both kinds of server-side rename apply through the same `apply_renames`
/// loop. A move_events entry needs no `consumed_old`/`rename_targets`
/// bookkeeping: its descendants never bump `seq` any more (that's the whole
/// point of ADR-0065), so they never appear as separate feed entries that
/// would need skipping -- unlike an id-detected folder rename, whose
/// descendants historically did show up as tombstone + live pairs.
/// Re-sorts shallowest-first same as [`detect_renames`], so an id-detected
/// rename and a prefix-move event landing in the same page still apply in
/// parent-before-child order.
pub(crate) fn merge_prefix_moves(mut plan: RenamePlan, moves: &[PrefixMoveEvent]) -> RenamePlan {
    for m in moves {
        plan.renames.push((m.old_prefix.clone(), m.new_prefix.clone()));
    }
    plan.renames.sort_by(|a, b| {
        a.0.matches('/').count()
            .cmp(&b.0.matches('/').count())
            .then_with(|| a.0.cmp(&b.0))
    });
    plan
}

/// A `disappeared` row matched to a freshly staged file sharing its OS file
/// identity -- a rename (ADR-0063).
#[derive(Debug, PartialEq, Eq)]
pub(crate) struct MoveMatch {
    pub from_rel: String,
    pub to_rel:   String,
}

/// Pair `disappeared` rows with freshly staged files by file identity,
/// confirming each candidate by size and content hash: identity alone is a
/// hint, not proof (NTFS reissues a FileIndex after deletion), and a false
/// positive on top of a same-size coincidence would move the wrong row. Pure
/// over its inputs so it's unit-testable without a DB.
///
/// Files only for now -- directories join in once `DirCreated` carries an
/// identity too.
pub(crate) fn match_disappeared_to_staged(
    disappeared: &[LocalFile],
    staged:      &[LocalFile],
) -> Vec<MoveMatch> {
    disappeared.iter()
        .filter_map(|d| {
            let d_id = d.file_identity.as_deref()?;
            let s = staged.iter().find(|s| {
                s.file_identity.as_deref() == Some(d_id)
                    && s.is_dir == d.is_dir
                    && s.size == d.size
                    && s.content_hash == d.content_hash
            })?;
            Some(MoveMatch { from_rel: d.path.clone(), to_rel: s.path.clone() })
        })
        .collect()
}

/// True iff local filesystem mutations on this root may be staged for the
/// server. Read-only roots (e.g. a `can_write=false` Shared Mount, ADR-0011)
/// never produce pending uploads/deletes — staging them would only feed a
/// doomed 403-retry loop, so the watcher drops the event and the next
/// reconcile restores the server version. This per-root guard is the actual
/// read-only guarantee; the OS read-only attribute is only a UX hint.
pub(crate) fn root_accepts_local_mutations(root: &SyncRootEntry) -> bool {
    !root.read_only
}

/// True iff a local root still exists server-side, checked against the right
/// namespace: owned roots against the sync-roots set, mounts against the
/// shared-mounts set (ADR-0011). A mount must NEVER be matched against the
/// own-roots set — its `server_root_id` is a share_id and would always miss,
/// which would prune every mount on each cycle. Drives the prune decision in
/// the engine loop (revocation / expiry / root-deletion all look identical to
/// the recipient: the share_id simply leaves the shared-mounts list).
pub(crate) fn root_still_on_server(
    is_mount: bool,
    server_root_id: &str,
    known_roots: &std::collections::HashSet<String>,
    known_mounts: &std::collections::HashSet<String>,
) -> bool {
    if is_mount {
        known_mounts.contains(server_root_id)
    } else {
        known_roots.contains(server_root_id)
    }
}

/// Pull-authoritative read_only refresh (ADR-0014): returns `(root_id,
/// new_read_only)` for every activated mount whose persisted `read_only` no
/// longer matches the server's `can_write` (keyed by share_id == server_root_id).
/// Both directions are reported — a revoked write right flips to read-only, a
/// granted one flips back. Own roots are skipped; mounts absent from the pulled
/// map are left untouched (the prune path owns removal). This is the guarantee
/// that survives a missed `share_updated` SSE — the next cycle reconciles it.
pub(crate) fn mount_read_only_changes(
    local: &[SyncRootEntry],
    server_can_write: &std::collections::HashMap<String, bool>,
) -> Vec<(String, bool)> {
    local
        .iter()
        .filter_map(|root| {
            if !root.is_mount() {
                return None;
            }
            let can_write = server_can_write.get(&root.server_root_id)?;
            let want_read_only = !can_write;
            (want_read_only != root.read_only).then(|| (root.id.clone(), want_read_only))
        })
        .collect()
}

/// Pull-authoritative name refresh: returns `(local_id, new_name)` for
/// every OWNED root whose persisted display name no longer matches the server
/// (keyed by server_root_id == the server root id). Mounts are skipped — their
/// name tracks the giver's share, not an own-root rename. A root absent from
/// the pulled map is left untouched (the prune path owns removal). Mirrors
/// [`mount_read_only_changes`]: the next cycle reconciles a missed SSE.
pub(crate) fn sync_root_name_changes(
    local: &[SyncRootEntry],
    server_names: &std::collections::HashMap<String, String>,
) -> Vec<(String, String)> {
    local
        .iter()
        .filter_map(|root| {
            if root.is_mount() {
                return None;
            }
            let name = server_names.get(&root.server_root_id)?;
            (*name != root.name).then(|| (root.id.clone(), name.clone()))
        })
        .collect()
}

/// True iff the rename source has a `synced` row in the DB — only then is a
/// server-side `move_file` safe. For `pending`/`pending_delete`/missing rows
/// the source never reached the server (or is mid-flight), so the move would
/// fall back to delete+upload and duplicate the file (see the race in `events.rs`).
pub(crate) fn should_attempt_server_move(
    db: &LocalDb, root_id: &str, from_rel: &str,
) -> bool {
    matches!(
        db.get_file(root_id, from_rel).ok().flatten(),
        Some(f) if f.sync_status == "synced"
    )
}

/// True iff a row whose path just disappeared should be marked
/// `disappeared` rather than `pending_delete` outright (ADR-0063): only a
/// row with a known OS file identity has anything for the next cycle's join
/// to match against. An untracked path, or one never touched since the
/// identity column was added, degrades to today's behaviour.
pub(crate) fn should_mark_disappeared(
    db: &LocalDb, root_id: &str, rel: &str,
) -> bool {
    matches!(
        db.get_file(root_id, rel).ok().flatten(),
        Some(f) if f.file_identity.is_some()
    )
}

/// True iff a finished upload may write `synced` for `path`. Returns false if
/// the watcher staged a delete or rename-away (`pending_delete`) while the
/// upload was in flight — overwriting that intent would resurrect the file
/// (Phantom-Reappear).
pub(crate) fn should_persist_upload_result(
    db: &LocalDb, root_id: &str, path: &str,
) -> bool {
    !matches!(
        db.get_file(root_id, path).ok().flatten(),
        Some(f) if f.sync_status == "pending_delete"
    )
}

/// True iff the server ever held `file`: a `pending_delete` row for which this
/// is false (a rename source retired before its upload landed) removes
/// nothing the user could see, so the delete pass must not report it. Either
/// marker suffices — a re-staged edit drops `server_hash`, a pulled file never
/// had a `server_version`.
pub(crate) fn delete_reached_server(file: &LocalFile) -> bool {
    file.server_version >= 0 || file.server_hash.is_some()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;
    use std::path::PathBuf;

    fn feed_entry(id: &str, path: &str, is_deleted: bool, is_dir: bool) -> ServerFileEntry {
        ServerFileEntry {
            id:             id.to_string(),
            path:           path.to_string(),
            size:           0,
            mtime:          "2026-01-01T00:00:00Z".to_string(),
            content_hash:   None,
            is_deleted,
            updated_at:     "2026-01-01T00:00:00Z".to_string(),
            server_version: Some(1),
            is_dir,
            seq:            0,
        }
    }

    /// A folder rename (server keeps file ids, only paths change) must be
    /// recognised as a subtree of renames — parent first — with the old paths
    /// marked consumed (so tombstones are skipped) and the new paths marked as
    /// targets (so they are not re-downloaded).
    #[test]
    fn detect_renames_maps_folder_subtree_by_id() {
        let local = vec![
            ("/Test".to_string(),        "id-dir".to_string(),  true),
            ("/Test/a.txt".to_string(),  "id-a".to_string(),    false),
            ("/Test/sub".to_string(),    "id-sub".to_string(),  true),
            ("/Test/sub/b.txt".to_string(), "id-b".to_string(), false),
            ("/Other.txt".to_string(),   "id-other".to_string(), false),
        ];
        let feed = vec![
            // Tombstones at old paths (new ids — irrelevant, is_deleted skips them).
            feed_entry("tomb-1", "/Test",          true,  true),
            feed_entry("tomb-2", "/Test/a.txt",    true,  false),
            feed_entry("tomb-3", "/Test/sub",      true,  true),
            feed_entry("tomb-4", "/Test/sub/b.txt", true, false),
            // Moved rows keep their original ids at the new paths.
            feed_entry("id-dir", "/Test 2",          false, true),
            feed_entry("id-a",   "/Test 2/a.txt",    false, false),
            feed_entry("id-sub", "/Test 2/sub",      false, true),
            feed_entry("id-b",   "/Test 2/sub/b.txt", false, false),
        ];

        let plan = detect_renames(&local, &feed);

        // Parent (/Test) before its children, deepest last.
        assert_eq!(plan.renames, vec![
            ("/Test".to_string(),           "/Test 2".to_string()),
            ("/Test/a.txt".to_string(),     "/Test 2/a.txt".to_string()),
            ("/Test/sub".to_string(),       "/Test 2/sub".to_string()),
            ("/Test/sub/b.txt".to_string(), "/Test 2/sub/b.txt".to_string()),
        ]);
        for old in ["/Test", "/Test/a.txt", "/Test/sub", "/Test/sub/b.txt"] {
            assert!(plan.consumed_old.contains(old), "old path {old} must be consumed");
        }
        assert!(plan.rename_targets.contains("/Test 2/sub/b.txt"));
        // The unrelated file is not part of any rename.
        assert!(!plan.consumed_old.contains("/Other.txt"));
    }

    /// Pull-authoritative name refresh: an owned root whose server name
    /// changed is reported with its local id + the new name. Unchanged roots
    /// and roots absent from the pulled map are left alone, and mounts are
    /// never touched (their name tracks the share, not an own-root rename).
    #[test]
    fn sync_root_name_changes_detects_owned_renames_only() {
        let owned = |id: &str, server_id: &str| SyncRootEntry {
            kind: crate::db::ROOT_KIND_OWNED.to_string(),
            ..mount_root(id, server_id, false)
        };
        let local = vec![
            owned("o-renamed", "srv-1"), // owned, server renamed it (name "n" -> "New Name")
            owned("o-same",    "srv-2"), // owned, unchanged
            owned("o-gone",    "srv-3"), // owned, not in pull
            mount_root("m1",   "share-1", false), // mount — never touched
        ];
        let mut server_names = HashMap::new();
        server_names.insert("srv-1".to_string(), "New Name".to_string());
        server_names.insert("srv-2".to_string(), "n".to_string()); // == local name
        server_names.insert("share-1".to_string(), "Hijack".to_string()); // must not touch mount

        let changes = sync_root_name_changes(&local, &server_names);
        assert_eq!(changes, vec![("o-renamed".to_string(), "New Name".to_string())]);
    }

    /// A genuinely new file (id unknown locally) is not a rename — it must fall
    /// through to the download path, leaving the plan empty.
    #[test]
    fn detect_renames_ignores_unknown_and_unchanged_ids() {
        let local = vec![
            ("/keep.txt".to_string(), "id-keep".to_string(), false),
        ];
        let feed = vec![
            feed_entry("id-keep", "/keep.txt", false, false), // same path → not a rename
            feed_entry("id-new",  "/new.txt",  false, false), // unknown id → download
        ];
        let plan = detect_renames(&local, &feed);
        assert!(plan.renames.is_empty(), "no rename expected; got {:?}", plan.renames);
        assert!(plan.consumed_old.is_empty());
        assert!(plan.rename_targets.is_empty());
    }

    /// The core ADR-0065 client behaviour: a prefix-move event runs through
    /// the exact same `apply_renames` pipeline as an id-detected rename, so
    /// no new client-side apply logic is needed for a directory move.
    #[test]
    fn merge_prefix_moves_adds_the_event_as_a_rename_pair() {
        let plan = RenamePlan::default();
        let moves = vec![PrefixMoveEvent {
            old_prefix: "/old".to_string(),
            new_prefix: "/new".to_string(),
            seq: 9,
        }];

        let plan = merge_prefix_moves(plan, &moves);

        assert_eq!(plan.renames, vec![("/old".to_string(), "/new".to_string())]);
    }

    /// An id-detected rename and a prefix-move event in the same page must
    /// still apply parent-before-child -- merging must re-sort, not append.
    #[test]
    fn merge_prefix_moves_keeps_shallowest_first_order() {
        let plan = RenamePlan {
            renames: vec![("/a/deep/child".to_string(), "/z/deep/child".to_string())],
            ..RenamePlan::default()
        };
        let moves = vec![PrefixMoveEvent {
            old_prefix: "/a".to_string(),
            new_prefix: "/z".to_string(),
            seq: 9,
        }];

        let plan = merge_prefix_moves(plan, &moves);

        assert_eq!(plan.renames, vec![
            ("/a".to_string(), "/z".to_string()),
            ("/a/deep/child".to_string(), "/z/deep/child".to_string()),
        ]);
    }

    /// server_file_id round-trips through the DB and survives a rename: set it
    /// on a row, list it back, rename the row, and confirm the id still maps to
    /// the new path — the invariant id-based rename detection relies on.
    #[test]
    fn server_file_id_round_trips_and_survives_rename() {
        let tmp = TempDir::new();
        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();

        db.upsert_file("r1", "/old.txt", 3, 0, &[1, 2, 3], "synced", Some(&[1, 2, 3])).unwrap();
        // No id yet → not listed.
        assert!(db.list_server_file_ids("r1").unwrap().is_empty());

        db.set_server_file_id("r1", "/old.txt", "srv-id-1").unwrap();
        let listed = db.list_server_file_ids("r1").unwrap();
        assert_eq!(listed, vec![("/old.txt".to_string(), "srv-id-1".to_string(), false)]);

        db.rename_file("r1", "/old.txt", "/new.txt").unwrap();
        let after = db.list_server_file_ids("r1").unwrap();
        assert_eq!(after, vec![("/new.txt".to_string(), "srv-id-1".to_string(), false)],
            "id must follow the row across a rename");
    }

    fn mount_root(id: &str, share_id: &str, read_only: bool) -> SyncRootEntry {
        SyncRootEntry {
            id:             id.to_string(),
            name:           "n".to_string(),
            local_path:     "/tmp/x".to_string(),
            server_root_id: share_id.to_string(),
            download_mode:  "on_demand".to_string(),
            read_only,
            paused:         false,
            kind:           crate::db::ROOT_KIND_MOUNT.to_string(),
        }
    }

    /// Pull-authoritative read_only refresh (ADR-0014): when the server's
    /// can_write for an activated mount no longer matches the persisted
    /// read_only, the flip is reported so the loop can apply it — both
    /// directions. Own roots are never touched, and a mount absent from the
    /// pulled set is left to the prune path, not flipped here.
    #[test]
    fn mount_read_only_changes_detects_can_write_flips() {
        let local = vec![
            mount_root("m-rw", "share-rw", false), // writable now revoked
            mount_root("m-ro", "share-ro", true),  // read-only now granted
            mount_root("m-same", "share-same", true), // unchanged
            mount_root("m-gone", "share-gone", false), // not in pull (revoked)
            SyncRootEntry { kind: crate::db::ROOT_KIND_OWNED.to_string(),
                ..mount_root("o1", "share-rw", false) }, // owned, id collides
        ];
        let mut can_write = HashMap::new();
        can_write.insert("share-rw".to_string(), false);   // → read_only=true
        can_write.insert("share-ro".to_string(), true);    // → read_only=false
        can_write.insert("share-same".to_string(), false); // already read_only

        let mut changes = mount_read_only_changes(&local, &can_write);
        changes.sort();

        assert_eq!(changes, vec![
            ("m-ro".to_string(), false),
            ("m-rw".to_string(), true),
        ]);
    }

    fn row(path: &str, identity: Option<&[u8]>, size: i64, hash: &[u8], status: &str) -> LocalFile {
        LocalFile {
            sync_root_id:   "r1".to_string(),
            path:           path.to_string(),
            size,
            mtime:          0,
            content_hash:   hash.to_vec(),
            sync_status:    status.to_string(),
            server_hash:    None,
            server_version: -1,
            attempt_count:  0,
            last_error:     None,
            next_retry_at:  None,
            is_dir:         false,
            file_identity:  identity.map(|b| b.to_vec()),
            disappeared_seen: false,
        }
    }

    #[test]
    fn match_disappeared_to_staged_confirms_by_identity_size_and_hash() {
        let disappeared = vec![row("/old.txt", Some(&[1, 2, 3]), 10, b"hash-a", "disappeared")];
        let staged      = vec![row("/new.txt", Some(&[1, 2, 3]), 10, b"hash-a", "pending")];

        let got = match_disappeared_to_staged(&disappeared, &staged);
        assert_eq!(got, vec![MoveMatch { from_rel: "/old.txt".to_string(), to_rel: "/new.txt".to_string() }]);
    }

    /// NTFS reissues a FileIndex after deletion; a same-size coincidence on
    /// top of a reused index must not be read as a move (explicit AC on
    //) -- the content hash is what tells them apart.
    #[test]
    fn match_disappeared_to_staged_rejects_a_reused_identity_with_different_content() {
        let disappeared = vec![row("/old.txt", Some(&[1, 2, 3]), 10, b"hash-a", "disappeared")];
        let staged      = vec![row("/new.txt", Some(&[1, 2, 3]), 10, b"hash-b", "pending")];

        assert!(match_disappeared_to_staged(&disappeared, &staged).is_empty());
    }

    /// A renamed folder joins by identity alone (its content_hash is always
    /// the fixed empty blob, so a size/hash-only guard would be no guard at
    /// all for directories) -- but must never cross-match a same-size empty
    /// file, so the type has to agree too.
    #[test]
    fn match_disappeared_to_staged_matches_directories_by_identity_and_type() {
        let mut old_dir = row("/old-dir", Some(&[7, 7, 7]), 0, &[], "disappeared");
        old_dir.is_dir = true;
        let mut new_dir = row("/new-dir", Some(&[7, 7, 7]), 0, &[], "pending");
        new_dir.is_dir = true;

        let got = match_disappeared_to_staged(&[old_dir], &[new_dir]);
        assert_eq!(got, vec![MoveMatch { from_rel: "/old-dir".to_string(), to_rel: "/new-dir".to_string() }]);
    }

    #[test]
    fn match_disappeared_to_staged_does_not_cross_match_a_directory_and_a_file() {
        let mut old_dir = row("/old-dir", Some(&[7, 7, 7]), 0, &[], "disappeared");
        old_dir.is_dir = true;
        let empty_file = row("/new.txt", Some(&[7, 7, 7]), 0, &[], "pending");

        assert!(match_disappeared_to_staged(&[old_dir], &[empty_file]).is_empty());
    }

    #[test]
    fn match_disappeared_to_staged_ignores_a_disappeared_row_without_identity() {
        let disappeared = vec![row("/old.txt", None, 10, b"hash-a", "disappeared")];
        let staged      = vec![row("/new.txt", None, 10, b"hash-a", "pending")];

        assert!(match_disappeared_to_staged(&disappeared, &staged).is_empty(),
            "no identity on either side means nothing to join on -- stays a delete + create");
    }

    struct TempDir(PathBuf);
    impl TempDir {
        fn new() -> Self {
            let p = std::env::temp_dir()
                .join(format!("priva-sync-test-{}", uuid::Uuid::new_v4()));
            std::fs::create_dir_all(&p).unwrap();
            Self(p)
        }
        fn path(&self) -> &std::path::Path { &self.0 }
    }
    impl Drop for TempDir {
        fn drop(&mut self) { let _ = std::fs::remove_dir_all(&self.0); }
    }

    /// Rename guard: a server-side move is only safe when the source is known
    /// as `synced`. For `pending` sources the upload is still in flight and
    /// `move_file` would 404 → the delete+upload fallback would duplicate the
    /// file (phantom re-appear after copy+rename).
    #[test]
    fn should_attempt_server_move_only_for_synced_source() {
        let tmp = TempDir::new();
        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();

        assert!(!should_attempt_server_move(&db, "root-x", "/missing.txt"));

        db.upsert_file("root-x", "/copy.txt", 10, 0, &[1,2,3], "pending", None).unwrap();
        assert!(!should_attempt_server_move(&db, "root-x", "/copy.txt"));

        db.upsert_file("root-x", "/gone.txt", 0, 0, &[], "pending_delete", None).unwrap();
        assert!(!should_attempt_server_move(&db, "root-x", "/gone.txt"));

        db.upsert_file("root-x", "/conf.txt", 10, 0, &[1,2,3], "conflict", None).unwrap();
        assert!(!should_attempt_server_move(&db, "root-x", "/conf.txt"));

        db.upsert_file("root-x", "/ok.txt", 10, 0, &[1,2,3], "synced", Some(&[1,2,3])).unwrap();
        assert!(should_attempt_server_move(&db, "root-x", "/ok.txt"));
    }

    /// Only a row with a known identity gets the `disappeared` treatment
    /// (ADR-0063) — nothing to join it against otherwise, so it stays
    /// today's `pending_delete`.
    #[test]
    fn should_mark_disappeared_only_when_identity_is_known() {
        let tmp = TempDir::new();
        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();

        assert!(!should_mark_disappeared(&db, "root-x", "/missing.txt"));

        db.upsert_file("root-x", "/no-identity.txt", 10, 0, &[1, 2, 3], "synced", None).unwrap();
        assert!(!should_mark_disappeared(&db, "root-x", "/no-identity.txt"));

        db.upsert_file("root-x", "/known.txt", 10, 0, &[1, 2, 3], "synced", None).unwrap();
        db.set_file_identity("root-x", "/known.txt", b"vol1-id42").unwrap();
        assert!(should_mark_disappeared(&db, "root-x", "/known.txt"));
    }

    fn ro_root(read_only: bool) -> SyncRootEntry {
        SyncRootEntry {
            id:             "r".to_string(),
            name:           "n".to_string(),
            local_path:     "/tmp/x".to_string(),
            server_root_id: "s".to_string(),
            download_mode:  "on_demand".to_string(),
            read_only,
            paused:         false,
            kind:           crate::db::ROOT_KIND_MOUNT.to_string(),
        }
    }

    /// Prune routing: an own root is matched against the sync-roots set, a
    /// mount against the shared-mounts set. A mount whose share_id is absent
    /// from the mounts set is orphaned even if some own-root happens to share
    /// the id space — the kinds never cross-match. ADR-0011.
    #[test]
    fn root_still_on_server_matches_by_kind() {
        let roots: std::collections::HashSet<String> =
            ["root-1".to_string()].into_iter().collect();
        let mounts: std::collections::HashSet<String> =
            ["share-1".to_string()].into_iter().collect();

        // Own root present / absent.
        assert!(root_still_on_server(false, "root-1", &roots, &mounts));
        assert!(!root_still_on_server(false, "root-x", &roots, &mounts));
        // Mount present / absent.
        assert!(root_still_on_server(true, "share-1", &roots, &mounts));
        assert!(!root_still_on_server(true, "share-x", &roots, &mounts));
        // Cross-kind must NOT match: a mount is never validated by the roots set.
        assert!(!root_still_on_server(true, "root-1", &roots, &mounts));
        assert!(!root_still_on_server(false, "share-1", &roots, &mounts));
    }

    /// A read-only root (can_write=false mount) must never accept local
    /// mutations — the staging guard returns false so no pending row is
    /// created. A writable root accepts them. See ADR-0011.
    #[test]
    fn root_accepts_local_mutations_respects_read_only() {
        assert!(!root_accepts_local_mutations(&ro_root(true)));
        assert!(root_accepts_local_mutations(&ro_root(false)));
    }

    /// Upload pre-commit recheck: if the watcher staged `pending_delete` for
    /// the same path while an upload was in flight (e.g. because the user
    /// renamed the file), the finishing upload must NOT overwrite the DB
    /// status with `synced` — otherwise the source lives on server-side and
    /// the phantom re-appear path triggers.
    #[test]
    fn should_persist_upload_result_skips_pending_delete() {
        let tmp = TempDir::new();
        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();

        assert!(should_persist_upload_result(&db, "root-x", "/new.txt"));

        db.upsert_file("root-x", "/pend.txt", 10, 0, &[1,2,3], "pending", None).unwrap();
        assert!(should_persist_upload_result(&db, "root-x", "/pend.txt"));

        db.upsert_file("root-x", "/dead.txt", 0, 0, &[], "pending_delete", None).unwrap();
        assert!(!should_persist_upload_result(&db, "root-x", "/dead.txt"));

        db.upsert_file("root-x", "/ok.txt", 10, 0, &[1,2,3], "synced", Some(&[1,2,3])).unwrap();
        assert!(should_persist_upload_result(&db, "root-x", "/ok.txt"));
    }

    /// Copy + immediate rename. The rename fallback keeps the still-
    /// `pending` source row as `pending_delete` instead of dropping it — a
    /// missing row reads as "nothing pending", so an in-flight upload would
    /// land under the old name with no record left to clean it up.
    #[test]
    fn should_persist_upload_result_skips_a_retired_rename_source() {
        let tmp = TempDir::new();
        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        db.upsert_file("root-x", "/copy.txt", 1, 1, b"h", "pending", None).unwrap();

        db.mark_pending_delete("root-x", "/copy.txt").unwrap();

        let rec = db.get_file("root-x", "/copy.txt").unwrap()
            .expect("row must not be dropped while an upload may be in flight");
        assert_eq!(rec.sync_status, "pending_delete");
        assert!(!should_persist_upload_result(&db, "root-x", "/copy.txt"));
    }

    /// A rename source kept as `pending_delete` that never reached the
    /// server must not toast as a deletion; anything the server ever held must.
    #[test]
    fn delete_reached_server_only_for_rows_the_server_held() {
        let tmp = TempDir::new();
        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        let get = |p: &str| db.get_file("root-x", p).unwrap().unwrap();

        db.upsert_file("root-x", "/never.txt", 1, 0, &[1], "pending_delete", None).unwrap();
        assert!(!delete_reached_server(&get("/never.txt")), "never uploaded");

        db.upsert_file("root-x", "/pulled.txt", 1, 0, &[1], "pending_delete", Some(&[1])).unwrap();
        assert!(delete_reached_server(&get("/pulled.txt")), "server_hash set, version still -1");

        db.upsert_file("root-x", "/edited.txt", 1, 0, &[1], "pending", None).unwrap();
        db.set_synced_with_version("root-x", "/edited.txt", &[1], 4).unwrap();
        db.upsert_file("root-x", "/edited.txt", 1, 0, &[2], "pending_delete", None).unwrap();
        assert!(delete_reached_server(&get("/edited.txt")), "version survives a re-stage");
    }
}
