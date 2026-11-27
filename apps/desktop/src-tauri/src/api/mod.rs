use anyhow::{anyhow, Result};
use reqwest::{Client, StatusCode};
use serde::{Deserialize, Serialize};
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use tokio::sync::RwLock as AsyncLock;

use crate::db::LocalDb;

mod auth;
mod authed;
mod sync_roots;
mod shared_mounts;
mod files;
mod chunked;
pub(crate) mod delta;
pub(crate) mod shares;

pub use authed::AuthedApi;
pub(crate) use chunked::{await_assembly, UploadSessionGone, ASSEMBLY_POLL_WAIT};
#[cfg(test)]
pub(crate) use authed::tests::authed_for;

// ─── Transfer Stats ───────────────────────────────────────────────────────────

/// Cumulative byte counters shared across the API client and any spawned
/// transfer tasks. A separate sampler task in `main.rs` reads these once a
/// second to derive throughput for the bottom status bar.
#[derive(Default)]
pub struct TransferStats {
    pub upload_bytes:   AtomicU64,
    pub download_bytes: AtomicU64,
}

impl TransferStats {
    pub fn add_upload(&self, n: u64) {
        self.upload_bytes.fetch_add(n, Ordering::Relaxed);
    }
    pub fn add_download(&self, n: u64) {
        self.download_bytes.fetch_add(n, Ordering::Relaxed);
    }
    pub fn snapshot(&self) -> (u64, u64) {
        (
            self.upload_bytes.load(Ordering::Relaxed),
            self.download_bytes.load(Ordering::Relaxed),
        )
    }
}

// ─── Response Types ───────────────────────────────────────────────────────────

/// Response returned by the server after a successful file upload.
#[derive(Debug, Deserialize)]
pub struct UploadResponse {
    pub server_version: i64,
    pub content_hash:   String, // hex-encoded BLAKE3
}

/// What a complete request answered. The server no longer decides inside the
/// request: it either hands back a verdict it already held, or accepts the
/// assembly and leaves the caller to wait for one.
#[derive(Debug)]
pub enum CompleteAck {
    Accepted,
    Settled(UploadOutcome),
}

/// Returned by `init_chunked_upload`.
#[derive(Debug, Deserialize)]
pub struct ChunkedUploadSession {
    pub upload_id: String,
}

/// Returned by `complete_chunked_upload`.
#[derive(Debug, Deserialize)]
pub struct ChunkedCompleteResponse {
    pub server_version: i64,
    pub content_hash:   String,
}

/// The terminal verdict of a server-side assembly, as the status endpoint
/// reports it. `kind` mirrors the coordinator's three outcomes:
/// `ok` | `version_mismatch` | `conflict`.
#[derive(Debug, Deserialize)]
pub struct ChunkedUploadResult {
    pub kind:           String,
    #[serde(default)]
    pub server_version: i64,
    #[serde(default)]
    pub content_hash:   String,
    #[serde(default)]
    pub conflict_path:  Option<String>,
}

/// Why an assembly failed for good, as a translatable code.
#[derive(Debug, Deserialize)]
pub struct ChunkedUploadFailure {
    pub code: String,
}

/// Returned by `chunked_upload_status`.
#[derive(Debug, Deserialize)]
pub struct ChunkedUploadStatus {
    /// [start, end) byte ranges still missing, in the order the server
    /// happens to return them (ADR-0066) — a resuming client places its next
    /// chunks inside these, not by index. Absent (defaults to empty) once the
    /// session has moved past accepting chunks — a poll for the assembly
    /// verdict has no use for it.
    #[serde(default)]
    pub missing_ranges: Vec<(u64, u64)>,
    /// Where the session stands: `open` while chunks are still arriving, then
    /// `queued` | `running` while the server assembles, then `completed` |
    /// `failed`. Absent from servers without async chunked-upload assembly.
    #[serde(default)]
    pub status:         Option<String>,
    /// Set once `status` is `completed`.
    #[serde(default)]
    pub result:         Option<ChunkedUploadResult>,
    /// Set once `status` is `failed`.
    #[serde(default)]
    pub error:          Option<ChunkedUploadFailure>,
}

/// Result of an upload attempt — either the file was accepted or the server
/// detected a version conflict that the client must resolve first.
#[derive(Debug)]
pub enum UploadOutcome {
    Uploaded(UploadResponse),
    /// Server returned 409: its current version differs from `If-Match`.
    Conflict {
        server_version: i64,
        server_hash:    String, // hex-encoded BLAKE3
        /// Server-side path where the rejected upload was preserved so the
        /// user can reconcile via the web UI. `None` if the server failed
        /// to move the staging bytes.
        conflict_path:  Option<String>,
    },
}

