//! Multi-part chunked upload protocol — used for files larger than
//! `CHUNKED_UPLOAD_THRESHOLD`. Handles both fresh sessions and resume:
//! if a previous upload was interrupted, the server's missing-chunk list
//! drives a partial re-upload.

use anyhow::Result;
use std::path::Path;
use std::time::{Duration, SystemTime};
use tracing::{info, warn};

use super::retry::with_retry_throttled;
use crate::api::{await_assembly, AuthedApi, CompleteAck, UploadSessionGone, ASSEMBLY_POLL_WAIT};

/// Run one single-shot network call gated on the upload-concurrency
/// semaphore. Used for the admin calls around a chunked transfer
/// (init/status/complete) that have no retry loop of their own but still
/// need to count against the same concurrency budget as everything else.
async fn gated<T>(
    sem: &tokio::sync::Semaphore,
    fut: impl std::future::Future<Output = Result<T>>,
) -> Result<T> {
    let _permit = sem.acquire().await.expect("semaphore closed");
    fut.await
}

/// Everything about the target file/session a chunked upload needs, bundled
/// so `upload_file_chunked` stays under clippy's argument-count lint —
/// `api` and `sem` stay separate params since they are infra, not request data.
pub(crate) struct ChunkedUploadRequest<'a> {
    pub root_server_id: &'a str,
    pub server_path: &'a str,
    pub local_path: &'a Path,
    pub mtime: SystemTime,
    pub known_server_version: i64,
    pub existing_upload_id: Option<String>,
    pub snapshot_id: &'a str,
    pub sessions: &'a dyn ChunkedSessionStore,
    /// BLAKE3 of the local file as it is about to be sent. Empty only when the
    /// caller could not hash it; the server then has nothing to verify against.
    pub content_hash: &'a [u8],
}

pub(crate) struct ChunkedSessionMetadata<'a> {
    pub file_size: u64,
    pub mtime: SystemTime,
    pub snapshot_id: &'a str,
    pub content_hash: &'a [u8],
}

/// The slice of the local ledger a chunked upload needs. A freshly opened
/// session is recorded before the first chunk goes out, so an interrupted
/// upload can resume instead of re-sending everything; it is dropped once the
/// session is finished. Errors are the caller's to log — losing a ledger entry
/// costs a re-upload, never correctness.
pub(crate) trait ChunkedSessionStore: Send + Sync {
    fn remember(
        &self,
        root_server_id: &str,
        server_path: &str,
        upload_id: &str,
        metadata: ChunkedSessionMetadata<'_>,
    );
    fn forget(&self, root_server_id: &str, server_path: &str);
}

/// Bounds and target for the per-chunk size heuristic (ADR-0066). Client-side
/// constants, not a server contract: the server has no visibility into this
/// client's network path and gains nothing by overriding them, so it only
/// enforces its own flat ceiling independent of what a client picks here.
pub(crate) const MIN_CHUNK_SIZE: u64 = 1 << 20; // 1 MiB
pub(crate) const MAX_CHUNK_SIZE: u64 = 100 << 20; // 100 MiB
const TARGET_CHUNK_DURATION: Duration = Duration::from_secs(10);

/// The size the very first chunk of a session uses, before any timing sample
/// exists to adapt from — also reused after a resume, since no history
/// survives a restart. The historical fixed size, kept as a reasonable seed
/// rather than for any protocol reason.
const DEFAULT_INITIAL_CHUNK_SIZE: u64 = 5 << 20; // 5 MiB

/// The size the next chunk should use, given the size and wall-clock duration
/// of the one just sent. Ported from NextCloud's `propagateuploadng.cpp`
/// unmodified: a damped single-sample correction toward whatever size would
/// have hit `TARGET_CHUNK_DURATION`, not a separate throughput moving
/// average — the halving is the damping, so one slow or fast outlier corrects
/// itself over 2-3 chunks rather than overshooting immediately.
pub(crate) fn next_chunk_size(previous_size: u64, elapsed: Duration) -> u64 {
    // A near-zero elapsed time would send predicted_good_size to infinity for
    // no real signal (the clock resolution, not the network) — floor it.
    let elapsed = elapsed.max(Duration::from_millis(1));
    let predicted_good_size =
        (previous_size as f64) * TARGET_CHUNK_DURATION.as_secs_f64() / elapsed.as_secs_f64();
    let target = previous_size as f64 / 2.0 + predicted_good_size / 2.0;
    (target.round() as u64).clamp(MIN_CHUNK_SIZE, MAX_CHUNK_SIZE)
}

