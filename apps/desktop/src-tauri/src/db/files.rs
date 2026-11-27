use anyhow::Result;
use rusqlite::{params, Connection, OptionalExtension};
use super::{LocalDb, LocalFile};

/// Upload-queue lanes, served highest first (ADR-0059): a change the live
/// watcher registered goes before the walk's backlog.
///
/// Lane 0 is also the column's schema default. The terminal transitions that
/// drop a row out of the lane write it as a literal `0` in their `SET` lists
/// rather than formatting a constant into five otherwise-static statements.
pub(crate) const PRIORITY_BULK:        i64 = 0;
pub(crate) const PRIORITY_LIVE_CHANGE: i64 = 1;

/// The rows a sync cycle owes work for: everything queued, plus the retryable
/// failures whose retry window has passed. Named once because several queries
/// select on it (`list_pending_batch` via [`pending_lane`], `has_pending`,
/// `count_pending`) and drift between them would mean the upload path and the
/// "is anything left?" gate disagreeing about what counts as outstanding.
///
/// Binds `?2` as the current unix timestamp; every caller must pass it there.
const DUE_PENDING: &str = "(sync_status IN ('pending', 'pending_delete')
     OR (sync_status IN ('pending_failed', 'blocked_remote_quota', 'in_use')
         AND next_retry_at IS NOT NULL
         AND next_retry_at <= ?2))";

/// The statuses [`DUE_PENDING`] selects, as a `SET`-clause guard for writes
/// that may only touch a row while it is still queued.
const QUEUED_STATUSES: &str =
    "sync_status IN ('pending', 'pending_delete', 'pending_failed', 'blocked_remote_quota', 'in_use')";

/// One lane of the upload queue: the next `limit` due rows of the given
/// `priority`, in `path` order. Split out so [`LocalDb::list_pending_batch`]
/// can read each lane with one statement instead of sorting them together
/// (ADR-0059).
///
/// The lane number is formatted into the SQL rather than bound as a
/// parameter. Current SQLite consults a bound value when deciding whether a
/// partial index applies, but a literal makes the index predicate implied by
/// the statement text itself, which is the form that holds regardless of
/// planner version. The value is one of the `PRIORITY_*` constants above — an
/// `i64` from this module, never caller input.
fn pending_lane(
    conn:         &Connection,
    sync_root_id: &str,
    now:          i64,
    limit:        usize,
    priority:     i64,
) -> Result<Vec<LocalFile>> {
    let sql = format!(
        "SELECT sync_root_id, path, size, mtime, content_hash, sync_status, server_hash, server_version, attempt_count, last_error, next_retry_at, is_dir
         FROM local_files
         WHERE sync_root_id = ?1
           AND priority = {priority}
           AND {DUE_PENDING}
         ORDER BY path
         LIMIT ?3"
    );
    let mut stmt = conn.prepare_cached(&sql)?;
    let rows = stmt.query_map(
        params![sync_root_id, now, limit as i64],
        LocalFile::from_row,
    )?;
    Ok(rows.filter_map(|r| r.ok()).collect())
}

/// Escape a path for use as a literal prefix inside a SQL `LIKE` pattern
/// (paired with `ESCAPE '\'` at the call site).
///
/// Paths come from real filenames and can contain literal `%` and `_` — a
/// folder named `50%_off` is unremarkable — and both are LIKE wildcards.
/// `_` in particular ("any one character") is a footgun, since it is an
/// extremely common filename character.
fn escape_like(path: &str) -> String {
    path.replace('\\', "\\\\").replace('%', "\\%").replace('_', "\\_")
}

