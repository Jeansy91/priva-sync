//! Explorer namespace for multiple PrivaSync sync roots.

use std::collections::HashSet;
use std::ffi::c_void;
use std::os::windows::fs::MetadataExt;
use std::path::{Path, PathBuf};

use anyhow::{anyhow, Context, Result};
use tracing::{info, warn};
use windows::core::{Interface, HSTRING, PCWSTR, PWSTR};
use windows::Storage::Provider::StorageProviderSyncRootManager;
use windows::Win32::Storage::FileSystem::{FILE_ATTRIBUTE_DIRECTORY, FILE_ATTRIBUTE_REPARSE_POINT};
use windows::Win32::System::Com::{
    CoCreateInstance, CoInitializeEx, CoUninitialize, IPersistFile, CLSCTX_INPROC_SERVER,
    COINIT_APARTMENTTHREADED,
};
use windows::Win32::System::Registry::{
    RegCloseKey, RegCreateKeyExW, RegDeleteTreeW, RegDeleteValueW, RegEnumKeyExW, RegOpenKeyExW,
    RegQueryInfoKeyW, RegQueryValueExW, RegSetValueExW, HKEY, HKEY_CURRENT_USER,
    HKEY_LOCAL_MACHINE, KEY_READ, KEY_WRITE, REG_DWORD, REG_EXPAND_SZ, REG_OPTION_NON_VOLATILE,
    REG_SZ,
};
use windows::Win32::UI::Shell::{
    IShellLinkW, SHChangeNotify, ShellLink, SHCNE_ASSOCCHANGED, SHCNE_UPDATEDIR, SHCNF_IDLIST,
    SHCNF_PATHW,
};

const NAMESPACE_CLSID: &str = "{A95133A0-8C8E-4BB3-9D25-37DC3B8F56B4}";
const SHELL_FOLDER_INSTANCE_CLSID: &str = "{0E5AAE11-A475-4c5b-AB00-C66DE400274E}";
const SYNC_ROOT_MANAGER_KEY: &str =
    "Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\SyncRootManager";
const PROVIDER_ID_PREFIX: &str = "PrivaSync!";

/// Remove stale roots and migrate away from the native Cloud Files group root.
pub fn reconcile(roots: &[(String, String)]) -> Result<()> {
    prune_stale_sync_roots(roots)?;
    sync(roots)
}

/// Show shortcuts to active sync roots under one Explorer namespace.
pub fn sync(roots: &[(String, String)]) -> Result<()> {
    let namespace_dir = namespace_dir(roots)?;
    for legacy_path in legacy_namespace_dirs() {
        if legacy_path == namespace_dir
            || roots
                .iter()
                .any(|(_, root)| path_contains(root, &legacy_path))
        {
            continue;
        }
        remove_native_namespace_root(&legacy_path)?;
        clear_namespace_registration(&legacy_path)?;
        match std::fs::remove_dir(&legacy_path) {
            Ok(()) => {}
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(error) if error.kind() == std::io::ErrorKind::DirectoryNotEmpty => {
                warn!(
                    "preserving non-empty legacy PrivaSync namespace folder: {}",
                    legacy_path.display()
                );
            }
            Err(error) => {
                return Err(error).context("remove legacy PrivaSync namespace folder");
            }
        }
    }
    remove_native_namespace_root(&namespace_dir)?;
    clear_namespace_registration(&namespace_dir)?;

    let roots_dir = namespace_dir.join("Roots");
    std::fs::create_dir_all(&roots_dir).context("create PrivaSync Explorer namespace folder")?;
    let mut grouped = roots.len() >= 2;
    if grouped {
        let mut shortcuts = Vec::with_capacity(roots.len());
        let mut names = HashSet::new();
        for (index, (name, path)) in roots.iter().enumerate() {
            let visible_name = unique_name(name, index, &mut names);
            shortcuts.push((
                roots_dir.join(format!("{visible_name}.lnk")),
                PathBuf::from(path),
            ));
        }
        if let Err(error) = create_root_shortcuts(shortcuts) {
            warn!("could not add sync roots to the Explorer namespace: {error:#}");
            grouped = false;
        }
    }
    if !grouped {
        remove_owned_links(&roots_dir)?;
    }

    register_namespace_folder(&roots_dir, grouped)?;
    set_sync_root_visibility(grouped)?;
    let roots_path = HSTRING::from(roots_dir.to_string_lossy().as_ref());

    unsafe {
        SHChangeNotify(
            SHCNE_UPDATEDIR,
            SHCNF_PATHW,
            Some(roots_path.as_ptr() as *const c_void),
            None,
        );
        SHChangeNotify(SHCNE_ASSOCCHANGED, SHCNF_IDLIST, None, None);
    }

    info!(
        "Explorer namespace {} for {} PrivaSync sync root(s)",
        if grouped { "enabled" } else { "disabled" },
        roots.len(),
    );
    Ok(())
}

