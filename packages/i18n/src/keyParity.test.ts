import { describe, it, expect } from "vitest";
import de from "./locales/de.json";
import en from "./locales/en.json";

type Json = Record<string, unknown>;

function flatKeys(obj: Json, prefix = ""): string[] {
  const out: string[] = [];
  for (const [k, v] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === "object" && !Array.isArray(v)) {
      out.push(...flatKeys(v as Json, path));
    } else {
      out.push(path);
    }
  }
  return out.sort();
}

describe("locale key parity", () => {
  it("de.json and en.json expose identical key sets", () => {
    const deKeys = flatKeys(de as Json);
    const enKeys = flatKeys(en as Json);
    const onlyInDe = deKeys.filter((k) => !enKeys.includes(k));
    const onlyInEn = enKeys.filter((k) => !deKeys.includes(k));
    expect({ onlyInDe, onlyInEn }).toEqual({ onlyInDe: [], onlyInEn: [] });
  });

  // Source of truth: server emits these audit event types (see
  // apps/server/internal/audit/audit.go + handler call-sites). Every type that
  // can appear in the audit log must be translatable in both locales,
  // otherwise the UI shows the raw key (see issue: untranslated audit events).
  const SERVER_AUDIT_EVENTS = [
    "auth.login.success",
    "auth.login.fail",
    "auth.login.lockout",
    "auth.logout",
    "auth.token_refresh",
    "auth.password_changed",
    "auth.password_reset",
    "auth.username_changed",
    "auth.email_change_requested",
    "auth.email_changed",
    "auth.2fa_enabled",
    "auth.2fa_disabled",
    "files.deleted",
    "shares.created",
    "shares.deleted",
    "admin.user_created",
    "admin.user_updated",
    "admin.user_deleted",
    "admin.quota_set",
    "admin.quota_recomputed",
    "admin.user_unlocked",
  ] as const;

  it.each(["de", "en"] as const)(
    "%s locale has a label for every server audit event",
    (lang) => {
      const locale = (lang === "de" ? de : en) as Json;
      const events = ((locale.admin as Json)?.auditLog as Json)?.events as
        | Record<string, string>
        | undefined;
      const missing = SERVER_AUDIT_EVENTS.filter(
        (ev) => !events || typeof events[ev] !== "string" || events[ev].trim() === "",
      );
      expect(missing).toEqual([]);
    },
  );

  it("no key resolves to an empty string", () => {
    const empty = (obj: Json, prefix = ""): string[] => {
      const out: string[] = [];
      for (const [k, v] of Object.entries(obj)) {
        const path = prefix ? `${prefix}.${k}` : k;
        if (v && typeof v === "object" && !Array.isArray(v)) {
          out.push(...empty(v as Json, path));
        } else if (typeof v === "string" && v.trim() === "") {
          out.push(path);
        }
      }
      return out;
    };
    expect({ deEmpty: empty(de as Json), enEmpty: empty(en as Json) })
      .toEqual({ deEmpty: [], enEmpty: [] });
  });
});
