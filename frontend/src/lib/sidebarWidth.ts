// Sizing policy for the chat session sidebar splitter. Kept pure so the
// drag, keyboard and restore paths clamp identically in unit tests.
export const SIDEBAR_DEFAULT_WIDTH = 240;
export const SIDEBAR_MIN_WIDTH = 200;
export const SIDEBAR_MAX_RATIO = 0.5;
export const SIDEBAR_KEYBOARD_STEP = 16;

/** sidebarMaxWidth caps the sidebar at roughly half the window so the chat
 * area never gets squeezed out; tiny windows still allow the readable min. */
export function sidebarMaxWidth(viewportWidth: number): number {
  const half = Math.floor(viewportWidth * SIDEBAR_MAX_RATIO);
  return Math.max(SIDEBAR_MIN_WIDTH, half);
}

/** clampSidebarWidth snaps any requested width into the supported range. */
export function clampSidebarWidth(width: number, viewportWidth: number): number {
  const rounded = Math.round(width);
  if (!Number.isFinite(rounded)) return SIDEBAR_DEFAULT_WIDTH;
  return Math.min(Math.max(rounded, SIDEBAR_MIN_WIDTH), sidebarMaxWidth(viewportWidth));
}

/** parseStoredSidebarWidth restores the last width from localStorage.
 * Missing, malformed and out-of-range values fall back to the default. */
export function parseStoredSidebarWidth(raw: string | null, viewportWidth: number): number {
  const fallback = clampSidebarWidth(SIDEBAR_DEFAULT_WIDTH, viewportWidth);
  if (!raw || !raw.trim()) return fallback;
  const parsed = Number(raw);
  if (!Number.isFinite(parsed)) return fallback;
  return clampSidebarWidth(parsed, viewportWidth);
}