//! The application identifier, read from the one place that defines it.
//!
//! `tauri.conf.json` owns the identifier. Code that runs before the Tauri
//! builder exists (logging) or without an
//! `AppHandle` (Explorer namespace paths) gets it from the same file, embedded
//! at compile time, so no second copy can drift out of sync.

use std::sync::OnceLock;

static IDENTIFIER: OnceLock<String> = OnceLock::new();

/// The bundle identifier from `tauri.conf.json`.
pub fn identifier() -> &'static str {
    IDENTIFIER.get_or_init(|| {
        let conf: serde_json::Value =
            serde_json::from_str(include_str!("../tauri.conf.json")).expect("valid tauri.conf.json");
        conf["identifier"]
            .as_str()
            .expect("tauri.conf.json has an identifier")
            .to_string()
    })
}

#[cfg(test)]
mod tests {
    #[test]
    fn identifier_is_the_github_one() {
        assert_eq!(super::identifier(), "io.github.jeansy91.privasync");
    }
}
