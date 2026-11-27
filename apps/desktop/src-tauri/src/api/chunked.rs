use anyhow::Result;
use futures_util::StreamExt as _;
use reqwest::StatusCode;
use std::time::{Duration, Instant, SystemTime};

use super::{
    ApiSnapshot, CompleteAck, UploadOutcome, UploadResponse,
    ChunkedUploadSession, ChunkedUploadStatus, ChunkedUploadResult, ChunkedCompleteResponse,
    ServerError, check_status, upload_pieces,
};

/// How long the server is asked to hold one status request. Comfortably under
/// what a reverse proxy tolerates, so the answer comes from the server rather
/// than from a gateway timeout.
pub(crate) const ASSEMBLY_POLL_WAIT: Duration = Duration::from_secs(30);

/// Error codes with which the server says the upload session itself is gone —
/// expired, or swept after expiry. Told apart by code rather than by status:
/// the same 404 also means "sync root not found", and that is not a reason to
/// throw a session away.
const SESSION_GONE_CODES: &[&str] = &[
    "UPLOAD_SESSION_EXPIRED",
    "UPLOAD_SESSION_NOT_FOUND",
    "UPLOAD_SESSION_NOT_FOUND_OR_EXPIRED",
];

/// The server no longer holds this upload session. Nothing about the
/// session can be resumed, so the caller drops it from the ledger and opens a
/// fresh one. Displays the same `"{op}: HTTP {status}"` text `check_status`
/// would, so retry classification treats it like any other 404/410.
#[derive(Debug)]
pub(crate) struct UploadSessionGone(String);

impl std::fmt::Display for UploadSessionGone {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.0)
    }
}
impl std::error::Error for UploadSessionGone {}

/// [`check_status`] for requests addressed to one session: a 404/410 the
/// server explains with a session code becomes the typed [`UploadSessionGone`].
async fn check_session_status(res: reqwest::Response, op: &str) -> Result<reqwest::Response> {
    let status = res.status();
    if status != StatusCode::NOT_FOUND && status != StatusCode::GONE {
        check_status(status, op)?;
        return Ok(res);
    }
    let message = format!("{op}: HTTP {status}");
    let code = ServerError::from_response(res, "").await.code;
    if code.as_deref().is_some_and(|c| SESSION_GONE_CODES.contains(&c)) {
        return Err(anyhow::Error::new(UploadSessionGone(message)));
    }
    Err(anyhow::anyhow!(message))
}

/// Total time to wait for one assembly before giving up on this attempt. The
/// session outlives it by far, so the transfer's normal retry simply resumes
/// the waiting.
const MAX_ASSEMBLY_WAIT: Duration = Duration::from_secs(60 * 60);

/// Map a finished assembly onto the outcome the sync engine already knows.
fn assembly_outcome(result: Option<ChunkedUploadResult>) -> Result<UploadOutcome> {
    let Some(result) = result else {
        anyhow::bail!("server reported a finished upload without a result");
    };
    match result.kind.as_str() {
        "ok" => Ok(UploadOutcome::Uploaded(UploadResponse {
            server_version: result.server_version,
            content_hash:   result.content_hash,
        })),
        "version_mismatch" | "conflict" => Ok(UploadOutcome::Conflict {
            server_version: result.server_version,
            server_hash:    result.content_hash,
            conflict_path:  result.conflict_path,
        }),
        other => anyhow::bail!("server reported an unknown upload result: {other}"),
    }
}

/// Wait for the server to finish assembling, then report the same outcome a
/// plain upload would have produced. Assembling tens of gigabytes takes
/// minutes, so `poll` is a long poll (see [`ASSEMBLY_POLL_WAIT`]) rather than a
/// busy loop, and the total budget is generous: the session stays answerable
/// for a day, and asking again costs nothing. Each poll is its own request, so
/// the caller can hand every one of them a current token.
pub(crate) async fn await_assembly<F, Fut>(poll: F) -> Result<UploadOutcome>
where
    F:   Fn() -> Fut,
    Fut: std::future::Future<Output = Result<ChunkedUploadStatus>>,
{
    let deadline = Instant::now() + MAX_ASSEMBLY_WAIT;
    loop {
        let status = poll().await?;
        match status.status.as_deref() {
            Some("completed") => return assembly_outcome(status.result),
            Some("failed") => {
                let code = status.error.map(|e| e.code)
                    .unwrap_or_else(|| "UPLOAD_FAILED".to_string());
                anyhow::bail!("server could not assemble the upload: {code}");
            }
            Some("queued") | Some("running") => {}
            // `open` cannot follow an accepted complete, and a server
            // without async chunked-upload assembly sends no status at all. Neither is worth
            // waiting an hour for.
            _ => anyhow::bail!("server reports no assembly state for this upload"),
        }
        if Instant::now() >= deadline {
            anyhow::bail!("server is still assembling the upload after {MAX_ASSEMBLY_WAIT:?}");
        }
    }
}

