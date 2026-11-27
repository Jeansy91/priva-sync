use anyhow::{anyhow, Result};
use super::{ApiClient, LoginResponse, MeResponse, ServerError, TokenResponse, check_status, extract_cookie_value};

impl ApiClient {
    pub async fn login(
        &mut self,
        username: &str,
        password: &str,
        client_id: Option<&crate::client_id::ClientIdentification>,
    ) -> Result<(LoginResponse, Option<String>)> {
        let mut payload = serde_json::json!({
            "username": username,
            "password": password,
        });
        if let Some(ident) = client_id {
            payload["client"] = serde_json::json!(ident);
        }
        let res = self.http
            .post(format!("{}/api/v1/auth/login", self.base_url))
            .json(&payload)
            .send()
            .await?;

        let status = res.status();
        if !status.is_success() {
            return Err(ServerError::from_response(res, "login failed").await.into());
        }

        // Extract refresh_token from Set-Cookie header BEFORE consuming body
        let refresh_token = extract_cookie_value(res.headers(), "refresh_token");

        let data: LoginResponse = res.json().await?;
        if let Some(token) = &data.access_token {
            self.access_token = Some(token.clone());
        }
        Ok((data, refresh_token))
    }

    /// POST /auth/2fa → tauscht das im Login-Schritt ausgestellte
    /// `session_token` gegen ein echtes Access-Token, sobald der Nutzer den
    /// TOTP- oder Backup-Code bestätigt hat.
    pub async fn verify_2fa(
        &mut self,
        session_token: &str,
        totp_code: Option<&str>,
        backup_code: Option<&str>,
        client_id: Option<&crate::client_id::ClientIdentification>,
    ) -> Result<(TokenResponse, Option<String>)> {
        let mut payload = serde_json::json!({ "session_token": session_token });
        // A 2FA-gated login ends here rather than in login(), so without this
        // the device would never register itself with the server at all.
        if let Some(ident) = client_id {
            payload["client"] = serde_json::json!(ident);
        }
        if let Some(code) = totp_code {
            payload["totp_code"] = serde_json::json!(code);
        }
        if let Some(code) = backup_code {
            payload["backup_code"] = serde_json::json!(code);
        }

        let res = self.http
            .post(format!("{}/api/v1/auth/2fa", self.base_url))
            .json(&payload)
            .send()
            .await?;

        let status = res.status();
        if !status.is_success() {
            return Err(ServerError::from_response(res, "2fa verification failed").await.into());
        }

        let refresh_token = extract_cookie_value(res.headers(), "refresh_token");
        let data: TokenResponse = res.json().await?;
        self.access_token = Some(data.access_token.clone());
        Ok((data, refresh_token))
    }

    /// GET /auth/me → liefert User-Id und (optional) Avatar-URL.
    /// Wird beim Start gerufen, damit der Header das Profilbild ziehen kann.
    pub async fn me(&self) -> Result<MeResponse> {
        let res = self.http
            .get(format!("{}/api/v1/auth/me", self.base_url))
            .header("Authorization", self.bearer()?)
            .send().await?;
        if !res.status().is_success() {
            return Err(anyhow!("me failed: {}", res.status()));
        }
        Ok(res.json().await?)
    }

    /// GET /auth/avatar/{userID} → PNG-Bytes oder None bei 404.
    pub async fn fetch_avatar(&self, user_id: &str) -> Result<Option<Vec<u8>>> {
        let res = self.http
            .get(format!("{}/api/v1/auth/avatar/{}", self.base_url, user_id))
            .header("Authorization", self.bearer()?)
            .send().await?;
        if res.status() == reqwest::StatusCode::NOT_FOUND {
            return Ok(None);
        }
        if !res.status().is_success() {
            return Err(anyhow!("fetch_avatar failed: {}", res.status()));
        }
        Ok(Some(res.bytes().await?.to_vec()))
    }

