import type { UpdateStatus } from "@/lib/api";

export type UpdateProgress = {
  written: number;
  total: number;
  rate: number;
};

export type UpdateBadgeTone = "brand" | "progress" | "ready" | "error";

export type UpdateBadge = {
  label: string;
  tone: UpdateBadgeTone;
  percent?: number;
};

/** updateProgressFromEvent unwraps the WailsEvent envelope. Payloads from the
 * updater live under `event.data`; reading `written` from the envelope leaves
 * the progress bar stuck at zero. */
export function updateProgressFromEvent(event: unknown): UpdateProgress | null {
  const payload = (event as { data?: unknown })?.data ?? event;
  if (
    payload &&
    typeof payload === "object" &&
    "written" in payload &&
    typeof (payload as { written?: unknown }).written === "number"
  ) {
    return payload as UpdateProgress;
  }
  return null;
}

const VISIBLE_STATES = new Set([
  "available",
  "downloading",
  "verifying",
  "installing",
  "ready",
  "error",
]);

/** sidebarUpdateVisible deliberately ignores check-only failures. The footer
 * only grows an entry when there is a real release to act on (or a failed
 * install of that release). */
export function sidebarUpdateVisible(status: UpdateStatus | null): boolean {
  if (!status || !VISIBLE_STATES.has(status.state)) return false;
  return !!status.availableVersion;
}

/** formatUpdateBytes renders the small transfer sizes shown in the updater. */
export function formatUpdateBytes(n: number): string {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let value = n;
  let index = 0;
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024;
    index += 1;
  }
  return `${value.toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

/** updatePercent returns null until the updater reports a total byte count. */
export function updatePercent(progress: UpdateProgress | null): number | null {
  if (!progress || progress.total <= 0) return null;
  return Math.min(100, Math.max(0, Math.round((progress.written / progress.total) * 100)));
}

/** updateBadge keeps the entry compact at the sidebar's minimum width. */
export function updateBadge(
  status: UpdateStatus | null,
  progress: UpdateProgress | null,
  busy: boolean,
): UpdateBadge | null {
  if (!sidebarUpdateVisible(status)) return null;
  const state = status!.state;
  const percent = updatePercent(progress);

  if (state === "ready") return { label: "重启", tone: "ready" };
  if (state === "error") return { label: "重试", tone: "error" };
  if (state === "verifying") return { label: "校验中", tone: "progress" };
  if (state === "installing") return { label: "准备中", tone: "progress" };
  if (state === "downloading") {
    return { label: percent == null ? "更新中" : `${percent}%`, tone: "progress", percent: percent ?? undefined };
  }
  return {
    label: busy ? "准备中" : status!.canAutoInstall ? "更新" : "下载",
    tone: "brand",
  };
}

/** updatePhaseTitle is used by both the chip title and the popover heading. */
export function updatePhaseTitle(status: UpdateStatus | null): string {
  switch (status?.state) {
    case "available":
      return status.canAutoInstall ? "发现新版本" : "新版本可下载";
    case "downloading":
      return "正在下载更新";
    case "verifying":
      return "正在校验安装包";
    case "installing":
      return "正在准备安装";
    case "ready":
      return "更新已准备就绪";
    case "error":
      return "更新遇到问题";
    default:
      return "检查更新";
  }
}
