import { Loader2 } from "lucide-react";
import { useTranslation } from "@privasync/i18n";
import type { UpdateStage } from "../tauri";
import { formatBytes } from "./sync-roots/syncRootsHelpers";

/**
 * Full-screen cover for the startup auto-update (ADR-0060). Everything else
 * stays mounted behind it — the login form keeps whatever was typed into it —
 * but nothing of it is visible while an update is being decided or installed.
 *
 * Rendered only for a non-idle stage; `idle` is the caller's cue to drop it.
 */
export default function UpdateGate({ stage }: { stage: Exclude<UpdateStage, { stage: "idle" }> }) {
  const { t } = useTranslation();

  const total      = stage.stage === "downloading" ? stage.total : null;
  const downloaded = stage.stage === "downloading" ? stage.downloaded : 0;
  // Without a Content-Length there is nothing to fill a bar with — the byte
  // counter below carries the progress on its own in that case.
  const percent    = total && total > 0 ? Math.min(100, Math.round((downloaded / total) * 100)) : null;

  const label =
    stage.stage === "checking"
      ? t("desktop.update.gate.checking")
      : stage.stage === "downloading"
        ? t("desktop.update.gate.downloading", { version: stage.version })
        : t("desktop.update.gate.installing", { version: stage.version });

  return (
    <div className="fixed inset-0 z-50 flex flex-col items-center justify-center bg-background px-4">
      <div className="mb-8 flex flex-col items-center gap-2">
        <img src="/logo.png" alt="PrivaSync" className="h-20 w-20" />
        <h1 className="text-2xl font-bold text-foreground">PrivaSync</h1>
      </div>

      <div className="flex w-full max-w-sm flex-col items-center gap-3">
        <div className="flex items-center gap-2 text-sm text-foreground">
          <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
          <span>{label}</span>
        </div>

        {stage.stage === "downloading" && (
          <>
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-accent">
              <div
                className="h-full rounded-full bg-blue-500 transition-[width] duration-200"
                style={{ width: `${percent ?? 100}%` }}
              />
            </div>
            <p className="text-xs text-muted-foreground">
              {total
                ? `${formatBytes(downloaded)} / ${formatBytes(total)}`
                : formatBytes(downloaded)}
            </p>
          </>
        )}

        {stage.stage !== "checking" && (
          <p className="text-xs text-muted-foreground">{t("desktop.update.gate.restartHint")}</p>
        )}
      </div>
    </div>
  );
}
