use anyhow::Result;
use futures_util::TryStreamExt;
use reqwest::StatusCode;
use std::path::Path;
use std::time::SystemTime;
use tokio_util::io::ReaderStream;

use super::{
    ApiClient, ApiSnapshot, DeleteOutcome, DownloadStream, UploadOutcome, UploadResponse,
    MoveOutcome, check_status, range_header, resource_base,
};

impl ApiClient {
    /// Start a download. `range` is a `(offset, length)` window; `None` asks
    /// for the whole file. The body is left on the wire for the caller to
    /// drain — see [`DownloadStream`].
    ///
    /// Bytes are credited to the shared counter as they arrive, including on
    /// this path: the FETCH_DATA hydration callback in `cloud_files::windows`
    /// uses the locked `ApiClient` rather than an `ApiSnapshot`, so without it
    /// on-demand downloads would be missing from the transfer-speed bar.
    pub async fn download_stream(
        &self,
        root_id:     &str,
        server_path: &str,
        is_mount:    bool,
        range:       Option<(u64, u64)>,
    ) -> Result<DownloadStream> {
        let url = format!(
            "{}/files/download?path={}",
            resource_base(&self.base_url, is_mount, root_id),
            urlencoding::encode(server_path)
        );

        let mut req = self.http.get(&url).header("Authorization", self.bearer()?);
        if let Some((offset, length)) = range {
            req = req.header("Range", range_header(offset, length));
        }
        let res = req.send().await?;

        check_status(res.status(), "download file")?;
        Ok(DownloadStream::new(res, self.stats.clone()))
    }
}

impl ApiSnapshot {
    /// Upload a file.  Returns `UploadOutcome::Uploaded` on success or
    /// `UploadOutcome::Conflict` when the server detects an ETag mismatch (409).
    /// All other HTTP errors propagate as `Err`.
    /// `known_server_version` is sent as `If-Match`; use -1 for new files.
    pub async fn upload_file(
        &self,
        root_id:              &str,
        server_path:          &str,
        local_path:           &Path,
        mtime:                SystemTime,
        known_server_version: i64,
        is_mount:             bool,
    ) -> Result<UploadOutcome> {
        let file      = tokio::fs::File::open(local_path).await?;
        let file_size = file.metadata().await?.len();
        let mtime_rfc = chrono::DateTime::<chrono::Utc>::from(mtime)
            .format("%Y-%m-%dT%H:%M:%S%.3fZ").to_string();

        // Wrap the file stream so each chunk read by reqwest gets its byte
        // length added to the live upload counter — gives the bottom-bar
        // sampler real-time throughput instead of one big jump at the end.
        let stats = self.stats.clone();
        let stream = ReaderStream::new(file).inspect_ok(move |chunk: &bytes::Bytes| {
            stats.add_upload(chunk.len() as u64);
        });
        let body   = reqwest::Body::wrap_stream(stream);
        let url    = format!(
            "{}/files?path={}",
            resource_base(&self.base_url, is_mount, root_id), urlencoding::encode(server_path)
        );

        let mut req = self.http
            .put(&url)
            .header("Authorization", &self.bearer)
            .header("X-File-Mtime", mtime_rfc)
            .header("Content-Length", file_size);

        if known_server_version >= 0 {
            req = req.header("If-Match", known_server_version.to_string());
        }

        let res = req.body(body).send().await?;

        if res.status() == StatusCode::CONFLICT {
            // Parse the server's conflict payload to get current state.
            let body: serde_json::Value = res.json().await.unwrap_or_default();
            return Ok(UploadOutcome::Conflict {
                server_version: body["server_version"].as_i64().unwrap_or(-1),
                server_hash:    body["server_hash"].as_str().unwrap_or("").to_string(),
                conflict_path:  body["conflict_path"].as_str().map(String::from),
            });
        }

        check_status(res.status(), "upload file")?;
        let resp: UploadResponse = res.json().await?;
        Ok(UploadOutcome::Uploaded(resp))
    }

