use std::path::PathBuf;
use anyhow::Result;
use notify::{Event, EventKind, RecursiveMode, Watcher, recommended_watcher};
use notify::event::{ModifyKind, RenameMode};
use tokio::sync::mpsc;
use tracing::{debug, warn};

/// Drains events off `rx` until `predicate` accepts one, or `timeout`
/// elapses. Bounded, non-blind waiting for an async OS callback whose
/// delivery latency isn't under the test's control -- the alternative,
/// waiting a fixed sleep before reading, would either flake under load or
/// waste time in the common case.
#[cfg(test)]
async fn recv_until(
    rx:       &mut mpsc::Receiver<FileEvent>,
    timeout:  std::time::Duration,
    mut predicate: impl FnMut(&FileEvent) -> bool,
) -> Option<FileEvent> {
    let deadline = tokio::time::Instant::now() + timeout;
    loop {
        let remaining = deadline.saturating_duration_since(tokio::time::Instant::now());
        if remaining.is_zero() {
            return None;
        }
        match tokio::time::timeout(remaining, rx.recv()).await {
            Ok(Some(ev)) if predicate(&ev) => return Some(ev),
            Ok(Some(_)) => continue, // unrelated noise (e.g. the setup write's own event)
            Ok(None) | Err(_) => return None,
        }
    }
}

/// Ereignis vom File Watcher an die Sync Engine.
#[derive(Debug, Clone)]
pub enum FileEvent {
    Modified(PathBuf),
    Deleted(PathBuf),
    Renamed { from: PathBuf, to: PathBuf },
    /// A directory was created. Only meaningful for a currently-empty
    /// directory — one with content is synced implicitly via its files'
    /// own Modified events, mirroring reconcile_local's dir-pass (ADR-0037).
    DirCreated(PathBuf),
}

/// Starts an OS-native file watcher and returns an async receiver.
pub fn start_watcher(paths: Vec<PathBuf>) -> Result<mpsc::Receiver<FileEvent>> {
    let (tx, rx) = mpsc::channel::<FileEvent>(256);

    let tx_clone = tx.clone();
    let mut watcher = recommended_watcher(move |res: notify::Result<Event>| {
        match res {
            Ok(event) => {
                if let Some(fe) = translate_event(event) {
                    if !is_temp_file(&fe) {
                        let _ = tx_clone.blocking_send(fe);
                    }
                }
            }
            Err(e) => warn!("watcher error: {e}"),
        }
    })?;

    for path in &paths {
        if path.exists() {
            watcher.watch(path, RecursiveMode::Recursive)?;
            debug!("watching: {}", path.display());
        }
    }

    // Keep the watcher alive in a thread so it isn't dropped.
    std::thread::spawn(move || {
        let _watcher = watcher;
        // Block forever — the watcher lives as long as this thread does.
        // Closing the tx channel allows the thread to exit.
        loop {
            std::thread::park();
        }
    });

    Ok(rx)
}

fn translate_event(event: Event) -> Option<FileEvent> {
    match event.kind {
        EventKind::Create(_) => {
            let path = event.paths.first()?;
            if path.is_dir() {
                Some(FileEvent::DirCreated(path.clone()))
            } else if path.is_file() {
                Some(FileEvent::Modified(path.clone()))
            } else {
                None // gone again before we could stat it, or a non-regular entry
            }
        }
        // `Any` belongs here, not in the catch-all: ReadDirectoryChangesW
        // reports one `FILE_ACTION_MODIFIED` for everything — data, size,
        // attributes, timestamps alike — and notify's Windows backend can
        // only map that to `ModifyKind::Any`. Matching `Data` alone dropped
        // every content change on Windows, leaving a copied-in file staged
        // from its `FILE_ACTION_ADDED` event, when it was still zero bytes,
        // and nothing to correct it until the next full walk.
        //
        // The cost of treating a metadata-only change as a content change is
        // one `stat` — `stage_upload`'s size/mtime fast path returns before
        // hashing when nothing actually moved.
        EventKind::Modify(ModifyKind::Data(_) | ModifyKind::Any) => {
            event.paths.first()
                .filter(|p| p.is_file())
                .map(|p| FileEvent::Modified(p.clone()))
        }
        EventKind::Modify(ModifyKind::Name(RenameMode::Both)) if event.paths.len() == 2 => {
            Some(FileEvent::Renamed {
                from: event.paths[0].clone(),
                to:   event.paths[1].clone(),
            })
        }
        EventKind::Modify(ModifyKind::Name(RenameMode::From)) => {
            event.paths.first().map(|p| FileEvent::Deleted(p.clone()))
        }
        EventKind::Modify(ModifyKind::Name(RenameMode::To)) => {
            let path = event.paths.first()?;
            if path.is_dir() {
                Some(FileEvent::DirCreated(path.clone()))
            } else if path.is_file() {
                Some(FileEvent::Modified(path.clone()))
            } else {
                None
            }
        }
        EventKind::Remove(_) => {
            event.paths.first().map(|p| FileEvent::Deleted(p.clone()))
        }
        _ => None,
    }
}

