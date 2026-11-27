import {
  FolderOpen, Trash2, RefreshCw, Download, Cloud, HardDrive,
  Lock, ShieldCheck, Pause, Play,
} from "lucide-react";
import { useTranslation } from "@privasync/i18n";
import { type SyncRootEntry, type SyncProgressEvent } from "../../tauri";
import { isVolumeRoot } from "../../volumeRoot";

interface SyncRootListItemProps {
  root:             SyncRootEntry;
  progress:         Record<string, SyncProgressEvent>;
  supportsOnDemand: boolean;
  onToggleDownloadMode: (root: SyncRootEntry) => void;
  onTogglePaused:       (root: SyncRootEntry) => void;
  onRemove:             (root: SyncRootEntry) => void;
}

/**
 * One row in the sync-root list. Shows the root name + current local
 * path, surfaces active transfer progress (uploads/downloads/deletes)
 * with the right icon + progress bar, and offers the per-root
 * actions: download-mode toggle, pause/resume, remove.
 */
export function SyncRootListItem(props: SyncRootListItemProps) {
  const { root, progress, supportsOnDemand, onToggleDownloadMode, onTogglePaused, onRemove } = props;
  const { t } = useTranslation();

  const active = Object.values(progress ?? {});
  // The most recently started transfer, i.e. the last slot added to the map —
  // object keys keep insertion order, and a slot is removed on "file_done".
  //
  // Picking the highest `current` used to mean the same thing, back when the
  // counter advanced per *started* task. It counts completed files now, so
  // every file in flight carries the same value and the tie-break pinned the
  // label to whichever of the eight had been there longest.
  const progressMeta = active.length > 0 ? active[active.length - 1] : null;
  const pct = progressMeta
    ? Math.round((progressMeta.current / progressMeta.total) * 100)
    : 0;
  const isOnDemand = root.download_mode === "on_demand";

  return (
    <li
      className={`rounded-xl bg-card border shadow-xs overflow-hidden transition-opacity ${
        root.paused
          ? "border-amber-200 dark:border-amber-900 opacity-60"
          : "border-border"
      }`}
    >
      <div className="flex items-center gap-3 p-3">
        <div className={`flex h-9 w-9 shrink-0 items-center justify-center
                         rounded-lg ${
                           root.paused
                             ? "bg-amber-50 dark:bg-amber-950/40"
                             : active.length > 0 ? "bg-blue-100 dark:bg-blue-900/40" : "bg-blue-50 dark:bg-blue-950/40"
                         }`}>
          <FolderOpen className={`h-5 w-5 ${
            root.paused
              ? "text-amber-600 dark:text-amber-400"
              : active.length > 0 ? "text-blue-700 dark:text-blue-300" : "text-blue-600 dark:text-blue-400"
          }`} />
        </div>
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium text-foreground">{root.name}</p>
          {progressMeta ? (
            <div className="mt-0.5 space-y-0.5">
              <div className="flex items-center gap-1.5">
                {progressMeta.action === "upload"
                  ? <RefreshCw className="h-3 w-3 text-blue-500 shrink-0 animate-spin" />
                  : progressMeta.action === "download"
                    ? <Download className="h-3 w-3 text-green-500 shrink-0" />
                    : progressMeta.action === "delete"
                      ? <Trash2 className="h-3 w-3 text-red-500 shrink-0" />
                      : progressMeta.action === "permissions"
                        ? <ShieldCheck className="h-3 w-3 text-purple-500 shrink-0" />
                        : <RefreshCw className="h-3 w-3 text-muted-foreground shrink-0 animate-spin" />}
                <span className="truncate text-xs text-blue-600 dark:text-blue-400 font-medium">
                  {progressMeta.file.split("/").pop() ?? progressMeta.file}
                </span>
              </div>
              <p className="text-[10px] text-muted-foreground/70">
                {progressMeta.current}/{progressMeta.total}
              </p>
            </div>
          ) : (
            <p className="truncate text-xs text-muted-foreground/70">{root.local_path}</p>
          )}
        </div>

        {root.paused && (
          <span className="shrink-0 flex items-center gap-1 rounded-lg
                           border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-2 py-1
                           text-xs font-medium text-amber-700 dark:text-amber-300"
                title={t("desktop.syncRoots.badgePausedTitle")}>
            <Pause className="h-3 w-3" />
            {t("desktop.syncRoots.badgePaused")}
          </span>
        )}

        {root.read_only && (
          <span className="shrink-0 flex items-center gap-1 rounded-lg
                           border border-border bg-muted/40 px-2 py-1
                           text-xs text-muted-foreground"
                title={t("desktop.syncRoots.badgeReadOnlyTitle")}>
            <Lock className="h-3 w-3" />
            {t("desktop.syncRoots.badgeReadOnly")}
          </span>
        )}

        {/* A drive root cannot carry Cloud Files placeholders, so on-demand is
            not on offer there — shown as a fixed badge rather than hidden, so
            the mode is still visible and its reason readable (ADR-0057). */}
        {supportsOnDemand && !root.read_only && isVolumeRoot(root.local_path) && (
          <span
            title={t("desktop.syncRoots.volumeRootEagerOnly")}
            className="shrink-0 flex items-center gap-1 rounded-lg border border-border
                       bg-muted/40 px-2 py-1 text-xs text-muted-foreground"
          >
            <HardDrive className="h-3 w-3" /> {t("desktop.syncRoots.toggleEager")}
          </span>
        )}

        {supportsOnDemand && !root.read_only && !isVolumeRoot(root.local_path) && (
          <button
            onClick={() => onToggleDownloadMode(root)}
            title={isOnDemand
              ? t("desktop.syncRoots.toggleToEager")
              : t("desktop.syncRoots.toggleToOnDemand")}
            className={`shrink-0 flex items-center gap-1 rounded-lg border px-2 py-1
                        text-xs transition-colors ${
                          isOnDemand
                            ? "border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400 hover:bg-blue-100 dark:hover:bg-blue-900/40"
                            : "border-border bg-muted/40 text-muted-foreground hover:bg-accent"
                        }`}
          >
            {isOnDemand
              ? <><Cloud className="h-3 w-3" /> {t("desktop.syncRoots.toggleOnDemand")}</>
              : <><HardDrive className="h-3 w-3" /> {t("desktop.syncRoots.toggleEager")}</>}
          </button>
        )}

        <button
          onClick={() => onTogglePaused(root)}
          title={root.paused
            ? t("desktop.syncRoots.resumeTitle")
            : t("desktop.syncRoots.pauseTitle")}
          className={`shrink-0 rounded-sm p-1 transition-colors ${
            root.paused
              ? "text-amber-600 dark:text-amber-400 hover:bg-amber-50 dark:hover:bg-amber-950/40"
              : "text-muted-foreground/50 hover:bg-accent hover:text-foreground"
          }`}
        >
          {root.paused
            ? <Play className="h-4 w-4" />
            : <Pause className="h-4 w-4" />}
        </button>

        <button
          onClick={() => onRemove(root)}
          title={t("desktop.syncRoots.removeTitle")}
          className="shrink-0 rounded-sm p-1 text-muted-foreground/50 hover:bg-red-50 dark:hover:bg-red-950/40
                     hover:text-red-500 transition-colors"
        >
          <Trash2 className="h-4 w-4" />
        </button>
      </div>

      {progressMeta && (
        <div className="h-1 w-full bg-muted">
          <div
            className={`h-full transition-all duration-300 ${
              progressMeta.action === "download" ? "bg-green-500" : "bg-blue-500"
            }`}
            style={{ width: `${pct}%` }}
          />
        </div>
      )}
    </li>
  );
}
