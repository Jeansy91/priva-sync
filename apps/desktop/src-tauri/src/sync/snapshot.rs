//! Point-in-time copies of files about to be uploaded (ADR-0077).
//!
//! A program that writes a file in place while it holds it open (Access/Jet,
//! SQLite) would otherwise have its writes land in the middle of an upload
//! read. The snapshot is taken under a write-denying share mode and hashed in
//! the same pass, so the bytes the server receives and the hash it is told are
//! one consistent state — and the source is locked only for as long as the
//! local copy takes, not for the whole transfer.

use std::path::{Path, PathBuf};
use std::time::SystemTime;

/// Jet/ACE database extensions and the lock file the engine keeps beside an
/// open database. Matched case-insensitively. `fdb` and `hdb` are the Jet4
/// family and history files of Legacy Family Tree, which keeps `<name>.ldb`
/// and `<name>~hdb.ldb` beside them while a family file is open.
const JET_DATABASE_EXTENSIONS: &[&str] = &[
    "accdb", "accde", "accdr", "accdt", "mdb", "mde", "mda", "fdb", "hdb",
];
const JET_LOCK_EXTENSIONS: &[&str] = &["laccdb", "ldb"];

/// `ERROR_SHARING_VIOLATION` / `ERROR_LOCK_VIOLATION`: another handle holds
/// write access, or a byte range we read is locked.
#[cfg(windows)]
const IN_USE_OS_ERRORS: &[i32] = &[32, 33];

/// Why a file cannot be snapshotted right now. Not a failure: the wait is on
/// the user closing the program that has it open.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum InUse {
    /// A Jet/ACE lock file sits next to the database.
    LockFile,
    /// Another process holds the file open for writing.
    OpenForWriting,
}

impl InUse {
    /// Shown in the "Sync-Probleme" list.
    pub fn message(self) -> &'static str {
        match self {
            Self::LockFile =>
                "Datenbank ist geöffnet – Upload folgt, sobald sie geschlossen ist",
            Self::OpenForWriting =>
                "Datei ist in einem anderen Programm geöffnet – Upload folgt, sobald sie geschlossen ist",
        }
    }
}

#[derive(Debug)]
pub(crate) enum SnapshotError {
    InUse(InUse),
    InsufficientSpace { required: u64, available: u64 },
    Io(std::io::Error),
}

impl std::fmt::Display for SnapshotError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InUse(reason) => f.write_str(reason.message()),
            Self::InsufficientSpace {
                required,
                available,
            } => write!(
                f,
                "Für diese und laufende Upload-Kopien werden mindestens {required} Byte \
                 benötigt (inklusive 1-GiB-Reserve); verfügbar sind {available} Byte",
            ),
            Self::Io(error) => write!(f, "{error}"),
        }
    }
}

impl std::error::Error for SnapshotError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Io(error) => Some(error),
            Self::InUse(_) | Self::InsufficientSpace { .. } => None,
        }
    }
}

impl From<std::io::Error> for SnapshotError {
    fn from(e: std::io::Error) -> Self {
        if is_in_use_error(&e) {
            Self::InUse(InUse::OpenForWriting)
        } else {
            Self::Io(e)
        }
    }
}

/// A consistent copy of a file, removed on drop unless a durable upload
/// session still needs it for resumption.
pub(crate) struct UploadSnapshot {
    tmp: PathBuf,
    pub id: String,
    pub hash: Vec<u8>,
    pub size: u64,
    /// The source's mtime at snapshot time — what the server records, not the
    /// copy's own creation time.
    pub mtime: SystemTime,
    keep_for_resume: bool,
}

impl UploadSnapshot {
    pub(super) fn new(
        tmp: PathBuf,
        id: String,
        hash: Vec<u8>,
        size: u64,
        mtime: SystemTime,
    ) -> Self {
        Self {
            tmp,
            id,
            hash,
            size,
            mtime,
            keep_for_resume: false,
        }
    }

    pub fn path(&self) -> &Path {
        &self.tmp
    }

    pub fn preserve_for_resume(&mut self) {
        self.keep_for_resume = true;
    }

    pub fn remove_on_drop(&mut self) {
        self.keep_for_resume = false;
    }
}

impl Drop for UploadSnapshot {
    fn drop(&mut self) {
        if self.keep_for_resume {
            return;
        }
        if let Err(e) = std::fs::remove_file(&self.tmp) {
            if e.kind() != std::io::ErrorKind::NotFound {
                tracing::warn!(
                    "failed to remove upload snapshot '{}': {e}",
                    self.tmp.display()
                );
            }
        }
    }
}

