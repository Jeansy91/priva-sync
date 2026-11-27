import { useState } from "react";
import { Download, Loader2 } from "lucide-react";
import { Button } from "@privasync/ui";
import { useTranslation } from "@privasync/i18n";
import { cmd, type UpdateInfo } from "../tauri";
import { commandErrorMessage } from "../commandError";

interface Props {
  info:      UpdateInfo;
}

export default function UpdateBanner({ info }: Props) {
  const { t } = useTranslation();
  const [installing, setInstalling] = useState(false);
  const [error, setError]           = useState<string | null>(null);

  async function handleInstall() {
    setInstalling(true);
    setError(null);
    try {
      await cmd.installUpdate();
      // The process restarts on success; we generally don't reach this line.
    } catch (e) {
      setError(commandErrorMessage(e));
      setInstalling(false);
    }
  }

  return (
    <div className="flex items-center gap-3 border-b border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/40 px-4 py-2.5 text-sm">
      <Download className="h-4 w-4 shrink-0 text-blue-600 dark:text-blue-400" />
      <div className="min-w-0 flex-1">
        <p className="font-medium text-blue-900 dark:text-blue-200">
          {t("desktop.update.available", { version: info.version })}
          <span className="ml-1.5 text-xs font-normal text-blue-600 dark:text-blue-400">
            {t("desktop.update.installed", { current: info.current })}
          </span>
        </p>
        {info.notes && (
          <p className="mt-0.5 line-clamp-2 text-xs text-blue-700 dark:text-blue-300">{info.notes}</p>
        )}
        {error && (
          <p className="mt-1 text-xs text-red-600 dark:text-red-400">
            {t("desktop.update.error", { message: error })}
          </p>
        )}
      </div>
      <Button size="sm" onClick={handleInstall} disabled={installing} className="shrink-0 gap-1.5">
        {installing
          ? <Loader2 className="h-3 w-3 animate-spin" />
          : <Download className="h-3 w-3" />}
        {installing ? t("desktop.update.installing") : t("desktop.update.install")}
      </Button>
    </div>
  );
}