/// Result of a delete attempt.
#[derive(Debug, PartialEq, Eq)]
pub enum DeleteOutcome {
    /// The file is gone server-side — either this call removed it, or it was
    /// already absent (404 is success for a delete).
    Deleted,
    /// Server returned 409: someone wrote a newer version than the one we
    /// meant to delete. Deleting anyway would discard their change silently.
    Conflict { server_version: i64 },
}

/// Result of a move attempt. Conflict indicates that another client already
/// moved or modified the file: the server-side current path may differ from
/// what the local client expected.
#[derive(Debug)]
pub enum MoveOutcome {
    Moved,
    Conflict {
        server_version: i64,
        current_path:   String,
        conflict_path:  Option<String>,
    },
}

#[derive(Debug, Deserialize)]
pub struct MeResponse {
    pub id:         String,
    pub avatar_url: Option<String>,
}

#[derive(Debug, Deserialize)]
pub struct LoginResponse {
    pub access_token:  Option<String>,
    pub requires_2fa:  bool,
    pub session_token: Option<String>,
}

#[derive(Debug, Deserialize)]
pub struct TokenResponse {
    pub access_token: String,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ServerSyncRoot {
    pub id:   String,
    pub name: String,
    pub path: String,
}

/// Embedded giver profile on a Shared Mount (ADR-0009/0011).
#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct MountOwner {
    pub id:         String,
    pub username:   String,
    pub avatar_url: Option<String>,
}

/// Recipient-side view of an incoming user-share (ADR-0011). `id` is the
/// share_id; the desktop stores it as `server_root_id` on a `kind='mount'`
/// sync-root row and routes REST calls to `/shared-mounts/{id}`.
#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ServerMount {
    pub id:        String,
    pub name:      String,
    pub path:      String,
    #[serde(default)]
    pub is_file:   bool,
    #[serde(default)]
    pub can_write: bool,
    pub expires_at: Option<String>,
    pub owner:     MountOwner,
}

#[derive(Debug, Deserialize, Serialize, Clone)]
pub struct ServerFileEntry {
    pub id:             String,
    pub path:           String,
    pub size:           i64,
    pub mtime:          String,
    pub content_hash:   Option<String>,         // hex-encoded BLAKE3
    pub is_deleted:     bool,
    pub updated_at:     String,                 // RFC3339
    pub server_version: Option<i64>,
    /// True when the entry is an empty directory rather than a file.
    /// Server liefert das Feld mit `omitempty`, also Default false.
    #[serde(default)]
    pub is_dir:         bool,
    /// Monotonic changes-feed cursor (ADR-0036), present on both the owned
    /// sync-roots and the Shared-Mounts `/changes` endpoint -- the two share
    /// one feed contract.
    #[serde(default)]
    pub seq:            i64,
}

/// A single ADR-0065 prefix-move event standing in for an entire
/// directory-move subtree: the server rewrites the affected `files` rows
/// without bumping their `seq` and emits exactly this one entry instead, so
/// the client recomputes the prefix change on its own rows rather than
/// seeing one entry per descendant.
#[derive(Debug, Deserialize, Clone)]
pub struct PrefixMoveEvent {
    pub old_prefix: String,
    pub new_prefix: String,
    pub seq:        i64,
}

/// One page of the changes-feed (ADR-0036), owned sync-root and Shared Mount
/// alike. `next_cursor` is present only when there is more past this page --
/// absent means the caller has caught up. `moves` (ADR-0065) carries any
/// directory-move prefix events in this window; empty on a page with none.
#[derive(Debug, Deserialize, Clone)]
pub struct ChangesPage {
    pub changes:     Vec<ServerFileEntry>,
    #[serde(default)]
    pub moves:       Vec<PrefixMoveEvent>,
    #[serde(default)]
    pub next_cursor: Option<i64>,
}

/// One result row of the ADR-0062 stat endpoint. `found = false` means the
/// server has no record of the path at all -- distinct from `found = true,
/// is_deleted = true`, which means the server had it and tombstoned it. The
/// retry sweep must never conflate the two: only the latter is "apply the
/// tombstone"; the former is "nothing to do, drop the retry row".
#[derive(Debug, Deserialize, Clone, Default)]
pub struct FileStatEntry {
    pub path:           String,
    #[serde(default)]
    pub found:          bool,
    #[serde(default)]
    pub id:             String,
    #[serde(default)]
    pub size:           i64,
    #[serde(default)]
    pub mtime:          String,
    #[serde(default)]
    pub content_hash:   String,
    #[serde(default)]
    pub is_deleted:     bool,
    #[serde(default)]
    pub is_dir:         bool,
    #[serde(default)]
    pub updated_at:     String,
    #[serde(default)]
    pub server_version: i64,
}

#[derive(Debug, Deserialize)]
struct FilesStatResponse {
    results: Vec<FileStatEntry>,
}

// ─── Client ───────────────────────────────────────────────────────────────────