fn prune_stale_sync_roots(roots: &[(String, String)]) -> Result<()> {
    let active_paths: HashSet<String> =
        roots.iter().map(|(_, path)| normalize_path(path)).collect();
    let current = registered_priva_sync_roots()?;
    let mut stale = Vec::new();

    for (id, path, clsid) in current {
        if !active_paths.contains(&normalize_path(&path)) && !Path::new(&path).exists() {
            stale.push((id, path, clsid));
        }
    }

    for (id, path, clsid) in stale {
        if let Err(error) = StorageProviderSyncRootManager::Unregister(&HSTRING::from(id.as_str()))
        {
            warn!("StorageProviderSyncRootManager could not unregister stale root '{path}': {error:#}");
        }

        let root_key = format!("{SYNC_ROOT_MANAGER_KEY}\\{id}");
        let root_key_remains =
            get_registry_string(HKEY_LOCAL_MACHINE, &root_key, "DisplayNameResource")?.is_some();
        if root_key_remains {
            if let Err(error) = delete_registry_tree(HKEY_LOCAL_MACHINE, &root_key) {
                warn!("could not remove stale sync-root registry key '{root_key}': {error:#}");
            }
        }
        if let Some(clsid) = clsid {
            if let Err(error) = delete_registry_tree(
                HKEY_CURRENT_USER,
                &format!("Software\\Classes\\CLSID\\{clsid}"),
            ) {
                warn!("could not remove stale sync-root CLSID '{clsid}': {error:#}");
            }
            if let Err(error) = delete_registry_tree(
                HKEY_CURRENT_USER,
                &format!("Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\Desktop\\NameSpace\\{clsid}"),
            ) {
                warn!("could not remove stale Explorer namespace CLSID '{clsid}': {error:#}");
            }
        }
        info!("removed stale PrivaSync sync-root registration: {path}");
    }
    Ok(())
}

