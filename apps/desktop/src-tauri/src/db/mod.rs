use anyhow::Result;
use rusqlite::{params, Connection, Row};
use std::path::Path;
use tracing::info;

mod config;
pub use config::DEFAULT_IGNORE_PATTERNS;
mod chunked_uploads;
mod failed_changes;
mod files;
mod sync_roots;

/// Local SQLite database holding the desktop client's sync state.
pub struct LocalDb {
    pub conn: std::sync::Mutex<Connection>,
}

// ─── Migrations ───────────────────────────────────────────────────────────────

struct Migration {
    version: i64,
    description: &'static str,
    sql: &'static str,
}

/// All schema migrations in order.
/// Rules:
///   - Never edit an existing migration — add a new one.
///   - Migrations must be idempotent where possible (use IF NOT EXISTS / IF EXISTS).
///   - ALTER TABLE ... ADD COLUMN is NOT idempotent, but is safe because the
///     migration runner only executes each version once.
const MIGRATIONS: &[Migration] = &[
    Migration {
        version: 1,
        description: "initial schema",
        sql: "
            PRAGMA journal_mode = WAL;
            PRAGMA foreign_keys = ON;

            CREATE TABLE IF NOT EXISTS config (
                key   TEXT PRIMARY KEY,
                value TEXT NOT NULL
            );

            CREATE TABLE IF NOT EXISTS sync_roots (
                id             TEXT PRIMARY KEY,
                name           TEXT NOT NULL,
                local_path     TEXT NOT NULL UNIQUE,
                server_root_id TEXT NOT NULL
            );

            CREATE TABLE IF NOT EXISTS local_files (
                sync_root_id TEXT    NOT NULL,
                path         TEXT    NOT NULL,
                size         INTEGER NOT NULL DEFAULT 0,
                mtime        INTEGER NOT NULL DEFAULT 0,
                content_hash BLOB,
                sync_status  TEXT    NOT NULL DEFAULT 'pending',
                server_hash  BLOB,
                updated_at   INTEGER NOT NULL DEFAULT (unixepoch()),
                PRIMARY KEY (sync_root_id, path)
            );

            CREATE INDEX IF NOT EXISTS idx_local_files_status
                ON local_files(sync_root_id, sync_status);
        ",
    },
    Migration {
        version: 2,
        description: "add download_mode to sync_roots",
        sql: "ALTER TABLE sync_roots ADD COLUMN download_mode TEXT NOT NULL DEFAULT 'on_demand'",
    },
    Migration {
        version: 3,
        description: "add read_only to sync_roots",
        sql: "ALTER TABLE sync_roots ADD COLUMN read_only INTEGER NOT NULL DEFAULT 0",
    },
    Migration {
        version: 4,
        description: "add server_version to local_files for ETag-based conflict detection",
        sql: "ALTER TABLE local_files ADD COLUMN server_version INTEGER NOT NULL DEFAULT -1",
    },
    Migration {
        version: 5,
        description: "add permissions to local_files for platform-specific permission sync",
        sql: "ALTER TABLE local_files ADD COLUMN permissions TEXT",
    },
    Migration {
        version: 6,
        description: "drop permissions column — permission sync removed",
        sql: "ALTER TABLE local_files DROP COLUMN permissions",
    },
    Migration {
        version: 7,
        description: "add failure tracking columns to local_files",
        sql: "
            ALTER TABLE local_files ADD COLUMN last_error    TEXT;
            ALTER TABLE local_files ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0;
            ALTER TABLE local_files ADD COLUMN next_retry_at INTEGER;
        ",
    },
    Migration {
        version: 8,
        description: "table for failed server-change application attempts",
        sql: "
            CREATE TABLE IF NOT EXISTS failed_server_changes (
                id              INTEGER PRIMARY KEY AUTOINCREMENT,
                sync_root_id    TEXT    NOT NULL,
                server_path     TEXT    NOT NULL,
                payload         TEXT    NOT NULL,
                last_error      TEXT    NOT NULL DEFAULT '',
                attempt_count   INTEGER NOT NULL DEFAULT 0,
                next_retry_at   INTEGER NOT NULL DEFAULT 0,
                created_at      INTEGER NOT NULL DEFAULT (unixepoch()),
                UNIQUE(sync_root_id, server_path)
            );
            CREATE INDEX IF NOT EXISTS idx_failed_server_changes_due
                ON failed_server_changes(next_retry_at);
        ",
    },
    Migration {
        version: 9,
        description: "add paused to sync_roots",
        sql: "ALTER TABLE sync_roots ADD COLUMN paused INTEGER NOT NULL DEFAULT 0",
    },
    Migration {
        version: 10,
        description: "add is_dir to local_files for empty-directory sync",
        sql: "ALTER TABLE local_files ADD COLUMN is_dir INTEGER NOT NULL DEFAULT 0",
    },
    Migration {
        version: 11,
        description: "add kind to sync_roots for Shared Mounts (ADR-0011)",
        // 'owned' = own server sync root, 'mount' = activated Shared Mount
        // whose server_root_id carries the share_id instead of a root id.
        sql: "ALTER TABLE sync_roots ADD COLUMN kind TEXT NOT NULL DEFAULT 'owned'",
    },
    Migration {
        version: 12,
        description: "store the on-disk conflict copy path so resolve can act on it (ADR-0003)",
        // Absolute path of the `.conflict.<ts>` copy the reconcile/upload path
        // saved next to the file. NULL for non-conflict rows.
        sql: "ALTER TABLE local_files ADD COLUMN conflict_copy_path TEXT",
    },
    Migration {
        version: 13,
        description: "track server_file_id on local_files for id-based rename detection",
        // The server's files.id for this row. Lets the pull recognise a
        // server-side rename by identity (same id, new path) and move the
        // local file instead of download-new + delete-old. NULL until the
        // next pull backfills it for rows written before this migration.
        sql: "ALTER TABLE local_files ADD COLUMN server_file_id TEXT",
    },
    // Future migrations go here.  Never edit the entries above — add new ones.
    Migration {
        version: 14,
        description: "remember open chunked upload sessions so a retry resumes instead of re-sending",
        // Keyed by the file being uploaded. file_size + mtime_unix are the
        // guard: a session may only be resumed while the local file still
        // looks the way it did when the session was opened.
        sql: "
            CREATE TABLE IF NOT EXISTS chunked_uploads (
                root_server_id TEXT    NOT NULL,
                server_path    TEXT    NOT NULL,
                upload_id      TEXT    NOT NULL,
                file_size      INTEGER NOT NULL,
                mtime_unix     INTEGER NOT NULL,
                PRIMARY KEY (root_server_id, server_path)
            )
        ",
    },
    Migration {
        version: 15,
        description: "priority lane so watcher-staged changes overtake the walk queue (ADR-0059)",
        // Two lanes: 1 = a change the live watcher registered, 0 = the walk's
        // backlog.
        //
        // The partial index is the point: only rows currently in the lane are
        // indexed, so the lane query stays a small index scan in `path` order
        // even while a full walk has staged millions of ordinary pending rows
        // — and lane 0 needs no index at all, it rides the primary key exactly
        // as the unprioritised query did. The index collapses back to (near)
        // empty as the rows sync, because `priority` is cleared on the
        // terminal transitions.
        sql: "
            ALTER TABLE local_files ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;
            CREATE INDEX IF NOT EXISTS idx_local_files_priority
                ON local_files(sync_root_id, path) WHERE priority = 1;
        ",
    },
    Migration {
        version: 16,
        description: "drop payload from failed_server_changes -- a retry re-fetches current state instead of replaying a frozen snapshot (ADR-0062)",
        sql: "ALTER TABLE failed_server_changes DROP COLUMN payload",
    },
    Migration {
        version: 17,
        description: "file identity so a local rename survives on platforms notify never pairs From/To for (ADR-0063)",
        // Lazily filled at upload/download/walk touch, not backfilled here —
        // a migration that stats a million-row root is exactly what ADR-0037
        // made unaffordable. Same reason the index stays out of this
        // migration: it is only worth having once most rows carry a value,
        // and gets created at that point instead of paying maintenance for
        // every insert during a first sync.
        sql: "ALTER TABLE local_files ADD COLUMN file_identity BLOB",
    },
    Migration {
        version: 18,
        description: "second-look flag for a disappeared row (ADR-0063): From/To straddling a cycle boundary get one resolve pass before a miss becomes a real delete",
        sql: "ALTER TABLE local_files ADD COLUMN disappeared_seen INTEGER NOT NULL DEFAULT 0",
    },
    Migration {
        version: 19,
        description: "persist upload snapshot identity and hash for safe chunked-upload resume",
        sql: "
            ALTER TABLE chunked_uploads ADD COLUMN snapshot_id TEXT;
            ALTER TABLE chunked_uploads ADD COLUMN content_hash BLOB;
            ALTER TABLE chunked_uploads ADD COLUMN mtime_nanos INTEGER NOT NULL DEFAULT 0;
            DELETE FROM chunked_uploads
             WHERE snapshot_id IS NULL OR content_hash IS NULL OR length(content_hash) != 32;
        ",
    },
];

