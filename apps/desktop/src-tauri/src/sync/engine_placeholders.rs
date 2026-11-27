use std::path::{Path, PathBuf};
use anyhow::Result;
use tracing::{info, warn};

use crate::cloud_files::{self, ConvertOutcome};
use crate::db::SyncRootEntry;

use super::SyncEngine;

/// Collector behind [`SyncEngine::dirty_dirs`]: sync-root id → the relative
/// directory paths whose Explorer status that root's current cycle invalidated.
pub(super) type DirtyDirs =
    std::sync::Mutex<std::collections::HashMap<String, std::collections::HashSet<String>>>;

/// The directories a change to `rel` invalidates, deepest first: its ancestor
/// chain up to — but not including — the sync root, plus `rel` itself when it
/// is a directory. `rel` is sync-root-relative with a leading slash.
fn dirty_dir_chain(rel: &str, is_dir: bool) -> Vec<&str> {
    let mut cur = rel.trim_end_matches('/');
    if !is_dir {
        let Some(cut) = cur.rfind('/') else { return Vec::new() };
        cur = &cur[..cut];
    }
    let mut chain = Vec::new();
    while !cur.is_empty() {
        chain.push(cur);
        let Some(cut) = cur.rfind('/') else { break };
        cur = &cur[..cut];
    }
    chain
}

/// Free-standing twin of [`SyncEngine::mark_dirs_dirty`] for the spawned
/// upload and hydration tasks, which hold `Arc` clones rather than `&self`.
pub(super) fn record_dirty_dirs(dirty: &DirtyDirs, root_id: &str, rel: &str, is_dir: bool) {
    // Directory status only exists where the Cloud Files API does; elsewhere
    // this would just grow a map nobody reads.
    if !cloud_files::supports_placeholders() {
        return;
    }
    let Ok(mut map) = dirty.lock() else { return };
    let set = map.entry(root_id.to_string()).or_default();
    for dir in dirty_dir_chain(rel, is_dir) {
        // A path already in the set was inserted together with its ancestors,
        // so there is nothing left to add above it.
        if !set.insert(dir.to_string()) {
            break;
        }
    }
}

impl SyncEngine {
    /// Records every directory whose Explorer status the touched entry `rel`
    /// invalidated — see [`SyncEngine::dirty_dirs`]. `rel` is sync-root-
    /// relative with a leading slash; pass `is_dir` when it is itself a
    /// directory, which then dirties itself on top of its ancestors.
    pub(super) fn mark_dirs_dirty(&self, root_id: &str, rel: &str, is_dir: bool) {
        record_dirty_dirs(&self.dirty_dirs, root_id, rel, is_dir);
    }

