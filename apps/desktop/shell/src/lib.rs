//! `privasync-shell` — in-proc COM shell extension hosting the Explorer
//! context menu (ADR-0021). Exposes the standard COM DLL entry points plus
//! `DllRegisterServer`/`DllUnregisterServer` for self-registration via
//! `regsvr32`.

#![allow(non_snake_case)]

#[cfg(target_os = "windows")]
use std::sync::atomic::{AtomicUsize, Ordering};

#[cfg(target_os = "windows")]
static ACTIVE_COM_OBJECTS: AtomicUsize = AtomicUsize::new(0);
#[cfg(target_os = "windows")]
static SERVER_LOCKS: AtomicUsize = AtomicUsize::new(0);

#[cfg(target_os = "windows")]
pub(crate) fn retain_com_object() {
    ACTIVE_COM_OBJECTS.fetch_add(1, Ordering::Relaxed);
}

#[cfg(target_os = "windows")]
pub(crate) fn release_com_object() {
    ACTIVE_COM_OBJECTS.fetch_sub(1, Ordering::Release);
}

#[cfg(target_os = "windows")]
mod com;
#[cfg(target_os = "windows")]
mod namespace;
mod proto;

#[cfg(target_os = "windows")]
mod exports {
    use std::ffi::c_void;
    use std::sync::atomic::{AtomicIsize, Ordering};

    use windows::core::{Interface, BOOL, GUID, HRESULT, PCWSTR};
    use windows::Win32::Foundation::{
        CLASS_E_CLASSNOTAVAILABLE, E_FAIL, HMODULE, S_FALSE, S_OK,
    };
    use windows::Win32::System::Com::{IClassFactory, IClassFactory_Impl};
    use windows::Win32::System::LibraryLoader::GetModuleFileNameW;
    use windows::Win32::System::Registry::{
        RegCloseKey, RegCreateKeyExW, RegDeleteTreeW, RegDeleteValueW, RegOpenKeyExW,
        RegSetValueExW, HKEY, HKEY_CURRENT_USER, KEY_WRITE, REG_OPTION_NON_VOLATILE, REG_SZ,
    };
    use windows::core::implement;

    use crate::com::{MenuCommand, CLSID_PRIVASYNC_MENU};
    use crate::namespace::{NamespaceFolder, CLSID_PRIVASYNC_NAMESPACE};

    /// This DLL's module handle, captured in `DllMain`, used to resolve our own
    /// path during registration.
    static MODULE: AtomicIsize = AtomicIsize::new(0);

    #[no_mangle]
    extern "system" fn DllMain(hinst: HMODULE, reason: u32, _reserved: *mut c_void) -> BOOL {
        const DLL_PROCESS_ATTACH: u32 = 1;
        if reason == DLL_PROCESS_ATTACH {
            MODULE.store(hinst.0 as isize, Ordering::Relaxed);
        }
        BOOL(1)
    }

    /// COM class factory that creates the registered PrivaSync shell object.
    #[implement(IClassFactory)]
    struct Factory {
        clsid: GUID,
    }

    impl Drop for Factory {
        fn drop(&mut self) {
            super::release_com_object();
        }
    }

    impl IClassFactory_Impl for Factory_Impl {
        fn CreateInstance(
            &self,
            _outer: windows_core::Ref<'_, windows::core::IUnknown>,
            riid: *const GUID,
            ppv: *mut *mut c_void,
        ) -> windows::core::Result<()> {
            let object: windows::core::IUnknown = if self.clsid == CLSID_PRIVASYNC_MENU {
                let command: windows::Win32::UI::Shell::IExplorerCommand =
                    MenuCommand::root().into();
                command.into()
            } else {
                let folder: windows::Win32::UI::Shell::IShellFolder =
                    NamespaceFolder::create()?;
                folder.into()
            };
            unsafe { object.query(riid, ppv).ok() }
        }

        fn LockServer(&self, _lock: BOOL) -> windows::core::Result<()> {
            if _lock.as_bool() {
                super::SERVER_LOCKS.fetch_add(1, Ordering::Relaxed);
            } else {
                let _ = super::SERVER_LOCKS.fetch_update(
                    Ordering::Relaxed,
                    Ordering::Relaxed,
                    |locks| Some(locks.saturating_sub(1)),
                );
            }
            Ok(())
        }
    }

    #[no_mangle]
    extern "system" fn DllGetClassObject(
        rclsid: *const GUID,
        riid: *const GUID,
        ppv: *mut *mut c_void,
    ) -> HRESULT {
        unsafe {
            if *rclsid != CLSID_PRIVASYNC_MENU && *rclsid != CLSID_PRIVASYNC_NAMESPACE {
                return CLASS_E_CLASSNOTAVAILABLE;
            }
            super::retain_com_object();
            let factory: IClassFactory = Factory { clsid: *rclsid }.into();
            factory.query(riid, ppv)
        }
    }

    #[no_mangle]
    extern "system" fn DllCanUnloadNow() -> HRESULT {
        if super::ACTIVE_COM_OBJECTS.load(Ordering::Acquire) == 0
            && super::SERVER_LOCKS.load(Ordering::Acquire) == 0
        {
            S_OK
        } else {
            S_FALSE
        }
    }