/// A resumed upload must not send a chunk larger than what is actually
/// missing — the heuristic's target size is a preference, the remaining gap
/// is a hard limit, or the chunk would write past the end of the file.
pub(crate) fn clamp_to_remaining(target_size: u64, remaining_gap: u64) -> u64 {
    target_size.min(remaining_gap)
}

/// Decide whether an existing session can still be resumed. The one refusal
/// drops the ledger entry, so the next attempt opens a fresh session instead
/// of coming back to this one. Unlike the index-based scheme this replaces
/// (ADR-0066), the server's missing_ranges are usable as-is — offsets are
/// explicit now, so there is nothing left to derive and get wrong.
fn resumable(
    status: crate::api::ChunkedUploadStatus,
    upload_id: String,
    root_server_id: &str,
    server_path: &str,
    sessions: &dyn ChunkedSessionStore,
) -> Option<(String, Vec<(u64, u64)>)> {
    // A session the server has given up on replays that verdict for as long as
    // its row lives. Nothing about resuming can change the answer, and a hash
    // mismatch usually means the local file has moved on anyway.
    if status.status.as_deref() == Some("failed") {
        let code = status
            .error
            .map(|e| e.code)
            .unwrap_or_else(|| "unknown".to_string());
        warn!("chunked upload: session {upload_id} for {server_path} failed for good ({code}) — starting a fresh session");
        sessions.forget(root_server_id, server_path);
        return None;
    }

    info!(
        "chunked upload: resuming {server_path} at session {upload_id} ({} byte range(s) missing)",
        status.missing_ranges.len()
    );
    Some((upload_id, status.missing_ranges))
}

/// Upload a large file using the multi-part chunked protocol.
///
/// Handles resume: if `req.existing_upload_id` is Some, queries the server
/// for missing chunks and only re-uploads those.  Otherwise opens a new
/// session.
///
/// Every network call gates on `sem` — the upload-concurrency semaphore
/// shared with the rest of the upload queue — so a chunked transfer counts
/// against the same budget as any other file and never occupies a slot while
/// merely backing off from a transient error. Every call is also its own
/// [`AuthedApi::call`]: a large file takes longer than one access token lives.
///
/// Returns `UploadOutcome` on success, or `Err` for unrecoverable failures.
pub(crate) async fn upload_file_chunked(
    api: &AuthedApi,
    sem: &tokio::sync::Semaphore,
    req: ChunkedUploadRequest<'_>,
) -> Result<crate::api::UploadOutcome> {
    let ChunkedUploadRequest {
        root_server_id,
        server_path,
        local_path,
        mtime,
        known_server_version,
        existing_upload_id,
        snapshot_id,
        sessions,
        content_hash,
    } = req;

    let file_size = local_path.metadata()?.len();

    // ── Open or resume session ────────────────────────────────────────────
    let resumed = match existing_upload_id {
        Some(id) => {
            let status = gated(
                sem,
                api.call(|snap| {
                    let id = &id;
                    async move { snap.chunked_upload_status(root_server_id, id, None).await }
                }),
            )
            .await;
            match status {
                Ok(status) => resumable(status, id, root_server_id, server_path, sessions),
                // Expired or swept: asking again gets the same 404 forever, so
                // this attempt opens a fresh session instead.
                Err(e) if e.is::<UploadSessionGone>() => {
                    warn!("chunked upload: session {id} for {server_path} no longer exists on the server — starting a fresh session");
                    sessions.forget(root_server_id, server_path);
                    None
                }
                Err(e) => return Err(e),
            }
        }
        None => None,
    };

    let (upload_id, to_upload) = match resumed {
        Some(session) => session,
        None => {
            let sess = gated(
                sem,
                api.call(|snap| async move {
                    snap.init_chunked_upload(
                        root_server_id,
                        server_path,
                        file_size,
                        mtime,
                        known_server_version,
                        content_hash,
                    )
                    .await
                }),
            )
            .await?;
            let id = sess.upload_id.clone();
            // Record before the first chunk goes out: a session that is only
            // remembered after a successful upload is worth nothing to a retry.
            sessions.remember(
                root_server_id,
                server_path,
                &id,
                ChunkedSessionMetadata {
                    file_size,
                    mtime,
                    snapshot_id,
                    content_hash,
                },
            );
            info!("chunked upload: {server_path} → session {id}");
            (id, vec![(0u64, file_size)])
        }
    };

    let outcome = send_and_complete(
        api,
        sem,
        root_server_id,
        server_path,
        local_path,
        &upload_id,
        to_upload,
    )
    .await;
    match &outcome {
        // Finished, one way or another — a conflict is an answer too, and
        // resuming this session would only replay it.
        Ok(_) => sessions.forget(root_server_id, server_path),
        // The session expired under the transfer: the next attempt must not
        // come back to it.
        Err(e) if e.is::<UploadSessionGone>() => sessions.forget(root_server_id, server_path),
        Err(_) => {}
    }
    outcome
}