    /// Start a download. `range` is a `(offset, length)` window; `None` asks
    /// for the whole file. See [`DownloadStream`].
    pub async fn download_stream(
        &self,
        root_id:     &str,
        server_path: &str,
        is_mount:    bool,
        range:       Option<(u64, u64)>,
    ) -> Result<DownloadStream> {
        let url = format!(
            "{}/files/download?path={}",
            resource_base(&self.base_url, is_mount, root_id), urlencoding::encode(server_path)
        );
        let mut req = self.http.get(&url).header("Authorization", &self.bearer);
        if let Some((offset, length)) = range {
            req = req.header("Range", range_header(offset, length));
        }
        let res = req.send().await?;
        check_status(res.status(), "download file")?;
        Ok(DownloadStream::new(res, self.stats.clone()))
    }

    /// Move a file server-side. `known_server_version` is sent as `If-Match`
    /// so the server can detect Move-Move-Konflikte; -1 disables the check.
    pub async fn move_file(
        &self,
        root_id:              &str,
        from_path:            &str,
        to_path:              &str,
        known_server_version: i64,
        is_mount:             bool,
    ) -> Result<MoveOutcome> {
        let url = format!("{}/files", resource_base(&self.base_url, is_mount, root_id));
        let mut req = self.http
            .patch(&url)
            .header("Authorization", &self.bearer)
            .json(&serde_json::json!({ "op": "move", "from": from_path, "to": to_path }));
        if known_server_version >= 0 {
            req = req.header("If-Match", known_server_version.to_string());
        }
        let res = req.send().await?;

        if res.status() == StatusCode::CONFLICT {
            let body: serde_json::Value = res.json().await.unwrap_or_default();
            return Ok(MoveOutcome::Conflict {
                server_version: body["server_version"].as_i64().unwrap_or(-1),
                current_path:   body["current_path"].as_str().unwrap_or(from_path).to_string(),
                conflict_path:  body["conflict_path"].as_str().map(String::from),
            });
        }

        check_status(res.status(), "move file")?;
        Ok(MoveOutcome::Moved)
    }

    /// Delete a file server-side. `known_server_version` is sent as `If-Match`
    /// so a parallel edit is not silently discarded; use -1 to delete
    /// unconditionally (nothing known to state).
    pub async fn delete_file(
        &self,
        root_id:              &str,
        server_path:          &str,
        known_server_version: i64,
        is_mount:             bool,
    ) -> Result<DeleteOutcome> {
        let url = format!(
            "{}/files?path={}",
            resource_base(&self.base_url, is_mount, root_id), urlencoding::encode(server_path)
        );

        let mut req = self.http.delete(&url).header("Authorization", &self.bearer);
        if known_server_version >= 0 {
            req = req.header("If-Match", known_server_version.to_string());
        }
        let res = req.send().await?;

        if res.status() == StatusCode::NOT_FOUND {
            return Ok(DeleteOutcome::Deleted);
        }
        if res.status() == StatusCode::CONFLICT {
            let body: serde_json::Value = res.json().await.unwrap_or_default();
            return Ok(DeleteOutcome::Conflict {
                server_version: body["server_version"].as_i64().unwrap_or(-1),
            });
        }
        check_status(res.status(), "delete file")?;
        Ok(DeleteOutcome::Deleted)
    }