fn registered_priva_sync_roots() -> Result<Vec<(String, String, Option<String>)>> {
    let sid = super::registration::current_user_sid()?;
    let expected_prefix = format!("{PROVIDER_ID_PREFIX}{sid}!");
    let path_w: Vec<u16> = SYNC_ROOT_MANAGER_KEY
        .encode_utf16()
        .chain(Some(0))
        .collect();
    let mut key = HKEY::default();
    unsafe {
        RegOpenKeyExW(
            HKEY_LOCAL_MACHINE,
            PCWSTR(path_w.as_ptr()),
            Some(0),
            KEY_READ,
            &mut key,
        )
        .ok()
        .context("open Windows sync-root registry")?;
    }

    let result = (|| {
        let mut count = 0u32;
        let mut max_name_len = 0u32;
        unsafe {
            RegQueryInfoKeyW(
                key,
                None,
                None,
                None,
                Some(&mut count),
                Some(&mut max_name_len),
                None,
                None,
                None,
                None,
                None,
                None,
            )
            .ok()
            .context("query Windows sync-root registry")?;
        }

        let mut entries = Vec::new();
        for index in 0..count {
            let mut name = vec![0u16; max_name_len as usize + 2];
            let mut name_len = name.len() as u32;
            unsafe {
                RegEnumKeyExW(
                    key,
                    index,
                    Some(PWSTR(name.as_mut_ptr())),
                    &mut name_len,
                    None,
                    None,
                    None,
                    None,
                )
                .ok()
                .context("enumerate Windows sync-root registry")?;
            }
            let id = String::from_utf16_lossy(&name[..name_len as usize]);
            if !id.starts_with(&expected_prefix) {
                continue;
            }

            let root_key = format!("{SYNC_ROOT_MANAGER_KEY}\\{id}");
            let user_roots_key = format!("{root_key}\\UserSyncRoots");
            let path =
                get_registry_string(HKEY_LOCAL_MACHINE, &user_roots_key, &sid)?.unwrap_or_default();
            let clsid = get_registry_string(HKEY_LOCAL_MACHINE, &root_key, "NamespaceCLSID")?;
            entries.push((id, path, clsid));
        }
        Ok(entries)
    })();
    unsafe {
        let _ = RegCloseKey(key);
    }
    result
}

fn clear_namespace_registration(namespace_dir: &Path) -> Result<()> {
    let roots_dir = namespace_dir.join("Roots");
    remove_owned_links(&roots_dir)?;
    match std::fs::remove_dir(&roots_dir) {
        Ok(()) => {}
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
        Err(error) if error.kind() == std::io::ErrorKind::DirectoryNotEmpty => {
            warn!(
                "legacy PrivaSync namespace folder is not empty: {}",
                roots_dir.display()
            );
        }
        Err(error) => return Err(error).context("remove legacy PrivaSync namespace folder"),
    }

    let clsid_key = format!("Software\\Classes\\CLSID\\{NAMESPACE_CLSID}");
    let namespace_key = format!(
        "Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\Desktop\\NameSpace\\{NAMESPACE_CLSID}"
    );
    let hidden_keys = [
        "Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\HideDesktopIcons\\NewStartPanel",
        "Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\HideDesktopIcons\\ClassicStartMenu",
    ];

    delete_registry_tree(HKEY_CURRENT_USER, &clsid_key)?;
    delete_registry_tree(HKEY_CURRENT_USER, &namespace_key)?;
    for key in hidden_keys {
        delete_registry_value(HKEY_CURRENT_USER, key, NAMESPACE_CLSID)?;
    }
    Ok(())
}

fn remove_native_namespace_root(path: &Path) -> Result<()> {
    let id = super::registration::unregister_namespace_root(path)?;
    let root_key = format!("{SYNC_ROOT_MANAGER_KEY}\\{id}");
    let clsid = get_registry_string(HKEY_LOCAL_MACHINE, &root_key, "NamespaceCLSID")?;
    if get_registry_string(HKEY_LOCAL_MACHINE, &root_key, "DisplayNameResource")?.is_some() {
        delete_registry_tree(HKEY_LOCAL_MACHINE, &root_key)
            .context("remove stale native namespace sync-root registration")?;
    }

    if let Some(clsid) = clsid {
        delete_registry_tree(
            HKEY_CURRENT_USER,
            &format!("Software\\Classes\\CLSID\\{clsid}"),
        )?;
        delete_registry_tree(
            HKEY_CURRENT_USER,
            &format!(
                "Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\Desktop\\NameSpace\\{clsid}"
            ),
        )?;
    }
    Ok(())
}

fn local_namespace_dir(local_app_data: &Path) -> PathBuf {
    local_app_data
        .join(crate::app_id::identifier())
        .join("PrivaSync")
        .join("ExplorerNamespace")
}

