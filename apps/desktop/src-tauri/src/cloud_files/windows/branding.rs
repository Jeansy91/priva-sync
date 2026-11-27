//! Folder branding for the sync root so Explorer marks the root folder
//! itself as a PrivaSync root when viewed from its parent directory —
//! without replacing the folder's icon.
//!
//! The Storage Provider registration (SyncRootManager + namespace CLSID)
//! already brands the navigation-pane entry with its own icon; a second,
//! separate icon swap on the folder itself (via desktop.ini) was found too
//! intrusive across many roots on one system. Instead this writes a hidden
//! `desktop.ini` with an `InfoTip` — a hover tooltip naming the root — plus
//! the read-only attribute on the folder (without which Explorer ignores
//! the ini).

use std::path::Path;

use anyhow::{Context, Result};

use windows::core::PCWSTR;
use windows::Win32::Storage::FileSystem::{
    GetFileAttributesW, SetFileAttributesW, FILE_ATTRIBUTE_HIDDEN, FILE_ATTRIBUTE_NORMAL,
    FILE_ATTRIBUTE_READONLY, FILE_ATTRIBUTE_SYSTEM, FILE_FLAGS_AND_ATTRIBUTES,
    INVALID_FILE_ATTRIBUTES,
};
use windows::Win32::UI::Shell::{SHGetFileInfoW, SHFILEINFOW, SHGFI_ICONLOCATION};

use super::to_hstring;

fn change_attributes(path: &Path, f: impl FnOnce(u32) -> u32) -> Result<()> {
    let path_w = to_hstring(path)?;
    unsafe {
        let current = GetFileAttributesW(PCWSTR(path_w.as_ptr()));
        if current == INVALID_FILE_ATTRIBUTES {
            return Err(windows::core::Error::from_thread()).context("GetFileAttributesW");
        }
        SetFileAttributesW(
            PCWSTR(path_w.as_ptr()),
            FILE_FLAGS_AND_ATTRIBUTES(f(current)),
        )
        .context("SetFileAttributesW")
    }
}

fn add_attributes(path: &Path, add: FILE_FLAGS_AND_ATTRIBUTES) -> Result<()> {
    change_attributes(path, |cur| cur | add.0)
}

pub(super) fn folder_icon_resource(dir: &Path) -> Result<String> {
    let (path, index) = folder_icon_location(dir)?;
    Ok(format!("{path},{index}"))
}

pub(super) fn folder_icon_location(dir: &Path) -> Result<(String, i32)> {
    let path = to_hstring(dir)?;
    let mut info = SHFILEINFOW::default();
    let result = unsafe {
        SHGetFileInfoW(
            PCWSTR(path.as_ptr()),
            FILE_FLAGS_AND_ATTRIBUTES(0),
            Some(&mut info),
            std::mem::size_of::<SHFILEINFOW>() as u32,
            SHGFI_ICONLOCATION,
        )
    };
    if result == 0 {
        return Err(windows::core::Error::from_thread()).context("query sync-root folder icon");
    }

    let path = String::from_utf16_lossy(
        &info.szDisplayName[..info
            .szDisplayName
            .iter()
            .position(|unit| *unit == 0)
            .unwrap_or(info.szDisplayName.len())],
    );
    if path.is_empty() {
        return Err(anyhow::anyhow!(
            "Windows returned an empty sync-root icon path"
        ));
    }
    Ok((path, info.iIcon))
}

pub fn apply_folder_branding(dir: &Path, root_name: &str) -> Result<()> {
    let ini = dir.join("desktop.ini");
    std::fs::write(
        &ini,
        format!("[.ShellClassInfo]\r\nInfoTip=PrivaSync-Root: {root_name}\r\n"),
    )
    .context("write desktop.ini")?;
    add_attributes(&ini, FILE_ATTRIBUTE_HIDDEN | FILE_ATTRIBUTE_SYSTEM)?;
    add_attributes(dir, FILE_ATTRIBUTE_READONLY)?;
    Ok(())
}

