//! Placeholder creation, conversion of existing files, and eager
//! hydration. The `ConvertOutcome` enum lives in the parent module so
//! both Windows + stub backends use the same shape.

use std::ffi::c_void;
use std::path::Path;

use anyhow::{Context, Result};

use windows::core::{HRESULT, PCWSTR};
use windows::Win32::Foundation::{
    CloseHandle, ERROR_ACCESS_DENIED, ERROR_ALREADY_EXISTS, ERROR_CLOUD_FILE_PINNED, ERROR_FILE_EXISTS,
    ERROR_MORE_DATA, ERROR_NOT_A_CLOUD_FILE, HANDLE,
};
use windows::Win32::Storage::CloudFilters::{
    CfCloseHandle, CfConvertToPlaceholder, CfCreatePlaceholders,
    CfDehydratePlaceholder, CfGetPlaceholderInfo,
    CfHydratePlaceholder, CfOpenFileWithOplock,
    CfUpdatePlaceholder,
    CF_PLACEHOLDER_BASIC_INFO, CF_PLACEHOLDER_INFO_BASIC,
    CF_DEHYDRATE_FLAG_NONE,
    CF_FS_METADATA,
    CF_HYDRATE_FLAG_NONE,
    CF_OPEN_FILE_FLAG_EXCLUSIVE, CF_OPEN_FILE_FLAG_NONE,
    CF_CREATE_FLAG_NONE,
    CF_PLACEHOLDER_CREATE_FLAG_MARK_IN_SYNC,
    CF_PLACEHOLDER_CREATE_INFO,
    CF_CONVERT_FLAG_MARK_IN_SYNC, CF_CONVERT_FLAG_NONE,
    CF_UPDATE_FLAG_DEHYDRATE, CF_UPDATE_FLAG_MARK_IN_SYNC,
};
use windows::Win32::Storage::FileSystem::{
    CreateFileW, GetFileAttributesW, SetFileAttributesW,
    FILE_ATTRIBUTE_DIRECTORY, FILE_ATTRIBUTE_NORMAL, FILE_ATTRIBUTE_READONLY,
    FILE_BASIC_INFO, FILE_FLAGS_AND_ATTRIBUTES, FILE_FLAG_BACKUP_SEMANTICS,
    FILE_SHARE_DELETE, FILE_SHARE_READ, FILE_SHARE_WRITE, INVALID_FILE_ATTRIBUTES,
    OPEN_EXISTING, FILE_READ_ATTRIBUTES, FILE_WRITE_ATTRIBUTES, FILE_WRITE_DATA, DELETE,
};
use windows::core::HSTRING;

use super::{ConvertOutcome, PlaceholderInfo, PlaceholderOutcome, to_hstring, unix_to_filetime};

