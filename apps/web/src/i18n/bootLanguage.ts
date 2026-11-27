/**
 * Reihenfolge der Sprachkandidaten für `setupI18n` vor dem Login:
 * localStorage > navigator.languages > navigator.language.
 * resolveLanguage filtert unbekannte Locales selbst → Fallback EN.
 */
export function bootLanguageCandidates(
  stored:             string | null,
  navigatorLanguages: readonly string[],
  navigatorLanguage:  string,
): string[] {
  const out: string[] = []
  if (stored && stored.trim().length > 0) out.push(stored.trim())
  if (navigatorLanguages.length > 0) out.push(...navigatorLanguages)
  else if (navigatorLanguage)        out.push(navigatorLanguage)
  return out
}

export const STORED_LANG_KEY = 'privasync.lang'
