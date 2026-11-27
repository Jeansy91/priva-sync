//! Read-only share lookups for the Explorer context menu (ADR-0021).
//!
//! The desktop does not manage shares (that lives in the web UI), but the
//! context menu needs to know whether an entry is already shared / has a public
//! link so it can offer "manage shares" / "copy link" instead of "create link".
//! Only the owner-scoped `GET /sync-roots/{id}/shares` is used here.

use std::collections::HashMap;

use anyhow::Result;
use serde::Deserialize;

use super::{check_status, ApiClient};

/// One share on an entry, trimmed to the fields the menu gates on. Extra server
/// fields are ignored by serde.
#[derive(Debug, Clone, Default, Deserialize)]
pub struct ShareEntry {
    #[serde(default)]
    pub share_type: String,
    #[serde(default)]
    pub read_token: Option<String>,
    #[serde(default)]
    pub write_token: Option<String>,
    #[serde(default)]
    pub token: Option<String>,
}

impl ShareEntry {
    /// Whether this share exposes a public link (dual-link tokens or a legacy
    /// single token, or an explicitly public share type).
    pub fn is_public(&self) -> bool {
        self.read_token.is_some()
            || self.write_token.is_some()
            || self.token.is_some()
            || self.share_type.eq_ignore_ascii_case("public")
    }
}

/// All shares of a sync root, keyed by entry path (server form, leading slash),
/// exactly as `ListSharesByRoot` returns them.
pub type RootShares = HashMap<String, Vec<ShareEntry>>;

impl ApiClient {
    /// Fetch every share on a sync root, grouped by entry path.
    pub async fn list_root_shares(&self, root_id: &str) -> Result<RootShares> {
        let res = self
            .http
            .get(format!("{}/api/v1/sync-roots/{}/shares", self.base_url, root_id))
            .header("Authorization", self.bearer()?)
            .send()
            .await?;

        check_status(res.status(), "list root shares")?;
        Ok(res.json().await?)
    }
}

/// Reduce a root's shares to the two flags the menu needs for one entry path.
/// The root itself is addressed as `"/"` server-side, so an empty relative path
/// falls back to that key.
pub fn share_flags(shares: &RootShares, rel_path: &str) -> (bool, bool) {
    let entries = shares
        .get(rel_path)
        .or_else(|| if rel_path.is_empty() { shares.get("/") } else { None });
    match entries {
        Some(list) if !list.is_empty() => (true, list.iter().any(ShareEntry::is_public)),
        _ => (false, false),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn shares(pairs: &[(&str, Vec<ShareEntry>)]) -> RootShares {
        pairs.iter().map(|(p, v)| (p.to_string(), v.clone())).collect()
    }

    #[test]
    fn unshared_path_has_no_flags() {
        let s = shares(&[]);
        assert_eq!(share_flags(&s, "/docs/a.txt"), (false, false));
    }

    #[test]
    fn user_share_is_shared_without_public_link() {
        let s = shares(&[("/docs", vec![ShareEntry { share_type: "user".into(), ..Default::default() }])]);
        assert_eq!(share_flags(&s, "/docs"), (true, false));
    }

    #[test]
    fn public_token_marks_public_link() {
        let s = shares(&[("/docs", vec![ShareEntry { read_token: Some("t".into()), ..Default::default() }])]);
        assert_eq!(share_flags(&s, "/docs"), (true, true));
    }

    #[test]
    fn empty_relative_path_falls_back_to_root_key() {
        let s = shares(&[("/", vec![ShareEntry { share_type: "user".into(), ..Default::default() }])]);
        assert_eq!(share_flags(&s, ""), (true, false));
    }

    fn client_for(url: String) -> ApiClient {
        let mut c = ApiClient::new(url, std::sync::Arc::new(crate::api::TransferStats::default()));
        c.set_token("test-token");
        c
    }

    #[tokio::test]
    async fn list_root_shares_parses_the_grouped_map() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("GET", "/api/v1/sync-roots/r1/shares")
            .match_header("authorization", "Bearer test-token")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"/docs":[{"share_type":"user"}],"/":[{"share_type":"public","read_token":"tok"}]}"#)
            .create_async()
            .await;

        let client = client_for(server.url());
        let shares = client.list_root_shares("r1").await.unwrap();

        assert_eq!(share_flags(&shares, "/docs"), (true, false));
        assert_eq!(share_flags(&shares, ""), (true, true));
        mock.assert_async().await;
    }

    #[tokio::test]
    async fn list_root_shares_propagates_error_status() {
        let mut server = mockito::Server::new_async().await;
        let _mock = server
            .mock("GET", "/api/v1/sync-roots/r1/shares")
            .with_status(500)
            .create_async()
            .await;

        let client = client_for(server.url());
        let err = client.list_root_shares("r1").await.expect_err("500 must surface as an error");
        assert!(err.to_string().contains("HTTP 500"));
    }
}
