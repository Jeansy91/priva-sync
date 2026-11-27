//! Explorer context-menu logic (ADR-0021).
//!
//! Platform-agnostic core: classify a path into [`PathMenuState`], map that
//! state to the visible menu (a tree of [`MenuItem`]s), and (later) dispatch a
//! verb. The Windows shell extension and the named-pipe server are thin layers
//! over this module — all menu logic lives here so there is a single,
//! unit-tested source of truth (the desktop pendant to the web
//! `buildEntryMenuItems`).

use std::path::{Component, Path};

/// A sync root reduced to what the path locator needs — decoupled from the DB
/// row so the matching logic stays pure and unit-testable.
#[derive(Debug, Clone)]
pub struct RootRef {
    pub id:             String,
    pub local_path:     String,
    pub read_only:      bool,
    pub is_mount:       bool,
    /// Server-side root id (owned roots) or share id (mounts).
    pub server_root_id: String,
    /// Display name of the sync root. The web URL addresses owned roots by
    /// name (`/files/<name>/…`), mounts by share id (`/files/_m/<id>/…`).
    pub name:           String,
}

/// Build the web-UI deep link for an entry, mirroring the web app's
/// `parseFilesUrl`: own roots are `/files/<name>/<segs>`, Shared Mounts
/// `/files/_m/<shareId>/<segs>` (ADR-0010). For a file we navigate to its
/// parent folder (the web lists folders, files appear inside). Each path
/// segment is percent-encoded individually, matching the web's
/// `decodeURIComponent` per segment.
pub fn web_files_url(
    web_base:      &str,
    root_label:    &str,
    is_mount:      bool,
    relative_path: &str,
    is_dir:        bool,
) -> String {
    let base = web_base.trim_end_matches('/');
    let mut url = if is_mount {
        format!("{base}/files/_m/{}", urlencoding::encode(root_label))
    } else {
        format!("{base}/files/{}", urlencoding::encode(root_label))
    };

    let mut segs: Vec<&str> = relative_path.split('/').filter(|s| !s.is_empty()).collect();
    // A file is not a navigable folder — open its containing directory.
    if !is_dir {
        segs.pop();
    }
    for seg in segs {
        url.push('/');
        url.push_str(&urlencoding::encode(seg));
    }
    url
}

/// Result of locating a target path inside the known sync roots.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Located {
    /// Index into the `roots` slice passed to [`locate`].
    pub root_index:    usize,
    /// Server-form path relative to the root: leading `/`, forward slashes,
    /// original case (`/docs/report.pdf`). Empty string means the root itself.
    pub relative_path: String,
}

/// Decompose a path into `(original, match_key)` components. The key is
/// case-folded on Windows so matching is case-insensitive, while the original
/// preserves case for the (case-sensitive) server relative path.
fn components_keyed(p: &Path) -> Vec<(String, String)> {
    p.components()
        .map(|c| {
            let orig = match c {
                Component::Prefix(pre) => pre.as_os_str().to_string_lossy().into_owned(),
                Component::RootDir => "\\".to_string(),
                Component::CurDir => ".".to_string(),
                Component::ParentDir => "..".to_string(),
                Component::Normal(s) => s.to_string_lossy().into_owned(),
            };
            let key = if cfg!(target_os = "windows") { orig.to_lowercase() } else { orig.clone() };
            (orig, key)
        })
        .collect()
}

/// Find which sync root contains `target` and the relative path within it.
/// Matching is component-wise (so `C:\Sync` never matches `C:\SyncOther`); when
/// roots nest, the deepest match wins. Returns `None` if the path is outside
/// every root.
pub fn locate(roots: &[RootRef], target: &Path) -> Option<Located> {
    let t = components_keyed(target);
    let mut best: Option<(usize, usize)> = None; // (root_index, root component count)
    for (i, r) in roots.iter().enumerate() {
        let rc = components_keyed(Path::new(&r.local_path));
        if rc.is_empty() || rc.len() > t.len() {
            continue;
        }
        let is_prefix = rc.iter().zip(t.iter()).all(|(a, b)| a.1 == b.1);
        if is_prefix && best.is_none_or(|(_, len)| rc.len() > len) {
            best = Some((i, rc.len()));
        }
    }
    let (idx, rlen) = best?;
    let rel: Vec<&str> = t[rlen..].iter().map(|(o, _)| o.as_str()).collect();
    let relative_path = if rel.is_empty() { String::new() } else { format!("/{}", rel.join("/")) };
    Some(Located { root_index: idx, relative_path })
}