/// Inspect an existing database that predates the migration system and return
/// the highest migration version already implicitly applied.
fn detect_legacy_version(conn: &Connection) -> i64 {
    // No sync_roots table → truly empty database, start from scratch.
    let has_sync_roots: bool = conn
        .query_row(
            "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='sync_roots'",
            [],
            |r| r.get::<_, i64>(0),
        )
        .unwrap_or(0)
        > 0;

    if !has_sync_roots {
        return 0;
    }

    let col_exists = |col: &str| -> bool {
        conn.query_row(
            &format!("SELECT count(*) FROM pragma_table_info('sync_roots') WHERE name='{col}'"),
            [],
            |r| r.get::<_, i64>(0),
        )
        .unwrap_or(0)
            > 0
    };

    if col_exists("read_only") {
        3
    } else if col_exists("download_mode") {
        2
    } else {
        1
    }
}

impl LocalDb {
    pub fn open(path: impl AsRef<Path>) -> Result<Self> {
        if let Some(parent) = path.as_ref().parent() {
            std::fs::create_dir_all(parent)?;
        }
        let conn = Connection::open(path)?;

        // SQLite's LIKE is case-insensitive for ASCII by default regardless
        // of column collation -- `list_immediate_children`'s directory-scope
        // query (db/files.rs) needs it case-sensitive, or a case-sensitive
        // filesystem's "/Foo" and "/foo" would wrongly share one scope.
        // Connection-scoped, so this must run on every open, not just once
        // at migration time.
        conn.execute_batch("PRAGMA case_sensitive_like = ON;")?;

        // Ensure the migrations bookkeeping table exists.
        conn.execute_batch(
            "CREATE TABLE IF NOT EXISTS schema_migrations (
                 version    INTEGER PRIMARY KEY,
                 applied_at INTEGER NOT NULL DEFAULT (unixepoch())
             )",
        )?;