pub struct ApiClient {
    pub base_url:     String,
    pub(super) http:  Client,
    pub access_token: Option<String>,
    pub stats:        Arc<TransferStats>,
}

impl ApiClient {
    pub fn new(base_url: impl Into<String>, stats: Arc<TransferStats>) -> Self {
        let http = Client::builder()
            .timeout(std::time::Duration::from_secs(60))
            .connect_timeout(std::time::Duration::from_secs(10))
            .build()
            .expect("failed to build reqwest client");
        Self {
            base_url: base_url.into().trim_end_matches('/').to_string(),
            http,
            access_token: None,
            stats,
        }
    }

    pub fn set_token(&mut self, token: impl Into<String>) {
        self.access_token = Some(token.into());
    }

    pub fn clear_token(&mut self) {
        self.access_token = None;
    }

    pub fn is_authenticated(&self) -> bool {
        self.access_token.is_some()
    }

    pub(super) fn bearer(&self) -> Result<String> {
        self.access_token
            .as_ref()
            .map(|t| format!("Bearer {t}"))
            .ok_or_else(|| anyhow::Error::new(NotAuthenticated))
    }

    /// Capture a snapshot for concurrent use.  Returns `Err` if not authenticated.
    pub fn snapshot(&self) -> Result<ApiSnapshot> {
        Ok(ApiSnapshot {
            http:     self.http.clone(),
            base_url: self.base_url.clone(),
            bearer:   self.bearer()?,
            stats:    self.stats.clone(),
        })
    }
}

// ─── Snapshot for concurrent use ─────────────────────────────────────────────

/// A cheap clone of the API client's credentials + HTTP client that can be
/// used from multiple concurrent tasks without holding the `ApiClient` mutex.
/// `reqwest::Client` is `Arc`-backed internally so cloning is O(1).
#[derive(Clone)]
pub struct ApiSnapshot {
    pub(super) http:     Client,
    pub(super) base_url: String,
    pub(super) bearer:   String,
    pub(super) stats:    Arc<TransferStats>,
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

/// A download whose headers have arrived but whose body is still on the wire.
///
/// `server_version` is read off the response rather than derived from the
/// bytes, because a range request never sees the whole file: it describes the
/// file the window was cut from, not the window.
pub(crate) struct DownloadStream {
    res:                reqwest::Response,
    stats:              Arc<TransferStats>,
    /// True when the server honoured a `Range` request (HTTP 206). A server
    /// that ignores the header answers 200 with the whole file, and a caller
    /// serving a window must not mistake one for the other.
    pub is_partial:     bool,
    /// `X-Server-Version`, used to pin a multi-request hydration to one
    /// version of the file.
    pub server_version: Option<i64>,
}

impl DownloadStream {
    fn new(res: reqwest::Response, stats: Arc<TransferStats>) -> Self {
        let header = |name: &str| {
            res.headers().get(name).and_then(|v| v.to_str().ok()).map(str::to_owned)
        };
        let server_version = header("X-Server-Version").and_then(|v| v.parse().ok());
        Self {
            is_partial: res.status() == StatusCode::PARTIAL_CONTENT,
            res,
            stats,
            server_version,
        }
    }

    /// Drain the body into `out`, crediting every byte as it arrives.
    /// Returns the number of bytes written.
    pub async fn drain_to<W>(self, out: &mut W) -> Result<u64>
    where
        W: tokio::io::AsyncWrite + Unpin,
    {
        stream_body_counted(self.res, &self.stats, out).await
    }

