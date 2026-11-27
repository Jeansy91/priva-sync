import { useState } from "react";
import { FolderOpen, Check, X, Cloud, HardDrive } from "lucide-react";
import { open as openDialog } from "@tauri-apps/plugin-dialog";
import { Button, Input } from "@privasync/ui";
import { useTranslation } from "@privasync/i18n";
import { cmd, type ServerSyncRoot } from "../../tauri";
import { commandErrorMessage } from "../../commandError";
import { isVolumeRoot } from "../../volumeRoot";

interface AddSyncRootFormProps {
  serverRoots:      ServerSyncRoot[];
  supportsOnDemand: boolean;
  onCreated:        (newServerRoot: ServerSyncRoot) => void;
  onClose:          () => void;
  onError:          (msg: string) => void;
  onAdded:          () => void;
}

/**
 * "Add sync folder" form embedded in SyncRootsTab. Always creates a *new*
 * server root — already-existing roots are surfaced as their own add-able
 * list (AvailableSyncRootsSection), so this form only coins new folders.
 * Picks a local folder via the Tauri dialog and optionally flips between
 * on-demand and eager download modes (only when the platform supports
 * placeholders).
 */
export function AddSyncRootForm(props: AddSyncRootFormProps) {
  const { serverRoots, supportsOnDemand, onCreated, onClose, onError, onAdded } = props;
  const { t } = useTranslation();

  const [newRootName, setNewRootName]   = useState("");
  const [localPath, setLocalPath]       = useState("");
  const [downloadMode, setDownloadMode] = useState<"on_demand" | "eager">(
    supportsOnDemand ? "on_demand" : "eager"
  );

  async function pickDirectory() {
    try {
      const selected = await openDialog({ directory: true, multiple: false });
      if (typeof selected === "string") setLocalPath(selected);
    } catch {
      onError(t("desktop.syncRoots.pickFolderError"));
    }
  }

  async function handleAdd(e: React.FormEvent) {
    e.preventDefault();
    if (!localPath.trim() || !newRootName.trim()) return;

    onError("");
    try {
      // Reuse an existing server root with the same name rather than failing —
      // the create-only form should be forgiving of a repeated name.
      const existing = serverRoots.find(
        (r) => r.name.toLowerCase() === newRootName.trim().toLowerCase()
      );
      let serverRoot: ServerSyncRoot;
      if (existing) {
        serverRoot = existing;
      } else {
        serverRoot = await cmd.createServerSyncRoot(newRootName.trim());
        onCreated(serverRoot);
      }

      await cmd.addSyncRoot(
        serverRoot.id,
        serverRoot.name,
        localPath.trim(),
        serverRoot.id,
        // The picker is hidden for a drive root, but the state behind it may
        // still say "on_demand" from before the folder was chosen.
        isVolumeRoot(localPath.trim()) ? "eager" : downloadMode,
      );
      onClose();
      onAdded();
    } catch (err) {
      onError(commandErrorMessage(err));
    }
  }

  return (
    <form
      onSubmit={handleAdd}
      className="rounded-xl border border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/40 p-4 space-y-3"
    >
      <p className="text-xs font-semibold text-blue-800 dark:text-blue-300">{t("desktop.syncRoots.addTitle")}</p>

      <div>
        <label className="mb-1 block text-xs font-medium text-muted-foreground">
          {t("desktop.syncRoots.serverFolder")}
        </label>
        <Input
          type="text"
          autoFocus
          placeholder={t("desktop.syncRoots.newFolderName")}
          value={newRootName}
          onChange={(e) => setNewRootName(e.target.value)}
          className="rounded-lg py-2 h-auto"
        />
      </div>

      <div>
        <label className="mb-1 block text-xs font-medium text-muted-foreground">
          {t("desktop.syncRoots.localPath")}
        </label>
        <div className="flex gap-2">
          <Input
            type="text"
            readOnly
            value={localPath}
            placeholder={t("desktop.syncRoots.pickFolder")}
            onClick={pickDirectory}
            className="flex-1 w-auto cursor-pointer rounded-lg py-2 h-auto"
          />
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={pickDirectory}
            className="gap-1 text-muted-foreground"
          >
            <FolderOpen className="h-3.5 w-3.5" />
            {t("desktop.common.browse")}
          </Button>
        </div>
      </div>

      {/* A drive root cannot carry placeholders, so the choice does not exist
          there — the backend forces eager regardless (ADR-0057). */}
      {supportsOnDemand && isVolumeRoot(localPath) && (
        <p className="rounded-lg bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
          {t("desktop.syncRoots.volumeRootEagerOnly")}
        </p>
      )}

      {supportsOnDemand && !isVolumeRoot(localPath) && (
        <div>
          <label className="mb-1 block text-xs font-medium text-muted-foreground">
            {t("desktop.syncRoots.downloadMode")}
          </label>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => setDownloadMode("on_demand")}
              className={`flex flex-1 items-center gap-2 rounded-lg border px-3 py-2 text-xs
                         transition-colors ${downloadMode === "on_demand"
                           ? "border-blue-500 bg-blue-50 dark:bg-blue-950/40 text-blue-700 dark:text-blue-300"
                           : "border-border bg-card text-muted-foreground hover:bg-accent/50"}`}
            >
              <Cloud className="h-3.5 w-3.5 shrink-0" />
              <span>
                <span className="font-medium">{t("desktop.syncRoots.onDemandTitle")}</span>
                <span className="block text-muted-foreground/70">{t("desktop.syncRoots.onDemandSubtitle")}</span>
              </span>
            </button>
            <button
              type="button"
              onClick={() => setDownloadMode("eager")}
              className={`flex flex-1 items-center gap-2 rounded-lg border px-3 py-2 text-xs
                         transition-colors ${downloadMode === "eager"
                           ? "border-blue-500 bg-blue-50 dark:bg-blue-950/40 text-blue-700 dark:text-blue-300"
                           : "border-border bg-card text-muted-foreground hover:bg-accent/50"}`}
            >
              <HardDrive className="h-3.5 w-3.5 shrink-0" />
              <span>
                <span className="font-medium">{t("desktop.syncRoots.eagerTitle")}</span>
                <span className="block text-muted-foreground/70">{t("desktop.syncRoots.eagerSubtitle")}</span>
              </span>
            </button>
          </div>
        </div>
      )}

      <p className="text-xs text-amber-700 dark:text-amber-300 bg-amber-50 dark:bg-amber-950/40 rounded-lg px-3 py-2">
        {t("desktop.syncRoots.willCreate")}
      </p>

      <div className="flex gap-2 pt-1">
        <Button type="submit" size="sm" disabled={!localPath || !newRootName} className="gap-1">
          <Check className="h-3.5 w-3.5" />
          {t("desktop.syncRoots.confirmAdd")}
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => { onClose(); onError(""); }}
          className="gap-1 text-muted-foreground"
        >
          <X className="h-3.5 w-3.5" />
          {t("desktop.common.cancel")}
        </Button>
      </div>
    </form>
  );
}