        // Determine where we are.  If the table is empty this might be either
        // a fresh database or a legacy one that predates the migration system.
        let tracked_version: i64 = conn.query_row(
            "SELECT COALESCE(MAX(version), 0) FROM schema_migrations",
            [],
            |r| r.get(0),
        )?;

        let current_version = if tracked_version == 0 {
            detect_legacy_version(&conn)
        } else {
            tracked_version
        };

        // Seed schema_migrations for legacy databases so subsequent runs are
        // aware of what has already been applied.
        if current_version > 0 && tracked_version == 0 {
            for m in MIGRATIONS.iter().filter(|m| m.version <= current_version) {
                conn.execute(
                    "INSERT OR IGNORE INTO schema_migrations (version) VALUES (?1)",
                    params![m.version],
                )?;
            }
        }

        // Apply every pending migration in version order.
        for m in MIGRATIONS.iter().filter(|m| m.version > current_version) {
            info!("applying DB migration v{}: {}", m.version, m.description);
            conn.execute_batch(m.sql)?;
            conn.execute(
                "INSERT INTO schema_migrations (version) VALUES (?1)",
                params![m.version],
            )?;
        }

        Ok(Self {
            conn: std::sync::Mutex::new(conn),
        })
    }
}

// ─── Types ────────────────────────────────────────────────────────────────────

