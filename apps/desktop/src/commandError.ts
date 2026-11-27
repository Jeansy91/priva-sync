import { translateApiError, type ServerError } from "@privasync/i18n";

/**
 * User-facing text for a rejected Tauri command. Commands returning the Rust
 * `CommandError` reject with `{error, code}`, whose code is translated; the
 * rest reject with a plain string, shown as-is.
 */
export function commandErrorMessage(err: unknown): string {
  if (typeof err === "object" && err !== null && typeof (err as ServerError).error === "string") {
    return translateApiError(err as ServerError);
  }
  return String(err);
}