    /// Create an empty directory on the server (`POST /directories`).
    /// Called by the upload path for is_dir entries that carry no file body.
    ///
    /// Returns the server's file id for the created directory row so the caller
    /// can persist it immediately. Without that id an empty directory moved
    /// before the next pull round-trip is not recognised as a rename (the
    /// detection keys off server_file_id) and surfaces as a delete. An
    /// empty string is returned when the response carries no id (e.g. a mount
    /// endpoint that omits it) — the caller treats that as "nothing to store".
    pub async fn create_directory(&self, root_id: &str, server_path: &str, is_mount: bool) -> Result<String> {
        let url = format!(
            "{}/directories",
            resource_base(&self.base_url, is_mount, root_id),
        );
        let res = self.http
            .post(&url)
            .header("Authorization", &self.bearer)
            .json(&serde_json::json!({ "path": server_path }))
            .send()
            .await?;
        check_status(res.status(), "create directory")?;
        // Best-effort id extraction: a missing/garbled body still counts as a
        // successful create (status already validated), it just leaves the id
        // to the lazy pull-backfill path.
        let id = res
            .json::<serde_json::Value>()
            .await
            .ok()
            .and_then(|v| v.get("id").and_then(|i| i.as_str()).map(String::from))
            .unwrap_or_default();
        Ok(id)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::api::{ApiClient, TransferStats};
    use reqwest::Client;
    use std::sync::Arc;

    fn client_for(url: String) -> ApiClient {
        let mut c = ApiClient::new(url, Arc::new(TransferStats::default()));
        c.set_token("test-token");
        c
    }

    fn write_tmp(bytes: &[u8]) -> std::path::PathBuf {
        let p = std::env::temp_dir().join(format!("rt-files-{}.txt", uuid::Uuid::new_v4()));
        std::fs::write(&p, bytes).unwrap();
        p
    }

    // Shared Mounts route through a different namespace, so the mount form of
    // the URL needs its own coverage.
    #[tokio::test]
    async fn client_download_stream_routes_a_mount_through_the_shared_mounts_namespace() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("GET", "/api/v1/shared-mounts/share-1/files/download?path=%2Fa.txt")
            .with_status(200)
            .with_body(b"payload-bytes".as_slice())
            .create_async()
            .await;

        let client = client_for(server.url());
        let bytes  = client.download_stream("share-1", "/a.txt", true, None)
            .await.unwrap()
            .collect().await.unwrap();

        assert_eq!(&bytes[..], b"payload-bytes");
        assert_eq!(client.stats.snapshot().1, "payload-bytes".len() as u64);
        mock.assert_async().await;
    }

    // The Cloud-Filter hydration asks for the window Explorer touched. Without
    // a Range header the server answers with the whole file, so a few-kilobyte
    // read of a multi-GB file pulls the whole file across the wire.
    #[tokio::test]
    async fn client_download_stream_requests_only_the_asked_for_window() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("GET", "/api/v1/sync-roots/r1/files/download?path=%2Fa.txt")
            .match_header("range", "bytes=2-5")
            .with_status(206)
            .with_header("Content-Range", "bytes 2-5/10")
            .with_body(b"2345".as_slice())
            .create_async()
            .await;

        let client = client_for(server.url());
        let stream = client
            .download_stream("r1", "/a.txt", false, Some((2, 4)))
            .await
            .unwrap();
        assert!(stream.is_partial, "a 206 must be recognisable as a window, not a whole file");

        let mut sink = Vec::new();
        let written  = stream.drain_to(&mut sink).await.unwrap();

