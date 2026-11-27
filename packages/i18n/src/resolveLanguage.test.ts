import { describe, expect, it } from "vitest";
import { resolveLanguage } from "./resolveLanguage";

describe("resolveLanguage", () => {
  it("falls back to 'en' when no candidate is supported", () => {
    expect(resolveLanguage([], ["de", "en"], "en")).toBe("en");
    expect(resolveLanguage(["fr", "es"], ["de", "en"], "en")).toBe("en");
  });

  it("returns the first supported candidate", () => {
    expect(resolveLanguage(["de"], ["de", "en"], "en")).toBe("de");
    expect(resolveLanguage(["fr", "de"], ["de", "en"], "en")).toBe("de");
  });

  it("matches BCP-47 region tags by primary subtag", () => {
    expect(resolveLanguage(["de-AT"], ["de", "en"], "en")).toBe("de");
    expect(resolveLanguage(["en-US"], ["de", "en"], "en")).toBe("en");
  });

  it("is case-insensitive", () => {
    expect(resolveLanguage(["DE"], ["de", "en"], "en")).toBe("de");
    expect(resolveLanguage(["En-gb"], ["de", "en"], "en")).toBe("en");
  });
});