pub fn remove_folder_branding(dir: &Path) -> Result<()> {
    if !dir.exists() {
        return Ok(());
    }
    let ini = dir.join("desktop.ini");
    if ini.exists() {
        // Clear hidden+system+readonly first — std::fs::remove_file cannot
        // delete a read-only file and some tools mark the ini read-only.
        change_attributes(&ini, |_| FILE_ATTRIBUTE_NORMAL.0)?;
        std::fs::remove_file(&ini).context("remove desktop.ini")?;
    }
    change_attributes(dir, |cur| cur & !FILE_ATTRIBUTE_READONLY.0)?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn attrs(path: &Path) -> u32 {
        use std::os::windows::fs::MetadataExt;
        std::fs::metadata(path).unwrap().file_attributes()
    }

    #[test]
    fn apply_sets_explorer_required_attributes() {
        const READONLY: u32 = 0x1;
        const HIDDEN: u32 = 0x2;
        const SYSTEM: u32 = 0x4;

        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path().join("root");
        std::fs::create_dir(&dir).unwrap();

        apply_folder_branding(&dir, "Test Root").unwrap();

        // Explorer only honors desktop.ini when the folder carries the
        // read-only (or system) attribute; the ini itself is hidden+system
        // so it does not clutter the sync root.
        assert_ne!(attrs(&dir) & READONLY, 0, "folder must be read-only");
        let ini = attrs(&dir.join("desktop.ini"));
        assert_ne!(ini & HIDDEN, 0, "desktop.ini must be hidden");
        assert_ne!(ini & SYSTEM, 0, "desktop.ini must be system");
    }

    #[test]
    fn apply_twice_updates_info_tip_despite_protected_ini() {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path().join("root");
        std::fs::create_dir(&dir).unwrap();

        apply_folder_branding(&dir, "Old Name").unwrap();
        // register() runs on every app start — re-branding must overwrite
        // the hidden+system ini from the previous run, not fail on it.
        apply_folder_branding(&dir, "New Name").unwrap();

        let ini = std::fs::read_to_string(dir.join("desktop.ini")).unwrap();
        assert!(
            ini.contains("InfoTip=PrivaSync-Root: New Name"),
            "ini was: {ini}"
        );
    }

    #[test]
    fn remove_deletes_ini_and_clears_readonly() {
        const READONLY: u32 = 0x1;

        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path().join("root");
        std::fs::create_dir(&dir).unwrap();

        apply_folder_branding(&dir, "Test Root").unwrap();
        remove_folder_branding(&dir).unwrap();

        assert!(
            !dir.join("desktop.ini").exists(),
            "desktop.ini must be gone"
        );
        assert_eq!(attrs(&dir) & READONLY, 0, "folder must not stay read-only");
    }

    #[test]
    fn remove_on_missing_folder_is_a_no_op() {
        let tmp = tempfile::tempdir().unwrap();

        // unregister() can run after the user already deleted the folder.
        remove_folder_branding(&tmp.path().join("gone")).unwrap();
    }

    #[test]
    fn remove_on_unbranded_folder_is_a_no_op() {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path().join("root");
        std::fs::create_dir(&dir).unwrap();

        remove_folder_branding(&dir).unwrap();
    }

    #[test]
    fn apply_writes_desktop_ini_with_info_tip() {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path().join("root");
        std::fs::create_dir(&dir).unwrap();

        apply_folder_branding(&dir, "Meine Dateien").unwrap();

        let ini = std::fs::read_to_string(dir.join("desktop.ini")).unwrap();
        assert!(ini.contains("[.ShellClassInfo]"), "ini was: {ini}");
        assert!(
            !ini.contains("IconResource="),
            "folder icon must not be changed by desktop.ini: {ini}"
        );
        assert!(
            ini.contains("InfoTip=PrivaSync-Root: Meine Dateien"),
            "ini was: {ini}"
        );
    }

    #[test]
    fn folder_icon_resource_returns_a_shell_resource() {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path().join("root");
        std::fs::create_dir(&dir).unwrap();

        let resource = folder_icon_resource(&dir).unwrap();

        assert!(
            resource.rsplit_once(',').is_some(),
            "resource was: {resource}"
        );
    }
}
