//! The sync engine's only way to the server (ADR-0071).
//!
//! An [`ApiSnapshot`] carries the bearer token it was taken with. Held across
//! an upload batch, it outlived the 15-minute access token and every request
//! after that came back 401 — and nothing on that path refreshed, so whole
//! batches failed until some unrelated caller happened to refresh. Here each
//! request takes its snapshot when it starts, so it always carries the
//! current token, and a 401 goes through [`with_auth_retry`]: one refresh
//! shared by all concurrent callers, then a replay.

use std::future::Future;
use std::sync::Arc;

use anyhow::Result;
use tokio::sync::RwLock as AsyncLock;

use crate::db::LocalDb;
use super::{with_auth_retry, ApiClient, ApiSnapshot};

#[derive(Clone)]
pub struct AuthedApi {
    client:             Arc<AsyncLock<ApiClient>>,
    db:                 Arc<LocalDb>,
    on_session_expired: Arc<dyn Fn() + Send + Sync>,
}

impl AuthedApi {
    pub fn new(
        client:             Arc<AsyncLock<ApiClient>>,
        db:                 Arc<LocalDb>,
        on_session_expired: impl Fn() + Send + Sync + 'static,
    ) -> Self {
        Self { client, db, on_session_expired: Arc::new(on_session_expired) }
    }

    pub async fn is_authenticated(&self) -> bool {
        self.client.read().await.is_authenticated()
    }

    /// Run one request on a snapshot taken when it starts. Each call is one
    /// request, not a sequence: a sequence would share one token again, and a
    /// long chunked upload outlives it.
    pub async fn call<T, F, Fut>(&self, op: F) -> Result<T>
    where
        F:   Fn(ApiSnapshot) -> Fut,
        Fut: Future<Output = Result<T>>,
    {
        let op = &op;
        with_auth_retry(
            &self.client, &self.db,
            || (self.on_session_expired)(),
            || async move {
                let snap = self.client.read().await.snapshot()?;
                op(snap).await
            },
        ).await
    }

    /// [`Self::call`] for the requests that live on [`ApiClient`] itself.
    /// `op` reads the client under its own lock, for as long as the request
    /// takes — the same as every caller of those methods does.
    pub async fn call_client<T, F, Fut>(&self, op: F) -> Result<T>
    where
        F:   Fn(Arc<AsyncLock<ApiClient>>) -> Fut,
        Fut: Future<Output = Result<T>>,
    {
        with_auth_retry(
            &self.client, &self.db,
            || (self.on_session_expired)(),
            || op(self.client.clone()),
        ).await
    }
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    use crate::api::TransferStats;
    use std::sync::atomic::{AtomicBool, AtomicU32, Ordering};

    /// An [`AuthedApi`] against `url`, logged in with `token`. The temp dir
    /// travels along so the database outlives the test's last request.
    pub(crate) fn authed_for(url: String, token: &str) -> (AuthedApi, tempfile::TempDir) {
        let tmp = tempfile::tempdir().unwrap();
        let db  = Arc::new(LocalDb::open(tmp.path().join("db.sqlite")).unwrap());
        let mut client = ApiClient::new(url, Arc::new(TransferStats::default()));
        client.set_token(token);
        (AuthedApi::new(Arc::new(AsyncLock::new(client)), db, || {}), tmp)
    }

    // The defect this prevents: a token that expired between two requests must
    // be refreshed and the request replayed, not reported as a failure of the
    // file it was for.
    #[tokio::test]
    async fn call_refreshes_an_expired_token_and_replays_with_the_new_one() {
        let mut server = mockito::Server::new_async().await;
        server.mock("DELETE", "/api/v1/sync-roots/r1/files")
            .match_query(mockito::Matcher::Any)
            .match_header("authorization", "Bearer old-token")
            .with_status(401)
            .expect(1)
            .create_async().await;
        let replay = server.mock("DELETE", "/api/v1/sync-roots/r1/files")
            .match_query(mockito::Matcher::Any)
            .match_header("authorization", "Bearer fresh-token")
            .with_status(204)
            .expect(1)
            .create_async().await;
        server.mock("POST", "/api/v1/auth/refresh")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"access_token":"fresh-token"}"#)
            .expect(1)
            .create_async().await;

        let (api, _tmp) = authed_for(server.url(), "old-token");
        let outcome = api
            .call(|snap| async move { snap.delete_file("r1", "/a.txt", -1, false).await })
            .await
            .expect("the replay must succeed");

        assert!(matches!(outcome, crate::api::DeleteOutcome::Deleted));
        replay.assert_async().await;
    }

    // Each call snapshots afresh: once someone refreshed, the next request
    // carries the new token without a 401 of its own.
    #[tokio::test]
    async fn call_picks_up_a_token_refreshed_since_the_previous_call() {
        let mut server = mockito::Server::new_async().await;
        let fresh = server.mock("DELETE", "/api/v1/sync-roots/r1/files")
            .match_query(mockito::Matcher::Any)
            .match_header("authorization", "Bearer second-token")
            .with_status(204)
            .expect(1)
            .create_async().await;

        let (api, _tmp) = authed_for(server.url(), "first-token");
        api.client.write().await.set_token("second-token");
        api.call(|snap| async move { snap.delete_file("r1", "/a.txt", -1, false).await })
            .await
            .expect("request with the current token");

        fresh.assert_async().await;
    }

    // A rejected refresh ends the session: the hook fires and the caller
    // gets an auth failure, which the engine does not count against a file.
    #[tokio::test]
    async fn call_reports_a_rejected_refresh_as_an_auth_failure() {
        let mut server = mockito::Server::new_async().await;
        server.mock("DELETE", "/api/v1/sync-roots/r1/files")
            .match_query(mockito::Matcher::Any)
            .with_status(401)
            .create_async().await;
        server.mock("POST", "/api/v1/auth/refresh")
            .with_status(401)
            .create_async().await;

        let (api, tmp) = authed_for(server.url(), "old-token");
        let fired = Arc::new(AtomicBool::new(false));
        let api = AuthedApi::new(api.client.clone(), api.db.clone(), {
            let fired = fired.clone();
            move || fired.store(true, Ordering::SeqCst)
        });

        let err = api
            .call(|snap| async move { snap.delete_file("r1", "/a.txt", -1, false).await })
            .await
            .expect_err("a dead session cannot succeed");

        assert!(crate::api::is_auth_failure(&err), "{err:#}");
        assert!(fired.load(Ordering::SeqCst), "session_expired must be signalled");
        // Without a token, the next call fails before it reaches the network.
        let calls = AtomicU32::new(0);
        let err = api.call(|_snap| { calls.fetch_add(1, Ordering::SeqCst); async { Ok(()) } })
            .await
            .expect_err("no token, no request");
        assert!(crate::api::is_auth_failure(&err), "{err:#}");
        assert_eq!(calls.load(Ordering::SeqCst), 0);
        drop(tmp);
    }
}
