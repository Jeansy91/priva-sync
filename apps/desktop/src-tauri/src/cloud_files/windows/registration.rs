//! Sync-root registration and unregistration via the WinRT
//! `StorageProviderSyncRootManager`. Lower-level `CfRegisterSyncRoot`
//! is intentionally not used: it returns S_OK without making the
//! provider visible to the kernel filter, which would cause
//! `CfConvertToPlaceholder` to fail with 0x8007017C.

use std::path::Path;

use anyhow::{anyhow, Context, Result};
use tracing::{info, warn};

use windows::core::{HSTRING, PWSTR};
use windows::Win32::Foundation::{CloseHandle, LocalFree, HANDLE};
use windows::Win32::Security::Authorization::ConvertSidToStringSidW;
use windows::Win32::Security::{GetTokenInformation, TokenUser, TOKEN_QUERY, TOKEN_USER};
use windows::Win32::System::Threading::{GetCurrentProcess, OpenProcessToken};
use windows::Win32::UI::Shell::{SHChangeNotify, SHCNE_UPDATEDIR, SHCNF_PATHW};

use windows::Security::Cryptography::{BinaryStringEncoding, CryptographicBuffer};
use windows::Storage::Provider::{
    StorageProviderHardlinkPolicy, StorageProviderHydrationPolicy,
    StorageProviderHydrationPolicyModifier, StorageProviderInSyncPolicy,
    StorageProviderPopulationPolicy, StorageProviderProtectionMode, StorageProviderSyncRootInfo,
    StorageProviderSyncRootManager,
};
use windows::Storage::StorageFolder;

use super::to_hstring;

/// Identifier used as the first segment of the
/// `StorageProviderSyncRootInfo.Id`.
const PROVIDER_ID_STR: &str = "PrivaSync";

/// Fallback provider icon in case Windows cannot resolve the sync-root icon.
fn provider_icon_resource() -> String {
    match std::env::current_exe() {
        Ok(exe) => format!("{},0", exe.display()),
        Err(_) => "%SystemRoot%\\system32\\imageres.dll,-1043".to_string(),
    }
}

fn root_icon_resource(local_path: &Path) -> String {
    match super::branding::folder_icon_resource(local_path) {
        Ok(resource) => resource,
        Err(error) => {
            warn!(
                "could not resolve Explorer icon for sync root '{}': {error:#}; using provider icon",
                local_path.display()
            );
            provider_icon_resource()
        }
    }
}

/// Returns the current user's SID as a string (e.g. `S-1-5-21-...`). The
/// Storage Provider sync-root Id format is `<ProviderId>!<UserSid>!<AccountId>`
/// and registration fails if the middle segment is not a valid SID for the
/// current user.
pub(super) fn current_user_sid() -> Result<String> {
    unsafe {
        let mut token = HANDLE::default();
        OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut token)
            .context("OpenProcessToken")?;

        // First call: query required buffer length. Expected to "fail" with
        // ERROR_INSUFFICIENT_BUFFER while writing the size into `len`.
        let mut len: u32 = 0;
        let _ = GetTokenInformation(token, TokenUser, None, 0, &mut len);
        if len == 0 {
            let _ = CloseHandle(token);
            return Err(anyhow!("GetTokenInformation: zero-length probe"));
        }

        let mut buf = vec![0u8; len as usize];
        let res = GetTokenInformation(
            token,
            TokenUser,
            Some(buf.as_mut_ptr() as *mut _),
            len,
            &mut len,
        );
        let _ = CloseHandle(token);
        res.context("GetTokenInformation(TokenUser)")?;

        let token_user = &*(buf.as_ptr() as *const TOKEN_USER);
        let mut sid_pwstr = PWSTR::null();
        ConvertSidToStringSidW(token_user.User.Sid, &mut sid_pwstr)
            .context("ConvertSidToStringSidW")?;

        let s = sid_pwstr.to_string().context("PWSTR.to_string")?;
        // ConvertSidToStringSidW allocates with LocalAlloc; free with LocalFree.
        let _ = LocalFree(Some(windows::Win32::Foundation::HLOCAL(
            sid_pwstr.0 as *mut _,
        )));
        Ok(s)
    }
}

/// Stable per-path account identifier — the third segment of the sync-root
/// Id. Hashing the absolute path keeps it deterministic across runs without
/// depending on user-mutable state. Bang character is forbidden inside any
/// segment by the WinRT API.
fn account_id_for_path(local_path: &Path) -> String {
    use std::collections::hash_map::DefaultHasher;
    use std::hash::{Hash, Hasher};
    let mut h = DefaultHasher::new();
    local_path.to_string_lossy().to_lowercase().hash(&mut h);
    format!("p{:016x}", h.finish())
}

