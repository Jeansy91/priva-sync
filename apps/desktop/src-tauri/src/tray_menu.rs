//! Pure layout logic for the tray "Ordner öffnen" submenu (ADR-0021 follow-up).
//!
//! Splits the registered sync roots into two groups — own sync folders and
//! folders shared with the user (activated mounts) — and emits an ordered row
//! sequence with group headers and a separator. Kept free of any Tauri or
//! i18n dependency so the grouping/sorting/separator rules are unit-testable
//! without a running app.

use crate::db::SyncRootEntry;

/// One row in the flattened submenu. Headers and the separator carry no data;
/// the caller maps them to disabled label items / a `PredefinedMenuItem`.
/// `Root` borrows the entry so the caller can build the `open_root_<id>` menu id.
#[derive(Debug, PartialEq)]
pub enum OpenRow<'a> {
    /// Disabled header for the group of own sync folders.
    OwnedHeader,
    /// Disabled header for the group of folders shared with the user.
    SharedHeader,
    /// Visual separator between the two non-empty groups.
    Separator,
    /// An openable sync root.
    Root { id: &'a str, name: &'a str },
}

/// Build the ordered row sequence for the "Ordner öffnen" submenu: own sync
/// folders first, then folders shared with the user. A group header precedes
/// its roots, a separator sits between the two groups, and empty groups are
/// omitted entirely.
pub fn build_open_rows(roots: &[SyncRootEntry]) -> Vec<OpenRow<'_>> {
    let (mut shared, mut owned): (Vec<&SyncRootEntry>, Vec<&SyncRootEntry>) =
        roots.iter().partition(|r| r.is_mount());
    let by_name = |a: &&SyncRootEntry, b: &&SyncRootEntry| {
        a.name.to_lowercase().cmp(&b.name.to_lowercase())
    };
    owned.sort_by(by_name);
    shared.sort_by(by_name);

    let mut rows = Vec::new();
    if !owned.is_empty() {
        rows.push(OpenRow::OwnedHeader);
        for r in &owned {
            rows.push(OpenRow::Root { id: &r.id, name: &r.name });
        }
    }
    if !shared.is_empty() {
        if !owned.is_empty() {
            rows.push(OpenRow::Separator);
        }
        rows.push(OpenRow::SharedHeader);
        for r in shared {
            rows.push(OpenRow::Root { id: &r.id, name: &r.name });
        }
    }
    rows
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::{ROOT_KIND_MOUNT, ROOT_KIND_OWNED};

    fn root(id: &str, name: &str, kind: &str) -> SyncRootEntry {
        SyncRootEntry {
            id:             id.to_string(),
            name:           name.to_string(),
            local_path:     format!("C:/sync/{id}"),
            server_root_id: id.to_string(),
            download_mode:  "on_demand".to_string(),
            read_only:      false,
            paused:         false,
            kind:           kind.to_string(),
        }
    }

    #[test]
    fn single_owned_root_sits_under_owned_header() {
        let roots = vec![root("a", "Fotos", ROOT_KIND_OWNED)];
        let rows = build_open_rows(&roots);
        assert_eq!(
            rows,
            vec![OpenRow::OwnedHeader, OpenRow::Root { id: "a", name: "Fotos" }],
        );
    }

    #[test]
    fn single_mount_sits_under_shared_header() {
        let roots = vec![root("m", "Team", ROOT_KIND_MOUNT)];
        let rows = build_open_rows(&roots);
        assert_eq!(
            rows,
            vec![OpenRow::SharedHeader, OpenRow::Root { id: "m", name: "Team" }],
        );
    }

    #[test]
    fn both_groups_are_owned_first_with_separator_between() {
        let roots = vec![
            root("a", "Fotos", ROOT_KIND_OWNED),
            root("m", "Team", ROOT_KIND_MOUNT),
        ];
        let rows = build_open_rows(&roots);
        assert_eq!(
            rows,
            vec![
                OpenRow::OwnedHeader,
                OpenRow::Root { id: "a", name: "Fotos" },
                OpenRow::Separator,
                OpenRow::SharedHeader,
                OpenRow::Root { id: "m", name: "Team" },
            ],
        );
    }

    #[test]
    fn roots_sort_alphabetically_case_insensitive_within_group() {
        let roots = vec![
            root("1", "Zebra", ROOT_KIND_OWNED),
            root("2", "apfel", ROOT_KIND_OWNED),
            root("3", "Birne", ROOT_KIND_OWNED),
        ];
        let rows = build_open_rows(&roots);
        let names: Vec<&str> = rows
            .iter()
            .filter_map(|r| match r {
                OpenRow::Root { name, .. } => Some(*name),
                _ => None,
            })
            .collect();
        assert_eq!(names, vec!["apfel", "Birne", "Zebra"]);
    }

    #[test]
    fn no_roots_yields_no_rows() {
        assert!(build_open_rows(&[]).is_empty());
    }
}
