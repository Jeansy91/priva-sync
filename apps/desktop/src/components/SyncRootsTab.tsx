import { useEffect, useState } from "react";
import { FolderOpen, FolderSync, Plus, RefreshCw } from "lucide-react";
import { Button } from "@privasync/ui";
import { useTranslation } from "@privasync/i18n";
import { cmd, type SyncRootEntry, type ServerSyncRoot, type RemoveSyncRootOptions } from "../tauri";
import { commandErrorMessage } from "../commandError";
import { useAppStore } from "../store";
import { AddSyncRootForm } from "./sync-roots/AddSyncRootForm";
import { SyncRootListItem } from "./sync-roots/SyncRootListItem";
import { RemoveSyncRootDialog } from "./sync-roots/RemoveSyncRootDialog";
import { SharedMountsSection } from "./sync-roots/SharedMountsSection";
import { AvailableSyncRootsSection } from "./sync-roots/AvailableSyncRootsSection";

export default function SyncRootsTab() {
  const { t } = useTranslation();
  const { syncRoots, setSyncRoots, rootProgress, config, setStatus } = useAppStore();
  const [serverRoots, setServerRoots]   = useState<ServerSyncRoot[]>([]);
  const [adding, setAdding]             = useState(false);
  const [error, setError]               = useState("");
  const [confirmRoot, setConfirmRoot]   = useState<SyncRootEntry | null>(null);
  const [removeOpts, setRemoveOpts]     = useState<RemoveSyncRootOptions | null>(null);
  const [removeBusy, setRemoveBusy]     = useState(false);
  const [removeInfo, setRemoveInfo]     = useState("");

  const supportsOnDemand = config?.supports_on_demand ?? false;

  useEffect(() => {
    reload();
    loadServerRoots();
  }, []);

  async function reload() {
    const roots = await cmd.listSyncRoots();
    setSyncRoots(roots);
  }

  async function loadServerRoots() {
    try {
      const roots = await cmd.listServerSyncRoots();
      setServerRoots(roots);
    } catch {
      // Not critical — user can still create a new server folder
    }
  }

  async function openRemoveDialog(root: SyncRootEntry) {
    setConfirmRoot(root);
    setRemoveOpts(null);
    setRemoveInfo("");
    try {
      const opts = await cmd.getRemoveSyncRootOptions(root.id);
      setRemoveOpts(opts);
    } catch {
      setRemoveOpts({ dehydrated_count: 0, dehydrated_bytes: 0 });
    }
  }

  function closeRemoveDialog() {
    setConfirmRoot(null);
    setRemoveOpts(null);
    setRemoveInfo("");
    setRemoveBusy(false);
  }

  async function performRemove() {
    if (!confirmRoot) return;
    setRemoveBusy(true);
    try {
      // A mount is removed locally only (server share stays); an own root goes
      // through the regular remove path. See ADR-0011.
      if (confirmRoot.kind === "mount") {
        await cmd.removeMount(confirmRoot.server_root_id);
      } else {
        await cmd.removeSyncRoot(confirmRoot.id);
      }
      closeRemoveDialog();
      await reload();
    } catch (err) {
      setRemoveBusy(false);
      closeRemoveDialog();
      setError(commandErrorMessage(err));
    }
  }

  async function downloadAllFirst() {
    if (!confirmRoot) return;
    setRemoveBusy(true);
    try {
      if (confirmRoot.download_mode !== "eager") {
        await cmd.setDownloadMode(confirmRoot.id, "eager");
      }
      await cmd.forceSync();
      await reload();
      setRemoveInfo(t("desktop.syncRoots.remove.downloadStarted"));
    } catch (err) {
      setError(commandErrorMessage(err));
      closeRemoveDialog();
    } finally {
      setRemoveBusy(false);
    }
  }

  async function toggleDownloadMode(root: SyncRootEntry) {
    const next = root.download_mode === "on_demand" ? "eager" : "on_demand";
    try {
      await cmd.setDownloadMode(root.id, next);
      await reload();
    } catch (err) {
      setError(commandErrorMessage(err));
    }
  }

  async function togglePaused(root: SyncRootEntry) {
    try {
      if (root.paused) {
        await cmd.resumeSyncRoot(root.id);
      } else {
        await cmd.pauseSyncRoot(root.id);
      }
      await reload();
    } catch (err) {
      setError(commandErrorMessage(err));
    }
  }

  async function handleForceSync() {
    await cmd.forceSync();
    const status = await cmd.getSyncStatus();
    setStatus(status);
  }

  // Alphabetical, locale-aware order for every list so renames don't shuffle
  // entries by insertion order.
  const byName = <T extends { name: string }>(list: T[]): T[] =>
    [...list].sort((a, b) => a.name.localeCompare(b.name));

  const addableRoots = byName(
    serverRoots.filter((sr) => !syncRoots.find((r) => r.server_root_id === sr.id))
  );
  // Keep the Sync / Mount grouping even for added folders: each group shows
  // its activated entries on top, the not-yet-added ones below.
  const ownedActive = byName(syncRoots.filter((r) => r.kind === "owned"));
  const mountActive = byName(syncRoots.filter((r) => r.kind === "mount"));

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-foreground">
          <FolderSync className="h-4 w-4 text-muted-foreground" />
          {t("desktop.syncRoots.title")}
        </h2>
        <div className="flex gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={handleForceSync}
            title={t("desktop.syncRoots.syncNowTitle")}
            className="gap-1 text-muted-foreground"
          >
            <RefreshCw className="h-3.5 w-3.5" />
            {t("desktop.syncRoots.syncNow")}
          </Button>
          <Button size="sm" onClick={() => { setAdding(true); setError(""); }} className="gap-1">
            <Plus className="h-3.5 w-3.5" />
            {t("desktop.syncRoots.addRoot")}
          </Button>
        </div>
      </div>

      {error && (
        <p className="rounded-lg bg-red-50 dark:bg-red-950/40 px-3 py-2 text-xs text-red-700 dark:text-red-300">{error}</p>
      )}

      {adding && (
        <AddSyncRootForm
          serverRoots={serverRoots}
          supportsOnDemand={supportsOnDemand}
          onCreated={(sr) => setServerRoots((prev) => [...prev, sr])}
          onClose={() => setAdding(false)}
          onError={setError}
          onAdded={reload}
        />
      )}

      {ownedActive.length === 0 && addableRoots.length === 0 ? (
        <div className="flex flex-col items-center gap-3 rounded-xl border-2 border-dashed
                        border-border py-12 text-center">
          <FolderOpen className="h-10 w-10 text-muted-foreground/50" />
          <p className="text-sm text-muted-foreground/70">{t("desktop.syncRoots.emptyMessage")}</p>
          <Button
            variant="link"
            onClick={() => setAdding(true)}
            className="h-auto p-0 text-xs"
          >
            {t("desktop.syncRoots.emptyCta")}
          </Button>
        </div>
      ) : (
        <>
          {ownedActive.length > 0 && (
            <ul className="space-y-2">
              {ownedActive.map((root) => (
                <SyncRootListItem
                  key={root.id}
                  root={root}
                  progress={rootProgress[root.id] ?? {}}
                  supportsOnDemand={supportsOnDemand}
                  onToggleDownloadMode={toggleDownloadMode}
                  onTogglePaused={togglePaused}
                  onRemove={openRemoveDialog}
                />
              ))}
            </ul>
          )}

          <AvailableSyncRootsSection
            addableRoots={addableRoots}
            supportsOnDemand={supportsOnDemand}
            onAdded={reload}
            onError={setError}
          />
        </>
      )}

      <SharedMountsSection
        activeMounts={mountActive}
        progress={rootProgress}
        supportsOnDemand={supportsOnDemand}
        onToggleDownloadMode={toggleDownloadMode}
        onTogglePaused={togglePaused}
        onRemove={openRemoveDialog}
        onActivated={reload}
        onError={setError}
      />

      <RemoveSyncRootDialog
        root={confirmRoot}
        options={removeOpts}
        busy={removeBusy}
        info={removeInfo}
        onClose={closeRemoveDialog}
        onConfirm={performRemove}
        onDownloadFirst={downloadAllFirst}
      />
    </div>
  );
}
