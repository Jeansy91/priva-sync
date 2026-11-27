//! Process-wide tracing setup: console plus a rolling file log.
//!
//! Until this existed the daemon logged to stdout only. A released build is
//! a `windows_subsystem = "windows"` binary with no console attached, so
//! every warning it emitted — failed placeholder creation, refused
//! registration, retry exhaustion — went nowhere, and a field report could
//! only be diagnosed by rebuilding from source on the affected machine.
//!
//! Initialised before the Tauri builder so start-up failures are captured
//! too. That means the log directory is resolved here rather than through
//! `AppHandle::path().app_log_dir()`; [`log_dir`] mirrors Tauri's own rule
//! per platform.

use std::path::PathBuf;

use tracing_subscriber::layer::SubscriberExt as _;
use tracing_subscriber::util::SubscriberInitExt as _;
use tracing_subscriber::{fmt, EnvFilter};

/// `identifier` from `tauri.conf.json` — the directory name Tauri derives
/// every per-app path from.
fn identifier() -> &'static str {
    crate::app_id::identifier()
}

/// Rotated daily; older files past this count are deleted by the appender.
/// A week covers "it broke on Friday, reported on Monday" without letting a
/// chatty sync loop grow unbounded.
const MAX_LOG_FILES: usize = 7;

/// Directory the rolling log files live in. `None` when no home/appdata
/// location can be resolved, in which case logging stays console-only.
fn log_dir() -> Option<PathBuf> {
    #[cfg(target_os = "windows")]
    {
        std::env::var_os("LOCALAPPDATA")
            .map(|p| PathBuf::from(p).join(identifier()).join("logs"))
    }
    #[cfg(target_os = "macos")]
    {
        std::env::var_os("HOME")
            .map(|p| PathBuf::from(p).join("Library").join("Logs").join(identifier()))
    }
    #[cfg(not(any(target_os = "windows", target_os = "macos")))]
    {
        std::env::var_os("XDG_DATA_HOME")
            .map(PathBuf::from)
            .or_else(|| std::env::var_os("HOME").map(|p| PathBuf::from(p).join(".local/share")))
            .map(|p| p.join(identifier()).join("logs"))
    }
}

/// Install the global subscriber. Call once, as early in `main` as possible.
pub fn init() {
    let filter = || {
        EnvFilter::try_from_default_env()
            .unwrap_or_else(|_| EnvFilter::new("privasync=debug,warn"))
    };
    // The file layer is deliberately one level quieter than the console.
    // `debug` includes the per-second `transfer_speed` sampler — on its own
    // ~86k lines a day of nothing — and the console is where you go when you
    // are actively watching. `PRIVASYNC_LOG` raises it when a report needs
    // the detail.
    let file_filter = || {
        EnvFilter::try_from_env("PRIVASYNC_LOG")
            .unwrap_or_else(|_| EnvFilter::new("privasync=info,warn"))
    };

    let Some(dir) = log_dir() else {
        tracing_subscriber::fmt().with_env_filter(filter()).init();
        tracing::warn!("no log directory could be resolved — logging to console only");
        return;
    };

    if let Err(e) = std::fs::create_dir_all(&dir) {
        tracing_subscriber::fmt().with_env_filter(filter()).init();
        tracing::warn!("log directory '{}' not usable ({e}) — logging to console only", dir.display());
        return;
    }

    let appender = match tracing_appender::rolling::Builder::new()
        .rotation(tracing_appender::rolling::Rotation::DAILY)
        .filename_prefix("privasync")
        .filename_suffix("log")
        .max_log_files(MAX_LOG_FILES)
        .build(&dir)
    {
        Ok(a) => a,
        Err(e) => {
            tracing_subscriber::fmt().with_env_filter(filter()).init();
            tracing::warn!("rolling log appender for '{}' failed ({e}) — console only", dir.display());
            return;
        }
    };

    // The worker guard flushes buffered lines on drop. This one must outlive
    // every logging call in the process, i.e. all of `main` — leaking it is
    // the intent, not an oversight. Records still in the queue at a hard
    // exit are the accepted cost of not blocking the sync loop on file I/O.
    let (writer, guard) = tracing_appender::non_blocking(appender);
    std::mem::forget(guard);

    // Per-layer filters, not one global one: the registry would otherwise
    // apply the *most* permissive filter to both layers and the file would
    // get the console's debug volume anyway.
    use tracing_subscriber::Layer as _;
    tracing_subscriber::registry()
        .with(fmt::layer().with_filter(filter()))
        .with(fmt::layer().with_writer(writer).with_ansi(false).with_filter(file_filter()))
        .init();

    tracing::info!("logging to {}", dir.display());
}
