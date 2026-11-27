//! Staging a server download on its way to disk.
//!
//! Downloads never pass through memory whole: the body streams into a
//! temp file next to its destination while a BLAKE3 hasher runs over the same
//! bytes, so one pass produces both the file and its hash. Only the closing
//! `rename` makes the result visible under its real name — that rename is the
//! atomic step every caller relies on.

use anyhow::Result;
use std::path::{Path, PathBuf};

use crate::api::ApiSnapshot;

/// A server version fully written to a temp file next to its destination,
/// with the hash of what was written.
///
/// The temp file is owned by this value: dropping it without [`publish`]
/// removes the file, so an abandoned download never leaves a stray `.tmp`
/// behind for the next local walk to trip over.
///
/// [`publish`]: StagedDownload::publish
pub(crate) struct StagedDownload {
    tmp:      Option<PathBuf>,
    /// BLAKE3 of the staged bytes, computed during the write.
    pub hash: Vec<u8>,
    pub size: i64,
}

impl StagedDownload {
    /// Move the staged bytes onto `dest`. This rename is the atomic step: the
    /// destination holds either its old contents or the complete new ones,
    /// never a half-written file. A failed rename cleans the temp file up
    /// rather than leaving it for the next local walk to find.
    pub fn publish(mut self, dest: &Path) -> Result<()> {
        let tmp = self.take();
        if let Err(e) = std::fs::rename(&tmp, dest) {
            let _ = std::fs::remove_file(&tmp);
            return Err(e.into());
        }
        Ok(())
    }

    /// Hand the temp file's path over and give up the cleanup guard.
    fn take(&mut self) -> PathBuf {
        self.tmp.take().expect("a StagedDownload can only be taken once")
    }

    /// A winner already on disk, without a server in the loop. Test-only —
    /// production values come from [`stage_download`].
    #[cfg(test)]
    pub(super) fn staged_for_test(dest: &Path, data: &[u8]) -> Self {
        let tmp = staging_path(dest);
        std::fs::write(&tmp, data).expect("write staged test file");
        Self {
            tmp:  Some(tmp),
            hash: blake3::hash(data).as_bytes().to_vec(),
            size: data.len() as i64,
        }
    }
}

impl Drop for StagedDownload {
    fn drop(&mut self) {
        if let Some(tmp) = &self.tmp {
            let _ = std::fs::remove_file(tmp);
        }
    }
}

/// A staging path next to `dest`. Two properties matter and pull in opposite
/// directions: the name has to end in `.tmp`, because that is what the local
/// walk and the file watcher skip on, and it has to be unique, because
/// `dest.with_extension("tmp")` collides for `notes.txt` and `notes.md` in the
/// same directory — harmless while the file existed for a moment, a real race
/// once a multi-GB download holds it open for minutes.
pub(super) fn staging_path(dest: &Path) -> PathBuf {
    let name = dest.file_name().unwrap_or_default().to_string_lossy();
    dest.with_file_name(format!("{name}.{}.tmp", uuid::Uuid::new_v4()))
}

/// Download `server_path` into a temp file next to `dest`, hashing as it goes.
/// The destination is not touched — the caller renames the staged file onto it
/// when it is ready to.
pub(crate) async fn stage_download(
    api:         &ApiSnapshot,
    root_id:     &str,
    server_path: &str,
    is_mount:    bool,
    dest:        &Path,
) -> Result<StagedDownload> {
    if let Some(parent) = dest.parent() {
        std::fs::create_dir_all(parent)?;
    }

    let stream = api.download_stream(root_id, server_path, is_mount, None).await?;

    let tmp = staging_path(dest);
    // Guard from here on: every early return below must not leave the temp
    // file behind, so it is owned by a StagedDownload before the first write.
    let mut staged = StagedDownload {
        tmp:  Some(tmp.clone()),
        hash: Vec::new(),
        size: 0,
    };

    // `sink` is declared after `staged` and so drops first: on Windows the
    // guard's `remove_file` would fail while the handle is still open.
    let mut sink = HashingWriter::create(&tmp).await?;
    let written  = stream.drain_to(&mut sink).await?;
    let hash     = sink.finish().await?;

    staged.hash = hash;
    staged.size = written as i64;
    Ok(staged)
}

/// A file being written and hashed at the same time. Keeping the hasher on the
/// write path is what collapses the old write-then-read-back-and-hash into one
/// pass over the bytes.
struct HashingWriter {
    file:   tokio::fs::File,
    hasher: blake3::Hasher,
}

impl HashingWriter {
    async fn create(path: &Path) -> Result<Self> {
        Ok(Self {
            file:   tokio::fs::File::create(path).await?,
            hasher: blake3::Hasher::new(),
        })
    }

    /// Flush the file and return the BLAKE3 of everything written through it.
    async fn finish(mut self) -> Result<Vec<u8>> {
        use tokio::io::AsyncWriteExt as _;
        self.file.flush().await?;
        Ok(self.hasher.finalize().as_bytes().to_vec())
    }
}

