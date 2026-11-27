//! `privasync://` deep-link handling.
//!
//! Pure core for the protocol handler: parse an incoming URL into a
//! [`RevealRequest`] and resolve it to a local directory inside a registered
//! sync root, which is then shown in the OS file manager. Anything that fails
//! validation resolves to `None` — the caller drops the request silently
//! (with a log line), never opening a path outside the sync roots.

use std::path::{Path, PathBuf};

/// A parsed `privasync://reveal?...` request.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RevealRequest {
    /// Server-side root id (owned roots) or share id (mounts) — matches
    /// `sync_roots.server_root_id` in the local DB.
    pub root_id: String,
    /// Server-form path relative to the root (`/docs/report.pdf`), already
    /// percent-decoded. Empty string addresses the root itself.
    pub path: String,
}

/// Parse a deep-link URL. Only the `reveal` action is understood; anything
/// else (wrong scheme, missing `root`, unparseable URL) returns `None`.
pub fn parse_reveal_url(url: &str) -> Option<RevealRequest> {
    let parsed = url::Url::parse(url).ok()?;
    if parsed.scheme() != "privasync" || parsed.host_str() != Some("reveal") {
        return None;
    }
    let mut root_id = None;
    let mut path = String::new();
    for (k, v) in parsed.query_pairs() {
        match k.as_ref() {
            "root" => root_id = Some(v.into_owned()),
            "path" => path = v.into_owned(),
            _ => {}
        }
    }
    Some(RevealRequest { root_id: root_id?, path })
}

/// Resolve a parsed request to an absolute local path inside the matching
/// sync root. Roots are matched on `server_root_id` (owned roots) / share id
/// (mounts). Returns `None` for unknown roots or any path that is not a plain
/// relative descent (traversal, absolute components) — callers must treat
/// `None` as a silent drop.
pub fn resolve_target(
    roots: &[crate::contextmenu::RootRef],
    req: &RevealRequest,
) -> Option<PathBuf> {
    let root = roots.iter().find(|r| r.server_root_id == req.root_id)?;
    let mut target = PathBuf::from(&root.local_path);
    for seg in req.path.split('/').filter(|s| !s.is_empty()) {
        // Each segment must be a single plain name: no traversal, no drive
        // prefixes, no backslash-smuggled sub-paths (Windows treats `\` as a
        // separator even when the URL used `/`).
        let is_plain = matches!(
            Path::new(seg).components().collect::<Vec<_>>().as_slice(),
            [std::path::Component::Normal(_)]
        );
        if !is_plain || seg.contains('\\') {
            return None;
        }
        target.push(seg);
    }
    Some(target)
}

/// Directory to show in the file manager for a reveal target: directories
/// open themselves, files open their containing directory. `is_dir` comes
/// from a filesystem check, so a not-yet-synced file also falls back to its
/// parent — the location exists even when the content doesn't.
pub fn reveal_dir(target: &Path, is_dir: bool) -> PathBuf {
    if is_dir {
        return target.to_path_buf();
    }
    // A file at the root of a sync root always has a parent (the root's
    // local_path), so the fallback to `target` is unreachable in practice.
    target.parent().map_or_else(|| target.to_path_buf(), Path::to_path_buf)
}

/// Whether a second-instance argv carries a `privasync://` URL. Such a
/// launch exists only to hand the URL over — the main window must stay
/// hidden, unlike a plain double-click relaunch.
pub fn has_reveal_url(argv: &[String]) -> bool {
    argv.iter().any(|a| a.starts_with("privasync://"))
}

/// Platform-specific invocation to open a local directory in the OS file
/// manager. Fired via `std::process::Command::spawn` — detached, no plugin
/// runtime, returns immediately. Tray clicks need this because the deprecated
/// `tauri_plugin_shell::Shell::open` blocks while the webview is hidden and
/// later unloads twice (#?).
pub fn explorer_invocation(path: &str) -> (&'static str, Vec<String>) {
    #[cfg(target_os = "windows")]
    let program = "explorer.exe";
    #[cfg(target_os = "macos")]
    let program = "open";
    #[cfg(all(unix, not(target_os = "macos")))]
    let program = "xdg-open";
    (program, vec![path.to_string()])
}

