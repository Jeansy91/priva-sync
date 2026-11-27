//! Named-pipe protocol mirror + client (ADR-0021).
//!
//! These types must stay byte-compatible with the daemon's `contextmenu`
//! module: the daemon owns all menu logic, the shell extension only renders the
//! returned tree and reports the chosen verb back. Keep the serde tags in sync.

use serde::{Deserialize, Serialize};

/// Pipe the daemon's context-menu server listens on.
const PIPE_NAME: &str = r"\\.\pipe\privasync";

#[derive(Serialize)]
#[serde(tag = "op", rename_all = "snake_case")]
pub enum Request {
    Menu { path: String },
    Invoke { verb: MenuVerb, path: String },
}

#[derive(Serialize, Deserialize, Clone, Copy, PartialEq, Eq, Debug)]
pub enum MenuVerb {
    OpenWeb,
    ShareWithUser,
    CreatePublicLink,
    CopyPublicLink,
    ExcludeFromSync,
    IncludeInSync,
    ManageShares,
    ResolveConflict,
    KeepOnDevice,
    FreeUpSpace,
    Synchronize,
}

/// A localized menu node as delivered by the daemon: the verb to invoke (absent
/// for a submenu), the display label (already localized daemon-side against the
/// shared catalog), and any children. The extension renders `label` verbatim.
#[derive(Deserialize, Clone, Debug)]
pub struct MenuNode {
    #[serde(default)]
    pub verb:     Option<MenuVerb>,
    pub label:    String,
    #[serde(default)]
    pub children: Vec<MenuNode>,
}

#[derive(Deserialize)]
#[serde(tag = "ok", rename_all = "snake_case")]
pub enum Response {
    Menu { items: Vec<MenuNode> },
    Done,
    // `error` is carried for completeness/logging though the shell only acts on
    // the variant; keep it so the wire format round-trips losslessly.
    Error {
        #[allow(dead_code)]
        error: String,
    },
}

/// One blocking request/response round trip over the pipe. Returns `None` if the
/// daemon is not running or the exchange fails — callers degrade to an empty
/// menu rather than blocking Explorer.
fn round_trip(req: &Request) -> Option<Response> {
    use std::fs::OpenOptions;
    use std::io::{BufRead, BufReader, Write};

    let mut pipe = OpenOptions::new().read(true).write(true).open(PIPE_NAME).ok()?;
    let mut line = serde_json::to_string(req).ok()?;
    line.push('\n');
    pipe.write_all(line.as_bytes()).ok()?;
    pipe.flush().ok()?;

    let mut reader = BufReader::new(pipe);
    let mut resp = String::new();
    reader.read_line(&mut resp).ok()?;
    serde_json::from_str(&resp).ok()
}

/// Fetch the rendered menu tree for `path`. Empty when the daemon is down or the
/// path is not actionable.
pub fn query_menu(path: &str) -> Vec<MenuNode> {
    match round_trip(&Request::Menu { path: path.to_string() }) {
        Some(Response::Menu { items }) => items,
        _ => Vec::new(),
    }
}

/// Tell the daemon the user picked `verb` for `path`. Returns whether it
/// reported success.
pub fn invoke(verb: MenuVerb, path: &str) -> bool {
    matches!(round_trip(&Request::Invoke { verb, path: path.to_string() }), Some(Response::Done))
}
