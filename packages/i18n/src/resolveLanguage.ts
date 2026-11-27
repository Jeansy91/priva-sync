/**
 * Picks the best-fit language out of a candidate list (e.g. navigator.languages
 * or an OS locale list) against a supported set. Matching is case-insensitive
 * and region-tag aware: "de-AT" matches "de".
 *
 * Falls back to {@link fallback} when no candidate matches — used for OS
 * locales that we don't ship translations for (default EN).
 */
export function resolveLanguage(
  candidates: readonly string[],
  supported: readonly string[],
  fallback: string,
): string {
  const supportedLower = supported.map((s) => s.toLowerCase());
  for (const raw of candidates) {
    const primary = raw.toLowerCase().split("-")[0];
    const idx = supportedLower.indexOf(primary);
    if (idx !== -1) return supported[idx];
  }
  return fallback;
}
