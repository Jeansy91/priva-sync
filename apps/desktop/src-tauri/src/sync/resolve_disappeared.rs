//! Resolving `disappeared` rows against freshly staged files (ADR-0063).
//!
//! Runs immediately before each upload batch is snapshotted (`transfer.rs`):
//! a `disappeared` row (a path the watcher lost) is joined against a freshly
//! staged file sharing its OS file identity. A match is a rename and
//! attempts a server-side move, same as a live-observed one
//! (`events::handle_file_event`) -- preserving the file id and skipping a
//! byte transfer. A row that survives one full resolve pass with no partner
//! gets one more chance (a `From`/`To` pair can straddle a cycle boundary)
//! before becoming a real `pending_delete`.
//!
//! Free functions over `&LocalDb` + `&AuthedApi`, not `SyncEngine`
//! methods -- like `stage_upload`/`stage_dir_created` (`events.rs`), so the
//! server-move path is unit-testable against a mocked HTTP server without a
//! `SyncEngine`, which needs a real Tauri `AppHandle` this crate's tests have
//! no way to construct.

use std::collections::HashSet;
use tracing::{info, warn};

use crate::api::{AuthedApi, MoveOutcome};
use crate::db::{LocalDb, SyncRootEntry};
use super::{SyncEngine, match_disappeared_to_staged, MoveMatch};

impl SyncEngine {
    pub(super) async fn resolve_disappeared(&self, root: &SyncRootEntry) -> anyhow::Result<()> {
        resolve_disappeared_for_root(&self.db, &self.api, root).await
    }
}

pub(super) async fn resolve_disappeared_for_root(
    db:   &LocalDb,
    api:  &AuthedApi,
    root: &SyncRootEntry,
) -> anyhow::Result<()> {
    let disappeared = db.list_disappeared(&root.id)?;
    if disappeared.is_empty() {
        return Ok(());
    }
    let staged  = db.list_staged_files(&root.id)?;
    let matches = match_disappeared_to_staged(&disappeared, &staged);
    let matched: HashSet<&str> = matches.iter().map(|m| m.from_rel.as_str()).collect();

    for m in &matches {
        apply_disappeared_move(db, api, root, m).await;
    }

    for d in &disappeared {
        if matched.contains(d.path.as_str()) {
            continue;
        }
        if d.disappeared_seen {
            // Survived a full resolve pass with no partner -- a genuine
            // deletion, not a rename whose `To` just hasn't landed yet.
            db.upsert_file(&root.id, &d.path, 0, 0, &[], "pending_delete", None)?;
        } else {
            db.mark_disappeared_missed(&root.id, &d.path)?;
        }
    }
    Ok(())
}