    /// Collect the body into memory. Only for bodies that are bounded by
    /// something other than the file size — a range window, in practice.
    /// Anything that lands on disk uses [`DownloadStream::drain_to`].
    pub async fn collect(self) -> Result<bytes::Bytes> {
        let mut buf = Vec::new();
        stream_body_counted(self.res, &self.stats, &mut buf).await?;
        Ok(bytes::Bytes::from(buf))
    }
}

/// Format a `(offset, length)` window as a `Range` header value. HTTP ranges
/// are inclusive on both ends, so the last byte is `offset + length - 1`.
pub(super) fn range_header(offset: u64, length: u64) -> String {
    format!("bytes={}-{}", offset, offset + length.max(1) - 1)
}

/// Stream a download body straight into `out`, crediting the bytes to the
/// live counter as they arrive. Peak memory is one transport piece, not the
/// file size — which is the whole point: a multi-GB download must not cost
/// multi-GB of RAM on its way to disk.
///
/// Crediting per piece rather than at the end also keeps the 1 Hz throughput
/// sampler honest; `Response::bytes()` credits the whole file in one jump,
/// which showed nothing for the duration of a large transfer and then a
/// single implausible spike.
pub(super) async fn stream_body_counted<W>(
    res:   reqwest::Response,
    stats: &TransferStats,
    out:   &mut W,
) -> Result<u64>
where
    W: tokio::io::AsyncWrite + Unpin,
{
    use futures_util::StreamExt as _;
    use tokio::io::AsyncWriteExt as _;

    let mut written = 0u64;
    let mut stream  = res.bytes_stream();
    while let Some(piece) = stream.next().await {
        let piece = piece?;
        out.write_all(&piece).await?;
        stats.add_download(piece.len() as u64);
        written += piece.len() as u64;
    }
    out.flush().await?;
    Ok(written)
}

/// How much of a chunk is handed to the transport at a time. Small enough
/// that the 1 Hz sampler sees continuous progress on a slow link, large
/// enough that the per-piece bookkeeping stays in the noise.
pub(super) const UPLOAD_PROGRESS_PIECE: usize = 64 * 1024;

/// Split an in-memory chunk into transport-sized pieces. `Bytes::slice` is a
/// refcount bump, so this neither copies nor allocates the payload again.
pub(super) fn upload_pieces(data: bytes::Bytes) -> Vec<bytes::Bytes> {
    (0..data.len())
        .step_by(UPLOAD_PROGRESS_PIECE)
        .map(|off| data.slice(off..(off + UPLOAD_PROGRESS_PIECE).min(data.len())))
        .collect()
}

/// Build the REST resource base for a file operation. Owned roots route to
/// `/sync-roots/{id}`, activated Shared Mounts to `/shared-mounts/{share_id}`
/// (ADR-0011). Every file API method takes `is_mount` and calls this so the
/// only per-kind difference is the namespace, never the sub-path.
pub(super) fn resource_base(base_url: &str, is_mount: bool, id: &str) -> String {
    let ns = if is_mount { "shared-mounts" } else { "sync-roots" };
    format!("{base_url}/api/v1/{ns}/{id}")
}

/// Typed 401 marker (ADR-0025). `check_status` still formats the exact same
/// `"{op}: HTTP 401"` text a plain non-success status would get — so
/// `retry::is_transient_error`'s `contains("HTTP 401")` string check keeps
/// working unchanged — but a caller that needs to react to auth expiry
/// specifically downcasts to this instead of matching the message.
#[derive(Debug)]
pub(crate) struct Unauthorized(String);

impl std::fmt::Display for Unauthorized {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.0)
    }
}
impl std::error::Error for Unauthorized {}

/// Marker for "the refresh attempt itself failed — the session is genuinely
/// gone". Distinct from [`Unauthorized`] so a caller can tell "please show a
/// re-login prompt" apart from a plain 401 (which `with_auth_retry` already
/// resolves internally by refreshing and replaying).
#[derive(Debug)]
pub(crate) struct SessionExpired;

impl std::fmt::Display for SessionExpired {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "session expired")
    }
}
impl std::error::Error for SessionExpired {}

/// No access token at all: the user never logged in, or a rejected refresh
/// already cleared it. Typed for the same reason as [`SessionExpired`].
#[derive(Debug)]
pub(crate) struct NotAuthenticated;

impl std::fmt::Display for NotAuthenticated {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "not authenticated")
    }
}
impl std::error::Error for NotAuthenticated {}

/// Whether `e` is about the session rather than about the request. Such a
/// failure says nothing about the file the request was for, so the sync
/// engine must not count it against that file: no backoff, no parking.
pub(crate) fn is_auth_failure(e: &anyhow::Error) -> bool {
    e.is::<Unauthorized>() || e.is::<SessionExpired>() || e.is::<NotAuthenticated>()
}

/// A rejection the server explained with its `{error, code}` body. Displays
/// as the English `error` text; `code` is the stable key the frontend
/// translates, so commands surfacing it to the user must keep it intact.
#[derive(Debug)]
pub(crate) struct ServerError {
    pub message: String,
    pub code:    Option<String>,
}

impl ServerError {
    /// Read the error body of a non-success response. Falls back to
    /// `fallback` when the body is missing or not the expected JSON shape.
    pub(super) async fn from_response(res: reqwest::Response, fallback: &str) -> Self {
        let body: serde_json::Value = res.json().await.unwrap_or_default();
        Self {
            message: body["error"].as_str().unwrap_or(fallback).to_string(),
            code:    body["code"].as_str().map(str::to_string),
        }
    }
}

/// [`check_status`] for responses whose failure reaches the user: a rejection
/// keeps the server's `code` so the UI can translate it. The message stays the
/// `"{op}: HTTP {status}"` text `check_status` produces, so string-based retry
/// classification is unaffected, and a 401 is still the typed [`Unauthorized`].
pub(super) async fn ensure_success(res: reqwest::Response, op: &str) -> Result<reqwest::Response> {
    let status = res.status();
    if status.is_success() || status == StatusCode::UNAUTHORIZED {
        check_status(status, op)?;
        return Ok(res);
    }
    let fallback = format!("{op}: HTTP {status}");
    let code = ServerError::from_response(res, "").await.code;
    Err(ServerError { message: fallback, code }.into())
}

