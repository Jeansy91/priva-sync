import { useState } from "react";
import { FolderOpen, Check } from "lucide-react";
import { Button } from "@privasync/ui";
import { open as openDialog } from "@tauri-apps/plugin-dialog";
import { useTranslation } from "@privasync/i18n";
import { cmd, type ServerSyncRoot } from "../../tauri";
import { commandErrorMessage } from "../../commandError";

interface AvailableSyncRootsSectionProps {
  // Own server sync roots that exist on the server but aren't mirrored
  // locally yet. Mirrors how incoming Shared Mounts are surfaced.
  addableRoots:     ServerSyncRoot[];
  supportsOnDemand: boolean;
  onAdded:          () => void;
  onError:          (msg: string) => void;
}

/**
 * Lists the user's own server sync roots that aren't mirrored locally yet,
 * each with an add button. Picking one asks for a local folder and turns it
 * into a normal (owned-kind) sync root — symmetric to SharedMountsSection,
 * which does the same for incoming Shared Mounts.
 */
export function AvailableSyncRootsSection(props: AvailableSyncRootsSectionProps) {
  const { addableRoots, supportsOnDemand, onAdded, onError } = props;
  const { t } = useTranslation();
  const [adding, setAdding] = useState<string | null>(null);

  async function add(root: ServerSyncRoot) {
    let localPath: string;
    try {
      const selected = await openDialog({ directory: true, multiple: false });
      if (typeof selected !== "string") return;
      localPath = selected;
    } catch {
      onError(t("desktop.syncRoots.pickFolderError"));
      return;
    }

    setAdding(root.id);
    try {
      // New owned root mirrors the chosen folder. On-demand when the platform
      // supports placeholders, eager otherwise — same default as the form.
      const downloadMode = supportsOnDemand ? "on_demand" : "eager";
      await cmd.addSyncRoot(root.id, root.name, localPath, root.id, downloadMode);
      onAdded();
    } catch (err) {
      onError(commandErrorMessage(err));
    } finally {
      setAdding(null);
    }
  }

  if (addableRoots.length === 0) return null;

  return (
    <div className="space-y-2">
      <h3 className="text-xs font-medium text-muted-foreground/70">
        {t("desktop.syncRoots.available.title")}
      </h3>
      <ul className="space-y-2">
        {addableRoots.map((root) => (
          <li
            key={root.id}
            className="flex items-center gap-3 rounded-xl border border-dashed
                       border-border bg-card p-3"
          >
            <div className="flex h-9 w-9 shrink-0 items-center justify-center
                            rounded-lg bg-muted/40">
              <FolderOpen className="h-5 w-5 text-muted-foreground/70" />
            </div>
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium text-foreground">{root.name}</p>
            </div>
            <Button
              size="sm"
              onClick={() => add(root)}
              disabled={adding === root.id}
              className="shrink-0 gap-1"
            >
              {adding === root.id
                ? <Check className="h-3.5 w-3.5" />
                : <FolderOpen className="h-3.5 w-3.5" />}
              {t("desktop.syncRoots.available.add")}
            </Button>
          </li>
        ))}
      </ul>
    </div>
  );
}