/// Transient names no sync should ever chase: half-written downloads, editor
/// scratch files, and our own conflict copies.
///
/// This filter decides *latency*, not policy. What actually syncs is decided
/// by the exclusion patterns, which the full walk applies (`sync::ignore`) —
/// so anything dropped here that the walk would still pick up does not stay
/// unsynced, it just waits for the next walk. That is why a blanket
/// "starts with a dot" rule had to go: the walk syncs `.gitignore` and
/// `.editorconfig` like any other file, and the default exclusion list names
/// individual dotfiles (`.DS_Store`, `.Spotlight-V100`, …) precisely because
/// dotfiles are not excluded as a class. Dropping them here only delayed them
/// — harmless while the walk ran every 30s, up to 20 minutes since ADR-0037
/// made it rare.
fn is_temp_file(event: &FileEvent) -> bool {
    let path = match event {
        FileEvent::Modified(p) | FileEvent::Deleted(p) | FileEvent::DirCreated(p) => p,
        FileEvent::Renamed { to, .. } => to,
    };
    let name = path.file_name().and_then(|n| n.to_str()).unwrap_or("");
    name.ends_with(".tmp")
        || name.ends_with('~')
        || name.ends_with(".part")
        || name.ends_with(".crdownload")
        || name.contains(".conflict.")
}

#[cfg(test)]
mod tests {
    use super::*;
    use notify::event::CreateKind;

    struct TempDir(PathBuf);
    impl TempDir {
        fn new() -> Self {
            let p = std::env::temp_dir()
                .join(format!("priva-sync-watcher-test-{}", uuid::Uuid::new_v4()));
            std::fs::create_dir_all(&p).unwrap();
            Self(p)
        }
    }
    impl Drop for TempDir {
        fn drop(&mut self) { let _ = std::fs::remove_dir_all(&self.0); }
    }

    // The gap this closes (ADR-0037): a Create event for a directory used to
    // be filtered out entirely (`.filter(|p| p.is_file())`), so an empty
    // folder was invisible to the live watcher and only picked up by the
    // periodic full walk -- fine at a 30s cadence, a real latency regression
    // once that walk runs only rarely (ADR-0037 trigger-gating).
    #[test]
    fn create_event_for_a_directory_becomes_dir_created() {
        let tmp = TempDir::new();
        let dir = tmp.0.join("sub");
        std::fs::create_dir_all(&dir).unwrap();

        let event = Event::new(EventKind::Create(CreateKind::Folder)).add_path(dir.clone());

        assert!(matches!(translate_event(event), Some(FileEvent::DirCreated(p)) if p == dir));
    }

    #[test]
    fn create_event_for_a_file_stays_modified() {
        let tmp = TempDir::new();
        let file = tmp.0.join("a.txt");
        std::fs::write(&file, b"x").unwrap();

        let event = Event::new(EventKind::Create(CreateKind::File)).add_path(file.clone());

        assert!(matches!(translate_event(event), Some(FileEvent::Modified(p)) if p == file));
    }

