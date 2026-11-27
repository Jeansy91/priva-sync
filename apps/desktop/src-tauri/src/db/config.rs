use anyhow::Result;
use rusqlite::params;
use super::LocalDb;

/// Config key holding the global sync-exclusion patterns. One value for
/// the whole client — applies to every sync root.
const IGNORE_PATTERNS_KEY: &str = "ignore_patterns";

/// Patterns excluded from sync out of the box: OS junk, editor/Office
/// temp + lock files, and transient database sidecars. gitignore syntax; the
/// list is the seed shown in Settings and is fully editable by the user. Lines
/// starting with `#` are comments, blank lines are ignored.
pub const DEFAULT_IGNORE_PATTERNS: &str = "\
# System-Dateien
.DS_Store
._*
.Spotlight-V100
.Trashes
.AppleDouble
Thumbs.db
ehthumbs.db
desktop.ini
$RECYCLE.BIN/
System Volume Information/

# Temporäre Dateien & Sperren
*.tmp
*.temp
*.swp
*.bak
*~
~$*
.~lock.*#
.goutputstream-*

# Datenbank-Hilfsdateien (transient)
*.db-journal
*.sqlite-journal
*.sqlite-wal
*.sqlite-shm
# Access/Jet-Sperrdateien: existieren nur, solange die Datenbank offen ist
*.ldb
*.laccdb
# Legacy Family Tree: kurzlebige Hilfsdatei beim Schreiben
*.kil
";

impl LocalDb {
    /// Global sync-exclusion patterns. Returns the user's saved value, or
    /// [`DEFAULT_IGNORE_PATTERNS`] when none was ever saved — so the sensible
    /// defaults apply on a fresh client without the user touching Settings.
    pub fn get_ignore_patterns(&self) -> Result<String> {
        Ok(self.config_get(IGNORE_PATTERNS_KEY)?
            .unwrap_or_else(|| DEFAULT_IGNORE_PATTERNS.to_string()))
    }

    /// Replace the global exclusion patterns. The next reconcile/watcher
    /// pass applies them; an empty string disables all exclusions.
    pub fn set_ignore_patterns(&self, patterns: &str) -> Result<()> {
        self.config_set(IGNORE_PATTERNS_KEY, patterns)
    }

    pub fn config_set(&self, key: &str, value: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "INSERT INTO config (key, value) VALUES (?1, ?2)
             ON CONFLICT(key) DO UPDATE SET value = excluded.value",
            params![key, value],
        )?;
        Ok(())
    }

    pub fn config_get(&self, key: &str) -> Result<Option<String>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare("SELECT value FROM config WHERE key = ?1")?;
        let mut rows = stmt.query(params![key])?;
        match rows.next()? {
            Some(row) => Ok(Some(row.get(0)?)),
            None      => Ok(None),
        }
    }
}