    /// Versucht das Access Token zu erneuern.
    /// `stored_token` ist der gespeicherte Refresh-Token-Wert aus der lokalen DB,
    /// der als Cookie-Header injiziert wird (da der In-Memory-Cookie-Store nach
    /// einem Neustart leer ist).
    ///
    /// Der Server rotiert den Refresh-Token bei jedem Aufruf (alten geloescht,
    /// neuen ausgestellt) — der neue Wert muss aus dem Set-Cookie-Header
    /// extrahiert und vom Aufrufer persistiert werden, sonst schlaegt der
    /// naechste Refresh mit dem bereits geloeschten alten Token fehl und die
    /// Session gilt faelschlich als abgelaufen.
    pub async fn refresh_token(
        &mut self,
        stored_token: Option<&str>,
        client_id: Option<&crate::client_id::ClientIdentification>,
    ) -> Result<Option<String>> {
        let mut builder = self.http
            .post(format!("{}/api/v1/auth/refresh", self.base_url));

        if let Some(token) = stored_token {
            builder = builder.header("Cookie", format!("refresh_token={token}"));
        }
        // The identification rides along on every refresh, not only on login:
        // a long-lived session refreshes for weeks without ever logging in
        // again, so this is what keeps the server's record of our version
        // current after an in-place app update. The header stays for servers
        // that only understand the old heartbeat.
        if let Some(ident) = client_id {
            builder = builder
                .header("X-Client-Device", &ident.device_id)
                .json(&serde_json::json!({ "client": ident }));
        }

        // check_status separates the two outcomes the caller must tell apart: a
        // rejected refresh token (401 -> typed Unauthorized, session is gone)
        // from any other failure (transport error, 5xx, proxy hiccup), where
        // the stored credentials are still valid and only the attempt failed.
        let res = builder.send().await?;
        check_status(res.status(), "token refresh")?;

        let new_refresh_token = extract_cookie_value(res.headers(), "refresh_token");

        let data: TokenResponse = res.json().await?;
        self.access_token = Some(data.access_token);
        Ok(new_refresh_token)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::api::TransferStats;
    use std::sync::Arc;

    fn client_for(url: String) -> ApiClient {
        ApiClient::new(url, Arc::new(TransferStats::default()))
    }

    fn ident(device_id: &str) -> crate::client_id::ClientIdentification {
        crate::client_id::ClientIdentification {
            device_id:      device_id.to_string(),
            device_name:    "TEST-PC".to_string(),
            platform:       "windows",
            os_version:     "11.0.26220".to_string(),
            client_version: "9.9.9".to_string(),
        }
    }

    #[tokio::test]
    async fn login_stores_access_token_and_extracts_refresh_cookie() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("POST", "/api/v1/auth/login")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_header("set-cookie", "refresh_token=abc123; HttpOnly; Path=/")
            .with_body(r#"{"access_token":"tok-1","requires_2fa":false,"session_token":null}"#)
            .create_async()
            .await;

        let mut client = client_for(server.url());
        let (resp, refresh) = client.login("alice", "hunter2", None).await.expect("login ok");

        assert_eq!(resp.access_token.as_deref(), Some("tok-1"));
        assert!(!resp.requires_2fa);
        assert_eq!(refresh.as_deref(), Some("abc123"));
        assert_eq!(client.access_token.as_deref(), Some("tok-1"));
        mock.assert_async().await;
    }

    #[tokio::test]
    async fn login_requiring_2fa_leaves_access_token_unset() {
        let mut server = mockito::Server::new_async().await;
        server
            .mock("POST", "/api/v1/auth/login")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"access_token":null,"requires_2fa":true,"session_token":"sess-1"}"#)
            .create_async()
            .await;

        let mut client = client_for(server.url());
        let (resp, _) = client.login("alice", "hunter2", None).await.expect("login ok");

        assert!(resp.requires_2fa);
        assert_eq!(resp.session_token.as_deref(), Some("sess-1"));
        assert!(client.access_token.is_none());
    }

