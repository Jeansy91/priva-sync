import { useEffect, useState } from "react";
import { AlertTriangle } from "lucide-react";
import { Trans, useTranslation } from "@privasync/i18n";
import {
  cmd,
  onSyncStatusChanged, onSyncConflict, onSessionExpired, onSyncProgress,
  onBulkDeleteWarning, onPathWarning, onUpdateAvailable, onRootOrphaned, onRootRenamed,
  onTrayNavigate, onUpdateStage,
  type BulkDeleteWarning, type UpdateInfo, type UpdateStage,
} from "./tauri";
import { useAppStore } from "./store";
import SetupPage from "./pages/SetupPage";
import PreLoginSettingsPage from "./pages/PreLoginSettingsPage";
import DashboardPage from "./pages/DashboardPage";
import UpdateBanner from "./components/UpdateBanner";
import UpdateGate from "./components/UpdateGate";
import ModalOverlay from "./components/ModalOverlay";

export default function App() {
  const { t } = useTranslation();
  const { config, setConfig, setStatus, setSyncRoots, setRootProgress, addPathWarning } = useAppStore();
  const [loading, setLoading] = useState(true);
  const [bulkDeleteWarning, setBulkDeleteWarning] = useState<BulkDeleteWarning | null>(null);
  const [updateInfo, setUpdateInfo] = useState<UpdateInfo | null>(null);
  // Startup auto-update (ADR-0060). `null` = not read back yet; the initial
  // load below waits for it, so the login screen or dashboard never flashes up
  // in front of a check that is already running.
  const [updateStage, setUpdateStage] = useState<UpdateStage | null>(null);
  // Settings are reachable without a session — everything in them is local.
  // DashboardPage owns its own tray_navigate handling; this covers the branch
  // where no dashboard is mounted, so the tray entry is not a dead click.
  const [preLoginSettings, setPreLoginSettings] = useState(false);

  useEffect(() => {
    Promise.all([cmd.getConfig(), cmd.getSyncStatus(), cmd.listSyncRoots(), cmd.getUpdateStage()])
      .then(([cfg, st, roots, stage]) => {
        setConfig(cfg);
        setStatus(st);
        setSyncRoots(roots);
        // An update_stage event may already have overtaken this reply — it is
        // the newer truth, so only fill in what is still unknown.
        setUpdateStage((prev) => prev ?? stage);
      })
      .catch(console.error)
      .finally(() => setLoading(false));

    // Subscribe to Rust events.
    // The event payload is already the full SyncStatus object — use it directly
    // without a second getSyncStatus() call to avoid race conditions.
    const unlistenStatus = onSyncStatusChanged((status) => {
      setStatus(status);
    });

    const unlistenConflict = onSyncConflict((evt) => {
      console.warn("Sync conflict:", evt);
      cmd.getSyncStatus().then(setStatus).catch(console.error);
    });

    const unlistenSession = onSessionExpired(() => {
      // Re-fetch instead of hardcoding blanks: server_url/username survive
      // logout server-side (state.rs::logout only clears the access token),
      // so this keeps the login form prefilled with the last-used values.
      cmd.getConfig().then(setConfig).catch(console.error);
    });

    const unlistenProgress = onSyncProgress(setRootProgress);

    const unlistenBulkDelete = onBulkDeleteWarning((warning) => {
      setBulkDeleteWarning(warning);
    });

    const unlistenPathWarning = onPathWarning((w) => {
      addPathWarning(w);
    });

    const unlistenUpdate = onUpdateAvailable((info) => {
      setUpdateInfo(info);
    });

    const unlistenStage = onUpdateStage(setUpdateStage);

    // Server-side sync-root deletion (e.g. via web UI) — refresh the list so
    // the removed root disappears from the UI immediately.
    const unlistenOrphaned = onRootOrphaned((evt) => {
      console.info(`Sync-Root '${evt.root_name}' wurde serverseitig entfernt — lokale Dateien bleiben unter '${evt.local_path}' erhalten.`);
      cmd.listSyncRoots().then(setSyncRoots).catch(console.error);
    });

    // Owned root renamed in the web UI — refresh so the new label shows.
    const unlistenRenamed = onRootRenamed(() => {
      cmd.listSyncRoots().then(setSyncRoots).catch(console.error);
    });

    return () => {
      unlistenStatus.then((fn) => fn());
      unlistenConflict.then((fn) => fn());
      unlistenSession.then((fn) => fn());
      unlistenProgress.then((fn) => fn());
      unlistenBulkDelete.then((fn) => fn());
      unlistenPathWarning.then((fn) => fn());
      unlistenUpdate.then((fn) => fn());
      unlistenStage.then((fn) => fn());
      unlistenOrphaned.then((fn) => fn());
      unlistenRenamed.then((fn) => fn());
    };
  }, []);

  // Tray "Einstellungen" while logged out: no DashboardPage exists to receive
  // the event, so open the pre-login settings page instead.
  useEffect(() => {
    const unlisten = onTrayNavigate((target) => {
      if (target === "settings") setPreLoginSettings(true);
    });
    return () => { unlisten.then((fn) => fn()); };
  }, []);

  // A session makes that page moot — clear it so a later sign-out lands on the
  // login form rather than on settings.
  useEffect(() => {
    if (config?.is_logged_in) setPreLoginSettings(false);
  }, [config?.is_logged_in]);

  async function handleConfirmBulkDelete() {
    if (!bulkDeleteWarning) return;
    try {
      await cmd.confirmBulkDelete(bulkDeleteWarning.root_id, bulkDeleteWarning.direction);
      setSyncRoots(await cmd.listSyncRoots());
    } catch (e) {
      console.error("confirmBulkDelete failed:", e);
    }
    setBulkDeleteWarning(null);
  }

  async function handleRestoreLocalFiles() {
    if (!bulkDeleteWarning) return;
    try {
      await cmd.restoreLocalFiles(bulkDeleteWarning.root_id);
      setSyncRoots(await cmd.listSyncRoots());
    } catch (e) {
      console.error("restoreLocalFiles failed:", e);
    }
    setBulkDeleteWarning(null);
  }

  async function handleKeepPaused() {
    // Root is already auto-paused by the bulk-delete guard. Refresh the list
    // so the SyncRootsTab badge shows up immediately, then close the modal.
    if (!bulkDeleteWarning) {
      return;
    }
    try {
      setSyncRoots(await cmd.listSyncRoots());
    } catch (e) {
      console.error("listSyncRoots failed:", e);
    }
    setBulkDeleteWarning(null);
  }

  if (loading) {
    return (
      <div className="flex h-screen items-center justify-center bg-background">
        <div className="h-8 w-8 animate-spin rounded-full border-4 border-blue-500 border-t-transparent" />
      </div>
    );
  }

  return (
    <>
      {/* Startup auto-update: covers everything, login screen included, until
          the check is settled (ADR-0060). An overlay rather than a branch, so
          the login form keeps what was typed into it while a check runs. */}
      {updateStage && updateStage.stage !== "idle" && <UpdateGate stage={updateStage} />}

      {updateInfo && (
        <UpdateBanner info={updateInfo} />
      )}

      {!config?.is_logged_in ? (
        preLoginSettings ? (
          <PreLoginSettingsPage onBack={() => setPreLoginSettings(false)} />
        ) : (
          <SetupPage
            onLogin={() => cmd.getConfig().then(setConfig)}
            initialServerUrl={config?.server_url ?? ""}
            initialUsername={config?.username ?? ""}
            onOpenSettings={() => setPreLoginSettings(true)}
          />
        )
      ) : (
        <DashboardPage />
      )}

      {/* Bulk-delete safety confirmation */}
      {bulkDeleteWarning && (
        <ModalOverlay>
          <div className="w-md rounded-2xl bg-card p-5 shadow-xl space-y-4">
            <div className="flex items-start gap-3">
              <div className="flex h-9 w-9 shrink-0 items-center justify-center
                              rounded-full bg-amber-100 dark:bg-amber-900/40">
                <AlertTriangle className="h-4 w-4 text-amber-600 dark:text-amber-400" />
              </div>
              <div>
                <p className="text-sm font-semibold text-foreground">
                  {t("desktop.bulkDelete.title")}
                </p>
                <p className="mt-1 text-xs text-muted-foreground">
                  <Trans
                    i18nKey="desktop.bulkDelete.summary"
                    values={{
                      name:        bulkDeleteWarning.root_name,
                      deleteCount: bulkDeleteWarning.delete_count,
                      totalCount:  bulkDeleteWarning.total_count,
                    }}
                    components={[
                      <span className="font-medium text-foreground" />,
                      <span className="font-semibold text-red-600 dark:text-red-400" />,
                    ]}
                  />
                </p>
                <p className="mt-2 text-xs text-muted-foreground">
                  {t("desktop.bulkDelete.paused")}
                </p>
              </div>
            </div>

            <div className="space-y-2 pt-1">
              {bulkDeleteWarning.direction === "incoming" ? (
                // Nothing has been removed locally yet -- there is no
                // local state to "restore", unlike the outbound direction.
                <button
                  onClick={handleConfirmBulkDelete}
                  className="w-full rounded-lg bg-red-600 px-3 py-2 text-left text-xs
                             font-medium text-white hover:bg-red-700"
                >
                  <span className="block">
                    {t("desktop.bulkDelete.applyTitle", { count: bulkDeleteWarning.delete_count })}
                  </span>
                  <span className="block text-[11px] font-normal text-red-100/90">
                    {t("desktop.bulkDelete.applySubtitle")}
                  </span>
                </button>
              ) : (
                <>
                  <button
                    onClick={handleConfirmBulkDelete}
                    className="w-full rounded-lg bg-red-600 px-3 py-2 text-left text-xs
                               font-medium text-white hover:bg-red-700"
                  >
                    <span className="block">
                      {t("desktop.bulkDelete.deleteTitle", { count: bulkDeleteWarning.delete_count })}
                    </span>
                    <span className="block text-[11px] font-normal text-red-100/90">
                      {t("desktop.bulkDelete.deleteSubtitle")}
                    </span>
                  </button>

                  <button
                    onClick={handleRestoreLocalFiles}
                    className="w-full rounded-lg border border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/40 px-3 py-2
                               text-left text-xs font-medium text-blue-700 dark:text-blue-300 hover:bg-blue-100 dark:hover:bg-blue-900/40"
                  >
                    <span className="block">{t("desktop.bulkDelete.restoreTitle")}</span>
                    <span className="block text-[11px] font-normal text-blue-700/80 dark:text-blue-300/80">
                      {t("desktop.bulkDelete.restoreSubtitle")}
                    </span>
                  </button>
                </>
              )}

              <button
                onClick={handleKeepPaused}
                className="w-full rounded-lg border border-border px-3 py-2 text-left
                           text-xs font-medium text-foreground hover:bg-accent/50"
              >
                <span className="block">{t("desktop.bulkDelete.keepPausedTitle")}</span>
                <span className="block text-[11px] font-normal text-muted-foreground">
                  {t("desktop.bulkDelete.keepPausedSubtitle")}
                </span>
              </button>
            </div>
          </div>
        </ModalOverlay>
      )}
    </>
  );
}
