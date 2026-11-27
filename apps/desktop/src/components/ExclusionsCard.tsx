import { useEffect, useState } from "react";
import { FileX, Check, Loader2, RotateCcw, AlertTriangle, Files, ChevronDown, ChevronRight } from "lucide-react";
import { Button } from "@privasync/ui";
import { useTranslation } from "@privasync/i18n";
import { cmd, type PatternError, type ExcludedPreview } from "../tauri";
import { commandErrorMessage } from "../commandError";
import { SettingsCard } from "./SettingsCard";
import { GitignoreEditor } from "./GitignoreEditor";

interface ExclusionsCardProps {
  onError: (msg: string) => void;
}

/**
 * Global sync-exclusion editor. One pattern list for every sync root,
 * gitignore syntax, seeded with sensible defaults (OS junk, temp/lock files,
 * database sidecars). Matching files are never uploaded — downloads are
 * unaffected. Lets the user edit the list and restore the built-in defaults.
 */
export function ExclusionsCard({ onError }: ExclusionsCardProps) {
  const { t } = useTranslation();
  const [patterns, setPatterns] = useState("");
  const [saved, setSaved]       = useState("");
  const [loading, setLoading]   = useState(true);
  const [saving, setSaving]     = useState(false);
  const [justSaved, setJustSaved] = useState(false);
  const [errors, setErrors]     = useState<PatternError[]>([]);
  const [preview, setPreview]   = useState<ExcludedPreview | null>(null);
  const [showSample, setShowSample] = useState(false);

  function refreshPreview() {
    cmd.countExcludedFiles().then(setPreview).catch(() => setPreview(null));
  }

  useEffect(() => {
    cmd.getIgnorePatterns()
      .then((p) => { setPatterns(p); setSaved(p); refreshPreview(); })
      .catch((err) => onError(commandErrorMessage(err)))
      .finally(() => setLoading(false));
  }, [onError]);

  // Validate via the real gitignore engine, debounced so each keystroke does
  // not round-trip to Rust. Errors block saving.
  useEffect(() => {
    if (loading) return;
    const id = setTimeout(() => {
      cmd.validateIgnorePatterns(patterns)
        .then(setErrors)
        .catch(() => setErrors([]));
    }, 300);
    return () => clearTimeout(id);
  }, [patterns, loading]);

  const dirty = patterns !== saved;
  const errorLines = new Set(errors.map((e) => e.line));

  async function save() {
    setSaving(true);
    try {
      await cmd.setIgnorePatterns(patterns);
      setSaved(patterns);
      setJustSaved(true);
      refreshPreview();
      setTimeout(() => setJustSaved(false), 2000);
    } catch (err) {
      onError(commandErrorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  async function restoreDefaults() {
    try {
      const def = await cmd.getDefaultIgnorePatterns();
      setPatterns(def);
    } catch (err) {
      onError(commandErrorMessage(err));
    }
  }

  return (
    <SettingsCard
      title={t("desktop.settings.exclusions.title")}
      description={t("desktop.settings.exclusions.description")}
      icon={<FileX className="h-4 w-4" />}
    >
      <div className="space-y-2">
        <GitignoreEditor
          value={loading ? "" : patterns}
          disabled={loading || saving}
          onChange={setPatterns}
          errorLines={errorLines}
          rows={10}
          placeholder={t("desktop.settings.exclusions.placeholder")}
        />
        <p className="text-[11px] text-muted-foreground/70">
          {t("desktop.settings.exclusions.hint")}
        </p>

        {errors.length > 0 && (
          <ul className="space-y-1 rounded-lg bg-red-50 dark:bg-red-950/40 px-3 py-2">
            {errors.map((e) => (
              <li key={e.line} className="flex items-start gap-1.5 text-[11px] text-red-700 dark:text-red-300">
                <AlertTriangle className="h-3 w-3 mt-0.5 shrink-0" />
                <span>{t("desktop.settings.exclusions.errorLine", { line: e.line, message: e.message })}</span>
              </li>
            ))}
          </ul>
        )}

        {preview && (
          preview.total === 0 ? (
            <p className="text-[11px] text-muted-foreground/70">
              {t("desktop.settings.exclusions.noneExcluded")}
            </p>
          ) : (
            <div className="text-xs text-muted-foreground">
              <button
                type="button"
                onClick={() => setShowSample((s) => !s)}
                className="flex items-center gap-1 hover:text-foreground transition-colors"
              >
                {showSample ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
                <Files className="h-3.5 w-3.5" />
                {t(preview.total === 1
                  ? "desktop.settings.exclusions.excludedCountOne"
                  : "desktop.settings.exclusions.excludedCountOther", { count: preview.total })}
              </button>
              {showSample && (
                <ul className="mt-1 max-h-40 overflow-auto rounded-lg bg-muted/40 px-3 py-2
                               font-mono text-[11px] space-y-0.5">
                  {preview.sample.map((p) => (
                    <li key={p} className="truncate" title={p}>{p}</li>
                  ))}
                  {preview.total > preview.sample.length && (
                    <li className="text-muted-foreground/60">
                      {t("desktop.settings.exclusions.andMore", { count: preview.total - preview.sample.length })}
                    </li>
                  )}
                </ul>
              )}
            </div>
          )
        )}

        <div className="flex items-center justify-between pt-1">
          <button
            type="button"
            onClick={restoreDefaults}
            disabled={loading || saving}
            className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground
                       transition-colors disabled:opacity-50"
          >
            <RotateCcw className="h-3.5 w-3.5" />
            {t("desktop.settings.exclusions.restoreDefaults")}
          </button>
          <Button size="sm" onClick={save} disabled={loading || saving || !dirty || errors.length > 0} className="gap-1">
            {saving
              ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
              : justSaved
                ? <Check className="h-3.5 w-3.5" />
                : null}
            {justSaved && !dirty
              ? t("desktop.settings.exclusions.saved")
              : t("desktop.settings.exclusions.save")}
          </Button>
        </div>
      </div>
    </SettingsCard>
  );
}