/// Send the missing byte ranges of an open session, then have the server
/// assemble it.
async fn send_and_complete(
    api: &AuthedApi,
    sem: &tokio::sync::Semaphore,
    root_server_id: &str,
    server_path: &str,
    local_path: &Path,
    upload_id: &str,
    to_upload: Vec<(u64, u64)>,
) -> Result<crate::api::UploadOutcome> {
    use std::io::{Read as _, Seek as _};

    if !to_upload.is_empty() {
        let mut file = std::fs::File::open(local_path)?;
        // Seeded before any timing sample exists; adapts chunk by chunk from
        // here on (ADR-0066), across gaps too — network conditions don't reset
        // at a gap boundary just because a resume introduced one.
        let mut chunk_size = DEFAULT_INITIAL_CHUNK_SIZE;

        for (gap_start, gap_end) in to_upload {
            let mut offset = gap_start;
            while offset < gap_end {
                let len = clamp_to_remaining(chunk_size, gap_end - offset);
                file.seek(std::io::SeekFrom::Start(offset))?;
                let mut buf = vec![0u8; len as usize];
                file.read_exact(&mut buf)?;
                let data = bytes::Bytes::from(buf);

                let started = std::time::Instant::now();
                with_retry_throttled(sem, &format!("{server_path}[{offset}]"), || {
                    api.call(|snap| {
                        let data = data.clone();
                        async move {
                            snap.upload_chunk(root_server_id, upload_id, offset, data)
                                .await
                        }
                    })
                })
                .await?;

                chunk_size = next_chunk_size(len, started.elapsed());
                offset += len;
            }
        }
    }

    // ── Complete ──────────────────────────────────────────────────────────
    // Only the acceptance counts against the upload budget. The server-side
    // assembly of a multi-gigabyte file takes minutes, and holding a slot for
    // all of it would stall every other transfer behind one big file.
    let ack = gated(
        sem,
        api.call(|snap| async move {
            snap.complete_chunked_upload(root_server_id, upload_id)
                .await
        }),
    )
    .await?;
    match ack {
        CompleteAck::Settled(outcome) => Ok(outcome),
        CompleteAck::Accepted => {
            await_assembly(|| {
                api.call(|snap| async move {
                    snap.chunked_upload_status(root_server_id, upload_id, Some(ASSEMBLY_POLL_WAIT))
                        .await
                })
            })
            .await
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write as _;

    use crate::api::UploadOutcome;

    // A chunk that took exactly the target duration needs no correction —
    // predictedGoodSize equals the current size, so the damped average of the
    // two is the same size again.
    #[test]
    fn next_chunk_size_unchanged_when_elapsed_matches_target() {
        let got = next_chunk_size(4 << 20, Duration::from_secs(10));
        assert_eq!(got, 4 << 20);
    }

    // Faster than target predicts a larger chunk would still make it — the
    // damped average moves size up, but only halfway there (ADR-0066 ports
    // NextCloud's formula unmodified: targetSize = size/2 + predicted/2).
    #[test]
    fn next_chunk_size_grows_when_elapsed_is_faster_than_target() {
        let got = next_chunk_size(4 << 20, Duration::from_secs(5)); // 2x too fast
        let predicted = (4u64 << 20) * 10 / 5; // 8 MiB
        let want = (4u64 << 20) / 2 + predicted / 2; // 6 MiB
        assert_eq!(got, want);
    }

    // Slower than target predicts a smaller chunk would have made it — size
    // moves down, again only halfway.
    #[test]
    fn next_chunk_size_shrinks_when_elapsed_is_slower_than_target() {
        let got = next_chunk_size(4 << 20, Duration::from_secs(20)); // 2x too slow
        let predicted = (4u64 << 20) * 10 / 20; // 2 MiB
        let want = (4u64 << 20) / 2 + predicted / 2; // 3 MiB
        assert_eq!(got, want);
    }

    // However fast a chunk arrives, the next one never exceeds MAX_CHUNK_SIZE
    // — an unbounded upward correction would defeat the point of chunking.
    #[test]
    fn next_chunk_size_clamps_to_the_maximum() {
        let got = next_chunk_size(MAX_CHUNK_SIZE, Duration::from_millis(1));
        assert_eq!(got, MAX_CHUNK_SIZE);
    }

    // However slow, the next chunk never drops below MIN_CHUNK_SIZE — a chunk
    // that small would drown in per-request overhead.
    #[test]
    fn next_chunk_size_clamps_to_the_minimum() {
        let got = next_chunk_size(MIN_CHUNK_SIZE, Duration::from_secs(600));
        assert_eq!(got, MIN_CHUNK_SIZE);
    }

    // A resumed upload must not send a chunk larger than what is actually
    // missing — the target size is a preference, the remaining gap is a hard
    // limit, or the client overshoots past the end of the file.
    #[test]
    fn clamp_to_remaining_never_exceeds_the_gap() {
        assert_eq!(clamp_to_remaining(4 << 20, 100), 100);
        assert_eq!(clamp_to_remaining(4 << 20, 8 << 20), 4 << 20);
    }

    /// Records what the upload path asked the ledger to do.
    #[derive(Default)]
    struct FakeStore {
        remembered: std::sync::Mutex<Vec<(String, String)>>,
        forgotten: std::sync::Mutex<Vec<String>>,
    }

    impl ChunkedSessionStore for FakeStore {
        fn remember(
            &self,
            _root: &str,
            server_path: &str,
            upload_id: &str,
            _metadata: ChunkedSessionMetadata<'_>,
        ) {
            self.remembered
                .lock()
                .unwrap()
                .push((server_path.to_string(), upload_id.to_string()));
        }
        fn forget(&self, _root: &str, server_path: &str) {
            self.forgotten.lock().unwrap().push(server_path.to_string());
        }
    }

    fn api_for(url: String) -> (AuthedApi, tempfile::TempDir) {
        crate::api::authed_for(url, "test-token")
    }

    // A resumed upload places its chunk at the offset the server's
    // missing_ranges names directly (ADR-0066) — nothing is derived from a
    // chunk_size or chunk count, so there is nothing to get wrong for a file
    // that does not divide evenly.
    #[tokio::test]
    async fn resume_places_a_chunk_at_the_offset_the_server_names() {
        let content = b"0123456789ab"; // 12 bytes; bytes [5,12) missing
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("uneven.bin");
        std::fs::File::create(&path)
            .unwrap()
            .write_all(content)
            .unwrap();

        let mut server = mockito::Server::new_async().await;
        server
            .mock("GET", "/api/v1/sync-roots/r1/uploads/u1/status")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"missing_ranges":[[5,12]]}"#)
            .create_async()
            .await;
        let chunk = server
            .mock("PUT", "/api/v1/sync-roots/r1/uploads/u1/chunks/5")
            .match_body(mockito::Matcher::Exact("56789ab".to_string()))
            .with_status(200)
            .expect(1)
            .create_async()
            .await;
        server
            .mock("POST", "/api/v1/sync-roots/r1/uploads/u1/complete")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"server_version":7,"content_hash":"ab12"}"#)
            .create_async()
            .await;

        let (api, _api_db) = api_for(server.url());
        let sem = tokio::sync::Semaphore::new(4);
        let outcome = upload_file_chunked(
            &api,
            &sem,
            ChunkedUploadRequest {
                root_server_id: "r1",
                server_path: "/uneven.bin",
                local_path: &path,
                mtime: SystemTime::UNIX_EPOCH,
                known_server_version: -1,
                existing_upload_id: Some("u1".to_string()),
                snapshot_id: "snapshot-1",
                sessions: &FakeStore::default(),
                content_hash: b"",
            },
        )
        .await
        .expect("chunked upload");

        assert!(
            matches!(outcome, UploadOutcome::Uploaded(_)),
            "unexpected outcome"
        );
        chunk.assert_async().await;
    }

    // A fresh session must be recorded before any chunk goes out, so an
    // interrupted upload can resume it — and dropped once the session is
    // finished, so the next upload of the same file does not resume a session
    // that has already been assembled.
    #[tokio::test]
    async fn a_session_is_remembered_when_opened_and_forgotten_when_finished() {
        let content = b"abcdefghij"; // 10 bytes, one chunk
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("mod.pak");
        std::fs::File::create(&path)
            .unwrap()
            .write_all(content)
            .unwrap();

        let mut server = mockito::Server::new_async().await;
        server
            .mock("POST", "/api/v1/sync-roots/r1/uploads")
            .with_status(201)
            .with_header("content-type", "application/json")
            .with_body(r#"{"upload_id":"u-new"}"#)
            .create_async()
            .await;
        server
            .mock("PUT", "/api/v1/sync-roots/r1/uploads/u-new/chunks/0")
            .with_status(200)
            .create_async()
            .await;
        server
            .mock("POST", "/api/v1/sync-roots/r1/uploads/u-new/complete")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"server_version":3,"content_hash":"beef"}"#)
            .create_async()
            .await;

        let store = FakeStore::default();
        let (api, _api_db) = api_for(server.url());
        let sem = tokio::sync::Semaphore::new(4);
        upload_file_chunked(
            &api,
            &sem,
            ChunkedUploadRequest {
                root_server_id: "r1",
                server_path: "/mod.pak",
                local_path: &path,
                mtime: SystemTime::UNIX_EPOCH,
                known_server_version: -1,
                existing_upload_id: None,
                snapshot_id: "snapshot-1",
                sessions: &store,
                content_hash: &[0xAB, 0xCD],
            },
        )
        .await
        .expect("chunked upload");

        assert_eq!(
            store.remembered.lock().unwrap().as_slice(),
            &[("/mod.pak".to_string(), "u-new".to_string())],
        );
        assert_eq!(
            store.forgotten.lock().unwrap().as_slice(),
            &["/mod.pak".to_string()]
        );
    }

    // A session the server failed for good replays that failure until the row
    // expires. Resuming into it would burn hours of retries on an answer that
    // cannot change.
    #[tokio::test]
    async fn a_permanently_failed_session_is_abandoned_for_a_fresh_one() {
        let content = b"fresh start";
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("retry.bin");
        std::fs::File::create(&path)
            .unwrap()
            .write_all(content)
            .unwrap();

        let mut server = mockito::Server::new_async().await;
        server
            .mock("GET", "/api/v1/sync-roots/r1/uploads/u1/status")
            .match_query(mockito::Matcher::Any)
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"status":"failed","error":{"code":"CONTENT_HASH_MISMATCH"}}"#)
            .create_async()
            .await;
        let init = server
            .mock("POST", "/api/v1/sync-roots/r1/uploads")
            .with_status(201)
            .with_header("content-type", "application/json")
            .with_body(r#"{"upload_id":"u2"}"#)
            .expect(1)
            .create_async()
            .await;
        server
            .mock("PUT", "/api/v1/sync-roots/r1/uploads/u2/chunks/0")
            .with_status(200)
            .create_async()
            .await;
        server
            .mock("POST", "/api/v1/sync-roots/r1/uploads/u2/complete")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"server_version":4,"content_hash":"beef"}"#)
            .create_async()
            .await;

        let store = FakeStore::default();
        let (api, _api_db) = api_for(server.url());
        let sem = tokio::sync::Semaphore::new(4);
        let outcome = upload_file_chunked(
            &api,
            &sem,
            ChunkedUploadRequest {
                root_server_id: "r1",
                server_path: "/retry.bin",
                local_path: &path,
                mtime: SystemTime::UNIX_EPOCH,
                known_server_version: -1,
                existing_upload_id: Some("u1".to_string()),
                snapshot_id: "snapshot-1",
                sessions: &store,
                content_hash: b"",
            },
        )
        .await
        .expect("upload");

        match outcome {
            UploadOutcome::Uploaded(resp) => assert_eq!(resp.server_version, 4),
            UploadOutcome::Conflict { .. } => panic!("expected Uploaded"),
        }
        init.assert_async().await;
        assert!(
            store
                .forgotten
                .lock()
                .unwrap()
                .iter()
                .any(|p| p == "/retry.bin"),
            "the failed session was not forgotten",
        );
    }

    fn request<'a>(
        path: &'a Path,
        existing: Option<&str>,
        store: &'a FakeStore,
    ) -> ChunkedUploadRequest<'a> {
        ChunkedUploadRequest {
            root_server_id: "r1",
            server_path: "/big.bin",
            local_path: path,
            mtime: SystemTime::UNIX_EPOCH,
            known_server_version: -1,
            existing_upload_id: existing.map(str::to_string),
            snapshot_id: "snapshot-1",
            sessions: store,
            content_hash: b"",
        }
    }

    // The server swept the session this ledger entry names. Asking about
    // it again answers 404 forever, so this same attempt drops it and opens a
    // fresh session instead of failing.
    #[tokio::test]
    async fn a_session_the_server_no_longer_knows_is_replaced_by_a_fresh_one() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("big.bin");
        std::fs::File::create(&path)
            .unwrap()
            .write_all(b"payload")
            .unwrap();

        let mut server = mockito::Server::new_async().await;
        server
            .mock("GET", "/api/v1/sync-roots/r1/uploads/u-old/status")
            .with_status(404)
            .with_header("content-type", "application/json")
            .with_body(r#"{"code":"UPLOAD_SESSION_NOT_FOUND_OR_EXPIRED","error":"gone"}"#)
            .expect(1)
            .create_async()
            .await;
        let init = server
            .mock("POST", "/api/v1/sync-roots/r1/uploads")
            .with_status(201)
            .with_header("content-type", "application/json")
            .with_body(r#"{"upload_id":"u-new"}"#)
            .expect(1)
            .create_async()
            .await;
        server
            .mock("PUT", "/api/v1/sync-roots/r1/uploads/u-new/chunks/0")
            .with_status(200)
            .create_async()
            .await;
        server
            .mock("POST", "/api/v1/sync-roots/r1/uploads/u-new/complete")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"server_version":2,"content_hash":"beef"}"#)
            .create_async()
            .await;

        let store = FakeStore::default();
        let (api, _api_db) = api_for(server.url());
        let sem = tokio::sync::Semaphore::new(4);
        let outcome = upload_file_chunked(&api, &sem, request(&path, Some("u-old"), &store))
            .await
            .expect("a gone session must not fail the upload");

        assert!(matches!(outcome, UploadOutcome::Uploaded(_)));
        init.assert_async().await;
        assert_eq!(
            store.remembered.lock().unwrap().as_slice(),
            &[("/big.bin".to_string(), "u-new".to_string())],
        );
    }

    // A session that expires while its chunks are going out cannot be
    // finished. The ledger entry goes, so the next attempt starts fresh
    // instead of resuming into the same 410.
    #[tokio::test]
    async fn a_session_that_expires_mid_transfer_is_forgotten() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("big.bin");
        std::fs::File::create(&path)
            .unwrap()
            .write_all(b"payload")
            .unwrap();

        let mut server = mockito::Server::new_async().await;
        server
            .mock("GET", "/api/v1/sync-roots/r1/uploads/u1/status")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"missing_ranges":[[0,7]]}"#)
            .create_async()
            .await;
        server
            .mock("PUT", "/api/v1/sync-roots/r1/uploads/u1/chunks/0")
            .with_status(410)
            .with_header("content-type", "application/json")
            .with_body(r#"{"code":"UPLOAD_SESSION_EXPIRED","error":"expired"}"#)
            .create_async()
            .await;

        let store = FakeStore::default();
        let (api, _api_db) = api_for(server.url());
        let sem = tokio::sync::Semaphore::new(4);
        let err = upload_file_chunked(&api, &sem, request(&path, Some("u1"), &store))
            .await
            .expect_err("an expired session cannot complete");

        assert!(err.is::<UploadSessionGone>(), "{err:#}");
        assert_eq!(
            store.forgotten.lock().unwrap().as_slice(),
            &["/big.bin".to_string()]
        );
    }
}
