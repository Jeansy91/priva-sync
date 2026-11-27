//! COM `IExplorerCommand` shell extension (ADR-0021).
//!
//! One top-level "PrivaSync" command whose sub-commands are built dynamically
//! from the daemon's menu tree for the right-clicked path. Leaf commands invoke
//! a verb over the pipe; `Submenu` nodes become nested `IExplorerCommand`s.
//!
//! The root learns its path from the `IShellItemArray` passed to the
//! title/state calls (Explorer does not pass a selection to `EnumSubCommands`),
//! so it caches the resolved menu on first sight.
//!
//! NB: COM method signatures track the `windows` 0.61 `#[implement]` codegen.
//! Only exercised inside `explorer.exe` — verify by registering the DLL and
//! right-clicking a synced file.

#![allow(non_snake_case)]

use std::panic::{catch_unwind, AssertUnwindSafe};
use std::sync::Mutex;

use windows::core::{implement, Result, GUID, PWSTR, BOOL, HRESULT};
use windows::Win32::Foundation::{E_FAIL, E_NOTIMPL, S_FALSE, S_OK};
use windows::Win32::System::Com::{CoTaskMemAlloc, CoTaskMemFree, IBindCtx};
use windows::Win32::UI::Shell::{
    IEnumExplorerCommand, IEnumExplorerCommand_Impl, IExplorerCommand, IExplorerCommand_Impl,
    IShellItemArray, SIGDN_FILESYSPATH,
};

use crate::proto::{self, MenuNode, MenuVerb};

/// CLSID identifying our command handler in the registry.
pub const CLSID_PRIVASYNC_MENU: GUID = GUID::from_u128(0x7b2e4f10_1c3a_4d8e_9a21_5f0b6c7d8e90);

// EXPCMDSTATE / EXPCMDFLAGS values.
const ECS_ENABLED: u32 = 0x0;
const ECS_HIDDEN: u32 = 0x8;
const ECF_DEFAULT: u32 = 0x0;
const ECF_HASSUBCOMMANDS: u32 = 0x1;

/// Run a COM method body, turning any panic into an error HRESULT. A Rust
/// panic unwinding across the COM/FFI boundary is undefined behaviour and
/// crashes the host (Explorer) — every `IExplorerCommand` method must be
/// wrapped so a bug degrades to "no menu" instead of a process crash.
fn guard<T>(f: impl FnOnce() -> Result<T>) -> Result<T> {
    catch_unwind(AssertUnwindSafe(f)).unwrap_or_else(|_| Err(E_FAIL.into()))
}

/// Same as [`guard`] but for the raw-`HRESULT` enumerator methods.
fn guard_hr(f: impl FnOnce() -> HRESULT) -> HRESULT {
    catch_unwind(AssertUnwindSafe(f)).unwrap_or(E_FAIL)
}

/// Allocate a wide, NUL-terminated copy of `s` with `CoTaskMemAlloc`, as COM
/// out-string params require (the caller frees it).
fn co_pwstr(s: &str) -> Result<PWSTR> {
    let mut wide: Vec<u16> = s.encode_utf16().collect();
    wide.push(0);
    unsafe {
        let p = CoTaskMemAlloc(wide.len() * std::mem::size_of::<u16>()) as *mut u16;
        if p.is_null() {
            return Err(E_FAIL.into());
        }
        std::ptr::copy_nonoverlapping(wide.as_ptr(), p, wide.len());
        Ok(PWSTR(p))
    }
}

/// Read a filesystem path only when the selection contains one item.
fn selected_path(items: Option<&IShellItemArray>) -> Option<String> {
    let arr = items?;
    unsafe {
        if arr.GetCount().ok()? != 1 {
            return None;
        }
        let item = arr.GetItemAt(0).ok()?;
        let name = item.GetDisplayName(SIGDN_FILESYSPATH).ok()?;
        let s = name.to_string().ok();
        CoTaskMemFree(Some(name.0 as *const _));
        s
    }
}

