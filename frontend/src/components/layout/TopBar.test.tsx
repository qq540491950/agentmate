import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { TopBar } from "./TopBar";
import { resolveTopbarLayout } from "@/lib/topbarLayout";

// Seam agreed in issue #75: only externally observable structure and semantic
// attributes are asserted — no style details, no internals.
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
});