#[derive(Debug, Clone, serde::Serialize)]
pub struct FailedServerChange {
    pub id: i64,
    pub sync_root_id: String,
    pub server_path: String,
    pub last_error: String,
    pub attempt_count: i64,
    pub next_retry_at: i64,
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct LocalFile {
    pub sync_root_id: String,
    pub path: String,
    pub size: i64,
    pub mtime: i64,
    pub content_hash: Vec<u8>,
    pub sync_status: String, // synced | pending | pending_delete | pending_incoming_delete | pending_failed | conflict | disappeared | blocked_remote_quota | in_use
    pub server_hash: Option<Vec<u8>>,
    /// Last server_version acknowledged after a successful upload.
    /// -1 means we have never successfully uploaded this file.
    pub server_version: i64,
    /// Failure tracking: how many times we have already failed in a row.
    /// Persisted via `set_failed`, cleared on `set_synced`.
    pub attempt_count: i64,
    pub last_error: Option<String>,
    pub next_retry_at: Option<i64>,
    /// True when this row represents a directory (empty folder) rather than a file.
    pub is_dir: bool,
    /// OS-assigned identity (NTFS/ReFS file id, or inode+device), lazily
    /// filled at upload/download/walk touch. `None` until first filled, or
    /// permanently on a filesystem that gives none (ADR-0063).
    pub file_identity: Option<Vec<u8>>,
    /// True once a `disappeared` row has survived one resolve pass without a
    /// matching staged path -- the next miss converts it to `pending_delete`
    /// (ADR-0063's "second look").  Meaningless outside `sync_status =
    /// "disappeared"`.
    pub disappeared_seen: bool,
}

/// `kind` value for an own server sync root.
pub const ROOT_KIND_OWNED: &str = "owned";
/// `kind` value for an activated Shared Mount (ADR-0011). For these,
/// `server_root_id` carries the **share_id**, and REST calls route to the
/// `/shared-mounts/{share_id}` namespace instead of `/sync-roots/{id}`.
pub const ROOT_KIND_MOUNT: &str = "mount";

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct SyncRootEntry {
    pub id: String,
    pub name: String,
    pub local_path: String,
    pub server_root_id: String,
    /// "on_demand" (placeholder, Windows CF API) or "eager" (full download)
    pub download_mode: String,
    /// When true the client never uploads or deletes — only downloads.
    pub read_only: bool,
    /// When true the sync engine skips this root entirely (no reconcile,
    /// no uploads, no downloads). Set automatically by the bulk-delete guard
    /// or manually via `pause_sync_root`.
    pub paused: bool,
    /// `"owned"` or `"mount"` (ADR-0011). Drives REST URL namespace and,
    /// for mounts, whether `read_only` blocks staging. Defaults to
    /// `"owned"` for rows written before migration 11.
    #[serde(default = "default_root_kind")]
    pub kind: String,
}

fn default_root_kind() -> String {
    ROOT_KIND_OWNED.to_string()
}

impl LocalFile {
    /// Build a `LocalFile` from a row produced by the canonical 12-column SELECT
    /// used by every `list_*` / `get_*` reader in this module.
    /// Required columns propagate errors via `?`; optional columns (with
    /// historical defaults) fall back gracefully.
    pub(super) fn from_row(row: &Row) -> rusqlite::Result<Self> {
        Ok(LocalFile {
            sync_root_id: row.get(0)?,
            path: row.get(1)?,
            size: row.get(2)?,
            mtime: row.get(3)?,
            content_hash: row.get(4).unwrap_or_default(),
            sync_status: row.get(5)?,
            server_hash: row.get(6)?,
            server_version: row.get(7).unwrap_or(-1),
            attempt_count: row.get(8).unwrap_or(0),
            last_error: row.get(9).ok(),
            next_retry_at: row.get(10).ok(),
            is_dir: row.get::<_, i64>(11).unwrap_or(0) != 0,
            file_identity: row.get(12).ok(),
            disappeared_seen: row.get::<_, i64>(13).unwrap_or(0) != 0,
        })
    }
}

impl SyncRootEntry {
    pub(super) fn from_row(row: &Row) -> rusqlite::Result<Self> {
        Ok(SyncRootEntry {
            id: row.get(0)?,
            name: row.get(1)?,
            local_path: row.get(2)?,
            server_root_id: row.get(3)?,
            download_mode: row.get(4).unwrap_or_else(|_| "on_demand".to_string()),
            read_only: row.get::<_, i64>(5).unwrap_or(0) != 0,
            paused: row.get::<_, i64>(6).unwrap_or(0) != 0,
            kind: row
                .get::<_, String>(7)
                .unwrap_or_else(|_| ROOT_KIND_OWNED.to_string()),
        })
    }

