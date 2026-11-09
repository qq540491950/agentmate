import { describe, expect, it } from "vitest";
import type { UpdateStatus } from "@/lib/api";
import {
  formatUpdateBytes,
  sidebarUpdateVisible,
  updateBadge,
  updatePercent,
  updatePhaseTitle,
  updateProgressFromEvent,
} from "./updateUi";

function status(overrides: Partial<UpdateStatus> = {}): UpdateStatus {
  return {
    currentVersion: "0.6.3",
    state: "available",
    mode: "auto-install",
    availableVersion: "0.6.4",
    autoCheck: true,
    downloadUrl: "https://example.test/releases",
    canAutoInstall: true,
    ...overrides,
  };
}

describe("sidebarUpdateVisible", () => {
  it("仅在存在可操作版本时显示侧栏入口", () => {
    expect(sidebarUpdateVisible(null)).toBe(false);
    expect(sidebarUpdateVisible(status({ state: "idle" }))).toBe(false);
    expect(sidebarUpdateVisible(status({ state: "up-to-date" }))).toBe(false);
    expect(sidebarUpdateVisible(status({ state: "checking" }))).toBe(false);
    expect(sidebarUpdateVisible(status({ state: "error", availableVersion: "" }))).toBe(false);
    expect(sidebarUpdateVisible(status())).toBe(true);
    expect(sidebarUpdateVisible(status({ state: "downloading" }))).toBe(true);
    expect(sidebarUpdateVisible(status({ state: "ready" }))).toBe(true);
    expect(sidebarUpdateVisible(status({ state: "error" }))).toBe(true);
  });
});

describe("updateBadge", () => {
  it("发现版本时显示紧凑更新胶囊", () => {
    expect(updateBadge(status(), null, false)).toEqual({ label: "更新", tone: "brand" });
    expect(updateBadge(status(), null, true)).toEqual({ label: "准备中", tone: "brand" });
    expect(updateBadge(status({ canAutoInstall: false }), null, false)).toEqual({
      label: "下载",
      tone: "brand",
    });
  });

  it("下载中显示百分比，校验和安装阶段显示阶段名", () => {
    expect(updateBadge(status({ state: "downloading" }), { written: 12, total: 30, rate: 0 }, false))
      .toEqual({ label: "40%", tone: "progress", percent: 40 });
    expect(updateBadge(status({ state: "verifying" }), null, false))
      .toEqual({ label: "校验中", tone: "progress" });
    expect(updateBadge(status({ state: "installing" }), null, false))
      .toEqual({ label: "准备中", tone: "progress" });
  });

  it("完成与失败状态保留可操作入口", () => {
    expect(updateBadge(status({ state: "ready" }), null, false))
      .toEqual({ label: "重启", tone: "ready" });
    expect(updateBadge(status({ state: "error" }), null, false))
      .toEqual({ label: "重试", tone: "error" });
  });
});

describe("update helpers", () => {
  it("格式化下载进度并夹取百分比", () => {
    expect(updatePercent({ written: 5, total: 10, rate: 0 })).toBe(50);
    expect(updatePercent({ written: 20, total: 10, rate: 0 })).toBe(100);
    expect(updatePercent({ written: 5, total: 0, rate: 0 })).toBeNull();
    expect(formatUpdateBytes(0)).toBe("0 B");
    expect(formatUpdateBytes(1536)).toBe("1.5 KB");
  });

  it("从 Wails 事件信封读取下载进度", () => {
    expect(updateProgressFromEvent({
      name: "wails:updater:download-progress",
      data: { written: 12, total: 30, rate: 2 },
    })).toEqual({ written: 12, total: 30, rate: 2 });
    expect(updateProgressFromEvent({ written: 1, total: 2, rate: 0 }))
      .toEqual({ written: 1, total: 2, rate: 0 });
    expect(updateProgressFromEvent({ name: "chat:done", data: null })).toBeNull();
  });

  it("给浮层标题提供阶段文案", () => {
    expect(updatePhaseTitle(status())).toBe("发现新版本");
    expect(updatePhaseTitle(status({ state: "downloading" }))).toBe("正在下载更新");
    expect(updatePhaseTitle(status({ state: "ready" }))).toBe("更新已准备就绪");
  });
});
