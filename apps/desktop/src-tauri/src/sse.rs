// SSE subscriber: opens a long-lived connection to /api/v1/events and reacts
// to server push events. Currently handles:
//   - conflict_added    -> notification + tray switch + Tauri event
//   - conflict_resolved -> force-sync via sync_notify + Tauri event
//
// Reconnect: exponential backoff (1s -> 30s cap), reset to 1s after a
// successful connection. On 401 try an API-client refresh; if that also
// fails, the loop continues and the next attempt catches it.

use std::sync::{Arc, Mutex as StdMutex, OnceLock};
use std::time::Duration;

use anyhow::Result;
use bytes::Bytes;
use futures_util::StreamExt;
use serde::Deserialize;
use serde_json::Value;

/// Mirror of the fields of `internal/sse/events.File` (Go) that
/// [`on_file_uploaded`] actually reads -- a typed parse used only for a debug
/// log line, so the rest of the server's struct is deliberately not mirrored.
#[derive(Debug, Clone, Deserialize)]
struct SseFile {
    size: i64,
}

/// Mirror of `internal/sse/events.FileUploaded` (Go).
#[derive(Debug, Clone, Deserialize)]
struct FileUploadedPayload {
    root_id: String,
    path:    String,
    file:    SseFile,
}

/// Mirror of `internal/sse/events.ConflictAdded` (Go).
#[derive(Debug, Clone, Deserialize)]
struct ConflictAddedPayload {
    #[serde(default)]
    original_path: String,
    #[serde(default)]
    root_id: String,
}

/// Wire-format version the server emits. Must match
/// `apps/server/internal/sse/events.EnvelopeVersion`.
const ENVELOPE_VERSION: u32 = 2;
use tauri::{AppHandle, Emitter, Manager};
use tauri_plugin_notification::NotificationExt;
use tokio::sync::{Notify, RwLock};
use tracing::{debug, info, warn};

use crate::api::ApiClient;

const INITIAL_BACKOFF: Duration = Duration::from_secs(1);
const MAX_BACKOFF:     Duration = Duration::from_secs(30);

/// Spawn the SSE subscription loop. Returns immediately.
pub fn spawn_subscription(app: AppHandle, api: Arc<RwLock<ApiClient>>) {
    tauri::async_runtime::spawn(async move {
        run_loop(app, api).await;
    });
}

async fn run_loop(app: AppHandle, api: Arc<RwLock<ApiClient>>) {
    let db = app.state::<Arc<crate::db::LocalDb>>().inner().clone();
    let mut backoff = INITIAL_BACKOFF;
    // Track whether we've already complained about a missing token so we
    // don't log "kein Token" every 5 seconds while waiting for the user to
    // re-authenticate. Reset once the token reappears.
    let mut warned_no_token = false;

    loop {
        // Snapshot base_url + whether we have a token at all — the
        // connection must not block during the stream because another
        // caller is holding the lock.
        let (base_url, has_token) = {
            let client = api.read().await;
            (client.base_url.clone(), client.access_token.is_some())
        };

        if !has_token {
            if !warned_no_token {
                info!("sse: kein Token — warte auf Anmeldung");
                // Tell the frontend to surface the login screen. Sync engine
                // emits the same on session expiry; double-emit is harmless.
                let _ = app.emit("session_expired", ());
                warned_no_token = true;
            }
            // Exponential backoff so we don't poll the lock every 5s forever
            // while the user is away. Capped at MAX_BACKOFF (30s).
            tokio::time::sleep(backoff).await;
            backoff = (backoff * 2).min(MAX_BACKOFF);
            continue;
        }

        warned_no_token = false;

        // with_auth_retry (ADR-0025) owns the 401→refresh→replay dance for
        // the connection setup; a mid-stream drop goes through the existing
        // reconnect below, which re-enters here and so passes back through
        // the wrapper too.
        let connect_result = {
            let client   = api.clone();
            let base_url = base_url.clone();
            crate::api::with_auth_retry(
                &api, &db,
                || { let _ = app.emit("session_expired", ()); },
                move || {
                    let client   = client.clone();
                    let base_url = base_url.clone();
                    async move { connect(&client, &base_url).await }
                },
            ).await
        };

        match connect_result {
            Ok(response) => {
                info!("sse: verbunden mit {base_url}/api/v1/events");
                match consume_stream(&app, response).await {
                    Ok(()) => {
                        info!("sse: stream geschlossen, reconnect");
                        backoff = INITIAL_BACKOFF;
                    }
                    Err(e) => {
                        warn!("sse: stream-Fehler: {e}, retry in {:?}", backoff);
                        tokio::time::sleep(backoff).await;
                        backoff = (backoff * 2).min(MAX_BACKOFF);
                    }
                }
            }
            Err(e) if e.is::<crate::api::SessionExpired>() => {
                // with_auth_retry already cleared the token (client + db)
                // and emitted session_expired — retry immediately, the next
                // iteration's token check now finds it empty.
            }
            Err(e) => {
                warn!("sse: connect fehlgeschlagen: {e}, retry in {:?}", backoff);
                tokio::time::sleep(backoff).await;
                backoff = (backoff * 2).min(MAX_BACKOFF);
            }
        }
    }
}

