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

  it("浏览器（stub）：渲染按钮区（操作无操作）以便视觉回归覆盖，无拖拽区、无留白", () => {
    expect(resolveTopbarLayout("browser")).toEqual({
      windowButtonsArea: true,
      trafficLightInset: 0,
      dragRegion: false,
    });
  });
});

describe("detectTopbarPlatform", () => {
  // 真实契约：Wails runtime 注入的是大写字段（internal/runtime/runtime_prod.go:
  // `window._wails.environment={"OS":…,"Arch":…,"Debug":false}`），@wailsio/runtime
  // 内部（system.js / drag.js）也按大写读取。
  it("Wails 注入 windows 环境时返回 windows", () => {
    expect(detectTopbarPlatform({ OS: "windows" })).toBe("windows");
  });

  it("Wails 注入 darwin 环境时返回 macos", () => {
    expect(detectTopbarPlatform({ OS: "darwin" })).toBe("macos");
  });

  it("无 Wails 环境（浏览器 / stub）降级为 browser", () => {
    expect(detectTopbarPlatform({ OS: undefined })).toBe("browser");
    expect(detectTopbarPlatform({})).toBe("browser");
  });

  it("Linux 桌面按 browser 降级（v1 无 Linux 自绘 chrome）", () => {
    expect(detectTopbarPlatform({ OS: "linux" })).toBe("browser");
  });
});