/// Observable sync/share state of a single path, as the menu sees it. Carries
/// no UI strings — the shell layer localizes from the chosen verbs.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct PathMenuState {
    /// Path lies inside a registered sync root (owned root or activated mount).
    pub in_sync_root: bool,
    /// Path lives inside an incoming **Shared Mount** (recipient side). Mounts
    /// cannot be re-shared by the recipient (ADR-0012), so sharing is hidden.
    pub is_mount: bool,
    /// A public share link already exists for this entry. Flips the public-link
    /// action from "create" (in the submenu) to a quick top-level "copy".
    pub has_public_link: bool,
    /// This entry is currently matched by an exclusion pattern, so it is
    /// not uploaded. Flips the exclude toggle to "include again".
    pub excluded: bool,
    /// The owning sync root is read-only (a non-writable mount). Read-only roots
    /// never upload, so the exclude toggle has no effect and is hidden.
    pub read_only: bool,
    /// At least one share grant exists for this entry → offer "manage shares".
    pub is_shared: bool,
    /// A conflict copy exists for this entry → offer the conflict resolver
    /// (ADR-0016).
    pub in_conflict: bool,
    /// CF pin state: the entry is pinned ("always keep on this device"). Flips
    /// the hydration toggle between pin and "free up space".
    pub pinned: bool,
}

/// A verb the shell extension can render and invoke. The variant is the contract
/// over the pipe; the shell layer maps it to a localized label.
#[derive(Debug, Clone, Copy, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub enum MenuVerb {
    /// Open this entry in the web UI (browser deep-link).
    OpenWeb,
    /// Open the share dialog to grant access to another user (ADR-0015).
    ShareWithUser,
    /// Create a public share link for this entry (ADR-0015 dual-link).
    CreatePublicLink,
    /// Copy the existing public share link to the clipboard.
    CopyPublicLink,
    /// Add a path-anchored exclusion pattern so this entry stops syncing.
    ExcludeFromSync,
    /// Remove the path's exclusion pattern so it syncs again.
    IncludeInSync,
    /// Open the share-grants management view for this already-shared entry.
    ManageShares,
    /// Jump to the conflict resolver for this entry (ADR-0016).
    ResolveConflict,
    /// Pin the entry so its content always stays on this device (CF hydrate).
    KeepOnDevice,
    /// Unpin and dehydrate the entry to reclaim local space (CF dehydrate).
    FreeUpSpace,
    /// Turn this folder (outside any sync root) into a new sync root.
    Synchronize,
}

/// A grouping node rendered as an Explorer submenu (via `IEnumSubCommands`).
#[derive(Debug, Clone, Copy, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub enum MenuGroup {
    /// "Teilen" — collects the share-related verbs.
    Share,
}

/// One node of the context menu: either a directly invokable verb or a submenu
/// grouping further nodes. This is the internal, label-free tree produced by the
/// pure [`items`]; the daemon turns it into the localized [`MenuNode`] wire tree.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum MenuItem {
    Action(MenuVerb),
    Submenu { group: MenuGroup, items: Vec<MenuItem> },
}

/// A localized menu node as sent to the shell extension: the verb to invoke
/// (absent for a submenu), the display label, and any children. The shell layer
/// renders `label` verbatim — all i18n happens daemon-side against the shared
/// rust-i18n catalog, so the extension carries no strings of its own.
#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub struct MenuNode {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub verb:     Option<MenuVerb>,
    pub label:    String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub children: Vec<MenuNode>,
}

impl MenuVerb {
    /// rust-i18n key for this verb's label.
    pub fn i18n_key(self) -> &'static str {
        match self {
            MenuVerb::OpenWeb          => "contextmenu.openWeb",
            MenuVerb::ShareWithUser    => "contextmenu.shareWithUser",
            MenuVerb::CreatePublicLink => "contextmenu.createPublicLink",
            MenuVerb::CopyPublicLink   => "contextmenu.copyPublicLink",
            MenuVerb::ExcludeFromSync  => "contextmenu.excludeFromSync",
            MenuVerb::IncludeInSync    => "contextmenu.includeInSync",
            MenuVerb::ManageShares     => "contextmenu.manageShares",
            MenuVerb::ResolveConflict  => "contextmenu.resolveConflict",
            MenuVerb::KeepOnDevice     => "contextmenu.keepOnDevice",
            MenuVerb::FreeUpSpace      => "contextmenu.freeUpSpace",
            MenuVerb::Synchronize      => "contextmenu.synchronize",
        }
    }
}