/// Make the entry at `info.relative_path` reflect the server state described
/// by `info`: create a dehydrated CF placeholder when nothing is there, and
/// re-point an existing entry at the new state when something is.
///
/// The second half is the part that is easy to get wrong. `CfCreatePlaceholders`
/// answers `ERROR_ALREADY_EXISTS` for *any* existing entry, and treating that
/// as success is only correct while the entry still describes the same server
/// version. For an update it is not: the placeholder keeps the old size, mtime
/// and identity, and a hydrated file keeps its old bytes outright — the caller
/// would then record the new hash and version for content that is not on disk.
/// So an existing entry goes through `CfUpdatePlaceholder` instead, which
/// re-points the metadata and drops the local bytes in one step.
///
/// Fails loudly on both failure surfaces of `CfCreatePlaceholders`: the
/// call's HRESULT *and* the per-entry `Result` field (see `classify_create`). The caller turns an `Err` into a `failed_server_changes` row, so a
/// transient refusal — most commonly `0x8007017C` while the CF filter driver
/// is still attaching to a freshly registered sync root — is retried with
/// backoff instead of being lost.
pub fn ensure_placeholder(
    base_path: &Path,
    info: &PlaceholderInfo<'_>,
) -> Result<PlaceholderOutcome> {
    let rel       = info.relative_path.trim_start_matches('/');
    let (parent_rel, file_name) = rel.rsplit_once('/').unwrap_or(("", rel));

    let parent_path = if parent_rel.is_empty() {
        base_path.to_path_buf()
    } else {
        base_path.join(parent_rel.replace('/', std::path::MAIN_SEPARATOR_STR))
    };
    std::fs::create_dir_all(&parent_path)?;

    let parent_w  = to_hstring(&parent_path)?;
    let name_w    = HSTRING::from(file_name);
    let identity  = info.relative_path.as_bytes();
    let mtime_ft  = unix_to_filetime(info.mtime);
    let metadata  = fs_metadata(info.size, mtime_ft);

    let mut placeholder = CF_PLACEHOLDER_CREATE_INFO {
        RelativeFileName: PCWSTR(name_w.as_ptr()),
        FsMetadata: metadata,
        FileIdentity:       identity.as_ptr() as *const c_void,
        FileIdentityLength: identity.len() as u32,
        // MARK_IN_SYNC: placeholder is created as already-synced so the
        // File Explorer Status column shows the cloud-checkmark immediately
        // rather than a sync-pending icon.
        Flags:              CF_PLACEHOLDER_CREATE_FLAG_MARK_IN_SYNC,
        Result:             windows::core::HRESULT(0),
        CreateUsn:          0,
    };

    let outer = unsafe {
        CfCreatePlaceholders(
            PCWSTR(parent_w.as_ptr()),
            std::slice::from_mut(&mut placeholder),
            CF_CREATE_FLAG_NONE,
            None,
        )
    };
    let outer_hr = outer.as_ref().err().map_or(HRESULT(0), |e| e.code());

    match classify_create(outer_hr, placeholder.Result) {
        CreateVerdict::Created => Ok(PlaceholderOutcome::Created),
        // Something is already at this path. It may describe the same server
        // version (a harmless re-run) or an older one (a real update) — we
        // cannot tell from here, and the update is idempotent, so just do it.
        CreateVerdict::AlreadyExists => {
            let full_path = parent_path.join(file_name);
            match update_placeholder(&full_path, info.relative_path, &metadata) {
                Ok(()) => Ok(PlaceholderOutcome::Updated),
                // Pinned file or plain file: legitimate states, not failures —
                // the caller writes the new bytes itself instead.
                Err(e) if needs_full_download(&e) => Ok(PlaceholderOutcome::NeedsFullDownload),
                Err(e) => Err(e),
            }
        }
        CreateVerdict::CallFailed(hr) =>
            Err(anyhow::Error::from(windows::core::Error::from(hr)).context("CfCreatePlaceholders")),
        CreateVerdict::EntryFailed(hr) =>
            Err(anyhow::Error::from(windows::core::Error::from(hr)).context("CfCreatePlaceholders(entry)")),
    }
}

/// What a `CfCreatePlaceholders` call amounted to, once both of its failure
/// surfaces are read.
#[derive(Debug, PartialEq, Eq)]
enum CreateVerdict {
    Created,
    AlreadyExists,
    /// The call's own HRESULT failed.
    CallFailed(HRESULT),
    /// The call succeeded but the entry's `Result` field carries a failure.
    EntryFailed(HRESULT),
}

/// Read `CfCreatePlaceholders`' two failure surfaces.
///
/// The call's HRESULT was assumed to cover only the batch, with a per-entry
/// failure reported solely in the entry's `Result` (S_OK overall). That holds
/// for some failures, but for a single-entry call Windows reports
/// `ERROR_ALREADY_EXISTS` as the *outer* HRESULT — checking only the entry made
/// the update path unreachable, so a changed file whose path already existed
/// failed forever. `ALREADY_EXISTS` therefore counts from either
/// surface.
///
/// The entry `Result` still has to be read on an outer success: a placeholder
/// that was never created would otherwise look like a successful download, and
/// the caller would record the file as synced with no failure row to retry
/// from.
fn classify_create(outer: HRESULT, entry: HRESULT) -> CreateVerdict {
    let exists = |hr: HRESULT| {
        hr == HRESULT::from_win32(ERROR_ALREADY_EXISTS.0)
            || hr == HRESULT::from_win32(ERROR_FILE_EXISTS.0)
    };
    if exists(outer) {
        CreateVerdict::AlreadyExists
    } else if outer.is_err() {
        CreateVerdict::CallFailed(outer)
    } else if exists(entry) {
        CreateVerdict::AlreadyExists
    } else if entry.is_err() {
        CreateVerdict::EntryFailed(entry)
    } else {
        CreateVerdict::Created
    }
}

