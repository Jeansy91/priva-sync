//! Wire adapters for the rsync-style delta-sync endpoints (ADR-0002):
//! fetch the server's block signatures, then post a copy/data reconstruction
//! plan. Both speak the exact contract the server handler defines — `strong`
//! hashes and literal `data` travel base64, `expected_hash` hex, and the client's
//! known server version rides in `If-Match` so a stale base loses the optimistic
//! lock the same way a full upload does.

use anyhow::{anyhow, Result};
use base64::Engine;
use reqwest::StatusCode;
use serde::Deserialize;
use serde_json::json;
use std::time::SystemTime;

use super::{check_status, resource_base, ApiSnapshot, UploadOutcome, UploadResponse};
use crate::sync::delta::{BlockSignature, DeltaOp};

/// Outcome of a delta-request: either the server offered signatures to diff
/// against, or it declined (no base / below threshold) and the client must fall
/// back to a full upload.
#[derive(Debug)]
pub enum DeltaAvailability {
    Available {
        block_size: usize,
        signatures: Vec<BlockSignature>,
    },
    NotAvailable,
}

/// Wire shape of one signature; `strong` is base64 (Go marshals `[]byte` so).
#[derive(Deserialize)]
struct WireSignature {
    offset: i64,
    size:   i64,
    weak:   u32,
    strong: String,
}

#[derive(Deserialize)]
struct DeltaRequestBody {
    delta_available: bool,
    #[serde(default)]
    block_size: i64,
    #[serde(default)]
    signatures: Vec<WireSignature>,
}

impl ApiSnapshot {
    /// `GET …/files/delta` — fetch per-block signatures of the current server
    /// version so the client can diff locally. Returns `NotAvailable` when the
    /// server has no usable base or the file is at/below the delta threshold.
    /// Delta endpoints exist only for owned sync-roots (no mount variant).
    pub async fn delta_request(
        &self,
        root_id:     &str,
        server_path: &str,
    ) -> Result<DeltaAvailability> {
        let url = format!(
            "{}/files/delta?path={}",
            resource_base(&self.base_url, false, root_id),
            urlencoding::encode(server_path),
        );
        let res = self.http
            .get(&url)
            .header("Authorization", &self.bearer)
            .send()
            .await?;
        check_status(res.status(), "delta request")?;

        let body: DeltaRequestBody = res.json().await?;
        if !body.delta_available {
            return Ok(DeltaAvailability::NotAvailable);
        }

        let mut signatures = Vec::with_capacity(body.signatures.len());
        for w in body.signatures {
            let strong = base64::engine::general_purpose::STANDARD
                .decode(&w.strong)
                .map_err(|e| anyhow!("delta request: invalid base64 strong hash: {e}"))?;
            signatures.push(BlockSignature {
                offset: w.offset,
                size:   w.size,
                weak:   w.weak,
                strong,
            });
        }
        Ok(DeltaAvailability::Available {
            block_size: body.block_size as usize,
            signatures,
        })
    }