impl std::fmt::Display for ServerError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.message)
    }
}
impl std::error::Error for ServerError {}

pub(super) fn check_status(status: StatusCode, op: &str) -> Result<()> {
    if status.is_success() {
        Ok(())
    } else if status == StatusCode::UNAUTHORIZED {
        Err(anyhow::Error::new(Unauthorized(format!("{op}: HTTP {status}"))))
    } else {
        Err(anyhow!("{op}: HTTP {status}"))
    }
}

/// Run `op` once; on a typed [`Unauthorized`] (401), refresh the access
/// token exactly once and replay `op` — no double-refresh, no string-
/// matching on "401" (ADR-0025). Concurrent callers collapse into a single
/// refresh: the access token the op ran with is the epoch, and a caller that
/// finds it already changed replays instead of refreshing again (ADR-0042). On success the new token is persisted to
/// `db` so the next process/command sees it too. On refresh failure the
/// token is cleared (in-memory + `db`), `on_session_expired` runs, and the
/// caller gets a typed [`SessionExpired`] instead of the refresh endpoint's
/// own error — callers that need a specific user-facing message downcast to
/// it; everyone else just propagates it like any other error.
///
/// `on_session_expired` is injected (rather than this function taking an
/// `AppHandle` and emitting `session_expired` itself) so the retry/refresh
/// logic is unit-testable without a running app — same shape as
/// `sync::failure`'s `record_failure`/`materialise_conflict`.
pub async fn with_auth_retry<T, F, Fut>(
    client:             &AsyncLock<ApiClient>,
    db:                 &LocalDb,
    on_session_expired: impl FnOnce(),
    op:                 F,
) -> Result<T>
where
    F:   Fn() -> Fut,
    Fut: std::future::Future<Output = Result<T>>,
{
    // The access token this op runs with doubles as the refresh epoch: if it
    // has changed by the time the op comes back 401, someone else already
    // refreshed and there is nothing to do but replay.
    let token_before = client.read().await.access_token.clone();

    match op().await {
        Err(e) if e.is::<Unauthorized>() => {
            let mut c = client.write().await;

            // Refreshing again here would replay a refresh token the server
            // already rotated away, and its rejection would be read as an
            // ended session — logging the user out mid-work while a perfectly
            // good session exists. Reading the epoch under the write lock is
            // what makes concurrent 401s collapse into a single refresh.
            if c.access_token != token_before {
                drop(c);
                return op().await;
            }

            let stored_rt = db.config_get("refresh_token").ok().flatten();
            let ident = crate::client_id::current(db);
            let refresh_result = c.refresh_token(stored_rt.as_deref(), Some(&ident)).await;
            let new_rt = match refresh_result {
                Ok(new_rt) => new_rt,
                // Only a rejected refresh token means the session is really
                // gone. Any other failure — transport error, 5xx, proxy
                // hiccup — leaves the stored credentials valid, so keep them
                // and let the caller's retry/backoff have another go. Clearing
                // them here would log the user out on every network blip.
                Err(e) if !e.is::<Unauthorized>() => return Err(e),
                Err(_) => {
                    c.clear_token();
                    drop(c);
                    let _ = db.config_set("access_token", "");
                    on_session_expired();
                    return Err(anyhow::Error::new(SessionExpired));
                }
            };
            let new_token = c.access_token.clone().unwrap_or_default();
            let _ = db.config_set("access_token", &new_token);
            if let Some(rt) = new_rt {
                let _ = db.config_set("refresh_token", &rt);
            }
            drop(c);

            op().await
        }
        other => other,
    }
}

