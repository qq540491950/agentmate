import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ThemeChoice } from "./ThemeChoice";

describe("ThemeChoice", () => {
  it("renders four compact theme options as a single-select radio group", () => {
    const html = renderToStaticMarkup(
      <ThemeChoice value="nord" onChange={vi.fn()} />,
    );
    expect(html).toContain('role="radiogroup"');
    expect(html.match(/role="radio"/g)).toHaveLength(4);
    expect(html).toContain('aria-checked="true"');
    expect(html).toContain("极夜蓝灰与雪原浅色，默认主题");
    expect(html).toContain("石墨");
    expect(html).toContain("纸墨");
    expect(html).toContain("高对比");
    expect(html).not.toContain("theme-preview");
  });
});