/// Re-point an existing placeholder at `metadata` and drop whatever bytes it
/// currently holds, so the next read fetches the server's current content.
///
/// `CF_UPDATE_FLAG_DEHYDRATE` without a range array applies to the whole
/// file; `MARK_IN_SYNC` keeps Explorer's Status column on the cloud-checkmark
/// rather than flipping it to sync-pending for a file that *is* in sync — the
/// bytes just are not local. Deliberately never called for the sync-root
/// directory itself: `CfUpdatePlaceholder` answers
/// `ERROR_CLOUD_FILE_ACCESS_DENIED` there (see status.rs).
///
/// The handle must be opened `CF_OPEN_FILE_FLAG_EXCLUSIVE`: with
/// `CF_OPEN_FILE_FLAG_NONE` `CfUpdatePlaceholder` answers `ERROR_INVALID_HANDLE`
/// for every combination of update flags (measured against a live sync root,
//). Hydrate and dehydrate take the plain handle; update does not.
fn update_placeholder(path: &Path, identity: &str, metadata: &CF_FS_METADATA) -> Result<()> {
    let path_w = to_hstring(path)?;
    let handle = unsafe {
        CfOpenFileWithOplock(PCWSTR(path_w.as_ptr()), CF_OPEN_FILE_FLAG_EXCLUSIVE)
            .context("CfOpenFileWithOplock for update")?
    };
    let identity = identity.as_bytes();
    let result = unsafe {
        CfUpdatePlaceholder(
            handle,
            Some(metadata),
            Some(identity.as_ptr() as *const c_void),
            identity.len() as u32,
            None,
            CF_UPDATE_FLAG_MARK_IN_SYNC | CF_UPDATE_FLAG_DEHYDRATE,
            None,
            None,
        )
        .context("CfUpdatePlaceholder")
    };
    unsafe { CfCloseHandle(handle); }
    result
}

/// True for the refusals after which the caller has to write the bytes itself
/// instead of re-pointing a placeholder:
///
/// - `ERROR_CLOUD_FILE_PINNED`: the user pinned the file ("always keep on this
///   device"), so the platform will not drop its bytes.
/// - `ERROR_NOT_A_CLOUD_FILE`: the entry is a plain file, not a placeholder.
///   An editor's atomic save (write a temp file, replace the original) leaves
///   exactly this behind, and there is nothing to re-point.
fn needs_full_download(e: &anyhow::Error) -> bool {
    e.downcast_ref::<windows::core::Error>().is_some_and(|w| {
        w.code() == HRESULT::from_win32(ERROR_CLOUD_FILE_PINNED.0)
            || w.code() == HRESULT::from_win32(ERROR_NOT_A_CLOUD_FILE.0)
    })
}

/// The filesystem metadata a placeholder carries: the server's size, with
/// every timestamp set to the server's mtime.
fn fs_metadata(size: i64, mtime_ft: i64) -> CF_FS_METADATA {
    CF_FS_METADATA {
        BasicInfo: FILE_BASIC_INFO {
            CreationTime:   mtime_ft,
            LastAccessTime: mtime_ft,
            LastWriteTime:  mtime_ft,
            ChangeTime:     mtime_ft,
            FileAttributes: FILE_ATTRIBUTE_NORMAL.0,
        },
        FileSize: size,
    }
}