    /// Restore the Explorer status of every directory this cycle touched, then
    /// of the sync root itself (ADR-0052).
    ///
    /// The CF filter clears a directory's in-sync bit whenever a descendant
    /// changes, so this must be (re)applied at the end of every settled cycle
    /// — not once at connect time, where the placeholder-init and transfer
    /// passes would immediately wipe it. Only the provider process (this
    /// daemon holds the CfConnectSyncRoot handle) may set root in-sync state.
    ///
    /// Subdirectories used to be left out, which is why a folder stayed on
    /// "Synchronisierung ausstehend" forever once its contents had been
    /// downloaded: the initial pass converted it while it was still empty,
    /// the download then cleared the bit, and nothing set it again. Folders
    /// created by the download itself were never placeholders at all, hence
    /// the conversion below rather than a bare `mark_in_sync`.
    pub(super) fn settle_directory_status(&self, root: &SyncRootEntry) {
        // Taken before the placeholder check, not after: on a root that cannot
        // carry placeholders the collector still fills up every cycle
        // (`record_dirty_dirs` only knows the root id, not its path), and
        // returning early without draining it would grow that set for the
        // lifetime of the process.
        let dirty = self.dirty_dirs.lock().ok()
            .and_then(|mut map| map.remove(&root.id))
            .unwrap_or_default();

        if !cloud_files::placeholders_available_for(Path::new(&root.local_path)) {
            return;
        }

        // Deepest first: marking a parent before its child would be undone
        // again, because setting the child's bit clears the parent's.
        let mut dirs: Vec<String> = dirty.into_iter().collect();
        dirs.sort_by_key(|d| std::cmp::Reverse(d.matches('/').count()));

        let base = Path::new(&root.local_path);
        for rel in &dirs {
            let abs = base.join(rel.trim_start_matches('/').replace('/', "\\"));
            // The directory may have been removed again in the same cycle.
            if !abs.is_dir() {
                continue;
            }
            match cloud_files::convert_to_placeholder(&abs, rel, true) {
                // Conversion carries MARK_IN_SYNC, so a fresh placeholder is
                // already done. An existing one is left untouched by the call
                // and needs the flag set explicitly.
                ConvertOutcome::Succeeded => {}
                ConvertOutcome::AlreadyPlaceholder => {
                    if let Err(e) = cloud_files::mark_in_sync(&abs) {
                        warn!("mark_in_sync(dir '{rel}') failed for '{}': {e:#}", root.name);
                    }
                }
                ConvertOutcome::NotUnderSyncRoot => {}
                ConvertOutcome::Failed(e) => {
                    warn!("settle_directory_status['{rel}'] for '{}': {e:#}", root.name);
                }
            }
        }

        if let Err(e) = cloud_files::mark_in_sync(base) {
            warn!("mark_in_sync(sync root) failed for '{}': {e:#}", root.name);
        }
    }

    /// When `download_mode` has been switched to `eager`, hydrate every
    /// dehydrated placeholder in the root.  Called on every cycle so it also
    /// catches files that failed on a previous attempt.
    ///
    /// Driven off the DB in keyset pages rather than a `walkdir` of the whole
    /// root: the old form collected every dehydrated path into one `Vec` and
    /// ran the traversal plus a `GetFileAttributesW` per file inline in the
    /// engine loop — an unbounded allocation and a frozen loop on a
    /// million-file root, the same defect the placeholder pass had. A page is
    /// filtered on a blocking thread and hydrated before the next one is read,
    /// so memory is one page and the loop breathes between them.
    ///
    /// Two sweeps: the first counts what has to be hydrated, the second does
    /// it. The count is what the UI's `x/y` progress needs, and there is no
    /// cheaper source for it — hydration state lives in the filesystem, not in
    /// the DB. Both sweeps are page-wise, and the counting one costs a stat per
    /// tracked file, which is nothing against the downloads that follow.
    ///
    /// Idempotent and interruption-safe: nothing is written that would have to
    /// be undone, and a killed run simply finds the still-dehydrated files
    /// again on the next cycle.
    pub(super) async fn hydrate_pending_placeholders(&self, root: &SyncRootEntry) -> Result<()> {
        if !cloud_files::placeholders_available_for(Path::new(&root.local_path))
            || root.download_mode != "eager"
        {
            return Ok(());
        }

        let base = PathBuf::from(&root.local_path);

        let total = self.count_dehydrated(root, &base).await?;
        if total == 0 {
            return Ok(());
        }
        info!("hydrating {total} dehydrated placeholder(s) for '{}'", root.name);

        let mut cursor = String::new();
        let mut done   = 0usize;

        while let Some((page, next)) = self.next_dehydrated_page(root, &base, &cursor).await? {
            cursor = next;
            for rel in page {
                let path = base.join(rel.trim_start_matches('/'));
                done += 1;
                // The count came from a sweep that ran before this one; a file
                // dehydrated in between would push `done` past it.
                let total = total.max(done);

                self.emit_progress(&root.id, done, total, &rel, "download");

                let lp = path.clone();
                match tokio::task::spawn_blocking(move || cloud_files::hydrate_file(&lp))
                    .await
                    .map_err(|e| anyhow::anyhow!("spawn_blocking: {e}"))?
                {
                    Ok(()) => {
                        let _ = cloud_files::mark_in_sync(&path);
                        // Hydration flips the file's own bit; its ancestors lose
                        // theirs and are restored by `settle_directory_status`.
                        self.mark_dirs_dirty(&root.id, &rel, false);
                        info!("hydrated placeholder: {rel}");
                    }
                    Err(e) => warn!("hydration failed for '{rel}': {e}"),
                }

                // Per-file Done — without it every hydrated file keeps its slot in
                // the UI's progress map and the label stays pinned to the first
                // one for the whole sweep.
                self.emit_progress(&root.id, done, total, &rel, "file_done");
            }
        }

        self.emit_progress_done(&root.id);
        Ok(())
    }