fn legacy_namespace_dirs() -> Vec<PathBuf> {
    [
        std::env::var_os("LOCALAPPDATA")
            .map(PathBuf::from)
            .map(|path| {
                path.join(crate::app_id::identifier())
                    .join("ExplorerNamespace")
            }),
        std::env::var_os("TEMP")
            .map(PathBuf::from)
            .map(|path| path.join("PrivaSync").join("ExplorerNamespace")),
    ]
    .into_iter()
    .flatten()
    .collect()
}

fn namespace_dir(roots: &[(String, String)]) -> Result<PathBuf> {
    let candidates = [
        std::env::var_os("LOCALAPPDATA")
            .map(PathBuf::from)
            .map(|path| local_namespace_dir(&path)),
        std::env::var_os("TEMP")
            .map(PathBuf::from)
            .map(|path| path.join("PrivaSync").join("NamespaceRoot")),
    ];

    for candidate in candidates.into_iter().flatten() {
        if roots
            .iter()
            .any(|(_, root)| path_contains(root, &candidate))
        {
            continue;
        }
        if let Err(error) = std::fs::create_dir_all(&candidate) {
            warn!(
                "cannot access legacy Explorer namespace at '{}': {error}",
                candidate.display()
            );
            continue;
        }
        return Ok(candidate);
    }

    anyhow::bail!("could not locate the PrivaSync Explorer namespace folder")
}

fn normalize_path(path: &str) -> String {
    let mut normalized = path.replace('/', "\\").to_lowercase();
    if let Some(without_prefix) = normalized.strip_prefix("\\\\?\\") {
        normalized = without_prefix.to_string();
    }
    while normalized.len() > 3 && normalized.ends_with('\\') {
        normalized.pop();
    }
    normalized
}

fn path_contains(parent: &str, child: &Path) -> bool {
    let parent = normalize_path(parent);
    let child = normalize_path(&child.to_string_lossy());
    child == parent || child.starts_with(&format!("{}\\", parent.trim_end_matches('\\')))
}

fn unique_name(name: &str, index: usize, used: &mut HashSet<String>) -> String {
    let mut safe: String = name
        .chars()
        .map(|ch| {
            if "<>:\"/\\|?*".contains(ch) || ch.is_control() {
                '_'
            } else {
                ch
            }
        })
        .collect();
    safe = safe
        .trim_end_matches([' ', '.'])
        .chars()
        .take(120)
        .collect();
    if safe.is_empty() || is_reserved_name(&safe) {
        safe = format!("Sync-Root-{}", index + 1);
    }

    let base = safe.clone();
    let mut suffix = 2;
    while !used.insert(safe.to_lowercase()) {
        safe = format!("{base} ({suffix})");
        suffix += 1;
    }
    safe
}

fn is_reserved_name(name: &str) -> bool {
    let stem = name.split('.').next().unwrap_or(name).to_ascii_uppercase();
    matches!(stem.as_str(), "CON" | "PRN" | "AUX" | "NUL")
        || ["COM", "LPT"]
            .iter()
            .any(|prefix| (1..=9).any(|index| stem == format!("{prefix}{index}")))
}

fn remove_owned_links(roots_dir: &Path) -> Result<()> {
    let entries = match std::fs::read_dir(roots_dir) {
        Ok(entries) => entries,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(error) => return Err(error).context("read PrivaSync namespace folder"),
    };

    for entry in entries {
        let entry = entry.context("read PrivaSync namespace entry")?;
        let path = entry.path();
        let metadata =
            std::fs::symlink_metadata(&path).context("inspect PrivaSync namespace entry")?;
        let attributes = metadata.file_attributes();
        if attributes & FILE_ATTRIBUTE_REPARSE_POINT.0 != 0 {
            if attributes & FILE_ATTRIBUTE_DIRECTORY.0 != 0 {
                std::fs::remove_dir(path).context("remove old sync-root junction")?;
            } else {
                std::fs::remove_file(path).context("remove old sync-root link")?;
            }
        } else if metadata.is_file()
            && path
                .extension()
                .is_some_and(|extension| extension.eq_ignore_ascii_case("lnk"))
        {
            std::fs::remove_file(path).context("remove old sync-root shortcut")?;
        }
    }
    Ok(())
}