    #[tokio::test]
    async fn login_surfaces_server_error_message() {
        let mut server = mockito::Server::new_async().await;
        server
            .mock("POST", "/api/v1/auth/login")
            .with_status(401)
            .with_header("content-type", "application/json")
            .with_body(r#"{"error":"invalid credentials","code":"INVALID_CREDENTIALS"}"#)
            .create_async()
            .await;

        let mut client = client_for(server.url());
        let err = client.login("alice", "wrong", None).await.unwrap_err();

        assert_eq!(err.to_string(), "invalid credentials");
        let server_err = err.downcast_ref::<ServerError>().expect("typed server error");
        assert_eq!(server_err.code.as_deref(), Some("INVALID_CREDENTIALS"));
    }

    #[tokio::test]
    async fn verify_2fa_with_totp_code_stores_access_token() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("POST", "/api/v1/auth/2fa")
            .match_body(mockito::Matcher::Json(serde_json::json!({
                "session_token": "sess-1",
                "totp_code": "123456",
            })))
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_header("set-cookie", "refresh_token=abc123; HttpOnly; Path=/")
            .with_body(r#"{"access_token":"tok-2"}"#)
            .create_async()
            .await;

        let mut client = client_for(server.url());
        let (resp, refresh) = client.verify_2fa("sess-1", Some("123456"), None, None).await.expect("verify ok");

        assert_eq!(resp.access_token, "tok-2");
        assert_eq!(refresh.as_deref(), Some("abc123"));
        assert_eq!(client.access_token.as_deref(), Some("tok-2"));
        mock.assert_async().await;
    }

    #[tokio::test]
    async fn verify_2fa_with_backup_code_stores_access_token() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("POST", "/api/v1/auth/2fa")
            .match_body(mockito::Matcher::Json(serde_json::json!({
                "session_token": "sess-1",
                "backup_code": "deadbeefcafef00d",
            })))
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"access_token":"tok-3"}"#)
            .create_async()
            .await;

        let mut client = client_for(server.url());
        let (resp, _) = client.verify_2fa("sess-1", None, Some("deadbeefcafef00d"), None).await.expect("verify ok");