/// Full deep-link handling: parse the URL, resolve it against the registered
/// sync roots and show the target's directory in the OS file manager.
/// Invalid or out-of-root requests are dropped silently apart from a log
/// line — the browser side gets no feedback channel anyway.
pub fn handle_url(db: &crate::db::LocalDb, url: &str) {
    let Some(req) = parse_reveal_url(url) else {
        tracing::warn!("deep link ignored (not a privasync reveal URL): {url}");
        return;
    };
    let roots = crate::contextmenu_pipe::db_root_refs(db);
    let Some(target) = resolve_target(&roots, &req) else {
        tracing::warn!(
            "deep link dropped (unknown root or invalid path): root={} path={:?}",
            req.root_id, req.path,
        );
        return;
    };
    let dir = reveal_dir(&target, target.is_dir());
    let (program, args) = explorer_invocation(&dir.to_string_lossy());
    if let Err(e) = std::process::Command::new(program).args(&args).spawn() {
        tracing::warn!("deep link reveal spawn failed: {e}");
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::contextmenu::RootRef;

    fn root(server_root_id: &str, local_path: &str) -> RootRef {
        RootRef {
            id:             format!("local-{server_root_id}"),
            local_path:     local_path.to_string(),
            read_only:      false,
            is_mount:       false,
            server_root_id: server_root_id.to_string(),
            name:           server_root_id.to_string(),
        }
    }

    #[test]
    fn parses_reveal_url_with_root_and_encoded_path() {
        let req = parse_reveal_url("privasync://reveal?root=abc-123&path=%2Fdocs%2FReport%201.pdf")
            .expect("should parse");
        assert_eq!(req.root_id, "abc-123");
        assert_eq!(req.path, "/docs/Report 1.pdf");
    }

    #[test]
    fn rejects_foreign_scheme_unknown_action_and_missing_root() {
        assert_eq!(parse_reveal_url("https://reveal?root=a&path=%2Fx"), None);
        assert_eq!(parse_reveal_url("privasync://open?root=a&path=%2Fx"), None);
        assert_eq!(parse_reveal_url("privasync://reveal?path=%2Fx"), None);
        assert_eq!(parse_reveal_url("not a url"), None);
    }

    #[test]
    fn resolves_path_inside_matching_root() {
        let roots = vec![
            root("srv-a", r"C:\Sync\Docs"),
            root("srv-b", r"C:\Sync\Music"),
        ];
        let req = RevealRequest { root_id: "srv-b".into(), path: "/albums/cover.jpg".into() };
        let got = resolve_target(&roots, &req).expect("should resolve");
        assert_eq!(got, Path::new(r"C:\Sync\Music").join("albums").join("cover.jpg"));
    }

    #[test]
    fn empty_path_resolves_to_root_itself() {
        let roots = vec![root("srv-a", r"C:\Sync\Docs")];
        let req = RevealRequest { root_id: "srv-a".into(), path: String::new() };
        let got = resolve_target(&roots, &req).expect("should resolve");
        assert_eq!(got, PathBuf::from(r"C:\Sync\Docs"));
    }

    #[test]
    fn drops_traversal_and_absolute_segments() {
        let roots = vec![root("srv-a", r"C:\Sync\Docs")];
        let attempts = [
            "/../secrets.txt",
            "/docs/../../secrets.txt",
            "/..",
            r"/docs/..\..\secrets.txt",
            r"/C:\Windows\system32",
            "/./docs",
        ];
        for path in attempts {
            let req = RevealRequest { root_id: "srv-a".into(), path: path.into() };
            assert_eq!(resolve_target(&roots, &req), None, "must drop {path:?}");
        }
    }

    #[test]
    fn drops_unknown_root_id() {
        let roots = vec![root("srv-a", r"C:\Sync\Docs")];
        let req = RevealRequest { root_id: "nope".into(), path: "/x.txt".into() };
        assert_eq!(resolve_target(&roots, &req), None);
    }

    #[test]
    fn detects_reveal_url_in_second_instance_argv() {
        let exe = "privasync-desktop.exe".to_string();
        assert!(has_reveal_url(&[
            exe.clone(),
            "privasync://reveal?root=a&path=%2Fx".to_string(),
        ]));
        assert!(!has_reveal_url(std::slice::from_ref(&exe)));
        assert!(!has_reveal_url(&[exe, "--flag".to_string()]));
        assert!(!has_reveal_url(&[]));
    }

    // reveal_dir relies on Path::parent over backslash paths, which only
    // segments on Windows; elsewhere `\` is an ordinary char.
    #[cfg(target_os = "windows")]
    #[test]
    fn reveal_dir_opens_dir_itself_and_file_parent() {
        let dir = Path::new(r"C:\Sync\Docs\projects");
        assert_eq!(reveal_dir(dir, true), PathBuf::from(r"C:\Sync\Docs\projects"));

        let file = Path::new(r"C:\Sync\Docs\projects\plan.pdf");
        assert_eq!(reveal_dir(file, false), PathBuf::from(r"C:\Sync\Docs\projects"));
    }

    #[cfg(target_os = "windows")]
    #[test]
    fn explorer_invocation_uses_explorer_exe_on_windows() {
        let (program, args) = explorer_invocation(r"C:\Users\test\Documents");
        assert_eq!(program, "explorer.exe");
        assert_eq!(args, vec![r"C:\Users\test\Documents".to_string()]);
    }
}
