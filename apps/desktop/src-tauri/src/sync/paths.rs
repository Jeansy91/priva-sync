//! Path validation + manipulation helpers shared across the sync engine.
//!
//! All functions are pure and self-contained — they receive paths/strings and
//! return decisions, no I/O against state. This keeps them trivially testable
//! and independent of `SyncEngine`.

use std::path::{Path, PathBuf};
use anyhow::Result;

use crate::db::SyncRootEntry;

pub(super) fn find_root<'a>(roots: &'a [SyncRootEntry], path: &Path) -> Option<&'a SyncRootEntry> {
    roots.iter().find(|r| path.starts_with(&r.local_path))
}

/// Characters that are illegal in Windows file/folder names.
const WINDOWS_INVALID_CHARS: &[char] = &['<', '>', ':', '"', '/', '\\', '|', '?', '*'];

/// Returns `true` if any path component contains a Windows-invalid character.
pub(super) fn has_windows_invalid_chars(server_path: &str) -> bool {
    server_path.split('/').filter(|s| !s.is_empty()).any(|component| {
        component.chars().any(|c| WINDOWS_INVALID_CHARS.contains(&c))
            // Windows also forbids trailing dots/spaces in component names.
            || component.ends_with('.')
            || component.ends_with(' ')
    })
}

/// Returns `true` when `component` collides case-insensitively with an
/// existing entry in `parent_dir` but differs in exact spelling.
fn case_collision(parent_dir: &Path, component: &str) -> bool {
    let Ok(entries) = std::fs::read_dir(parent_dir) else { return false };
    let lower = component.to_lowercase();
    entries
        .filter_map(|e| e.ok())
        .any(|e| {
            let name = e.file_name().to_string_lossy().to_string();
            name.to_lowercase() == lower && name != component
        })
}

/// A platform problem with a server-side path, discovered before we touch the
/// filesystem. The UI translates `code()` and renders `params()`; `blocking()`
/// decides whether the file is skipped (illegal on this platform) or synced
/// anyway with only a warning (a long path may still succeed with
/// the Windows Long Path option enabled).
#[derive(Debug, Clone, PartialEq, Eq)]
pub(super) enum PathIssue {
    /// A path component contains characters Windows forbids. Blocking.
    InvalidChars,
    /// A component collides case-insensitively with an existing entry. Blocking.
    CaseCollision,
    /// The full local path is longer than the warning threshold. Non-blocking.
    TooLong { len: usize, limit: usize },
}

impl PathIssue {
    /// Stable machine code the UI maps to a translated message.
    pub(super) fn code(&self) -> &'static str {
        match self {
            PathIssue::InvalidChars  => "invalid_chars",
            PathIssue::CaseCollision => "case_collision",
            PathIssue::TooLong { .. } => "path_too_long",
        }
    }

    /// Whether the file is skipped (`true`) or synced with only a warning
    /// (`false`, e.g. a long path that may still work with Long Path enabled).
    pub(super) fn blocking(&self) -> bool {
        matches!(self, PathIssue::InvalidChars | PathIssue::CaseCollision)
    }

    /// Interpolation parameters for the translated message. Only `TooLong`
    /// carries values (`len`, `limit`); the others render a static string.
    pub(super) fn params(&self) -> serde_json::Value {
        match self {
            PathIssue::TooLong { len, limit } => serde_json::json!({ "len": len, "limit": limit }),
            _ => serde_json::json!({}),
        }
    }
}

/// Checks a server-side path for platform problems before downloading.
/// Returns the first applicable [`PathIssue`], prioritising blocking issues
/// (illegal characters, case collision) over the non-blocking length warning:
/// a path that cannot be created at all should surface the real blocker, not a
/// length hint. Returns `None` when the path is fine on this platform.
pub(super) fn check_server_path(base: &Path, server_path: &str) -> Option<PathIssue> {
    // Windows-invalid characters — only a problem on Windows. Blocking.
    #[cfg(target_os = "windows")]
    if has_windows_invalid_chars(server_path) {
        return Some(PathIssue::InvalidChars);
    }

    // Case-sensitivity collision — only a problem on Windows (case-insensitive FS).
    #[cfg(target_os = "windows")]
    {
        let mut dir = base.to_path_buf();
        for component in server_path.split('/').filter(|s| !s.is_empty()) {
            if case_collision(&dir, component) {
                return Some(PathIssue::CaseCollision);
            }
            dir.push(component);
        }
    }

    // Path length — MAX_PATH is a Windows concern. Non-blocking.
    #[cfg(target_os = "windows")]
    if let Some(len) = exceeds_path_limit(base, server_path, WINDOWS_PATH_LIMIT) {
        return Some(PathIssue::TooLong { len, limit: WINDOWS_PATH_LIMIT });
    }

    // base/server_path unused on non-Windows builds; silence lints.
    let _ = (base, server_path);

    None
}

