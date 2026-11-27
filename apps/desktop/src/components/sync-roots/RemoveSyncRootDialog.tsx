import { Trash2 } from "lucide-react";
import { Button } from "@privasync/ui";
import { Trans, useTranslation } from "@privasync/i18n";
import { type SyncRootEntry, type RemoveSyncRootOptions } from "../../tauri";
import ModalOverlay from "../ModalOverlay";
import { formatBytes } from "./syncRootsHelpers";

interface RemoveSyncRootDialogProps {
  root:         SyncRootEntry | null
  options:      RemoveSyncRootOptions | null
  busy:         boolean
  info:         string
  onClose:      () => void
  onConfirm:    () => void
  onDownloadFirst: () => void
}

/**
 * Destructive confirmation for removing a local sync-root binding.
 * When dehydrated placeholders exist, offers an extra "download all
 * first" button that flips the root to eager mode and forces a sync
 * before the actual removal.
 */
export function RemoveSyncRootDialog(props: RemoveSyncRootDialogProps) {
  const { root, options, busy, info, onClose, onConfirm, onDownloadFirst } = props;
  const { t } = useTranslation();

  if (!root) return null;

  return (
    <ModalOverlay onDismiss={busy ? undefined : onClose}>
      <div className="w-md rounded-2xl bg-card p-5 shadow-xl space-y-4">
        <div className="flex items-start gap-3">
          <div className="flex h-9 w-9 shrink-0 items-center justify-center
                          rounded-full bg-red-100 dark:bg-red-900/40">
            <Trash2 className="h-4 w-4 text-red-600 dark:text-red-400" />
          </div>
          <div className="min-w-0 flex-1">
            <p className="text-sm font-semibold text-foreground">
              {t("desktop.syncRoots.remove.title")}
            </p>
            <p className="mt-1 text-xs text-muted-foreground">
              <Trans
                i18nKey="desktop.syncRoots.remove.subtitle"
                values={{ name: root.name }}
                components={[<span className="font-medium text-foreground" />]}
              />
            </p>
            {options === null ? (
              <p className="mt-2 text-xs text-muted-foreground/70">{t("desktop.syncRoots.remove.checking")}</p>
            ) : options.dehydrated_count > 0 ? (
              <p className="mt-2 text-xs text-amber-700 dark:text-amber-300 bg-amber-50 dark:bg-amber-950/40 rounded-lg px-2 py-1.5">
                {t(
                  options.dehydrated_count === 1
                    ? "desktop.syncRoots.remove.dehydratedOne"
                    : "desktop.syncRoots.remove.dehydratedOther",
                  {
                    count: options.dehydrated_count,
                    size:  formatBytes(options.dehydrated_bytes),
                  },
                )}
              </p>
            ) : null}
            {info && (
              <p className="mt-2 text-xs text-blue-700 dark:text-blue-300 bg-blue-50 dark:bg-blue-950/40 rounded-lg px-2 py-1.5">
                {info}
              </p>
            )}
          </div>
        </div>

        <div className="space-y-2 pt-1">
          {options && !info && (
            <>
              <Button
                variant="destructive"
                size="sm"
                disabled={busy}
                onClick={onConfirm}
                className="w-full rounded-lg"
              >
                {t("desktop.syncRoots.remove.action")}
              </Button>

              {options.dehydrated_count > 0 && root.download_mode !== "eager" && (
                <button
                  disabled={busy}
                  onClick={onDownloadFirst}
                  className="w-full rounded-lg border border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/40 px-3 py-2
                             text-left text-xs font-medium text-blue-700 dark:text-blue-300 hover:bg-blue-100 dark:hover:bg-blue-900/40
                             disabled:opacity-40"
                >
                  <span className="block">{t("desktop.syncRoots.remove.downloadFirst")}</span>
                  <span className="block text-[11px] font-normal text-blue-700/80 dark:text-blue-300/80">
                    {t("desktop.syncRoots.remove.downloadFirstSubtitle")}
                  </span>
                </button>
              )}
            </>
          )}
        </div>

        <div className="flex justify-end pt-1">
          <Button variant="outline" size="sm" onClick={onClose} className="text-muted-foreground">
            {info
              ? t("desktop.syncRoots.remove.close")
              : t("desktop.syncRoots.remove.cancel")}
          </Button>
        </div>
      </div>
    </ModalOverlay>
  );
}