/// Resolved state of a node: the path it acts on and its child menu items.
#[derive(Default)]
struct NodeCache {
    path:     String,
    children: Vec<MenuNode>,
    resolved: bool,
}

/// A single context-menu node: either a leaf that invokes `verb`, or a
/// container (root / submenu) whose children come from the daemon tree.
#[implement(IExplorerCommand)]
pub struct MenuCommand {
    title: String,
    verb:  Option<MenuVerb>,
    root:  bool,
    cache: Mutex<NodeCache>,
}

impl MenuCommand {
    /// Top-level entry; its menu is resolved lazily from the selection.
    pub fn root() -> MenuCommand {
        crate::retain_com_object();
        MenuCommand {
            title: "PrivaSync".into(),
            verb:  None,
            root:  true,
            cache: Mutex::new(NodeCache::default()),
        }
    }

    /// Build a child command from a localized daemon node (leaf or submenu).
    fn from_node(node: &MenuNode, path: &str) -> MenuCommand {
        crate::retain_com_object();
        MenuCommand {
            title: node.label.clone(),
            verb:  node.verb,
            root:  false,
            cache: Mutex::new(NodeCache {
                path:     path.into(),
                children: node.children.clone(),
                resolved: true,
            }),
        }
    }

    /// Resolve a single selected path. Multi-selection deliberately hides the menu.
    fn ensure(&self, items: Option<&IShellItemArray>) {
        let Ok(mut c) = self.cache.lock() else { return };
        if !self.root {
            return;
        }
        let Some(path) = selected_path(items) else {
            c.path.clear();
            c.children.clear();
            c.resolved = true;
            return;
        };
        if c.resolved && c.path == path {
            return;
        }
        c.children = proto::query_menu(&path);
        c.path = path;
        c.resolved = true;
    }

    /// File stem of this node's icon in the `menu-icons` folder next to the DLL.
    fn icon_name(&self) -> &'static str {
        match self.verb {
            None => "root",
            Some(MenuVerb::OpenWeb) => "open-web",
            Some(MenuVerb::ShareWithUser) => "share-user",
            Some(MenuVerb::ManageShares) => "manage-shares",
            Some(MenuVerb::CreatePublicLink | MenuVerb::CopyPublicLink) => "link",
            Some(MenuVerb::ExcludeFromSync) => "exclude",
            Some(MenuVerb::IncludeInSync) => "include",
            Some(MenuVerb::ResolveConflict) => "conflict",
            Some(MenuVerb::KeepOnDevice) => "keep",
            Some(MenuVerb::FreeUpSpace) => "free-space",
            Some(MenuVerb::Synchronize) => "sync",
        }
    }

    fn icon_location(&self) -> Result<PWSTR> {
        let dll = crate::exports::dll_path();
        let dir = std::path::Path::new(&dll).parent().ok_or(E_FAIL)?;
        let icon = dir.join("menu-icons").join(format!("{}.ico", self.icon_name()));
        co_pwstr(&icon.to_string_lossy())
    }
}

/// Lift nested submenu children into the parent list. The Windows 11 modern
/// menu renders a single submenu level only; nested containers would show up
/// without a chevron and be unusable.
fn flatten(nodes: &[MenuNode]) -> Vec<&MenuNode> {
    nodes
        .iter()
        .flat_map(|n| if n.verb.is_none() { flatten(&n.children) } else { vec![n] })
        .collect()
}

impl Drop for MenuCommand {
    fn drop(&mut self) {
        crate::release_com_object();
    }
}

impl IExplorerCommand_Impl for MenuCommand_Impl {
    fn GetTitle(&self, items: windows_core::Ref<'_, IShellItemArray>) -> Result<PWSTR> {
        guard(|| {
            self.ensure(items.as_ref());
            co_pwstr(&self.title)
        })
    }

