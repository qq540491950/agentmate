import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { SidebarFooter } from "./SidebarFooter";

describe("SidebarFooter", () => {
  it("保留版本号与设置、帮助、主题三个入口", () => {
    const html = renderToStaticMarkup(
      <SidebarFooter
        version="0.6.3"
        themePreset="nord"
        themeMode="dark"
        onOpenSettings={vi.fn()}
        onOpenHelp={vi.fn()}
        onThemePresetChange={vi.fn()}
        onThemeModeChange={vi.fn()}
      />,
    );

    expect(html).toContain("AgentMate v0.6.3");
    expect(html).toContain('title="设置（⌘,）"');
    expect(html).toContain('title="快捷键与命令"');
    expect(html).toContain('title="主题与外观"');
  });
});