/// Convert a local filesystem path to a server-relative path string.
/// Normalises Unicode to NFC so paths from different platforms (macOS NFD,
/// Windows NFC/UTF-16) compare correctly.
pub(super) fn relative_path(base: &Path, path: &Path) -> Result<String> {
    use unicode_normalization::UnicodeNormalization as _;
    let rel = path.strip_prefix(base)
        .map_err(|_| anyhow::anyhow!("path not under base"))?
        .to_str()
        .ok_or_else(|| anyhow::anyhow!("non-UTF8 path"))?
        .replace('\\', "/");
    // NFC-normalise so macOS NFD filenames stored in the DB match NFC paths
    // received from other clients or the server.
    let normalised: String = rel.nfc().collect();
    Ok(format!("/{normalised}"))
}

/// Remove a local path — file or directory. Returns `true` if anything was
/// removed, `false` if the path didn't exist. Directories are removed
/// recursively so cascade-deletes (parent before children) propagate fully.
pub(crate) fn remove_local_entry(local_path: &Path) -> Result<bool> {
    if !local_path.exists() {
        return Ok(false);
    }
    if local_path.is_dir() {
        std::fs::remove_dir_all(local_path)?;
    } else {
        std::fs::remove_file(local_path)?;
    }
    Ok(true)
}

/// Threshold (in characters) above which a full local path is flagged as a
/// path-length warning. Windows' classic `MAX_PATH` is 260 for the whole
/// absolute path; we warn a little earlier so users have head-room. The Long
/// Path option (opt-in) raises the real ceiling to 32 767.
pub(super) const WINDOWS_PATH_LIMIT: usize = 255;

/// Length of the full local path (`base` + `server_path`) when it exceeds
/// `limit`, otherwise `None`. Pure and platform-agnostic so the decision is
/// testable everywhere; callers gate the actual warning on Windows.
pub(super) fn exceeds_path_limit(base: &Path, server_path: &str, limit: usize) -> Option<usize> {
    let full = base.join(server_path.trim_start_matches('/'));
    let len = full.as_os_str().to_string_lossy().chars().count();
    (len > limit).then_some(len)
}