fn create_root_shortcuts(shortcuts: Vec<(PathBuf, PathBuf)>) -> Result<()> {
    std::thread::spawn(move || {
        let _apartment = ComApartment::initialize()?;
        for (shortcut, target) in shortcuts {
            create_root_shortcut(&shortcut, &target)?;
        }
        Ok(())
    })
    .join()
    .map_err(|_| anyhow!("Explorer shortcut thread panicked"))?
}

struct ComApartment;

impl ComApartment {
    fn initialize() -> Result<Self> {
        unsafe { CoInitializeEx(None, COINIT_APARTMENTTHREADED).ok() }
            .context("initialize Explorer shortcut COM apartment")?;
        Ok(Self)
    }
}

impl Drop for ComApartment {
    fn drop(&mut self) {
        unsafe { CoUninitialize() };
    }
}

fn create_root_shortcut(shortcut_path: &Path, target_path: &Path) -> Result<()> {
    let target = target_path
        .to_str()
        .ok_or_else(|| anyhow!("sync-root path is not valid UTF-8"))?;
    let shortcut: IShellLinkW = unsafe {
        CoCreateInstance(&ShellLink, None, CLSCTX_INPROC_SERVER)
            .context("create Explorer shell link")?
    };
    let target = HSTRING::from(target);
    unsafe {
        shortcut
            .SetPath(PCWSTR(target.as_ptr()))
            .context("set sync-root shortcut target")?;
    }

    match super::branding::folder_icon_location(target_path) {
        Ok((icon_path, icon_index)) => {
            let icon_path = HSTRING::from(icon_path);
            if let Err(error) =
                unsafe { shortcut.SetIconLocation(PCWSTR(icon_path.as_ptr()), icon_index) }
            {
                warn!(
                    "could not set Explorer icon for sync root '{}': {error:#}",
                    target_path.display()
                );
            }
        }
        Err(error) => {
            warn!(
                "could not resolve Explorer icon for sync root '{}': {error:#}",
                target_path.display()
            );
        }
    }

    let persist: IPersistFile = shortcut
        .cast()
        .context("query shell link persistence interface")?;
    let shortcut_path = HSTRING::from(shortcut_path.to_string_lossy().as_ref());
    unsafe {
        persist
            .Save(PCWSTR(shortcut_path.as_ptr()), true)
            .context("save sync-root shortcut")?;
    }
    Ok(())
}

