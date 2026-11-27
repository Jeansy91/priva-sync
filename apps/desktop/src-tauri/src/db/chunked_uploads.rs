use std::time::{Duration, SystemTime};

use super::LocalDb;
use anyhow::Result;
use rusqlite::{params, OptionalExtension};

/// Durable metadata needed to resume a chunked upload from the exact bytes
/// already sent to the server.
#[derive(Debug, Clone)]
pub struct ChunkedUploadSession {
    pub upload_id: String,
    pub snapshot_id: String,
    pub content_hash: Vec<u8>,
    pub file_size: u64,
    pub mtime: SystemTime,
}

impl LocalDb {
    /// Return the resumable session for this file, if it references a managed
    /// snapshot. The caller verifies the source and snapshot hashes before use.
    pub fn chunked_upload_for(
        &self,
        root_server_id: &str,
        server_path: &str,
    ) -> Result<Option<ChunkedUploadSession>> {
        let conn = self.conn.lock().unwrap();
        let found = conn
            .query_row(
                "SELECT upload_id, snapshot_id, content_hash, file_size, mtime_unix, mtime_nanos
                   FROM chunked_uploads
                  WHERE root_server_id = ?1 AND server_path = ?2
                    AND snapshot_id IS NOT NULL AND content_hash IS NOT NULL",
                params![root_server_id, server_path],
                |row| {
                    let seconds = row.get::<_, i64>(4)?;
                    let nanos = row.get::<_, u32>(5)?.min(999_999_999);
                    Ok(ChunkedUploadSession {
                        upload_id: row.get(0)?,
                        snapshot_id: row.get(1)?,
                        content_hash: row.get(2)?,
                        file_size: row.get::<_, i64>(3)?.max(0) as u64,
                        mtime: SystemTime::UNIX_EPOCH
                            + Duration::from_secs(seconds.max(0) as u64)
                            + Duration::from_nanos(nanos as u64),
                    })
                },
            )
            .optional()?;
        Ok(found)
    }

    /// IDs referenced by the ledger, used to retain only snapshots that may
    /// still have an open server-side upload session during startup cleanup.
    pub fn chunked_upload_snapshot_ids(&self) -> Result<Vec<String>> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "DELETE FROM chunked_uploads
              WHERE NOT EXISTS (
                  SELECT 1 FROM sync_roots r
                  JOIN local_files f ON f.sync_root_id = r.id
                   WHERE r.server_root_id = chunked_uploads.root_server_id
                     AND f.path = chunked_uploads.server_path
                     AND f.is_dir = 0
                     AND f.sync_status IN ('pending', 'pending_failed', 'blocked_remote_quota', 'in_use')
              )",
            [],
        )?;
        let mut stmt =
            conn.prepare("SELECT snapshot_id FROM chunked_uploads WHERE snapshot_id IS NOT NULL")?;
        let rows = stmt.query_map([], |row| row.get(0))?;
        Ok(rows.collect::<rusqlite::Result<Vec<_>>>()?)
    }

    /// Record the session before its first chunk goes out, so an interrupted
    /// upload can resume using the snapshot whose content hash was recorded.
    pub fn remember_chunked_upload(
        &self,
        root_server_id: &str,
        server_path: &str,
        upload_id: &str,
        metadata: crate::sync::chunked::ChunkedSessionMetadata<'_>,
    ) -> Result<()> {
        let since_epoch = metadata
            .mtime
            .duration_since(SystemTime::UNIX_EPOCH)
            .unwrap_or_default();
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "INSERT INTO chunked_uploads
                 (root_server_id, server_path, upload_id, file_size, mtime_unix,
                  snapshot_id, content_hash, mtime_nanos)
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)
             ON CONFLICT(root_server_id, server_path) DO UPDATE SET
                 upload_id    = excluded.upload_id,
                 file_size    = excluded.file_size,
                 mtime_unix   = excluded.mtime_unix,
                 snapshot_id  = excluded.snapshot_id,
                 content_hash = excluded.content_hash,
                 mtime_nanos  = excluded.mtime_nanos",
            params![
                root_server_id,
                server_path,
                upload_id,
                metadata.file_size as i64,
                since_epoch.as_secs() as i64,
                metadata.snapshot_id,
                metadata.content_hash,
                since_epoch.subsec_nanos() as i64
            ],
        )?;
        Ok(())
    }

    /// Drop the session — it completed, or it is no longer resumable.
    pub fn forget_chunked_upload(&self, root_server_id: &str, server_path: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "DELETE FROM chunked_uploads WHERE root_server_id = ?1 AND server_path = ?2",
            params![root_server_id, server_path],
        )?;
        Ok(())
    }
}

