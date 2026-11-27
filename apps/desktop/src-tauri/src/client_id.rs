// Client identification sent to the server on login + refresh so the admin
// overview can show which desktop installation is signed in (user, OS,
// version, hostname, IP, last access).
//
// The device_id is a stable per-installation UUID persisted in LocalDb the
// first time we need it. Reinstalling the app yields a new id — fine, since
// it really is a new device from the server's point of view.

use serde::Serialize;
use uuid::Uuid;

use crate::db::LocalDb;

const DEVICE_ID_KEY: &str = "device_id";

#[derive(Debug, Clone, Serialize)]
pub struct ClientIdentification {
    pub device_id:      String,
    pub device_name:    String,
    pub platform:       &'static str,
    pub os_version:     String,
    pub client_version: String,
}

/// Loads the persisted device_id or, on first run, generates a fresh UUID and
/// stores it. Errors fall through to a fresh ephemeral id — the server treats
/// it as a one-shot session, which is the least-bad behavior when persistence
/// is broken.
fn device_id(db: &LocalDb) -> String {
    if let Ok(Some(existing)) = db.config_get(DEVICE_ID_KEY) {
        if !existing.is_empty() {
            return existing;
        }
    }
    let fresh = Uuid::new_v4().to_string();
    let _ = db.config_set(DEVICE_ID_KEY, &fresh);
    fresh
}

fn platform() -> &'static str {
    if cfg!(target_os = "windows") { "windows" }
    else if cfg!(target_os = "macos") { "macos" }
    else { "linux" }
}

pub fn current(db: &LocalDb) -> ClientIdentification {
    let info = os_info::get();
    ClientIdentification {
        device_id:      device_id(db),
        device_name:    gethostname::gethostname().to_string_lossy().into_owned(),
        platform:       platform(),
        os_version:     info.version().to_string(),
        client_version: env!("CARGO_PKG_VERSION").to_string(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;

    fn test_db() -> Arc<LocalDb> {
        let tmp = tempfile::tempdir().unwrap();
        Arc::new(LocalDb::open(tmp.path().join("db.sqlite")).unwrap())
    }

    #[test]
    fn device_id_is_generated_and_persisted_on_first_call() {
        let db = test_db();
        let id = device_id(&db);
        assert!(!id.is_empty());
        assert!(uuid::Uuid::parse_str(&id).is_ok());

        let stored = db.config_get(DEVICE_ID_KEY).unwrap();
        assert_eq!(stored.as_deref(), Some(id.as_str()));
    }

    #[test]
    fn device_id_is_stable_across_calls() {
        let db = test_db();
        let first = device_id(&db);
        let second = device_id(&db);
        assert_eq!(first, second);
    }

    #[test]
    fn device_id_differs_across_separate_databases() {
        let a = device_id(&test_db());
        let b = device_id(&test_db());
        assert_ne!(a, b);
    }

    #[test]
    fn platform_matches_the_build_target() {
        let p = platform();
        assert!(["windows", "macos", "linux"].contains(&p));
    }

    #[test]
    fn current_populates_all_fields() {
        let db = test_db();
        let info = current(&db);
        assert!(!info.device_id.is_empty());
        assert!(!info.client_version.is_empty());
        assert!(["windows", "macos", "linux"].contains(&info.platform));
    }
}