fn register_namespace_folder(roots_dir: &Path, pinned: bool) -> Result<()> {
    let clsid_key = format!("Software\\Classes\\CLSID\\{NAMESPACE_CLSID}");
    let namespace_key = format!(
        "Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\Desktop\\NameSpace\\{NAMESPACE_CLSID}"
    );
    let hidden_key =
        "Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\HideDesktopIcons\\NewStartPanel";
    let classic_hidden_key =
        "Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\HideDesktopIcons\\ClassicStartMenu";
    let icon = format!("{},0", std::env::current_exe()?.display());

    set_registry_string(HKEY_CURRENT_USER, &clsid_key, None, "PrivaSync", REG_SZ)?;
    set_registry_string(
        HKEY_CURRENT_USER,
        &format!("{clsid_key}\\DefaultIcon"),
        None,
        &icon,
        REG_SZ,
    )?;
    set_registry_dword(
        HKEY_CURRENT_USER,
        &clsid_key,
        Some("System.IsPinnedToNameSpaceTree"),
        u32::from(pinned),
    )?;
    set_registry_dword(HKEY_CURRENT_USER, &clsid_key, Some("SortOrderIndex"), 0x42)?;
    let inproc = format!("{clsid_key}\\InProcServer32");
    let custom_shell_folder = shell_extension_dll();
    if let Some(dll_path) = &custom_shell_folder {
        set_registry_string(
            HKEY_CURRENT_USER,
            &inproc,
            None,
            &dll_path.to_string_lossy(),
            REG_SZ,
        )?;
    } else {
        warn!("privasync_shell.dll not found; using the non-expandable shell-link namespace");
        set_registry_string(
            HKEY_CURRENT_USER,
            &inproc,
            None,
            r"%SystemRoot%\System32\shell32.dll",
            REG_EXPAND_SZ,
        )?;
    }
    set_registry_string(
        HKEY_CURRENT_USER,
        &inproc,
        Some("ThreadingModel"),
        "Apartment",
        REG_SZ,
    )?;
    if custom_shell_folder.is_none() {
        set_registry_string(
            HKEY_CURRENT_USER,
            &format!("{clsid_key}\\Instance"),
            Some("CLSID"),
            SHELL_FOLDER_INSTANCE_CLSID,
            REG_SZ,
        )?;
    }
    let init = format!("{clsid_key}\\Instance\\InitPropertyBag");
    set_registry_dword(HKEY_CURRENT_USER, &init, Some("Attributes"), 0x11)?;
    set_registry_string(
        HKEY_CURRENT_USER,
        &init,
        Some("TargetFolderPath"),
        &roots_dir.to_string_lossy(),
        REG_SZ,
    )?;
    let shell_folder = format!("{clsid_key}\\ShellFolder");
    set_registry_dword(
        HKEY_CURRENT_USER,
        &shell_folder,
        Some("FolderValueFlags"),
        0x28,
    )?;
    set_registry_dword(
        HKEY_CURRENT_USER,
        &shell_folder,
        Some("Attributes"),
        0xF080004D,
    )?;
    set_registry_string(HKEY_CURRENT_USER, &namespace_key, None, "PrivaSync", REG_SZ)?;
    set_registry_dword(HKEY_CURRENT_USER, hidden_key, Some(NAMESPACE_CLSID), 1)?;
    set_registry_dword(
        HKEY_CURRENT_USER,
        classic_hidden_key,
        Some(NAMESPACE_CLSID),
        1,
    )?;
    Ok(())
}

fn shell_extension_dll() -> Option<PathBuf> {
    let installed = std::env::current_exe()
        .ok()
        .and_then(|path| path.parent().map(|directory| directory.join("privasync_shell.dll")));
    let development = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("..")
        .join("shell")
        .join("target")
        .join("x86_64-pc-windows-msvc")
        .join("release")
        .join("privasync_shell.dll");

    let candidates = if cfg!(debug_assertions) {
        [Some(development), installed]
    } else {
        [installed, Some(development)]
    };
    candidates
        .into_iter()
        .flatten()
        .find(|path| path.is_file())
}

fn set_sync_root_visibility(grouped: bool) -> Result<()> {
    let current = StorageProviderSyncRootManager::GetCurrentSyncRoots()
        .context("StorageProviderSyncRootManager::GetCurrentSyncRoots")?;
    for index in 0..current.Size()? {
        let root = current.GetAt(index)?;
        let id = root.Id()?.to_string();
        if !id.starts_with(PROVIDER_ID_PREFIX) {
            continue;
        }
        if let Err(error) = set_registered_root_pinned(&id, !grouped) {
            warn!("could not update Explorer visibility for sync root '{id}': {error:#}");
        }
    }
    Ok(())
}

fn set_registered_root_pinned(id: &str, pinned: bool) -> Result<()> {
    let sync_root_key = format!("{SYNC_ROOT_MANAGER_KEY}\\{id}");
    let Some(clsid) = get_registry_string(HKEY_LOCAL_MACHINE, &sync_root_key, "NamespaceCLSID")?
    else {
        return Ok(());
    };
    let clsid_key = format!("Software\\Classes\\CLSID\\{clsid}");
    set_registry_dword(
        HKEY_CURRENT_USER,
        &clsid_key,
        Some("System.IsPinnedToNameSpaceTree"),
        u32::from(pinned),
    )
}

