use std::io::{Read, Write};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::SystemTime;

use super::snapshot::{self, SnapshotError, UploadSnapshot};

const SNAPSHOT_SPACE_RESERVE_BYTES: u64 = 1024 * 1024 * 1024;
const SNAPSHOT_FILE_PREFIX: &str = "upload-snapshot-";

type SpaceProbe = fn(&Path) -> std::io::Result<u64>;

fn available_space(path: &Path) -> std::io::Result<u64> {
    fs2::available_space(path)
}

#[derive(Clone)]
pub(crate) struct SnapshotStore {
    dir: PathBuf,
    reserved_bytes: Arc<Mutex<u64>>,
    space_probe: SpaceProbe,
}

struct SpaceReservation {
    reserved_bytes: Arc<Mutex<u64>>,
    size: u64,
}

impl SpaceReservation {
    fn ensure_for_write(
        &mut self,
        store: &SnapshotStore,
        size: usize,
    ) -> Result<(), SnapshotError> {
        let size = size as u64;
        if size > self.size {
            store.extend_reservation(self, size - self.size)?;
        }
        Ok(())
    }

    fn account_written(&mut self, size: usize) {
        let size = size as u64;
        let mut reserved = self.reserved_bytes.lock().unwrap();
        *reserved -= size;
        self.size -= size;
    }
}

impl Drop for SpaceReservation {
    fn drop(&mut self) {
        let mut reserved = self.reserved_bytes.lock().unwrap();
        *reserved -= self.size;
    }
}

impl SnapshotStore {
    pub(crate) fn new(
        dir: PathBuf,
        retained_snapshot_ids: Option<Vec<String>>,
    ) -> std::io::Result<Self> {
        Self::new_with_space_probe(dir, available_space, retained_snapshot_ids)
    }

    fn new_with_space_probe(
        dir: PathBuf,
        space_probe: SpaceProbe,
        retained_snapshot_ids: Option<Vec<String>>,
    ) -> std::io::Result<Self> {
        std::fs::create_dir_all(&dir)?;
        if let Some(retained_snapshot_ids) = retained_snapshot_ids {
            if let Err(e) = cleanup_stale_snapshots(&dir, &retained_snapshot_ids) {
                tracing::warn!("failed to clean stale upload snapshots: {e}");
            }
        }
        Ok(Self {
            dir,
            reserved_bytes: Arc::new(Mutex::new(0)),
            space_probe,
        })
    }

    fn reserve(&self, size: u64) -> Result<SpaceReservation, SnapshotError> {
        let mut reserved = self.reserved_bytes.lock().unwrap();
        let available = (self.space_probe)(&self.dir)?;
        let Some(new_reserved) = reserved.checked_add(size) else {
            return Err(SnapshotError::InsufficientSpace {
                required: u64::MAX,
                available,
            });
        };
        let Some(required) = new_reserved.checked_add(SNAPSHOT_SPACE_RESERVE_BYTES) else {
            return Err(SnapshotError::InsufficientSpace {
                required: u64::MAX,
                available,
            });
        };
        if available < required {
            return Err(SnapshotError::InsufficientSpace {
                required,
                available,
            });
        }
        *reserved = new_reserved;
        Ok(SpaceReservation {
            reserved_bytes: self.reserved_bytes.clone(),
            size,
        })
    }

    fn extend_reservation(
        &self,
        reservation: &mut SpaceReservation,
        extra: u64,
    ) -> Result<(), SnapshotError> {
        let mut reserved = self.reserved_bytes.lock().unwrap();
        let available = (self.space_probe)(&self.dir)?;
        let Some(new_reserved) = reserved.checked_add(extra) else {
            return Err(SnapshotError::InsufficientSpace {
                required: u64::MAX,
                available,
            });
        };
        let Some(required) = new_reserved.checked_add(SNAPSHOT_SPACE_RESERVE_BYTES) else {
            return Err(SnapshotError::InsufficientSpace {
                required: u64::MAX,
                available,
            });
        };
        if available < required {
            return Err(SnapshotError::InsufficientSpace {
                required,
                available,
            });
        }
        *reserved = new_reserved;
        reservation.size += extra;
        Ok(())
    }