    fn GetIcon(&self, _items: windows_core::Ref<'_, IShellItemArray>) -> Result<PWSTR> {
        guard(|| self.icon_location())
    }

    fn GetToolTip(&self, _items: windows_core::Ref<'_, IShellItemArray>) -> Result<PWSTR> {
        Err(E_NOTIMPL.into())
    }

    fn GetCanonicalName(&self) -> Result<GUID> {
        Ok(GUID::zeroed())
    }

    fn GetState(&self, items: windows_core::Ref<'_, IShellItemArray>, _slow: BOOL) -> Result<u32> {
        guard(|| {
            self.ensure(items.as_ref());
            // A container with no children (path not actionable) hides itself.
            let empty = self.cache.lock().map(|c| c.children.is_empty()).unwrap_or(true);
            if self.verb.is_none() && empty {
                Ok(ECS_HIDDEN)
            } else {
                Ok(ECS_ENABLED)
            }
        })
    }

    fn Invoke(
        &self,
        items: windows_core::Ref<'_, IShellItemArray>,
        _ctx: windows_core::Ref<'_, IBindCtx>,
    ) -> Result<()> {
        guard(|| {
            let Some(verb) = self.verb else { return Ok(()) }; // container: no-op
            let fallback = self.cache.lock().map(|c| c.path.clone()).unwrap_or_default();
            let path = selected_path(items.as_ref()).unwrap_or(fallback);
            if proto::invoke(verb, &path) { Ok(()) } else { Err(E_FAIL.into()) }
        })
    }

    fn GetFlags(&self) -> Result<u32> {
        // Containers (root + submenus) declare sub-commands.
        if self.verb.is_none() { Ok(ECF_HASSUBCOMMANDS) } else { Ok(ECF_DEFAULT) }
    }

    fn EnumSubCommands(&self) -> Result<IEnumExplorerCommand> {
        guard(|| {
            let (path, children) = match self.cache.lock() {
                Ok(c) => (c.path.clone(), c.children.clone()),
                Err(_) => (String::new(), Vec::new()),
            };
            let commands: Vec<IExplorerCommand> = flatten(&children)
                .into_iter()
                .map(|node| MenuCommand::from_node(node, &path).into())
                .collect();
            Ok(SubCommandEnum::new(commands).into())
        })
    }
}

/// `IEnumExplorerCommand` over a fixed list of child commands.
#[implement(IEnumExplorerCommand)]
pub struct SubCommandEnum {
    commands: Vec<IExplorerCommand>,
    cursor:   Mutex<usize>,
}

impl SubCommandEnum {
    fn new(commands: Vec<IExplorerCommand>) -> SubCommandEnum {
        SubCommandEnum { commands, cursor: Mutex::new(0) }
    }
}

impl IEnumExplorerCommand_Impl for SubCommandEnum_Impl {
    fn Next(&self, celt: u32, out: *mut Option<IExplorerCommand>, fetched: *mut u32) -> HRESULT {
        guard_hr(|| {
            let Ok(mut idx) = self.cursor.lock() else { return E_FAIL };
            let mut n = 0u32;
            while n < celt && *idx < self.commands.len() {
                unsafe { *out.add(n as usize) = Some(self.commands[*idx].clone()) };
                *idx += 1;
                n += 1;
            }
            if !fetched.is_null() {
                unsafe { *fetched = n };
            }
            if n == celt { S_OK } else { S_FALSE }
        })
    }

    fn Skip(&self, celt: u32) -> Result<()> {
        guard(|| {
            if let Ok(mut idx) = self.cursor.lock() {
                *idx += celt as usize;
            }
            Ok(())
        })
    }

    fn Reset(&self) -> Result<()> {
        guard(|| {
            if let Ok(mut idx) = self.cursor.lock() {
                *idx = 0;
            }
            Ok(())
        })
    }

    fn Clone(&self) -> Result<IEnumExplorerCommand> {
        guard(|| Ok(SubCommandEnum::new(self.commands.clone()).into()))
    }
}
