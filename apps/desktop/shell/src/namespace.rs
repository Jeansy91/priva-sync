//! Shell-folder adapter for the PrivaSync Explorer namespace.

use std::ffi::c_void;
use std::os::windows::ffi::OsStrExt;
use std::path::{Path, PathBuf};

use windows::core::{implement, Error, Interface, Result, GUID, PCWSTR};
use windows::Win32::Foundation::{E_FAIL, E_INVALIDARG, E_POINTER, HWND, S_FALSE, S_OK};
use windows::Win32::System::Com::{
    CoCreateInstance, IBindCtx, IPersistFile, IPersist_Impl, CLSCTX_INPROC_SERVER, STGM_READ,
};
use windows::Win32::System::Registry::{
    RegCloseKey, RegOpenKeyExW, RegQueryValueExW, HKEY, HKEY_CURRENT_USER, KEY_READ, REG_SZ,
};
use windows::Win32::UI::Shell::Common::{ITEMIDLIST, STRRET};
use windows::Win32::UI::Shell::{
    IEnumIDList, IEnumIDList_Impl, ILFree, IPersistFolder, IPersistFolder_Impl, IShellFolder,
    IShellFolder_Impl, IShellLinkW, SHGetDesktopFolder, SHParseDisplayName, ShellLink,
    StrRetToBufW, SHCONTF_FOLDERS, SHCONTF_NONFOLDERS, SHGDN_FORPARSING,
};

pub const CLSID_PRIVASYNC_NAMESPACE: GUID = GUID::from_u128(0xa95133a0_8c8e_4bb3_9d25_37dc3b8f56b4);

const ROOTS_PATH_KEY: &str =
    "Software\\Classes\\CLSID\\{A95133A0-8C8E-4BB3-9D25-37DC3B8F56B4}\\Instance\\InitPropertyBag";
const SFGAO_FOLDER: u32 = 0x2000_0000;
const SFGAO_HASSUBFOLDER: u32 = 0x8000_0000;
const SFGAO_FILESYSTEM: u32 = 0x4000_0000;
const SFGAO_FILESYSANCESTOR: u32 = 0x1000_0000;
const SFGAO_LINK: u32 = 0x0001_0000;

#[implement(IShellFolder, IPersistFolder)]
pub struct NamespaceFolder {
    inner: IShellFolder,
}

impl NamespaceFolder {
    pub fn create() -> Result<IShellFolder> {
        let roots_path = roots_path_from_registry()?;
        Self::from_roots_path(&roots_path)
    }

    fn from_roots_path(roots_path: &Path) -> Result<IShellFolder> {
        let inner = bind_folder(roots_path)?;
        crate::retain_com_object();
        Ok(NamespaceFolder { inner }.into())
    }

    fn shortcut_path(&self, pidl: *const ITEMIDLIST) -> Result<PathBuf> {
        shortcut_path(&self.inner, pidl)
    }

    fn shortcut_target(&self, pidl: *const ITEMIDLIST) -> Result<PathBuf> {
        let shortcut = self.shortcut_path(pidl)?;
        link_target(&shortcut)
    }
}

#[implement(IEnumIDList)]
struct FolderLinkEnumerator {
    inner: IEnumIDList,
    parent: IShellFolder,
}

impl FolderLinkEnumerator {
    fn wrap(inner: IEnumIDList, parent: IShellFolder) -> IEnumIDList {
        crate::retain_com_object();
        FolderLinkEnumerator { inner, parent }.into()
    }

    fn next_item(&self) -> Result<Option<*mut ITEMIDLIST>> {
        loop {
            let mut item = std::ptr::null_mut();
            let mut fetched = 0;
            let status = unsafe {
                self.inner
                    .Next(std::slice::from_mut(&mut item), Some(&mut fetched))
            };
            if status.is_err() {
                return Err(Error::from_hresult(status));
            }
            if fetched == 0 {
                return Ok(None);
            }

            match self.is_folder_item(item) {
                Ok(true) => return Ok(Some(item)),
                Ok(false) => unsafe { ILFree(Some(item)) },
                Err(error) => {
                    unsafe { ILFree(Some(item)) };
                    return Err(error);
                }
            }
        }
    }

