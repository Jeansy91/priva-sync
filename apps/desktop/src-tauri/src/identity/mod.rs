//! File identity — the OS-assigned id that survives a rename (ADR-0063).
//!
//! `notify` only pairs a rename's `From`/`To` events on Linux (inotify);
//! on Windows and macOS they arrive as two unrelated events. This module
//! reads a stable-across-rename identity (NTFS/ReFS file id on Windows,
//! inode+device elsewhere) so the sync engine can join a disappeared path
//! against a newly staged one instead of guessing from timing.

use std::io;
use std::path::Path;

#[cfg(windows)]
mod windows;

#[cfg(unix)]
mod unix;

/// Reads `path`'s current file identity. Works for both files and
/// directories. Two paths carry the same identity, on the same volume, iff
/// they are (or were, before a rename) the same filesystem entry.
///
/// Not a guarantee on its own — identities can be reused by the filesystem
/// after deletion — so callers must confirm a match by other means (size,
/// content hash) before treating it as proof of a move.
pub fn read(path: &Path) -> io::Result<Vec<u8>> {
    #[cfg(windows)]
    {
        windows::read(path)
    }
    #[cfg(unix)]
    {
        unix::read(path)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;

    #[test]
    fn identity_survives_rename_on_same_volume() {
        let dir = tempfile::tempdir().unwrap();
        let original = dir.path().join("a.txt");
        fs::write(&original, b"hello").unwrap();

        let before = read(&original).unwrap();

        let renamed = dir.path().join("b.txt");
        fs::rename(&original, &renamed).unwrap();
        let after = read(&renamed).unwrap();

        assert_eq!(before, after);
    }

    #[test]
    fn different_files_have_different_identity() {
        let dir = tempfile::tempdir().unwrap();
        let a = dir.path().join("a.txt");
        let b = dir.path().join("b.txt");
        fs::write(&a, b"hello").unwrap();
        fs::write(&b, b"hello").unwrap();

        assert_ne!(read(&a).unwrap(), read(&b).unwrap());
    }
}
