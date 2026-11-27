//! Per-root sync exclusion patterns (.gitignore semantics).
//!
//! Patterns are stored per sync root (DB column `ignore_patterns`) and applied
//! local→server only: a matching local file is never staged for upload or
//! delete. Downloads are untouched. The matcher is a thin wrapper over the
//! `ignore` crate's gitignore engine so anchoring, `dir/`, and `!negation`
//! behave exactly as users expect from a `.gitignore`.

use ignore::gitignore::{Gitignore, GitignoreBuilder};

/// Compiled exclusion matcher for one sync root. Cheap to build per reconcile
/// pass; holds no filesystem handles. Build once, query many.
#[derive(Clone)]
pub(crate) struct IgnoreMatcher {
    gi: Gitignore,
}

impl IgnoreMatcher {
    /// Build from the root's raw `ignore_patterns` text (one pattern per line,
    /// gitignore syntax). Blank lines and `#` comments are ignored.
    pub(crate) fn from_patterns(text: &str) -> Self {
        // The builder root is only used to anchor `/foo` patterns; matching is
        // done against root-relative paths, so an empty base is correct.
        let mut b = GitignoreBuilder::new("");
        for line in text.lines() {
            // GitignoreBuilder::add_line already treats blank/`#` lines as
            // no-ops, but skipping them keeps a malformed line from poisoning
            // the rest of the set.
            let _ = b.add_line(None, line);
        }
        let gi = b.build().unwrap_or_else(|_| Gitignore::empty());
        IgnoreMatcher { gi }
    }

    /// True iff `rel` (root-relative, '/'-separated, no leading slash) is
    /// excluded from sync. `is_dir` selects directory-only patterns (`dir/`).
    /// Parents are consulted too, so a file under an ignored directory matches.
    pub(crate) fn is_ignored(&self, rel: &str, is_dir: bool) -> bool {
        self.gi.matched_path_or_any_parents(rel, is_dir).is_ignore()
    }

    // (validation lives as a free function below — it builds one matcher per
    // line and so does not belong to an already-built matcher instance.)

    /// Whether the pattern set contains any negation (`!foo`) line.
    ///
    /// Without one, nothing inside an ignored directory can be un-ignored, so
    /// the walk may skip that subtree outright instead of descending into it
    /// only to discard everything it finds. With one, it must keep descending
    /// — a `!keep.txt` under an ignored folder has to stay reachable.
    pub(crate) fn has_negations(&self) -> bool {
        self.gi.num_whitelists() > 0
    }

    /// Same as [`is_ignored`] but for a DB-style path (leading-slash form,
    /// e.g. `/sub/file.bak`) as produced by `relative_path` and stored in
    /// `local_files`. Single seam shared by the reconcile scan and the watcher
    /// staging path so both strip the anchor identically.
    pub(crate) fn is_ignored_db_path(&self, db_rel: &str, is_dir: bool) -> bool {
        self.is_ignored(db_rel.trim_start_matches('/'), is_dir)
    }
}

/// One pattern line the gitignore engine rejected. `line` is 1-based so
/// it maps straight onto the editor's row numbers; `message` is the engine's
/// own error so the UI can show why.
#[derive(Debug, PartialEq, serde::Serialize)]
pub struct PatternError {
    pub line:    usize,
    pub pattern: String,
    pub message: String,
}

/// Validate exclusion patterns line by line against the real gitignore engine
/// — the same one [`IgnoreMatcher`] applies, so the editor never reports a
/// line as valid that would later be silently dropped. Blank lines and `#`
/// comments are skipped; every other line is compiled on its own and any
/// rejection is collected with its 1-based row number.
pub(crate) fn validate_patterns(text: &str) -> Vec<PatternError> {
    let mut errors = Vec::new();
    for (i, raw) in text.lines().enumerate() {
        let trimmed = raw.trim();
        if trimmed.is_empty() || trimmed.starts_with('#') {
            continue;
        }
        let mut b = GitignoreBuilder::new("");
        if let Err(e) = b.add_line(None, raw) {
            errors.push(PatternError {
                line:    i + 1,
                pattern: raw.to_string(),
                message: e.to_string(),
            });
        }
    }
    errors
}