    /// The path can vanish between the OS raising the event and us stat-ing
    /// it (e.g. a create immediately followed by a delete) -- neither
    /// `is_dir()` nor `is_file()` are true then, and the event is dropped
    /// rather than guessed at.
    #[test]
    fn create_event_for_a_path_gone_before_stat_is_dropped() {
        let tmp = TempDir::new();
        let gone = tmp.0.join("never-existed.txt");

        let event = Event::new(EventKind::Create(CreateKind::File)).add_path(gone);

        assert!(translate_event(event).is_none());
    }

    /// The shape ReadDirectoryChangesW actually produces. Its
    /// `FILE_ACTION_MODIFIED` carries no data/metadata distinction, so
    /// notify's Windows backend can only report `ModifyKind::Any` — matching
    /// `Data` alone meant every write to an existing file was dropped, and a
    /// file copied in stayed staged at the zero bytes its create event saw.
    #[test]
    fn windows_shaped_modify_any_is_treated_as_a_content_change() {
        let tmp  = TempDir::new();
        let file = tmp.0.join("a.txt");
        std::fs::write(&file, b"x").unwrap();

        let event = Event::new(EventKind::Modify(ModifyKind::Any)).add_path(file.clone());

        assert!(matches!(translate_event(event), Some(FileEvent::Modified(p)) if p == file));
    }

    /// A metadata-only change arrives as the same `Any` on Windows and cannot
    /// be told apart here — it is forwarded, and `stage_upload`'s size/mtime
    /// fast path is what keeps it from costing a hash.
    #[test]
    fn modify_any_for_a_path_that_is_not_a_file_is_dropped() {
        let tmp = TempDir::new();
        let dir = tmp.0.join("sub");
        std::fs::create_dir_all(&dir).unwrap();

        let event = Event::new(EventKind::Modify(ModifyKind::Any)).add_path(dir);

        assert!(translate_event(event).is_none());
    }

    /// A folder rename's `To` half used to be dropped outright
    /// (`.filter(|p| p.is_file())`), so the renamed folder's new name never
    /// reached the engine at all -- the old name became a subtree deletion
    /// and the new one was invisible until the next full walk.
    #[test]
    fn rename_to_event_for_a_directory_becomes_dir_created() {
        let tmp = TempDir::new();
        let dir = tmp.0.join("renamed-sub");
        std::fs::create_dir_all(&dir).unwrap();

        let event = Event::new(EventKind::Modify(ModifyKind::Name(RenameMode::To))).add_path(dir.clone());

        assert!(matches!(translate_event(event), Some(FileEvent::DirCreated(p)) if p == dir));
    }

    #[test]
    fn rename_to_event_for_a_file_stays_modified() {
        let tmp = TempDir::new();
        let file = tmp.0.join("renamed.txt");
        std::fs::write(&file, b"x").unwrap();

        let event = Event::new(EventKind::Modify(ModifyKind::Name(RenameMode::To))).add_path(file.clone());

        assert!(matches!(translate_event(event), Some(FileEvent::Modified(p)) if p == file));
    }

    /// Empirical, not synthetic: the real `notify` backend on this host, a
    /// real `fs::rename`, no constructed `Event`. This is the fact ADR-0063
    /// rests on -- verified once by hand for the issue, now pinned so a
    /// `notify` upgrade or a platform change can't silently invalidate the
    /// whole design without a red test.
    // inotify (Linux) is the one backend that does pair From/To into a
    // single Renamed event -- this test characterises the platforms ADR-0063
    // actually needed to fix and would assert the wrong thing there.
    #[cfg(not(target_os = "linux"))]
    #[tokio::test]
    async fn a_real_file_rename_on_this_host_arrives_as_two_unpaired_events() {
        let tmp  = TempDir::new();
        let from = tmp.0.join("a.txt");

        let mut rx = start_watcher(vec![tmp.0.clone()]).expect("watcher must start");
        let timeout = std::time::Duration::from_secs(10);

        // The write happens after the watcher is attached, so it raises its
        // own event -- drain it here so it can't be mistaken for one of the
        // rename's two events below.
        std::fs::write(&from, b"x").unwrap();
        let setup_seen = recv_until(&mut rx, timeout, |ev| matches!(ev, FileEvent::Modified(p) if p == &from)).await;
        assert!(setup_seen.is_some(), "sanity check: the watcher must see the setup write at all");

        let to = tmp.0.join("b.txt");
        std::fs::rename(&from, &to).unwrap();

        // On inotify (Linux) this host would instead report one paired
        // FileEvent::Renamed -- see ADR-0063's platform note. This test only
        // runs where notify never pairs the two: Windows and macOS.
        let deleted  = recv_until(&mut rx, timeout, |ev| matches!(ev, FileEvent::Deleted(p) if p == &from)).await;
        let appeared = recv_until(&mut rx, timeout, |ev|
            matches!(ev, FileEvent::Modified(p) | FileEvent::DirCreated(p) if p == &to)
        ).await;

        assert!(deleted.is_some(),  "the old path must be reported gone");
        assert!(appeared.is_some(), "the new path must be reported, unpaired with the old one");
    }