    fn is_folder_item(&self, pidl: *const ITEMIDLIST) -> Result<bool> {
        let mut attributes = SFGAO_FOLDER;
        unsafe { self.parent.GetAttributesOf(&[pidl], &mut attributes)? };
        if attributes & SFGAO_FOLDER != 0 {
            return Ok(true);
        }

        let path = shortcut_path(&self.parent, pidl)?;
        if path
            .extension()
            .is_some_and(|ext| ext.eq_ignore_ascii_case("lnk"))
        {
            return Ok(link_target(&path)?.is_dir());
        }
        Ok(false)
    }
}

impl Drop for FolderLinkEnumerator {
    fn drop(&mut self) {
        crate::release_com_object();
    }
}

impl IEnumIDList_Impl for FolderLinkEnumerator_Impl {
    fn Next(
        &self,
        count: u32,
        items: *mut *mut ITEMIDLIST,
        fetched: *mut u32,
    ) -> windows::core::HRESULT {
        if count > 0 && items.is_null() {
            return E_POINTER;
        }
        if count > 1 && fetched.is_null() {
            return E_INVALIDARG;
        }

        let mut total = 0;
        while total < count {
            match self.next_item() {
                Ok(Some(item)) => {
                    unsafe { *items.add(total as usize) = item };
                    total += 1;
                }
                Ok(None) => break,
                Err(error) => {
                    if !fetched.is_null() {
                        unsafe { *fetched = total };
                    }
                    return error.code();
                }
            }
        }

        if !fetched.is_null() {
            unsafe { *fetched = total };
        }
        if total == count {
            S_OK
        } else {
            S_FALSE
        }
    }

    fn Skip(&self, count: u32) -> windows::core::HRESULT {
        for _ in 0..count {
            match self.next_item() {
                Ok(Some(item)) => unsafe { ILFree(Some(item)) },
                Ok(None) => return S_FALSE,
                Err(error) => return error.code(),
            }
        }
        S_OK
    }

    fn Reset(&self) -> windows::core::HRESULT {
        unsafe { self.inner.Reset() }
    }

    fn Clone(&self, output: windows::core::OutRef<'_, IEnumIDList>) -> windows::core::HRESULT {
        let mut inner = None;
        let status = unsafe { self.inner.Clone(&mut inner) };
        if status.is_err() {
            return status;
        }
        match inner {
            Some(inner) => output
                .write(Some(FolderLinkEnumerator::wrap(inner, self.parent.clone())))
                .map_or_else(|error| error.code(), |_| status),
            None => E_FAIL,
        }
    }
}

impl Drop for NamespaceFolder {
    fn drop(&mut self) {
        crate::release_com_object();
    }
}

impl IPersist_Impl for NamespaceFolder_Impl {
    fn GetClassID(&self) -> Result<GUID> {
        Ok(CLSID_PRIVASYNC_NAMESPACE)
    }
}

impl IPersistFolder_Impl for NamespaceFolder_Impl {
    fn Initialize(&self, _pidl: *const ITEMIDLIST) -> Result<()> {
        Ok(())
    }
}

impl IShellFolder_Impl for NamespaceFolder_Impl {
    fn ParseDisplayName(
        &self,
        hwnd: HWND,
        pbc: windows::core::Ref<'_, IBindCtx>,
        display_name: &PCWSTR,
        eaten: *const u32,
        pidl: *mut *mut ITEMIDLIST,
        attributes: *mut u32,
    ) -> Result<()> {
        unsafe {
            self.inner.ParseDisplayName(
                hwnd,
                pbc.as_ref(),
                *display_name,
                (!eaten.is_null()).then_some(eaten),
                pidl,
                attributes,
            )
        }
    }

