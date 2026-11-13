// 主题应用：一套完整的主题预设，外加一个正交的浅色/深色模式。 组件使用语义
// token；本模块是唯一一个把主题状态写入文档的地方。
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

// 聊天栏宽度使用面板百分比，而不是固定的像素上限，因此该栏在每种模式下都会跟随
// 窗口缩放。
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

/** resolveTheme 是纯函数，因此启动、设置和测试会得到一致的结果。 */
export function resolveTheme(
  prefs: Pick<ThemePrefs, "themePreset" | "themeMode">,
  systemDark: boolean,
): ResolvedTheme {
  const preset = normalizeThemePreset(prefs.themePreset);
  const mode = normalizeThemeMode(prefs.themeMode);
  const dark = mode === "dark" || (mode === "system" && systemDark);
  return { preset, mode: dark ? "dark" : "light", dark };
}

/** prefsFromSettings 是唯一一处把持久化设置映射为 UI 偏好的地方。 */
export function prefsFromSettings(s: Settings): ThemePrefs {
  return {
    themePreset: normalizeThemePreset(s.themePreset),
    themeMode: normalizeThemeMode(s.themeMode),
    fontScale: s.fontScale || "md",
    chatWidth: s.chatWidth || "medium",
  };
}

/** applyTheme 将预设、模式、字体、宽度写入文档。 */
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

/** cacheTheme 将偏好写入 localStorage，以便启动时立即应用。 */
export function cacheTheme(s: Settings) {
  const prefs = prefsFromSettings(s);
  cacheThemePrefs(prefs);
}

/** cacheThemePrefs 持久化内联脚本所用的确切启动数据结构。 */
export function cacheThemePrefs(prefs: ThemePrefs) {
  if (typeof localStorage === "undefined") return;
  localStorage.setItem("wb-theme-prefs", JSON.stringify(prefs));
}

/** cachedTheme 读取启动缓存（回退到 Nord 深色）。 */
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

/** syncThemeFromBackend 从后端加载设置，缓存并应用它们。 */
export async function syncThemeFromBackend(): Promise<boolean> {
  try {
    const cfg = await Config.GetConfig();
    cacheTheme(cfg.settings);
    return applyTheme(cachedTheme());
  } catch {
    return applyTheme(cachedTheme());
  }
}

/** onSystemThemeChange 在操作系统外观发生变化时重新应用。 */
export function onSystemThemeChange(prefs: ThemePrefs, fn: (dark: boolean) => void): () => void {
  if (!media) return () => {};
  const handler = () => {
    if (normalizeThemeMode(prefs.themeMode) === "system") fn(applyTheme(prefs));
  };
  media.addEventListener("change", handler);
  return () => media.removeEventListener("change", handler);
}