    pub(crate) fn take(&self, path: &Path) -> Result<UploadSnapshot, SnapshotError> {
        let mut source = snapshot::open_for_snapshot(path)?;
        let meta = source.metadata()?;
        let size = meta.len();
        let mtime = meta.modified().unwrap_or(SystemTime::UNIX_EPOCH);
        let mut reservation = self.reserve(size)?;
        let id = uuid::Uuid::new_v4().to_string();
        let tmp = self.dir.join(format!("{SNAPSHOT_FILE_PREFIX}{id}.tmp",));
        let file = std::fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&tmp)?;
        let mut snapshot = UploadSnapshot::new(tmp, id, Vec::new(), 0, mtime);
        let mut sink = std::io::BufWriter::new(file);
        let mut hasher = blake3::Hasher::new();
        let mut buf = vec![0u8; 256 * 1024];
        loop {
            let n = source.read(&mut buf)?;
            if n == 0 {
                break;
            }
            reservation.ensure_for_write(self, n)?;
            hasher.update(&buf[..n]);
            sink.write_all(&buf[..n])?;
            snapshot.size += n as u64;
            reservation.account_written(n);
        }
        drop(source);
        sink.into_inner().map_err(|e| e.into_error())?.sync_all()?;
        snapshot.hash = hasher.finalize().as_bytes().to_vec();
        drop(reservation);
        Ok(snapshot)
    }

    pub(crate) fn resume(
        &self,
        source_path: &Path,
        snapshot_id: &str,
        expected_hash: &[u8],
        expected_size: u64,
        mtime: SystemTime,
    ) -> Result<Option<UploadSnapshot>, SnapshotError> {
        let Ok(parsed_id) = uuid::Uuid::parse_str(snapshot_id) else {
            return Ok(None);
        };
        if expected_hash.len() != 32 {
            self.remove_snapshot(&parsed_id.to_string());
            return Ok(None);
        }

        let snapshot_path = self.snapshot_path(&parsed_id.to_string());
        let mut source = snapshot::open_for_snapshot(source_path)?;
        let source_size = source.metadata()?.len();
        let source_hash = hash_reader(&mut source)?;
        drop(source);
        if source_size != expected_size || source_hash != expected_hash {
            self.remove_snapshot(&parsed_id.to_string());
            return Ok(None);
        }

        let mut cached = match std::fs::File::open(&snapshot_path) {
            Ok(file) => file,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
            Err(error) => return Err(error.into()),
        };
        let cached_size = cached.metadata()?.len();
        let cached_hash = hash_reader(&mut cached)?;
        if cached_size != expected_size || cached_hash != expected_hash {
            self.remove_snapshot(&parsed_id.to_string());
            return Ok(None);
        }

        let mut snapshot = UploadSnapshot::new(
            snapshot_path,
            parsed_id.to_string(),
            cached_hash,
            cached_size,
            mtime,
        );
        snapshot.preserve_for_resume();
        Ok(Some(snapshot))
    }

    pub(crate) fn remove_snapshot(&self, snapshot_id: &str) {
        let Ok(id) = uuid::Uuid::parse_str(snapshot_id) else {
            return;
        };
        let path = self.snapshot_path(&id.to_string());
        if let Err(error) = std::fs::remove_file(&path) {
            if error.kind() != std::io::ErrorKind::NotFound {
                tracing::warn!(
                    "failed to remove upload snapshot '{}': {error}",
                    path.display()
                );
            }
        }
    }

    pub(crate) fn discard_session(
        &self,
        db: &crate::db::LocalDb,
        root_server_id: &str,
        server_path: &str,
    ) -> anyhow::Result<()> {
        if let Some(session) = db.chunked_upload_for(root_server_id, server_path)? {
            db.forget_chunked_upload(root_server_id, server_path)?;
            self.remove_snapshot(&session.snapshot_id);
        }
        Ok(())
    }

    fn snapshot_path(&self, snapshot_id: &str) -> PathBuf {
        self.dir
            .join(format!("{SNAPSHOT_FILE_PREFIX}{snapshot_id}.tmp"))
    }

    #[cfg(test)]
    fn reserved_bytes(&self) -> u64 {
        *self.reserved_bytes.lock().unwrap()
    }
}

fn cleanup_stale_snapshots(dir: &Path, retained_snapshot_ids: &[String]) -> std::io::Result<()> {
    for entry in std::fs::read_dir(dir)? {
        let entry = entry?;
        if !entry.file_type()?.is_file() {
            continue;
        }
        let name = entry.file_name();
        let Some(name) = name.to_str() else {
            continue;
        };
        let Some(id) = name
            .strip_prefix(SNAPSHOT_FILE_PREFIX)
            .and_then(|name| name.strip_suffix(".tmp"))
        else {
            continue;
        };
        if uuid::Uuid::parse_str(id).is_ok()
            && !retained_snapshot_ids.iter().any(|retained| retained == id)
        {
            if let Err(e) = std::fs::remove_file(entry.path()) {
                if e.kind() != std::io::ErrorKind::NotFound {
                    tracing::warn!(
                        "failed to remove stale upload snapshot '{}': {e}",
                        entry.path().display()
                    );
                }
            }
        }
    }
    Ok(())
}

fn hash_reader(reader: &mut impl Read) -> std::io::Result<Vec<u8>> {
    let mut hasher = blake3::Hasher::new();
    let mut buffer = vec![0u8; 256 * 1024];
    loop {
        let read = reader.read(&mut buffer)?;
        if read == 0 {
            break;
        }
        hasher.update(&buffer[..read]);
    }
    Ok(hasher.finalize().as_bytes().to_vec())
}