    fn EnumObjects(
        &self,
        hwnd: HWND,
        flags: u32,
        result: windows::core::OutRef<'_, windows::Win32::UI::Shell::IEnumIDList>,
    ) -> windows::core::HRESULT {
        let mut enumerator = None;
        let folder_only =
            flags & SHCONTF_FOLDERS.0 as u32 != 0 && flags & SHCONTF_NONFOLDERS.0 as u32 == 0;
        let enumeration_flags = if folder_only {
            flags | SHCONTF_NONFOLDERS.0 as u32
        } else {
            flags
        };
        let status = unsafe {
            self.inner
                .EnumObjects(hwnd, enumeration_flags, &mut enumerator)
        };
        if status.is_err() {
            return status;
        }
        let Some(enumerator) = enumerator else {
            return status;
        };
        let enumerator = if folder_only {
            FolderLinkEnumerator::wrap(enumerator, self.inner.clone())
        } else {
            enumerator
        };
        result
            .write(Some(enumerator))
            .map_or_else(|error| error.code(), |_| status)
    }

    fn BindToObject(
        &self,
        pidl: *const ITEMIDLIST,
        _pbc: windows::core::Ref<'_, IBindCtx>,
        riid: *const GUID,
        ppv: *mut *mut c_void,
    ) -> Result<()> {
        let target_path = self.shortcut_target(pidl)?;
        let folder = bind_folder(&target_path)?;
        unsafe { folder.query(riid, ppv).ok() }
    }

    fn BindToStorage(
        &self,
        pidl: *const ITEMIDLIST,
        pbc: windows::core::Ref<'_, IBindCtx>,
        riid: *const GUID,
        ppv: *mut *mut c_void,
    ) -> Result<()> {
        let vtable = Interface::vtable(&self.inner);
        let bind_context = pbc.as_ref().map_or(std::ptr::null_mut(), Interface::as_raw);
        unsafe {
            (vtable.BindToStorage)(
                Interface::as_raw(&self.inner),
                pidl,
                bind_context,
                riid,
                ppv,
            )
            .ok()
        }
    }

    fn CompareIDs(
        &self,
        lparam: windows::Win32::Foundation::LPARAM,
        first: *const ITEMIDLIST,
        second: *const ITEMIDLIST,
    ) -> windows::core::HRESULT {
        unsafe { self.inner.CompareIDs(lparam, first, second) }
    }

    fn CreateViewObject(&self, hwnd: HWND, riid: *const GUID, ppv: *mut *mut c_void) -> Result<()> {
        let vtable = Interface::vtable(&self.inner);
        unsafe { (vtable.CreateViewObject)(Interface::as_raw(&self.inner), hwnd, riid, ppv).ok() }
    }

    fn GetAttributesOf(
        &self,
        count: u32,
        pidls: *const *const ITEMIDLIST,
        attributes: *mut u32,
    ) -> Result<()> {
        if attributes.is_null() || (count > 0 && pidls.is_null()) {
            return Err(Error::from(E_POINTER));
        }

        let pidl_slice = if count == 0 {
            &[]
        } else {
            unsafe { std::slice::from_raw_parts(pidls, count as usize) }
        };
        let requested = unsafe { *attributes };
        unsafe { self.inner.GetAttributesOf(pidl_slice, attributes)? };

        for pidl in pidl_slice {
            let shortcut = self.shortcut_path(*pidl)?;
            if !shortcut
                .extension()
                .is_some_and(|ext| ext.eq_ignore_ascii_case("lnk"))
            {
                continue;
            }
            if link_target(&shortcut)?.is_dir() {
                unsafe {
                    *attributes &= !SFGAO_LINK;
                    *attributes |= requested
                        & (SFGAO_FOLDER
                            | SFGAO_HASSUBFOLDER
                            | SFGAO_FILESYSTEM
                            | SFGAO_FILESYSANCESTOR);
                }
            }
        }
        Ok(())
    }

