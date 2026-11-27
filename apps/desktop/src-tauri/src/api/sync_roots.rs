use anyhow::Result;
use tracing::debug;
use super::{ApiClient, ServerSyncRoot, ChangesPage, FileStatEntry, FilesStatResponse, check_status, ensure_success, resource_base};

impl ApiClient {
    pub async fn list_sync_roots(&self) -> Result<Vec<ServerSyncRoot>> {
        let res = self.http
            .get(format!("{}/api/v1/sync-roots", self.base_url))
            .header("Authorization", self.bearer()?)
            .send()
            .await?;

        let res = ensure_success(res, "list sync roots").await?;
        Ok(res.json().await?)
    }

    /// Create a new server-side sync root. Returns the freshly-created root.
    pub async fn create_sync_root(&self, name: &str) -> Result<ServerSyncRoot> {
        let body = serde_json::json!({ "name": name }).to_string();
        let res = self.http
            .post(format!("{}/api/v1/sync-roots", self.base_url))
            .header("Authorization", self.bearer()?)
            .header("Content-Type", "application/json")
            .body(body)
            .send()
            .await?;

        let res = ensure_success(res, "create sync root").await?;
        Ok(res.json().await?)
    }

    /// Paginated changes-feed (ADR-0036). Owned roots and Shared Mounts share
    /// one contract: `since_seq` is the seq of the last entry already held (0
    /// for a first sync), and callers page by re-calling with the previous
    /// page's `next_cursor` until it is absent.
    ///
    /// Mounts used to have their own unpaginated, timestamp-based feed. That
    /// cost them both the clock-jump guarantee this ADR exists for and any
    /// bound on the response size, so the two were merged.
    pub async fn get_changes_page(
        &self,
        root_id: &str,
        since_seq: i64,
        limit: u32,
        is_mount: bool,
    ) -> Result<ChangesPage> {
        // feed_version=2 (ADR-0065): the server merges move_events into the
        // feed now and refuses a request that omits it, unconditionally --
        // not just when a move happens to be pending.
        let url = format!(
            "{}/changes?since={since_seq}&limit={limit}&feed_version=2",
            resource_base(&self.base_url, is_mount, root_id),
        );

        let res = self.http
            .get(&url)
            .header("Authorization", self.bearer()?)
            .send()
            .await?;

        check_status(res.status(), "get changes page")?;
        let page: ChangesPage = res.json().await?;
        debug!("got {} changes (next_cursor={:?})", page.changes.len(), page.next_cursor);
        Ok(page)
    }

    /// Batched fresh-state lookup for a list of paths (ADR-0062). Backs the
    /// failed_server_changes retry sweep: a retry re-fetches current state
    /// through this instead of replaying the stale snapshot it failed with,
    /// so it can never resurrect a path the server has since deleted or
    /// apply a version older than the one the server now has.
    pub async fn stat_files(
        &self, root_id: &str, is_mount: bool, paths: &[String],
    ) -> Result<Vec<FileStatEntry>> {
        let url = format!("{}/files/stat", resource_base(&self.base_url, is_mount, root_id));
        let body = serde_json::json!({ "paths": paths }).to_string();

        let res = self.http
            .post(&url)
            .header("Authorization", self.bearer()?)
            .header("Content-Type", "application/json")
            .body(body)
            .send()
            .await?;

        check_status(res.status(), "stat files")?;
        let parsed: FilesStatResponse = res.json().await?;
        Ok(parsed.results)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::api::TransferStats;
    use std::sync::Arc;

    fn client_for(url: String) -> ApiClient {
        let mut c = ApiClient::new(url, Arc::new(TransferStats::default()));
        c.set_token("tok-1");
        c
    }

    #[tokio::test]
    async fn list_sync_roots_parses_response() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("GET", "/api/v1/sync-roots")
            .match_header("authorization", "Bearer tok-1")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"[{"id":"r1","name":"Docs","path":"/data/r1"}]"#)
            .create_async()
            .await;

        let roots = client_for(server.url()).list_sync_roots().await.expect("list ok");

        assert_eq!(roots.len(), 1);
        assert_eq!(roots[0].name, "Docs");
        mock.assert_async().await;
    }