/// Convert an existing regular file into a CF placeholder so File Explorer's
/// Status column can track its sync state without a custom overlay DLL.
///
/// - `in_sync = false` → Status column shows "Sync pending" (↕)
/// - `in_sync = true`  → Status column shows "Available on this device" (✓)
///
/// Single-attempt conversion of a regular file to a CF placeholder.
///
/// `0x8007017C` (NOT_UNDER_SYNC_ROOT) is **not** retried in here — the CF
/// filter driver settles globally after `CfRegisterSyncRoot`, not per-file,
/// so retrying inside this function multiplies the wait by N files.
/// Callers that process many files (`ensure_placeholders`) collect the
/// failures and do *one* batched retry pass after a single short sleep.
pub fn convert_to_placeholder(path: &Path, server_path: &str, in_sync: bool) -> ConvertOutcome {
    let path_w = match to_hstring(path) {
        Ok(p)  => p,
        Err(e) => return ConvertOutcome::Failed(e.context("to_hstring")),
    };
    // Asked up front, on a read-only handle: on a long-lived root nearly every
    // entry already is a placeholder, and converting one is a no-op the
    // platform refuses anyway. Going straight for the write handle cost a
    // write-access open, a refused conversion and a placeholder query per
    // entry; this answers the common case in one cheaper open.
    if is_existing_placeholder(&path_w) {
        return ConvertOutcome::AlreadyPlaceholder;
    }

    let identity = server_path.as_bytes();
    let flags   = if in_sync { CF_CONVERT_FLAG_MARK_IN_SYNC } else { CF_CONVERT_FLAG_NONE };

    // A read-only file refuses the write handle conversion needs with
    // ACCESS_DENIED, so it stayed a plain file — no sync state in Explorer,
    // and a failure per file on every start. The attribute is lifted for the
    // conversion alone and put back once the handle is closed.
    let mut lifted = None;
    let opened = match open_for_convert(&path_w) {
        Err(e) if e.code() == HRESULT::from_win32(ERROR_ACCESS_DENIED.0) => {
            lifted = ReadOnlyLifted::lift(&path_w);
            if lifted.is_some() { open_for_convert(&path_w) } else { Err(e) }
        }
        other => other,
    };
    let handle = match opened {
        Ok(h)  => h,
        Err(e) => return ConvertOutcome::Failed(anyhow::Error::new(e).context("CreateFileW for convert")),
    };

    let result = unsafe {
        CfConvertToPlaceholder(
            handle,
            Some(identity.as_ptr() as *const c_void),
            identity.len() as u32,
            flags,
            None,
            None,
        )
    };
    // Only asked on a refusal, so the success path costs nothing extra.
    let already = result.is_err() && is_placeholder(handle);

    unsafe { let _ = CloseHandle(handle); }
    drop(lifted);

    // Classify on the HRESULT itself, not on the rendered error string: the
    // codes are the contract, and `0x8007017C` had been labelled
    // NOT_UNDER_SYNC_ROOT here although that is 390 (`0x80070186`) —
    // `0x8007017C` is ERROR_CLOUD_FILE_INVALID_REQUEST (380). Matching text
    // meant the genuine not-under-sync-root case fell into the loud
    // `Failed` bucket.
    match result {
        Ok(()) => ConvertOutcome::Succeeded,
        // Converting an existing placeholder is refused, and the code it is
        // refused with is not a contract: older builds answered 0x8007001A,
        // current ones (measured on 26220) answer 0x8007017C — the very
        // code the filter also uses while it is still attaching. Reading that
        // as "not ready" kept every placeholder of a long-lived root out of
        // the `AlreadyPlaceholder` path, so its in-sync bit was never
        // re-asserted. Asking the entry itself is what tells them apart.
        Err(_) if already => ConvertOutcome::AlreadyPlaceholder,
        Err(e) if super::is_filter_not_ready(e.code()) => {
            // The bucket holds two different HRESULTs and used to swallow both
            // silently, as a cosmetic detail. A field log then showed 6102 of
            // 6104 files landing in it — every one of them left as a plain
            // file carrying no sync state at all, which Explorer reports as
            // "available on this device" because the bytes are simply there.
            // The code is what tells the two cases apart, so say it — once,
            // because at that scale it would otherwise be the whole log.
            static REPORTED: std::sync::Once = std::sync::Once::new();
            REPORTED.call_once(|| {
                tracing::warn!(
                    "CfConvertToPlaceholder refused '{}' with {:?} — no placeholder, \
                     so no sync state in Explorer; further occurrences are counted, not logged",
                    path.display(), e.code(),
                );
            });
            ConvertOutcome::NotUnderSyncRoot
        }
        Err(e) => ConvertOutcome::Failed(
            anyhow::Error::new(e).context("CfConvertToPlaceholder"),
        ),
    }
}

