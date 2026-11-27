//! OS notification rendering for the sync engine.
//!
//! Two surfaces:
//! - `SyncEngine::notify_transfer_summary` — drained per-cycle, throttled,
//!   ONE toast for the whole cycle (all affected sync roots combined),
//!   tagged so a later cycle's toast replaces it in Action Center instead of
//!   stacking a new entry every throttle window.
//! - `SyncEngine::os_notify` — fire-and-forget for one-off events
//!   (root pruned, bulk-delete guard).
//!
//! The pure string-building helpers (`build_summary_body`, `aggregate_verb`,
//! `parent_of`, `common_path_prefix`, `summary_title_and_lines`) live here as
//! well — they have no engine dependency and stay unit-testable.

use std::time::Instant;
use tauri_plugin_notification::NotificationExt;

use super::{SyncEngine, TransferOp, TransferRecord, TRANSFER_NOTIFY_THROTTLE};

/// Group id for the per-cycle summary toast. Combined with a fixed tag, this
/// makes Windows replace the previous summary toast in Action Center rather
/// than piling up a new entry every throttle window.
#[cfg(windows)]
const SUMMARY_GROUP: &str = "privasync-sync-summary";
#[cfg(windows)]
const SUMMARY_TAG: &str = "sync-summary";

impl SyncEngine {
    /// Drain the per-cycle transfer log and, if anything moved AND we are
    /// outside the throttle window, fire ONE OS toast covering every
    /// affected sync root. Per root the line resolves top-down:
    /// - 1 file        → filename + operation
    /// - N files, same dir   → that dir + count + operation
    /// - N files, mixed dirs → deepest common parent (or root) + count
    pub(super) fn notify_transfer_summary(&self) {
        let items: Vec<TransferRecord> = match self.transfers.lock() {
            Ok(mut v) => std::mem::take(&mut *v),
            Err(_)    => return,
        };
        if items.is_empty() {
            return;
        }

        {
            let mut last = self.last_transfer_notify.lock().unwrap();
            if let Some(t) = *last {
                if t.elapsed() < TRANSFER_NOTIFY_THROTTLE {
                    // Within throttle window — drop the batch; next eligible
                    // cycle will surface its own summary.
                    return;
                }
            }
            *last = Some(Instant::now());
        }

        // Group by sync root, but surface a single toast for the whole
        // cycle — this used to fire one toast per root, which stacked N
        // toasts every throttle window whenever N roots synced at once.
        let mut by_root: std::collections::BTreeMap<String, Vec<TransferRecord>> =
            std::collections::BTreeMap::new();
        for it in items {
            by_root.entry(it.root_name.clone()).or_default().push(it);
        }

        let (title, lines) = summary_title_and_lines(&by_root);
        self.show_summary_toast(&title, &lines);
    }

    /// Fire-and-forget OS notification. Used for one-off events (root pruned,
    /// bulk-delete guard) — no throttling because these events are rare by
    /// design and each one is individually actionable. Untagged: each is its
    /// own event, so stacking is the correct behaviour here.
    pub(super) fn os_notify(&self, title: &str, body: &str) {
        let _ = self.app
            .notification()
            .builder()
            .title(title)
            .body(body)
            .show();
    }

