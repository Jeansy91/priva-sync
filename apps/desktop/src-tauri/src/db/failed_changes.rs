use anyhow::Result;
use rusqlite::{params, OptionalExtension};
use super::{LocalDb, FailedServerChange};

/// Config key holding the client version that last opened this database.
const LAST_RUN_VERSION_KEY: &str = "last_run_version";

impl LocalDb {
    /// Record (or update if already present) a server change that could not be
    /// applied locally. Increments attempt_count on collision so the backoff
    /// stretches with each new failure. Carries no payload (ADR-0062): a retry
    /// re-fetches the path's current state through the stat endpoint instead
    /// of replaying whatever the feed said at the moment of failure, which
    /// could otherwise resurrect a path the server has since deleted.
    pub fn record_failed_server_change(
        &self,
        sync_root_id:  &str,
        server_path:   &str,
        error:         &str,
        next_retry_at: i64,
    ) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "INSERT INTO failed_server_changes
               (sync_root_id, server_path, last_error, attempt_count, next_retry_at)
             VALUES (?1, ?2, ?3, 1, ?4)
             ON CONFLICT(sync_root_id, server_path) DO UPDATE SET
               last_error    = excluded.last_error,
               attempt_count = attempt_count + 1,
               next_retry_at = excluded.next_retry_at",
            params![sync_root_id, server_path, error, next_retry_at],
        )?;
        Ok(())
    }

    pub fn list_due_failed_server_changes(&self, now: i64) -> Result<Vec<FailedServerChange>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT id, sync_root_id, server_path,
                    last_error, attempt_count, next_retry_at
             FROM failed_server_changes
             WHERE next_retry_at <= ?1
             ORDER BY next_retry_at ASC"
        )?;
        let rows = stmt.query_map(params![now], |row| Ok(FailedServerChange {
            id:            row.get(0)?,
            sync_root_id:  row.get(1)?,
            server_path:   row.get(2)?,
            last_error:    row.get(3)?,
            attempt_count: row.get(4)?,
            next_retry_at: row.get(5)?,
        }))?;
        Ok(rows.filter_map(|r| r.ok()).collect())
    }

    pub fn list_all_failed_server_changes(&self) -> Result<Vec<FailedServerChange>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT id, sync_root_id, server_path,
                    last_error, attempt_count, next_retry_at
             FROM failed_server_changes
             ORDER BY next_retry_at ASC"
        )?;
        let rows = stmt.query_map([], |row| Ok(FailedServerChange {
            id:            row.get(0)?,
            sync_root_id:  row.get(1)?,
            server_path:   row.get(2)?,
            last_error:    row.get(3)?,
            attempt_count: row.get(4)?,
            next_retry_at: row.get(5)?,
        }))?;
        Ok(rows.filter_map(|r| r.ok()).collect())
    }

    pub fn bump_failed_server_change(
        &self,
        id:            i64,
        error:         &str,
        next_retry_at: i64,
    ) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE failed_server_changes
             SET last_error    = ?2,
                 attempt_count = attempt_count + 1,
                 next_retry_at = ?3
             WHERE id = ?1",
            params![id, error, next_retry_at],
        )?;
        Ok(())
    }

    pub fn delete_failed_server_change(&self, id: i64) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute("DELETE FROM failed_server_changes WHERE id = ?1", params![id])?;
        Ok(())
    }

    /// Give every parked failure one fresh backoff schedule on the first start
    /// of a client version other than the one that last ran here.
    ///
    /// A failure whose backoff ran out is parked (`next_retry_at = i64::MAX`)
    /// and only a manual retry touches it again. That is right within one
    /// version — the same code would fail the same way — but wrong across an
    /// update: a failure the new version fixes would otherwise sit in the
    /// list forever, one manual click per row. Rows still inside their
    /// backoff are left alone; they get their retry anyway.
    ///
    /// The version is stored in the same transaction, so the unpark happens
    /// exactly once per version change. Returns how many rows were unparked.
    pub fn unpark_failures_on_version_change(&self, version: &str, now: i64) -> Result<usize> {
        let mut conn = self.conn.lock().unwrap();
        let tx = conn.transaction()?;
        let last: Option<String> = tx
            .query_row(
                "SELECT value FROM config WHERE key = ?1",
                params![LAST_RUN_VERSION_KEY],
                |row| row.get(0),
            )
            .optional()?;
        if last.as_deref() == Some(version) {
            return Ok(0);
        }
        let server = tx.execute(
            "UPDATE failed_server_changes
             SET next_retry_at = ?1, attempt_count = 0
             WHERE next_retry_at = ?2",
            params![now, i64::MAX],
        )?;
        let local = tx.execute(
            "UPDATE local_files
             SET next_retry_at = ?1, attempt_count = 0, updated_at = unixepoch()
             WHERE sync_status = 'pending_failed' AND next_retry_at = ?2",
            params![now, i64::MAX],
        )?;
        tx.execute(
            "INSERT INTO config (key, value) VALUES (?1, ?2)
             ON CONFLICT(key) DO UPDATE SET value = excluded.value",
            params![LAST_RUN_VERSION_KEY, version],
        )?;
        tx.commit()?;
        Ok(server + local)
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
    fn record_then_bump_increments_attempt_count() {
        let db = test_db();
        db.record_failed_server_change("root-1", "/a.txt", "first error", 100).unwrap();

        let all = db.list_all_failed_server_changes().unwrap();
        assert_eq!(all.len(), 1);
        assert_eq!(all[0].attempt_count, 1);
        assert_eq!(all[0].last_error, "first error");

        // Re-recording the same (sync_root_id, server_path) is an upsert that
        // bumps attempt_count rather than inserting a second row.
        db.record_failed_server_change("root-1", "/a.txt", "second error", 200).unwrap();

        let all = db.list_all_failed_server_changes().unwrap();
        assert_eq!(all.len(), 1);
        assert_eq!(all[0].attempt_count, 2);
        assert_eq!(all[0].last_error, "second error");
        assert_eq!(all[0].next_retry_at, 200);
    }

    #[test]
    fn list_due_only_returns_entries_at_or_before_now() {
        let db = test_db();
        db.record_failed_server_change("root-1", "/due.txt", "e", 100).unwrap();
        db.record_failed_server_change("root-1", "/future.txt", "e", 500).unwrap();

        let due = db.list_due_failed_server_changes(200).unwrap();
        assert_eq!(due.len(), 1);
        assert_eq!(due[0].server_path, "/due.txt");
    }

    #[test]
    fn bump_updates_error_and_retry_time_without_touching_others() {
        let db = test_db();
        db.record_failed_server_change("root-1", "/a.txt", "first", 100).unwrap();
        let id = db.list_all_failed_server_changes().unwrap()[0].id;

        db.bump_failed_server_change(id, "retried and failed again", 999).unwrap();

        let entry = &db.list_all_failed_server_changes().unwrap()[0];
        assert_eq!(entry.attempt_count, 2);
        assert_eq!(entry.last_error, "retried and failed again");
        assert_eq!(entry.next_retry_at, 999);
    }

    #[test]
    fn delete_removes_the_entry() {
        let db = test_db();
        db.record_failed_server_change("root-1", "/a.txt", "e", 100).unwrap();
        let id = db.list_all_failed_server_changes().unwrap()[0].id;

        db.delete_failed_server_change(id).unwrap();

        assert!(db.list_all_failed_server_changes().unwrap().is_empty());
    }

    #[test]
    fn distinct_paths_are_independent_rows() {
        let db = test_db();
        db.record_failed_server_change("root-1", "/a.txt", "e", 100).unwrap();
        db.record_failed_server_change("root-1", "/b.txt", "e", 100).unwrap();
        db.record_failed_server_change("root-2", "/a.txt", "e", 100).unwrap();

        assert_eq!(db.list_all_failed_server_changes().unwrap().len(), 3);
    }

    /// A version change gives parked rows of both tables a fresh
    /// schedule; rows still inside their backoff keep it.
    #[test]
    fn a_version_change_unparks_parked_failures_only() {
        let db = test_db();
        db.record_failed_server_change("r1", "/parked.txt", "e", i64::MAX).unwrap();
        db.record_failed_server_change("r1", "/backoff.txt", "e", 500).unwrap();
        db.upsert_file("r1", "/up-parked.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_failed("r1", "/up-parked.txt", "boom", i64::MAX).unwrap();
        db.upsert_file("r1", "/up-backoff.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_failed("r1", "/up-backoff.txt", "boom", 500).unwrap();

        assert_eq!(db.unpark_failures_on_version_change("1.1.0", 100).unwrap(), 2);

        let server: Vec<_> = db.list_due_failed_server_changes(100).unwrap();
        assert_eq!(server.len(), 1);
        assert_eq!(server[0].server_path, "/parked.txt");
        assert_eq!(server[0].attempt_count, 0);
        let backoff = db.list_all_failed_server_changes().unwrap()
            .into_iter().find(|r| r.server_path == "/backoff.txt").unwrap();
        assert_eq!(backoff.next_retry_at, 500);

        let up = db.get_file("r1", "/up-parked.txt").unwrap().unwrap();
        assert_eq!(up.next_retry_at, Some(100));
        assert_eq!(up.attempt_count, 0);
        let up = db.get_file("r1", "/up-backoff.txt").unwrap().unwrap();
        assert_eq!(up.next_retry_at, Some(500));
    }

    /// Same version again (and the first start, which records it) unparks
    /// exactly once — a row parked afterwards stays parked.
    #[test]
    fn the_same_version_leaves_parked_failures_alone() {
        let db = test_db();
        assert_eq!(db.unpark_failures_on_version_change("1.1.0", 100).unwrap(), 0);
        db.record_failed_server_change("r1", "/parked.txt", "e", i64::MAX).unwrap();

        assert_eq!(db.unpark_failures_on_version_change("1.1.0", 200).unwrap(), 0);
        assert_eq!(db.list_all_failed_server_changes().unwrap()[0].next_retry_at, i64::MAX);

        assert_eq!(db.unpark_failures_on_version_change("1.2.0", 300).unwrap(), 1);
    }
}