/// The handle `CfConvertToPlaceholder` requires: per its docs it MUST carry
/// FILE_WRITE_ATTRIBUTES, FILE_WRITE_DATA and DELETE. With
/// FILE_WRITE_ATTRIBUTES alone the API answers an unrelated 0x8007017C
/// instead of ACCESS_DENIED.
fn open_for_convert(path_w: &HSTRING) -> windows::core::Result<HANDLE> {
    unsafe {
        CreateFileW(
            PCWSTR(path_w.as_ptr()),
            (FILE_WRITE_ATTRIBUTES | FILE_WRITE_DATA | DELETE).0,
            FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
            None,
            OPEN_EXISTING,
            FILE_FLAG_BACKUP_SEMANTICS,
            None,
        )
    }
}

/// A file's read-only attribute, cleared for as long as this lives and set
/// again on drop.
///
/// Restoring ORs the bit into the attributes the file has *then*, rather than
/// writing back the ones read before: the conversion in between sets
/// attributes of its own, which a blind restore would wipe. Only the
/// attribute moves, never the file's mtime, so the watcher's size/mtime
/// fast-path sees no change to sync.
struct ReadOnlyLifted<'a> {
    path_w: &'a HSTRING,
}

impl<'a> ReadOnlyLifted<'a> {
    /// `None` unless `path_w` is a read-only file whose attribute could be
    /// cleared. Directories are left alone: read-only does not stop their
    /// write handle, and on a directory it means something else to Explorer
    /// (a `desktop.ini` customisation).
    fn lift(path_w: &'a HSTRING) -> Option<Self> {
        let attrs = unsafe { GetFileAttributesW(PCWSTR(path_w.as_ptr())) };
        if attrs == INVALID_FILE_ATTRIBUTES
            || attrs & FILE_ATTRIBUTE_READONLY.0 == 0
            || attrs & FILE_ATTRIBUTE_DIRECTORY.0 != 0
        {
            return None;
        }
        unsafe {
            SetFileAttributesW(
                PCWSTR(path_w.as_ptr()),
                FILE_FLAGS_AND_ATTRIBUTES(attrs & !FILE_ATTRIBUTE_READONLY.0),
            )
        }
        .ok()?;
        Some(Self { path_w })
    }
}

impl Drop for ReadOnlyLifted<'_> {
    fn drop(&mut self) {
        let attrs = unsafe { GetFileAttributesW(PCWSTR(self.path_w.as_ptr())) };
        let restored = attrs != INVALID_FILE_ATTRIBUTES
            && unsafe {
                SetFileAttributesW(
                    PCWSTR(self.path_w.as_ptr()),
                    FILE_FLAGS_AND_ATTRIBUTES(attrs | FILE_ATTRIBUTE_READONLY.0),
                )
            }
            .is_ok();
        if !restored {
            tracing::warn!(
                "could not restore the read-only attribute on '{}' after converting it",
                self.path_w,
            );
        }
    }
}

/// True when the entry at `path_w` already is a CF placeholder, asked through
/// a handle that only reads attributes. Any failure to open answers `false`,
/// which leaves the caller on its full conversion path.
fn is_existing_placeholder(path_w: &HSTRING) -> bool {
    let handle = match unsafe {
        CreateFileW(
            PCWSTR(path_w.as_ptr()),
            FILE_READ_ATTRIBUTES.0,
            FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
            None,
            OPEN_EXISTING,
            FILE_FLAG_BACKUP_SEMANTICS,
            None,
        )
    } {
        Ok(h)  => h,
        Err(_) => return false,
    };
    let placeholder = is_placeholder(handle);
    unsafe { let _ = CloseHandle(handle); }
    placeholder
}

