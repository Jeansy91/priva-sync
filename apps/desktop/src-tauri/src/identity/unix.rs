use std::io;
use std::os::unix::fs::MetadataExt;
use std::path::Path;

pub(super) fn read(path: &Path) -> io::Result<Vec<u8>> {
    let meta = std::fs::symlink_metadata(path)?;
    let mut bytes = Vec::with_capacity(16);
    bytes.extend_from_slice(&meta.dev().to_le_bytes());
    bytes.extend_from_slice(&meta.ino().to_le_bytes());
    Ok(bytes)
}
