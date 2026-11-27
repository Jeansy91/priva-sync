import { describe, it, expect, beforeAll } from "vitest";
import i18next from "i18next";
import { translateApiError } from "./translateApiError";

beforeAll(async () => {
  await i18next.init({
    lng: "de",
    fallbackLng: "de",
    resources: {
      de: { translation: { errors: { INVALID_CREDENTIALS: "Falscher Login" } } },
    },
  });
});

describe("translateApiError", () => {
  it("uebersetzt bekannten Code", () => {
    expect(translateApiError({ error: "invalid credentials", code: "INVALID_CREDENTIALS" }))
      .toBe("Falscher Login");
  });

  it("faellt auf error-String zurueck wenn Code unbekannt", () => {
    expect(translateApiError({ error: "weird thing", code: "NOT_MAPPED_YET" }))
      .toBe("weird thing");
  });

  it("faellt auf error-String zurueck wenn code fehlt", () => {
    expect(translateApiError({ error: "legacy server" }))
      .toBe("legacy server");
  });

  it("liefert leeren String fuer null/undefined", () => {
    expect(translateApiError(null)).toBe("");
    expect(translateApiError(undefined)).toBe("");
  });
});