/// Open the SSE connection: GET `/api/v1/events` with the client's current
/// bearer token, validated via the same typed [`crate::api::Unauthorized`]
/// (ADR-0025) every other API call uses — no ad-hoc status matching here.
async fn connect(api: &RwLock<ApiClient>, base_url: &str) -> Result<reqwest::Response> {
    let token = api.read().await.access_token.clone().unwrap_or_default();
    let url = format!("{base_url}/api/v1/events");
    let res = reqwest::Client::new()
        .get(&url)
        .header("Authorization", format!("Bearer {token}"))
        .header("Accept", "text/event-stream")
        .send()
        .await?;
    crate::api::check_status(res.status(), "sse connect")?;
    Ok(res)
}

/// Drain the event stream until the connection drops. Errors here are
/// genuine network/stream faults — auth is already resolved by [`connect`]
/// before this is ever called.
async fn consume_stream(app: &AppHandle, response: reqwest::Response) -> Result<()> {
    let mut stream = response.bytes_stream();
    let mut buf     = Vec::<u8>::new();

    while let Some(chunk) = stream.next().await {
        let chunk: Bytes = chunk?;
        buf.extend_from_slice(&chunk);

        // Events sind durch eine Leerzeile (\n\n) getrennt.
        while let Some(pos) = find_double_newline(&buf) {
            let event_block = buf.drain(..pos + 2).collect::<Vec<u8>>();
            let event_text  = String::from_utf8_lossy(&event_block);
            handle_event(app, &event_text);
        }
    }

    Ok(())
}

fn find_double_newline(buf: &[u8]) -> Option<usize> {
    buf.windows(2).position(|w| w == b"\n\n")
}

fn handle_event(app: &AppHandle, raw: &str) {
    let mut event_type = "";
    let mut data_lines: Vec<&str> = Vec::new();

    for line in raw.lines() {
        if let Some(t) = line.strip_prefix("event: ") {
            event_type = t.trim();
        } else if let Some(d) = line.strip_prefix("data: ") {
            data_lines.push(d);
        }
        // ":"-Lines (Keep-Alive) und leere Zeilen werden ignoriert.
    }

    if event_type.is_empty() {
        return;
    }

    let data_str = data_lines.join("\n");
    let envelope: Value = match serde_json::from_str(&data_str) {
        Ok(v) => v,
        Err(e) => {
            warn!("sse: {event_type} envelope JSON parse failed: {e}");
            return;
        }
    };

    let data = match unwrap_envelope(event_type, &envelope) {
        Some(d) => d,
        None    => return,
    };

    debug!("sse: event {event_type} data {data}");

    match event_type {
        "conflict_added"    => on_conflict_added(app, &data),
        "conflict_resolved" => on_conflict_resolved(app, &data),
        "file_uploaded"     => on_file_uploaded(app, &data),
        et if triggers_force_sync_pull(et) => on_share_changed(app, et, &data),
        other => {
            // Unbekannte Events trotzdem nach vorne reichen, damit Frontend
            // sie konsumieren kann.
            let _ = app.emit(other, data);
        }
    }
}

/// Validates the `{type, version, data}` envelope and returns `data` as an
/// owned `Value`. Returns `None` (and warns) for non-object envelopes or
/// version mismatches — the event is then dropped, mirroring the web parser.
fn unwrap_envelope(event_type: &str, envelope: &Value) -> Option<Value> {
    let obj = envelope.as_object()?;
    let version = obj.get("version").and_then(|v| v.as_u64()).unwrap_or(0) as u32;
    if version != ENVELOPE_VERSION {
        warn!(
            "sse: dropping {event_type} with envelope version {version}, expected {}",
            ENVELOPE_VERSION
        );
        return None;
    }
    obj.get("data").cloned()
}