impl MenuGroup {
    /// rust-i18n key for this submenu's label.
    pub fn i18n_key(self) -> &'static str {
        match self {
            MenuGroup::Share => "contextmenu.group.share",
        }
    }
}

/// Turn the pure verb tree into a localized [`MenuNode`] tree. `t` resolves an
/// i18n key to its label (the daemon passes a rust-i18n lookup for the user's
/// language); kept as a plain closure so this stays unit-testable.
pub fn decorate(items: &[MenuItem], t: &dyn Fn(&str) -> String) -> Vec<MenuNode> {
    items
        .iter()
        .map(|item| match item {
            MenuItem::Action(verb) => MenuNode {
                verb:     Some(*verb),
                label:    t(verb.i18n_key()),
                children: Vec::new(),
            },
            MenuItem::Submenu { group, items } => MenuNode {
                verb:     None,
                label:    t(group.i18n_key()),
                children: decorate(items, t),
            },
        })
        .collect()
}

/// Wire protocol between the shell extension (client) and the daemon (server),
/// one JSON object per line over the named pipe (ADR-0021). All menu logic
/// lives server-side: the client asks for the rendered menu tree and reports
/// back which verb the user picked.
#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
#[serde(tag = "op", rename_all = "snake_case")]
pub enum Request {
    /// "What should the menu for this path look like?"
    Menu { path: String },
    /// "The user invoked this verb on this path."
    Invoke { verb: MenuVerb, path: String },
}

/// Reply to a [`Request`].
#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
#[serde(tag = "ok", rename_all = "snake_case")]
pub enum Response {
    /// The rendered, localized menu tree for a `Menu` request.
    Menu { items: Vec<MenuNode> },
    /// An `Invoke` request completed successfully.
    Done,
    /// Any request failed; `error` is a developer-facing message.
    Error { error: String },
}

/// The daemon-side capability the protocol drives. Kept as a trait so the pure
/// [`handle`] dispatcher can be tested against a fake, without a pipe or DB.
pub trait MenuBackend {
    /// Compute the localized menu tree for a path (classify state, [`items`],
    /// then [`decorate`]).
    fn menu_for(&self, path: &Path) -> Vec<MenuNode>;
    /// Perform the side effect for a verb on a path.
    fn invoke(&self, verb: MenuVerb, path: &Path) -> Result<(), String>;
}

/// Pure request→response dispatch. The pipe server only frames NDJSON around
/// this; all decisions stay here and in the backend.
pub fn handle(req: Request, backend: &dyn MenuBackend) -> Response {
    match req {
        Request::Menu { path } => Response::Menu { items: backend.menu_for(Path::new(&path)) },
        Request::Invoke { verb, path } => match backend.invoke(verb, Path::new(&path)) {
            Ok(()) => Response::Done,
            Err(e) => Response::Error { error: e },
        },
    }
}