    fn GetUIObjectOf(
        &self,
        hwnd: HWND,
        count: u32,
        pidls: *const *const ITEMIDLIST,
        riid: *const GUID,
        reserved: *const u32,
        ppv: *mut *mut c_void,
    ) -> Result<()> {
        let vtable = Interface::vtable(&self.inner);
        unsafe {
            (vtable.GetUIObjectOf)(
                Interface::as_raw(&self.inner),
                hwnd,
                count,
                pidls,
                riid,
                reserved,
                ppv,
            )
            .ok()
        }
    }

    fn GetDisplayNameOf(
        &self,
        pidl: *const ITEMIDLIST,
        flags: windows::Win32::UI::Shell::SHGDNF,
        name: *mut STRRET,
    ) -> Result<()> {
        unsafe { self.inner.GetDisplayNameOf(pidl, flags, name) }
    }

    fn SetNameOf(
        &self,
        hwnd: HWND,
        pidl: *const ITEMIDLIST,
        name: &PCWSTR,
        flags: windows::Win32::UI::Shell::SHGDNF,
        output: *mut *mut ITEMIDLIST,
    ) -> Result<()> {
        unsafe {
            self.inner.SetNameOf(
                Some(hwnd),
                pidl,
                *name,
                flags,
                (!output.is_null()).then_some(output),
            )
        }
    }
}

fn shortcut_path(folder: &IShellFolder, pidl: *const ITEMIDLIST) -> Result<PathBuf> {
    let mut name = STRRET::default();
    unsafe { folder.GetDisplayNameOf(pidl, SHGDN_FORPARSING, &mut name)? };

    let mut buffer = vec![0u16; 32_768];
    unsafe { StrRetToBufW(&mut name, Some(pidl), &mut buffer)? };
    let end = buffer
        .iter()
        .position(|ch| *ch == 0)
        .ok_or_else(|| Error::from(E_FAIL))?;
    Ok(PathBuf::from(String::from_utf16_lossy(&buffer[..end])))
}

fn roots_path_from_registry() -> Result<PathBuf> {
    let key_name: Vec<u16> = ROOTS_PATH_KEY.encode_utf16().chain(Some(0)).collect();
    let value_name: Vec<u16> = "TargetFolderPath".encode_utf16().chain(Some(0)).collect();
    let mut key = HKEY::default();
    unsafe {
        RegOpenKeyExW(
            HKEY_CURRENT_USER,
            PCWSTR(key_name.as_ptr()),
            Some(0),
            KEY_READ,
            &mut key,
        )
        .ok()?;

        let mut value_type = REG_SZ;
        let mut bytes = 0;
        let query_size = RegQueryValueExW(
            key,
            PCWSTR(value_name.as_ptr()),
            None,
            Some(&mut value_type),
            None,
            Some(&mut bytes),
        );
        if query_size.is_err() || value_type != REG_SZ || bytes < 2 || bytes % 2 != 0 {
            let _ = RegCloseKey(key);
            return Err(Error::from(E_FAIL));
        }

        let mut data = vec![0u16; bytes as usize / 2];
        let query_value = RegQueryValueExW(
            key,
            PCWSTR(value_name.as_ptr()),
            None,
            Some(&mut value_type),
            Some(data.as_mut_ptr() as *mut u8),
            Some(&mut bytes),
        );
        let _ = RegCloseKey(key);
        query_value.ok()?;
        if value_type != REG_SZ || bytes % 2 != 0 {
            return Err(Error::from(E_FAIL));
        }
        let end = data.iter().position(|ch| *ch == 0).unwrap_or(data.len());
        if end == 0 {
            return Err(Error::from(E_FAIL));
        }
        let path = PathBuf::from(String::from_utf16_lossy(&data[..end]));
        if !path.is_absolute() {
            return Err(Error::from(E_FAIL));
        }
        Ok(path)
    }
}