impl ApiSnapshot {
    /// Initiate a new multi-part upload session. Returns the new `upload_id`.
    pub async fn init_chunked_upload(
        &self,
        root_id:              &str,
        server_path:          &str,
        total_size:           u64,
        mtime:                SystemTime,
        known_server_version: i64,
        content_hash: &[u8],
    ) -> Result<ChunkedUploadSession> {
        let url = format!("{}/api/v1/sync-roots/{}/uploads", self.base_url, root_id);
        let mtime_rfc = chrono::DateTime::<chrono::Utc>::from(mtime)
            .format("%Y-%m-%dT%H:%M:%S%.3fZ").to_string();

        let mut body = serde_json::json!({
            "path":       server_path,
            "total_size": total_size,
            "mtime":      mtime_rfc,
        });
        if known_server_version >= 0 {
            body["if_match"] = serde_json::json!(known_server_version);
        }
        // The server cannot check content it did not produce unless we state
        // what we are sending: without this the whole verification step at
        // complete is skipped and a truncated transfer is committed together
        // with a server-computed hash that agrees with the damage.
        if !content_hash.is_empty() {
            body["content_hash"] = serde_json::json!(hex::encode(content_hash));
        }

        let res = self.http
            .post(&url)
            .header("Authorization", &self.bearer)
            .json(&body)
            .send()
            .await?;
        check_status(res.status(), "init chunked upload")?;
        Ok(res.json().await?)
    }

    /// Upload one chunk at `offset`. `chunk_data` is the raw bytes for this
    /// chunk — its length is the chunk's length (ADR-0066), sent as
    /// `Content-Length` below, since chunk size varies per chunk now.
    pub async fn upload_chunk(
        &self,
        root_id:    &str,
        upload_id:  &str,
        offset:     u64,
        chunk_data: bytes::Bytes,
    ) -> Result<()> {
        let url = format!(
            "{}/api/v1/sync-roots/{}/uploads/{}/chunks/{}",
            self.base_url, root_id, upload_id, offset
        );
        // Hand the chunk to the transport in pieces and credit each one as it
        // goes out. Crediting the whole chunk after the request returned made
        // the 1 Hz throughput sampler read 0 B/s for seconds at a time and
        // then spike by a full chunk — on a slow link the displayed rate was
        // pure noise. reqwest polls the body as the socket drains, so the
        // per-piece credit tracks real wire progress.
        let chunk_len = chunk_data.len();
        let stats     = self.stats.clone();
        let stream    = futures_util::stream::iter(upload_pieces(chunk_data))
            .inspect(move |piece: &bytes::Bytes| stats.add_upload(piece.len() as u64))
            .map(Ok::<bytes::Bytes, std::io::Error>);

        let res = self.http
            .put(&url)
            .header("Authorization", &self.bearer)
            .header("Content-Length", chunk_len)
            .body(reqwest::Body::wrap_stream(stream))
            .send()
            .await?;
        check_session_status(res, "upload chunk").await?;
        Ok(())
    }

    /// Query the session: which chunks the server has received (for resume),
    /// and how the server-side assembly is going. With `wait` the request is a
    /// long poll — the server holds it until the session changes state or the
    /// time is up.
    pub async fn chunked_upload_status(
        &self,
        root_id:   &str,
        upload_id: &str,
        wait:      Option<Duration>,
    ) -> Result<ChunkedUploadStatus> {
        let mut url = format!(
            "{}/api/v1/sync-roots/{}/uploads/{}/status",
            self.base_url, root_id, upload_id
        );
        if let Some(wait) = wait {
            url = format!("{url}?wait={}s", wait.as_secs());
        }
        let mut req = self.http.get(&url).header("Authorization", &self.bearer);
        if let Some(wait) = wait {
            // The request must outlive the server's own hold, or the client
            // gives up on an answer that was about to arrive.
            req = req.timeout(wait + Duration::from_secs(30));
        }
        let res = req.send().await?;
        let res = check_session_status(res, "chunked upload status").await?;
        Ok(res.json().await?)
    }

