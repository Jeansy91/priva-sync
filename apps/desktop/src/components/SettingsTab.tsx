import { useState, useEffect } from "react";
import { getVersion } from "@tauri-apps/api/app";
import { RefreshCw, Download, Loader2, Check, Power, Languages, Cog, Info, SunMoon } from "lucide-react";
import { useTranslation, changeLanguage, LANGUAGE_AUTONYMS, type SupportedLanguage } from "@privasync/i18n";
import { cmd } from "../tauri";
import { commandErrorMessage } from "../commandError";
import { Select, Slider, Switch, Button, getThemePreference, setThemePreference, type ThemePreference } from "@privasync/ui";
import ModalOverlay from "./ModalOverlay";
import { SettingsCard } from "./SettingsCard";
import { ExclusionsCard } from "./ExclusionsCard";

export default function SettingsTab() {
  const { t, i18n } = useTranslation();
  const [error, setError]                     = useState("");
  const [concurrency, setConcurrency]         = useState(3);
  const [savingConcurrency, setSaving]        = useState(false);
  const [autostart, setAutostartState]        = useState(false);
  const [savingAutostart, setSavingAutostart] = useState(false);
  const [savingLang, setSavingLang]           = useState(false);
  const [theme, setTheme]                     = useState<ThemePreference>(() => getThemePreference());
  const [checkingUpdate, setCheckingUpdate]   = useState(false);
  const [appVersion, setAppVersion]           = useState("");
  const [updateStatus, setUpdateStatus]       = useState<
    { kind: "idle" } | { kind: "current" } | { kind: "available"; version: string }
  >({ kind: "idle" });

  useEffect(() => {
    cmd.getUploadConcurrency().then(setConcurrency).catch(console.error);
    cmd.getAutostart().then(setAutostartState).catch(console.error);
    getVersion().then(setAppVersion).catch(console.error);
  }, []);

  async function handleAutostartChange(enabled: boolean) {
    setAutostartState(enabled);
    setSavingAutostart(true);
    setError("");
    try {
      await cmd.setAutostart(enabled);
    } catch (err) {
      setAutostartState(!enabled);
      setError(commandErrorMessage(err));
    } finally {
      setSavingAutostart(false);
    }
  }

  async function handleConcurrencyChange(v: number) {
    setConcurrency(v);
    setSaving(true);
    try {
      await cmd.setUploadConcurrency(v);
    } catch (err) {
      setError(commandErrorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  async function handleLanguageChange(lang: string) {
    const next = lang as SupportedLanguage;
    if (next === i18n.language || savingLang) return;
    setError("");
    setSavingLang(true);
    try {
      await cmd.setLanguage(next);
      await changeLanguage(next);
    } catch (err) {
      setError(commandErrorMessage(err));
    } finally {
      setSavingLang(false);
    }
  }

  function handleThemeChange(value: string) {
    const next = value as ThemePreference;
    setThemePreference(next);
    setTheme(next);
  }

  async function handleCheckUpdate() {
    setCheckingUpdate(true);
    setError("");
    try {
      const info = await cmd.checkForUpdate();
      setUpdateStatus(info
        ? { kind: "available", version: info.version }
        : { kind: "current" });
    } catch (err) {
      setError(commandErrorMessage(err));
    } finally {
      setCheckingUpdate(false);
    }
  }

  async function handleInstallUpdate() {
    setCheckingUpdate(true);
    setError("");
    try {
      await cmd.installUpdate();
    } catch (err) {
      setError(commandErrorMessage(err));
      setCheckingUpdate(false);
    }
  }

  const currentLang = (i18n.language?.split("-")[0] ?? "en") as SupportedLanguage;

  return (
    <div className="space-y-4">
      <h2 className="text-sm font-semibold text-foreground">{t("desktop.settings.title")}</h2>

      {error && (
        <p className="rounded-lg bg-red-50 dark:bg-red-950/40 px-3 py-2 text-xs text-red-700 dark:text-red-300">{error}</p>
      )}

      <SettingsCard
        title={t("desktop.settings.sectionSync")}
        description={t("desktop.settings.syncDescription")}
        icon={<RefreshCw className="h-4 w-4" />}
      >
        <div className="space-y-2">
          <label className="block text-xs font-medium text-foreground">
            {t("desktop.settings.parallelUploads")}
          </label>
          <Slider
            value={concurrency}
            min={1}
            max={8}
            step={1}
            saving={savingConcurrency}
            onChange={handleConcurrencyChange}
          />
        </div>
      </SettingsCard>

      <ExclusionsCard onError={setError} />

      <SettingsCard
        title={t("desktop.settings.sectionLanguage")}
        description={t("desktop.settings.language.description")}
        icon={<Languages className="h-4 w-4" />}
      >
        <Select
          value={currentLang}
          onChange={handleLanguageChange}
          disabled={savingLang}
          options={[
            { value: "de", label: LANGUAGE_AUTONYMS.de },
            { value: "en", label: LANGUAGE_AUTONYMS.en },
          ]}
        />
      </SettingsCard>

      <SettingsCard
        title={t("settings.theme")}
        description={t("settings.themeDescription")}
        icon={<SunMoon className="h-4 w-4" />}
      >
        <Select
          value={theme}
          onChange={handleThemeChange}
          options={[
            { value: "system", label: t("settings.themeSystem") },
            { value: "light",  label: t("settings.themeLight") },
            { value: "dark",   label: t("settings.themeDark") },
          ]}
        />
      </SettingsCard>

      <SettingsCard
        title={t("desktop.settings.sectionSystem")}
        description={t("desktop.settings.autostartDescription")}
        icon={<Cog className="h-4 w-4" />}
      >
        <div className="flex items-center justify-between">
          <span className="flex items-center gap-1.5 text-xs font-medium text-foreground">
            <Power className="h-3.5 w-3.5 text-muted-foreground/70" />
            {t("desktop.settings.autostart")}
          </span>
          <Switch
            checked={autostart}
            disabled={savingAutostart}
            onChange={handleAutostartChange}
          />
        </div>
      </SettingsCard>

      <SettingsCard
        title={t("desktop.settings.sectionAbout")}
        description={t("desktop.settings.aboutDescription")}
        icon={<Info className="h-4 w-4" />}
      >
        {updateStatus.kind === "available" ? (
          <Button
            variant="outline"
            onClick={handleInstallUpdate}
            disabled={checkingUpdate}
            className="w-full justify-start gap-2 border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/40 text-blue-700 dark:text-blue-300 hover:bg-blue-100 dark:hover:bg-blue-900/40"
          >
            {checkingUpdate
              ? <Loader2 className="h-4 w-4 animate-spin" />
              : <Download className="h-4 w-4" />}
            {checkingUpdate
              ? t("desktop.settings.updateInstalling")
              : t("desktop.settings.updateInstall", { version: updateStatus.version })}
          </Button>
        ) : (
          <Button
            variant="default"
            onClick={handleCheckUpdate}
            disabled={checkingUpdate}
            className="w-full justify-start gap-2"
          >
            {checkingUpdate
              ? <Loader2 className="h-4 w-4 animate-spin" />
              : updateStatus.kind === "current"
                ? <Check className="h-4 w-4" />
                : <Download className="h-4 w-4" />}
            {checkingUpdate
              ? t("desktop.settings.updateChecking")
              : updateStatus.kind === "current"
                ? t("desktop.settings.updateUpToDate")
                : t("desktop.settings.updateCheck")}
          </Button>
        )}

        <p className="pt-1 text-center text-xs text-muted-foreground/70">
          {t("desktop.settings.version", { version: appVersion })}
        </p>
      </SettingsCard>

      {savingLang && (
        <ModalOverlay>
          <div className="flex flex-col items-center gap-3 rounded-2xl bg-card px-6 py-5 shadow-xl">
            <Loader2 className="h-6 w-6 animate-spin text-blue-600 dark:text-blue-400" />
            <p className="text-xs font-medium text-muted-foreground">
              {t("desktop.settings.language.switching")}
            </p>
          </div>
        </ModalOverlay>
      )}
    </div>
  );
}
