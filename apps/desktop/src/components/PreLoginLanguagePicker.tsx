import { Languages } from "lucide-react";
import { Select } from "@privasync/ui";
import {
  changeLanguage, useTranslation,
  SUPPORTED_LANGUAGES, LANGUAGE_AUTONYMS,
  type SupportedLanguage,
} from "@privasync/i18n";
import { cmd } from "../tauri";

/**
 * Sprach-Dropdown für die SetupPage (vor Login). Persistiert in der
 * LocalDb (gleiche kv-Spalte wie der Toggle in Settings), damit beim
 * nächsten App-Start der Bootstrap in `main.tsx` die Wahl aufgreift.
 */
export default function PreLoginLanguagePicker() {
  const { i18n } = useTranslation();
  const current = (i18n.language?.split("-")[0] ?? "en") as SupportedLanguage;

  async function pick(lang: string) {
    const next = lang as SupportedLanguage;
    if (next === current) return;
    try { await cmd.setLanguage(next); } catch { /* persist best-effort */ }
    await changeLanguage(next);
  }

  return (
    <div className="inline-flex items-center gap-2">
      <Languages className="h-4 w-4 text-muted-foreground/70" />
      <div className="w-32">
        <Select
          value={current}
          onChange={pick}
          options={SUPPORTED_LANGUAGES.map((lang) => ({
            value: lang,
            label: LANGUAGE_AUTONYMS[lang],
          }))}
        />
      </div>
    </div>
  );
}
