use anyhow::Result;
use rusqlite::params;
use super::{LocalDb, SyncRootEntry};

impl LocalDb {
    #[expect(clippy::too_many_arguments, reason = "one parameter per sync_roots column it writes")]
    pub fn upsert_sync_root(
        &self,
        id: &str,
        name: &str,
        local_path: &str,
        server_root_id: &str,
        download_mode: &str,
        read_only: bool,
        kind: &str,
    ) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        // `paused` is intentionally not touched here — INSERT picks up the
        // column default (0); ON CONFLICT preserves any user-set pause state.
        conn.execute(
            "INSERT INTO sync_roots (id, name, local_path, server_root_id, download_mode, read_only, kind)
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)
             ON CONFLICT(id) DO UPDATE SET
               name           = excluded.name,
               local_path     = excluded.local_path,
               server_root_id = excluded.server_root_id,
               download_mode  = excluded.download_mode,
               read_only      = excluded.read_only,
               kind           = excluded.kind",
            params![id, name, local_path, server_root_id, download_mode, read_only as i64, kind],
        )?;
        Ok(())
    }

    /// Pause or resume a sync root. Paused roots are skipped entirely by the
    /// sync cycle — no reconcile, no uploads, no downloads.
    pub fn set_sync_root_paused(&self, id: &str, paused: bool) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE sync_roots SET paused = ?1 WHERE id = ?2",
            params![paused as i64, id],
        )?;
        Ok(())
    }

    /// Drop all `pending_delete` rows for this root. Used by the bulk-delete
    /// "restore locally" flow: clears the staged deletes so the next download
    /// cycle (after `clear_last_sync`) re-creates the missing files from the
    /// server.
    pub fn clear_pending_deletes(&self, sync_root_id: &str) -> Result<usize> {
        let conn = self.conn.lock().unwrap();
        let n = conn.execute(
            "DELETE FROM local_files
             WHERE sync_root_id = ?1 AND sync_status = 'pending_delete'",
            params![sync_root_id],
        )?;
        Ok(n)
    }

    /// Reset the `last_sync` cursor so the next download cycle re-fetches the
    /// full server change history for this root.
    pub fn clear_last_sync(&self, sync_root_id: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "DELETE FROM config WHERE key = ?1",
            params![format!("last_sync_{sync_root_id}")],
        )?;
        Ok(())
    }

    /// Set a one-shot flag that allows the next sync cycle to skip the
    /// bulk-delete guard for `root_id`.
    pub fn set_bulk_delete_confirmed(&self, root_id: &str) -> Result<()> {
        self.config_set(&format!("bulk_delete_confirmed_{root_id}"), "1")
    }

    /// Read and immediately clear the bulk-delete confirmation flag.
    /// Returns `true` if the flag was set (user already confirmed).
    pub fn take_bulk_delete_confirmed(&self, root_id: &str) -> Result<bool> {
        let key = format!("bulk_delete_confirmed_{root_id}");
        let set = self.config_get(&key)?.as_deref() == Some("1");
        if set {
            let conn = self.conn.lock().unwrap();
            conn.execute("DELETE FROM config WHERE key = ?1", params![key])?;
        }
        Ok(set)
    }

    /// Set a one-shot flag that allows the next pull cycle to skip the
    /// *inbound* bulk-delete guard for `root_id`. A distinct key from
    /// [`Self::set_bulk_delete_confirmed`] on purpose (Fork 2b): the outbound
    /// and inbound guards can both legitimately be paused on the same root at
    /// once, and confirming one must never silently wave the other's
    /// unrelated wave through on its next check.
    pub fn set_bulk_delete_confirmed_incoming(&self, root_id: &str) -> Result<()> {
        self.config_set(&format!("bulk_delete_confirmed_incoming_{root_id}"), "1")
    }

    /// Read and immediately clear the *inbound* bulk-delete confirmation flag.
    pub fn take_bulk_delete_confirmed_incoming(&self, root_id: &str) -> Result<bool> {
        let key = format!("bulk_delete_confirmed_incoming_{root_id}");
        let set = self.config_get(&key)?.as_deref() == Some("1");
        if set {
            let conn = self.conn.lock().unwrap();
            conn.execute("DELETE FROM config WHERE key = ?1", params![key])?;
        }
        Ok(set)
    }

    /// Flip a mount's read-only flag in place. Used by the pull-authoritative
    /// permission refresh (ADR-0014) when the server's can_write changed without
    /// a revoke — the engine RO-guard then picks up the new value next cycle.
    pub fn set_sync_root_read_only(&self, id: &str, read_only: bool) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE sync_roots SET read_only = ?1 WHERE id = ?2",
            params![read_only as i64, id],
        )?;
        Ok(())
    }

    /// Update a root's display name in place. Used by the pull-authoritative
    /// name refresh when the user renamed the root in the web UI. The
    /// local folder path is untouched — only the label changes.
    pub fn set_sync_root_name(&self, id: &str, name: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE sync_roots SET name = ?1 WHERE id = ?2",
            params![name, id],
        )?;
        Ok(())
    }

    pub fn set_download_mode(&self, id: &str, mode: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE sync_roots SET download_mode = ?1 WHERE id = ?2",
            params![mode, id],
        )?;
        Ok(())
    }

    pub fn list_sync_roots(&self) -> Result<Vec<SyncRootEntry>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT id, name, local_path, server_root_id, download_mode, read_only, paused, kind
             FROM sync_roots"
        )?;
        // Map every row leniently — historically a single failing column
        // (e.g. NULL `download_mode`) would silently drop the entire row via
        // `?` + `filter_map(_, .ok())`, leaving the UI with fewer roots than
        // the DB actually holds. Mirror `get_sync_root`'s defaults instead.
        let rows = stmt.query_map([], |row| Ok(SyncRootEntry {
            id:             row.get::<_, String>(0).unwrap_or_default(),
            name:           row.get::<_, String>(1).unwrap_or_default(),
            local_path:     row.get::<_, String>(2).unwrap_or_default(),
            server_root_id: row.get::<_, String>(3).unwrap_or_default(),
            download_mode:  row.get::<_, String>(4).unwrap_or_else(|_| "on_demand".to_string()),
            read_only:      row.get::<_, i64>(5).unwrap_or(0) != 0,
            paused:         row.get::<_, i64>(6).unwrap_or(0) != 0,
            kind:           row.get::<_, String>(7).unwrap_or_else(|_| super::ROOT_KIND_OWNED.to_string()),
        }))?;
        let collected: Vec<SyncRootEntry> = rows.filter_map(|r| match r {
            Ok(entry) => Some(entry),
            Err(e)    => {
                tracing::warn!("list_sync_roots: dropping row that failed to map: {e}");
                None
            }
        }).collect();
        tracing::debug!("list_sync_roots: returning {} root(s)", collected.len());
        Ok(collected)
    }

    pub fn get_sync_root(&self, id: &str) -> Result<Option<SyncRootEntry>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT id, name, local_path, server_root_id, download_mode, read_only, paused, kind
             FROM sync_roots WHERE id = ?1"
        )?;
        let mut rows = stmt.query(params![id])?;
        match rows.next()? {
            Some(row) => Ok(Some(SyncRootEntry::from_row(row)?)),
            None      => Ok(None),
        }
    }

    pub fn remove_sync_root(&self, id: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        // Remove all file tracking records first so no pending_delete operations
        // can leak into a future sync cycle and accidentally delete server files.
        conn.execute("DELETE FROM local_files WHERE sync_root_id = ?1", params![id])?;
        // Drop any queued server-change retries — otherwise the retry sweep
        // would keep trying to apply downloads/deletes for a root that no
        // longer exists.
        conn.execute("DELETE FROM failed_server_changes WHERE sync_root_id = ?1", params![id])?;
        // Every per-root config key goes with the root. Two of them used to
        // survive a removal, and because the local root id *is* the server
        // root id, re-adding the same folder inherited them:
        //
        // - `changes_cursor_*` is what `adopt_on_first_sync` checks to decide
        //   whether this root has ever synced. A leftover cursor made it skip
        //   adoption on the re-add, so every already-uploaded file — whose DB
        //   row was just deleted — was staged as a blind upload, and the
        //   server materialised a conflict copy for each one.
        // - `reconcile_checkpoint_*` would make the first walk of the re-added
        //   root fast-forward past subtrees it has never actually seen.
        // - `adopt_cursor_*` likewise: the re-added root's adopt pass would
        //   start mid-feed and blind-upload everything before that point.
        conn.execute(
            "DELETE FROM config WHERE key IN (?1, ?2, ?3, ?4, ?5, ?6)",
            params![
                format!("last_sync_{id}"),
                format!("bulk_delete_confirmed_{id}"),
                format!("bulk_delete_confirmed_incoming_{id}"),
                format!("changes_cursor_{id}"),
                format!("reconcile_checkpoint_{id}"),
                format!("adopt_cursor_{id}"),
            ],
        )?;
        conn.execute("DELETE FROM sync_roots WHERE id = ?1", params![id])?;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn test_db() -> LocalDb {
        let tmp = tempfile::tempdir().unwrap();
        LocalDb::open(tmp.path().join("db.sqlite")).unwrap()
    }

    #[test]
    fn upsert_then_get_round_trips_all_fields() {
        let db = test_db();
        db.upsert_sync_root("r1", "Root 1", "/local/r1", "srv-1", "eager", true, super::super::ROOT_KIND_MOUNT)
            .unwrap();

        let got = db.get_sync_root("r1").unwrap().expect("row must exist");
        assert_eq!(got.id, "r1");
        assert_eq!(got.name, "Root 1");
        assert_eq!(got.local_path, "/local/r1");
        assert_eq!(got.server_root_id, "srv-1");
        assert_eq!(got.download_mode, "eager");
        assert!(got.read_only);
        assert!(!got.paused, "paused must start false regardless of the insert args");
        assert!(got.is_mount());
    }

    #[test]
    fn upsert_on_existing_id_updates_in_place_without_touching_paused() {
        let db = test_db();
        db.upsert_sync_root("r1", "Old Name", "/old", "srv-1", "on_demand", false, super::super::ROOT_KIND_OWNED)
            .unwrap();
        db.set_sync_root_paused("r1", true).unwrap();

        // Re-upsert with the same id must update the other columns but must
        // not reset the pause state the user explicitly set.
        db.upsert_sync_root("r1", "New Name", "/new", "srv-2", "eager", true, super::super::ROOT_KIND_MOUNT)
            .unwrap();

        let got = db.get_sync_root("r1").unwrap().unwrap();
        assert_eq!(got.name, "New Name");
        assert_eq!(got.local_path, "/new");
        assert_eq!(got.server_root_id, "srv-2");
        assert_eq!(got.download_mode, "eager");
        assert!(got.read_only);
        assert!(got.paused, "ON CONFLICT must preserve the previously-set pause state");

        assert_eq!(db.list_sync_roots().unwrap().len(), 1, "upsert on existing id must not duplicate the row");
    }

    #[test]
    fn get_sync_root_returns_none_for_missing_id() {
        let db = test_db();
        assert!(db.get_sync_root("nope").unwrap().is_none());
    }

    #[test]
    fn set_sync_root_paused_toggles_both_ways() {
        let db = test_db();
        db.upsert_sync_root("r1", "R", "/r", "srv", "eager", false, super::super::ROOT_KIND_OWNED).unwrap();

        db.set_sync_root_paused("r1", true).unwrap();
        assert!(db.get_sync_root("r1").unwrap().unwrap().paused);

        db.set_sync_root_paused("r1", false).unwrap();
        assert!(!db.get_sync_root("r1").unwrap().unwrap().paused);
    }

    #[test]
    fn set_sync_root_read_only_flips_flag() {
        let db = test_db();
        db.upsert_sync_root("r1", "R", "/r", "srv", "eager", false, super::super::ROOT_KIND_OWNED).unwrap();

        db.set_sync_root_read_only("r1", true).unwrap();
        assert!(db.get_sync_root("r1").unwrap().unwrap().read_only);

        db.set_sync_root_read_only("r1", false).unwrap();
        assert!(!db.get_sync_root("r1").unwrap().unwrap().read_only);
    }

    #[test]
    fn set_sync_root_name_updates_label_only() {
        let db = test_db();
        db.upsert_sync_root("r1", "Old", "/local", "srv", "eager", false, super::super::ROOT_KIND_OWNED).unwrap();

        db.set_sync_root_name("r1", "Renamed").unwrap();

        let got = db.get_sync_root("r1").unwrap().unwrap();
        assert_eq!(got.name, "Renamed");
        assert_eq!(got.local_path, "/local", "local_path must be untouched by a name update");
    }

    #[test]
    fn set_download_mode_updates_mode() {
        let db = test_db();
        db.upsert_sync_root("r1", "R", "/r", "srv", "on_demand", false, super::super::ROOT_KIND_OWNED).unwrap();

        db.set_download_mode("r1", "eager").unwrap();

        assert_eq!(db.get_sync_root("r1").unwrap().unwrap().download_mode, "eager");
    }

    #[test]
    fn list_sync_roots_returns_every_row() {
        let db = test_db();
        db.upsert_sync_root("r1", "R1", "/r1", "srv1", "eager", false, super::super::ROOT_KIND_OWNED).unwrap();
        db.upsert_sync_root("r2", "R2", "/r2", "srv2", "on_demand", true, super::super::ROOT_KIND_MOUNT).unwrap();

        let mut ids: Vec<String> = db.list_sync_roots().unwrap().into_iter().map(|r| r.id).collect();
        ids.sort();
        assert_eq!(ids, vec!["r1".to_string(), "r2".to_string()]);
    }

    #[test]
    fn clear_pending_deletes_only_removes_pending_delete_rows() {
        let db = test_db();
        db.upsert_sync_root("r1", "R", "/r", "srv", "eager", false, super::super::ROOT_KIND_OWNED).unwrap();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending_delete", None).unwrap();
        db.upsert_file("r1", "/b.txt", 1, 1, b"h", "pending", None).unwrap();

        let removed = db.clear_pending_deletes("r1").unwrap();

        assert_eq!(removed, 1);
        let remaining = db.list_all_files("r1").unwrap();
        assert_eq!(remaining.len(), 1);
        assert_eq!(remaining[0].path, "/b.txt");
    }

    #[test]
    fn clear_last_sync_removes_only_the_matching_config_key() {
        let db = test_db();
        db.config_set("last_sync_r1", "12345").unwrap();
        db.config_set("last_sync_r2", "67890").unwrap();

        db.clear_last_sync("r1").unwrap();

        assert_eq!(db.config_get("last_sync_r1").unwrap(), None);
        assert_eq!(db.config_get("last_sync_r2").unwrap(), Some("67890".to_string()));
    }

    #[test]
    fn bulk_delete_confirmed_is_a_one_shot_flag() {
        let db = test_db();
        assert!(!db.take_bulk_delete_confirmed("r1").unwrap(), "must default to false when never set");

        db.set_bulk_delete_confirmed("r1").unwrap();
        assert!(db.take_bulk_delete_confirmed("r1").unwrap(), "must be true right after set");
        assert!(!db.take_bulk_delete_confirmed("r1").unwrap(), "reading it once must clear the flag");
    }

    /// Fork 2b: the inbound and outbound guards must not share one
    /// flag. Both can legitimately be paused on the same root at once (a
    /// local reorg and a server-side reorg on the same folder are often the
    /// same event seen from two ends) -- confirming one must never also wave
    /// the other's unrelated, unreviewed wave through on the next cycle.
    #[test]
    fn bulk_delete_confirmed_incoming_is_independent_of_outbound() {
        let db = test_db();
        db.set_bulk_delete_confirmed("r1").unwrap();

        assert!(!db.take_bulk_delete_confirmed_incoming("r1").unwrap(),
            "confirming the outbound wave must not also confirm an unrelated inbound one");
        assert!(db.take_bulk_delete_confirmed("r1").unwrap(),
            "the outbound flag must still be there for its own guard to consume");
    }

    #[test]
    fn bulk_delete_confirmed_incoming_is_a_one_shot_flag() {
        let db = test_db();
        assert!(!db.take_bulk_delete_confirmed_incoming("r1").unwrap(), "must default to false when never set");

        db.set_bulk_delete_confirmed_incoming("r1").unwrap();
        assert!(db.take_bulk_delete_confirmed_incoming("r1").unwrap(), "must be true right after set");
        assert!(!db.take_bulk_delete_confirmed_incoming("r1").unwrap(), "reading it once must clear the flag");
    }

    #[test]
    fn remove_sync_root_cascades_files_failed_changes_and_config() {
        let db = test_db();
        db.upsert_sync_root("r1", "R", "/r", "srv", "eager", false, super::super::ROOT_KIND_OWNED).unwrap();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending", None).unwrap();
        db.record_failed_server_change("r1", "/a.txt", "e", 1).unwrap();
        db.config_set("last_sync_r1", "1").unwrap();
        db.set_bulk_delete_confirmed("r1").unwrap();
        db.set_bulk_delete_confirmed_incoming("r1").unwrap();
        db.config_set("changes_cursor_r1", "4711").unwrap();
        db.config_set("reconcile_checkpoint_r1", "/docs").unwrap();
        db.config_set("adopt_cursor_r1", "815").unwrap();

        db.remove_sync_root("r1").unwrap();

        assert!(db.get_sync_root("r1").unwrap().is_none());
        assert!(db.list_all_files("r1").unwrap().is_empty());
        assert!(db.list_all_failed_server_changes().unwrap().is_empty());
        assert_eq!(db.config_get("last_sync_r1").unwrap(), None);
        assert_eq!(db.config_get("bulk_delete_confirmed_r1").unwrap(), None);
        assert_eq!(db.config_get("bulk_delete_confirmed_incoming_r1").unwrap(), None);

        // Re-adding the same folder reuses this id (the local root id *is* the
        // server root id), so a surviving cursor would tell the re-added root
        // it had already synced once: adoption skipped, every existing file
        // blind-uploaded, a conflict copy per file on the server.
        assert_eq!(db.config_get("changes_cursor_r1").unwrap(), None,
            "a re-added root must look untouched to adopt_on_first_sync");
        assert_eq!(db.config_get("reconcile_checkpoint_r1").unwrap(), None,
            "and its first walk must not fast-forward past subtrees it never saw");
        assert_eq!(db.config_get("adopt_cursor_r1").unwrap(), None,
            "nor may its adopt pass start mid-feed");
    }

    #[test]
    fn remove_sync_root_on_missing_id_is_a_harmless_no_op() {
        let db = test_db();
        db.remove_sync_root("does-not-exist").unwrap();
        assert!(db.list_sync_roots().unwrap().is_empty());
    }
}