/// When we would write to `original` but it already exists with different
/// content, append a conflict suffix so the local copy can be inspected.
pub(super) fn make_conflict_path(original: &Path) -> PathBuf {
    let stem = original.file_stem().and_then(|s| s.to_str()).unwrap_or("file");
    let ext  = original.extension().and_then(|e| e.to_str()).unwrap_or("");
    let ts   = chrono::Local::now().format("%Y%m%d_%H%M%S");
    let name = if ext.is_empty() {
        format!("{stem}.conflict.{ts}")
    } else {
        format!("{stem}.conflict.{ts}.{ext}")
    };
    original.with_file_name(name)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn root(id: &str, local_path: &str) -> SyncRootEntry {
        SyncRootEntry {
            id:             id.to_string(),
            name:           "n".to_string(),
            local_path:     local_path.to_string(),
            server_root_id: "srv".to_string(),
            download_mode:  "on_demand".to_string(),
            read_only:      false,
            paused:         false,
            kind:           crate::db::ROOT_KIND_OWNED.to_string(),
        }
    }

    // find_root picks the root whose local_path prefixes the watcher event's
    // absolute path, so events under two sibling roots resolve independently.
    #[test]
    fn find_root_matches_by_local_path_prefix() {
        let roots = vec![root("r1", "C:/Sync/A"), root("r2", "C:/Sync/B")];
        let hit = find_root(&roots, Path::new("C:/Sync/B/doc.txt")).expect("must match root B");
        assert_eq!(hit.id, "r2");
    }

    // A path outside every configured root (e.g. a stray watcher event after a
    // root was just removed) must not fall through to an unrelated root.
    #[test]
    fn find_root_returns_none_for_unrelated_path() {
        let roots = vec![root("r1", "C:/Sync/A")];
        assert!(find_root(&roots, Path::new("C:/Elsewhere/doc.txt")).is_none());
    }

    #[test]
    fn has_windows_invalid_chars_detects_forbidden_characters() {
        assert!(has_windows_invalid_chars("/folder/na?me.txt"));
        assert!(has_windows_invalid_chars("/a:b"));
        assert!(!has_windows_invalid_chars("/normal/path/file.txt"));
    }

    // Windows also forbids a component ending in '.' or ' ', even without any
    // of the reserved characters — silently truncated/rejected by the shell.
    #[test]
    fn has_windows_invalid_chars_flags_trailing_dot_or_space() {
        assert!(has_windows_invalid_chars("/folder./file.txt"));
        assert!(has_windows_invalid_chars("/folder /file.txt"));
        assert!(!has_windows_invalid_chars("/folder/file.txt"));
    }

    #[test]
    fn make_conflict_path_inserts_conflict_infix_before_extension() {
        let original = Path::new("C:/Sync/notes.txt");
        let p = make_conflict_path(original);
        assert_eq!(p.parent(), original.parent(), "must stay in the same directory");
        let name = p.file_name().unwrap().to_string_lossy();
        assert!(name.starts_with("notes.conflict."), "unexpected: {name}");
        assert!(name.ends_with(".txt"), "extension must be preserved: {name}");
    }

    // A file with no extension still gets a well-formed conflict name (no
    // trailing dot with nothing after it).
    #[test]
    fn make_conflict_path_handles_extensionless_file() {
        let original = Path::new("C:/Sync/README");
        let p = make_conflict_path(original);
        let name = p.file_name().unwrap().to_string_lossy();
        assert!(name.starts_with("README.conflict."), "unexpected: {name}");
        assert!(!name.ends_with('.'), "must not leave a trailing dot: {name}");
    }

    #[test]
    fn exceeds_path_limit_flags_long_full_path() {
        let base = Path::new("C:/Users/x/Sync");
        let long = format!("/{}", "a".repeat(300));
        assert_eq!(
            exceeds_path_limit(base, &long, WINDOWS_PATH_LIMIT),
            Some(base.join("a".repeat(300)).as_os_str().to_string_lossy().chars().count()),
        );
    }

    #[test]
    fn exceeds_path_limit_ignores_short_path() {
        let base = Path::new("C:/Sync");
        assert_eq!(exceeds_path_limit(base, "/notes.txt", WINDOWS_PATH_LIMIT), None);
    }

    #[cfg(target_os = "windows")]
    #[test]
    fn too_long_issue_is_non_blocking_with_length_params() {
        let base = Path::new("C:/Users/x/Sync");
        let long = format!("/{}.txt", "a".repeat(300));
        let issue = check_server_path(base, &long).expect("long path must warn");
        assert_eq!(issue.code(), "path_too_long");
        assert!(!issue.blocking(), "path length must not block the sync");
        assert_eq!(issue.params()["limit"], WINDOWS_PATH_LIMIT);
        assert!(issue.params()["len"].as_u64().unwrap() > WINDOWS_PATH_LIMIT as u64);
    }

    #[cfg(target_os = "windows")]
    #[test]
    fn invalid_chars_take_priority_over_length_and_block() {
        let base = Path::new("C:/Sync");
        // Long *and* illegal (`?`): the blocking issue wins so we never emit a
        // non-blocking warning for a path that also cannot be created at all.
        let long_illegal = format!("/{}?.txt", "a".repeat(300));
        let issue = check_server_path(base, &long_illegal).expect("illegal path must warn");
        assert_eq!(issue.code(), "invalid_chars");
        assert!(issue.blocking());
    }

    #[test]
    fn short_valid_path_has_no_issue() {
        let base = Path::new("C:/Sync");
        assert!(check_server_path(base, "/notes.txt").is_none());
    }
}
