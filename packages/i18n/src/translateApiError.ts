import i18next from "i18next";

/**
 * Minimaler Server-Fehler-Shape — kompatibel zu `ApiError` aus
 * `@privasync/api-client`, aber ohne harte Abhaengigkeit, damit das
 * i18n-Paket frei von Client-Imports bleibt.
 */
export interface ServerError {
  error: string;
  code?: string;
}

/**
 * Uebersetzt einen Server-Fehler. Bevorzugt `code` (stabil, sprachneutral),
 * faellt auf den englischen `error`-String zurueck, wenn kein Mapping
 * existiert oder der Code fehlt (alte Server, neue Codes).
 */
export function translateApiError(err: ServerError | undefined | null): string {
  if (!err) return "";
  if (err.code) {
    const key = `errors.${err.code}`;
    const translated = i18next.t(key);
    if (translated !== key) return translated;
  }
  return err.error || "";
}