        assert_eq!(resp.access_token, "tok-3");
        mock.assert_async().await;
    }

    #[tokio::test]
    async fn verify_2fa_surfaces_server_error_message() {
        let mut server = mockito::Server::new_async().await;
        server
            .mock("POST", "/api/v1/auth/2fa")
            .with_status(401)
            .with_header("content-type", "application/json")
            .with_body(r#"{"error":"invalid totp code"}"#)
            .create_async()
            .await;

        let mut client = client_for(server.url());
        let err = client.verify_2fa("sess-1", Some("000000"), None, None).await.unwrap_err();

        assert_eq!(err.to_string(), "invalid totp code");
    }

    #[tokio::test]
    async fn me_returns_user_when_authenticated() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("GET", "/api/v1/auth/me")
            .match_header("authorization", "Bearer tok-1")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"id":"u-1","username":"alice","avatar_url":null}"#)
            .create_async()
            .await;

        let mut client = client_for(server.url());
        client.set_token("tok-1");
        let me = client.me().await.expect("me ok");

        assert_eq!(me.id, "u-1");
        mock.assert_async().await;
    }

    #[tokio::test]
    async fn me_fails_when_not_authenticated() {
        let client = client_for("http://unused.invalid".to_string());
        let err = client.me().await.unwrap_err();
        assert_eq!(err.to_string(), "not authenticated");
    }

    #[tokio::test]
    async fn fetch_avatar_returns_none_on_404() {
        let mut server = mockito::Server::new_async().await;
        server
            .mock("GET", "/api/v1/auth/avatar/u-1")
            .with_status(404)
            .create_async()
            .await;

        let mut client = client_for(server.url());
        client.set_token("tok-1");
        let avatar = client.fetch_avatar("u-1").await.expect("fetch_avatar ok");

        assert!(avatar.is_none());
    }

    #[tokio::test]
    async fn fetch_avatar_returns_bytes_on_success() {
        let mut server = mockito::Server::new_async().await;
        server
            .mock("GET", "/api/v1/auth/avatar/u-1")
            .with_status(200)
            .with_body(vec![0x89, 0x50, 0x4E, 0x47])
            .create_async()
            .await;

        let mut client = client_for(server.url());
        client.set_token("tok-1");
        let avatar = client.fetch_avatar("u-1").await.expect("fetch_avatar ok");

        assert_eq!(avatar, Some(vec![0x89, 0x50, 0x4E, 0x47]));
    }

    #[tokio::test]
    async fn refresh_token_updates_access_token() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("POST", "/api/v1/auth/refresh")
            .match_header("cookie", "refresh_token=stored-refresh")
            .match_header("x-client-device", "device-1")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"access_token":"fresh-tok"}"#)
            .create_async()
            .await;

        let mut client = client_for(server.url());
        let new_rt = client.refresh_token(Some("stored-refresh"), Some(&ident("device-1"))).await.expect("refresh ok");

        assert_eq!(client.access_token.as_deref(), Some("fresh-tok"));
        assert_eq!(new_rt, None);
        mock.assert_async().await;
    }

    // A session that never logs in again still has to report the version it is
    // running, or the server's device overview keeps showing the version from
    // before the last in-place update.
    #[tokio::test]
    async fn refresh_token_sends_the_full_identification() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("POST", "/api/v1/auth/refresh")
            .match_body(mockito::Matcher::Json(serde_json::json!({
                "client": {
                    "device_id":      "device-1",
                    "device_name":    "TEST-PC",
                    "platform":       "windows",
                    "os_version":     "11.0.26220",
                    "client_version": "9.9.9",
                },
            })))
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"access_token":"fresh-tok"}"#)
            .create_async()
            .await;

        let mut client = client_for(server.url());
        client.refresh_token(Some("stored-refresh"), Some(&ident("device-1"))).await.expect("refresh ok");

        mock.assert_async().await;
    }

    // A 2FA-gated login never reaches login()'s upsert, so the identification
    // has to travel with the second factor or the device stays invisible.
    #[tokio::test]
    async fn verify_2fa_sends_the_identification() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("POST", "/api/v1/auth/2fa")
            .match_body(mockito::Matcher::Json(serde_json::json!({
                "session_token": "sess-1",
                "totp_code":     "123456",
                "client": {
                    "device_id":      "device-1",
                    "device_name":    "TEST-PC",
                    "platform":       "windows",
                    "os_version":     "11.0.26220",
                    "client_version": "9.9.9",
                },
            })))
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_body(r#"{"access_token":"tok-2"}"#)
            .create_async()
            .await;

        let mut client = client_for(server.url());
        client.verify_2fa("sess-1", Some("123456"), None, Some(&ident("device-1"))).await.expect("verify ok");

        mock.assert_async().await;
    }

    // The server rotates the refresh token on every /auth/refresh call — the
    // new Set-Cookie value must be extracted and returned so the caller can
    // persist it. Without this, the next refresh replays the already-deleted
    // old token and the session is wrongly treated as expired.
    #[tokio::test]
    async fn refresh_token_extracts_rotated_refresh_cookie() {
        let mut server = mockito::Server::new_async().await;
        let mock = server
            .mock("POST", "/api/v1/auth/refresh")
            .match_header("cookie", "refresh_token=stored-refresh")
            .with_status(200)
            .with_header("content-type", "application/json")
            .with_header("set-cookie", "refresh_token=rotated-refresh; HttpOnly; Path=/")
            .with_body(r#"{"access_token":"fresh-tok"}"#)
            .create_async()
            .await;

        let mut client = client_for(server.url());
        let new_rt = client.refresh_token(Some("stored-refresh"), None).await.expect("refresh ok");

        assert_eq!(new_rt.as_deref(), Some("rotated-refresh"));
        mock.assert_async().await;
    }

    // A rejected refresh token is the one failure that ends the session, so it
    // must be distinguishable from every other one.
    #[tokio::test]
    async fn refresh_token_reports_a_rejected_token_as_unauthorized() {
        let mut server = mockito::Server::new_async().await;
        server
            .mock("POST", "/api/v1/auth/refresh")
            .with_status(401)
            .create_async()
            .await;

        let mut client = client_for(server.url());
        let err = client.refresh_token(None, None).await.unwrap_err();
        assert!(err.is::<crate::api::Unauthorized>(), "unexpected error: {err}");
    }

    // Anything else — a 5xx from the server, a proxy in a bad mood — leaves the
    // stored credentials valid, so it must not masquerade as a rejected token.
    #[tokio::test]
    async fn refresh_token_reports_a_server_error_as_a_plain_failure() {
        let mut server = mockito::Server::new_async().await;
        server
            .mock("POST", "/api/v1/auth/refresh")
            .with_status(502)
            .create_async()
            .await;

        let mut client = client_for(server.url());
        let err = client.refresh_token(None, None).await.unwrap_err();
        assert!(!err.is::<crate::api::Unauthorized>(), "a 502 must not read as a rejected refresh token");
    }
}