fn bind_folder(path: &Path) -> Result<IShellFolder> {
    let wide: Vec<u16> = path.as_os_str().encode_wide().chain(Some(0)).collect();
    let mut pidl = std::ptr::null_mut();
    unsafe {
        SHParseDisplayName(PCWSTR(wide.as_ptr()), None::<&IBindCtx>, &mut pidl, 0, None)?;
        if pidl.is_null() {
            return Err(Error::from(E_POINTER));
        }
        let desktop = match SHGetDesktopFolder() {
            Ok(desktop) => desktop,
            Err(error) => {
                ILFree(Some(pidl));
                return Err(error);
            }
        };
        let result = desktop.BindToObject(pidl, None::<&IBindCtx>);
        ILFree(Some(pidl));
        result
    }
}

fn link_target(shortcut_path: &Path) -> Result<PathBuf> {
    let shortcut: IShellLinkW =
        unsafe { CoCreateInstance(&ShellLink, None, CLSCTX_INPROC_SERVER)? };
    let persist_file: IPersistFile = shortcut.cast()?;
    let shortcut_path: Vec<u16> = shortcut_path
        .as_os_str()
        .encode_wide()
        .chain(Some(0))
        .collect();
    unsafe { persist_file.Load(PCWSTR(shortcut_path.as_ptr()), STGM_READ)? };

    let mut target = vec![0u16; 32_768];
    unsafe {
        shortcut.GetPath(&mut target, std::ptr::null_mut(), 0)?;
    }
    let end = target
        .iter()
        .position(|ch| *ch == 0)
        .ok_or_else(|| Error::from(E_FAIL))?;
    let target = PathBuf::from(String::from_utf16_lossy(&target[..end]));
    if target.as_os_str().is_empty() || !target.is_absolute() {
        return Err(Error::from(E_FAIL));
    }
    Ok(target)
}

#[cfg(test)]
mod tests {
    use super::*;

    use std::fs;
    use std::thread;
    use std::time::{SystemTime, UNIX_EPOCH};

    use windows::Win32::System::Com::{CoInitializeEx, CoUninitialize, COINIT_APARTMENTTHREADED};
    use windows::Win32::UI::Shell::{
        IEnumIDList, IExtractIconW, SHCONTF_FOLDERS, SHCONTF_NONFOLDERS, SHGDN_NORMAL,
    };

    struct ComApartment;

    impl ComApartment {
        fn initialize() -> Result<Self> {
            unsafe { CoInitializeEx(None, COINIT_APARTMENTTHREADED).ok()? };
            Ok(Self)
        }
    }

    impl Drop for ComApartment {
        fn drop(&mut self) {
            unsafe { CoUninitialize() };
        }
    }