#[cfg(test)]
mod tests {
    use std::path::Path;

    use super::*;
    use crate::sync::snapshot::SnapshotError;

    fn near_reserve_space(_: &Path) -> std::io::Result<u64> {
        Ok(SNAPSHOT_SPACE_RESERVE_BYTES + 10)
    }

    fn ample_space(_: &Path) -> std::io::Result<u64> {
        Ok(SNAPSHOT_SPACE_RESERVE_BYTES + 100)
    }

    fn insufficient_space(_: &Path) -> std::io::Result<u64> {
        Ok(SNAPSHOT_SPACE_RESERVE_BYTES + 9)
    }

    #[test]
    fn cleanup_removes_only_well_formed_owned_snapshots() {
        let dir = tempfile::tempdir().unwrap();
        let owned = dir.path().join(format!(
            "{SNAPSHOT_FILE_PREFIX}{}.tmp",
            uuid::Uuid::new_v4()
        ));
        let foreign_tmp = dir.path().join("notes.123.tmp");
        let malformed = dir
            .path()
            .join(format!("{SNAPSHOT_FILE_PREFIX}not-a-uuid.tmp"));
        let directory = dir.path().join(format!(
            "{SNAPSHOT_FILE_PREFIX}{}.tmp",
            uuid::Uuid::new_v4()
        ));
        std::fs::write(&owned, b"ours").unwrap();
        std::fs::write(&foreign_tmp, b"not ours").unwrap();
        std::fs::write(&malformed, b"not ours").unwrap();
        std::fs::create_dir(&directory).unwrap();

        cleanup_stale_snapshots(dir.path(), &[]).unwrap();

        assert!(!owned.exists());
        assert!(foreign_tmp.exists());
        assert!(malformed.exists());
        assert!(directory.is_dir());
    }

    #[test]
    fn cleanup_preserves_only_snapshots_referenced_by_upload_sessions() {
        let dir = tempfile::tempdir().unwrap();
        let retained_id = uuid::Uuid::new_v4().to_string();
        let stale_id = uuid::Uuid::new_v4().to_string();
        let retained = dir
            .path()
            .join(format!("{SNAPSHOT_FILE_PREFIX}{retained_id}.tmp"));
        let stale = dir
            .path()
            .join(format!("{SNAPSHOT_FILE_PREFIX}{stale_id}.tmp"));
        std::fs::write(&retained, b"active").unwrap();
        std::fs::write(&stale, b"stale").unwrap();

        cleanup_stale_snapshots(dir.path(), &[retained_id]).unwrap();

        assert!(retained.exists());
        assert!(!stale.exists());
    }

    #[test]
    fn store_startup_cleans_old_snapshots_without_touching_other_temp_files() {
        let dir = tempfile::tempdir().unwrap();
        let stale = dir.path().join(format!(
            "{SNAPSHOT_FILE_PREFIX}{}.tmp",
            uuid::Uuid::new_v4()
        ));
        let foreign_tmp = dir.path().join("notes.123.tmp");
        std::fs::write(&stale, b"stale").unwrap();
        std::fs::write(&foreign_tmp, b"not ours").unwrap();

        let _store = SnapshotStore::new_with_space_probe(
            dir.path().to_path_buf(),
            ample_space,
            Some(Vec::new()),
        )
        .unwrap();

        assert!(!stale.exists());
        assert!(foreign_tmp.exists());
    }

    #[test]
    fn active_snapshot_reservations_share_the_free_space_budget() {
        let dir = tempfile::tempdir().unwrap();
        let store = SnapshotStore::new_with_space_probe(
            dir.path().to_path_buf(),
            near_reserve_space,
            Some(Vec::new()),
        )
        .unwrap();

        let first = store.reserve(8).unwrap();
        assert!(matches!(
            store.reserve(3),
            Err(SnapshotError::InsufficientSpace { .. })
        ));

        drop(first);
        assert!(store.reserve(10).is_ok());
    }

    #[test]
    fn snapshot_is_created_in_cache_and_removed_when_dropped() {
        let dir = tempfile::tempdir().unwrap();
        let cache = dir.path().join("cache");
        let store =
            SnapshotStore::new_with_space_probe(cache.clone(), ample_space, Some(Vec::new()))
                .unwrap();
        let source = dir.path().join("database.db");
        std::fs::write(&source, b"database bytes").unwrap();

        let snapshot = store.take(&source).unwrap();
        let path = snapshot.path().to_path_buf();
        assert_eq!(path.parent(), Some(cache.as_path()));
        assert_eq!(std::fs::read(&path).unwrap(), b"database bytes");
        assert_eq!(
            snapshot.hash,
            blake3::hash(b"database bytes").as_bytes().to_vec()
        );
        assert!(path
            .file_name()
            .unwrap()
            .to_string_lossy()
            .ends_with(".tmp"));
        assert_eq!(std::fs::read(&source).unwrap(), b"database bytes");

        drop(snapshot);
        assert!(!path.exists());
        assert_eq!(store.reserved_bytes(), 0);
    }