/// Extrahiert den Wert eines bestimmten Cookies aus den Set-Cookie-Headern.
pub(super) fn extract_cookie_value(headers: &reqwest::header::HeaderMap, name: &str) -> Option<String> {
    headers.get_all("set-cookie")
        .iter()
        .filter_map(|v| v.to_str().ok())
        .find_map(|s| {
            s.split(';')
             .next()
             .and_then(|kv| {
                 let mut parts = kv.splitn(2, '=');
                 let n = parts.next()?.trim();
                 let v = parts.next()?.trim();
                 if n == name { Some(v.to_string()) } else { None }
             })
        })
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn upload_pieces_splits_on_the_transport_size_and_keeps_the_remainder() {
        let data   = bytes::Bytes::from(vec![0u8; UPLOAD_PROGRESS_PIECE * 2 + 7]);
        let pieces = upload_pieces(data);
        let sizes: Vec<usize> = pieces.iter().map(|p| p.len()).collect();
        assert_eq!(sizes, vec![UPLOAD_PROGRESS_PIECE, UPLOAD_PROGRESS_PIECE, 7]);
    }

    #[test]
    fn upload_pieces_preserves_the_payload_byte_for_byte() {
        let data: Vec<u8> = (0..UPLOAD_PROGRESS_PIECE + 3).map(|i| i as u8).collect();
        let rejoined: Vec<u8> = upload_pieces(bytes::Bytes::from(data.clone()))
            .iter()
            .flat_map(|p| p.iter().copied())
            .collect();
        assert_eq!(rejoined, data);
    }

    #[test]
    fn upload_pieces_of_an_empty_chunk_yields_no_pieces() {
        assert!(upload_pieces(bytes::Bytes::new()).is_empty());
    }

    #[tokio::test]
    async fn stream_body_counted_writes_the_body_and_credits_every_byte() {
        let mut server = mockito::Server::new_async().await;
        let _m = server.mock("GET", "/blob")
            .with_status(200)
            .with_body("hello world")
            .create_async().await;

        let stats = TransferStats::default();
        let res   = reqwest::get(format!("{}/blob", server.url())).await.expect("request");
        let mut sink = Vec::new();
        let written = stream_body_counted(res, &stats, &mut sink).await.expect("stream");

        assert_eq!(sink, b"hello world");
        assert_eq!(written, "hello world".len() as u64);
        assert_eq!(stats.snapshot(), (0, "hello world".len() as u64));
    }

    fn snap_for(url: String) -> ApiSnapshot {
        ApiSnapshot {
            http:     Client::new(),
            base_url: url,
            bearer:   "Bearer test-token".to_string(),
            stats:    Arc::new(TransferStats::default()),
        }
    }

    fn test_client(url: String) -> ApiClient {
        let mut c = ApiClient::new(url, Arc::new(TransferStats::default()));
        c.set_token("old-token");
        c
    }

    fn test_db() -> LocalDb {
        let tmp = tempfile::tempdir().unwrap();
        LocalDb::open(tmp.path().join("db.sqlite")).unwrap()
    }

    // A successful op must never touch the refresh endpoint — with_auth_retry
    // is a passthrough on the happy path.
    #[tokio::test]
    async fn with_auth_retry_succeeds_without_retry_when_op_succeeds() {
        let client = AsyncLock::new(test_client("http://unused.invalid".to_string()));
        let db = test_db();
        let calls = std::cell::Cell::new(0u32);
        let expired = std::cell::Cell::new(false);

        let result = with_auth_retry(&client, &db, || expired.set(true), || {
            calls.set(calls.get() + 1);
            async { Ok::<_, anyhow::Error>(42) }
        }).await;

        assert_eq!(result.unwrap(), 42);
        assert_eq!(calls.get(), 1, "op must not be retried on success");
        assert!(!expired.get());
    }

    // A 401 refreshes exactly once and replays op — the caller sees the
    // replay's result, not the original 401.
    #[tokio::test]
    async fn with_auth_retry_refreshes_once_and_replays_on_unauthorized() {
        let mut server = mockito::Server::new_async().await;
        let refresh_mock = server
            .mock("POST", "/api/v1/auth/refresh")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"access_token":"fresh-token"}"#)
            .expect(1)
            .create_async()
            .await;

        let client = AsyncLock::new(test_client(server.url()));
        let db = test_db();
        let calls = std::cell::Cell::new(0u32);
        let expired = std::cell::Cell::new(false);

        let result = with_auth_retry(&client, &db, || expired.set(true), || {
            let n = calls.get() + 1;
            calls.set(n);
            async move {
                if n == 1 {
                    Err(anyhow::Error::new(Unauthorized("op: HTTP 401".to_string())))
                } else {
                    Ok(99)
                }
            }
        }).await;

        assert_eq!(result.unwrap(), 99);
        assert_eq!(calls.get(), 2, "op must be replayed exactly once after refresh");
        assert!(!expired.get(), "a successful refresh must not signal session-expired");
        assert_eq!(client.read().await.access_token.as_deref(), Some("fresh-token"));
        assert_eq!(db.config_get("access_token").unwrap().as_deref(), Some("fresh-token"));
        refresh_mock.assert_async().await;
    }

    // When the refresh itself fails, the token is cleared everywhere,
    // on_session_expired fires, and the caller gets a typed SessionExpired
    // instead of the raw refresh error.
    #[tokio::test]
    async fn with_auth_retry_clears_token_and_signals_session_expired_when_refresh_fails() {
        let mut server = mockito::Server::new_async().await;
        let refresh_mock = server
            .mock("POST", "/api/v1/auth/refresh")
            .with_status(401)
            .expect(1)
            .create_async()
            .await;

        let client = AsyncLock::new(test_client(server.url()));
        let db = test_db();
        let expired = std::cell::Cell::new(false);

        let result = with_auth_retry(&client, &db, || expired.set(true), || async {
            Err::<i32, _>(anyhow::Error::new(Unauthorized("op: HTTP 401".to_string())))
        }).await;

        let err = result.unwrap_err();
        assert!(err.is::<SessionExpired>(), "unexpected error: {err}");
        assert!(expired.get());
        assert_eq!(client.read().await.access_token, None);
        assert_eq!(db.config_get("access_token").unwrap().as_deref(), Some(""));
        refresh_mock.assert_async().await;
    }

    // A refresh that fails for any reason other than a rejected refresh token
    // — transport error, 5xx, proxy hiccup — must not end the session. The
    // stored credentials are still valid; only the attempt failed. Clearing
    // them here is what logs the user out several times a day on a flaky link.
    #[tokio::test]
    async fn with_auth_retry_keeps_session_when_refresh_fails_transiently() {
        let mut server = mockito::Server::new_async().await;
        let refresh_mock = server
            .mock("POST", "/api/v1/auth/refresh")
            .with_status(502)
            .expect(1)
            .create_async()
            .await;

        let client = AsyncLock::new(test_client(server.url()));
        let db = test_db();
        db.config_set("access_token", "old-token").unwrap();
        db.config_set("refresh_token", "stored-refresh").unwrap();
        let expired = std::cell::Cell::new(false);

        let result = with_auth_retry(&client, &db, || expired.set(true), || async {
            Err::<i32, _>(anyhow::Error::new(Unauthorized("op: HTTP 401".to_string())))
        }).await;

        let err = result.unwrap_err();
        assert!(!err.is::<SessionExpired>(), "transient refresh failure must not expire the session: {err}");
        assert!(!expired.get(), "no session-expired signal on a transient failure");
        assert_eq!(client.read().await.access_token.as_deref(), Some("old-token"), "access token must survive");
        assert_eq!(db.config_get("access_token").unwrap().as_deref(), Some("old-token"));
        assert_eq!(db.config_get("refresh_token").unwrap().as_deref(), Some("stored-refresh"), "refresh token must survive");
        refresh_mock.assert_async().await;
    }

    // The server rotates the refresh token on every /auth/refresh call. If
    // with_auth_retry doesn't persist the rotated cookie, the next refresh
    // replays the already-deleted old token and the user is wrongly logged
    // out — this is the regression the fix addresses.
    #[tokio::test]
    async fn with_auth_retry_persists_rotated_refresh_token() {
        let mut server = mockito::Server::new_async().await;
        let refresh_mock = server
            .mock("POST", "/api/v1/auth/refresh")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_header("set-cookie", "refresh_token=rotated-refresh; HttpOnly; Path=/")
            .with_body(r#"{"access_token":"fresh-token"}"#)
            .expect(1)
            .create_async()
            .await;

        let client = AsyncLock::new(test_client(server.url()));
        let db = test_db();
        db.config_set("refresh_token", "stale-refresh").unwrap();
        let calls = std::cell::Cell::new(0u32);

        let result = with_auth_retry(&client, &db, || {}, || {
            let n = calls.get() + 1;
            calls.set(n);
            async move {
                if n == 1 {
                    Err(anyhow::Error::new(Unauthorized("op: HTTP 401".to_string())))
                } else {
                    Ok(99)
                }
            }
        }).await;

        assert_eq!(result.unwrap(), 99);
        assert_eq!(db.config_get("refresh_token").unwrap().as_deref(), Some("rotated-refresh"));
        refresh_mock.assert_async().await;
    }

    // Two operations that go 401 in the same moment must produce exactly one
    // refresh. The server rotates on refresh — the old refresh token is gone —
    // so a second refresh replaying the same stored token is rejected, and the
    // client wrongly concludes the session ended. That is the "logged out
    // several times a day" symptom.
    #[tokio::test]
    async fn concurrent_unauthorized_ops_refresh_only_once() {
        let mut server = mockito::Server::new_async().await;
        let refresh_mock = server
            .mock("POST", "/api/v1/auth/refresh")
            .match_header("cookie", "refresh_token=stored-refresh")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_header("set-cookie", "refresh_token=rotated-refresh; HttpOnly; Path=/")
            .with_body(r#"{"access_token":"fresh-token"}"#)
            .expect(1)
            .create_async()
            .await;

        let client = AsyncLock::new(test_client(server.url()));
        let db = test_db();
        db.config_set("refresh_token", "stored-refresh").unwrap();
        let expired = std::cell::Cell::new(false);

        // Both ops report 401 before either is allowed to refresh — that is the
        // overlap the race needs.
        let gate = Arc::new(tokio::sync::Barrier::new(2));
        let calls_a = std::cell::Cell::new(0u32);
        let calls_b = std::cell::Cell::new(0u32);

        let (result_a, result_b) = tokio::join!(
            with_auth_retry(&client, &db, || expired.set(true), || {
                let n = calls_a.get() + 1;
                calls_a.set(n);
                let gate = gate.clone();
                async move {
                    if n == 1 {
                        gate.wait().await;
                        Err(anyhow::Error::new(Unauthorized("op-a: HTTP 401".to_string())))
                    } else {
                        Ok("a")
                    }
                }
            }),
            with_auth_retry(&client, &db, || expired.set(true), || {
                let n = calls_b.get() + 1;
                calls_b.set(n);
                let gate = gate.clone();
                async move {
                    if n == 1 {
                        gate.wait().await;
                        Err(anyhow::Error::new(Unauthorized("op-b: HTTP 401".to_string())))
                    } else {
                        Ok("b")
                    }
                }
            }),
        );

        assert_eq!(result_a.unwrap(), "a");
        assert_eq!(result_b.unwrap(), "b");
        assert!(!expired.get(), "one refresh served both ops — no session-expired signal");
        assert_eq!(db.config_get("refresh_token").unwrap().as_deref(), Some("rotated-refresh"));
        assert_eq!(client.read().await.access_token.as_deref(), Some("fresh-token"));
        // .expect(1) above: a second refresh call fails this assertion.
        refresh_mock.assert_async().await;
    }

    // If the replay after a *successful* refresh still comes back 401, that
    // must not trigger a second refresh — the replay's own error propagates
    // as-is (not remapped to SessionExpired).
    #[tokio::test]
    async fn with_auth_retry_does_not_double_refresh_when_replay_still_unauthorized() {
        let mut server = mockito::Server::new_async().await;
        let refresh_mock = server
            .mock("POST", "/api/v1/auth/refresh")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"access_token":"fresh-token"}"#)
            .expect(1)
            .create_async()
            .await;

        let client = AsyncLock::new(test_client(server.url()));
        let db = test_db();
        let calls = std::cell::Cell::new(0u32);
        let expired = std::cell::Cell::new(false);

        let result = with_auth_retry(&client, &db, || expired.set(true), || {
            calls.set(calls.get() + 1);
            async { Err::<i32, _>(anyhow::Error::new(Unauthorized("op: HTTP 401".to_string()))) }
        }).await;

        assert!(result.unwrap_err().is::<Unauthorized>(), "replay's own 401 must propagate unchanged, not as SessionExpired");
        assert_eq!(calls.get(), 2, "op runs at most twice: original + one replay");
        assert!(!expired.get(), "refresh succeeded — no session-expired signal");
        refresh_mock.assert_async().await; // exactly 1 call — .expect(1) above fails the test otherwise
    }

    // resource_base must route owned roots to /sync-roots and activated
    // Shared Mounts to /shared-mounts, with no other path difference (ADR-0011).
    #[test]
    fn resource_base_routes_by_kind() {
        assert_eq!(
            resource_base("http://h", false, "r1"),
            "http://h/api/v1/sync-roots/r1"
        );
        assert_eq!(
            resource_base("http://h", true, "share-1"),
            "http://h/api/v1/shared-mounts/share-1"
        );
    }

    // A mount upload must hit /shared-mounts/{id}/files (PUT), proving the
    // is_mount flag threads through ApiSnapshot::upload_file end to end.
    #[tokio::test]
    async fn upload_routes_to_mount_namespace_when_is_mount() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("PUT", "/api/v1/shared-mounts/share-1/files?path=%2Fx.txt")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"server_version":1,"content_hash":"ab"}"#)
            .create_async()
            .await;

        let tmp = std::env::temp_dir().join(format!("rt-mount-{}.txt", uuid::Uuid::new_v4()));
        std::fs::write(&tmp, b"hi").unwrap();
        let snap = snap_for(server.url());
        let out = snap
            .upload_file("share-1", "/x.txt", &tmp, std::time::SystemTime::now(), -1, true)
            .await
            .expect("mount upload ok");
        let _ = std::fs::remove_file(&tmp);
        assert!(matches!(out, UploadOutcome::Uploaded(_)));
        mock.assert_async().await;
    }

    /// Tracer bullet for the upload path: ApiSnapshot::create_directory must
    /// send a POST to /api/v1/sync-roots/{root_id}/directories with
    /// `{"path": "..."}`, treat 201 as success, and return the server's file
    /// id so the caller can persist it for rename detection.
    #[tokio::test]
    async fn create_directory_posts_path_to_directories_endpoint() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("POST", "/api/v1/sync-roots/root-1/directories")
            .match_header("authorization", "Bearer test-token")
            .match_body(mockito::Matcher::Json(json!({"path": "/empty-folder"})))
            .with_status(201)
            .with_header("content-type", "application/json")
            .with_body(r#"{"id":"srv-dir-1","path":"/empty-folder","is_dir":true,"size":0}"#)
            .create_async()
            .await;

        let snap = snap_for(server.url());
        let id = snap.create_directory("root-1", "/empty-folder", false)
            .await
            .expect("create_directory soll bei 201 Ok zurueckgeben");
        assert_eq!(id, "srv-dir-1", "die Server-File-Id muss zurueckgegeben werden");

        mock.assert_async().await;
    }
}
