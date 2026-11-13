/** 由 Wails 更新器发出的每个生命周期事件。 */
export const UPDATE_EVENTS = [
  "wails:updater:check-started",
  "wails:updater:update-available",
  "wails:updater:no-update",
  "wails:updater:download-started",
  "wails:updater:download-progress",
  "wails:updater:download-complete",
  "wails:updater:verifying",
  "wails:updater:installing",
  "wails:updater:update-ready",
  "wails:updater:error",
] as const;
