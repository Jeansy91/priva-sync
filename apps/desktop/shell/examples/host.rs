//! Standalone debug host for the context-menu COM extension (ADR-0021).
//!
//! Loads `privasync_shell.dll` directly (no registry), instantiates the root
//! `IExplorerCommand`, and exercises every method against a path — exactly what
//! Explorer does when it builds the menu, but in a process we can crash and
//! debug without taking down Explorer.
//!
//! Run: `cargo run --example host -- "C:\\path\\to\\file"`
//! Each step prints *before* it runs, so the last line before an exit pinpoints
//! the faulting call.

#![cfg(target_os = "windows")]

use std::ffi::c_void;

use windows::core::{s, Interface, GUID, HSTRING};
use windows::Win32::Foundation::HMODULE;
use windows::Win32::System::Com::{
    CoInitializeEx, IClassFactory, COINIT_APARTMENTTHREADED,
};
use windows::Win32::System::LibraryLoader::{GetProcAddress, LoadLibraryW};
use windows::Win32::UI::Shell::{
    IEnumExplorerCommand, IExplorerCommand, IShellItem, IShellItemArray,
    SHCreateItemFromParsingName, SHCreateShellItemArrayFromShellItem,
};

const CLSID_PRIVASYNC_MENU: GUID = GUID::from_u128(0x7b2e4f10_1c3a_4d8e_9a21_5f0b6c7d8e90);

type DllGetClassObjectFn =
    unsafe extern "system" fn(*const GUID, *const GUID, *mut *mut c_void) -> windows::core::HRESULT;

fn main() -> windows::core::Result<()> {
    let dll = std::env::var("PRIVASYNC_SHELL_DLL").unwrap_or_else(|_| {
        r"E:\Workspace\priva-sync\apps\desktop\shell\target\release\privasync_shell.dll".into()
    });
    let path = std::env::args().nth(1).unwrap_or_else(|| {
        eprintln!("usage: host <file-or-folder-path>");
        std::process::exit(2)
    });

    unsafe {
        let _ = CoInitializeEx(None, COINIT_APARTMENTTHREADED);

        println!("[1] LoadLibrary {dll}");
        let module: HMODULE = LoadLibraryW(&HSTRING::from(dll))?;

        println!("[2] GetProcAddress DllGetClassObject");
        let proc = GetProcAddress(module, s!("DllGetClassObject"))
            .expect("DllGetClassObject not exported");
        let dgco: DllGetClassObjectFn = std::mem::transmute(proc);

        println!("[3] DllGetClassObject -> IClassFactory");
        let mut factory_ptr: *mut c_void = std::ptr::null_mut();
        dgco(&CLSID_PRIVASYNC_MENU, &IClassFactory::IID, &mut factory_ptr).ok()?;
        let factory: IClassFactory = IClassFactory::from_raw(factory_ptr);

        println!("[4] CreateInstance -> IExplorerCommand");
        let cmd: IExplorerCommand = factory.CreateInstance(None)?;

        println!("[5] build IShellItemArray for {path}");
        let item: IShellItem = SHCreateItemFromParsingName(&HSTRING::from(path), None)?;
        let array: IShellItemArray = SHCreateShellItemArrayFromShellItem(&item)?;

        println!("[6] GetFlags");
        let flags = cmd.GetFlags()?;
        println!("    flags = 0x{flags:x}");

        println!("[7] GetState");
        let state = cmd.GetState(&array, false)?;
        println!("    state = 0x{state:x}");

        println!("[8] GetTitle");
        let title = cmd.GetTitle(&array)?;
        println!("    title = {}", title.to_string()?);

        // Explorer also calls these during menu build — they must not AV even
        // when we decline to provide a value (E_NOTIMPL).
        println!("[8b] GetIcon");
        match cmd.GetIcon(&array) {
            Ok(p) => println!("    icon = {:?}", p.to_string()),
            Err(e) => println!("    icon = (none) 0x{:x}", e.code().0),
        }
        println!("[8c] GetToolTip");
        let _ = cmd.GetToolTip(&array);
        println!("[8d] GetCanonicalName");
        let _ = cmd.GetCanonicalName();

        println!("[9] EnumSubCommands");
        let en: IEnumExplorerCommand = cmd.EnumSubCommands()?;

        println!("[10] iterate sub-commands");
        loop {
            let mut buf: [Option<IExplorerCommand>; 1] = [None];
            let mut fetched = 0u32;
            let _ = en.Next(&mut buf, Some(std::ptr::addr_of_mut!(fetched)));
            if fetched == 0 {
                break;
            }
            if let Some(child) = &buf[0] {
                let t = child.GetTitle(&array).and_then(|p| p.to_string().map_err(Into::into));
                let flags = child.GetFlags().unwrap_or(0);
                println!("    - {t:?} (flags=0x{flags:x})");
                // Recurse one level into submenus, as Explorer does on hover.
                if flags & 0x1 != 0 {
                    if let Ok(sub) = child.EnumSubCommands() {
                        loop {
                            let mut b2: [Option<IExplorerCommand>; 1] = [None];
                            let mut f2 = 0u32;
                            let _ = sub.Next(&mut b2, Some(std::ptr::addr_of_mut!(f2)));
                            if f2 == 0 {
                                break;
                            }
                            if let Some(gc) = &b2[0] {
                                let gt = gc.GetTitle(&array).and_then(|p| p.to_string().map_err(Into::into));
                                println!("        · {:?}", gt);
                            }
                        }
                    }
                }
            }
        }

        println!("[done] no crash");
    }
    Ok(())
}
