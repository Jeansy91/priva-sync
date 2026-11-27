use tauri::Emitter;

use super::SyncEngine;

impl SyncEngine {
    /// Emit sync_status_changed with the full status object (pending_count
    /// included) so the frontend does not need a separate getSyncStatus() call.
    /// Pass an explicit `status` override (e.g. "syncing" while a cycle is
    /// running); pass "" to derive it purely from the DB.
    pub(super) fn emit_status(&self, status: &str) {
        // Counted, not listed — see `pending_counts_all`.
        let (pending_count, conflict_count) = self.db.pending_counts_all().unwrap_or((0, 0));
        let derived = if conflict_count > 0 { "conflict" }
                      else if pending_count  > 0 { "syncing" }
                      else                       { "idle" };
        let effective = if status.is_empty() { derived } else { status };
        self.app.emit("sync_status_changed", serde_json::json!({
            "pending_count":  pending_count,
            "conflict_count": conflict_count,
            "status":         effective,
        })).ok();
    }

    pub(super) fn emit_progress(&self, root_id: &str, current: usize, total: usize, file: &str, action: &str) {
        self.app.emit("sync_progress", serde_json::json!({
            "root_id": root_id,
            "current": current,
            "total":   total,
            "file":    file,
            "action":  action,
        })).ok();
    }

    pub(super) fn emit_progress_done(&self, root_id: &str) {
        self.app.emit("sync_progress", serde_json::json!({
            "root_id": root_id,
            "current": 0,
            "total":   0,
            "file":    "",
            "action":  "done",
        })).ok();
    }

    /// Asked once per cycle, so it is answered by the database rather than by
    /// scanning a materialised list of every pending row on the system — that
    /// list is the whole root during a first sync (ADR-0056).
    pub(super) fn overall_status(&self) -> &'static str {
        match self.db.pending_summary_all() {
            Ok((true, _))      => "conflict",
            Ok((false, true))  => "syncing",
            Ok((false, false)) => "idle",
            // A DB error is not evidence that everything is synced; keep the
            // cycle's own "syncing" reading rather than claiming idle.
            Err(_)             => "syncing",
        }
    }
}
