import { describe, expect, it } from "vitest";
import {
  normalizeThemeMode,
  normalizeThemePreset,
  prefsFromSettings,
  resolveTheme,
} from "./theme";
import type { Settings } from "@/lib/api";

describe("theme resolution", () => {
  it("falls back to Nord and dark for unknown or missing values", () => {
    expect(normalizeThemePreset("")).toBe("nord");
    expect(normalizeThemePreset("unknown")).toBe("nord");
    expect(normalizeThemeMode("")).toBe("dark");
    expect(normalizeThemeMode("sepia")).toBe("dark");
  });

  it("keeps a valid preset when resolving light and dark modes", () => {
    expect(resolveTheme({ themePreset: "graphite", themeMode: "light" }, true)).toEqual({
      preset: "graphite",
      mode: "light",
      dark: false,
    });
    expect(resolveTheme({ themePreset: "paper", themeMode: "dark" }, false)).toEqual({
      preset: "paper",
      mode: "dark",
      dark: true,
    });
  });

  it("system mode follows the OS without changing the preset", () => {
    expect(resolveTheme({ themePreset: "contrast", themeMode: "system" }, true)).toEqual({
      preset: "contrast",
      mode: "dark",
      dark: true,
    });
    expect(resolveTheme({ themePreset: "contrast", themeMode: "system" }, false)).toEqual({
      preset: "contrast",
      mode: "light",
      dark: false,
    });
  });

  it("migrates old settings to Nord while preserving unrelated appearance prefs", () => {
    const prefs = prefsFromSettings({
      themeMode: "system",
      fontScale: "lg",
      chatWidth: "wide",
    } as Settings);
    expect(prefs).toEqual({
      themePreset: "nord",
      themeMode: "system",
      fontScale: "lg",
      chatWidth: "wide",
    });
  });
});