        assert_eq!(sink, b"2345");
        assert_eq!(written, 4);
        assert_eq!(client.stats.snapshot().1, 4);
        mock.assert_async().await;
    }

    // A window carries no evidence about the version it was cut from, so that
    // has to travel in a header — it is what lets a multi-request hydration
    // notice that the file changed underneath it.
    #[tokio::test]
    async fn client_download_stream_exposes_the_server_version_header() {
        let mut server = mockito::Server::new_async().await;
        let _m = server
            .mock("GET", "/api/v1/sync-roots/r1/files/download?path=%2Fa.txt")
            .with_status(200)
            .with_header("X-Server-Version", "7")
            .with_body(b"body".as_slice())
            .create_async()
            .await;

        let client = client_for(server.url());
        let stream = client.download_stream("r1", "/a.txt", false, None).await.unwrap();

        assert!(!stream.is_partial, "a 200 is the whole file, not a window");
        assert_eq!(stream.server_version, Some(7));
    }


    fn snap_for(url: String) -> ApiSnapshot {
        ApiSnapshot {
            http:     Client::new(),
            base_url: url,
            bearer:   "Bearer test-token".to_string(),
            stats:    Arc::new(TransferStats::default()),
        }
    }

    #[tokio::test]
    async fn snapshot_upload_file_sends_if_match_and_returns_response() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("PUT", "/api/v1/sync-roots/r1/files?path=%2Fa.txt")
            .match_header("if-match", "5")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"server_version":6,"content_hash":"deadbeef"}"#)
            .create_async()
            .await;

        let tmp = write_tmp(b"snapshot upload body");
        let snap = snap_for(server.url());
        let out = snap.upload_file("r1", "/a.txt", &tmp, std::time::SystemTime::now(), 5, false)
            .await
            .unwrap();
        let _ = std::fs::remove_file(&tmp);
        match out {
            UploadOutcome::Uploaded(resp) => {
                assert_eq!(resp.server_version, 6);
                assert_eq!(resp.content_hash, "deadbeef");
            }
            UploadOutcome::Conflict { .. } => panic!("200 must be Uploaded"),
        }
        assert!(snap.stats.snapshot().0 > 0, "upload bytes must be counted");
        mock.assert_async().await;
    }

    #[tokio::test]
    async fn snapshot_upload_file_returns_conflict_on_409() {
        let mut server = mockito::Server::new_async().await;
        let _mock = server
            .mock("PUT", mockito::Matcher::Any)
            .with_status(409)
            .with_header("content-type", "application/json")
            .with_body(r#"{"server_version":8,"server_hash":"abc123","conflict_path":"/a.conflict.1"}"#)
            .create_async()
            .await;

        let tmp = write_tmp(b"x");
        let snap = snap_for(server.url());
        let out = snap.upload_file("r1", "/a.txt", &tmp, std::time::SystemTime::now(), 1, false)
            .await
            .unwrap();
        let _ = std::fs::remove_file(&tmp);
        match out {
            UploadOutcome::Conflict { server_version, server_hash, conflict_path } => {
                assert_eq!(server_version, 8);
                assert_eq!(server_hash, "abc123");
                assert_eq!(conflict_path, Some("/a.conflict.1".to_string()));
            }
            UploadOutcome::Uploaded(_) => panic!("409 must be Conflict"),
        }
    }

    // The sync engine downloads through an `ApiSnapshot`, so the streaming
    // entry point has to exist on both halves of the API surface.
    #[tokio::test]
    async fn snapshot_download_stream_drains_the_body_and_credits_it() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("GET", "/api/v1/sync-roots/r1/files/download?path=%2Fa.txt")
            .with_status(200)
            .with_header("X-Server-Version", "12")
            .with_body(b"snap-stream".as_slice())
            .create_async()
            .await;

        let snap   = snap_for(server.url());
        let stream = snap.download_stream("r1", "/a.txt", false, None).await.unwrap();
        assert_eq!(stream.server_version, Some(12));

        let mut sink = Vec::new();
        let written  = stream.drain_to(&mut sink).await.unwrap();

        assert_eq!(sink, b"snap-stream");
        assert_eq!(written, "snap-stream".len() as u64);
        assert_eq!(snap.stats.snapshot().1, "snap-stream".len() as u64);
        mock.assert_async().await;
    }

    #[tokio::test]
    async fn snapshot_move_file_returns_conflict_on_409() {
        let mut server = mockito::Server::new_async().await;
        let _mock = server
            .mock("PATCH", mockito::Matcher::Any)
            .with_status(409)
            .with_header("content-type", "application/json")
            .with_body(r#"{"server_version":2,"current_path":"/x"}"#)
            .create_async()
            .await;

        let snap = snap_for(server.url());
        let out = snap.move_file("r1", "/a", "/b", -1, false).await.unwrap();
        match out {
            MoveOutcome::Conflict { server_version, current_path, conflict_path } => {
                assert_eq!(server_version, 2);
                assert_eq!(current_path, "/x");
                assert_eq!(conflict_path, None, "missing field in the JSON body must fall back to None");
            }
            MoveOutcome::Moved => panic!("409 must be Conflict"),
        }
    }

    #[tokio::test]
    async fn snapshot_move_file_returns_conflict_with_conflict_path_on_409() {
        let mut server = mockito::Server::new_async().await;
        let _mock = server
            .mock("PATCH", mockito::Matcher::Any)
            .with_status(409)
            .with_header("content-type", "application/json")
            .with_body(r#"{"server_version":9,"current_path":"/moved-elsewhere","conflict_path":"/c.conflict.1"}"#)
            .create_async()
            .await;

        let snap = snap_for(server.url());
        let out = snap.move_file("r1", "/a", "/b", 1, false).await.unwrap();
        match out {
            MoveOutcome::Conflict { server_version, current_path, conflict_path } => {
                assert_eq!(server_version, 9);
                assert_eq!(current_path, "/moved-elsewhere");
                assert_eq!(conflict_path, Some("/c.conflict.1".to_string()));
            }
            MoveOutcome::Moved => panic!("409 must surface as Conflict, not Moved"),
        }
    }

    #[tokio::test]
    async fn snapshot_move_file_sends_if_match_when_version_known() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("PATCH", "/api/v1/sync-roots/r1/files")
            .match_header("if-match", "3")
            .match_body(mockito::Matcher::Json(serde_json::json!({"op": "move", "from": "/a", "to": "/b"})))
            .with_status(200)
            .create_async()
            .await;

        let snap = snap_for(server.url());
        let out = snap.move_file("r1", "/a", "/b", 3, false).await.unwrap();
        assert!(matches!(out, MoveOutcome::Moved));
        mock.assert_async().await;
    }

    #[tokio::test]
    async fn snapshot_move_file_omits_if_match_when_version_unknown() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("PATCH", "/api/v1/sync-roots/r1/files")
            .match_header("if-match", mockito::Matcher::Missing)
            .with_status(200)
            .create_async()
            .await;

        let snap = snap_for(server.url());
        snap.move_file("r1", "/a", "/b", -1, false).await.unwrap();
        mock.assert_async().await;
    }

    #[tokio::test]
    async fn snapshot_delete_file_treats_404_as_already_gone() {
        let mut server = mockito::Server::new_async().await;
        let _mock = server
            .mock("DELETE", mockito::Matcher::Any)
            .with_status(404)
            .create_async()
            .await;

        let snap = snap_for(server.url());
        let outcome = snap.delete_file("r1", "/gone.txt", -1, false).await
            .expect("404 must be treated as success");
        assert_eq!(outcome, DeleteOutcome::Deleted);
    }

    /// A parallel edit must not be discarded by a delete that was decided
    /// against an older version -- the server answers 409 and the caller
    /// rebases instead.
    #[tokio::test]
    async fn snapshot_delete_file_surfaces_a_version_conflict() {
        let mut server = mockito::Server::new_async().await;
        let _mock = server
            .mock("DELETE", mockito::Matcher::Any)
            .with_status(409)
            .with_body(r#"{"error":"version conflict","server_version":12}"#)
            .create_async()
            .await;

        let snap = snap_for(server.url());
        let outcome = snap.delete_file("r1", "/a.txt", 7, false).await.expect("409 is an answer");
        assert_eq!(outcome, DeleteOutcome::Conflict { server_version: 12 });
    }

    /// -1 means "nothing known to state", so no If-Match goes out and the
    /// delete stays unconditional.
    #[tokio::test]
    async fn snapshot_delete_file_omits_if_match_for_an_unknown_version() {
        let mut server = mockito::Server::new_async().await;
        let _mock = server
            .mock("DELETE", mockito::Matcher::Any)
            .match_request(|req| req.header("If-Match").is_empty())
            .with_status(204)
            .create_async()
            .await;

        let snap = snap_for(server.url());
        snap.delete_file("r1", "/a.txt", -1, false).await.expect("no If-Match, plain delete");
    }

    #[tokio::test]
    async fn snapshot_create_directory_falls_back_to_empty_id_on_garbled_body() {
        let mut server = mockito::Server::new_async().await;
        let _mock = server
            .mock("POST", "/api/v1/shared-mounts/share-1/directories")
            .with_status(201)
            .with_body("not json")
            .create_async()
            .await;

        let snap = snap_for(server.url());
        let id = snap.create_directory("share-1", "/sub", true).await.unwrap();
        assert_eq!(id, "", "a garbled response body must still count as success with an empty id");
    }
}