fn on_file_uploaded(app: &AppHandle, data: &Value) {
    // Typed parse for logging; loop must not crash on shape mismatch, so on
    // parse error we log and still forward the raw data to the frontend.
    match serde_json::from_value::<FileUploadedPayload>(data.clone()) {
        Ok(p) => debug!(
            "sse: file_uploaded root={} path={} size={}",
            p.root_id, p.path, p.file.size
        ),
        Err(e) => warn!("sse: file_uploaded payload parse failed: {e}"),
    };
    let _ = app.emit("file_uploaded", data.clone());
}

/// Shared Mount lifecycle (ADR-0011). SSE is only a latency optimization here:
/// the authoritative state is the per-cycle pull of GET /shared-mounts. So we
/// just kick an immediate sync (force the prune/pull to run now instead of
/// waiting for the 30 s poll) and tell the frontend to refresh its tray list.
/// Share-list events the recipient reacts to by forcing an immediate mount
/// pull/prune cycle. The pull is authoritative (ADR-0011/0014); these SSE
/// events only cut the latency until the next poll. `share_updated` covers a
/// can_write change that never trips the revoke/prune path (ADR-0014).
fn triggers_force_sync_pull(event_type: &str) -> bool {
    matches!(event_type, "share_added" | "share_revoked" | "share_updated")
}

fn on_share_changed(app: &AppHandle, event_type: &str, data: &Value) {
    if let Some(notify) = app.try_state::<Arc<Notify>>() {
        notify.notify_one();
        debug!("sse: {event_type} triggered force-sync (mount pull/prune)");
    }
    let _ = app.emit("shares_changed", data.clone());
}

/// True iff the conflict's root_id maps to a locally-tracked sync root. Used to
/// suppress conflict notifications for roots this client does not sync — e.g.
/// a conflict in a giver's root that the recipient only sees in the web view,
/// not as a local mount (ADR-0011). Fails open: if the DB can't be read we
/// still show the conflict rather than silently dropping it.
fn conflict_is_for_local_root(app: &AppHandle, root_id: &str) -> bool {
    if root_id.is_empty() {
        return true;
    }
    match app.try_state::<Arc<crate::db::LocalDb>>() {
        Some(db) => match db.list_sync_roots() {
            Ok(roots) => roots.iter().any(|r| r.server_root_id == root_id),
            Err(_) => true,
        },
        None => true,
    }
}

/// How long to keep collecting ConflictAdded events before firing one summary
/// OS notification. Long enough to absorb a whole sync cycle's worth of
/// conflicts, short enough that a lone conflict still notifies promptly.
const CONFLICT_NOTIFY_DEBOUNCE: Duration = Duration::from_secs(2);

/// Accumulates conflict paths between debounced flushes. `flush_scheduled`
/// guards against spawning more than one flush task per burst — every conflict
/// that lands while a flush is pending simply joins the batch.
#[derive(Default)]
struct ConflictBatch {
    paths:           Vec<String>,
    flush_scheduled: bool,
}

fn conflict_batch() -> &'static StdMutex<ConflictBatch> {
    static BATCH: OnceLock<StdMutex<ConflictBatch>> = OnceLock::new();
    BATCH.get_or_init(|| StdMutex::new(ConflictBatch::default()))
}

/// Build the consolidated conflict-notification body. One conflict names the
/// file; several are summarised by count so a burst yields a single toast
/// instead of one per file. Pure → unit-testable.
fn build_conflict_notification_body(paths: &[String]) -> String {
    match paths.len() {
        0 => String::new(),
        1 => format!("Konflikt erkannt: {}", paths[0]),
        n => format!("{n} Konflikte erkannt"),
    }
}

/// Queue one conflict for the next debounced summary notification. The first
/// conflict of a burst schedules a flush task; later conflicts within the
/// window just append to the batch. The flush drains the batch and fires a
/// single OS notification.
fn queue_conflict_notification(app: &AppHandle, original_path: &str) {
    {
        let mut batch = conflict_batch().lock().unwrap();
        batch.paths.push(original_path.to_string());
        if batch.flush_scheduled {
            return; // a flush is already pending — this conflict joins it
        }
        batch.flush_scheduled = true;
    }

    let app = app.clone();
    tauri::async_runtime::spawn(async move {
        tokio::time::sleep(CONFLICT_NOTIFY_DEBOUNCE).await;

        let paths = {
            let mut batch = conflict_batch().lock().unwrap();
            batch.flush_scheduled = false;
            std::mem::take(&mut batch.paths)
        };
        if paths.is_empty() {
            return;
        }

        let _ = app
            .notification()
            .builder()
            .title("PrivaSync — Konflikt")
            .body(build_conflict_notification_body(&paths))
            .show();
    });
}