    /// How many of the root's tracked files are dehydrated right now, counted
    /// page by page so nothing but the page itself is ever held.
    async fn count_dehydrated(&self, root: &SyncRootEntry, base: &Path) -> Result<usize> {
        let mut cursor = String::new();
        let mut total  = 0usize;
        while let Some((page, next)) = self.next_dehydrated_page(root, base, &cursor).await? {
            total += page.len();
            cursor = next;
        }
        Ok(total)
    }

    /// The next page of *dehydrated* tracked files after `cursor`, plus the
    /// cursor to continue from. `None` once the root is exhausted.
    ///
    /// The page is read from the DB and filtered on a blocking thread: the
    /// filter is one `GetFileAttributesW` per row, and a page's worth of those
    /// has no business running on an executor thread.
    async fn next_dehydrated_page(
        &self,
        root:   &SyncRootEntry,
        base:   &Path,
        cursor: &str,
    ) -> Result<Option<(Vec<String>, String)>> {
        let db      = self.db.clone();
        let root_id = root.id.clone();
        let base    = base.to_path_buf();
        let cursor  = cursor.to_string();

        tokio::task::spawn_blocking(move || -> Result<Option<(Vec<String>, String)>> {
            let mut cursor = cursor;
            loop {
                let paths = db.list_file_paths_after(&root_id, &cursor, HYDRATE_PAGE_SIZE)?;
                let Some(last) = paths.last().cloned() else { return Ok(None) };
                let dehydrated: Vec<String> = paths.into_iter()
                    .filter(|rel| {
                        cloud_files::is_dehydrated(&base.join(rel.trim_start_matches('/')))
                    })
                    .collect();
                cursor = last;
                // A page where nothing is dehydrated is not the end of the
                // root — keep reading rather than handing the caller an empty
                // page it would have to interpret.
                if !dehydrated.is_empty() {
                    return Ok(Some((dehydrated, cursor)));
                }
            }
        })
        .await
        .map_err(|e| anyhow::anyhow!("spawn_blocking: {e}"))?
    }
}

/// How many tracked files one hydration page covers. Bounds both the DB read
/// and the `is_dehydrated` stat burst that filters it; smaller than the
/// placeholder chunk because each surviving entry turns into a full download.
const HYDRATE_PAGE_SIZE: usize = 1_000;

#[cfg(test)]
mod tests {
    use super::dirty_dir_chain;

    #[test]
    fn file_dirties_every_ancestor_deepest_first() {
        assert_eq!(
            dirty_dir_chain("/docs/2026/q3/report.pdf", false),
            vec!["/docs/2026/q3", "/docs/2026", "/docs"],
        );
    }

    #[test]
    fn file_directly_in_the_sync_root_dirties_nothing() {
        // The root has its own marker and is never part of the chain.
        assert!(dirty_dir_chain("/report.pdf", false).is_empty());
    }

    #[test]
    fn directory_entry_dirties_itself_too() {
        assert_eq!(dirty_dir_chain("/docs/2026", true), vec!["/docs/2026", "/docs"]);
    }

    #[test]
    fn trailing_slash_does_not_produce_an_empty_segment() {
        assert_eq!(dirty_dir_chain("/docs/2026/", true), vec!["/docs/2026", "/docs"]);
    }


}
