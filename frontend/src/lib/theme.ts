// Theme application: a complete theme preset plus an orthogonal light/dark
// mode. Components consume semantic tokens; this module is the only place
// that writes theme state to the document.
import { Config, type Settings } from "@/lib/api";

export type ThemePreset = "nord" | "graphite" | "paper" | "contrast";
export type ThemeMode = "light" | "dark" | "system";

export interface ThemePrefs {
  themePreset: string;
  themeMode: string;
  fontScale: string;
  chatWidth: string;
}

export interface ResolvedTheme {
  preset: ThemePreset;
  mode: "light" | "dark";
  dark: boolean;
}

export const THEME_OPTIONS: ReadonlyArray<{
  value: ThemePreset;
  label: string;
  description: string;
}> = [
  { value: "nord", label: "Nord", description: "极夜蓝灰与雪原浅色，默认主题" },
  { value: "graphite", label: "石墨", description: "中性石墨与冷静蓝，接近旧版专业观感" },
  { value: "paper", label: "纸墨", description: "暖象牙、深墨与琥珀强调，适合白天" },
  { value: "contrast", label: "高对比", description: "可访问性优先，保留明确控件边界" },
];

export const THEME_MODES: ReadonlyArray<{ value: ThemeMode; label: string }> = [
  { value: "light", label: "浅色" },
  { value: "dark", label: "深色" },
  { value: "system", label: "跟随系统" },
];

export const FONT_SCALES: Record<string, { label: string; root: string }> = {
  sm: { label: "小", root: "93.75%" },
  md: { label: "中", root: "100%" },
  lg: { label: "大", root: "112.5%" },
};

// Chat column widths are pane percentages, not fixed pixel caps, so the column
// follows window resizing at every mode.
export const CHAT_WIDTHS: Record<string, { label: string; width: string }> = {
  narrow: { label: "窄", width: "56%" },
  medium: { label: "中", width: "72%" },
  wide: { label: "宽", width: "88%" },
};

const THEME_PRESETS = new Set<ThemePreset>(["nord", "graphite", "paper", "contrast"]);
const THEME_MODES_SET = new Set<ThemeMode>(["light", "dark", "system"]);

const media =
  typeof window !== "undefined" && typeof window.matchMedia === "function"
    ? window.matchMedia("(prefers-color-scheme: dark)")
    : null;

export function normalizeThemePreset(value: string | undefined | null): ThemePreset {
  const normalized = (value ?? "").trim().toLowerCase() as ThemePreset;
  return THEME_PRESETS.has(normalized) ? normalized : "nord";
}

export function normalizeThemeMode(value: string | undefined | null): ThemeMode {
  const normalized = (value ?? "").trim().toLowerCase() as ThemeMode;
  return THEME_MODES_SET.has(normalized) ? normalized : "dark";
}

/** resolveTheme is pure so boot, settings and tests agree on one result. */
export function resolveTheme(
  prefs: Pick<ThemePrefs, "themePreset" | "themeMode">,
  systemDark: boolean,
): ResolvedTheme {
  const preset = normalizeThemePreset(prefs.themePreset);
  const mode = normalizeThemeMode(prefs.themeMode);
  const dark = mode === "dark" || (mode === "system" && systemDark);
  return { preset, mode: dark ? "dark" : "light", dark };
}

/** prefsFromSettings is the one mapping from persisted settings to UI prefs. */
export function prefsFromSettings(s: Settings): ThemePrefs {
  return {
    themePreset: normalizeThemePreset(s.themePreset),
    themeMode: normalizeThemeMode(s.themeMode),
    fontScale: s.fontScale || "md",
    chatWidth: s.chatWidth || "medium",
  };
}

/** applyTheme writes preset/mode/font/width to the document. */
export function applyTheme(prefs: ThemePrefs): boolean {
  const resolved = resolveTheme(prefs, media?.matches ?? false);
  const root = document.documentElement;
  root.dataset.theme = resolved.preset;
  root.classList.toggle("dark", resolved.dark);
  root.style.colorScheme = resolved.mode;
  root.style.fontSize = (FONT_SCALES[prefs.fontScale] ?? FONT_SCALES.md).root;
  root.style.setProperty(
    "--chat-col-width",
    (CHAT_WIDTHS[prefs.chatWidth] ?? CHAT_WIDTHS.medium).width,
  );
  return resolved.dark;
}

/** cacheTheme writes prefs to localStorage for instant boot application. */
export function cacheTheme(s: Settings) {
  const prefs = prefsFromSettings(s);
  cacheThemePrefs(prefs);
}

/** cacheThemePrefs persists the exact boot shape used by the inline script. */
export function cacheThemePrefs(prefs: ThemePrefs) {
  if (typeof localStorage === "undefined") return;
  localStorage.setItem("wb-theme-prefs", JSON.stringify(prefs));
}

/** cachedTheme reads the boot cache (falls back to Nord dark). */
export function cachedTheme(): ThemePrefs {
  try {
    if (typeof localStorage !== "undefined") {
      const raw = localStorage.getItem("wb-theme-prefs");
      if (raw) {
        const prefs = JSON.parse(raw) as Partial<ThemePrefs>;
        return {
          themePreset: normalizeThemePreset(prefs.themePreset),
          themeMode: normalizeThemeMode(prefs.themeMode),
          fontScale: prefs.fontScale || "md",
          chatWidth: prefs.chatWidth || "medium",
        };
      }
    }
  } catch { /* ignore */ }
  return { themePreset: "nord", themeMode: "dark", fontScale: "md", chatWidth: "medium" };
}

/** syncThemeFromBackend loads settings from the backend, caches and applies them. */
export async function syncThemeFromBackend(): Promise<boolean> {
  try {
    const cfg = await Config.GetConfig();
    cacheTheme(cfg.settings);
    return applyTheme(cachedTheme());
  } catch {
    return applyTheme(cachedTheme());
  }
}

/** onSystemThemeChange re-applies when the OS appearance changes. */
export function onSystemThemeChange(prefs: ThemePrefs, fn: (dark: boolean) => void): () => void {
  if (!media) return () => {};
  const handler = () => {
    if (normalizeThemeMode(prefs.themeMode) === "system") fn(applyTheme(prefs));
  };
  media.addEventListener("change", handler);
  return () => media.removeEventListener("change", handler);
}