fn get_registry_string(hive: HKEY, subkey: &str, name: &str) -> Result<Option<String>> {
    let key_name: Vec<u16> = subkey.encode_utf16().chain(Some(0)).collect();
    let value_name: Vec<u16> = name.encode_utf16().chain(Some(0)).collect();
    let mut key = HKEY::default();
    let opened =
        unsafe { RegOpenKeyExW(hive, PCWSTR(key_name.as_ptr()), Some(0), KEY_READ, &mut key) };
    if opened.is_err() {
        return Ok(None);
    }

    let result = (|| {
        let mut value_type = REG_SZ;
        let mut byte_count = 0u32;
        unsafe {
            RegQueryValueExW(
                key,
                PCWSTR(value_name.as_ptr()),
                None,
                Some(&mut value_type),
                None,
                Some(&mut byte_count),
            )
            .ok()
            .context("query registry string size")?;
        }
        if byte_count == 0 {
            return Ok(None);
        }
        let mut data = vec![0u8; byte_count as usize];
        unsafe {
            RegQueryValueExW(
                key,
                PCWSTR(value_name.as_ptr()),
                None,
                Some(&mut value_type),
                Some(data.as_mut_ptr()),
                Some(&mut byte_count),
            )
            .ok()
            .context("read registry string")?;
        }
        if value_type != REG_SZ && value_type != REG_EXPAND_SZ {
            return Ok(None);
        }
        let value: Vec<u16> = data[..byte_count as usize]
            .chunks_exact(2)
            .map(|pair| u16::from_le_bytes([pair[0], pair[1]]))
            .take_while(|unit| *unit != 0)
            .collect();
        Ok(Some(String::from_utf16_lossy(&value)))
    })();
    unsafe {
        let _ = RegCloseKey(key);
    }
    result
}

fn set_registry_dword(hive: HKEY, subkey: &str, name: Option<&str>, value: u32) -> Result<()> {
    set_registry_value(hive, subkey, name, REG_DWORD, &value.to_le_bytes())
}

fn set_registry_string(
    hive: HKEY,
    subkey: &str,
    name: Option<&str>,
    value: &str,
    value_type: windows::Win32::System::Registry::REG_VALUE_TYPE,
) -> Result<()> {
    let data: Vec<u8> = value
        .encode_utf16()
        .chain(Some(0))
        .flat_map(u16::to_le_bytes)
        .collect();
    set_registry_value(hive, subkey, name, value_type, &data)
}

fn delete_registry_tree(hive: HKEY, subkey: &str) -> Result<()> {
    let subkey_w: Vec<u16> = subkey.encode_utf16().chain(Some(0)).collect();
    let result = unsafe { RegDeleteTreeW(hive, PCWSTR(subkey_w.as_ptr())) };
    if result.0 == 2 || result.0 == 3 {
        return Ok(());
    }
    result.ok().context("delete registry tree")
}

fn delete_registry_value(hive: HKEY, subkey: &str, name: &str) -> Result<()> {
    let subkey_w: Vec<u16> = subkey.encode_utf16().chain(Some(0)).collect();
    let value_name_w: Vec<u16> = name.encode_utf16().chain(Some(0)).collect();
    let mut key = HKEY::default();
    let opened = unsafe {
        RegOpenKeyExW(
            hive,
            PCWSTR(subkey_w.as_ptr()),
            Some(0),
            KEY_WRITE,
            &mut key,
        )
    };
    if opened.0 == 2 || opened.0 == 3 {
        return Ok(());
    }
    opened.ok().context("open registry key for value removal")?;

    let result = unsafe { RegDeleteValueW(key, PCWSTR(value_name_w.as_ptr())) };
    unsafe {
        let _ = RegCloseKey(key);
    }
    if result.0 == 2 {
        return Ok(());
    }
    result.ok().context("delete registry value")
}