    /// Show the per-cycle summary toast. Goes straight through
    /// `windows::UI::Notifications::ToastNotification` with an explicit
    /// tag+group so a later cycle replaces this toast instead of adding a
    /// new one — `tauri-plugin-notification`'s desktop backend drops both on
    /// Windows, and its `tauri-winrt-notification` layer only wires a tag
    /// through the progress-bar path, not a plain toast.
    #[cfg(windows)]
    fn show_summary_toast(&self, title: &str, lines: &[String]) {
        use windows::core::HSTRING;
        use windows::Data::Xml::Dom::XmlDocument;
        use windows::UI::Notifications::{ToastNotification, ToastNotificationManager};

        let mut xml = String::from(r#"<toast><visual><binding template="ToastGeneric">"#);
        xml.push_str(&format!("<text>{}</text>", xml_escape(title)));
        for line in lines.iter().take(2) {
            xml.push_str(&format!("<text>{}</text>", xml_escape(line)));
        }
        xml.push_str("</binding></visual></toast>");

        let build = || -> windows::core::Result<()> {
            let doc = XmlDocument::new()?;
            doc.LoadXml(&HSTRING::from(xml.as_str()))?;
            let toast = ToastNotification::CreateToastNotification(&doc)?;
            toast.SetTag(&HSTRING::from(SUMMARY_TAG))?;
            toast.SetGroup(&HSTRING::from(SUMMARY_GROUP))?;
            let notifier = ToastNotificationManager::CreateToastNotifierWithId(&HSTRING::from(
                self.windows_app_id().as_str(),
            ))?;
            notifier.Show(&toast)
        };
        if let Err(e) = build() {
            tracing::warn!("sync-summary toast failed: {e:?}");
        }
    }

    #[cfg(not(windows))]
    fn show_summary_toast(&self, title: &str, lines: &[String]) {
        let _ = self.app
            .notification()
            .builder()
            .title(title)
            .body(lines.join("\n"))
            .show();
    }

    /// AppUserModelID for the toast notifier. Mirrors the notification
    /// plugin's own rule (see `tauri-plugin-notification`'s desktop backend):
    /// dev builds run from `target/debug`/`target/release` without a
    /// registered AUMID, so passing the real bundle identifier there would
    /// make `CreateToastNotifierWithId` fail — fall back to the well-known
    /// PowerShell AUMID in that case.
    #[cfg(windows)]
    fn windows_app_id(&self) -> String {
        use std::path::MAIN_SEPARATOR as SEP;
        const POWERSHELL_APP_ID: &str =
            "{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\\WindowsPowerShell\\v1.0\\powershell.exe";

        let identifier = self.app.config().identifier.clone();
        let Ok(exe) = tauri::utils::platform::current_exe() else {
            return POWERSHELL_APP_ID.to_string();
        };
        let curr_dir = exe.parent().map(|d| d.display().to_string()).unwrap_or_default();
        if curr_dir.ends_with(format!("{SEP}target{SEP}debug").as_str())
            || curr_dir.ends_with(format!("{SEP}target{SEP}release").as_str())
        {
            POWERSHELL_APP_ID.to_string()
        } else {
            identifier
        }
    }
}

/// Resolve the toast title + up to two body lines for a whole sync cycle.
/// One root → keep the root name in the title, one line for its summary.
/// Multiple roots → generic title, one line per root; beyond two roots the
/// rest collapse into a trailing "+N weitere" line (ToastGeneric only
/// reliably renders a title plus two body lines).
fn summary_title_and_lines(
    by_root: &std::collections::BTreeMap<String, Vec<TransferRecord>>,
) -> (String, Vec<String>) {
    if by_root.len() == 1 {
        let (root_name, group) = by_root.iter().next().unwrap();
        return (format!("PrivaSync — {root_name}"), vec![build_summary_body(group)]);
    }

    let mut lines: Vec<String> = by_root
        .iter()
        .map(|(root_name, group)| format!("{root_name}: {}", build_summary_body(group)))
        .collect();
    if lines.len() > 2 {
        let overflow = lines.len() - 1;
        lines.truncate(1);
        lines.push(format!("+{overflow} weitere Ordner synchronisiert"));
    }
    ("PrivaSync".to_string(), lines)
}

/// Escape the five XML predefined entities for embedding into toast XML.
#[cfg(windows)]
fn xml_escape(s: &str) -> String {
    s.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;")
}

/// Build one sync root's summary line. Top-down resolution: 1 file → name;
/// multiple files in one dir → that dir; spread across dirs → deepest common
/// parent; files at the sync-root itself → no location clause at all.
fn build_summary_body(items: &[TransferRecord]) -> String {
    debug_assert!(!items.is_empty());
    let verb = aggregate_verb(items);

    if items.len() == 1 {
        let name = filename_of(&items[0].path);
        return format!("'{name}' {verb}");
    }

    let dirs: Vec<&str> = items.iter().map(|i| parent_of(&i.path)).collect();
    let scope = common_path_prefix(&dirs);
    if scope.is_empty() {
        return format!("{} Dateien {verb}", items.len());
    }
    format!("{} Dateien in '/{scope}' {verb}", items.len())
}

/// Returns the German verb for the notification: `hochgeladen` for pure
/// uploads, `heruntergeladen` for pure downloads, `gelöscht` for pure
/// deletes, `verschoben` for pure moves (server-side renames mirrored onto
/// disk), `synchronisiert` for any mix.
fn aggregate_verb(items: &[TransferRecord]) -> &'static str {
    let mut up = false; let mut down = false; let mut del = false; let mut ren = false;
    for it in items {
        match it.op {
            TransferOp::Uploaded   => up   = true,
            TransferOp::Downloaded => down = true,
            TransferOp::Deleted    => del  = true,
            // A local rename (server-side move mirrored onto disk) is a move of
            // its own — surfaced as "verschoben" when the batch is purely moves,
            // folded into the mixed "synchronisiert" otherwise.
            TransferOp::Renamed    => ren  = true,
        }
    }
    match (up, down, del, ren) {
        (true, false, false, false) => "hochgeladen",
        (false, true, false, false) => "heruntergeladen",
        (false, false, true, false) => "gelöscht",
        (false, false, false, true) => "verschoben",
        _                           => "synchronisiert",
    }
}