    /// The folder half of the same empirical check: real notify events for a
    /// real directory rename, confirming the `To`-side fix (RenameMode::To on
    /// a directory becomes DirCreated, not a dropped/filtered event) against
    /// what this host's watcher backend actually reports.
    #[cfg(not(target_os = "linux"))]
    #[tokio::test]
    async fn a_real_directory_rename_on_this_host_arrives_as_two_unpaired_events() {
        let tmp  = TempDir::new();
        let from = tmp.0.join("old-sub");

        let mut rx = start_watcher(vec![tmp.0.clone()]).expect("watcher must start");
        let timeout = std::time::Duration::from_secs(10);

        std::fs::create_dir_all(&from).unwrap();
        let setup_seen = recv_until(&mut rx, timeout, |ev| matches!(ev, FileEvent::DirCreated(p) if p == &from)).await;
        assert!(setup_seen.is_some(), "sanity check: the watcher must see the setup mkdir at all");

        let to = tmp.0.join("new-sub");
        std::fs::rename(&from, &to).unwrap();

        let deleted  = recv_until(&mut rx, timeout, |ev| matches!(ev, FileEvent::Deleted(p) if p == &from)).await;
        let appeared = recv_until(&mut rx, timeout, |ev| matches!(ev, FileEvent::DirCreated(p) if p == &to)).await;

        assert!(deleted.is_some(),  "the old directory path must be reported gone");
        assert!(appeared.is_some(), "the new directory path must arrive as DirCreated, unpaired with the old one");
    }

    #[test]
    fn is_temp_file_checks_the_dir_created_path_too() {
        assert!(is_temp_file(&FileEvent::DirCreated(PathBuf::from("/root/scratch.tmp"))));
        assert!(!is_temp_file(&FileEvent::DirCreated(PathBuf::from("/root/visible"))));
    }

    /// The watcher used to drop every name starting with a dot, while the
    /// full walk synced those files normally — so a `.gitignore` only
    /// reached the server on the next walk, up to 20 minutes later since
    /// ADR-0037 made that walk rare. What syncs is the exclusion list's call,
    /// not this filter's; the default list names individual dotfiles exactly
    /// because dotfiles are not excluded as a class.
    #[test]
    fn dotfiles_are_not_treated_as_temporary() {
        for name in [".gitignore", ".editorconfig", ".env", ".hidden"] {
            let p = PathBuf::from("/root").join(name);
            assert!(!is_temp_file(&FileEvent::Modified(p.clone())), "{name} must reach the engine");
            assert!(!is_temp_file(&FileEvent::Deleted(p)), "{name} deletion must reach the engine");
        }
    }

    /// Dropping the dot rule must not let the genuinely transient names
    /// through — those are still filtered on their suffix.
    #[test]
    fn transient_suffixes_are_still_filtered() {
        for name in ["a.tmp", "a.part", "a.crdownload", "a~", "notes.conflict.20260101_120000.txt"] {
            assert!(
                is_temp_file(&FileEvent::Modified(PathBuf::from("/root").join(name))),
                "{name} must stay filtered",
            );
        }
    }
}
