import i18next, { type i18n as I18n } from "i18next";
import { initReactI18next } from "react-i18next";
import de from "./locales/de.json";
import en from "./locales/en.json";
import { resolveLanguage } from "./resolveLanguage";

export const SUPPORTED_LANGUAGES = ["de", "en"] as const;
export type SupportedLanguage = (typeof SUPPORTED_LANGUAGES)[number];

export const FALLBACK_LANGUAGE: SupportedLanguage = "en";

/**
 * Autonyme — jede Sprache in ihrem eigenen Namen. Wird in
 * Sprach-Pickern verwendet, damit der Eintrag unabhängig von der
 * gerade aktiven UI-Sprache lesbar bleibt.
 */
export const LANGUAGE_AUTONYMS: Record<SupportedLanguage, string> = {
  de: "Deutsch",
  en: "English",
};

/**
 * Initialise i18next once per app. Call from the entrypoint with the
 * candidate languages: explicit user preference first (e.g. from `/me`),
 * then OS / browser locales. Unknown languages fall back to English.
 */
export function setupI18n(candidates: readonly string[]): I18n {
  const lang = resolveLanguage(candidates, SUPPORTED_LANGUAGES, FALLBACK_LANGUAGE);
  i18next.use(initReactI18next).init({
    resources: {
      de: { translation: de },
      en: { translation: en },
    },
    lng: lang,
    fallbackLng: FALLBACK_LANGUAGE,
    interpolation: { escapeValue: false },
    returnNull: false,
  });
  return i18next;
}

/** Switch language at runtime — used by the language picker in Settings. */
export async function changeLanguage(lang: SupportedLanguage): Promise<void> {
  await i18next.changeLanguage(lang);
}