/// Pure mapping from path state to the ordered menu tree. This is the heart of
/// the feature and the main test surface — keep it side-effect free.
pub fn items(state: &PathMenuState) -> Vec<MenuItem> {
    let mut out = Vec::new();
    if !state.in_sync_root {
        return out;
    }

    out.push(MenuItem::Action(MenuVerb::OpenWeb));

    // Sharing is owner-only: the recipient of a Shared Mount cannot re-share
    // it (ADR-0012), so the whole "Teilen" submenu is hidden for mounts.
    if !state.is_mount {
        let mut share_items = vec![MenuItem::Action(MenuVerb::ShareWithUser)];
        if state.has_public_link {
            // A link already exists → surface a quick top-level "copy" and drop
            // the "create" item from the submenu.
            out.push(MenuItem::Action(MenuVerb::CopyPublicLink));
        } else {
            share_items.push(MenuItem::Action(MenuVerb::CreatePublicLink));
        }
        if state.is_shared {
            share_items.push(MenuItem::Action(MenuVerb::ManageShares));
        }
        out.push(MenuItem::Submenu { group: MenuGroup::Share, items: share_items });
    }

    // Pin/unpin (CF hydration). Excluded entries are force-pinned and have no
    // server copy (ADR-0020 f), so dehydration is impossible — hide the toggle.
    if !state.excluded {
        out.push(MenuItem::Action(if state.pinned {
            MenuVerb::FreeUpSpace
        } else {
            MenuVerb::KeepOnDevice
        }));
    }

    if state.in_conflict {
        out.push(MenuItem::Action(MenuVerb::ResolveConflict));
    }

    // Exclude toggle bridges to the global exclusion patterns per path. Hidden on
    // read-only roots, which never upload anyway.
    if !state.read_only {
        out.push(MenuItem::Action(if state.excluded {
            MenuVerb::IncludeInSync
        } else {
            MenuVerb::ExcludeFromSync
        }));
    }

    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::Path;

    fn root(local: &str) -> RootRef {
        RootRef {
            id: local.into(),
            local_path: local.into(),
            read_only: false,
            is_mount: false,
            server_root_id: "srv".into(),
            name: "Docs".into(),
        }
    }

    #[test]
    fn web_url_owned_folder_uses_root_name_and_segments() {
        let u = web_files_url("https://web.example.com/", "My Root", false, "/docs/sub", true);
        assert_eq!(u, "https://web.example.com/files/My%20Root/docs/sub");
    }

    #[test]
    fn web_url_owned_file_navigates_to_parent_folder() {
        let u = web_files_url("https://web.example.com", "Root", false, "/docs/report.pdf", false);
        assert_eq!(u, "https://web.example.com/files/Root/docs");
    }

    #[test]
    fn web_url_file_directly_in_root_lands_on_root() {
        let u = web_files_url("https://w", "Root", false, "/a.txt", false);
        assert_eq!(u, "https://w/files/Root");
    }

    #[test]
    fn web_url_root_itself_is_root_listing() {
        let u = web_files_url("https://w", "Root", false, "", true);
        assert_eq!(u, "https://w/files/Root");
    }

    #[test]
    fn web_url_mount_uses_share_id_marker() {
        let u = web_files_url("https://w", "share-123", true, "/x/y", true);
        assert_eq!(u, "https://w/files/_m/share-123/x/y");
    }

    // Backslash paths only parse into segments on Windows; on other targets
    // `\` is an ordinary char, so the relative-path extraction can't work.
    #[cfg(target_os = "windows")]
    #[test]
    fn locates_file_inside_root_with_server_relative_path() {
        let roots = [root(r"C:\Sync")];
        let got = locate(&roots, Path::new(r"C:\Sync\docs\report.pdf")).unwrap();
        assert_eq!(got.root_index, 0);
        assert_eq!(got.relative_path, "/docs/report.pdf");
    }

    #[test]
    fn locating_the_root_itself_yields_empty_relative_path() {
        let roots = [root(r"C:\Sync")];
        let got = locate(&roots, Path::new(r"C:\Sync")).unwrap();
        assert_eq!(got.relative_path, "");
    }

    #[test]
    fn sibling_with_shared_prefix_is_not_a_match() {
        let roots = [root(r"C:\Sync")];
        assert!(locate(&roots, Path::new(r"C:\SyncOther\a.txt")).is_none());
    }

    #[test]
    fn path_outside_every_root_is_unlocated() {
        let roots = [root(r"C:\Sync")];
        assert!(locate(&roots, Path::new(r"D:\Other\a.txt")).is_none());
    }

    #[cfg(target_os = "windows")]
    #[test]
    fn matching_is_case_insensitive_on_windows() {
        let roots = [root(r"C:\Sync")];
        let got = locate(&roots, Path::new(r"c:\sync\A.txt")).unwrap();
        assert_eq!(got.relative_path, "/A.txt"); // original case preserved
    }

    #[cfg(target_os = "windows")]
    #[test]
    fn nested_roots_pick_the_deepest_match() {
        let roots = [root(r"C:\Sync"), root(r"C:\Sync\Inner")];
        let got = locate(&roots, Path::new(r"C:\Sync\Inner\a.txt")).unwrap();
        assert_eq!(got.root_index, 1);
        assert_eq!(got.relative_path, "/a.txt");
    }

    /// Collect every leaf verb in the tree, depth-first, so tests can assert on
    /// presence without caring about nesting.
    fn leaf_verbs(items: &[MenuItem]) -> Vec<MenuVerb> {
        let mut out = Vec::new();
        for item in items {
            match item {
                MenuItem::Action(v) => out.push(*v),
                MenuItem::Submenu { items, .. } => out.extend(leaf_verbs(items)),
            }
        }
        out
    }

    /// Verbs sitting directly at the top level (not nested in any submenu).
    fn top_level_verbs(items: &[MenuItem]) -> Vec<MenuVerb> {
        items.iter().filter_map(|i| match i {
            MenuItem::Action(v) => Some(*v),
            _ => None,
        }).collect()
    }

    fn share_submenu(items: &[MenuItem]) -> Option<&Vec<MenuItem>> {
        items.iter().find_map(|i| match i {
            MenuItem::Submenu { group: MenuGroup::Share, items } => Some(items),
            _ => None,
        })
    }

    #[test]
    fn entry_in_sync_root_offers_open_web() {
        let state = PathMenuState { in_sync_root: true, ..Default::default() };
        assert!(leaf_verbs(&items(&state)).contains(&MenuVerb::OpenWeb));
    }

    #[test]
    fn entry_in_sync_root_offers_share_submenu_with_user_and_public_link() {
        let state = PathMenuState { in_sync_root: true, ..Default::default() };
        let menu = items(&state);
        let share = share_submenu(&menu).expect("Share submenu present");
        let verbs = leaf_verbs(share);
        assert!(verbs.contains(&MenuVerb::ShareWithUser));
        assert!(verbs.contains(&MenuVerb::CreatePublicLink));
    }

    #[test]
    fn without_public_link_submenu_offers_create_and_no_top_level_copy() {
        let state = PathMenuState { in_sync_root: true, ..Default::default() };
        let menu = items(&state);
        let share = share_submenu(&menu).expect("Share submenu present");
        assert!(leaf_verbs(share).contains(&MenuVerb::CreatePublicLink));
        assert!(!top_level_verbs(&menu).contains(&MenuVerb::CopyPublicLink));
    }

    #[test]
    fn with_public_link_offers_top_level_copy_and_no_create() {
        let state = PathMenuState { in_sync_root: true, has_public_link: true, ..Default::default() };
        let menu = items(&state);
        assert!(top_level_verbs(&menu).contains(&MenuVerb::CopyPublicLink));
        assert!(!leaf_verbs(&menu).contains(&MenuVerb::CreatePublicLink));
    }

    #[test]
    fn not_excluded_entry_offers_exclude_and_not_include() {
        let state = PathMenuState { in_sync_root: true, ..Default::default() };
        let verbs = top_level_verbs(&items(&state));
        assert!(verbs.contains(&MenuVerb::ExcludeFromSync));
        assert!(!verbs.contains(&MenuVerb::IncludeInSync));
    }

    #[test]
    fn excluded_entry_offers_include_and_not_exclude() {
        let state = PathMenuState { in_sync_root: true, excluded: true, ..Default::default() };
        let verbs = top_level_verbs(&items(&state));
        assert!(verbs.contains(&MenuVerb::IncludeInSync));
        assert!(!verbs.contains(&MenuVerb::ExcludeFromSync));
    }

    #[test]
    fn read_only_root_hides_exclude_toggle() {
        let state = PathMenuState { in_sync_root: true, read_only: true, ..Default::default() };
        let verbs = top_level_verbs(&items(&state));
        assert!(!verbs.contains(&MenuVerb::ExcludeFromSync));
        assert!(!verbs.contains(&MenuVerb::IncludeInSync));
    }

    #[test]
    fn shared_entry_offers_manage_shares() {
        let state = PathMenuState { in_sync_root: true, is_shared: true, ..Default::default() };
        let menu = items(&state);
        let share = share_submenu(&menu).expect("Share submenu present");
        assert!(leaf_verbs(share).contains(&MenuVerb::ManageShares));
    }

    #[test]
    fn unshared_entry_hides_manage_shares() {
        let state = PathMenuState { in_sync_root: true, ..Default::default() };
        assert!(!leaf_verbs(&items(&state)).contains(&MenuVerb::ManageShares));
    }

    #[test]
    fn conflict_entry_offers_resolve() {
        let state = PathMenuState { in_sync_root: true, in_conflict: true, ..Default::default() };
        assert!(top_level_verbs(&items(&state)).contains(&MenuVerb::ResolveConflict));
    }

    #[test]
    fn non_conflict_entry_hides_resolve() {
        let state = PathMenuState { in_sync_root: true, ..Default::default() };
        assert!(!top_level_verbs(&items(&state)).contains(&MenuVerb::ResolveConflict));
    }

    #[test]
    fn unpinned_entry_offers_keep_on_device() {
        let state = PathMenuState { in_sync_root: true, ..Default::default() };
        let verbs = top_level_verbs(&items(&state));
        assert!(verbs.contains(&MenuVerb::KeepOnDevice));
        assert!(!verbs.contains(&MenuVerb::FreeUpSpace));
    }

    #[test]
    fn pinned_entry_offers_free_up_space() {
        let state = PathMenuState { in_sync_root: true, pinned: true, ..Default::default() };
        let verbs = top_level_verbs(&items(&state));
        assert!(verbs.contains(&MenuVerb::FreeUpSpace));
        assert!(!verbs.contains(&MenuVerb::KeepOnDevice));
    }

    #[test]
    fn excluded_entry_hides_pin_toggle() {
        let state = PathMenuState { in_sync_root: true, excluded: true, ..Default::default() };
        let verbs = top_level_verbs(&items(&state));
        assert!(!verbs.contains(&MenuVerb::KeepOnDevice));
        assert!(!verbs.contains(&MenuVerb::FreeUpSpace));
    }

    #[test]
    fn folder_outside_sync_root_offers_nothing() {
        let state = PathMenuState::default();
        assert!(items(&state).is_empty());
    }

    #[test]
    fn file_outside_sync_root_offers_nothing() {
        let state = PathMenuState::default();
        assert!(items(&state).is_empty());
    }

    /// Identity translator for tests: label == key, so assertions stay readable.
    fn keys(items: &[MenuItem]) -> Vec<MenuNode> {
        decorate(items, &|k| k.to_string())
    }

    struct FakeBackend {
        menu:   Vec<MenuNode>,
        result: Result<(), String>,
    }
    impl MenuBackend for FakeBackend {
        fn menu_for(&self, _path: &Path) -> Vec<MenuNode> { self.menu.clone() }
        fn invoke(&self, _verb: MenuVerb, _path: &Path) -> Result<(), String> { self.result.clone() }
    }

    #[test]
    fn decorate_maps_labels_and_keeps_structure() {
        let items = items(&PathMenuState { in_sync_root: true, ..Default::default() });
        let nodes = decorate(&items, &|k| format!("<{k}>"));
        // First node is the OpenWeb action, localized.
        assert_eq!(nodes[0].verb, Some(MenuVerb::OpenWeb));
        assert_eq!(nodes[0].label, "<contextmenu.openWeb>");
        // The share submenu is a container (no verb) with localized label + kids.
        let share = nodes.iter().find(|n| n.verb.is_none()).expect("submenu");
        assert_eq!(share.label, "<contextmenu.group.share>");
        assert!(share.children.iter().any(|c| c.verb == Some(MenuVerb::ShareWithUser)));
    }

    #[test]
    fn request_response_survive_json_round_trip() {
        let req = Request::Invoke { verb: MenuVerb::OpenWeb, path: r"C:\Sync\a.txt".into() };
        let json = serde_json::to_string(&req).unwrap();
        assert_eq!(serde_json::from_str::<Request>(&json).unwrap(), req);

        let resp = Response::Menu { items: keys(&items(&PathMenuState { in_sync_root: true, ..Default::default() })) };
        let json = serde_json::to_string(&resp).unwrap();
        assert_eq!(serde_json::from_str::<Response>(&json).unwrap(), resp);
    }

    #[test]
    fn handle_menu_request_returns_backend_menu() {
        let menu = keys(&[MenuItem::Action(MenuVerb::OpenWeb)]);
        let backend = FakeBackend { menu: menu.clone(), result: Ok(()) };
        let resp = handle(Request::Menu { path: "p".into() }, &backend);
        assert_eq!(resp, Response::Menu { items: menu });
    }

    #[test]
    fn handle_invoke_maps_ok_to_done_and_err_to_error() {
        let ok = FakeBackend { menu: vec![], result: Ok(()) };
        assert_eq!(
            handle(Request::Invoke { verb: MenuVerb::OpenWeb, path: "p".into() }, &ok),
            Response::Done,
        );
        let err = FakeBackend { menu: vec![], result: Err("boom".into()) };
        assert_eq!(
            handle(Request::Invoke { verb: MenuVerb::OpenWeb, path: "p".into() }, &err),
            Response::Error { error: "boom".into() },
        );
    }

    #[test]
    fn mount_entry_hides_share_submenu() {
        let state = PathMenuState { in_sync_root: true, is_mount: true, ..Default::default() };
        let menu = items(&state);
        assert!(share_submenu(&menu).is_none());
    }
}
