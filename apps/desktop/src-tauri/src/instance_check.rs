//! Checks whether a user-entered server address actually hosts a PrivaSync
//! instance, before the user commits to logging in against it.
//!
//! Reuses the public, unauthenticated `/api/v1/setup/status` endpoint
//! (first-run admin bootstrap check) as a cheap identity signal — its JSON
//! shape (`{"needs_setup": bool}`) is specific enough that a generic web
//! server or a typo'd host won't accidentally match.

use std::time::Duration;

/// Pure classifier: does this JSON body look like a PrivaSync
/// `/api/v1/setup/status` response? Kept separate from the network call so
/// it's unit-testable without a running server.
pub fn looks_like_privasync(body: &serde_json::Value) -> bool {
    body.get("needs_setup").and_then(|v| v.as_bool()).is_some()
}

/// Check whether `url` hosts a reachable PrivaSync instance. Never returns
/// `Err` for reachability problems (wrong host, timeout, non-PrivaSync
/// server) — those all classify as `Ok(false)` so the frontend can treat the
/// result as a plain status icon instead of an error to handle.
#[tauri::command]
pub async fn verify_instance(url: String) -> Result<bool, String> {
    let base = crate::public_url::normalize_public_url(&url);
    if base.is_empty() {
        return Ok(false);
    }

    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(5))
        .build()
        .map_err(|e| e.to_string())?;

    let resp = match client.get(format!("{base}/api/v1/setup/status")).send().await {
        Ok(r) => r,
        Err(_) => return Ok(false),
    };
    if !resp.status().is_success() {
        return Ok(false);
    }
    let body: serde_json::Value = match resp.json().await {
        Ok(b) => b,
        Err(_) => return Ok(false),
    };
    Ok(looks_like_privasync(&body))
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn matches_needs_setup_true() {
        assert!(looks_like_privasync(&json!({ "needs_setup": true })));
    }

    #[test]
    fn matches_needs_setup_false() {
        assert!(looks_like_privasync(&json!({ "needs_setup": false })));
    }

    #[test]
    fn rejects_missing_field() {
        assert!(!looks_like_privasync(&json!({ "ok": true })));
    }

    #[test]
    fn rejects_wrong_type() {
        assert!(!looks_like_privasync(&json!({ "needs_setup": "yes" })));
    }

    #[test]
    fn rejects_empty_object() {
        assert!(!looks_like_privasync(&json!({})));
    }
}
