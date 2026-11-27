import { ArrowLeft } from "lucide-react";
import { useTranslation } from "@privasync/i18n";
import SettingsTab from "../components/SettingsTab";

interface Props {
  onBack: () => void;
}

/**
 * Settings without a session. Every setting in `SettingsTab` is backed by the
 * local database rather than the server — language, theme, autostart, upload
 * concurrency and exclusions — so none of it needs a login, and those are
 * exactly the knobs someone reaches for before (or instead of) signing in.
 * Reuses `SettingsTab` verbatim so the two entry points cannot drift apart.
 */
export default function PreLoginSettingsPage({ onBack }: Props) {
  const { t } = useTranslation();

  return (
    <div className="flex h-screen flex-col bg-background">
      <header className="flex items-center gap-2 border-b border-border bg-card px-4 py-2">
        <button
          type="button"
          onClick={onBack}
          aria-label={t("desktop.common.back")}
          className="flex items-center gap-1.5 rounded-lg px-2 py-1 text-xs font-medium
                     text-muted-foreground hover:bg-accent/50 hover:text-foreground"
        >
          <ArrowLeft className="h-4 w-4" />
          {t("desktop.common.back")}
        </button>
        <img src="/logo.png" alt="" className="ml-1 h-5 w-5" />
        <span className="text-sm font-semibold text-foreground">PrivaSync</span>
      </header>

      <main className="flex-1 overflow-y-auto p-4">
        <SettingsTab />
      </main>
    </div>
  );
}