    pub(crate) fn dll_path() -> String {
        let hmodule = HMODULE(MODULE.load(Ordering::Relaxed) as *mut c_void);
        let mut buf = [0u16; 1024];
        let len = unsafe { GetModuleFileNameW(Some(hmodule), &mut buf) } as usize;
        String::from_utf16_lossy(&buf[..len])
    }

    /// Create (or open) `HKCU\subkey` and set a string value. `name = None`
    /// writes the key's default value.
    fn set_value(subkey: &str, name: Option<&str>, value: &str) -> windows::core::Result<()> {
        let sub: Vec<u16> = subkey.encode_utf16().chain(std::iter::once(0)).collect();
        let mut key = HKEY::default();
        unsafe {
            RegCreateKeyExW(
                HKEY_CURRENT_USER,
                PCWSTR(sub.as_ptr()),
                Some(0),
                PCWSTR::null(),
                REG_OPTION_NON_VOLATILE,
                KEY_WRITE,
                None,
                &mut key,
                None,
            )
            .ok()?;

            let data: Vec<u16> = value.encode_utf16().chain(std::iter::once(0)).collect();
            let bytes = std::slice::from_raw_parts(data.as_ptr() as *const u8, data.len() * 2);
            let name_w: Option<Vec<u16>> =
                name.map(|n| n.encode_utf16().chain(std::iter::once(0)).collect());
            let name_ptr = name_w.as_ref().map(|w| PCWSTR(w.as_ptr())).unwrap_or(PCWSTR::null());

            let res = RegSetValueExW(key, name_ptr, Some(0), REG_SZ, Some(bytes));
            let _ = RegCloseKey(key);
            res.ok()
        }
    }

    /// GUID in registry brace form, e.g. `{7B2E4F10-...}`.
    fn clsid_braced() -> String {
        format!("{{{:?}}}", CLSID_PRIVASYNC_MENU).to_uppercase()
    }

    fn register() -> windows::core::Result<()> {
        let clsid = clsid_braced();
        let dll = dll_path();

        // 1) The COM server itself.
        set_value(&format!("Software\\Classes\\CLSID\\{clsid}"), None, "PrivaSync")?;
        let inproc = format!("Software\\Classes\\CLSID\\{clsid}\\InprocServer32");
        set_value(&inproc, None, &dll)?;
        set_value(&inproc, Some("ThreadingModel"), "Apartment")?;

        // 2) Attach as an IExplorerCommand handler for files and folders. The
        //    handler hides itself for paths outside a sync root, so a global
        //    association is fine.
        for class in ["*", "Directory"] {
            let verb = format!("Software\\Classes\\{class}\\shell\\PrivaSync");
            set_value(&verb, None, "PrivaSync")?;
            set_value(&verb, Some("MUIVerb"), "PrivaSync")?;
            // NB: no `SubCommands` value — sub-commands are provided dynamically
            // by the handler's IEnumExplorerCommand, and a (even empty)
            // SubCommands string makes Explorer expect static registry verbs.
            set_value(&verb, Some("ExplorerCommandHandler"), &clsid)?;
        }
        Ok(())
    }

    fn unregister() -> windows::core::Result<()> {
        let clsid = clsid_braced();
        let trees = [
            format!("Software\\Classes\\CLSID\\{clsid}"),
            "Software\\Classes\\CLSID\\{A95133A0-8C8E-4BB3-9D25-37DC3B8F56B4}".to_string(),
            "Software\\Classes\\*\\shell\\PrivaSync".to_string(),
            "Software\\Classes\\Directory\\shell\\PrivaSync".to_string(),
            "Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\Desktop\\NameSpace\\{A95133A0-8C8E-4BB3-9D25-37DC3B8F56B4}".to_string(),
        ];
        for t in trees {
            let w: Vec<u16> = t.encode_utf16().chain(std::iter::once(0)).collect();
            unsafe {
                let _ = RegDeleteTreeW(HKEY_CURRENT_USER, PCWSTR(w.as_ptr()));
            }
        }
        for key in [
            "Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\HideDesktopIcons\\NewStartPanel",
            "Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\HideDesktopIcons\\ClassicStartMenu",
        ] {
            let key_w: Vec<u16> = key.encode_utf16().chain(std::iter::once(0)).collect();
            let value_w: Vec<u16> = "{A95133A0-8C8E-4BB3-9D25-37DC3B8F56B4}"
                .encode_utf16()
                .chain(std::iter::once(0))
                .collect();
            let mut registry_key = HKEY::default();
            unsafe {
                if RegOpenKeyExW(
                    HKEY_CURRENT_USER,
                    PCWSTR(key_w.as_ptr()),
                    Some(0),
                    KEY_WRITE,
                    &mut registry_key,
                )
                .is_ok()
                {
                    let _ = RegDeleteValueW(registry_key, PCWSTR(value_w.as_ptr()));
                    let _ = RegCloseKey(registry_key);
                }
            }
        }
        Ok(())
    }

    #[no_mangle]
    extern "system" fn DllRegisterServer() -> HRESULT {
        match register() {
            Ok(()) => S_OK,
            Err(e) => e.code(),
        }
    }

    #[no_mangle]
    extern "system" fn DllUnregisterServer() -> HRESULT {
        match unregister() {
            Ok(()) => S_OK,
            Err(_) => E_FAIL,
        }
    }
}