/// True when the entry behind `handle` already is a CF placeholder. Asked
/// through the Cloud Files API rather than the reparse tag: the filter
/// disguises placeholders from processes in the default compatibility mode,
/// so a plain attribute query reports a normal file here. A buffer too small
/// for the identity (`ERROR_MORE_DATA`) still means the entry is one; any
/// other failure answers `false`, which keeps the caller on its refusal.
fn is_placeholder(handle: HANDLE) -> bool {
    let mut info = CF_PLACEHOLDER_BASIC_INFO::default();
    let queried = unsafe {
        CfGetPlaceholderInfo(
            handle,
            CF_PLACEHOLDER_INFO_BASIC,
            &mut info as *mut _ as *mut c_void,
            std::mem::size_of::<CF_PLACEHOLDER_BASIC_INFO>() as u32,
            None,
        )
    };
    match queried {
        Ok(()) => true,
        Err(e) => e.code() == HRESULT::from_win32(ERROR_MORE_DATA.0),
    }
}

/// Settle an excluded file in Explorer: convert it to an *in-sync*
/// placeholder so the Status column shows "available on this device" (✓)
/// instead of the misleading "sync pending", and pin it so Windows never
/// dehydrates a file that has no server copy to recall from.
///
/// Best-effort and idempotent: an already-converted file just gets re-pinned;
/// `NotUnderSyncRoot` (CF filter not settled yet) is left for the next pass.
/// Uses only the standard CF placeholder APIs that every synced file already
/// goes through — unlike the custom property column, this does not crash
/// Explorer (ADR-0020).
pub fn mark_excluded(path: &Path, identity: &str) -> Result<()> {
    match convert_to_placeholder(path, identity, true) {
        ConvertOutcome::Failed(e)        => return Err(e),
        ConvertOutcome::NotUnderSyncRoot => return Ok(()),
        ConvertOutcome::Succeeded | ConvertOutcome::AlreadyPlaceholder => {}
    }
    super::status::set_pinned(path)
}

/// Trigger synchronous hydration of a dehydrated placeholder.
///
/// This calls `CfHydratePlaceholder`, which causes Windows to invoke our
/// registered FETCH_DATA callback on its own thread-pool thread.  The
/// callback downloads the file from the server and transfers the bytes back
/// via `CfExecute`.  `CfHydratePlaceholder` blocks until the callback is done,
/// so this function must be called from a **blocking** thread (e.g. via
/// `tokio::task::spawn_blocking`) — never directly from a Tokio executor thread.
pub fn hydrate_file(path: &Path) -> Result<()> {
    let path_w = to_hstring(path)?;
    // CfOpenFileWithOplock opens the placeholder for the sync provider with
    // an appropriate oplock so Windows knows we are the CF sync provider.
    let handle = unsafe {
        CfOpenFileWithOplock(PCWSTR(path_w.as_ptr()), CF_OPEN_FILE_FLAG_NONE)
            .context("CfOpenFileWithOplock")?
    };
    let result = unsafe {
        // length = -1 means "hydrate the entire file".
        // Passing None for overlapped makes this call synchronous.
        CfHydratePlaceholder(handle, 0, -1, CF_HYDRATE_FLAG_NONE, None)
            .context("CfHydratePlaceholder")
    };
    // Always close the CF handle regardless of outcome.
    unsafe { CfCloseHandle(handle); }
    result
}

/// Dehydrate a placeholder, freeing its local bytes while keeping the file
/// visible in Explorer (the inverse of [`hydrate_file`]). Backs the "free up
/// space" context-menu verb. Like hydration this must run on a blocking thread.
pub fn dehydrate_file(path: &Path) -> Result<()> {
    let path_w = to_hstring(path)?;
    let handle = unsafe {
        CfOpenFileWithOplock(PCWSTR(path_w.as_ptr()), CF_OPEN_FILE_FLAG_NONE)
            .context("CfOpenFileWithOplock")?
    };
    let result = unsafe {
        // length = -1 means "dehydrate the entire file".
        CfDehydratePlaceholder(handle, 0, -1, CF_DEHYDRATE_FLAG_NONE, None)
            .context("CfDehydratePlaceholder")
    };
    unsafe { CfCloseHandle(handle); }
    result
}

#[cfg(test)]
mod tests {
    use super::*;
    use super::super::registration::{register, unregister};