    /// `POST …/files/delta/apply` — send the reconstruction plan. The server
    /// rebuilds the full file from it, verifies `expected_hash`, then commits
    /// through the Sync Coordinator. Returns `Uploaded` on 200 and `Conflict`
    /// on 409 (stale `If-Match`); a 422 (no base / bad plan / hash mismatch)
    /// propagates as `Err` so the caller can fall back to a full upload.
    /// `expected_hash` is the raw BLAKE3 digest of the new file.
    pub async fn delta_apply(
        &self,
        root_id:              &str,
        server_path:          &str,
        ops:                  &[DeltaOp],
        expected_hash:        &[u8],
        mtime:                SystemTime,
        known_server_version: i64,
    ) -> Result<UploadOutcome> {
        let url = format!(
            "{}/files/delta/apply",
            resource_base(&self.base_url, false, root_id),
        );
        let mtime_rfc = chrono::DateTime::<chrono::Utc>::from(mtime)
            .format("%Y-%m-%dT%H:%M:%S%.3fZ")
            .to_string();

        let mut literal_bytes = 0u64;
        let wire_ops: Vec<serde_json::Value> = ops
            .iter()
            .map(|op| match op {
                DeltaOp::Copy { offset, length } => {
                    json!({ "copy": { "offset": offset, "length": length } })
                }
                DeltaOp::Data(bytes) => {
                    literal_bytes += bytes.len() as u64;
                    json!({ "data": base64::engine::general_purpose::STANDARD.encode(bytes) })
                }
            })
            .collect();

        let body = json!({
            "path":          server_path,
            "expected_hash": hex::encode(expected_hash),
            "mtime":         mtime_rfc,
            "ops":           wire_ops,
        });

        let mut req = self.http
            .post(&url)
            .header("Authorization", &self.bearer)
            .json(&body);
        if known_server_version >= 0 {
            req = req.header("If-Match", known_server_version.to_string());
        }

        let res = req.send().await?;

        if res.status() == StatusCode::CONFLICT {
            let body: serde_json::Value = res.json().await.unwrap_or_default();
            return Ok(UploadOutcome::Conflict {
                server_version: body["server_version"].as_i64().unwrap_or(-1),
                server_hash:    body["server_hash"].as_str().unwrap_or("").to_string(),
                conflict_path:  body["conflict_path"].as_str().map(String::from),
            });
        }

        check_status(res.status(), "delta apply")?;
        // Only the literal bytes actually crossed the wire — count them so the
        // bottom transfer-speed bar reflects the delta saving, not the full file.
        self.stats.add_upload(literal_bytes);
        let resp: UploadResponse = res.json().await?;
        Ok(UploadOutcome::Uploaded(resp))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::api::TransferStats;
    use reqwest::Client;
    use std::sync::Arc;
    use std::time::UNIX_EPOCH;

    fn snap_for(url: String) -> ApiSnapshot {
        ApiSnapshot {
            http:     Client::new(),
            base_url: url,
            bearer:   "Bearer test-token".to_string(),
            stats:    Arc::new(TransferStats::default()),
        }
    }

    // delta_request must parse delta_available=true, decode the base64 strong
    // hash back to raw bytes, and surface block_size + server_version.
    #[tokio::test]
    async fn delta_request_parses_available_signatures() {
        let mut server = mockito::Server::new_async().await;
        let strong = vec![0xDE, 0xAD, 0xBE, 0xEF];
        let strong_b64 = base64::engine::general_purpose::STANDARD.encode(&strong);
        let mock = server
            .mock("GET", "/api/v1/sync-roots/root-1/files/delta?path=%2Fbig.bin")
            .match_header("authorization", "Bearer test-token")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(format!(
                r#"{{"delta_available":true,"block_size":1048576,"server_version":7,
                    "signatures":[{{"index":0,"offset":0,"size":1048576,"weak":42,"strong":"{strong_b64}"}}]}}"#
            ))
            .create_async()
            .await;

        let snap = snap_for(server.url());
        let out = snap.delta_request("root-1", "/big.bin").await.expect("delta request ok");