impl LocalDb {
    #[expect(clippy::too_many_arguments, reason = "one parameter per local_files column it writes")]
    pub fn upsert_file(
        &self,
        sync_root_id: &str,
        path: &str,
        size: i64,
        mtime: i64,
        hash: &[u8],
        sync_status: &str,
        server_hash: Option<&[u8]>,
    ) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "INSERT INTO local_files
               (sync_root_id, path, size, mtime, content_hash, sync_status, server_hash, updated_at)
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, unixepoch())
             ON CONFLICT(sync_root_id, path) DO UPDATE SET
               size         = excluded.size,
               mtime        = excluded.mtime,
               content_hash = excluded.content_hash,
               sync_status  = excluded.sync_status,
               server_hash  = excluded.server_hash,
               updated_at   = unixepoch()",
            params![sync_root_id, path, size, mtime, hash, sync_status, server_hash],
        )?;
        Ok(())
    }

    /// Record that a staged file is still being written: refresh its size and
    /// mtime without touching status, hash or server state.
    ///
    /// Used by the write-settling check — a staged row whose size/mtime no
    /// longer match the disk means the file moved on since it was staged, so
    /// it is not ready to upload yet. Only the two fields the next settle
    /// comparison reads are updated; the row's `content_hash` goes stale on
    /// purpose, because nothing consumes a staged row's hash (the upload path
    /// re-hashes right before sending, `reconcile` hashes the file itself).
    /// Hashing a multi-gigabyte file again on every write event would cost far
    /// more than the value it produces.
    ///
    /// Deliberately no status predicate. Which rows are eligible is the
    /// caller's decision (`is_staged_for_upload`), and a status filter here
    /// would silently no-op for the retryable states `list_pending_batch` also
    /// returns — leaving those rows skipped by the settle check forever.
    pub fn set_staged_metadata(
        &self,
        sync_root_id: &str,
        path:         &str,
        size:         i64,
        mtime:        i64,
    ) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE local_files
                SET size = ?3, mtime = ?4, updated_at = unixepoch()
              WHERE sync_root_id = ?1 AND path = ?2",
            params![sync_root_id, path, size, mtime],
        )?;
        Ok(())
    }

    /// Stage an empty folder as a pending upload.
    /// The row gets is_dir=1, size=0, empty content_hash.
    pub fn upsert_dir(
        &self,
        sync_root_id: &str,
        path:         &str,
        sync_status:  &str,
    ) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "INSERT INTO local_files
               (sync_root_id, path, size, mtime, content_hash, sync_status, server_hash, is_dir, updated_at)
             VALUES (?1, ?2, 0, 0, X'', ?3, NULL, 1, unixepoch())
             ON CONFLICT(sync_root_id, path) DO UPDATE SET
               sync_status = excluded.sync_status,
               is_dir      = 1,
               updated_at  = unixepoch()",
            params![sync_root_id, path, sync_status],
        )?;
        Ok(())
    }

    pub fn get_file(&self, sync_root_id: &str, path: &str) -> Result<Option<LocalFile>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT sync_root_id, path, size, mtime, content_hash, sync_status, server_hash, server_version, attempt_count, last_error, next_retry_at, is_dir, file_identity, disappeared_seen
             FROM local_files WHERE sync_root_id = ?1 AND path = ?2"
        )?;
        let mut rows = stmt.query(params![sync_root_id, path])?;
        match rows.next()? {
            Some(row) => Ok(Some(LocalFile::from_row(row)?)),
            None      => Ok(None),
        }
    }

    /// The next `limit` pending rows for a root: the priority lane first, the
    /// rest of the queue after — each in `path` order.
    ///
    /// Bounded for the upload path: during a first sync every row of the root
    /// is pending at once, and materialising all of them per cycle is the
    /// memory spike ADR-0037 got rid of everywhere else. `path` order is the
    /// primary key's order, so the cap costs no sort, and the batch a cycle
    /// leaves behind is picked up by the next one.
    ///
    /// The lanes are one query each rather than a single `ORDER BY priority
    /// DESC, path` (ADR-0059): that would have to sort the whole pending set —
    /// the entire root during a first sync — while a lane query rides its
    /// partial index and the bulk query keeps its sort-free primary-key order.
    pub fn list_pending_batch(&self, sync_root_id: &str, limit: usize) -> Result<Vec<LocalFile>> {
        let conn = self.conn.lock().unwrap();
        let now = chrono::Utc::now().timestamp();
        let mut out = pending_lane(&conn, sync_root_id, now, limit, PRIORITY_LIVE_CHANGE)?;
        if out.len() < limit {
            out.extend(pending_lane(&conn, sync_root_id, now, limit - out.len(), PRIORITY_BULK)?);
        }
        Ok(out)
    }

    /// Move a staged row into the priority lane — used for everything the live
    /// watcher reports, so a change the user just made on disk is uploaded
    /// ahead of whatever a running full walk has queued up (ADR-0059).
    ///
    /// Returns whether this call actually *entered* the row into the lane. The
    /// `priority = 0` term is what makes that answer meaningful: a file being
    /// copied in emits write events throughout, and each one re-stages a row
    /// that is already in the lane. Counting those as arrivals would make a
    /// running upload batch preempt itself for the whole duration of the copy.
    ///
    /// The status guard belongs here rather than at the call sites: a `synced`
    /// row raised into the lane would sit in the partial index forever,
    /// because only the transitions *out of* the queue clear the flag.
    pub fn set_priority(&self, sync_root_id: &str, path: &str) -> Result<bool> {
        let conn = self.conn.lock().unwrap();
        let changed = conn.execute(
            &format!(
                "UPDATE local_files SET priority = ?3
                  WHERE sync_root_id = ?1 AND path = ?2
                    AND priority = 0
                    AND {QUEUED_STATUSES}"
            ),
            params![sync_root_id, path, PRIORITY_LIVE_CHANGE],
        )?;
        Ok(changed > 0)
    }

    /// Whether anything at all is still pending for a root — an `EXISTS`
    /// check against [`DUE_PENDING`] rather than materialising rows just to
    /// look at whether any came back.
    pub fn has_pending(&self, sync_root_id: &str) -> Result<bool> {
        let conn = self.conn.lock().unwrap();
        let now = chrono::Utc::now().timestamp();
        let found: i64 = conn.query_row(
            &format!(
                "SELECT EXISTS(
                   SELECT 1 FROM local_files
                   WHERE sync_root_id = ?1 AND {DUE_PENDING})"
            ),
            params![sync_root_id, now],
            |r| r.get(0),
        )?;
        Ok(found != 0)
    }

    /// How many rows are still pending for a root — the same [`DUE_PENDING`]
    /// predicate the other queries here select on, counted instead of
    /// materialised.
    ///
    /// This is the denominator the upload progress needs: what is left to do
    /// for this root, not how many rows one batch happened to take
    /// (ADR-0056).
    pub fn count_pending(&self, sync_root_id: &str) -> Result<usize> {
        let conn = self.conn.lock().unwrap();
        let now = chrono::Utc::now().timestamp();
        let count: i64 = conn.query_row(
            &format!(
                "SELECT COUNT(*) FROM local_files
                  WHERE sync_root_id = ?1 AND {DUE_PENDING}"
            ),
            params![sync_root_id, now],
            |r| r.get(0),
        )?;
        Ok(count as usize)
    }

    /// `(pending, conflict)` counts across all *active* roots — the two numbers
    /// the tray line shows, with the same predicate as
    /// [`LocalDb::list_pending_all`].
    ///
    /// Counted, not listed: this is emitted after every single transferred
    /// file, and listing every pending row of the system to read its `len()`
    /// meant materialising the whole queue tens of thousands of times during a
    /// first sync.
    pub fn pending_counts_all(&self) -> Result<(usize, usize)> {
        let conn = self.conn.lock().unwrap();
        let (pending, conflict): (i64, i64) = conn.query_row(
            "SELECT
               COUNT(*),
               COALESCE(SUM(CASE WHEN lf.sync_status = 'conflict' THEN 1 ELSE 0 END), 0)
             FROM local_files lf
             JOIN sync_roots sr ON sr.id = lf.sync_root_id
             WHERE lf.sync_status IN ('pending', 'pending_delete', 'conflict')
               AND sr.paused = 0",
            [],
            |r| Ok((r.get(0)?, r.get(1)?)),
        )?;
        Ok((pending as usize, conflict as usize))
    }

    /// `(any conflict, anything pending)` across all *active* roots — the two
    /// facts the tray status is derived from, answered by the database instead
    /// of by counting a materialised `Vec` of every pending row on the system.
    /// Same root/status predicate as [`LocalDb::list_pending_all`].
    pub fn pending_summary_all(&self) -> Result<(bool, bool)> {
        let conn = self.conn.lock().unwrap();
        let (conflict, pending): (i64, i64) = conn.query_row(
            "SELECT
               EXISTS(SELECT 1 FROM local_files lf
                        JOIN sync_roots sr ON sr.id = lf.sync_root_id
                       WHERE lf.sync_status = 'conflict' AND sr.paused = 0),
               EXISTS(SELECT 1 FROM local_files lf
                        JOIN sync_roots sr ON sr.id = lf.sync_root_id
                       WHERE lf.sync_status IN ('pending', 'pending_delete', 'conflict')
                         AND sr.paused = 0)",
            [],
            |r| Ok((r.get(0)?, r.get(1)?)),
        )?;
        Ok((conflict != 0, pending != 0))
    }

    /// How many rows the root tracks. Denominator of the bulk-delete ratio,
    /// which used to read `list_all_files(..).len()` — a whole root in memory
    /// to produce one number.
    pub fn count_tracked_files(&self, sync_root_id: &str) -> Result<usize> {
        let conn = self.conn.lock().unwrap();
        let count: i64 = conn.query_row(
            "SELECT COUNT(*) FROM local_files WHERE sync_root_id = ?1",
            params![sync_root_id],
            |r| r.get(0),
        )?;
        Ok(count as usize)
    }

    /// How many rows a root's `pending_delete` rows will remove in total.
    ///
    /// The bulk-delete guard's numerator has to come from the database, not
    /// from the batch it is about to upload: with a batched upload path (or a
    /// walk that is still staging), the batch only ever shows a slice of the
    /// wave, and a guard that judges a mass deletion by a slice never fires.
    ///
    /// `stage_delete` never cascades into the local index: a deleted
    /// directory gets exactly one `pending_delete` row, no matter how many
    /// files live under it. Counting rows alone would let a directory
    /// deletion hide an arbitrarily large subtree from the guard, so a
    /// `pending_delete` directory row additionally counts every descendant
    /// still known in the index — a `LIKE`-prefix scan, not a filesystem walk.
    pub fn count_pending_deletes(&self, sync_root_id: &str) -> Result<usize> {
        let conn = self.conn.lock().unwrap();
        let base: i64 = conn.query_row(
            "SELECT COUNT(*) FROM local_files
              WHERE sync_root_id = ?1 AND sync_status = 'pending_delete'",
            params![sync_root_id],
            |r| r.get(0),
        )?;

        let mut stmt = conn.prepare(
            "SELECT path FROM local_files
              WHERE sync_root_id = ?1 AND sync_status = 'pending_delete' AND is_dir = 1"
        )?;
        let dir_paths = stmt.query_map(params![sync_root_id], |r| r.get::<_, String>(0))?;

        let mut descendants: i64 = 0;
        for dir_path in dir_paths {
            let pattern = format!("{}/%", escape_like(&dir_path?));
            let n: i64 = conn.query_row(
                "SELECT COUNT(*) FROM local_files
                  WHERE sync_root_id = ?1 AND path LIKE ?2 ESCAPE '\\'",
                params![sync_root_id, pattern],
                |r| r.get(0),
            )?;
            descendants += n;
        }

        Ok((base + descendants) as usize)
    }

    /// Counterpart to [`Self::count_pending_deletes`] for the inbound
    /// direction: how many rows are staged as `pending_incoming_delete`
    /// — the guard's numerator before it applies a wave of server-initiated
    /// deletions. No directory-descendant expansion is needed here (contrast
    /// `count_pending_deletes`): the server already cascades one tombstone
    /// per real descendant file into the changes feed, so every row that
    /// should count already has its own row.
    pub fn count_pending_incoming_deletes(&self, sync_root_id: &str) -> Result<usize> {
        let conn = self.conn.lock().unwrap();
        let count: i64 = conn.query_row(
            "SELECT COUNT(*) FROM local_files
              WHERE sync_root_id = ?1 AND sync_status = 'pending_incoming_delete'",
            params![sync_root_id],
            |r| r.get(0),
        )?;
        Ok(count as usize)
    }

    /// One keyset page of a root's tracked file paths: up to `limit` paths
    /// greater than `after`, in `path` order (`""` starts at the beginning).
    ///
    /// Keyset rather than `OFFSET` so paging stays index-backed on
    /// `PRIMARY KEY (sync_root_id, path)` all the way through a million-row
    /// root, and stable while the pass it drives writes to other columns.
    pub fn list_file_paths_after(
        &self,
        sync_root_id: &str,
        after:        &str,
        limit:        usize,
    ) -> Result<Vec<String>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT path FROM local_files
              WHERE sync_root_id = ?1
                AND path > ?2
                AND is_dir = 0
                AND sync_status != 'pending_delete'
              ORDER BY path
              LIMIT ?3"
        )?;
        let rows = stmt.query_map(
            params![sync_root_id, after, limit as i64],
            |r| r.get::<_, String>(0),
        )?;
        Ok(rows.filter_map(|r| r.ok()).collect())
    }

    /// All pending files across all *active* sync roots (for status display).
    /// Rows belonging to paused roots are intentionally excluded — those will
    /// not progress until the user resumes the root, so counting them keeps
    /// the global status stuck on "syncing" forever.
    pub fn list_pending_all(&self) -> Result<Vec<LocalFile>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT lf.sync_root_id, lf.path, lf.size, lf.mtime, lf.content_hash,
                    lf.sync_status, lf.server_hash, lf.server_version,
                    lf.attempt_count, lf.last_error, lf.next_retry_at, lf.is_dir
             FROM local_files lf
             JOIN sync_roots sr ON sr.id = lf.sync_root_id
             WHERE lf.sync_status IN ('pending', 'pending_delete', 'conflict')
               AND sr.paused = 0"
        )?;
        let rows = stmt.query_map([], LocalFile::from_row)?;
        Ok(rows.filter_map(|r| r.ok()).collect())
    }

    /// Every local file/delete stuck in a failure state — 'pending_failed'
    /// (exhausted the exponential backoff, manual-only) or
    /// 'blocked_remote_quota' (steady poll waiting on a mount giver's quota,
    /// ADR-0011) or 'in_use' (steady poll waiting on another program to close
    /// the file, ADR-0077) — across all sync roots, including paused ones: a paused
    /// root doesn't clear an existing failure, and the user may still want
    /// to see it. Feeds the "Sync-Probleme" list; deliberately not
    /// `list_pending_all`, which excludes these statuses on purpose so the
    /// tray status doesn't get stuck on "syncing" (see its own doc comment).
    pub fn list_all_local_failures(&self) -> Result<Vec<LocalFile>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT sync_root_id, path, size, mtime, content_hash,
                    sync_status, server_hash, server_version,
                    attempt_count, last_error, next_retry_at, is_dir
             FROM local_files
             WHERE sync_status IN ('pending_failed', 'blocked_remote_quota', 'in_use')
             ORDER BY next_retry_at IS NULL, next_retry_at ASC"
        )?;
        let rows = stmt.query_map([], LocalFile::from_row)?;
        Ok(rows.filter_map(|r| r.ok()).collect())
    }

    /// Paths of immediate (one level deep) children of `dir_path` currently
    /// tracked in the local index, excluding rows already `pending_delete`.
    /// Used by the local walk's per-directory deletion-detection (ADR-0037)
    /// so it never has to load the whole root's file list -- memory scales
    /// with one directory's width, not the root's size. `dir_path` is a
    /// server-relative directory path with no trailing slash ("" for the
    /// sync root itself, "/foo" for a subdirectory).
    pub fn list_immediate_children(&self, sync_root_id: &str, dir_path: &str) -> Result<Vec<String>> {
        let escaped = escape_like(dir_path);
        let immediate_pattern = format!("{escaped}/%");
        let deeper_pattern    = format!("{escaped}/%/%");

        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT path FROM local_files
             WHERE sync_root_id = ?1
               AND path LIKE ?2 ESCAPE '\\'
               AND path NOT LIKE ?3 ESCAPE '\\'
               AND sync_status NOT IN ('pending_delete', 'disappeared')"
        )?;
        let rows = stmt.query_map(
            params![sync_root_id, immediate_pattern, deeper_pattern],
            |r| r.get::<_, String>(0),
        )?;
        Ok(rows.filter_map(|r| r.ok()).collect())
    }

    /// Sync status of every tracked file directly inside `dir_path`, keyed by
    /// the file's root-relative path. `dir_path` is root-relative and carries
    /// no trailing slash; the empty string addresses the sync root itself.
    ///
    /// Directory-scoped on purpose. The placeholder pass walks the tree and
    /// only ever needs one directory's rows at a time; the whole-root
    /// alternative ([`LocalDb::list_all_files`]) materialises every row of the
    /// root at once, which is gigabytes on a million-file root — the same
    /// memory rule ADR-0037 applied to the local walk.
    pub fn file_statuses_in_dir(
        &self,
        sync_root_id: &str,
        dir_path:     &str,
    ) -> Result<std::collections::HashMap<String, String>> {
        let escaped = escape_like(dir_path);
        let immediate_pattern = format!("{escaped}/%");
        let deeper_pattern    = format!("{escaped}/%/%");

        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT path, sync_status FROM local_files
             WHERE sync_root_id = ?1
               AND path LIKE ?2 ESCAPE '\\'
               AND path NOT LIKE ?3 ESCAPE '\\'
               AND is_dir = 0
               AND sync_status != 'pending_delete'"
        )?;
        let rows = stmt.query_map(
            params![sync_root_id, immediate_pattern, deeper_pattern],
            |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)),
        )?;
        Ok(rows.filter_map(|r| r.ok()).collect())
    }

    /// All tracked files for a given sync root regardless of status. No
    /// production caller — a full-dump test helper for asserting on a root's
    /// entire row set at once.
    #[cfg(test)]
    pub fn list_all_files(&self, sync_root_id: &str) -> Result<Vec<LocalFile>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT sync_root_id, path, size, mtime, content_hash, sync_status, server_hash, server_version, attempt_count, last_error, next_retry_at, is_dir
             FROM local_files
             WHERE sync_root_id = ?1"
        )?;
        let rows = stmt.query_map(params![sync_root_id], LocalFile::from_row)?;
        Ok(rows.filter_map(|r| r.ok()).collect())
    }

    /// Look up a file by its server root ID and server-relative path.
    /// Used by the hydration callback to record the downloaded file.
    pub fn get_file_by_server_path(
        &self,
        server_root_id: &str,
        server_path:    &str,
    ) -> Result<Option<LocalFile>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT lf.sync_root_id, lf.path, lf.size, lf.mtime,
                    lf.content_hash, lf.sync_status, lf.server_hash, lf.server_version, lf.attempt_count, lf.last_error, lf.next_retry_at, lf.is_dir
             FROM local_files lf
             JOIN sync_roots  sr ON sr.id = lf.sync_root_id
             WHERE sr.server_root_id = ?1 AND lf.path = ?2"
        )?;
        let mut rows = stmt.query(params![server_root_id, server_path])?;
        match rows.next()? {
            Some(row) => Ok(Some(LocalFile::from_row(row)?)),
            None      => Ok(None),
        }
    }

    pub fn set_synced(&self, sync_root_id: &str, path: &str, server_hash: &[u8]) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE local_files
             SET sync_status   = 'synced',
                 server_hash   = ?1,
                 last_error    = NULL,
                 attempt_count = 0,
                 next_retry_at = NULL,
                 priority      = 0
             WHERE sync_root_id = ?2 AND path = ?3",
            params![server_hash, sync_root_id, path],
        )?;
        Ok(())
    }

    /// Insert (or refresh) a row for a file that already matches the server,
    /// recording it as fully synced with the server_version. Used by the adopt
    /// pass on a freshly (re-)added root so pre-existing files are not blind-
    /// uploaded (which the server would treat as a real conflict). `hash` is the
    /// local BLAKE3, which equals the server's content_hash for an adopted file,
    /// so it doubles as content_hash and server_hash.
    pub fn upsert_synced_with_version(
        &self,
        sync_root_id:   &str,
        path:           &str,
        size:           i64,
        mtime:          i64,
        hash:           &[u8],
        server_version: i64,
    ) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "INSERT INTO local_files
               (sync_root_id, path, size, mtime, content_hash, sync_status, server_hash, server_version, updated_at)
             VALUES (?1, ?2, ?3, ?4, ?5, 'synced', ?5, ?6, unixepoch())
             ON CONFLICT(sync_root_id, path) DO UPDATE SET
               size           = excluded.size,
               mtime          = excluded.mtime,
               content_hash   = excluded.content_hash,
               sync_status    = 'synced',
               server_hash    = excluded.server_hash,
               server_version = excluded.server_version,
               priority       = 0,
               updated_at     = unixepoch()",
            params![sync_root_id, path, size, mtime, hash, server_version],
        )?;
        Ok(())
    }

    /// Like `set_synced` but also stores the server_version returned after upload.
    pub fn set_synced_with_version(
        &self,
        sync_root_id:   &str,
        path:           &str,
        server_hash:    &[u8],
        server_version: i64,
    ) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE local_files
             SET sync_status   = 'synced',
                 server_hash   = ?1,
                 server_version = ?2,
                 last_error    = NULL,
                 attempt_count = 0,
                 next_retry_at = NULL,
                 priority      = 0
             WHERE sync_root_id = ?3 AND path = ?4",
            params![server_hash, server_version, sync_root_id, path],
        )?;
        Ok(())
    }

    /// Close a conflict row. `pending` controls the resulting status: `false`
    /// keeps the server version (status `synced`, server_hash aligned with the
    /// local content), `true` marks the file for re-upload (the local bytes
    /// just changed). Clears the stored conflict-copy path either way. See
    /// ADR-0003 / the desktop strategies in `sync::conflict_resolve`.
    pub fn resolve_conflict(&self, sync_root_id: &str, path: &str, pending: bool) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        let sql = if pending {
            "UPDATE local_files
             SET sync_status = 'pending', conflict_copy_path = NULL, updated_at = unixepoch()
             WHERE sync_root_id = ?1 AND path = ?2"
        } else {
            "UPDATE local_files
             SET sync_status = 'synced', server_hash = content_hash, conflict_copy_path = NULL, priority = 0
             WHERE sync_root_id = ?1 AND path = ?2"
        };
        conn.execute(sql, params![sync_root_id, path])?;
        Ok(())
    }

    /// Mark a row as a conflict and record the absolute path of the on-disk
    /// `.conflict.<ts>` loser copy so a later resolve can act on it.
    pub fn set_conflict(&self, sync_root_id: &str, path: &str, conflict_copy_path: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE local_files SET sync_status = 'conflict', conflict_copy_path = ?3, priority = 0
             WHERE sync_root_id = ?1 AND path = ?2",
            params![sync_root_id, path, conflict_copy_path],
        )?;
        Ok(())
    }

    /// The absolute path of the stored loser copy for a conflict row, or None
    /// when the row is missing or carries no copy (e.g. older conflict rows).
    pub fn conflict_copy_path(&self, sync_root_id: &str, path: &str) -> Result<Option<String>> {
        let conn = self.conn.lock().unwrap();
        let v = conn.query_row(
            "SELECT conflict_copy_path FROM local_files WHERE sync_root_id = ?1 AND path = ?2",
            params![sync_root_id, path],
            |r| r.get::<_, Option<String>>(0),
        ).optional()?;
        Ok(v.flatten())
    }

    pub fn delete_file_record(&self, sync_root_id: &str, path: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "DELETE FROM local_files WHERE sync_root_id = ?1 AND path = ?2",
            params![sync_root_id, path],
        )?;
        Ok(())
    }

    /// Rename a local file record after a successful server-side move.
    /// Updates `path` in-place so the DB stays consistent with the server.
    pub fn rename_file(&self, sync_root_id: &str, from_path: &str, to_path: &str) -> Result<()> {
        // The whole subtree moves, not just the named row. A directory rename
        // relocates every descendant on disk and server-side in one step
        // (repository.MoveDirectory does the same rewrite), so leaving the
        // child rows at their old paths would orphan them: nothing walks a
        // directory that no longer exists, so they are never revisited, while
        // the files under the new path look untracked and get blind-uploaded.
        //
        // `substr` is 1-indexed and counts characters, matching `length`, so
        // the tail after the old prefix is carried over verbatim.
        let from_subtree = format!("{}/%", escape_like(from_path));
        let to_subtree   = format!("{}/%", escape_like(to_path));

        let mut conn = self.conn.lock().unwrap();
        let tx = conn.transaction()?;

        // Clear whatever occupies the destination first. A row there is a
        // stale duplicate -- typically one a full walk created for the moved
        // content before the pull got a chance to recognise the rename -- and
        // without this the UPDATE below would hit the UNIQUE(sync_root_id,
        // path) constraint and abandon the rename entirely.
        tx.execute(
            "DELETE FROM local_files
             WHERE sync_root_id = ?1
               AND (path = ?2 OR path LIKE ?3 ESCAPE '\\')",
            params![sync_root_id, to_path, to_subtree],
        )?;

        // A row staged `pending_incoming_delete` that turns out to be
        // the near side of a rename was never actually deleted -- clearing
        // the flag here, at the primitive that moves a row's path, closes
        // that off for every caller instead of relying on call sites to know
        // about it.
        tx.execute(
            "UPDATE local_files
             SET path = ?3 || substr(path, length(?2) + 1),
                 sync_status = CASE WHEN sync_status = 'pending_incoming_delete'
                                    THEN 'synced' ELSE sync_status END
             WHERE sync_root_id = ?1
               AND (path = ?2 OR path LIKE ?4 ESCAPE '\\')",
            params![sync_root_id, from_path, to_path, from_subtree],
        )?;

        tx.commit()?;
        Ok(())
    }

    /// Record the server's file id for a local row so a later pull can spot a
    /// server-side rename by identity. No-op when the row is missing.
    pub fn set_server_file_id(&self, sync_root_id: &str, path: &str, server_file_id: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE local_files SET server_file_id = ?3 WHERE sync_root_id = ?1 AND path = ?2",
            params![sync_root_id, path, server_file_id],
        )?;
        Ok(())
    }

    /// Record the OS-assigned identity for a local row (ADR-0063) — lazily
    /// filled at upload/download/walk touch, the same pattern as
    /// `set_server_file_id`. No-op when the row is missing.
    pub fn set_file_identity(&self, sync_root_id: &str, path: &str, identity: &[u8]) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE local_files SET file_identity = ?3 WHERE sync_root_id = ?1 AND path = ?2",
            params![sync_root_id, path, identity],
        )?;
        Ok(())
    }

    /// Flip a row to `pending_delete`, leaving everything else untouched. The
    /// row is the only record that a result for `path` may still be due from
    /// an in-flight upload, so it must outlive the local file.
    pub fn mark_pending_delete(&self, sync_root_id: &str, path: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE local_files SET sync_status = 'pending_delete', priority = 0
             WHERE sync_root_id = ?1 AND path = ?2",
            params![sync_root_id, path],
        )?;
        Ok(())
    }

    /// Mark a row as having disappeared from its path (ADR-0063): a rename or
    /// a deletion, not yet known which. Flips only `sync_status` (and resets
    /// the second-look flag for a fresh disappearance) -- size, mtime,
    /// content_hash and file_identity are exactly what the next cycle's join
    /// needs, and are left untouched.
    pub fn mark_disappeared(&self, sync_root_id: &str, path: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE local_files SET sync_status = 'disappeared', priority = 0, disappeared_seen = 0
             WHERE sync_root_id = ?1 AND path = ?2",
            params![sync_root_id, path],
        )?;
        Ok(())
    }

    /// Staging half of the inbound bulk-delete guard: flip a row to
    /// `pending_incoming_delete` without applying anything yet, so the whole
    /// pull cycle's true delete count is durable (counted by
    /// [`Self::count_pending_incoming_deletes`]) before any of it touches
    /// disk. Leaves content_hash/server_hash untouched -- the flush step
    /// still needs them, same care as [`Self::mark_disappeared`].
    pub fn mark_pending_incoming_delete(&self, sync_root_id: &str, path: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE local_files SET sync_status = 'pending_incoming_delete'
             WHERE sync_root_id = ?1 AND path = ?2",
            params![sync_root_id, path],
        )?;
        Ok(())
    }

    /// Every row staged as `pending_incoming_delete` for a root --
    /// what the flush step actually removes from disk + DB once a pull
    /// cycle clears the guard without tripping it.
    pub fn list_pending_incoming_deletes(&self, sync_root_id: &str) -> Result<Vec<LocalFile>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT sync_root_id, path, size, mtime, content_hash, sync_status, server_hash, server_version, attempt_count, last_error, next_retry_at, is_dir, file_identity, disappeared_seen
             FROM local_files
             WHERE sync_root_id = ?1 AND sync_status = 'pending_incoming_delete'"
        )?;
        let rows = stmt.query_map(params![sync_root_id], LocalFile::from_row)?;
        Ok(rows.filter_map(|r| r.ok()).collect())
    }

    /// Record that a `disappeared` row survived one resolve pass without a
    /// matching staged path -- the next miss converts it to `pending_delete`
    /// (ADR-0063's "second look", so a `From`/`To` pair straddling a cycle
    /// boundary still gets to pair up).
    pub fn mark_disappeared_missed(&self, sync_root_id: &str, path: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE local_files SET disappeared_seen = 1 WHERE sync_root_id = ?1 AND path = ?2",
            params![sync_root_id, path],
        )?;
        Ok(())
    }

    /// Every `disappeared` row for a root -- candidates the next resolve
    /// pass tries to join against a freshly staged path (ADR-0063).
    pub fn list_disappeared(&self, sync_root_id: &str) -> Result<Vec<LocalFile>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT sync_root_id, path, size, mtime, content_hash, sync_status, server_hash, server_version, attempt_count, last_error, next_retry_at, is_dir, file_identity, disappeared_seen
             FROM local_files
             WHERE sync_root_id = ?1 AND sync_status = 'disappeared'"
        )?;
        let rows = stmt.query_map(params![sync_root_id], LocalFile::from_row)?;
        Ok(rows.filter_map(|r| r.ok()).collect())
    }

    /// Freshly staged files and directories for a root -- the far side a
    /// resolve pass tries to join a `disappeared` row against (ADR-0063). A
    /// renamed folder's `To` half stages as a pending directory row exactly
    /// like a file's `To` stages as a pending upload.
    pub fn list_staged_files(&self, sync_root_id: &str) -> Result<Vec<LocalFile>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT sync_root_id, path, size, mtime, content_hash, sync_status, server_hash, server_version, attempt_count, last_error, next_retry_at, is_dir, file_identity, disappeared_seen
             FROM local_files
             WHERE sync_root_id = ?1 AND sync_status = 'pending'"
        )?;
        let rows = stmt.query_map(params![sync_root_id], LocalFile::from_row)?;
        Ok(rows.filter_map(|r| r.ok()).collect())
    }

    /// Paths under `dir_path` (at any depth) whose local state has not
    /// reached the server yet — queued, stuck after retries, blocked on a
    /// giver's quota, or an open conflict.
    ///
    /// Used before a server-driven removal of a whole directory: those rows
    /// are the only local work in the subtree that exists nowhere else, so
    /// they have to be rescued before the directory goes.
    /// Whether anything below `dir_path` is still waiting to reach the server
    /// — the same predicate [`LocalDb::list_unsynced_under`] lists, answered
    /// as a yes/no so it can be asked once per directory.
    ///
    /// This is what decides whether a directory may be converted to an
    /// *in-sync* placeholder. Asserting it unconditionally put a green
    /// checkmark on folders whose contents had not been uploaded yet — an
    /// Explorer telling the user their data was safe when it was still queued.
    pub fn has_unsynced_under(&self, sync_root_id: &str, dir_path: &str) -> Result<bool> {
        let subtree = format!("{}/%", escape_like(dir_path));
        let conn = self.conn.lock().unwrap();
        let found: i64 = conn.query_row(
            "SELECT EXISTS(
               SELECT 1 FROM local_files
                WHERE sync_root_id = ?1
                  AND path LIKE ?2 ESCAPE '\\'
                  AND is_dir = 0
                  AND sync_status IN
                      ('pending', 'pending_delete', 'pending_failed',
                       'blocked_remote_quota', 'in_use', 'conflict'))",
            params![sync_root_id, subtree],
            |r| r.get(0),
        )?;
        Ok(found != 0)
    }

    pub fn list_unsynced_under(&self, sync_root_id: &str, dir_path: &str) -> Result<Vec<String>> {
        let subtree = format!("{}/%", escape_like(dir_path));
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT path FROM local_files
             WHERE sync_root_id = ?1
               AND path LIKE ?2 ESCAPE '\\'
               AND is_dir = 0
               AND sync_status IN
                   ('pending', 'pending_failed', 'blocked_remote_quota', 'in_use', 'conflict')
             ORDER BY path"
        )?;
        let rows = stmt.query_map(params![sync_root_id, subtree], |r| r.get::<_, String>(0))?;
        Ok(rows.filter_map(|r| r.ok()).collect())
    }

    /// `(path, server_file_id, is_dir)` for every row in the root that carries
    /// a known server id. Feeds the id-based rename detection in the pull
    /// reconcile; rows without an id (not yet backfilled) are skipped.
    pub fn list_server_file_ids(&self, sync_root_id: &str) -> Result<Vec<(String, String, bool)>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT path, server_file_id, is_dir FROM local_files
             WHERE sync_root_id = ?1 AND server_file_id IS NOT NULL"
        )?;
        let rows = stmt.query_map(params![sync_root_id], |r| {
            Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?, r.get::<_, i64>(2)? != 0))
        })?;
        Ok(rows.filter_map(|r| r.ok()).collect())
    }

    /// Mark a local file as 'pending_failed' after exhausted upload/delete
    /// retries. Increments attempt_count and schedules a future retry.
    pub fn set_failed(
        &self,
        sync_root_id:  &str,
        path:          &str,
        error:         &str,
        next_retry_at: i64,
    ) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE local_files
             SET sync_status   = 'pending_failed',
                 last_error    = ?3,
                 attempt_count = attempt_count + 1,
                 next_retry_at = ?4,
                 updated_at    = unixepoch()
             WHERE sync_root_id = ?1 AND path = ?2",
            params![sync_root_id, path, error, next_retry_at],
        )?;
        Ok(())
    }

    /// Mark a mount upload as blocked by the giver's full quota (ADR-0011).
    /// Distinct from `set_failed`: a fixed `next_retry_at` (no exponential
    /// backoff) and `attempt_count` is left untouched, since this is a wait on
    /// the giver, not a client-side failure. The local bytes stay on disk.
    pub fn set_blocked_remote_quota(
        &self,
        sync_root_id:  &str,
        path:          &str,
        next_retry_at: i64,
    ) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE local_files
             SET sync_status   = 'blocked_remote_quota',
                 last_error    = 'remote quota full',
                 next_retry_at = ?3,
                 updated_at    = unixepoch()
             WHERE sync_root_id = ?1 AND path = ?2",
            params![sync_root_id, path, next_retry_at],
        )?;
        Ok(())
    }

    /// Mark an upload as deferred because another program has the file open
    /// (ADR-0077). Same shape as [`Self::set_blocked_remote_quota`]: a fixed
    /// `next_retry_at` and `attempt_count` untouched — the wait is on the user
    /// closing the program, not a client-side failure. Returns whether the row
    /// just entered the state, so the caller can refresh the problems list
    /// once instead of on every retry.
    pub fn set_in_use(
        &self,
        sync_root_id:  &str,
        path:          &str,
        message:       &str,
        next_retry_at: i64,
    ) -> Result<bool> {
        let conn = self.conn.lock().unwrap();
        let was_in_use: bool = conn.query_row(
            "SELECT sync_status = 'in_use' FROM local_files WHERE sync_root_id = ?1 AND path = ?2",
            params![sync_root_id, path],
            |r| r.get(0),
        ).optional()?.unwrap_or(false);
        conn.execute(
            "UPDATE local_files
             SET sync_status   = 'in_use',
                 last_error    = ?3,
                 next_retry_at = ?4,
                 updated_at    = unixepoch()
             WHERE sync_root_id = ?1 AND path = ?2",
            params![sync_root_id, path, message, next_retry_at],
        )?;
        Ok(!was_in_use)
    }

    /// Reset a previously-failed local file back to 'pending' so the next sync
    /// cycle picks it up. Resets retry counter as well — used by manual retry.
    pub fn reset_failed(&self, sync_root_id: &str, path: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "UPDATE local_files
             SET sync_status   = 'pending',
                 last_error    = NULL,
                 attempt_count = 0,
                 next_retry_at = NULL,
                 updated_at    = unixepoch()
             WHERE sync_root_id = ?1 AND path = ?2",
            params![sync_root_id, path],
        )?;
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
    fn upsert_then_get_round_trips_a_file_row() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 10, 100, b"hash1", "pending", Some(b"srvhash")).unwrap();

        let got = db.get_file("r1", "/a.txt").unwrap().expect("row must exist");
        assert_eq!(got.size, 10);
        assert_eq!(got.mtime, 100);
        assert_eq!(got.content_hash, b"hash1");
        assert_eq!(got.sync_status, "pending");
        assert_eq!(got.server_hash, Some(b"srvhash".to_vec()));
        assert_eq!(got.server_version, -1, "server_version defaults to -1 until a successful upload");
        assert!(!got.is_dir);
    }

    #[test]
    fn upsert_on_existing_key_overwrites_fields() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 10, 100, b"h1", "pending", None).unwrap();
        db.upsert_file("r1", "/a.txt", 20, 200, b"h2", "synced", Some(b"h2")).unwrap();

        let got = db.get_file("r1", "/a.txt").unwrap().unwrap();
        assert_eq!(got.size, 20);
        assert_eq!(got.mtime, 200);
        assert_eq!(got.content_hash, b"h2");
        assert_eq!(got.sync_status, "synced");
    }

    #[test]
    fn get_file_returns_none_when_missing() {
        let db = test_db();
        assert!(db.get_file("r1", "/nope.txt").unwrap().is_none());
    }

    #[test]
    fn set_file_identity_then_get_file_round_trips() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 10, 100, b"h", "pending", None).unwrap();
        db.set_file_identity("r1", "/a.txt", b"identity-bytes").unwrap();

        let got = db.get_file("r1", "/a.txt").unwrap().unwrap();
        assert_eq!(got.file_identity, Some(b"identity-bytes".to_vec()));
    }

    #[test]
    fn get_file_returns_none_identity_when_never_set() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 10, 100, b"h", "pending", None).unwrap();

        let got = db.get_file("r1", "/a.txt").unwrap().unwrap();
        assert_eq!(got.file_identity, None);
    }

    /// The join a later sync cycle runs (ADR-0063) needs size, hash and
    /// identity of a disappeared row exactly as they were before it vanished
    /// -- `mark_disappeared` must flip only `sync_status`.
    #[test]
    fn mark_disappeared_flips_status_and_preserves_everything_else() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 10, 100, b"h1", "synced", Some(b"h1")).unwrap();
        db.set_file_identity("r1", "/a.txt", b"vol1-id42").unwrap();

        db.mark_disappeared("r1", "/a.txt").unwrap();

        let got = db.get_file("r1", "/a.txt").unwrap().unwrap();
        assert_eq!(got.sync_status, "disappeared");
        assert_eq!(got.size, 10);
        assert_eq!(got.mtime, 100);
        assert_eq!(got.content_hash, b"h1");
        assert_eq!(got.file_identity, Some(b"vol1-id42".to_vec()));
        assert!(!got.disappeared_seen, "a freshly disappeared row has not survived a resolve pass yet");
    }

    #[test]
    fn list_disappeared_scopes_to_root_and_status() {
        let db = test_db();
        db.upsert_file("r1", "/gone.txt", 1, 1, b"h", "synced", None).unwrap();
        db.mark_disappeared("r1", "/gone.txt").unwrap();
        db.upsert_file("r1", "/still-here.txt", 1, 1, b"h", "synced", None).unwrap();
        db.upsert_file("r2", "/also-gone.txt", 1, 1, b"h", "synced", None).unwrap();
        db.mark_disappeared("r2", "/also-gone.txt").unwrap();

        let got: Vec<String> = db.list_disappeared("r1").unwrap().into_iter().map(|f| f.path).collect();
        assert_eq!(got, vec!["/gone.txt".to_string()]);
    }

    /// A renamed folder's `To` half stages as `DirCreated` and needs
    /// to be a join candidate exactly like a file -- excluded only by status.
    #[test]
    fn list_staged_files_scopes_to_pending_rows_files_and_dirs() {
        let db = test_db();
        db.upsert_file("r1", "/new.txt", 1, 1, b"h", "pending", None).unwrap();
        db.upsert_file("r1", "/synced.txt", 1, 1, b"h", "synced", None).unwrap();
        db.upsert_file("r1", "/gone.txt", 1, 1, b"h", "pending_delete", None).unwrap();
        db.upsert_dir("r1", "/new-dir", "pending").unwrap();
        db.upsert_dir("r1", "/synced-dir", "synced").unwrap();

        let mut got: Vec<String> = db.list_staged_files("r1").unwrap().into_iter().map(|f| f.path).collect();
        got.sort();
        assert_eq!(got, vec!["/new-dir".to_string(), "/new.txt".to_string()],
            "pending files and pending directories, not synced/pending_delete rows");
    }

    /// The "second look" rule (ADR-0063): a `From`/`To` pair straddling a
    /// cycle boundary must not be read as a deletion on the very first
    /// resolve pass that sees no partner yet.
    #[test]
    fn mark_disappeared_missed_sets_the_seen_flag_without_changing_status() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 10, 100, b"h1", "synced", None).unwrap();
        db.mark_disappeared("r1", "/a.txt").unwrap();

        db.mark_disappeared_missed("r1", "/a.txt").unwrap();

        let got = db.get_file("r1", "/a.txt").unwrap().unwrap();
        assert_eq!(got.sync_status, "disappeared", "still open for one more cycle's join");
        assert!(got.disappeared_seen);
    }

    #[test]
    fn list_immediate_children_scopes_to_one_directory_level() {
        let db = test_db();
        db.upsert_file("r1", "/top.txt", 1, 1, b"h", "synced", None).unwrap();       // root-level, not under /foo
        db.upsert_file("r1", "/foo/a.txt", 1, 1, b"h", "synced", None).unwrap();     // immediate child of /foo
        db.upsert_dir("r1", "/foo/sub", "synced").unwrap();                         // immediate child of /foo
        db.upsert_file("r1", "/foo/sub/deep.txt", 1, 1, b"h", "synced", None).unwrap(); // grandchild, must be excluded
        db.upsert_file("r1", "/foobar.txt", 1, 1, b"h", "synced", None).unwrap();    // sibling with shared prefix, must be excluded

        let mut got = db.list_immediate_children("r1", "/foo").unwrap();
        got.sort();
        assert_eq!(got, vec!["/foo/a.txt".to_string(), "/foo/sub".to_string()]);
    }

    #[test]
    fn list_immediate_children_at_root_excludes_pending_delete_and_nested() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "synced", None).unwrap();
        db.upsert_file("r1", "/gone.txt", 0, 0, &[], "pending_delete", None).unwrap();
        db.upsert_file("r1", "/dir/nested.txt", 1, 1, b"h", "synced", None).unwrap();

        let got = db.list_immediate_children("r1", "").unwrap();
        assert_eq!(got, vec!["/a.txt".to_string()],
            "pending_delete and nested-under-/dir rows must not appear at root scope");
    }

    /// A `disappeared` row must be invisible to the walk's deletion check
    /// (ADR-0063): a walk racing the yet-unresolved rename must not decide
    /// the on-disk absence at the old path is a real deletion and overwrite
    /// the row's identity/hash -- exactly what the next resolve pass needs.
    #[test]
    fn list_immediate_children_excludes_disappeared() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "synced", None).unwrap();
        db.upsert_file("r1", "/moved-away.txt", 10, 100, b"h1", "synced", Some(b"h1")).unwrap();
        db.set_file_identity("r1", "/moved-away.txt", b"id-1").unwrap();
        db.mark_disappeared("r1", "/moved-away.txt").unwrap();

        let got = db.list_immediate_children("r1", "").unwrap();
        assert_eq!(got, vec!["/a.txt".to_string()],
            "a disappeared row must not be re-examined by the walk while it awaits resolution");
    }

    /// A directory name containing literal `%`/`_` must not be misread as a
    /// SQL LIKE wildcard -- `_` in particular ("any one character") is a
    /// footgun since it's an extremely common filename character. Without
    /// escaping, this directory's own children would be wrongly excluded by
    /// the deeper-pattern NOT LIKE check.
    #[test]
    fn list_immediate_children_escapes_percent_and_underscore_in_the_directory_name() {
        let db = test_db();
        db.upsert_file("r1", "/50%_off/a.txt", 1, 1, b"h", "synced", None).unwrap();
        db.upsert_file("r1", "/50X_off/a.txt", 1, 1, b"h", "synced", None).unwrap(); // would match an unescaped "50%_off" pattern

        let got = db.list_immediate_children("r1", "/50%_off").unwrap();
        assert_eq!(got, vec!["/50%_off/a.txt".to_string()],
            "literal % and _ in the directory name must be matched literally, not as wildcards");
    }

    /// Regression for a bug an advisor-agent review caught before this
    /// shipped: SQLite's LIKE is case-insensitive for ASCII by default
    /// regardless of column collation. Without `PRAGMA case_sensitive_like
    /// = ON` (LocalDb::open), a directory-scoped query for "/Foo" on a
    /// case-sensitive filesystem (Linux) would also match rows actually
    /// belonging to a sibling "/foo" -- not a deletion-safety risk (the
    /// per-path `exists()` re-stat guard still protects the real target),
    /// but it breaks the "scoped to exactly this directory" invariant this
    /// whole design exists for.
    #[test]
    fn list_immediate_children_is_case_sensitive() {
        let db = test_db();
        db.upsert_file("r1", "/Foo/a.txt", 1, 1, b"h", "synced", None).unwrap();
        db.upsert_file("r1", "/foo/b.txt", 1, 1, b"h", "synced", None).unwrap();

        let got = db.list_immediate_children("r1", "/Foo").unwrap();
        assert_eq!(got, vec!["/Foo/a.txt".to_string()],
            "a case-sensitive filesystem's /Foo and /foo must not share one scope");
    }

    #[test]
    fn upsert_dir_stages_an_empty_directory_row() {
        let db = test_db();
        db.upsert_dir("r1", "/sub", "pending").unwrap();

        let got = db.get_file("r1", "/sub").unwrap().unwrap();
        assert!(got.is_dir);
        assert_eq!(got.size, 0);
        assert_eq!(got.sync_status, "pending");
    }

    #[test]
    fn upsert_dir_on_existing_key_flips_is_dir_and_keeps_updating_status() {
        let db = test_db();
        db.upsert_file("r1", "/sub", 5, 5, b"h", "pending", None).unwrap();
        db.upsert_dir("r1", "/sub", "synced").unwrap();

        let got = db.get_file("r1", "/sub").unwrap().unwrap();
        assert!(got.is_dir);
        assert_eq!(got.sync_status, "synced");
    }

    #[test]
    fn list_pending_batch_includes_pending_and_pending_delete_and_due_failed() {
        let db = test_db();
        let now = chrono::Utc::now().timestamp();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending", None).unwrap();
        db.upsert_file("r1", "/b.txt", 1, 1, b"h", "pending_delete", None).unwrap();
        db.upsert_file("r1", "/c.txt", 1, 1, b"h", "synced", None).unwrap();
        db.upsert_file("r1", "/due.txt", 1, 1, b"h", "pending_failed", None).unwrap();
        db.set_failed("r1", "/due.txt", "boom", now - 10).unwrap();
        db.upsert_file("r1", "/future.txt", 1, 1, b"h", "pending_failed", None).unwrap();
        db.set_failed("r1", "/future.txt", "boom", now + 10_000).unwrap();

        let mut paths: Vec<String> = db.list_pending_batch("r1", 10).unwrap().into_iter().map(|f| f.path).collect();
        paths.sort();
        assert_eq!(paths, vec!["/a.txt".to_string(), "/b.txt".to_string(), "/due.txt".to_string()]);
    }

    #[test]
    fn list_pending_batch_includes_due_blocked_remote_quota() {
        let db = test_db();
        let now = chrono::Utc::now().timestamp();
        db.upsert_file("r1", "/quota.txt", 1, 1, b"h", "blocked_remote_quota", None).unwrap();
        db.set_blocked_remote_quota("r1", "/quota.txt", now - 1).unwrap();

        let paths: Vec<String> = db.list_pending_batch("r1", 10).unwrap().into_iter().map(|f| f.path).collect();
        assert_eq!(paths, vec!["/quota.txt".to_string()]);
    }

    #[test]
    fn list_pending_all_excludes_paused_roots() {
        let db = test_db();
        db.upsert_sync_root("r1", "Active", "/r1", "srv1", "eager", false, super::super::ROOT_KIND_OWNED).unwrap();
        db.upsert_sync_root("r2", "Paused", "/r2", "srv2", "eager", false, super::super::ROOT_KIND_OWNED).unwrap();
        db.set_sync_root_paused("r2", true).unwrap();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending", None).unwrap();
        db.upsert_file("r2", "/b.txt", 1, 1, b"h", "pending", None).unwrap();

        let paths: Vec<String> = db.list_pending_all().unwrap().into_iter().map(|f| f.path).collect();
        assert_eq!(paths, vec!["/a.txt".to_string()], "rows from a paused root must not appear");
    }

    // list_pending_all deliberately excludes pending_failed/blocked_remote_quota
    // (see its own doc comment) — this is the dedicated query the failures UI
    // must use instead. Regression guard for the bug where list_sync_failures
    // filtered list_pending_all's results and so never found anything.
    #[test]
    fn list_all_local_failures_finds_rows_list_pending_all_deliberately_excludes() {
        let db = test_db();
        db.upsert_sync_root("r1", "Active", "/r1", "srv1", "eager", false, super::super::ROOT_KIND_OWNED).unwrap();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending", None).unwrap();
        db.upsert_file("r1", "/b.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_failed("r1", "/b.txt", "boom", 999).unwrap();
        db.upsert_file("r1", "/c.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_blocked_remote_quota("r1", "/c.txt", 888).unwrap();

        let pending_all: Vec<String> = db.list_pending_all().unwrap().into_iter().map(|f| f.path).collect();
        assert_eq!(pending_all, vec!["/a.txt".to_string()],
            "list_pending_all must still exclude both failure statuses");

        let mut failures: Vec<String> = db.list_all_local_failures().unwrap().into_iter().map(|f| f.path).collect();
        failures.sort();
        assert_eq!(failures, vec!["/b.txt".to_string(), "/c.txt".to_string()]);
    }

    // A paused root must not hide an existing failure from the user — only
    // list_pending_all's "active work" count excludes paused roots.
    #[test]
    fn list_all_local_failures_includes_paused_roots() {
        let db = test_db();
        db.upsert_sync_root("r1", "Paused", "/r1", "srv1", "eager", false, super::super::ROOT_KIND_OWNED).unwrap();
        db.set_sync_root_paused("r1", true).unwrap();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_failed("r1", "/a.txt", "boom", 999).unwrap();

        let paths: Vec<String> = db.list_all_local_failures().unwrap().into_iter().map(|f| f.path).collect();
        assert_eq!(paths, vec!["/a.txt".to_string()]);
    }

    // Manual-only rows (backoff schedule exhausted, next_retry_at = NULL)
    // sort after due-soonest rows instead of unpredictably.
    #[test]
    fn list_all_local_failures_orders_due_soonest_first_manual_only_last() {
        let db = test_db();
        db.upsert_file("r1", "/manual.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_failed("r1", "/manual.txt", "boom", i64::MAX).unwrap();
        // set_failed's next_retry_at is caller-supplied; simulate the real
        // "manual-only" case (see retry::compute_next_retry) with a NULL.
        db.conn.lock().unwrap().execute(
            "UPDATE local_files SET next_retry_at = NULL WHERE path = '/manual.txt'", [],
        ).unwrap();
        db.upsert_file("r1", "/soon.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_failed("r1", "/soon.txt", "boom", 100).unwrap();

        let paths: Vec<String> = db.list_all_local_failures().unwrap().into_iter().map(|f| f.path).collect();
        assert_eq!(paths, vec!["/soon.txt".to_string(), "/manual.txt".to_string()]);
    }

    #[test]
    fn list_all_files_returns_every_status() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "synced", None).unwrap();
        db.upsert_file("r1", "/b.txt", 1, 1, b"h", "pending", None).unwrap();
        db.upsert_file("r2", "/c.txt", 1, 1, b"h", "pending", None).unwrap();

        let files = db.list_all_files("r1").unwrap();
        assert_eq!(files.len(), 2, "must only return rows for the requested root");
    }

    #[test]
    fn file_statuses_in_dir_is_scoped_to_one_directory() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt",           1, 1, b"h", "synced",  None).unwrap();
        db.upsert_file("r1", "/docs/b.txt",      1, 1, b"h", "pending", None).unwrap();
        db.upsert_file("r1", "/docs/deep/c.txt", 1, 1, b"h", "synced",  None).unwrap();
        db.upsert_dir("r1", "/docs/deep", "synced").unwrap();
        db.upsert_file("r2", "/docs/other.txt",  1, 1, b"h", "synced",  None).unwrap();

        let root = db.file_statuses_in_dir("r1", "").unwrap();
        assert_eq!(root.len(), 1, "only the root's own files, not the subtree");
        assert_eq!(root.get("/a.txt").map(String::as_str), Some("synced"));

        let docs = db.file_statuses_in_dir("r1", "/docs").unwrap();
        assert_eq!(docs.get("/docs/b.txt").map(String::as_str), Some("pending"));
        assert!(!docs.contains_key("/docs/deep/c.txt"), "deeper levels belong to their own chunk");
        assert!(!docs.contains_key("/docs/deep"), "directory rows are not placeholder-file candidates");
    }

    /// A file staged for deletion has no server state left to point a
    /// placeholder at — converting it would put a cloud icon on a file that
    /// is about to disappear.
    #[test]
    fn file_statuses_in_dir_skips_pending_delete() {
        let db = test_db();
        db.upsert_file("r1", "/gone.txt", 1, 1, b"h", "pending_delete", None).unwrap();
        db.upsert_file("r1", "/here.txt", 1, 1, b"h", "synced",         None).unwrap();

        let rows = db.file_statuses_in_dir("r1", "").unwrap();
        assert_eq!(rows.len(), 1);
        assert!(rows.contains_key("/here.txt"));
    }

    /// `%` and `_` are ordinary filename characters and LIKE wildcards at the
    /// same time; an unescaped directory name would pull in siblings.
    #[test]
    fn file_statuses_in_dir_escapes_like_wildcards() {
        let db = test_db();
        db.upsert_file("r1", "/50%_off/a.txt", 1, 1, b"h", "synced", None).unwrap();
        db.upsert_file("r1", "/50xyoff/b.txt", 1, 1, b"h", "synced", None).unwrap();

        let rows = db.file_statuses_in_dir("r1", "/50%_off").unwrap();
        assert_eq!(rows.len(), 1);
        assert!(rows.contains_key("/50%_off/a.txt"));
    }

    #[test]
    fn list_pending_batch_caps_the_queue_and_keeps_path_order() {
        let db = test_db();
        for i in 0..5 {
            db.upsert_file("r1", &format!("/f{i}.txt"), 1, 1, b"h", "pending", None).unwrap();
        }
        db.upsert_file("r1", "/synced.txt", 1, 1, b"h", "synced", None).unwrap();

        let batch: Vec<String> = db.list_pending_batch("r1", 3).unwrap()
            .into_iter().map(|f| f.path).collect();
        assert_eq!(batch, vec!["/f0.txt", "/f1.txt", "/f2.txt"]);

        // The cap is a cap, not a filter: what it left behind is still pending.
        assert_eq!(db.list_pending_batch("r1", 100).unwrap().len(), 5);
    }

    /// The point of the priority lane (ADR-0059): a change the watcher just
    /// staged is served before the walk's queue, even when that queue sorts
    /// ahead of it by path and would fill the batch on its own.
    #[test]
    fn list_pending_batch_serves_the_priority_lane_first() {
        let db = test_db();
        for i in 0..5 {
            db.upsert_file("r1", &format!("/aaa{i}.txt"), 1, 1, b"h", "pending", None).unwrap();
        }
        db.upsert_file("r1", "/zzz-live.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_priority("r1", "/zzz-live.txt").unwrap();

        let batch: Vec<String> = db.list_pending_batch("r1", 3).unwrap()
            .into_iter().map(|f| f.path).collect();
        assert_eq!(batch, vec!["/zzz-live.txt", "/aaa0.txt", "/aaa1.txt"]);
    }

    /// Within each lane `path` order still holds, and the bulk queue only
    /// fills what the lane left of the batch.
    #[test]
    fn priority_lane_and_bulk_queue_each_keep_path_order() {
        let db = test_db();
        for name in ["/b.txt", "/d.txt", "/a.txt", "/c.txt"] {
            db.upsert_file("r1", name, 1, 1, b"h", "pending", None).unwrap();
        }
        db.set_priority("r1", "/d.txt").unwrap();
        db.set_priority("r1", "/b.txt").unwrap();

        let batch: Vec<String> = db.list_pending_batch("r1", 10).unwrap()
            .into_iter().map(|f| f.path).collect();
        assert_eq!(batch, vec!["/b.txt", "/d.txt", "/a.txt", "/c.txt"]);
    }

    /// Only the call that actually enters a row into the lane reports an
    /// arrival. This is what a running upload batch counts to decide whether
    /// to cut itself short — a file being copied in emits write events
    /// throughout, and every one of them re-stages a row that is already in
    /// the lane. Counting those would make the batch preempt itself for the
    /// whole duration of the copy.
    #[test]
    fn only_the_first_raise_of_a_row_counts_as_an_arrival() {
        let db = test_db();
        db.upsert_file("r1", "/growing.bin", 1, 1, b"h", "pending", None).unwrap();

        assert!(db.set_priority("r1", "/growing.bin").unwrap(), "entering the lane is an arrival");
        assert!(!db.set_priority("r1", "/growing.bin").unwrap(), "already in the lane — not again");
        assert!(!db.set_priority("r1", "/growing.bin").unwrap());

        // Once it syncs and changes again, it is a genuine new arrival.
        db.set_synced("r1", "/growing.bin", b"h").unwrap();
        db.upsert_file("r1", "/growing.bin", 2, 2, b"h2", "pending", None).unwrap();
        assert!(db.set_priority("r1", "/growing.bin").unwrap());
    }

    /// `set_priority` only raises rows that are actually queued. Staging has
    /// many reasons to write nothing at all, and marking a `synced` row would
    /// leave an entry in the partial index that no transition ever clears.
    #[test]
    fn set_priority_ignores_rows_that_are_not_queued() {
        let db = test_db();
        db.upsert_file("r1", "/done.txt", 1, 1, b"h", "synced",   None).unwrap();
        db.upsert_file("r1", "/open.txt", 1, 1, b"h", "conflict", None).unwrap();
        assert!(!db.set_priority("r1", "/done.txt").unwrap());
        assert!(!db.set_priority("r1", "/open.txt").unwrap());
        // A missing row must not error either — the watcher can fire for a
        // path staging decided against.
        assert!(!db.set_priority("r1", "/never-staged.txt").unwrap());

        assert!(db.list_pending_batch("r1", 10).unwrap().is_empty());
    }

    /// A retryable failure keeps its place in the lane: the live change is
    /// still the one the user is waiting for, and it is due again on the next
    /// retry window.
    #[test]
    fn priority_survives_a_retryable_failure() {
        let db = test_db();
        db.upsert_file("r1", "/bulk.txt", 1, 1, b"h", "pending", None).unwrap();
        db.upsert_file("r1", "/live.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_priority("r1", "/live.txt").unwrap();

        let past = chrono::Utc::now().timestamp() - 60;
        db.set_failed("r1", "/live.txt", "boom", past).unwrap();

        let batch: Vec<String> = db.list_pending_batch("r1", 10).unwrap()
            .into_iter().map(|f| f.path).collect();
        assert_eq!(batch, vec!["/live.txt", "/bulk.txt"]);
    }

    /// Reaching a terminal state clears the flag, so the partial index stays
    /// small instead of accumulating every path the watcher ever touched.
    #[test]
    fn terminal_transitions_clear_the_priority_flag() {
        let db = test_db();

        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_priority("r1", "/a.txt").unwrap();
        db.set_synced("r1", "/a.txt", b"h").unwrap();
        db.upsert_file("r1", "/a.txt", 2, 2, b"h2", "pending", None).unwrap();

        db.upsert_file("r1", "/b.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_priority("r1", "/b.txt").unwrap();
        db.set_synced_with_version("r1", "/b.txt", b"h", 7).unwrap();
        db.upsert_file("r1", "/b.txt", 2, 2, b"h2", "pending", None).unwrap();

        db.upsert_file("r1", "/c.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_priority("r1", "/c.txt").unwrap();
        db.set_conflict("r1", "/c.txt", "/tmp/c.conflict.txt").unwrap();
        db.resolve_conflict("r1", "/c.txt", true).unwrap();

        db.upsert_file("r1", "/d.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_priority("r1", "/d.txt").unwrap();
        db.upsert_synced_with_version("r1", "/d.txt", 2, 2, b"h2", 3).unwrap();
        db.upsert_file("r1", "/d.txt", 3, 3, b"h3", "pending", None).unwrap();

        // Nothing is left in the lane, so the batch is plain path order.
        let batch: Vec<String> = db.list_pending_batch("r1", 10).unwrap()
            .into_iter().map(|f| f.path).collect();
        assert_eq!(batch, vec!["/a.txt", "/b.txt", "/c.txt", "/d.txt"]);
    }

    #[test]
    fn has_pending_reflects_pending_and_synced_transitions() {
        let db = test_db();
        assert!(!db.has_pending("r1").unwrap());
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending", None).unwrap();
        assert!(db.has_pending("r1").unwrap());
        db.set_synced("r1", "/a.txt", b"h").unwrap();
        assert!(!db.has_pending("r1").unwrap(), "a synced root has nothing pending");
    }

    #[test]
    fn pending_summary_all_reports_conflict_and_pending_separately() {
        let db = test_db();
        db.upsert_sync_root("r1", "R1", "/local/r1", "srv-1", "eager", false, super::super::ROOT_KIND_OWNED).unwrap();
        assert_eq!(db.pending_summary_all().unwrap(), (false, false));

        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending", None).unwrap();
        assert_eq!(db.pending_summary_all().unwrap(), (false, true));

        db.upsert_file("r1", "/b.txt", 1, 1, b"h", "conflict", None).unwrap();
        assert_eq!(db.pending_summary_all().unwrap(), (true, true));
    }

    /// A paused root does not keep the tray on "syncing" — nothing in it will
    /// progress until the user resumes it. Same rule `list_pending_all` has.
    #[test]
    fn pending_summary_all_ignores_paused_roots() {
        let db = test_db();
        db.upsert_sync_root("r1", "R1", "/local/r1", "srv-1", "eager", false, super::super::ROOT_KIND_OWNED).unwrap();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_sync_root_paused("r1", true).unwrap();

        assert_eq!(db.pending_summary_all().unwrap(), (false, false));
    }

    #[test]
    fn count_pending_matches_list_pending_batch_without_the_cap() {
        let db = test_db();
        for i in 0..5 {
            db.upsert_file("r1", &format!("/f{i}.txt"), 1, 1, b"h", "pending", None).unwrap();
        }
        db.upsert_file("r1", "/done.txt", 1, 1, b"h", "synced", None).unwrap();

        // The number the progress bar counts against: the whole queue, not
        // what one capped batch happened to take.
        assert_eq!(db.count_pending("r1").unwrap(), 5);
        assert_eq!(db.list_pending_batch("r1", 2).unwrap().len(), 2);
    }

    #[test]
    fn pending_counts_all_counts_what_pending_summary_only_flags() {
        let db = test_db();
        db.upsert_sync_root("r1", "R1", "/local/r1", "srv-1", "eager", false, super::super::ROOT_KIND_OWNED).unwrap();
        assert_eq!(db.pending_counts_all().unwrap(), (0, 0));

        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending",  None).unwrap();
        db.upsert_file("r1", "/b.txt", 1, 1, b"h", "pending",  None).unwrap();
        db.upsert_file("r1", "/c.txt", 1, 1, b"h", "conflict", None).unwrap();
        // Conflicts count as pending too — same predicate as list_pending_all.
        assert_eq!(db.pending_counts_all().unwrap(), (3, 1));

        db.set_sync_root_paused("r1", true).unwrap();
        assert_eq!(db.pending_counts_all().unwrap(), (0, 0), "paused roots stay out");
    }

    /// The bulk-delete guard's two numbers. They have to come from here rather
    /// than from the upload batch, which only ever shows a slice of the wave.
    #[test]
    fn delete_and_tracked_counts_cover_the_whole_root_not_a_batch() {
        let db = test_db();
        for i in 0..10 {
            let status = if i < 6 { "pending_delete" } else { "synced" };
            db.upsert_file("r1", &format!("/f{i}.txt"), 1, 1, b"h", status, None).unwrap();
        }
        db.upsert_file("r2", "/other.txt", 1, 1, b"h", "pending_delete", None).unwrap();

        assert_eq!(db.count_tracked_files("r1").unwrap(), 10);
        assert_eq!(db.count_pending_deletes("r1").unwrap(), 6);
        // Which is what a batch of 2 would have hidden.
        assert_eq!(db.list_pending_batch("r1", 2).unwrap().len(), 2);
    }

    /// A single `pending_delete` row on a directory hides its whole subtree
    /// from the guard: `stage_delete` never cascades into the local
    /// index, so the directory counts as 1 no matter how many descendants it
    /// carries. The count has to add the descendants back in.
    #[test]
    fn count_pending_deletes_counts_directory_descendants_not_the_row() {
        let db = test_db();
        db.upsert_dir("r1", "/docs", "pending_delete").unwrap();
        db.upsert_file("r1", "/docs/a.txt", 1, 1, b"h", "synced", None).unwrap();
        db.upsert_file("r1", "/docs/b.txt", 1, 1, b"h", "synced", None).unwrap();
        db.upsert_dir("r1", "/docs/sub", "synced").unwrap();
        db.upsert_file("r1", "/docs/sub/c.txt", 1, 1, b"h", "synced", None).unwrap();
        db.upsert_file("r1", "/untouched.txt", 1, 1, b"h", "synced", None).unwrap();

        // The dir row itself (1) plus every descendant row regardless of
        // is_dir (3 files + 1 subdirectory) — not the 1 the row alone gives.
        assert_eq!(db.count_pending_deletes("r1").unwrap(), 5);
    }

    /// A `LIKE '<path>%'` scan without a separator would also match
    /// `/docsomething.txt` as a "descendant" of `/docs` — a false hit that
    /// inflates the count for a folder whose name happens to prefix a sibling.
    #[test]
    fn count_pending_deletes_respects_path_boundary_not_just_prefix() {
        let db = test_db();
        db.upsert_dir("r1", "/docs", "pending_delete").unwrap();
        db.upsert_file("r1", "/docsomething.txt", 1, 1, b"h", "synced", None).unwrap();

        assert_eq!(db.count_pending_deletes("r1").unwrap(), 1, "sibling with shared prefix must not count as a descendant");
    }

    /// `%` and `_` are ordinary filename characters but LIKE wildcards; a
    /// directory named `50%_off` must not turn into an open pattern that
    /// matches unrelated paths.
    #[test]
    fn count_pending_deletes_escapes_like_wildcards_in_directory_name() {
        let db = test_db();
        db.upsert_dir("r1", "/50%_off", "pending_delete").unwrap();
        db.upsert_file("r1", "/50%_off/a.txt", 1, 1, b"h", "synced", None).unwrap();
        // Without escaping, "_" would match any single character here too.
        db.upsert_file("r1", "/50X_offX.txt", 1, 1, b"h", "synced", None).unwrap();

        assert_eq!(db.count_pending_deletes("r1").unwrap(), 2, "only the real descendant counts");
    }

    /// Counterpart to `count_pending_deletes` for the inbound direction
    ///. Unlike the outbound guard, no directory-descendant expansion
    /// is needed: the server already cascades one tombstone per real
    /// descendant file into the changes feed, so every row that should count
    /// already gets its own `pending_incoming_delete` row — a plain COUNT.
    #[test]
    fn count_pending_incoming_deletes_counts_flagged_rows() {
        let db = test_db();
        assert_eq!(db.count_pending_incoming_deletes("r1").unwrap(), 0, "fresh root has nothing staged");

        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending_incoming_delete", None).unwrap();
        db.upsert_file("r1", "/b.txt", 1, 1, b"h", "pending_incoming_delete", None).unwrap();
        db.upsert_file("r1", "/c.txt", 1, 1, b"h", "synced", None).unwrap();

        assert_eq!(db.count_pending_incoming_deletes("r1").unwrap(), 2, "only rows flagged for incoming delete count");
    }

    /// Staging half of the inbound guard: flips a row to
    /// `pending_incoming_delete` without touching content_hash/server_hash —
    /// the flush step still needs those to know what it's removing, and
    /// nothing about staging changes what the row *was*, same care as
    /// `mark_disappeared`.
    #[test]
    fn mark_pending_incoming_delete_flips_status_only() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 5, 100, b"hash", "synced", Some(b"srv-hash")).unwrap();

        db.mark_pending_incoming_delete("r1", "/a.txt").unwrap();

        let files = db.list_all_files("r1").unwrap();
        let row = files.iter().find(|f| f.path == "/a.txt").unwrap();
        assert_eq!(row.sync_status, "pending_incoming_delete");
        assert_eq!(row.size, 5, "staging must not touch unrelated fields");
    }

    /// Flush half of the inbound guard: every row staged this cycle,
    /// for the flush step to actually remove from disk + DB once the cycle
    /// clears the guard.
    #[test]
    fn list_pending_incoming_deletes_returns_only_staged_rows() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending_incoming_delete", None).unwrap();
        db.upsert_file("r1", "/b.txt", 1, 1, b"h", "synced", None).unwrap();
        db.upsert_dir("r1", "/docs", "pending_incoming_delete").unwrap();

        let staged = db.list_pending_incoming_deletes("r1").unwrap();

        let mut paths: Vec<&str> = staged.iter().map(|f| f.path.as_str()).collect();
        paths.sort();
        assert_eq!(paths, vec!["/a.txt", "/docs"]);
    }

    /// Gate for the in-sync flag on a directory placeholder: a folder may only
    /// claim to be synced when nothing below it is still queued.
    #[test]
    fn has_unsynced_under_sees_the_whole_subtree() {
        let db = test_db();
        db.upsert_file("r1", "/docs/deep/waiting.txt", 1, 1, b"h", "pending", None).unwrap();
        db.upsert_file("r1", "/docs/done.txt",         1, 1, b"h", "synced",  None).unwrap();
        db.upsert_file("r1", "/other/done.txt",        1, 1, b"h", "synced",  None).unwrap();

        assert!(db.has_unsynced_under("r1", "/docs").unwrap(),
            "a pending file several levels down still blocks the folder");
        assert!(!db.has_unsynced_under("r1", "/other").unwrap(),
            "a fully synced subtree may claim it");
        assert!(db.has_unsynced_under("r1", "").unwrap(), "the root sees everything");

        db.set_synced("r1", "/docs/deep/waiting.txt", b"h").unwrap();
        assert!(!db.has_unsynced_under("r1", "/docs").unwrap());
    }

    #[test]
    fn list_file_paths_after_pages_by_keyset() {
        let db = test_db();
        for name in ["/a.txt", "/b.txt", "/c.txt"] {
            db.upsert_file("r1", name, 1, 1, b"h", "synced", None).unwrap();
        }
        db.upsert_dir("r1", "/adir", "synced").unwrap();
        db.upsert_file("r1", "/gone.txt", 1, 1, b"h", "pending_delete", None).unwrap();

        let first = db.list_file_paths_after("r1", "", 2).unwrap();
        assert_eq!(first, vec!["/a.txt", "/b.txt"], "directories are not hydration candidates");

        let second = db.list_file_paths_after("r1", first.last().unwrap(), 2).unwrap();
        assert_eq!(second, vec!["/c.txt"], "a row staged for deletion is not one either");

        assert!(db.list_file_paths_after("r1", second.last().unwrap(), 2).unwrap().is_empty());
    }

    #[test]
    fn get_file_by_server_path_joins_via_sync_root() {
        let db = test_db();
        db.upsert_sync_root("r1", "R1", "/local/r1", "srv-root-1", "eager", false, super::super::ROOT_KIND_OWNED)
            .unwrap();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending", None).unwrap();

        let got = db.get_file_by_server_path("srv-root-1", "/a.txt").unwrap();
        assert!(got.is_some());
        assert!(db.get_file_by_server_path("unknown-server-id", "/a.txt").unwrap().is_none());
    }

    #[test]
    fn set_synced_clears_failure_state_and_sets_server_hash() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_failed("r1", "/a.txt", "boom", 999).unwrap();

        db.set_synced("r1", "/a.txt", b"newhash").unwrap();

        let got = db.get_file("r1", "/a.txt").unwrap().unwrap();
        assert_eq!(got.sync_status, "synced");
        assert_eq!(got.server_hash, Some(b"newhash".to_vec()));
        assert_eq!(got.attempt_count, 0);
        assert!(got.last_error.is_none());
        assert!(got.next_retry_at.is_none());
    }

    #[test]
    fn upsert_synced_with_version_inserts_and_updates() {
        let db = test_db();
        db.upsert_synced_with_version("r1", "/a.txt", 10, 100, b"h", 3).unwrap();

        let got = db.get_file("r1", "/a.txt").unwrap().unwrap();
        assert_eq!(got.sync_status, "synced");
        assert_eq!(got.server_version, 3);
        assert_eq!(got.content_hash, b"h");
        assert_eq!(got.server_hash, Some(b"h".to_vec()), "hash doubles as content_hash and server_hash");

        db.upsert_synced_with_version("r1", "/a.txt", 20, 200, b"h2", 4).unwrap();
        let got = db.get_file("r1", "/a.txt").unwrap().unwrap();
        assert_eq!(got.size, 20);
        assert_eq!(got.server_version, 4);
    }

    #[test]
    fn set_synced_with_version_stores_version_and_clears_failure() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_failed("r1", "/a.txt", "boom", 999).unwrap();

        db.set_synced_with_version("r1", "/a.txt", b"srvhash", 7).unwrap();

        let got = db.get_file("r1", "/a.txt").unwrap().unwrap();
        assert_eq!(got.sync_status, "synced");
        assert_eq!(got.server_version, 7);
        assert_eq!(got.attempt_count, 0);
    }

    #[test]
    fn resolve_conflict_pending_reuploads_local_bytes() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "conflict", None).unwrap();
        db.set_conflict("r1", "/a.txt", "/copy/a.conflict.1").unwrap();

        db.resolve_conflict("r1", "/a.txt", true).unwrap();

        let got = db.get_file("r1", "/a.txt").unwrap().unwrap();
        assert_eq!(got.sync_status, "pending");
        assert_eq!(db.conflict_copy_path("r1", "/a.txt").unwrap(), None, "resolve must clear the stored copy path");
    }

    #[test]
    fn resolve_conflict_not_pending_keeps_server_version() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 1, 1, b"local-hash", "conflict", None).unwrap();
        db.set_conflict("r1", "/a.txt", "/copy/a.conflict.1").unwrap();

        db.resolve_conflict("r1", "/a.txt", false).unwrap();

        let got = db.get_file("r1", "/a.txt").unwrap().unwrap();
        assert_eq!(got.sync_status, "synced");
        assert_eq!(got.server_hash, Some(b"local-hash".to_vec()), "server_hash must be aligned with content_hash");
        assert_eq!(db.conflict_copy_path("r1", "/a.txt").unwrap(), None);
    }

    #[test]
    fn conflict_copy_path_is_none_for_missing_row_and_for_null_column() {
        let db = test_db();
        assert_eq!(db.conflict_copy_path("r1", "/missing.txt").unwrap(), None);

        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending", None).unwrap();
        assert_eq!(db.conflict_copy_path("r1", "/a.txt").unwrap(), None, "no conflict set yet -- must be None, not an error");
    }

    #[test]
    fn delete_file_record_removes_the_row() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending", None).unwrap();

        db.delete_file_record("r1", "/a.txt").unwrap();

        assert!(db.get_file("r1", "/a.txt").unwrap().is_none());
    }

    #[test]
    fn rename_file_updates_path_in_place() {
        let db = test_db();
        db.upsert_file("r1", "/old.txt", 5, 5, b"h", "synced", None).unwrap();

        db.rename_file("r1", "/old.txt", "/new.txt").unwrap();

        assert!(db.get_file("r1", "/old.txt").unwrap().is_none());
        let got = db.get_file("r1", "/new.txt").unwrap().unwrap();
        assert_eq!(got.size, 5, "rename must preserve the row's other columns");
    }

    /// A directory rename moves the whole subtree on disk and server-side in
    /// one step, so the index has to follow. Children left at the old prefix
    /// are unreachable: nothing walks a directory that no longer exists, so
    /// they are never revisited, while the files under the new path look
    /// untracked and get blind-uploaded.
    #[test]
    fn rename_file_moves_the_whole_subtree() {
        let db = test_db();
        db.upsert_dir("r1", "/dir", "synced").unwrap();
        db.upsert_file("r1", "/dir/a.txt", 1, 1, b"a", "synced", Some(b"a")).unwrap();
        db.upsert_file("r1", "/dir/deep/b.txt", 2, 2, b"b", "synced", Some(b"b")).unwrap();
        db.upsert_file("r1", "/other.txt", 3, 3, b"c", "synced", None).unwrap();

        db.rename_file("r1", "/dir", "/moved").unwrap();

        assert!(db.get_file("r1", "/dir").unwrap().is_none());
        assert!(db.get_file("r1", "/dir/a.txt").unwrap().is_none());
        assert!(db.get_file("r1", "/dir/deep/b.txt").unwrap().is_none());

        assert!(db.get_file("r1", "/moved").unwrap().unwrap().is_dir);
        assert_eq!(db.get_file("r1", "/moved/a.txt").unwrap().unwrap().size, 1);
        let deep = db.get_file("r1", "/moved/deep/b.txt").unwrap().unwrap();
        assert_eq!(deep.size, 2);
        assert_eq!(deep.server_hash, Some(b"b".to_vec()),
            "a subtree rename must carry every column, not just the path");

        assert!(db.get_file("r1", "/other.txt").unwrap().is_some(),
            "rows outside the renamed subtree must not be touched");
    }

    /// A sibling sharing the renamed directory's name as a prefix ("/dir2"
    /// next to "/dir") must not be dragged along -- the pattern is anchored
    /// on the separator, not on the bare prefix.
    #[test]
    fn rename_file_leaves_a_prefix_sharing_sibling_alone() {
        let db = test_db();
        db.upsert_file("r1", "/dir/a.txt", 1, 1, b"a", "synced", None).unwrap();
        db.upsert_file("r1", "/dir2/b.txt", 2, 2, b"b", "synced", None).unwrap();

        db.rename_file("r1", "/dir", "/moved").unwrap();

        assert!(db.get_file("r1", "/moved/a.txt").unwrap().is_some());
        assert!(db.get_file("r1", "/dir2/b.txt").unwrap().is_some(),
            "/dir2 only shares a prefix with /dir and must stay put");
    }

    /// The collision the old single-row rename could not survive: a full walk
    /// ran before the pull recognised the move and already created rows under
    /// the destination. Those are stale duplicates of the very content being
    /// moved -- the rename has to win, not abort on the UNIQUE constraint.
    #[test]
    fn rename_file_replaces_stale_rows_at_the_destination() {
        let db = test_db();
        db.upsert_file("r1", "/dir/a.txt", 1, 1, b"real", "synced", Some(b"real")).unwrap();
        // What a walk would have staged for the same content at its new path.
        db.upsert_file("r1", "/moved/a.txt", 1, 1, b"real", "pending", None).unwrap();

        db.rename_file("r1", "/dir", "/moved").unwrap();

        let got = db.get_file("r1", "/moved/a.txt").unwrap().unwrap();
        assert_eq!(got.sync_status, "synced",
            "the moved row wins over the walk's blind-upload duplicate");
        assert_eq!(got.server_hash, Some(b"real".to_vec()));
        assert!(db.get_file("r1", "/dir/a.txt").unwrap().is_none());
    }

    /// A row staged `pending_incoming_delete` (the inbound bulk-delete
    /// guard's durable hold) must not carry that flag through a rename.
    /// `list_server_file_ids` is status-blind by design -- a staged row must
    /// stay visible to rename detection, or a genuine rename of a path the
    /// guard is holding would be silently missed instead of just mishandled.
    /// Once detected, `rename_file` must clear the stale flag, or the flush
    /// step deletes the file that was just legitimately moved to its new
    /// path instead of the tombstone it was actually staged for.
    #[test]
    fn rename_file_clears_pending_incoming_delete_status() {
        let db = test_db();
        db.upsert_file("r1", "/old.txt", 5, 5, b"h", "synced", None).unwrap();
        db.set_server_file_id("r1", "/old.txt", "srv-1").unwrap();
        db.mark_pending_incoming_delete("r1", "/old.txt").unwrap();

        // Precondition the whole bug chain depends on: staging must not hide
        // the row from rename detection.
        assert_eq!(db.list_server_file_ids("r1").unwrap(),
            vec![("/old.txt".to_string(), "srv-1".to_string(), false)],
            "a staged row must stay visible to id-based rename detection");

        db.rename_file("r1", "/old.txt", "/new.txt").unwrap();

        let got = db.get_file("r1", "/new.txt").unwrap().unwrap();
        assert_eq!(got.sync_status, "synced",
            "a renamed row was not actually deleted -- the stale staging flag must not survive the move");
        assert!(db.list_pending_incoming_deletes("r1").unwrap().is_empty(),
            "the flush step must not find anything to remove at the new path");
    }

    /// Literal `%` and `_` in a directory name are LIKE wildcards. Without
    /// escaping, renaming "/50%_off" would also rewrite unrelated rows.
    #[test]
    fn rename_file_escapes_percent_and_underscore_in_the_path() {
        let db = test_db();
        db.upsert_file("r1", "/50%_off/a.txt", 1, 1, b"a", "synced", None).unwrap();
        db.upsert_file("r1", "/50X_off/b.txt", 2, 2, b"b", "synced", None).unwrap();

        db.rename_file("r1", "/50%_off", "/sale").unwrap();

        assert!(db.get_file("r1", "/sale/a.txt").unwrap().is_some());
        assert!(db.get_file("r1", "/50X_off/b.txt").unwrap().is_some(),
            "literal % and _ must be matched literally, not as wildcards");
    }

    /// Everything the recursive removal would destroy and that exists
    /// nowhere else — at any depth, in every not-yet-uploaded state.
    #[test]
    fn list_unsynced_under_finds_every_state_at_every_depth() {
        let db = test_db();
        db.upsert_file("r1", "/dir/queued.txt",        1, 1, b"a", "pending",              None).unwrap();
        db.upsert_file("r1", "/dir/deep/stuck.txt",    1, 1, b"b", "pending_failed",       None).unwrap();
        db.upsert_file("r1", "/dir/blocked.txt",       1, 1, b"c", "blocked_remote_quota", None).unwrap();
        db.upsert_file("r1", "/dir/open.txt",          1, 1, b"d", "conflict",             None).unwrap();
        db.upsert_file("r1", "/dir/done.txt",          1, 1, b"e", "synced",               None).unwrap();
        db.upsert_file("r1", "/dir/removed.txt",       1, 1, b"f", "pending_delete",       None).unwrap();
        db.upsert_dir("r1", "/dir/deep", "pending").unwrap();
        db.upsert_file("r1", "/elsewhere/queued.txt",  1, 1, b"g", "pending",              None).unwrap();

        let got = db.list_unsynced_under("r1", "/dir").unwrap();

        assert_eq!(got, vec![
            "/dir/blocked.txt".to_string(),
            "/dir/deep/stuck.txt".to_string(),
            "/dir/open.txt".to_string(),
            "/dir/queued.txt".to_string(),
        ]);
    }

    /// A directory row carries no bytes of its own, and a pending_delete row
    /// is asking to be gone — neither is work to rescue.
    #[test]
    fn list_unsynced_under_is_empty_when_everything_is_settled() {
        let db = test_db();
        db.upsert_file("r1", "/dir/done.txt", 1, 1, b"a", "synced", None).unwrap();
        db.upsert_dir("r1", "/dir/sub", "pending").unwrap();

        assert!(db.list_unsynced_under("r1", "/dir").unwrap().is_empty());
    }

    /// "/dir2" only shares a prefix with "/dir" and must not be swept in.
    #[test]
    fn list_unsynced_under_ignores_a_prefix_sharing_sibling() {
        let db = test_db();
        db.upsert_file("r1", "/dir2/queued.txt", 1, 1, b"a", "pending", None).unwrap();

        assert!(db.list_unsynced_under("r1", "/dir").unwrap().is_empty());
    }


    #[test]
    fn set_and_list_server_file_ids_skips_rows_without_one() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "synced", None).unwrap();
        db.upsert_file("r1", "/b.txt", 1, 1, b"h", "synced", None).unwrap();
        db.upsert_dir("r1", "/sub", "synced").unwrap();
        db.set_server_file_id("r1", "/a.txt", "id-a").unwrap();
        db.set_server_file_id("r1", "/sub", "id-sub").unwrap();

        let mut ids = db.list_server_file_ids("r1").unwrap();
        ids.sort();
        assert_eq!(
            ids,
            vec![
                ("/a.txt".to_string(), "id-a".to_string(), false),
                ("/sub".to_string(), "id-sub".to_string(), true),
            ],
            "/b.txt has no server_file_id and must be skipped"
        );
    }

    #[test]
    fn set_failed_increments_attempt_count_and_schedules_retry() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending", None).unwrap();

        db.set_failed("r1", "/a.txt", "first failure", 100).unwrap();
        let got = db.get_file("r1", "/a.txt").unwrap().unwrap();
        assert_eq!(got.sync_status, "pending_failed");
        assert_eq!(got.attempt_count, 1);
        assert_eq!(got.last_error, Some("first failure".to_string()));
        assert_eq!(got.next_retry_at, Some(100));

        db.set_failed("r1", "/a.txt", "second failure", 200).unwrap();
        let got = db.get_file("r1", "/a.txt").unwrap().unwrap();
        assert_eq!(got.attempt_count, 2, "each set_failed call must increment, not reset");
        assert_eq!(got.last_error, Some("second failure".to_string()));
    }

    #[test]
    fn set_blocked_remote_quota_leaves_attempt_count_untouched() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_failed("r1", "/a.txt", "e", 50).unwrap();

        db.set_blocked_remote_quota("r1", "/a.txt", 999).unwrap();

        let got = db.get_file("r1", "/a.txt").unwrap().unwrap();
        assert_eq!(got.sync_status, "blocked_remote_quota");
        assert_eq!(got.next_retry_at, Some(999));
        assert_eq!(got.attempt_count, 1, "quota-block is not a client failure -- attempt_count must not bump");
    }

    // ADR-0077: waiting on another program to close the file is not a client
    // failure, and only the first deferral counts as entering the state.
    #[test]
    fn set_in_use_leaves_attempt_count_untouched_and_reports_entry_once() {
        let db = test_db();
        db.upsert_file("r1", "/a.accdb", 1, 1, b"h", "pending", None).unwrap();
        db.set_failed("r1", "/a.accdb", "e", 50).unwrap();

        assert!(db.set_in_use("r1", "/a.accdb", "offen", 999).unwrap(), "first deferral enters the state");
        assert!(!db.set_in_use("r1", "/a.accdb", "offen", 1059).unwrap(), "a retry is no new entry");

        let got = db.get_file("r1", "/a.accdb").unwrap().unwrap();
        assert_eq!(got.sync_status, "in_use");
        assert_eq!(got.next_retry_at, Some(1059));
        assert_eq!(got.last_error.as_deref(), Some("offen"));
        assert_eq!(got.attempt_count, 1, "in_use is not a client failure -- attempt_count must not bump");
    }

    #[test]
    fn in_use_row_is_retried_when_due_and_listed_as_problem_but_not_as_pending() {
        let db = test_db();
        let now = chrono::Utc::now().timestamp();
        db.upsert_sync_root("r1", "Active", "/r1", "srv1", "eager", false, super::super::ROOT_KIND_OWNED).unwrap();
        db.upsert_file("r1", "/due.accdb", 1, 1, b"h", "pending", None).unwrap();
        db.set_in_use("r1", "/due.accdb", "offen", now - 1).unwrap();
        db.upsert_file("r1", "/later.accdb", 1, 1, b"h", "pending", None).unwrap();
        db.set_in_use("r1", "/later.accdb", "offen", now + 10_000).unwrap();

        let batch: Vec<String> = db.list_pending_batch("r1", 10).unwrap().into_iter().map(|f| f.path).collect();
        assert_eq!(batch, vec!["/due.accdb".to_string()]);

        assert!(db.list_pending_all().unwrap().is_empty(),
            "tray status must not stay on syncing while a file waits to be closed");

        let mut problems: Vec<String> = db.list_all_local_failures().unwrap().into_iter().map(|f| f.path).collect();
        problems.sort();
        assert_eq!(problems, vec!["/due.accdb".to_string(), "/later.accdb".to_string()]);
    }

    #[test]
    fn reset_failed_clears_failure_state_back_to_pending() {
        let db = test_db();
        db.upsert_file("r1", "/a.txt", 1, 1, b"h", "pending", None).unwrap();
        db.set_failed("r1", "/a.txt", "boom", 999).unwrap();

        db.reset_failed("r1", "/a.txt").unwrap();

        let got = db.get_file("r1", "/a.txt").unwrap().unwrap();
        assert_eq!(got.sync_status, "pending");
        assert_eq!(got.attempt_count, 0);
        assert!(got.last_error.is_none());
        assert!(got.next_retry_at.is_none());
    }
}