    /// Hand the assembly to the server. It only accepts the work and answers
    /// `202` — reading and hashing tens of gigabytes takes minutes, which no
    /// request survives; while it happened inside the request, a reverse proxy
    /// answered `504` even though every chunk had already landed. A session
    /// that already holds a verdict answers with it directly. Waiting for an
    /// accepted assembly is [`await_assembly`], deliberately a
    /// separate call: this request holds an upload slot, and the wait must not.
    pub async fn complete_chunked_upload(
        &self,
        root_id:   &str,
        upload_id: &str,
    ) -> Result<CompleteAck> {
        let url = format!(
            "{}/api/v1/sync-roots/{}/uploads/{}/complete",
            self.base_url, root_id, upload_id
        );
        let res = self.http
            .post(&url)
            .header("Authorization", &self.bearer)
            .header("Content-Length", 0)
            .send()
            .await?;

        if res.status() == StatusCode::ACCEPTED {
            return Ok(CompleteAck::Accepted);
        }

        if res.status() == StatusCode::CONFLICT {
            let body: serde_json::Value = res.json().await.unwrap_or_default();
            // Not every 409 is a version conflict: chunks the server never
            // received land here too, and reporting that as a conflict would
            // send the client off to download a file it just uploaded.
            if body["code"] == "CHUNKS_MISSING" {
                anyhow::bail!("server is missing chunks for this upload");
            }
            return Ok(CompleteAck::Settled(UploadOutcome::Conflict {
                server_version: body["server_version"].as_i64().unwrap_or(-1),
                server_hash:    body["server_hash"].as_str().unwrap_or("").to_string(),
                conflict_path:  body["conflict_path"].as_str().map(String::from),
            }));
        }

        let res = check_session_status(res, "complete chunked upload").await?;
        let resp: ChunkedCompleteResponse = res.json().await?;
        Ok(CompleteAck::Settled(UploadOutcome::Uploaded(UploadResponse {
            server_version: resp.server_version,
            content_hash:   resp.content_hash,
        })))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::api::{CompleteAck, TransferStats};
    use reqwest::Client;
    use std::sync::Arc;

    fn snap_for(url: String) -> ApiSnapshot {
        ApiSnapshot {
            http:     Client::new(),
            base_url: url,
            bearer:   "Bearer test-token".to_string(),
            stats:    Arc::new(TransferStats::default()),
        }
    }

    #[tokio::test]
    async fn init_chunked_upload_sends_if_match_only_when_known_version_is_non_negative() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("POST", "/api/v1/sync-roots/root-1/uploads")
            .match_body(mockito::Matcher::PartialJson(serde_json::json!({ "if_match": 5 })))
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"upload_id":"u1"}"#)
            .create_async()
            .await;

        let session = snap_for(server.url())
            .init_chunked_upload("root-1", "/big.bin", 3_000_000, SystemTime::UNIX_EPOCH, 5, b"")
            .await
            .expect("init ok");