fn set_registry_value(
    hive: HKEY,
    subkey: &str,
    name: Option<&str>,
    value_type: windows::Win32::System::Registry::REG_VALUE_TYPE,
    data: &[u8],
) -> Result<()> {
    let subkey_w: Vec<u16> = subkey.encode_utf16().chain(Some(0)).collect();
    let value_name_w = name.map(|name| name.encode_utf16().chain(Some(0)).collect::<Vec<_>>());
    let value_name = value_name_w
        .as_ref()
        .map(|name| PCWSTR(name.as_ptr()))
        .unwrap_or_else(PCWSTR::null);
    let mut key = HKEY::default();
    unsafe {
        RegCreateKeyExW(
            hive,
            PCWSTR(subkey_w.as_ptr()),
            Some(0),
            PCWSTR::null(),
            REG_OPTION_NON_VOLATILE,
            KEY_WRITE,
            None,
            &mut key,
            None,
        )
        .ok()
        .context("create registry key")?;
        let result = RegSetValueExW(key, value_name, Some(0), value_type, Some(data))
            .ok()
            .context("write registry value");
        let _ = RegCloseKey(key);
        result
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use windows::Win32::System::Com::STGM_READ;

    #[test]
    fn local_namespace_shell_parent_is_named_privasync() {
        let group_path = local_namespace_dir(Path::new(r"C:\Users\Alice\AppData\Local"));
        assert_eq!(
            group_path.parent().and_then(Path::file_name),
            Some(std::ffi::OsStr::new("PrivaSync"))
        );
    }

    #[test]
    fn sync_root_shortcut_preserves_target_folder_icon() -> Result<()> {
        let temp_dir = tempfile::tempdir()?;
        let target_path = temp_dir.path().join("Sync root");
        std::fs::create_dir(&target_path)?;
        let shortcut_path = temp_dir.path().join("Sync root.lnk");
        let expected = super::super::branding::folder_icon_location(&target_path)?;

        create_root_shortcuts(vec![(shortcut_path.clone(), target_path)])?;

        let shortcut_path = HSTRING::from(shortcut_path.to_string_lossy().as_ref());
        let actual = std::thread::spawn(move || -> Result<(String, i32)> {
            let _apartment = ComApartment::initialize()?;
            let shortcut: IShellLinkW = unsafe {
                CoCreateInstance(&ShellLink, None, CLSCTX_INPROC_SERVER)
                    .context("create Explorer shell link for test")?
            };
            let persist: IPersistFile = shortcut
                .cast()
                .context("query shell link persistence interface for test")?;
            unsafe {
                persist
                    .Load(PCWSTR(shortcut_path.as_ptr()), STGM_READ)
                    .context("load Explorer shell link for test")?;
            }

            let mut icon_path = vec![0u16; 260];
            let mut icon_index = 0;
            unsafe {
                shortcut
                    .GetIconLocation(&mut icon_path, &mut icon_index)
                    .context("read sync-root shortcut icon for test")?;
            }
            let end = icon_path
                .iter()
                .position(|unit| *unit == 0)
                .unwrap_or(icon_path.len());
            Ok((String::from_utf16_lossy(&icon_path[..end]), icon_index))
        })
        .join()
        .map_err(|_| anyhow!("Explorer shortcut test thread panicked"))??;

        assert_eq!(actual, expected);
        Ok(())
    }

    #[test]
    fn shortcut_names_are_safe_and_unique() {
        let mut used = HashSet::new();

        assert_eq!(unique_name("Documents", 0, &mut used), "Documents");
        assert_eq!(unique_name("documents", 1, &mut used), "documents (2)");
        assert_eq!(unique_name("CON.", 2, &mut used), "Sync-Root-3");
        assert_eq!(unique_name("Pictures/2026", 3, &mut used), "Pictures_2026");
    }
}