    fn exists()  -> HRESULT { HRESULT::from_win32(ERROR_ALREADY_EXISTS.0) }
    fn file_exists() -> HRESULT { HRESULT::from_win32(ERROR_FILE_EXISTS.0) }
    fn other()   -> HRESULT { HRESULT::from_win32(ERROR_CLOUD_FILE_PINNED.0) }

    /// For a single-entry call Windows reports ALREADY_EXISTS as the
    /// call's own HRESULT, so it must reach the update path from there.
    #[test]
    fn already_exists_as_the_outer_hresult_is_an_update_not_a_failure() {
        assert_eq!(classify_create(exists(),      HRESULT(0)), CreateVerdict::AlreadyExists);
        assert_eq!(classify_create(file_exists(), HRESULT(0)), CreateVerdict::AlreadyExists);
    }

    #[test]
    fn already_exists_in_the_entry_result_is_still_an_update() {
        assert_eq!(classify_create(HRESULT(0), exists()),      CreateVerdict::AlreadyExists);
        assert_eq!(classify_create(HRESULT(0), file_exists()), CreateVerdict::AlreadyExists);
    }

    /// The entry check must survive: an outer success with a failed entry is a
    /// placeholder that was never created, not a download that worked.
    #[test]
    fn a_failed_entry_behind_a_successful_call_is_a_failure() {
        assert_eq!(classify_create(HRESULT(0), other()), CreateVerdict::EntryFailed(other()));
    }

    #[test]
    fn any_other_outer_failure_stays_a_failure() {
        assert_eq!(classify_create(other(), HRESULT(0)), CreateVerdict::CallFailed(other()));
    }

    #[test]
    fn both_surfaces_clean_is_created() {
        assert_eq!(classify_create(HRESULT(0), HRESULT(0)), CreateVerdict::Created);
    }

    struct Unregister(std::path::PathBuf);
    impl Drop for Unregister {
        fn drop(&mut self) { let _ = unregister(&self.0); }
    }

    /// A read-only file used to fail its conversion with ACCESS_DENIED on
    /// every start and never get a sync state. It must convert, and keep its
    /// read-only attribute — that is the user's, not ours.
    #[test]
    fn a_read_only_file_converts_and_stays_read_only() {
        let dir  = tempfile::tempdir().unwrap();
        let root = dir.path().join("root");
        std::fs::create_dir_all(&root).unwrap();
        register(&root, "privasync-readonly-test").unwrap();
        let _guard = Unregister(root.clone());

        let file = root.join("photo.jpg");
        std::fs::write(&file, b"jpeg").unwrap();
        let mut perms = std::fs::metadata(&file).unwrap().permissions();
        perms.set_readonly(true);
        std::fs::set_permissions(&file, perms).unwrap();

        let outcome = match convert_to_placeholder(&file, "/photo.jpg", true) {
            ConvertOutcome::Succeeded        => "succeeded".to_string(),
            ConvertOutcome::Failed(e)        => format!("failed: {e:#}"),
            ConvertOutcome::NotUnderSyncRoot => "not under sync root".to_string(),
            ConvertOutcome::AlreadyPlaceholder => "already a placeholder".to_string(),
        };
        assert_eq!(outcome, "succeeded", "a read-only file must convert");
        assert!(std::fs::metadata(&file).unwrap().permissions().readonly(),
            "the read-only attribute must be back after the conversion");
        assert!(matches!(convert_to_placeholder(&file, "/photo.jpg", true),
            ConvertOutcome::AlreadyPlaceholder));

        // tempdir cleanup cannot delete a read-only file.
        let mut perms = std::fs::metadata(&file).unwrap().permissions();
        #[allow(clippy::permissions_set_readonly_false)]
        perms.set_readonly(false);
        std::fs::set_permissions(&file, perms).unwrap();
    }