fn on_conflict_added(app: &AppHandle, data: &Value) {
    // Typed parse: read original_path off the inner data. Falls back to "?"
    // if the payload shape drifts so the notification still fires (the loop
    // must not panic on a malformed payload).
    let parsed = serde_json::from_value::<ConflictAddedPayload>(data.clone());
    let (original, root_id) = match parsed {
        Ok(p) => (p.original_path, p.root_id),
        Err(e) => {
            warn!("sse: conflict_added payload parse failed: {e}");
            (String::from("?"), String::new())
        }
    };

    // Gate: skip the notification + tray switch for roots we don't sync locally.
    if !conflict_is_for_local_root(app, &root_id) {
        debug!("sse: conflict_added for non-local root {root_id} — suppressing notification");
        let _ = app.emit("conflict_added", data.clone());
        return;
    }

    let original = if original.is_empty() { String::from("?") } else { original };

    // Coalesce the OS notification: a single sync that conflicts N files fires
    // N ConflictAdded events back-to-back, which used to mean N toasts. Batch
    // them behind a short debounce and surface one summary toast once the burst
    // settles. The tray switch + Tauri event below stay per-event (cheap, no
    // spam) so the icon and conflict list react immediately.
    queue_conflict_notification(app, &original);

    // Tray sofort auf "conflict" umschalten, damit das Icon ohne weiteren
    // Sync-Cycle wechselt.
    let _ = app.emit(
        "sync_status_changed",
        serde_json::json!({
            "status":         "conflict",
            "pending_count":  0,
            "conflict_count": 1,
        }),
    );

    // Frontend-Page kann darauf hoeren um die Conflict-Liste zu reloaden.
    let _ = app.emit("conflict_added", data.clone());
}

fn on_conflict_resolved(app: &AppHandle, data: &Value) {
    // Another client resolved a conflict — pull the canonical server version
    // immediately instead of waiting for the next polling cycle. A global
    // sync_notify is enough; the next cycle handles all roots, which matches
    // the usual force-sync semantics for the typical few-roots case.
    if let Some(notify) = app.try_state::<Arc<Notify>>() {
        notify.notify_one();
        debug!("sse: conflict_resolved triggered force-sync");
    } else {
        warn!("sse: conflict_resolved — no Notify in state, sync not triggered");
    }

    let _ = app.emit("conflict_resolved", data.clone());
}

#[cfg(test)]
mod tests {
    use super::{build_conflict_notification_body, unwrap_envelope, ENVELOPE_VERSION};
    use serde_json::json;

    /// A burst of conflicts must collapse into one notification body: a single
    /// conflict names the file, several are summarised by count. Guards the
    /// per-file push-spam regression (re-add over existing files).
    #[test]
    fn conflict_notification_body_consolidates_a_burst() {
        assert_eq!(
            build_conflict_notification_body(&["/docs/a.txt".to_string()]),
            "Konflikt erkannt: /docs/a.txt",
        );
        let many = vec![
            "/a.txt".to_string(),
            "/b.txt".to_string(),
            "/c.txt".to_string(),
        ];
        assert_eq!(build_conflict_notification_body(&many), "3 Konflikte erkannt");
    }

    #[test]
    fn returns_data_for_matching_version() {
        let env = json!({"type": "file_uploaded", "version": ENVELOPE_VERSION, "data": {"path": "/x"}});
        let data = unwrap_envelope("file_uploaded", &env).expect("data");
        assert_eq!(data, json!({"path": "/x"}));
    }

    #[test]
    fn drops_event_with_wrong_version() {
        let env = json!({"type": "file_uploaded", "version": 99, "data": {"path": "/x"}});
        assert!(unwrap_envelope("file_uploaded", &env).is_none());
    }

    #[test]
    fn drops_non_object_envelope() {
        assert!(unwrap_envelope("file_uploaded", &json!(42)).is_none());
    }

    /// share_updated must force an immediate mount pull just like share_added/
    /// _revoked — the pull stays authoritative, SSE only cuts latency
    /// (ADR-0014). Non-share events must not be misrouted into the pull path.
    #[test]
    fn share_events_trigger_force_sync_pull() {
        use super::triggers_force_sync_pull;
        assert!(triggers_force_sync_pull("share_added"));
        assert!(triggers_force_sync_pull("share_revoked"));
        assert!(triggers_force_sync_pull("share_updated"));
        assert!(!triggers_force_sync_pull("file_uploaded"));
        assert!(!triggers_force_sync_pull("conflict_added"));
    }
}
