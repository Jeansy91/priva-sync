import { useEffect, useState, useCallback } from "react";
import { Users, FolderOpen, Lock, Check } from "lucide-react";
import { Button } from "@privasync/ui";
import { open as openDialog } from "@tauri-apps/plugin-dialog";
import { useTranslation } from "@privasync/i18n";
import {
  cmd, onSharesChanged,
  type AvailableMount, type SyncRootEntry, type SyncProgressEvent,
} from "../../tauri";
import { commandErrorMessage } from "../../commandError";
import { SyncRootListItem } from "./SyncRootListItem";

interface SharedMountsSectionProps {
  // Already-activated mounts (kind='mount' sync roots). Shown on top of the
  // group, before the not-yet-activated ones — see ADR-0011.
  activeMounts:         SyncRootEntry[];
  progress:             Record<string, Record<string, SyncProgressEvent>>;
  supportsOnDemand:     boolean;
  onToggleDownloadMode: (root: SyncRootEntry) => void;
  onTogglePaused:       (root: SyncRootEntry) => void;
  onRemove:             (root: SyncRootEntry) => void;
  // Called after a mount is activated so the parent reloads its sync-root list
  // (an activated mount becomes a kind='mount' sync root). See ADR-0011.
  onActivated:          () => void;
  onError:              (msg: string) => void;
}

/**
 * The "Shared with me" group: incoming directory shares from other users
 * (ADR-0011). Already-activated mounts render on top via SyncRootListItem;
 * the not-yet-activated ones (derived purely from the server pull) follow
 * under a "available" sub-heading, each with an activate button that asks
 * for a local folder. The whole group disappears when there is nothing to
 * show — neither active nor available.
 */
export function SharedMountsSection(props: SharedMountsSectionProps) {
  const {
    activeMounts, progress, supportsOnDemand,
    onToggleDownloadMode, onTogglePaused, onRemove, onActivated, onError,
  } = props;
  const { t } = useTranslation();
  const [available, setAvailable]   = useState<AvailableMount[]>([]);
  const [activating, setActivating] = useState<string | null>(null);

  const reload = useCallback(async () => {
    try {
      setAvailable(await cmd.listAvailableMounts());
    } catch {
      // Non-critical: the server may be briefly unreachable. The next pull
      // (or a shares_changed event) refreshes the list.
    }
  }, []);

  useEffect(() => {
    reload();
    const unlisten = onSharesChanged(reload);
    return () => { unlisten.then((fn) => fn()); };
  }, [reload]);

  async function activate(mount: AvailableMount) {
    let localPath: string;
    try {
      const selected = await openDialog({ directory: true, multiple: false });
      if (typeof selected !== "string") return;
      localPath = selected;
    } catch {
      onError(t("desktop.syncRoots.pickFolderError"));
      return;
    }

    setActivating(mount.share_id);
    try {
      await cmd.activateMount(
        mount.share_id, mount.name, localPath, mount.can_write, mount.owner_username,
      );
      await reload();
      onActivated();
    } catch (err) {
      onError(commandErrorMessage(err));
    } finally {
      setActivating(null);
    }
  }

  if (activeMounts.length === 0 && available.length === 0) return null;

  // Alphabetical, locale-aware — match the activated lists above.
  const availableSorted = [...available].sort((a, b) => a.name.localeCompare(b.name));

  return (
    <div className="space-y-2">
      <h3 className="flex items-center gap-2 text-sm font-semibold text-foreground">
        <Users className="h-4 w-4 text-muted-foreground" />
        {t("desktop.sharedMounts.title")}
      </h3>

      {activeMounts.length > 0 && (
        <ul className="space-y-2">
          {activeMounts.map((root) => (
            <SyncRootListItem
              key={root.id}
              root={root}
              progress={progress[root.id] ?? {}}
              supportsOnDemand={supportsOnDemand}
              onToggleDownloadMode={onToggleDownloadMode}
              onTogglePaused={onTogglePaused}
              onRemove={onRemove}
            />
          ))}
        </ul>
      )}

      {available.length > 0 && (
        <>
          <h4 className="pt-1 text-xs font-medium text-muted-foreground/70">
            {t("desktop.sharedMounts.availableTitle")}
          </h4>
          <ul className="space-y-2">
            {availableSorted.map((mount) => (
              <li
                key={mount.share_id}
                className="flex items-center gap-3 rounded-xl border border-dashed
                           border-border bg-card p-3"
              >
                <div className="flex h-9 w-9 shrink-0 items-center justify-center
                                rounded-lg bg-muted/40">
                  <Users className="h-5 w-5 text-muted-foreground/70" />
                </div>
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium text-foreground">{mount.name}</p>
                  <p className="truncate text-xs text-muted-foreground/70">
                    {t("desktop.sharedMounts.fromOwner", { owner: mount.owner_username })}
                  </p>
                </div>

                {!mount.can_write && (
                  <span className="shrink-0 flex items-center gap-1 rounded-lg
                                   border border-border bg-muted/40 px-2 py-1
                                   text-xs text-muted-foreground"
                        title={t("desktop.syncRoots.badgeReadOnlyTitle")}>
                    <Lock className="h-3 w-3" />
                    {t("desktop.syncRoots.badgeReadOnly")}
                  </span>
                )}

                <Button
                  size="sm"
                  onClick={() => activate(mount)}
                  disabled={activating === mount.share_id}
                  className="shrink-0 gap-1"
                >
                  {activating === mount.share_id
                    ? <Check className="h-3.5 w-3.5" />
                    : <FolderOpen className="h-3.5 w-3.5" />}
                  {t("desktop.sharedMounts.activate")}
                </Button>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}