pub(super) fn sync_root_id(local_path: &Path) -> Result<String> {
    let sid = current_user_sid()?;
    Ok(format!(
        "{PROVIDER_ID_STR}!{sid}!{}",
        account_id_for_path(local_path)
    ))
}

fn storage_folder(path: &Path) -> Result<StorageFolder> {
    let path_str = path.to_str().ok_or_else(|| anyhow!("non-UTF-8 path"))?;
    let path_h = HSTRING::from(path_str);
    StorageFolder::GetFolderFromPathAsync(&path_h)
        .context("GetFolderFromPathAsync")?
        .join()
        .context("GetFolderFromPathAsync.join")
}

fn sync_root_info(
    id: &str,
    folder: &StorageFolder,
    display_name: &str,
    icon_resource: &str,
) -> Result<StorageProviderSyncRootInfo> {
    let info = StorageProviderSyncRootInfo::new().context("StorageProviderSyncRootInfo::new")?;
    info.SetId(&HSTRING::from(id))?;
    info.SetPath(folder)?;
    info.SetDisplayNameResource(&HSTRING::from(display_name))?;
    info.SetIconResource(&HSTRING::from(icon_resource))?;
    info.SetVersion(&HSTRING::from("1.0"))?;
    info.SetHydrationPolicy(StorageProviderHydrationPolicy::Full)?;
    info.SetHydrationPolicyModifier(StorageProviderHydrationPolicyModifier::None)?;
    info.SetPopulationPolicy(StorageProviderPopulationPolicy::AlwaysFull)?;
    info.SetProtectionMode(StorageProviderProtectionMode::Unknown)?;
    info.SetShowSiblingsAsGroup(false)?;
    info.SetHardlinkPolicy(StorageProviderHardlinkPolicy::None)?;
    info.SetInSyncPolicy(
        StorageProviderInSyncPolicy::FileLastWriteTime
            | StorageProviderInSyncPolicy::DirectoryLastWriteTime,
    )?;
    // Recommended-but-optional context blob. Some Windows versions reject
    // registrations without one; safest to always set something deterministic.
    let context =
        CryptographicBuffer::ConvertStringToBinary(&HSTRING::from(id), BinaryStringEncoding::Utf8)
            .context("CryptographicBuffer::ConvertStringToBinary")?;
    info.SetContext(&context)?;

    Ok(info)
}

pub fn register(local_path: &Path, display_name: &str) -> Result<()> {
    let id = sync_root_id(local_path)?;

    // NB: We deliberately do NOT call Unregister here even though earlier
    // builds did to clean up legacy CfRegisterSyncRoot entries. Calling
    // Unregister + Register on every startup invalidates dehydrated
    // on-demand placeholders — Windows treats them as orphaned and
    // reconcile_local then sees the whole sync root as locally deleted,
    // tripping the bulk-delete guard. StorageProviderSyncRootManager.Register
    // is idempotent for an identical Id+params, so re-registering on
    // startup is a safe no-op when the registration is already correct.

    let icon = root_icon_resource(local_path);
    let folder = storage_folder(local_path)?;
    let info = sync_root_info(&id, &folder, display_name, &icon)?;
    StorageProviderSyncRootManager::Register(&info)
        .context("StorageProviderSyncRootManager::Register")?;

    // The registration above brands the navigation-pane entry; the folder
    // itself gets an InfoTip hover tooltip via a classic desktop.ini.
    // Cosmetic — never fail register.
    if let Err(e) = super::branding::apply_folder_branding(local_path, display_name) {
        warn!(
            "folder branding failed for '{}': {e:#}",
            local_path.display()
        );
    }

    // Nudge Explorer to re-evaluate the sync root + its parent. Without this
    // it keeps the cached pre-registration view (no cloud icon, no Status
    // column) until the user navigates away and back.
    let path_w_self = to_hstring(local_path)?;
    let parent_path_w = local_path.parent().and_then(|p| to_hstring(p).ok());
    unsafe {
        SHChangeNotify(
            SHCNE_UPDATEDIR,
            SHCNF_PATHW,
            Some(path_w_self.as_ptr() as *const _),
            None,
        );
        if let Some(parent_w) = &parent_path_w {
            SHChangeNotify(
                SHCNE_UPDATEDIR,
                SHCNF_PATHW,
                Some(parent_w.as_ptr() as *const _),
                None,
            );
        }
    }

    wait_for_filter(local_path);

    info!(
        "registered cloud sync root: {} (id={id})",
        local_path.display()
    );
    Ok(())
}

