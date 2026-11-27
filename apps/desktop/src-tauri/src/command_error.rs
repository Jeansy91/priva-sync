//! The error every user-facing Tauri command rejects with.
//!
//! Same `{error, code}` shape as a server error body, so the frontend runs it
//! through `translateApiError`: `code` selects the translation, `error` is the
//! English fallback for failures nobody assigned a code (I/O, database).
//! Server rejections keep the server's code; local failures the user can act
//! on get a code from [`codes`], translated under the same `errors.*` keys.

use serde::Serialize;

use crate::api::{ServerError, SessionExpired};

/// Codes for failures that originate in the client. Reuses a server code
/// where the meaning is identical, so one translation covers both.
pub mod codes {
    pub const SERVER_URL_REQUIRED:      &str = "SERVER_URL_REQUIRED";
    pub const SESSION_EXPIRED:          &str = "SESSION_EXPIRED";
    pub const SYNC_ROOT_NOT_FOUND:      &str = "SYNC_ROOT_NOT_FOUND";
    pub const CONFLICT_COPY_MISSING:    &str = "CONFLICT_COPY_MISSING";
    pub const ON_DEMAND_ON_DRIVE_ROOT:  &str = "ON_DEMAND_ON_DRIVE_ROOT";
}

#[derive(Debug, Serialize)]
pub struct CommandError {
    error: String,
    code:  Option<String>,
}

impl CommandError {
    pub fn coded(code: &str, error: impl Into<String>) -> Self {
        Self { error: error.into(), code: Some(code.to_string()) }
    }
}

impl From<anyhow::Error> for CommandError {
    fn from(e: anyhow::Error) -> Self {
        if e.is::<SessionExpired>() {
            return Self::coded(codes::SESSION_EXPIRED, e.to_string());
        }
        let code = e.downcast_ref::<ServerError>().and_then(|s| s.code.clone());
        Self { error: e.to_string(), code }
    }
}

impl From<String> for CommandError {
    fn from(error: String) -> Self {
        Self { error, code: None }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_server_code_survives_when_converting_from_anyhow() {
        let e = anyhow::Error::new(ServerError {
            message: "invalid credentials".into(),
            code:    Some("INVALID_CREDENTIALS".into()),
        });
        let c = CommandError::from(e);
        assert_eq!(c.error, "invalid credentials");
        assert_eq!(c.code.as_deref(), Some("INVALID_CREDENTIALS"));
    }

    #[test]
    fn test_session_expired_gets_code_when_converting_from_anyhow() {
        let c = CommandError::from(anyhow::Error::new(SessionExpired));
        assert_eq!(c.code.as_deref(), Some(codes::SESSION_EXPIRED));
    }

    #[test]
    fn test_plain_error_has_no_code_when_converting_from_anyhow() {
        let c = CommandError::from(anyhow::anyhow!("disk full"));
        assert_eq!(c.error, "disk full");
        assert!(c.code.is_none());
    }

    #[test]
    fn test_serializes_as_server_error_shape() {
        let json = serde_json::to_value(CommandError::coded("X", "y")).unwrap();
        assert_eq!(json, serde_json::json!({ "error": "y", "code": "X" }));
    }
}