impl tokio::io::AsyncWrite for HashingWriter {
    fn poll_write(
        mut self: std::pin::Pin<&mut Self>,
        cx:       &mut std::task::Context<'_>,
        buf:      &[u8],
    ) -> std::task::Poll<std::io::Result<usize>> {
        let this = &mut *self;
        let written = std::task::ready!(std::pin::Pin::new(&mut this.file).poll_write(cx, buf))?;
        // Hash exactly what the file accepted — a short write must not put the
        // hasher ahead of the file's contents.
        this.hasher.update(&buf[..written]);
        std::task::Poll::Ready(Ok(written))
    }

    fn poll_flush(
        mut self: std::pin::Pin<&mut Self>,
        cx:       &mut std::task::Context<'_>,
    ) -> std::task::Poll<std::io::Result<()>> {
        std::pin::Pin::new(&mut self.file).poll_flush(cx)
    }

    fn poll_shutdown(
        mut self: std::pin::Pin<&mut Self>,
        cx:       &mut std::task::Context<'_>,
    ) -> std::task::Poll<std::io::Result<()>> {
        std::pin::Pin::new(&mut self.file).poll_shutdown(cx)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;

    fn snap_for(url: String) -> ApiSnapshot {
        let mut client = crate::api::ApiClient::new(url, Arc::new(crate::api::TransferStats::default()));
        client.set_token("test-token");
        client.snapshot().expect("snapshot")
    }

    // The single pass: the bytes land in a sibling temp file and the hash
    // comes out of the same pass, so nothing has to re-read the file to learn
    // what it holds.
    #[tokio::test]
    async fn stage_download_writes_a_sibling_temp_file_and_hashes_it_in_one_pass() {
        let mut server = mockito::Server::new_async().await;
        let _m = server
            .mock("GET", mockito::Matcher::Any)
            .with_status(200)
            .with_body(b"server-payload".as_slice())
            .create_async()
            .await;

        let dir  = tempfile::tempdir().unwrap();
        let dest = dir.path().join("notes.txt");

        let staged = stage_download(&snap_for(server.url()), "r1", "/notes.txt", false, &dest)
            .await
            .expect("stage");

        assert_eq!(staged.size, "server-payload".len() as i64);
        assert_eq!(staged.hash, blake3::hash(b"server-payload").as_bytes().to_vec());

        assert!(!dest.exists(), "the destination stays untouched until it is published");
        let staged_now: Vec<_> = std::fs::read_dir(dir.path()).unwrap()
            .filter_map(|e| e.ok())
            .map(|e| e.path())
            .collect();
        assert_eq!(staged_now.len(), 1, "exactly one temp file, next to the destination");
        assert_eq!(staged_now[0].extension().and_then(|e| e.to_str()), Some("tmp"));

        staged.publish(&dest).expect("publish");
        assert_eq!(std::fs::read(&dest).unwrap(), b"server-payload");
    }

    // An abandoned download must not leave a `.tmp` behind — the local walk
    // skips those, so a stray one would sit in the sync root forever.
    #[tokio::test]
    async fn an_abandoned_download_takes_its_temp_file_with_it() {
        let mut server = mockito::Server::new_async().await;
        let _m = server
            .mock("GET", mockito::Matcher::Any)
            .with_status(200)
            .with_body(b"abandoned".as_slice())
            .create_async()
            .await;

        let dir  = tempfile::tempdir().unwrap();
        let dest = dir.path().join("notes.txt");

        let staged = stage_download(&snap_for(server.url()), "r1", "/notes.txt", false, &dest)
            .await
            .expect("stage");
        drop(staged);

        let leftovers: Vec<_> = std::fs::read_dir(dir.path())
            .unwrap()
            .filter_map(|e| e.ok())
            .map(|e| e.file_name().to_string_lossy().into_owned())
            .collect();
        assert!(leftovers.is_empty(), "temp files left behind: {leftovers:?}");
    }

    // Two downloads into one directory must not fight over the same temp file.
    // `dest.with_extension("tmp")` used to give `notes.txt` and `notes.md` the
    // same one; the `.tmp` extension itself has to stay, because that is what
    // the walk and the watcher skip on.
    #[test]
    fn staging_paths_are_unique_but_still_end_in_tmp() {
        let a = staging_path(Path::new("/root/notes.txt"));
        let b = staging_path(Path::new("/root/notes.md"));
        let c = staging_path(Path::new("/root/notes.txt"));

        for p in [&a, &b, &c] {
            assert_eq!(p.extension().and_then(|e| e.to_str()), Some("tmp"), "{p:?}");
            assert_eq!(p.parent(), Some(Path::new("/root")), "{p:?}");
        }
        assert_ne!(a, b, "different destinations must not collide");
        assert_ne!(a, c, "two downloads of the same file must not collide");
    }
}