    /// Real Cloud Files round trip: registers a throwaway sync root under the
    /// temp dir, the only way to see which HRESULT surface Windows really
    /// uses. Removes the registration again on drop.
    #[test]
    fn a_changed_file_at_an_existing_placeholder_path_is_repointed() {
        let dir  = tempfile::tempdir().unwrap();
        let root = dir.path().join("root");
        std::fs::create_dir_all(&root).unwrap();
        register(&root, "privasync-placeholder-test").unwrap();
        let _guard = Unregister(root.clone());

        let first = PlaceholderInfo { relative_path: "/fresh.3mf", size: 10, mtime: 1_700_000_000 };
        assert_eq!(ensure_placeholder(&root, &first).unwrap(), PlaceholderOutcome::Created);

        let newer = PlaceholderInfo { relative_path: "/fresh.3mf", size: 20, mtime: 1_700_000_100 };
        assert_eq!(ensure_placeholder(&root, &newer).unwrap(), PlaceholderOutcome::Updated);
    }

    fn kind(o: &ConvertOutcome) -> &'static str {
        match o {
            ConvertOutcome::Succeeded          => "succeeded",
            ConvertOutcome::AlreadyPlaceholder => "already",
            ConvertOutcome::NotUnderSyncRoot   => "not_under_root",
            ConvertOutcome::Failed(_)          => "failed",
        }
    }

    /// A second conversion is refused with the same code the filter uses
    /// while still attaching. It must still read as `AlreadyPlaceholder`, for
    /// files and directories, hydrated, dehydrated and pinned alike — otherwise
    /// a long-lived root never gets its in-sync bits re-asserted.
    #[test]
    fn converting_an_existing_placeholder_is_already_placeholder() {
        let dir  = tempfile::tempdir().unwrap();
        let root = dir.path().join("root");
        std::fs::create_dir_all(root.join("sub")).unwrap();
        register(&root, "privasync-placeholder-test").unwrap();
        let _guard = Unregister(root.clone());

        let file = root.join("plain.3mf");
        std::fs::write(&file, b"hello").unwrap();
        assert_eq!(kind(&convert_to_placeholder(&file, "/plain.3mf", true)), "succeeded");
        assert_eq!(kind(&convert_to_placeholder(&file, "/plain.3mf", true)), "already");
        assert_eq!(kind(&convert_to_placeholder(&file, "/plain.3mf", false)), "already");

        let sub = root.join("sub");
        assert_eq!(kind(&convert_to_placeholder(&sub, "/sub", true)), "succeeded");
        assert_eq!(kind(&convert_to_placeholder(&sub, "/sub", true)), "already");

        let info = PlaceholderInfo { relative_path: "/fresh.3mf", size: 10, mtime: 1_700_000_000 };
        ensure_placeholder(&root, &info).unwrap();
        assert_eq!(kind(&convert_to_placeholder(&root.join("fresh.3mf"), "/fresh.3mf", true)), "already");

        super::super::status::set_pinned(&file).unwrap();
        assert_eq!(kind(&convert_to_placeholder(&file, "/plain.3mf", true)), "already");
    }

    /// A refusal on something that is not a placeholder must not be dressed
    /// up as `AlreadyPlaceholder`: outside any sync root the plain file stays
    /// in the refusal bucket.
    #[test]
    fn a_plain_file_outside_a_sync_root_is_not_already_placeholder() {
        let dir  = tempfile::tempdir().unwrap();
        let file = dir.path().join("loose.txt");
        std::fs::write(&file, b"x").unwrap();
        assert_ne!(kind(&convert_to_placeholder(&file, "/loose.txt", true)), "already");
    }

    /// The state an editor's atomic save leaves behind: a plain file where a
    /// placeholder used to be. Must not fail (a failure is retried forever);
    /// the caller writes the bytes instead.
    #[test]
    fn a_plain_file_at_the_path_asks_the_caller_to_download() {
        let dir  = tempfile::tempdir().unwrap();
        let root = dir.path().join("root");
        std::fs::create_dir_all(&root).unwrap();
        register(&root, "privasync-placeholder-test").unwrap();
        let _guard = Unregister(root.clone());

        std::fs::write(root.join("plain.3mf"), b"hello").unwrap();
        let info = PlaceholderInfo { relative_path: "/plain.3mf", size: 10, mtime: 1_700_000_000 };
        assert_eq!(ensure_placeholder(&root, &info).unwrap(), PlaceholderOutcome::NeedsFullDownload);
    }
}