    #[test]
    fn shortcut_children_bind_as_expandable_folders() {
        thread::spawn(|| {
            let _apartment = ComApartment::initialize().unwrap();
            let unique = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos();
            let fixture = std::env::temp_dir()
                .join(format!("privasync-shell-{}-{unique}", std::process::id()));
            let roots = fixture.join("Roots");
            let root = fixture.join("SyncRoot");
            fs::create_dir_all(&roots).unwrap();
            fs::create_dir_all(&root).unwrap();
            fs::write(root.join("payload.txt"), b"child").unwrap();
            fs::write(roots.join("notes.txt"), b"not a sync root").unwrap();

            let link_path = roots.join("SyncRoot.lnk");
            let link: IShellLinkW =
                unsafe { CoCreateInstance(&ShellLink, None, CLSCTX_INPROC_SERVER) }.unwrap();
            let target_w: Vec<u16> = root.as_os_str().encode_wide().chain(Some(0)).collect();
            unsafe { link.SetPath(PCWSTR(target_w.as_ptr())) }.unwrap();
            let icon_path = Path::new(r"C:\Windows\System32\shell32.dll");
            let icon_path_w: Vec<u16> =
                icon_path.as_os_str().encode_wide().chain(Some(0)).collect();
            unsafe { link.SetIconLocation(PCWSTR(icon_path_w.as_ptr()), 3) }.unwrap();
            let persist: IPersistFile = link.cast().unwrap();
            let link_w: Vec<u16> = link_path.as_os_str().encode_wide().chain(Some(0)).collect();
            unsafe { persist.Save(PCWSTR(link_w.as_ptr()), true) }.unwrap();

            let folder = NamespaceFolder::from_roots_path(&roots).unwrap();
            let mut enumerator = None;
            unsafe {
                folder.EnumObjects(HWND::default(), SHCONTF_FOLDERS.0 as u32, &mut enumerator)
            }
            .ok()
            .unwrap();
            let enumerator: IEnumIDList = enumerator.unwrap();
            let mut children = [std::ptr::null_mut()];
            let mut fetched = 0;
            unsafe { enumerator.Next(&mut children, Some(&mut fetched)) }
                .ok()
                .unwrap();
            assert_eq!(fetched, 1);
            let mut remaining = [std::ptr::null_mut()];
            let mut remaining_fetched = 0;
            unsafe { enumerator.Next(&mut remaining, Some(&mut remaining_fetched)) }
                .ok()
                .unwrap();
            assert_eq!(remaining_fetched, 0);
            let child = children[0];

            let mut attributes = SFGAO_FOLDER | SFGAO_HASSUBFOLDER | SFGAO_LINK;
            unsafe { folder.GetAttributesOf(&[child as *const ITEMIDLIST], &mut attributes) }
                .unwrap();
            assert_eq!(attributes & SFGAO_FOLDER, SFGAO_FOLDER);
            assert_eq!(attributes & SFGAO_HASSUBFOLDER, SFGAO_HASSUBFOLDER);
            assert_eq!(attributes & SFGAO_LINK, 0);

            let icon: IExtractIconW =
                unsafe { folder.GetUIObjectOf(HWND::default(), &[child], None) }.unwrap();
            let mut icon_file = [0u16; 260];
            let mut icon_index = 0;
            let mut icon_flags = 0;
            unsafe { icon.GetIconLocation(0, &mut icon_file, &mut icon_index, &mut icon_flags) }
                .unwrap();
            let icon_end = icon_file.iter().position(|ch| *ch == 0).unwrap();
            assert!(String::from_utf16_lossy(&icon_file[..icon_end]).ends_with("shell32.dll"));
            assert_eq!(icon_index, 3);

            let target_folder: IShellFolder =
                unsafe { folder.BindToObject(child, None::<&IBindCtx>) }.unwrap();
            let mut child_enumerator = None;
            unsafe {
                target_folder.EnumObjects(
                    HWND::default(),
                    (SHCONTF_FOLDERS.0 | SHCONTF_NONFOLDERS.0) as u32,
                    &mut child_enumerator,
                )
            }
            .ok()
            .unwrap();
            let child_enumerator = child_enumerator.unwrap();
            let mut target_children = [std::ptr::null_mut()];
            let mut target_fetched = 0;
            unsafe { child_enumerator.Next(&mut target_children, Some(&mut target_fetched)) }
                .ok()
                .unwrap();
            assert_eq!(target_fetched, 1);

            let mut name = STRRET::default();
            unsafe { target_folder.GetDisplayNameOf(target_children[0], SHGDN_NORMAL, &mut name) }
                .unwrap();
            let mut name_buffer = [0u16; 260];
            unsafe { StrRetToBufW(&mut name, Some(target_children[0]), &mut name_buffer) }.unwrap();
            let end = name_buffer.iter().position(|ch| *ch == 0).unwrap();
            assert_eq!(String::from_utf16_lossy(&name_buffer[..end]), "payload.txt");

            unsafe {
                ILFree(Some(child));
                ILFree(Some(target_children[0]));
            }
            drop(child_enumerator);
            drop(target_folder);
            drop(enumerator);
            drop(folder);
            drop(persist);
            drop(link);
            fs::remove_dir_all(fixture).unwrap();
        })
        .join()
        .unwrap();
    }
}
