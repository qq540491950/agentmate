import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TopBar } from "./TopBar";
import { resolveTopbarLayout } from "@/lib/topbarLayout";

// issue #75 中约定的接缝：仅断言外部可观察的结构和语义 属性 ——
// 不涉及样式细节，也不涉及内部实现。
describe("TopBar", () => {
  it("渲染品牌区、中部与右侧扩展槽位", () => {
    const html = renderToStaticMarkup(<TopBar />);
    expect(html).toContain("AgentMate");
    expect(html).toContain('data-slot="topbar-slot-center"');
    expect(html).toContain('data-slot="topbar-slot-end"');
  });

  it("Windows 变体：渲染 3 个带 aria-label 的窗口按钮并声明拖拽区", () => {
    const html = renderToStaticMarkup(<TopBar layout={resolveTopbarLayout("windows")} />);
    expect(html).toContain('data-slot="topbar-window-buttons"');
    expect(html).toContain('aria-label="最小化"');
    expect(html).toContain('aria-label="最大化"');
    expect(html).toContain('aria-label="关闭"');
    expect(html).toContain("--wails-draggable");
  });

  it("浏览器（stub）变体：渲染窗口按钮（操作无操作），不声明拖拽区", () => {
    const html = renderToStaticMarkup(<TopBar />);
    expect(html).toContain('aria-label="最小化"');
    expect(html).not.toContain("--wails-draggable");
  });

  it("macOS 变体：预留红绿灯留白，不渲染窗口按钮区", () => {
    const html = renderToStaticMarkup(<TopBar layout={resolveTopbarLayout("macos")} />);
    expect(html).not.toContain('data-slot="topbar-window-buttons"');
    expect(html).toContain('data-slot="topbar-traffic-light-inset"');
  });

  // 端到端接缝：TopBar 不传 layout 时应从 window._wails.environment 自动解析
  // 平台。真实注入（internal/runtime/runtime_prod.go）是大写 OS 字段——此前
  // 组件读小写 os 导致平台恒判 browser、拖拽区永不声明（顶栏拖动失效）。
  describe("平台自动检测（真实 Wails 注入契约）", () => {
    afterEach(() => vi.unstubAllGlobals());

    it("Windows 环境：顶栏声明拖拽区，按钮区声明 no-drag", () => {
      vi.stubGlobal("window", {
        _wails: { environment: { OS: "windows", Arch: "amd64", Debug: false } },
      });
      const html = renderToStaticMarkup(<TopBar />);
      expect(html).toContain("--wails-draggable:drag");
      expect(html).toContain("--wails-draggable:no-drag");
    });

    it("macOS 环境：不声明拖拽区（沿用系统标题栏拖动）", () => {
      vi.stubGlobal("window", {
        _wails: { environment: { OS: "darwin", Arch: "arm64", Debug: false } },
      });
      const html = renderToStaticMarkup(<TopBar />);
      expect(html).not.toContain("--wails-draggable");
    });
  });
});