    #[test]
    fn unchanged_source_and_snapshot_can_be_resumed() {
        let dir = tempfile::tempdir().unwrap();
        let cache = dir.path().join("cache");
        let store =
            SnapshotStore::new_with_space_probe(cache, ample_space, Some(Vec::new())).unwrap();
        let source = dir.path().join("database.db");
        std::fs::write(&source, b"database bytes").unwrap();

        let mut original = store.take(&source).unwrap();
        let id = original.id.clone();
        let hash = original.hash.clone();
        let size = original.size;
        let mtime = original.mtime;
        let path = original.path().to_path_buf();
        original.preserve_for_resume();
        drop(original);

        let resumed = store
            .resume(&source, &id, &hash, size, mtime)
            .unwrap()
            .expect("matching source and cached snapshot should resume");
        assert_eq!(resumed.id, id);
        assert_eq!(resumed.path(), path);
        assert_eq!(resumed.hash, hash);
        drop(resumed);
        assert!(path.exists());
        let mut resumed = store
            .resume(&source, &id, &hash, size, mtime)
            .unwrap()
            .unwrap();
        resumed.remove_on_drop();
        drop(resumed);
        assert!(!path.exists());
    }

    #[test]
    fn changed_content_is_rejected_even_when_size_and_mtime_match() {
        let dir = tempfile::tempdir().unwrap();
        let store = SnapshotStore::new_with_space_probe(
            dir.path().join("cache"),
            ample_space,
            Some(Vec::new()),
        )
        .unwrap();
        let source = dir.path().join("database.db");
        std::fs::write(&source, b"first version").unwrap();
        let mut snapshot = store.take(&source).unwrap();
        let id = snapshot.id.clone();
        let hash = snapshot.hash.clone();
        let size = snapshot.size;
        let mtime = snapshot.mtime;
        let snapshot_path = snapshot.path().to_path_buf();
        snapshot.preserve_for_resume();
        drop(snapshot);

        std::fs::write(&source, b"other version").unwrap();
        std::fs::OpenOptions::new()
            .write(true)
            .open(&source)
            .unwrap()
            .set_times(std::fs::FileTimes::new().set_modified(mtime))
            .unwrap();
        assert_eq!(std::fs::metadata(&source).unwrap().len(), size);

        assert!(store
            .resume(&source, &id, &hash, size, mtime)
            .unwrap()
            .is_none());
        assert!(!snapshot_path.exists());
    }

    #[test]
    fn corrupt_or_missing_cached_snapshot_is_not_resumed() {
        let dir = tempfile::tempdir().unwrap();
        let store = SnapshotStore::new_with_space_probe(
            dir.path().join("cache"),
            ample_space,
            Some(Vec::new()),
        )
        .unwrap();
        let source = dir.path().join("database.db");
        std::fs::write(&source, b"database bytes").unwrap();
        let mut snapshot = store.take(&source).unwrap();
        let id = snapshot.id.clone();
        let hash = snapshot.hash.clone();
        let size = snapshot.size;
        let mtime = snapshot.mtime;
        let snapshot_path = snapshot.path().to_path_buf();
        snapshot.preserve_for_resume();
        drop(snapshot);

        std::fs::write(&snapshot_path, b"corrupted bytes").unwrap();
        assert!(store
            .resume(&source, &id, &hash, size, mtime)
            .unwrap()
            .is_none());
        assert!(!snapshot_path.exists());
        assert!(store
            .resume(&source, &id, &hash, size, mtime)
            .unwrap()
            .is_none());
    }

    #[test]
    fn insufficient_space_does_not_create_a_partial_snapshot() {
        let dir = tempfile::tempdir().unwrap();
        let cache = dir.path().join("cache");
        let store = SnapshotStore::new_with_space_probe(
            cache.clone(),
            insufficient_space,
            Some(Vec::new()),
        )
        .unwrap();
        let source = dir.path().join("database.db");
        std::fs::write(&source, b"database bytes").unwrap();

        let error = match store.take(&source) {
            Err(error) => error,
            Ok(_) => panic!("snapshot should have been rejected for insufficient space"),
        };
        assert!(matches!(error, SnapshotError::InsufficientSpace { .. }));
        assert!(error.to_string().contains("1-GiB-Reserve"));
        assert_eq!(std::fs::read_dir(&cache).unwrap().count(), 0);
        assert_eq!(store.reserved_bytes(), 0);
    }
}