pub(crate) fn open_for_snapshot(path: &Path) -> Result<std::fs::File, SnapshotError> {
    if has_jet_lock_file(path) {
        return Err(SnapshotError::InUse(InUse::LockFile));
    }
    Ok(open_denying_writes(path)?)
}

/// Whether `path` is in use right now, without reading its content — the
/// cheap check `process_uploads` runs before hashing, so an open database is
/// not read in full on every cycle only for the snapshot to be refused.
pub(crate) fn check_in_use(path: &Path) -> Option<InUse> {
    if has_jet_lock_file(path) {
        return Some(InUse::LockFile);
    }
    match open_denying_writes(path) {
        Err(e) if is_in_use_error(&e) => Some(InUse::OpenForWriting),
        _ => None,
    }
}

fn has_jet_lock_file(path: &Path) -> bool {
    let is_jet = path.extension().and_then(|e| e.to_str()).is_some_and(|e| {
        JET_DATABASE_EXTENSIONS
            .iter()
            .any(|j| e.eq_ignore_ascii_case(j))
    });
    is_jet
        && JET_LOCK_EXTENSIONS
            .iter()
            .any(|lock| path.with_extension(lock).exists())
}

#[cfg(windows)]
fn open_denying_writes(path: &Path) -> std::io::Result<std::fs::File> {
    use std::os::windows::fs::OpenOptionsExt;
    const FILE_SHARE_READ: u32 = 0x1;
    const FILE_SHARE_DELETE: u32 = 0x4;
    std::fs::OpenOptions::new()
        .read(true)
        .share_mode(FILE_SHARE_READ | FILE_SHARE_DELETE)
        .open(path)
}

/// No mandatory locks outside Windows; the lock-file check and the snapshot's
/// hash/bytes consistency still apply.
#[cfg(not(windows))]
fn open_denying_writes(path: &Path) -> std::io::Result<std::fs::File> {
    std::fs::File::open(path)
}

fn is_in_use_error(e: &std::io::Error) -> bool {
    #[cfg(windows)]
    {
        e.raw_os_error()
            .is_some_and(|c| IN_USE_OS_ERRORS.contains(&c))
    }
    #[cfg(not(windows))]
    {
        let _ = e;
        false
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn jet_lock_file_marks_the_database_in_use() {
        let dir = tempfile::tempdir().unwrap();
        for (db, lock) in [
            ("Kunden.accdb", "Kunden.laccdb"),
            ("Alt.MDB", "Alt.ldb"),
            // Legacy Family Tree: family file and history file.
            ("27leg0103.fdb", "27leg0103.ldb"),
            ("27leg0103~hdb.hdb", "27leg0103~hdb.ldb"),
        ] {
            let src = dir.path().join(db);
            std::fs::write(&src, b"x").unwrap();
            std::fs::write(dir.path().join(lock), b"").unwrap();
            assert_eq!(check_in_use(&src), Some(InUse::LockFile), "{db}");
            assert!(
                matches!(
                    open_for_snapshot(&src),
                    Err(SnapshotError::InUse(InUse::LockFile))
                ),
                "{db}"
            );
        }
    }

    #[test]
    fn lock_file_only_counts_beside_a_jet_database() {
        let dir = tempfile::tempdir().unwrap();
        let src = dir.path().join("Kunden.txt");
        std::fs::write(&src, b"x").unwrap();
        std::fs::write(dir.path().join("Kunden.ldb"), b"").unwrap();
        assert_eq!(check_in_use(&src), None);
    }

    /// The Access shared-mode open: read/write access, sharing read/write.
    #[cfg(windows)]
    #[test]
    fn a_handle_open_for_writing_marks_the_file_in_use() {
        use std::os::windows::fs::OpenOptionsExt;
        let dir = tempfile::tempdir().unwrap();
        let src = dir.path().join("live.db");
        std::fs::write(&src, b"x").unwrap();
        let _writer = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .share_mode(0x1 | 0x2)
            .open(&src)
            .unwrap();

        assert_eq!(check_in_use(&src), Some(InUse::OpenForWriting));
        assert!(matches!(
            open_for_snapshot(&src),
            Err(SnapshotError::InUse(InUse::OpenForWriting))
        ));
    }

    #[cfg(windows)]
    #[test]
    fn a_read_only_handle_does_not_block_the_snapshot() {
        let dir = tempfile::tempdir().unwrap();
        let src = dir.path().join("viewer.pdf");
        std::fs::write(&src, b"x").unwrap();
        let _reader = std::fs::File::open(&src).unwrap();

        assert_eq!(check_in_use(&src), None);
        assert!(open_for_snapshot(&src).is_ok());
    }
}