fn filename_of(path: &str) -> &str {
    path.trim_end_matches('/').rsplit('/').next().unwrap_or(path)
}

/// Parent directory of a server path. Returns "" for files at the sync-root.
fn parent_of(path: &str) -> &str {
    let p = path.trim_start_matches('/');
    p.rsplit_once('/').map(|(parent, _)| parent).unwrap_or("")
}

/// Longest common slash-separated path prefix shared by all entries.
/// Operates on whole path components — "/foo/bar" and "/foobar" share "".
fn common_path_prefix(paths: &[&str]) -> String {
    if paths.is_empty() { return String::new(); }
    let split = |p: &str| -> Vec<String> {
        p.trim_matches('/').split('/').filter(|s| !s.is_empty()).map(String::from).collect()
    };
    let first: Vec<String> = split(paths[0]);
    let rest: Vec<Vec<String>> = paths[1..].iter().map(|p| split(p)).collect();

    let mut common: Vec<String> = Vec::new();
    'outer: for (i, c) in first.iter().enumerate() {
        for other in &rest {
            if other.get(i) != Some(c) { break 'outer; }
        }
        common.push(c.clone());
    }
    common.join("/")
}

#[cfg(test)]
mod tests {
    use super::*;

    fn rec(op: TransferOp, path: &str) -> TransferRecord {
        TransferRecord { op, path: path.to_string(), root_name: "Root".to_string() }
    }

    /// A server-side move that was recognised as a rename (id-based) records
    /// `Renamed` ops only. The summary must read "verschoben", not the generic
    /// "synchronisiert" — and never "gelöscht". Regression for the empty-dir
    /// move that surfaced as a delete toast.
    #[test]
    fn pure_rename_batch_reads_verschoben() {
        let single = [rec(TransferOp::Renamed, "/sub/empty")];
        assert_eq!(build_summary_body(&single), "'empty' verschoben");

        let many = [
            rec(TransferOp::Renamed, "/dst/a"),
            rec(TransferOp::Renamed, "/dst/b"),
        ];
        assert_eq!(build_summary_body(&many), "2 Dateien in '/dst' verschoben");
    }

    /// A rename mixed with a real download stays "synchronisiert" — we only
    /// claim "verschoben" when the whole batch is moves.
    #[test]
    fn rename_mixed_with_download_stays_synchronisiert() {
        let items = [
            rec(TransferOp::Renamed, "/a"),
            rec(TransferOp::Downloaded, "/b"),
        ];
        assert_eq!(aggregate_verb(&items), "synchronisiert");
    }

    /// A pure-upload batch reads "hochgeladen", a pure-download batch
    /// "heruntergeladen", a pure-delete batch "gelöscht" — the three other
    /// single-op German verbs the notification can claim.
    #[test]
    fn single_op_batches_use_their_own_verb() {
        assert_eq!(aggregate_verb(&[rec(TransferOp::Uploaded, "/a")]), "hochgeladen");
        assert_eq!(aggregate_verb(&[rec(TransferOp::Downloaded, "/a")]), "heruntergeladen");
        assert_eq!(aggregate_verb(&[rec(TransferOp::Deleted, "/a")]), "gelöscht");
    }

    /// A mix of any two distinct ops (not just rename+download) falls back to
    /// the generic "synchronisiert" verb.
    #[test]
    fn mixed_upload_and_delete_is_synchronisiert() {
        let items = [rec(TransferOp::Uploaded, "/a"), rec(TransferOp::Deleted, "/b")];
        assert_eq!(aggregate_verb(&items), "synchronisiert");
    }