pub(super) fn unregister_namespace_root(path: &Path) -> Result<String> {
    let id = sync_root_id(path)?;
    let current = StorageProviderSyncRootManager::GetCurrentSyncRoots()
        .context("StorageProviderSyncRootManager::GetCurrentSyncRoots")?;
    for index in 0..current.Size()? {
        if current.GetAt(index)?.Id()? == id {
            StorageProviderSyncRootManager::Unregister(&HSTRING::from(id.as_str()))
                .context("unregister PrivaSync Explorer group root")?;
            break;
        }
    }
    Ok(id)
}

/// How long `register` waits for the CF filter driver to attach, and how
/// often it re-probes while waiting.
const FILTER_ATTACH_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(10);
const FILTER_ATTACH_INTERVAL: std::time::Duration = std::time::Duration::from_millis(100);

/// Block until the CF filter driver reports `local_path` as part of a sync
/// root (or the timeout expires). Returns whether the filter answered.
///
/// `StorageProviderSyncRootManager::Register` returns as soon as the
/// registration is recorded; the kernel filter attaches a moment later, and
/// every CF call issued in between fails with 0x8007017C
/// (`ERROR_CLOUD_FILE_NOT_UNDER_SYNC_ROOT`). Adding a sync root triggers an
/// immediate sync, and the changes feed delivers file entries before the
/// directory entries that contain them — so without this wait the very first
/// batch of `CfCreatePlaceholders` calls lands squarely inside that window.
///
/// Waiting *here* is what keeps it cheap: the filter settles once per root,
/// not per file, so the alternative is N files each paying (and failing)
/// their own settle.
///
/// Best-effort by design — a timeout is not an error. The placeholder path
/// reports its own failures and retries them with backoff; this only removes
/// the predictable first-cycle burst.
fn wait_for_filter(local_path: &Path) -> bool {
    use std::time::Instant;
    use windows::Win32::Storage::CloudFilters::{
        CfGetSyncRootInfoByPath, CF_SYNC_ROOT_BASIC_INFO, CF_SYNC_ROOT_INFO_BASIC,
    };

    let Ok(path_w) = to_hstring(local_path) else {
        return false;
    };
    let started = Instant::now();
    loop {
        let mut basic = CF_SYNC_ROOT_BASIC_INFO::default();
        let res = unsafe {
            CfGetSyncRootInfoByPath(
                windows::core::PCWSTR(path_w.as_ptr()),
                CF_SYNC_ROOT_INFO_BASIC,
                &mut basic as *mut _ as *mut std::ffi::c_void,
                std::mem::size_of::<CF_SYNC_ROOT_BASIC_INFO>() as u32,
                None,
            )
        };
        match res {
            Ok(()) => return true,
            // The two codes the filter answers with while it is still
            // attaching. Everything else is a condition waiting cannot fix —
            // stop instead of burning the whole timeout on it.
            Err(e) if super::is_filter_not_ready(e.code()) => {}
            Err(e) => {
                warn!(
                    "CF filter probe for '{}' failed: {e:#}",
                    local_path.display()
                );
                return false;
            }
        }
        if started.elapsed() >= FILTER_ATTACH_TIMEOUT {
            warn!(
                "CF filter did not attach to '{}' within {:?} — first placeholder \
                 batch may need a retry pass",
                local_path.display(),
                FILTER_ATTACH_TIMEOUT,
            );
            return false;
        }
        std::thread::sleep(FILTER_ATTACH_INTERVAL);
    }
}

pub fn unregister(local_path: &Path) -> Result<()> {
    let id = sync_root_id(local_path)?;
    if let Err(e) = StorageProviderSyncRootManager::Unregister(&HSTRING::from(id.clone())) {
        warn!("StorageProviderSyncRootManager::Unregister({id}) failed: {e:#}");
    }

    if let Err(e) = super::branding::remove_folder_branding(local_path) {
        warn!(
            "removing folder branding failed for '{}': {e:#}",
            local_path.display()
        );
    }

    // Explorer caches per-folder Cloud-Provider attribution; without an
    // explicit update notification the cloud icons linger on the (now
    // un-registered) folder until the user navigates away. SHCNE_UPDATEDIR
    // forces Explorer to re-query attributes for this directory.
    let path_w = to_hstring(local_path)?;
    unsafe {
        SHChangeNotify(
            SHCNE_UPDATEDIR,
            SHCNF_PATHW,
            Some(path_w.as_ptr() as *const _),
            None,
        );
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_root_icon_resource_uses_an_existing_shell_icon() {
        let temp_dir = tempfile::tempdir().unwrap();
        let icon = root_icon_resource(temp_dir.path());
        // "path,index" — Explorer resolves the icon from whatever is left
        // of the last comma; that file must exist or folders fall back to
        // the plain yellow folder icon.
        let (file, index) = icon.rsplit_once(',').expect("icon must have ,index suffix");
        index.parse::<i32>().expect("index must be numeric");
        assert!(
            std::path::Path::new(file).exists(),
            "icon file must exist: {file}"
        );
    }
}