    #[tokio::test]
    async fn list_sync_roots_fails_on_error_status() {
        let mut server = mockito::Server::new_async().await;
        server.mock("GET", "/api/v1/sync-roots").with_status(500).create_async().await;

        let err = client_for(server.url()).list_sync_roots().await.unwrap_err();
        assert!(err.to_string().contains("list sync roots"));
    }

    #[tokio::test]
    async fn create_sync_root_sends_name_and_parses_result() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("POST", "/api/v1/sync-roots")
            .match_header("authorization", "Bearer tok-1")
            .match_body(mockito::Matcher::Json(serde_json::json!({ "name": "Photos" })))
            .with_status(201)
            .with_header("content-type", "application/json")
            .with_body(r#"{"id":"r2","name":"Photos","path":"/data/r2"}"#)
            .create_async()
            .await;

        let root = client_for(server.url()).create_sync_root("Photos").await.expect("create ok");

        assert_eq!(root.id, "r2");
        assert_eq!(root.name, "Photos");
        mock.assert_async().await;
    }

    #[tokio::test]
    async fn get_changes_page_routes_to_shared_mounts_when_is_mount() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("GET", "/api/v1/shared-mounts/share-1/changes")
            .match_query(mockito::Matcher::UrlEncoded("since".into(), "0".into()))
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"changes":[{"id":"f1","path":"/a.txt","size":10,"mtime":"2026-01-01T00:00:00Z","content_hash":null,"is_deleted":false,"updated_at":"2026-01-01T00:00:00Z","server_version":1,"seq":3}]}"#)
            .create_async()
            .await;

        let page = client_for(server.url())
            .get_changes_page("share-1", 0, 100, true)
            .await
            .expect("get_changes_page ok");

        assert_eq!(page.changes.len(), 1);
        assert_eq!(page.changes[0].path, "/a.txt");
        assert_eq!(page.changes[0].seq, 3);
        mock.assert_async().await;
    }

    // ADR-0065: the server now merges move_events into the feed and refuses
    // any request that doesn't declare feed_version=2. The client must send
    // it unconditionally, and must decode a moves[] entry into a
    // PrefixMoveEvent -- the whole point of the redesign is that a directory
    // move arrives as one of these instead of one changes[] row per
    // descendant.
    #[tokio::test]
    async fn get_changes_page_sends_feed_version_and_parses_moves() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("GET", "/api/v1/sync-roots/r1/changes")
            .match_query(mockito::Matcher::AllOf(vec![
                mockito::Matcher::UrlEncoded("since".into(), "0".into()),
                mockito::Matcher::UrlEncoded("feed_version".into(), "2".into()),
            ]))
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"changes":[],"moves":[{"old_prefix":"/old","new_prefix":"/new","seq":9}]}"#)
            .create_async()
            .await;

        let page = client_for(server.url())
            .get_changes_page("r1", 0, 100, false)
            .await
            .expect("get_changes_page ok");

        assert_eq!(page.moves.len(), 1);
        assert_eq!(page.moves[0].old_prefix, "/old");
        assert_eq!(page.moves[0].new_prefix, "/new");
        assert_eq!(page.moves[0].seq, 9);
        mock.assert_async().await;
    }

    #[tokio::test]
    async fn get_changes_page_sends_since_and_limit_and_parses_next_cursor() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("GET", "/api/v1/sync-roots/r1/changes")
            .match_query(mockito::Matcher::UrlEncoded("since".into(), "5".into()))
            .match_query(mockito::Matcher::UrlEncoded("limit".into(), "2".into()))
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"changes":[{"id":"f1","path":"/a.txt","size":1,"mtime":"2026-01-01T00:00:00Z","content_hash":null,"is_deleted":false,"updated_at":"2026-01-01T00:00:00Z","server_version":1,"seq":6},{"id":"f2","path":"/b.txt","size":1,"mtime":"2026-01-01T00:00:00Z","content_hash":null,"is_deleted":false,"updated_at":"2026-01-01T00:00:00Z","server_version":1,"seq":7}],"next_cursor":7}"#)
            .create_async()
            .await;

        let page = client_for(server.url())
            .get_changes_page("r1", 5, 2, false)
            .await
            .expect("get_changes_page ok");

        assert_eq!(page.changes.len(), 2);
        assert_eq!(page.changes[1].seq, 7);
        assert_eq!(page.next_cursor, Some(7));
        mock.assert_async().await;
    }

    /// A caller that walks pages by re-issuing `since` = the previous
    /// response's `next_cursor` must see every entry exactly once, in order,
    /// and stop once a page arrives without `next_cursor` -- the client-side
    /// half of the ADR-0036 pagination contract.
    #[tokio::test]
    async fn get_changes_page_walks_multiple_pages_to_completion() {
        let mut server = mockito::Server::new_async().await;
        let page1 = server
            .mock("GET", "/api/v1/sync-roots/r1/changes")
            .match_query(mockito::Matcher::UrlEncoded("since".into(), "0".into()))
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"changes":[{"id":"f1","path":"/a.txt","size":1,"mtime":"2026-01-01T00:00:00Z","content_hash":null,"is_deleted":false,"updated_at":"2026-01-01T00:00:00Z","server_version":1,"seq":1}],"next_cursor":1}"#)
            .create_async()
            .await;
        let page2 = server
            .mock("GET", "/api/v1/sync-roots/r1/changes")
            .match_query(mockito::Matcher::UrlEncoded("since".into(), "1".into()))
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"changes":[{"id":"f2","path":"/b.txt","size":1,"mtime":"2026-01-01T00:00:00Z","content_hash":null,"is_deleted":false,"updated_at":"2026-01-01T00:00:00Z","server_version":1,"seq":2}]}"#)
            .create_async()
            .await;

        let client = client_for(server.url());
        let mut cursor = 0i64;
        let mut seen = Vec::new();
        loop {
            let page = client.get_changes_page("r1", cursor, 1, false).await.expect("get_changes_page ok");
            if page.changes.is_empty() {
                break;
            }
            seen.extend(page.changes.iter().map(|e| e.path.clone()));
            cursor = page.changes.last().unwrap().seq;
            if page.next_cursor.is_none() {
                break;
            }
        }

        assert_eq!(seen, vec!["/a.txt".to_string(), "/b.txt".to_string()]);
        page1.assert_async().await;
        page2.assert_async().await;
    }

    // The ADR-0062 contract: a path the server has no record of comes back
    // found=false, distinct from a path it deleted (found=true, is_deleted=true).
    #[tokio::test]
    async fn stat_files_distinguishes_found_deleted_and_unknown() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("POST", "/api/v1/sync-roots/r1/files/stat")
            .match_header("authorization", "Bearer tok-1")
            .match_body(mockito::Matcher::Json(serde_json::json!({
                "paths": ["/keep.txt", "/gone.txt", "/never.txt"]
            })))
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"results":[
                {"path":"/keep.txt","found":true,"id":"f1","size":5,"mtime":"2026-01-01T00:00:00Z","content_hash":"ab","is_deleted":false,"updated_at":"2026-01-01T00:00:00Z","server_version":1},
                {"path":"/gone.txt","found":true,"id":"f2","is_deleted":true,"updated_at":"2026-01-01T00:00:00Z","server_version":2},
                {"path":"/never.txt","found":false}
            ]}"#)
            .create_async()
            .await;

        let paths = vec!["/keep.txt".to_string(), "/gone.txt".to_string(), "/never.txt".to_string()];
        let results = client_for(server.url()).stat_files("r1", false, &paths).await.expect("stat_files ok");

        assert_eq!(results.len(), 3);
        assert!(results[0].found && !results[0].is_deleted);
        assert!(results[1].found && results[1].is_deleted);
        assert!(!results[2].found);
        mock.assert_async().await;
    }

    #[tokio::test]
    async fn stat_files_routes_to_shared_mounts_when_is_mount() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("POST", "/api/v1/shared-mounts/share-1/files/stat")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"results":[]}"#)
            .create_async()
            .await;

        let paths = vec!["/a.txt".to_string()];
        let results = client_for(server.url()).stat_files("share-1", true, &paths).await.expect("stat_files ok");

        assert!(results.is_empty());
        mock.assert_async().await;
    }
}
