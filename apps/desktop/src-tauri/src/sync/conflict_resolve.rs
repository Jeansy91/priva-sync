//! Conflict resolution strategies on the desktop (ADR-0003).
//!
//! A desktop conflict is local-only: when reconcile/upload detects a parallel
//! change it downloads the server's canonical version onto the file and saves
//! the prior local bytes as a `.conflict.<ts>` copy next to it, marking the
//! row `conflict`. Resolving picks what happens to those two on-disk files:
//!
//! - `KeepCurrent`  — keep the server version, drop the loser copy.
//! - `KeepConflict` — promote the loser bytes onto the file, re-upload.
//! - `KeepBoth`     — keep both; the loser copy becomes a regular synced file.
//!
//! The planning step is pure (no I/O) so the file-operation logic is unit-
//! testable; the executor and the Tauri command perform the actual side
//! effects.

use std::path::{Path, PathBuf};

use anyhow::{Context, Result};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[expect(clippy::enum_variant_names, reason = "mirrors the server's keep_current/keep_conflict/keep_both wire names")]
pub enum ResolveStrategy {
    KeepCurrent,
    KeepConflict,
    KeepBoth,
}

impl ResolveStrategy {
    /// Parse the wire value sent from the UI. Mirrors the server strategy
    /// names so both clients speak the same vocabulary.
    pub fn parse(s: &str) -> Option<Self> {
        match s {
            "keep_current"  => Some(Self::KeepCurrent),
            "keep_conflict" => Some(Self::KeepConflict),
            "keep_both"     => Some(Self::KeepBoth),
            _               => None,
        }
    }
}

/// A single filesystem step of a resolution. `original` is the synced file
/// (currently holding the server version); `copy` is the `.conflict.<ts>`
/// loser file on disk.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum FileOp {
    /// Remove the loser copy.
    DeleteCopy,
    /// Overwrite the original with the loser bytes, then remove the copy.
    PromoteCopyOntoOriginal,
    /// Rename the loser copy to a regular (non-`.conflict.`) name so the sync
    /// engine stops ignoring it and uploads it as its own file.
    RenameCopyTo(PathBuf),
}

/// What the resolution leaves the original file's DB row as.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum NewStatus {
    /// The original now matches the server — no upload needed.
    Synced,
    /// The original's bytes changed locally and must be re-uploaded.
    Pending,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ResolvePlan {
    pub ops:    Vec<FileOp>,
    pub status: NewStatus,
}

/// Pure planner: maps a strategy + the loser copy path to the filesystem ops
/// and the resulting DB status for the original row.
pub fn plan_resolve(strategy: ResolveStrategy, conflict_copy: &Path) -> ResolvePlan {
    match strategy {
        ResolveStrategy::KeepCurrent => ResolvePlan {
            ops:    vec![FileOp::DeleteCopy],
            status: NewStatus::Synced,
        },
        ResolveStrategy::KeepConflict => ResolvePlan {
            ops:    vec![FileOp::PromoteCopyOntoOriginal],
            status: NewStatus::Pending,
        },
        ResolveStrategy::KeepBoth => ResolvePlan {
            ops:    vec![FileOp::RenameCopyTo(kept_copy_path(conflict_copy))],
            status: NewStatus::Synced,
        },
    }
}

/// Turns a `.conflict.<ts>` loser path into a regular filename the sync engine
/// will not ignore. The engine skips any path containing the literal
/// `.conflict.` infix, so we swap it for `.local-`, preserving stem, timestamp
/// and extension (e.g. `notes.conflict.20260101_120000.txt` →
/// `notes.local-20260101_120000.txt`).
pub fn kept_copy_path(conflict_copy: &Path) -> PathBuf {
    let name = conflict_copy
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or("file");
    let renamed = name.replacen(".conflict.", ".local-", 1);
    conflict_copy.with_file_name(renamed)
}

/// One line of a text comparison between the two versions. `kind` is
/// `same` | `removed` (only in the server version) | `added` (only in the
/// local copy).
#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize)]
pub struct DiffLine {
    pub kind: String,
    pub text: String,
}