    #[test]
    fn filename_of_extracts_the_last_segment() {
        assert_eq!(filename_of("/sub/dir/notes.txt"), "notes.txt");
        assert_eq!(filename_of("/notes.txt"), "notes.txt");
        // A trailing slash (directory path) still yields the directory name,
        // not an empty string.
        assert_eq!(filename_of("/sub/dir/"), "dir");
    }

    #[test]
    fn common_path_prefix_finds_shared_ancestor() {
        assert_eq!(common_path_prefix(&["a/b/x.txt", "a/b/y.txt"]), "a/b");
        assert_eq!(common_path_prefix(&["a/b/x.txt", "a/c/y.txt"]), "a");
        // Component-wise, not textual: "foo" and "foobar" share no path
        // component even though they share a character prefix.
        assert_eq!(common_path_prefix(&["foo/x.txt", "foobar/y.txt"]), "");
        assert_eq!(common_path_prefix(&[]), "");
    }

    /// Multiple files spread across sibling directories roll up to their
    /// deepest common parent, with the count in the body.
    #[test]
    fn build_summary_body_mixed_dirs_uses_common_parent() {
        let items = [
            rec(TransferOp::Uploaded, "/proj/src/a.rs"),
            rec(TransferOp::Uploaded, "/proj/docs/b.md"),
        ];
        assert_eq!(build_summary_body(&items), "2 Dateien in '/proj' hochgeladen");
    }

    /// Files with no shared parent (both at sync-root level) drop the
    /// location clause entirely instead of naming a meaningless "/" root.
    #[test]
    fn build_summary_body_root_level_drops_location_clause() {
        let items = [
            rec(TransferOp::Downloaded, "/a.txt"),
            rec(TransferOp::Downloaded, "/b.txt"),
        ];
        assert_eq!(build_summary_body(&items), "2 Dateien heruntergeladen");
    }

    /// A single-file batch names the file directly, without a directory label.
    #[test]
    fn build_summary_body_single_file_names_it_directly() {
        let items = [rec(TransferOp::Deleted, "/sub/gone.txt")];
        assert_eq!(build_summary_body(&items), "'gone.txt' gelöscht");
    }

    /// A single affected root keeps the root name in the title and produces
    /// exactly one body line — the pre-existing single-root behaviour.
    #[test]
    fn summary_title_and_lines_single_root() {
        let mut by_root = std::collections::BTreeMap::new();
        by_root.insert("Fotos".to_string(), vec![rec(TransferOp::Uploaded, "/a.jpg")]);
        let (title, lines) = summary_title_and_lines(&by_root);
        assert_eq!(title, "PrivaSync — Fotos");
        assert_eq!(lines, vec!["'a.jpg' hochgeladen".to_string()]);
    }

    /// Two affected roots both fit as their own line under a generic title —
    /// no toast-per-root stacking.
    #[test]
    fn summary_title_and_lines_two_roots_get_one_line_each() {
        let mut by_root = std::collections::BTreeMap::new();
        by_root.insert("Fotos".to_string(), vec![rec(TransferOp::Uploaded, "/a.jpg")]);
        by_root.insert("Dokumente".to_string(), vec![rec(TransferOp::Downloaded, "/b.pdf")]);
        let (title, lines) = summary_title_and_lines(&by_root);
        assert_eq!(title, "PrivaSync");
        assert_eq!(
            lines,
            vec![
                "Dokumente: 'b.pdf' heruntergeladen".to_string(),
                "Fotos: 'a.jpg' hochgeladen".to_string(),
            ]
        );
    }

    /// More than two affected roots collapse into one named line plus an
    /// overflow line, since ToastGeneric only reliably renders two body lines.
    #[test]
    fn summary_title_and_lines_more_than_two_roots_collapse_overflow() {
        let mut by_root = std::collections::BTreeMap::new();
        by_root.insert("A".to_string(), vec![rec(TransferOp::Uploaded, "/a")]);
        by_root.insert("B".to_string(), vec![rec(TransferOp::Uploaded, "/b")]);
        by_root.insert("C".to_string(), vec![rec(TransferOp::Uploaded, "/c")]);
        let (title, lines) = summary_title_and_lines(&by_root);
        assert_eq!(title, "PrivaSync");
        assert_eq!(lines.len(), 2);
        assert_eq!(lines[1], "+2 weitere Ordner synchronisiert");
    }
}