/// Ledger access as the chunked upload path needs it. Failures here only cost
/// a re-upload, so they are logged and swallowed rather than failing a
/// transfer that is otherwise fine.
impl crate::sync::chunked::ChunkedSessionStore for LocalDb {
    fn remember(
        &self,
        root_server_id: &str,
        server_path: &str,
        upload_id: &str,
        metadata: crate::sync::chunked::ChunkedSessionMetadata<'_>,
    ) {
        if let Err(e) =
            self.remember_chunked_upload(root_server_id, server_path, upload_id, metadata)
        {
            tracing::warn!("chunked upload ledger: remember failed for {server_path}: {e}");
        }
    }

    fn forget(&self, root_server_id: &str, server_path: &str) {
        if let Err(e) = self.forget_chunked_upload(root_server_id, server_path) {
            tracing::warn!("chunked upload ledger: forget failed for {server_path}: {e}");
        }
    }
}

#[cfg(test)]
mod tests {
    use super::super::LocalDb;
    use std::time::{Duration, SystemTime};

    fn test_db() -> (tempfile::TempDir, LocalDb) {
        let tmp = tempfile::tempdir().unwrap();
        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();
        (tmp, db)
    }

    fn remember(db: &LocalDb, upload_id: &str, snapshot_id: &str, hash: &[u8]) {
        db.remember_chunked_upload(
            "r1",
            "/mod.pak",
            upload_id,
            crate::sync::chunked::ChunkedSessionMetadata {
                file_size: 4096,
                mtime: SystemTime::UNIX_EPOCH + Duration::new(1700, 123_456_789),
                snapshot_id,
                content_hash: hash,
            },
        )
        .unwrap();
    }

    #[test]
    fn remembered_session_returns_snapshot_metadata_and_exact_mtime() {
        let (_tmp, db) = test_db();
        let hash = blake3::hash(b"database").as_bytes().to_vec();
        remember(&db, "u-1", "snapshot-1", &hash);

        let got = db.chunked_upload_for("r1", "/mod.pak").unwrap().unwrap();
        assert_eq!(got.upload_id, "u-1");
        assert_eq!(got.snapshot_id, "snapshot-1");
        assert_eq!(got.content_hash, hash);
        assert_eq!(got.file_size, 4096);
        assert_eq!(
            got.mtime,
            SystemTime::UNIX_EPOCH + Duration::new(1700, 123_456_789)
        );
    }

    #[test]
    fn reopening_replaces_the_previous_session() {
        let (_tmp, db) = test_db();
        remember(&db, "u-1", "snapshot-1", b"first");
        remember(&db, "u-2", "snapshot-2", b"second");

        let got = db.chunked_upload_for("r1", "/mod.pak").unwrap().unwrap();
        assert_eq!(got.upload_id, "u-2");
        assert_eq!(got.snapshot_id, "snapshot-2");
        assert_eq!(got.content_hash, b"second");
    }

    #[test]
    fn snapshot_ids_are_listed_for_startup_cleanup() {
        let (_tmp, db) = test_db();
        db.upsert_sync_root("local", "root", "test-root", "r1", "on_demand", false, "personal").unwrap();
        db.upsert_file("local", "/mod.pak", 4096, 1700, b"hash", "pending", None).unwrap();
        remember(&db, "u-1", "snapshot-1", b"hash");

        assert_eq!(
            db.chunked_upload_snapshot_ids().unwrap(),
            vec!["snapshot-1".to_string()]
        );
        db.set_synced("local", "/mod.pak", b"hash").unwrap();
        assert!(db.chunked_upload_snapshot_ids().unwrap().is_empty());
        assert!(db.chunked_upload_for("r1", "/mod.pak").unwrap().is_none());
    }

    #[test]
    fn forgetting_removes_the_session() {
        let (_tmp, db) = test_db();
        remember(&db, "u-1", "snapshot-1", b"hash");
        db.forget_chunked_upload("r1", "/mod.pak").unwrap();

        assert!(db.chunked_upload_for("r1", "/mod.pak").unwrap().is_none());
    }
}