/// Line-based LCS diff between the server version (`current`) and the local
/// conflict copy (`conflict`). Pure; the command layer reads the bytes.
pub fn diff_lines(current: &str, conflict: &str) -> Vec<DiffLine> {
    let a: Vec<&str> = current.split('\n').collect();
    let b: Vec<&str> = conflict.split('\n').collect();
    let n = a.len();
    let m = b.len();

    // LCS length table.
    let mut lcs = vec![vec![0usize; m + 1]; n + 1];
    for i in (0..n).rev() {
        for j in (0..m).rev() {
            lcs[i][j] = if a[i] == b[j] {
                lcs[i + 1][j + 1] + 1
            } else {
                lcs[i + 1][j].max(lcs[i][j + 1])
            };
        }
    }

    let mut out = Vec::new();
    let (mut i, mut j) = (0, 0);
    while i < n && j < m {
        if a[i] == b[j] {
            out.push(DiffLine { kind: "same".into(), text: a[i].to_string() });
            i += 1;
            j += 1;
        } else if lcs[i + 1][j] >= lcs[i][j + 1] {
            out.push(DiffLine { kind: "removed".into(), text: a[i].to_string() });
            i += 1;
        } else {
            out.push(DiffLine { kind: "added".into(), text: b[j].to_string() });
            j += 1;
        }
    }
    while i < n { out.push(DiffLine { kind: "removed".into(), text: a[i].to_string() }); i += 1; }
    while j < m { out.push(DiffLine { kind: "added".into(),   text: b[j].to_string() }); j += 1; }
    out
}

/// Cheap binary sniff: real text never contains a NUL byte.
pub fn is_probably_text(bytes: &[u8]) -> bool {
    !bytes.contains(&0)
}

/// Size and modification time of one side of a conflict, as found on disk.
#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize)]
pub struct VersionFacts {
    pub size: u64,
    /// Milliseconds since the Unix epoch; `None` when the file system has no
    /// usable modification time.
    pub modified_ms: Option<i64>,
}

pub fn version_facts(path: &Path) -> Result<VersionFacts> {
    use std::time::UNIX_EPOCH;
    let meta = std::fs::metadata(path).with_context(|| format!("reading metadata of {}", path.display()))?;
    let modified_ms = meta.modified().ok().map(|t| match t.duration_since(UNIX_EPOCH) {
        Ok(d) => d.as_millis() as i64,
        Err(before) => -(before.duration().as_millis() as i64),
    });
    Ok(VersionFacts { size: meta.len(), modified_ms })
}

/// Largest image either side may be and still get an inline preview.
pub const PREVIEW_SIZE_LIMIT: u64 = 25 * 1024 * 1024;

/// MIME type for the image formats the webview renders natively; TIFF and RAW
/// formats are left out on purpose.
pub fn image_mime(path: &Path) -> Option<&'static str> {
    let ext = path.extension()?.to_str()?.to_ascii_lowercase();
    Some(match ext.as_str() {
        "jpg" | "jpeg" => "image/jpeg",
        "png" => "image/png",
        "gif" => "image/gif",
        "webp" => "image/webp",
        "bmp" => "image/bmp",
        "avif" => "image/avif",
        _ => return None,
    })
}

/// `data:` URL for an image file, or `None` when it is not a previewable image
/// or exceeds [`PREVIEW_SIZE_LIMIT`]. `kind_from` supplies the extension: the
/// loser copy's own name carries a `.conflict-…` marker, so the original path
/// decides the type for both sides.
pub fn image_data_url(path: &Path, kind_from: &Path) -> Result<Option<String>> {
    use base64::Engine;
    let Some(mime) = image_mime(kind_from) else { return Ok(None) };
    if version_facts(path)?.size > PREVIEW_SIZE_LIMIT {
        return Ok(None);
    }
    let bytes = std::fs::read(path).with_context(|| format!("reading {}", path.display()))?;
    let encoded = base64::engine::general_purpose::STANDARD.encode(bytes);
    Ok(Some(format!("data:{mime};base64,{encoded}")))
}