/// How many files the current patterns exclude, plus a capped sample for the UI
///. Lets the user see what their patterns actually catch.
#[derive(Debug, PartialEq, serde::Serialize)]
pub struct ExcludedPreview {
    pub total:  usize,
    /// Up to `cap` matched files as `"<RootName>/rel/path"` for display.
    pub sample: Vec<String>,
}

/// Walk each `(root_name, base)` and count the files the matcher excludes,
/// collecting up to `cap` of them as a labelled sample. Mirrors the
/// reconcile scan's path form (leading-slash, '/'-separated) so the count
/// matches exactly what staging would skip.
pub(crate) fn preview_excluded(
    roots: &[(String, std::path::PathBuf)],
    matcher: &IgnoreMatcher,
    cap: usize,
) -> ExcludedPreview {
    let mut total = 0;
    let mut sample = Vec::new();
    for (name, base) in roots {
        for entry in walkdir::WalkDir::new(base)
            .into_iter()
            .filter_map(|e| e.ok())
            .filter(|e| e.file_type().is_file())
        {
            let Ok(rel) = super::paths::relative_path(base, entry.path()) else { continue };
            if matcher.is_ignored_db_path(&rel, false) {
                total += 1;
                if sample.len() < cap {
                    sample.push(format!("{name}{rel}"));
                }
            }
        }
    }
    ExcludedPreview { total, sample }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The default list is where "does this file sync?" is decided — the
    /// watcher's own filter only affects how soon. It names individual
    /// dotfiles rather than excluding them as a class, so the ones it does
    /// not name have to come through.
    #[test]
    fn default_patterns_exclude_named_dotfiles_but_not_dotfiles_at_large() {
        let m = IgnoreMatcher::from_patterns(crate::db::DEFAULT_IGNORE_PATTERNS);

        for excluded in [".DS_Store", "._resource", ".~lock.report.odt#", ".goutputstream-A1B2C3"] {
            assert!(m.is_ignored(excluded, false), "{excluded} must stay excluded");
        }
        for synced in [".gitignore", ".editorconfig", ".env"] {
            assert!(!m.is_ignored(synced, false), "{synced} must sync like any other file");
        }
    }

    /// An Access/Jet lock file lives exactly as long as its database is open,
    /// so the watcher reports it and the file is gone again before it could
    /// be staged. The database itself must still sync.
    #[test]
    fn default_patterns_exclude_access_lock_files_but_not_the_database() {
        let m = IgnoreMatcher::from_patterns(crate::db::DEFAULT_IGNORE_PATTERNS);

        for excluded in ["27leg0101.ldb", "27leg0101~hdb.ldb", "Kunden.laccdb", "27leg0101.kil"] {
            assert!(m.is_ignored(excluded, false), "{excluded} must be excluded");
        }
        for synced in ["Kunden.mdb", "Kunden.accdb", "27leg0101.fdb"] {
            assert!(!m.is_ignored(synced, false), "{synced} must sync");
        }
    }

    /// Tracer: a plain glob excludes the matching file and leaves others alone.
    #[test]
    fn simple_glob_excludes_matching_file_only() {
        let m = IgnoreMatcher::from_patterns("*.tmp");
        assert!(m.is_ignored("scratch.tmp", false), "*.tmp must match scratch.tmp");
        assert!(!m.is_ignored("doc.txt", false), "doc.txt must not be ignored");
    }

    /// A directory pattern (`node_modules/`) excludes everything beneath it —
    /// a file deep inside the directory is matched via its ignored parent.
    #[test]
    fn directory_pattern_excludes_nested_files() {
        let m = IgnoreMatcher::from_patterns("node_modules/");
        assert!(m.is_ignored("node_modules", true), "the dir itself is ignored");
        assert!(m.is_ignored("node_modules/pkg/lib.js", false), "nested file is ignored");
        assert!(!m.is_ignored("src/lib.js", false), "unrelated file is kept");
    }

    /// A later negation (`!keep.log`) re-includes a file that an earlier
    /// pattern (`*.log`) excluded — order matters, exactly like .gitignore.
    #[test]
    fn negation_reincludes_excluded_file() {
        let m = IgnoreMatcher::from_patterns("*.log\n!keep.log");
        assert!(m.is_ignored("debug.log", false), "*.log still excludes debug.log");
        assert!(!m.is_ignored("keep.log", false), "!keep.log re-includes keep.log");
    }

    /// Empty input plus blank lines and `#` comments exclude nothing — the
    /// common case of a root with no configured patterns must sync everything.
    #[test]
    fn blank_and_comment_lines_exclude_nothing() {
        let m = IgnoreMatcher::from_patterns("# just a comment\n\n   \n");
        assert!(!m.is_ignored("anything.txt", false));
        let empty = IgnoreMatcher::from_patterns("");
        assert!(!empty.is_ignored("anything.txt", false));
    }

    /// The DB-path seam strips the leading anchor slash before matching, so the
    /// leading-slash form stored in `local_files` (and produced by the watcher
    /// via `relative_path`) is excluded identically to the bare form.
    #[test]
    fn db_path_form_strips_leading_slash() {
        let m = IgnoreMatcher::from_patterns("*.bak\nbuild/");
        assert!(m.is_ignored_db_path("/scratch.bak", false), "leading-slash file matches");
        assert!(m.is_ignored_db_path("/build/out.o", false), "file under ignored dir matches");
        assert!(!m.is_ignored_db_path("/src/main.rs", false), "unrelated path kept");
    }

    /// preview_excluded counts every matched file across roots (incl. nested)
    /// and labels the sample with the root name, while non-matches are left out.
    #[test]
    fn preview_excluded_counts_and_samples_matches() {
        use std::fs;
        let dir = tempfile::tempdir().unwrap();
        let base = dir.path();
        fs::write(base.join("a.bak"), b"x").unwrap();
        fs::write(base.join("keep.txt"), b"x").unwrap();
        fs::create_dir(base.join("sub")).unwrap();
        fs::write(base.join("sub/c.bak"), b"x").unwrap();

        let m = IgnoreMatcher::from_patterns("*.bak");
        let roots = vec![("Docs".to_string(), base.to_path_buf())];
        let prev = preview_excluded(&roots, &m, 10);

        assert_eq!(prev.total, 2, "two .bak files excluded, got {prev:?}");
        assert!(prev.sample.iter().any(|s| s == "Docs/a.bak"));
        assert!(prev.sample.iter().any(|s| s == "Docs/sub/c.bak"));
        assert!(!prev.sample.iter().any(|s| s.contains("keep.txt")));
    }

    /// A well-formed list (globs, comment, blank, negation) validates clean.
    #[test]
    fn validate_accepts_wellformed_patterns() {
        let errs = validate_patterns("*.tmp\n# a comment\n\nbuild/\n!keep.log");
        assert!(errs.is_empty(), "expected no errors, got {errs:?}");
    }

    /// A malformed glob (unclosed `{` alternate group) is reported with its
    /// 1-based line number, while the surrounding valid lines are not.
    #[test]
    fn validate_flags_malformed_pattern_with_line_number() {
        let errs = validate_patterns("*.tmp\na{b\nbuild/");
        assert_eq!(errs.len(), 1, "exactly one bad line, got {errs:?}");
        assert_eq!(errs[0].line, 2);
        assert_eq!(errs[0].pattern, "a{b");
        assert!(!errs[0].message.is_empty());
    }

    /// Gate for the walk's subtree skip: only a negation can rescue something
    /// inside an ignored directory, so its absence makes descending pointless.
    #[test]
    fn negations_are_detected() {
        assert!(!IgnoreMatcher::from_patterns("build/\n*.tmp").has_negations());
        assert!(IgnoreMatcher::from_patterns("*.log\n!important.log").has_negations());
        assert!(!IgnoreMatcher::from_patterns(crate::db::DEFAULT_IGNORE_PATTERNS).has_negations(),
            "the shipped defaults have none — a fresh client gets the cheap path");
    }
}
