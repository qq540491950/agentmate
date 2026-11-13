// 聊天会话侧边栏分隔条的尺寸策略。保持为纯函数，使拖拽、键盘和恢复路径在单元测
// 试中以相同方式限制宽度。
export const SIDEBAR_DEFAULT_WIDTH = 240;
export const SIDEBAR_MIN_WIDTH = 200;
export const SIDEBAR_MAX_RATIO = 0.5;
export const SIDEBAR_KEYBOARD_STEP = 16;

/**
 * sidebarMaxWidth
 * 将侧边栏上限设为约窗口宽度的一半，以确保聊天区域绝不会被挤掉；极小窗口仍允许
 * 可读的最小值。
 */
export function sidebarMaxWidth(viewportWidth: number): number {
  const half = Math.floor(viewportWidth * SIDEBAR_MAX_RATIO);
  return Math.max(SIDEBAR_MIN_WIDTH, half);
}

/** clampSidebarWidth 将任意请求的宽度限制到支持范围内。 */
export function clampSidebarWidth(width: number, viewportWidth: number): number {
  const rounded = Math.round(width);
  if (!Number.isFinite(rounded)) return SIDEBAR_DEFAULT_WIDTH;
  return Math.min(Math.max(rounded, SIDEBAR_MIN_WIDTH), sidebarMaxWidth(viewportWidth));
}

/**
 * parseStoredSidebarWidth 从 localStorage
 * 恢复上次的宽度。缺失、格式错误和超出范围的值回退到默认值。
 */
export function parseStoredSidebarWidth(raw: string | null, viewportWidth: number): number {
  const fallback = clampSidebarWidth(SIDEBAR_DEFAULT_WIDTH, viewportWidth);
  if (!raw || !raw.trim()) return fallback;
  const parsed = Number(raw);
  if (!Number.isFinite(parsed)) return fallback;
  return clampSidebarWidth(parsed, viewportWidth);
}