/// Applies a planned resolution to disk. `original` is the synced file path,
/// `conflict_copy` the loser copy. Best-effort cleanup is not used here: a
/// failed file op aborts so the caller leaves the conflict row open rather
/// than half-resolving.
pub fn execute_plan(plan: &ResolvePlan, original: &Path, conflict_copy: &Path) -> Result<()> {
    for op in &plan.ops {
        match op {
            FileOp::DeleteCopy => {
                if conflict_copy.exists() {
                    std::fs::remove_file(conflict_copy)
                        .with_context(|| format!("removing conflict copy {}", conflict_copy.display()))?;
                }
            }
            FileOp::PromoteCopyOntoOriginal => {
                std::fs::copy(conflict_copy, original)
                    .with_context(|| format!("promoting {} onto {}", conflict_copy.display(), original.display()))?;
                std::fs::remove_file(conflict_copy)
                    .with_context(|| format!("removing conflict copy {}", conflict_copy.display()))?;
            }
            FileOp::RenameCopyTo(target) => {
                std::fs::rename(conflict_copy, target)
                    .with_context(|| format!("renaming {} to {}", conflict_copy.display(), target.display()))?;
            }
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;

    #[test]
    fn test_keep_current_drops_copy_and_stays_synced() {
        let plan = plan_resolve(ResolveStrategy::KeepCurrent, Path::new("/x/n.conflict.20260101_120000.txt"));
        assert_eq!(plan.ops, vec![FileOp::DeleteCopy]);
        assert_eq!(plan.status, NewStatus::Synced);
    }

    #[test]
    fn test_keep_conflict_promotes_copy_and_marks_pending() {
        let plan = plan_resolve(ResolveStrategy::KeepConflict, Path::new("/x/n.conflict.20260101_120000.txt"));
        assert_eq!(plan.ops, vec![FileOp::PromoteCopyOntoOriginal]);
        assert_eq!(plan.status, NewStatus::Pending);
    }

    #[test]
    fn test_keep_both_renames_copy_to_non_magic_name() {
        let plan = plan_resolve(ResolveStrategy::KeepBoth, Path::new("/x/n.conflict.20260101_120000.txt"));
        assert_eq!(plan.status, NewStatus::Synced);
        match &plan.ops[..] {
            [FileOp::RenameCopyTo(target)] => {
                let s = target.to_string_lossy();
                assert!(!s.contains(".conflict."), "kept name must not be ignored by the engine: {s}");
                assert!(s.ends_with("n.local-20260101_120000.txt"), "unexpected kept name: {s}");
            }
            other => panic!("unexpected ops: {other:?}"),
        }
    }

    #[test]
    fn test_kept_copy_path_without_extension() {
        let p = kept_copy_path(Path::new("/x/README.conflict.20260101_120000"));
        assert_eq!(p, PathBuf::from("/x/README.local-20260101_120000"));
    }

    #[test]
    fn test_execute_keep_current_removes_loser_keeps_original() {
        let dir = tempfile::tempdir().unwrap();
        let orig = dir.path().join("n.txt");
        let copy = dir.path().join("n.conflict.20260101_120000.txt");
        fs::write(&orig, b"server").unwrap();
        fs::write(&copy, b"local").unwrap();

        let plan = plan_resolve(ResolveStrategy::KeepCurrent, &copy);
        execute_plan(&plan, &orig, &copy).unwrap();

        assert_eq!(fs::read(&orig).unwrap(), b"server");
        assert!(!copy.exists(), "loser copy must be gone");
    }

    #[test]
    fn test_execute_keep_conflict_promotes_loser_bytes() {
        let dir = tempfile::tempdir().unwrap();
        let orig = dir.path().join("n.txt");
        let copy = dir.path().join("n.conflict.20260101_120000.txt");
        fs::write(&orig, b"server").unwrap();
        fs::write(&copy, b"local-wins").unwrap();

        let plan = plan_resolve(ResolveStrategy::KeepConflict, &copy);
        execute_plan(&plan, &orig, &copy).unwrap();

        assert_eq!(fs::read(&orig).unwrap(), b"local-wins");
        assert!(!copy.exists(), "loser copy must be consumed");
    }

    #[test]
    fn test_execute_keep_both_renames_loser_to_regular_file() {
        let dir = tempfile::tempdir().unwrap();
        let orig = dir.path().join("n.txt");
        let copy = dir.path().join("n.conflict.20260101_120000.txt");
        fs::write(&orig, b"server").unwrap();
        fs::write(&copy, b"local").unwrap();

        let plan = plan_resolve(ResolveStrategy::KeepBoth, &copy);
        execute_plan(&plan, &orig, &copy).unwrap();

        let kept = dir.path().join("n.local-20260101_120000.txt");
        assert_eq!(fs::read(&orig).unwrap(), b"server", "original keeps server version");
        assert_eq!(fs::read(&kept).unwrap(), b"local", "loser kept under regular name");
        assert!(!copy.exists(), "the .conflict. copy was renamed away");
    }

    #[test]
    fn test_diff_lines_marks_changed_line() {
        let out = diff_lines("a\nx\nc", "a\ny\nc");
        assert!(out.contains(&DiffLine { kind: "removed".into(), text: "x".into() }));
        assert!(out.contains(&DiffLine { kind: "added".into(),   text: "y".into() }));
        let same: Vec<_> = out.iter().filter(|l| l.kind == "same").map(|l| l.text.clone()).collect();
        assert_eq!(same, vec!["a", "c"]);
    }

    #[test]
    fn test_is_probably_text() {
        assert!(is_probably_text("Hallo Wörld".as_bytes()));
        assert!(!is_probably_text(&[b'P', b'K', 0, b'x']));
    }

    #[test]
    fn test_parse_strategy() {
        assert_eq!(ResolveStrategy::parse("keep_current"),  Some(ResolveStrategy::KeepCurrent));
        assert_eq!(ResolveStrategy::parse("keep_conflict"), Some(ResolveStrategy::KeepConflict));
        assert_eq!(ResolveStrategy::parse("keep_both"),     Some(ResolveStrategy::KeepBoth));
        assert_eq!(ResolveStrategy::parse("bogus"),         None);
    }

    #[test]
    fn test_version_facts_reports_exact_size_and_mtime() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("a.bin");
        fs::write(&file, vec![7u8; 1234]).unwrap();
        let stamp = std::time::UNIX_EPOCH + std::time::Duration::from_secs(1_700_000_000);
        fs::File::options().write(true).open(&file).unwrap().set_modified(stamp).unwrap();

        let facts = version_facts(&file).unwrap();

        assert_eq!(facts, VersionFacts { size: 1234, modified_ms: Some(1_700_000_000_000) });
    }

    #[test]
    fn test_image_mime_covers_webview_formats_only() {
        for (name, mime) in [("a.JPG", "image/jpeg"), ("a.jpeg", "image/jpeg"), ("a.png", "image/png"), ("a.WebP", "image/webp")] {
            assert_eq!(image_mime(Path::new(name)), Some(mime), "{name}");
        }
        for name in ["scan.tif", "raw.cr2", "doc.pdf", "noext"] {
            assert_eq!(image_mime(Path::new(name)), None, "{name}");
        }
    }

    #[test]
    fn test_image_data_url_encodes_images_and_skips_other_files() {
        let dir = tempfile::tempdir().unwrap();
        let copy = dir.path().join("p.conflict-20261101T100000.jpg");
        fs::write(&copy, [0xFF, 0xD8, 0xFF]).unwrap();

        // The type comes from the original name, not the marker-laden copy name.
        let url = image_data_url(&copy, Path::new("p.jpg")).unwrap();
        assert_eq!(url.as_deref(), Some("data:image/jpeg;base64,/9j/"));
        assert_eq!(image_data_url(&copy, Path::new("p.tif")).unwrap(), None);
    }
}