        match out {
            DeltaAvailability::Available { block_size, signatures } => {
                assert_eq!(block_size, 1 << 20);
                assert_eq!(signatures.len(), 1);
                assert_eq!(signatures[0].weak, 42);
                assert_eq!(signatures[0].offset, 0);
                assert_eq!(signatures[0].strong, strong, "strong hash must base64-decode to raw bytes");
            }
            DeltaAvailability::NotAvailable => panic!("expected Available"),
        }
        mock.assert_async().await;
    }

    // delta_available=false (no base / below threshold) → NotAvailable so the
    // caller falls back to a full upload.
    #[tokio::test]
    async fn delta_request_handles_not_available() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("GET", "/api/v1/sync-roots/root-1/files/delta?path=%2Fsmall.txt")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"delta_available":false}"#)
            .create_async()
            .await;

        let snap = snap_for(server.url());
        let out = snap.delta_request("root-1", "/small.txt").await.expect("ok");

        assert!(matches!(out, DeltaAvailability::NotAvailable));
        mock.assert_async().await;
    }

    // delta_apply must post the plan in the server's exact wire shape (copy ops,
    // base64 data, hex expected_hash, mtime) with the known version in If-Match,
    // and map 200 → Uploaded.
    #[tokio::test]
    async fn delta_apply_posts_plan_and_maps_uploaded() {
        let mut server = mockito::Server::new_async().await;
        let data = vec![1u8, 2, 3];
        let expected_body = json!({
            "path": "/big.bin",
            "expected_hash": "aabb",
            "mtime": "1970-01-01T00:00:00.000Z",
            "ops": [
                { "copy": { "offset": 0, "length": 1048576 } },
                { "data": base64::engine::general_purpose::STANDARD.encode(&data) },
            ],
        });
        let mock = server
            .mock("POST", "/api/v1/sync-roots/root-1/files/delta/apply")
            .match_header("authorization", "Bearer test-token")
            .match_header("if-match", "7")
            .match_body(mockito::Matcher::Json(expected_body))
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"server_version":8,"content_hash":"aabb"}"#)
            .create_async()
            .await;

        let ops = vec![
            DeltaOp::Copy { offset: 0, length: 1 << 20 },
            DeltaOp::Data(data),
        ];
        let snap = snap_for(server.url());
        let out = snap
            .delta_apply("root-1", "/big.bin", &ops, &[0xAA, 0xBB], UNIX_EPOCH, 7)
            .await
            .expect("delta apply ok");

        match out {
            UploadOutcome::Uploaded(resp) => assert_eq!(resp.server_version, 8),
            other => panic!("expected Uploaded, got {other:?}"),
        }
        mock.assert_async().await;
    }

    // A 409 from a stale If-Match must surface as Conflict (same body shape as a
    // full-upload conflict) so the existing conflict-copy path handles it.
    #[tokio::test]
    async fn delta_apply_maps_conflict() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("POST", "/api/v1/sync-roots/root-1/files/delta/apply")
            .with_status(409)
            .with_header("content-type", "application/json")
            .with_body(r#"{"error":"version conflict","server_version":9,"server_hash":"ff","conflict_path":"/big.bin.conflict"}"#)
            .create_async()
            .await;

        let ops = vec![DeltaOp::Data(vec![1, 2, 3])];
        let snap = snap_for(server.url());
        let out = snap
            .delta_apply("root-1", "/big.bin", &ops, &[1], UNIX_EPOCH, 5)
            .await
            .expect("ok");

        match out {
            UploadOutcome::Conflict { server_version, server_hash, conflict_path } => {
                assert_eq!(server_version, 9);
                assert_eq!(server_hash, "ff");
                assert_eq!(conflict_path.as_deref(), Some("/big.bin.conflict"));
            }
            other => panic!("expected Conflict, got {other:?}"),
        }
        mock.assert_async().await;
    }

    // A 422 (no base / bad plan / hash mismatch) must propagate as Err so the
    // caller falls back to a full upload instead of treating it as success.
    #[tokio::test]
    async fn delta_apply_failure_is_error_for_fallback() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("POST", "/api/v1/sync-roots/root-1/files/delta/apply")
            .with_status(422)
            .with_header("content-type", "application/json")
            .with_body(r#"{"error":"DELTA_RECONSTRUCTION_FAILED"}"#)
            .create_async()
            .await;

        let ops = vec![DeltaOp::Data(vec![1, 2, 3])];
        let snap = snap_for(server.url());
        let out = snap
            .delta_apply("root-1", "/big.bin", &ops, &[1], UNIX_EPOCH, 5)
            .await;

        assert!(out.is_err(), "422 must be an error so the caller falls back to full upload");
        mock.assert_async().await;
    }
}