    /// True when this entry is an activated Shared Mount (ADR-0011): REST
    /// calls route to `/shared-mounts/{server_root_id}` and `read_only`
    /// blocks staging.
    pub fn is_mount(&self) -> bool {
        self.kind == ROOT_KIND_MOUNT
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn open_on_fresh_path_applies_every_migration() {
        let tmp = tempfile::tempdir().unwrap();
        let db = LocalDb::open(tmp.path().join("db.sqlite")).unwrap();

        let conn = db.conn.lock().unwrap();
        let version: i64 = conn
            .query_row(
                "SELECT COALESCE(MAX(version), 0) FROM schema_migrations",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(
            version,
            MIGRATIONS.last().unwrap().version,
            "a fresh DB must land on the latest migration"
        );
    }

    #[test]
    fn open_is_idempotent_across_repeated_opens() {
        let tmp = tempfile::tempdir().unwrap();
        let path = tmp.path().join("db.sqlite");
        LocalDb::open(&path).unwrap();
        // Re-opening an already-migrated DB must not error (migrations are
        // filtered to `version > current_version`, so nothing re-applies).
        let db = LocalDb::open(&path).unwrap();

        let conn = db.conn.lock().unwrap();
        let version: i64 = conn
            .query_row(
                "SELECT COALESCE(MAX(version), 0) FROM schema_migrations",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(version, MIGRATIONS.last().unwrap().version);
    }

    /// A database that predates the migration system (only the v1 tables,
    /// no `schema_migrations` bookkeeping) must be recognised via
    /// `detect_legacy_version` and fast-forwarded, not re-run from scratch
    /// (which would error on `ALTER TABLE ADD COLUMN` for columns that don't
    /// yet exist versus ones that already do).
    #[test]
    fn open_detects_legacy_schema_and_backfills_bookkeeping() {
        let tmp = tempfile::tempdir().unwrap();
        let path = tmp.path().join("legacy.sqlite");
        {
            // Simulate a v3 database (read_only column present, nothing past it)
            // written before schema_migrations existed.
            let conn = Connection::open(&path).unwrap();
            conn.execute_batch(
                "CREATE TABLE config (key TEXT PRIMARY KEY, value TEXT NOT NULL);
                 CREATE TABLE sync_roots (
                     id TEXT PRIMARY KEY, name TEXT NOT NULL, local_path TEXT NOT NULL UNIQUE,
                     server_root_id TEXT NOT NULL,
                     download_mode TEXT NOT NULL DEFAULT 'on_demand',
                     read_only INTEGER NOT NULL DEFAULT 0
                 );
                 CREATE TABLE local_files (
                     sync_root_id TEXT NOT NULL, path TEXT NOT NULL,
                     size INTEGER NOT NULL DEFAULT 0, mtime INTEGER NOT NULL DEFAULT 0,
                     content_hash BLOB, sync_status TEXT NOT NULL DEFAULT 'pending',
                     server_hash BLOB, updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
                     PRIMARY KEY (sync_root_id, path)
                 );",
            )
            .unwrap();
        }

        let db = LocalDb::open(&path).unwrap();

        // The legacy DB must now be usable through the normal repository API --
        // e.g. sync_root_read_only / kind columns from later migrations exist.
        db.upsert_sync_root("r1", "R", "/r", "srv", "eager", false, ROOT_KIND_OWNED)
            .unwrap();
        let got = db.get_sync_root("r1").unwrap().unwrap();
        assert_eq!(got.kind, ROOT_KIND_OWNED);

        let conn = db.conn.lock().unwrap();
        let version: i64 = conn
            .query_row(
                "SELECT COALESCE(MAX(version), 0) FROM schema_migrations",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(
            version,
            MIGRATIONS.last().unwrap().version,
            "must fast-forward past the legacy migrations too"
        );
    }

    #[test]
    fn detect_legacy_version_reads_the_highest_matching_marker_column() {
        let tmp = tempfile::tempdir().unwrap();

        // No sync_roots table at all -> version 0 (truly empty DB).
        let conn = Connection::open(tmp.path().join("empty.sqlite")).unwrap();
        assert_eq!(detect_legacy_version(&conn), 0);

        // sync_roots present but without download_mode/read_only -> v1.
        let conn = Connection::open(tmp.path().join("v1.sqlite")).unwrap();
        conn.execute_batch(
            "CREATE TABLE sync_roots (id TEXT PRIMARY KEY, name TEXT, local_path TEXT, server_root_id TEXT);",
        )
        .unwrap();
        assert_eq!(detect_legacy_version(&conn), 1);

        // download_mode present, read_only absent -> v2.
        let conn = Connection::open(tmp.path().join("v2.sqlite")).unwrap();
        conn.execute_batch(
            "CREATE TABLE sync_roots (id TEXT PRIMARY KEY, name TEXT, local_path TEXT,
                 server_root_id TEXT, download_mode TEXT NOT NULL DEFAULT 'on_demand');",
        )
        .unwrap();
        assert_eq!(detect_legacy_version(&conn), 2);

        // read_only present -> v3.
        let conn = Connection::open(tmp.path().join("v3.sqlite")).unwrap();
        conn.execute_batch(
            "CREATE TABLE sync_roots (id TEXT PRIMARY KEY, name TEXT, local_path TEXT,
                 server_root_id TEXT, download_mode TEXT NOT NULL DEFAULT 'on_demand',
                 read_only INTEGER NOT NULL DEFAULT 0);",
        )
        .unwrap();
        assert_eq!(detect_legacy_version(&conn), 3);
    }
}