async fn apply_disappeared_move(db: &LocalDb, api: &AuthedApi, root: &SyncRootEntry, m: &MoveMatch) {
    let Ok(Some(from_row)) = db.get_file(&root.id, &m.from_rel) else { return };

    if from_row.server_version < 0 {
        // Never reached the server -- nothing to move there. The row
        // already staged at the new path carries the right content and
        // uploads normally; just drop the stale source row.
        let _ = db.delete_file_record(&root.id, &m.from_rel);
        return;
    }

    let target_known_to_server = matches!(
        db.get_file(&root.id, &m.to_rel),
        Ok(Some(target)) if target.server_version >= 0
    );
    if target_known_to_server {
        // This is an overwrite (e.g. an atomic-save editor renaming its
        // scratch file onto the original), not a rename. A server move onto
        // an occupied path would spawn a conflict copy. Retire the source;
        // the staged target uploads as a regular overwrite.
        let _ = db.upsert_file(&root.id, &m.from_rel, 0, 0, &[], "pending_delete", None);
        return;
    }

    let moved = api
        .call(|snap| async move {
            snap.move_file(&root.server_root_id, &m.from_rel, &m.to_rel, from_row.server_version, root.is_mount()).await
        })
        .await;
    match moved {
        Ok(MoveOutcome::Moved) => {
            let _ = db.rename_file(&root.id, &m.from_rel, &m.to_rel);
            let _ = db.set_synced(&root.id, &m.to_rel, &from_row.content_hash);
            info!("server move (resolved from disappeared): {} → {}", m.from_rel, m.to_rel);
        }
        Ok(MoveOutcome::Conflict { server_version, current_path, conflict_path }) => {
            warn!(
                "resolve_disappeared move conflict: '{}' -> '{}' \
                 (server current='{current_path}' v={server_version}, conflict_copy={conflict_path:?})",
                m.from_rel, m.to_rel
            );
        }
        Err(e) => {
            warn!("resolve_disappeared: server move failed for '{}' -> '{}': {e}", m.from_rel, m.to_rel);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn test_db() -> LocalDb {
        let tmp = tempfile::tempdir().unwrap();
        LocalDb::open(tmp.path().join("db.sqlite")).unwrap()
    }

    fn api_for(url: String) -> (AuthedApi, tempfile::TempDir) {
        crate::api::authed_for(url, "test-token")
    }

    fn test_root() -> SyncRootEntry {
        SyncRootEntry {
            id:             "root-1".to_string(),
            name:           "test".to_string(),
            local_path:     "/irrelevant".to_string(),
            server_root_id: "srv-1".to_string(),
            download_mode:  "eager".to_string(),
            read_only:      false,
            paused:         false,
            kind:           crate::db::ROOT_KIND_OWNED.to_string(),
        }
    }

    /// The core case ADR-0063 exists for: a row known to the server that
    /// disappeared, and a freshly staged file elsewhere sharing its identity
    /// -- resolves as one server-side move, no bytes re-sent, both rows
    /// collapse into one `synced` row at the new path.
    #[tokio::test]
    async fn a_synced_source_matched_to_a_staged_file_moves_server_side() {
        let db = test_db();
        db.upsert_file("root-1", "/old.txt", 10, 100, b"content-hash", "synced", Some(b"content-hash")).unwrap();
        db.set_file_identity("root-1", "/old.txt", b"id-1").unwrap();
        // A prior successful upload records a server_version -- required for
        // should_attempt_server_move's sibling check on this path too.
        db.set_synced_with_version("root-1", "/old.txt", b"content-hash", 5).unwrap();
        db.mark_disappeared("root-1", "/old.txt").unwrap();

        db.upsert_file("root-1", "/new.txt", 10, 100, b"content-hash", "pending", None).unwrap();
        db.set_file_identity("root-1", "/new.txt", b"id-1").unwrap();

        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("PATCH", "/api/v1/sync-roots/srv-1/files")
            .match_header("if-match", "5")
            .match_body(mockito::Matcher::Json(serde_json::json!({"op": "move", "from": "/old.txt", "to": "/new.txt"})))
            .with_status(200)
            .create_async()
            .await;

        let (api, _api_db) = api_for(server.url());
        let root = test_root();
        resolve_disappeared_for_root(&db, &api, &root).await.unwrap();

        mock.assert_async().await;
        assert!(db.get_file("root-1", "/old.txt").unwrap().is_none(), "the old row must be gone, collapsed into the new path");
        let moved = db.get_file("root-1", "/new.txt").unwrap().expect("row must exist at the new path");
        assert_eq!(moved.sync_status, "synced");
        assert_eq!(moved.server_hash, Some(b"content-hash".to_vec()));
    }

    /// A row that never reached the server has nothing to move there -- the
    /// staged file at the new path already carries the right content and
    /// uploads normally. No HTTP call must be made for this case (no mock is
    /// registered; a stray call would fail against mockito's default 501).
    #[tokio::test]
    async fn a_never_synced_source_is_dropped_locally_without_a_server_call() {
        let db = test_db();
        db.upsert_file("root-1", "/old.txt", 10, 100, b"content-hash", "synced", None).unwrap();
        db.set_file_identity("root-1", "/old.txt", b"id-1").unwrap();
        db.mark_disappeared("root-1", "/old.txt").unwrap();
        // server_version stays at its default -1 -- this row was staged and
        // touched by the walk (identity got filled) but never uploaded.

        db.upsert_file("root-1", "/new.txt", 10, 100, b"content-hash", "pending", None).unwrap();
        db.set_file_identity("root-1", "/new.txt", b"id-1").unwrap();

        let server = mockito::Server::new_async().await;
        let (api, _api_db) = api_for(server.url());
        let root   = test_root();
        resolve_disappeared_for_root(&db, &api, &root).await.unwrap();

        assert!(db.get_file("root-1", "/old.txt").unwrap().is_none());
        let staged = db.get_file("root-1", "/new.txt").unwrap().expect("staged row untouched");
        assert_eq!(staged.sync_status, "pending", "left for the normal upload path to pick up");
    }

    /// An atomic-save editor renames its scratch file onto the original. The
    /// target is already known to the server, so this is an overwrite, not a
    /// rename: a server move onto an occupied path must not be attempted. The
    /// scratch row becomes a real delete, the staged target uploads normally.
    #[tokio::test]
    async fn a_rename_onto_an_occupied_target_is_not_sent_as_a_server_move() {
        let db = test_db();
        // Original X, known to the server at v5.
        db.upsert_file("root-1", "/X.3mf", 10, 100, b"old-hash", "synced", Some(b"old-hash")).unwrap();
        db.set_file_identity("root-1", "/X.3mf", b"id-old").unwrap();
        db.set_synced_with_version("root-1", "/X.3mf", b"old-hash", 5).unwrap();

        // The editor's scratch file, already uploaded under its own name.
        db.upsert_file("root-1", "/X.3mf.new", 12, 200, b"new-hash", "synced", Some(b"new-hash")).unwrap();
        db.set_file_identity("root-1", "/X.3mf.new", b"id-new").unwrap();
        db.set_synced_with_version("root-1", "/X.3mf.new", b"new-hash", 0).unwrap();

        // The scratch file disappears, X is re-staged with its identity and content.
        db.mark_disappeared("root-1", "/X.3mf.new").unwrap();
        db.upsert_file("root-1", "/X.3mf", 12, 200, b"new-hash", "pending", None).unwrap();
        db.set_file_identity("root-1", "/X.3mf", b"id-new").unwrap();

        let mut server = mockito::Server::new_async().await;
        let mv = server
            .mock("PATCH", "/api/v1/sync-roots/srv-1/files")
            .with_status(200)
            .expect(0)
            .create_async()
            .await;

        let (api, _api_db) = api_for(server.url());
        resolve_disappeared_for_root(&db, &api, &test_root()).await.unwrap();

        mv.assert_async().await;
        let source = db.get_file("root-1", "/X.3mf.new").unwrap().expect("source row kept for the delete");
        assert_eq!(source.sync_status, "pending_delete");
        let target = db.get_file("root-1", "/X.3mf").unwrap().expect("target row untouched");
        assert_eq!(target.sync_status, "pending", "left for the normal upload path as an overwrite");
    }

    /// The "second look" rule: a disappeared row with no matching staged path
    /// gets one full resolve pass to be missed before it becomes a real
    /// delete, so a `From`/`To` pair straddling a cycle boundary still joins.
    #[tokio::test]
    async fn an_unmatched_row_becomes_pending_delete_only_after_a_second_miss() {
        let db = test_db();
        db.upsert_file("root-1", "/gone.txt", 10, 100, b"h", "synced", None).unwrap();
        db.set_file_identity("root-1", "/gone.txt", b"id-1").unwrap();
        db.mark_disappeared("root-1", "/gone.txt").unwrap();

        let server = mockito::Server::new_async().await;
        let (api, _api_db) = api_for(server.url());
        let root   = test_root();

        resolve_disappeared_for_root(&db, &api, &root).await.unwrap();
        let after_first = db.get_file("root-1", "/gone.txt").unwrap().unwrap();
        assert_eq!(after_first.sync_status, "disappeared", "one miss is not enough yet");
        assert!(after_first.disappeared_seen);

        resolve_disappeared_for_root(&db, &api, &root).await.unwrap();
        let after_second = db.get_file("root-1", "/gone.txt").unwrap().unwrap();
        assert_eq!(after_second.sync_status, "pending_delete", "survived a full pass unmatched -- a real deletion");
    }
}
