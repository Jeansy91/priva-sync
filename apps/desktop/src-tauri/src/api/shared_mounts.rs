use anyhow::Result;
use tracing::debug;
use super::{ApiClient, ServerMount, ensure_success};

impl ApiClient {
    /// List every Shared Mount the caller receives (GET /shared-mounts).
    /// The desktop pulls this each cycle to discover available mounts and to
    /// detect server-side revocation (ADR-0011). The server already filters
    /// out expired shares and public-token shares.
    pub async fn list_shared_mounts(&self) -> Result<Vec<ServerMount>> {
        let res = self.http
            .get(format!("{}/api/v1/shared-mounts", self.base_url))
            .header("Authorization", self.bearer()?)
            .send()
            .await?;

        let res = ensure_success(res, "list shared mounts").await?;
        let mounts: Vec<ServerMount> = res.json().await?;
        debug!("got {} shared mount(s) from server", mounts.len());
        Ok(mounts)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::api::TransferStats;
    use std::sync::Arc;

    fn client_for(url: String) -> ApiClient {
        let mut c = ApiClient::new(url, Arc::new(TransferStats::default()));
        c.set_token("test-token");
        c
    }

    #[tokio::test]
    async fn list_shared_mounts_parses_the_response_array() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("GET", "/api/v1/shared-mounts")
            .match_header("authorization", "Bearer test-token")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(
                r#"[{"id":"m1","name":"Shared Folder","path":"/x","is_file":false,"can_write":true,
                     "expires_at":null,"owner":{"id":"u1","username":"alice","avatar_url":null}}]"#,
            )
            .create_async()
            .await;

        let client = client_for(server.url());
        let mounts = client.list_shared_mounts().await.unwrap();

        assert_eq!(mounts.len(), 1);
        assert_eq!(mounts[0].id, "m1");
        assert_eq!(mounts[0].name, "Shared Folder");
        assert!(mounts[0].can_write);
        assert_eq!(mounts[0].owner.username, "alice");
        mock.assert_async().await;
    }

    #[tokio::test]
    async fn list_shared_mounts_propagates_error_status() {
        let mut server = mockito::Server::new_async().await;
        let _mock = server
            .mock("GET", "/api/v1/shared-mounts")
            .with_status(500)
            .with_body(r#"{"error":"internal error","code":"INTERNAL_ERROR"}"#)
            .create_async()
            .await;

        let client = client_for(server.url());
        let err = client.list_shared_mounts().await.expect_err("500 must surface as an error");
        assert!(err.to_string().contains("HTTP 500"));
        let server_err = err.downcast_ref::<crate::api::ServerError>().expect("typed server error");
        assert_eq!(server_err.code.as_deref(), Some("INTERNAL_ERROR"));
    }
}
