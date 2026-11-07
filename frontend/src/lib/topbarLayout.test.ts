import { describe, expect, it } from "vitest";
import { detectTopbarPlatform, resolveTopbarLayout } from "./topbarLayout";

// Seam agreed in issue #75: the platform → topbar layout mapping is the pure
// heart of the shell; the component only renders what this function decides.
describe("resolveTopbarLayout", () => {
  it("Windows：预留自绘窗口按钮区并声明拖拽区，不留红绿灯位", () => {
    const layout = resolveTopbarLayout("windows");
    expect(layout.windowButtonsArea).toBe(true);
    expect(layout.dragRegion).toBe(true);
    expect(layout.trafficLightInset).toBe(0);
  });

  it("macOS：为红绿灯留白，不声明拖拽区、不留自绘按钮区", () => {
    const layout = resolveTopbarLayout("macos");
    expect(layout.windowButtonsArea).toBe(false);
    expect(layout.dragRegion).toBe(false);
    expect(layout.trafficLightInset).toBeGreaterThan(0);
  });

  it("浏览器（stub）：全部降级，无按钮区、无拖拽区、无留白", () => {
    expect(resolveTopbarLayout("browser")).toEqual({
      windowButtonsArea: false,
      trafficLightInset: 0,
      dragRegion: false,
    });
  });
});

describe("detectTopbarPlatform", () => {
  it("Wails 注入 windows 环境时返回 windows", () => {
    expect(detectTopbarPlatform({ os: "windows" })).toBe("windows");
  });

  it("Wails 注入 darwin 环境时返回 macos", () => {
    expect(detectTopbarPlatform({ os: "darwin" })).toBe("macos");
  });

  it("无 Wails 环境（浏览器 / stub）降级为 browser", () => {
    expect(detectTopbarPlatform({ os: undefined })).toBe("browser");
    expect(detectTopbarPlatform({})).toBe("browser");
  });

  it("Linux 桌面按 browser 降级（v1 无 Linux 自绘 chrome）", () => {
    expect(detectTopbarPlatform({ os: "linux" })).toBe("browser");
  });
});
