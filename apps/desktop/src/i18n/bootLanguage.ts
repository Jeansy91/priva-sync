/**
 * Baut die Kandidatenliste für `setupI18n` aus persistiertem DB-Wert
 * + Browser/OS-Locales. resolveLanguage in @privasync/i18n filtert
 * unbekannte Locales selbst raus → wir reichen alles in Reihenfolge durch.
 */
export function bootLanguageCandidates(
  persisted:          string | null,
  navigatorLanguages: readonly string[],
  navigatorLanguage:  string,
): string[] {
  const out: string[] = []
  if (persisted && persisted.trim().length > 0) {
    out.push(persisted.trim())
  }
  if (navigatorLanguages.length > 0) {
    out.push(...navigatorLanguages)
  } else if (navigatorLanguage) {
    out.push(navigatorLanguage)
  }
  return out
}