        assert_eq!(session.upload_id, "u1");
        mock.assert_async().await;
    }

    #[tokio::test]
    async fn init_chunked_upload_omits_if_match_for_new_file() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("POST", "/api/v1/sync-roots/root-1/uploads")
            .match_request(|req| {
                let body = String::from_utf8_lossy(req.body().unwrap());
                !body.contains("if_match")
            })
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"upload_id":"u2"}"#)
            .create_async()
            .await;

        snap_for(server.url())
            .init_chunked_upload("root-1", "/new.bin", 500, SystemTime::UNIX_EPOCH, -1, b"")
            .await
            .expect("init ok");

        mock.assert_async().await;
    }

    #[tokio::test]
    async fn upload_chunk_tracks_uploaded_bytes() {
        let mut server = mockito::Server::new_async().await;
        server
            .mock("PUT", "/api/v1/sync-roots/root-1/uploads/u1/chunks/0")
            .with_status(204)
            .create_async()
            .await;

        let snap = snap_for(server.url());
        snap.upload_chunk("root-1", "u1", 0, bytes::Bytes::from_static(b"hello world"))
            .await
            .expect("upload_chunk ok");

        let (uploaded, _) = snap.stats.snapshot();
        assert_eq!(uploaded, 11);
    }

    #[tokio::test]
    async fn chunked_upload_status_parses_missing_ranges() {
        let mut server = mockito::Server::new_async().await;
        server
            .mock("GET", "/api/v1/sync-roots/root-1/uploads/u1/status")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"missing_ranges":[[20,30]]}"#)
            .create_async()
            .await;

        let status = snap_for(server.url())
            .chunked_upload_status("root-1", "u1", None)
            .await
            .expect("status ok");

        assert_eq!(status.missing_ranges, vec![(20, 30)]);
    }

    #[tokio::test]
    async fn complete_chunked_upload_returns_uploaded_on_success() {
        let mut server = mockito::Server::new_async().await;
        server
            .mock("POST", "/api/v1/sync-roots/root-1/uploads/u1/complete")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"server_version":9,"content_hash":"abc123"}"#)
            .create_async()
            .await;

        let ack = snap_for(server.url())
            .complete_chunked_upload("root-1", "u1")
            .await
            .expect("complete ok");

        match ack {
            CompleteAck::Settled(UploadOutcome::Uploaded(resp)) => {
                assert_eq!(resp.server_version, 9);
                assert_eq!(resp.content_hash, "abc123");
            }
            other => panic!("expected a settled upload, got {other:?}"),
        }
    }

    #[tokio::test]
    async fn complete_chunked_upload_returns_conflict_on_409() {
        let mut server = mockito::Server::new_async().await;
        server
            .mock("POST", "/api/v1/sync-roots/root-1/uploads/u1/complete")
            .with_status(409)
            .with_header("content-type", "application/json")
            .with_body(r#"{"server_version":12,"server_hash":"deadbeef","conflict_path":"/big.bin.conflict-1"}"#)
            .create_async()
            .await;

        let ack = snap_for(server.url())
            .complete_chunked_upload("root-1", "u1")
            .await
            .expect("complete ok");

        match ack {
            CompleteAck::Settled(UploadOutcome::Conflict { server_version, server_hash, conflict_path }) => {
                assert_eq!(server_version, 12);
                assert_eq!(server_hash, "deadbeef");
                assert_eq!(conflict_path.as_deref(), Some("/big.bin.conflict-1"));
            }
            other => panic!("expected a settled conflict, got {other:?}"),
        }
    }

    // The server has no way to verify content it did not produce unless the
    // client states its hash up front — the complete step skips verification
    // entirely when the session carries none.
    #[tokio::test]
    async fn init_chunked_upload_sends_the_content_hash() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("POST", "/api/v1/sync-roots/root-1/uploads")
            .match_body(mockito::Matcher::PartialJson(
                serde_json::json!({ "content_hash": "abcd" }),
            ))
            .with_status(201)
            .with_header("content-type", "application/json")
            .with_body(r#"{"upload_id":"u1","chunk_size":5,"total_chunks":1}"#)
            .create_async()
            .await;

        let snap = snap_for(server.url());
        snap.init_chunked_upload("root-1", "/big.bin", 5, SystemTime::UNIX_EPOCH, -1, &[0xAB, 0xCD])
            .await
            .expect("init");

        mock.assert_async().await;
    }

    /// Mock a session that is accepted and then reported as finished.
    async fn accepted_then(status_body: &str) -> mockito::ServerGuard {
        let mut server = mockito::Server::new_async().await;
        server
            .mock("POST", "/api/v1/sync-roots/root-1/uploads/u1/complete")
            .with_status(202)
            .with_header("content-type", "application/json")
            .with_body(r#"{"upload_id":"u1","status":"queued"}"#)
            .create_async()
            .await;
        server
            .mock("GET", "/api/v1/sync-roots/root-1/uploads/u1/status")
            .match_query(mockito::Matcher::Any)
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(status_body)
            .create_async()
            .await;
        server
    }

    // The server no longer finishes the job inside the request: it accepts,
    // answers 202, and the verdict is picked up from the status endpoint.
    #[tokio::test]
    async fn complete_chunked_upload_reports_acceptance_without_waiting() {
        let server = accepted_then(r#"{"received_chunks":[0],"missing_chunks":[],"total_chunks":1}"#).await;

        let ack = snap_for(server.url())
            .complete_chunked_upload("root-1", "u1")
            .await
            .expect("complete ok");

        assert!(matches!(ack, CompleteAck::Accepted), "got {ack:?}");
    }

    #[tokio::test]
    async fn await_assembly_reports_a_finished_assembly_as_uploaded() {
        let server = accepted_then(
            r#"{"received_chunks":[0],"missing_chunks":[],"total_chunks":1,
                 "status":"completed",
                 "result":{"kind":"ok","server_version":9,"content_hash":"abc123"}}"#,
        ).await;

        let snap = snap_for(server.url());
        let outcome = await_assembly(|| snap.chunked_upload_status("root-1", "u1", Some(ASSEMBLY_POLL_WAIT)))
            .await
            .expect("await ok");

        match outcome {
            UploadOutcome::Uploaded(resp) => {
                assert_eq!(resp.server_version, 9);
                assert_eq!(resp.content_hash, "abc123");
            }
            UploadOutcome::Conflict { .. } => panic!("expected Uploaded"),
        }
    }

    // A conflict decided by the worker has to reach the same code path as one
    // decided inside the request, loser copy and all.
    #[tokio::test]
    async fn await_assembly_maps_a_conflict_decided_by_the_worker() {
        let server = accepted_then(
            r#"{"received_chunks":[0],"missing_chunks":[],"total_chunks":1,
                 "status":"completed",
                 "result":{"kind":"conflict","server_version":12,
                           "content_hash":"deadbeef",
                           "conflict_path":"/big.bin.conflict-1"}}"#,
        ).await;

        let snap = snap_for(server.url());
        let outcome = await_assembly(|| snap.chunked_upload_status("root-1", "u1", Some(ASSEMBLY_POLL_WAIT)))
            .await
            .expect("await ok");

        match outcome {
            UploadOutcome::Conflict { server_version, server_hash, conflict_path } => {
                assert_eq!(server_version, 12);
                assert_eq!(server_hash, "deadbeef");
                assert_eq!(conflict_path.as_deref(), Some("/big.bin.conflict-1"));
            }
            UploadOutcome::Uploaded(_) => panic!("expected Conflict"),
        }
    }

    // A failed assembly is an error, not an outcome — the transfer must not
    // record a server version it never got.
    #[tokio::test]
    async fn await_assembly_reports_a_failed_assembly_as_an_error() {
        let server = accepted_then(
            r#"{"received_chunks":[0],"missing_chunks":[],"total_chunks":1,
                 "status":"failed","error":{"code":"CONTENT_HASH_MISMATCH"}}"#,
        ).await;

        let snap = snap_for(server.url());
        let err = await_assembly(|| snap.chunked_upload_status("root-1", "u1", Some(ASSEMBLY_POLL_WAIT)))
            .await
            .expect_err("a failed assembly must not look like success");
        assert!(err.to_string().contains("CONTENT_HASH_MISMATCH"), "{err}");
    }

    // Chunks the server never received also arrive as 409. Treating that as a
    // version conflict would send the client off to download what it uploaded.
    #[tokio::test]
    async fn complete_chunked_upload_does_not_read_missing_chunks_as_a_conflict() {
        let mut server = mockito::Server::new_async().await;
        server
            .mock("POST", "/api/v1/sync-roots/root-1/uploads/u1/complete")
            .with_status(409)
            .with_header("content-type", "application/json")
            .with_body(r#"{"code":"CHUNKS_MISSING","error":"missing 2 chunk(s)"}"#)
            .create_async()
            .await;

        let err = snap_for(server.url())
            .complete_chunked_upload("root-1", "u1")
            .await
            .expect_err("missing chunks are not a conflict");
        assert!(err.to_string().contains("missing chunks"), "{err}");
    }

    async fn status_answering(status: usize, code: &str) -> mockito::ServerGuard {
        let mut server = mockito::Server::new_async().await;
        server
            .mock("GET", "/api/v1/sync-roots/root-1/uploads/u1/status")
            .with_status(status)
            .with_header("content-type", "application/json")
            .with_body(format!(r#"{{"code":"{code}","error":"x"}}"#))
            .create_async()
            .await;
        server
    }

    // A session the server swept is gone for good — typed, so the
    // caller drops it instead of asking about it again on every retry.
    #[tokio::test]
    async fn a_swept_or_expired_session_reads_as_gone() {
        for (status, code) in [
            (404, "UPLOAD_SESSION_NOT_FOUND_OR_EXPIRED"),
            (404, "UPLOAD_SESSION_NOT_FOUND"),
            (410, "UPLOAD_SESSION_EXPIRED"),
        ] {
            let server = status_answering(status, code).await;
            let err = snap_for(server.url())
                .chunked_upload_status("root-1", "u1", None)
                .await
                .expect_err("no session, no status");
            assert!(err.is::<UploadSessionGone>(), "{status} {code}: {err}");
            assert!(err.to_string().contains(&format!("HTTP {status}")), "{err}");
        }
    }

    // The same 404 also means "no such sync root". That says nothing about
    // the session, which must not be thrown away over it.
    #[tokio::test]
    async fn a_missing_sync_root_is_not_a_gone_session() {
        let server = status_answering(404, "SYNC_ROOT_NOT_FOUND").await;
        let err = snap_for(server.url())
            .chunked_upload_status("root-1", "u1", None)
            .await
            .expect_err("404");
        assert!(!err.is::<UploadSessionGone>(), "{err}");
        assert!(err.to_string().contains("HTTP 404"), "{err}");
    }
}
