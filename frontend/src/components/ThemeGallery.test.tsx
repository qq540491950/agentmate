import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ThemeGallery } from "./ThemeGallery";

describe("ThemeGallery", () => {
  it("renders four real-token previews as a single-select radio group", () => {
    const html = renderToStaticMarkup(
      <ThemeGallery value="nord" dark onChange={vi.fn()} />,
    );
    expect(html).toContain('role="radiogroup"');
    expect(html.match(/role="radio"/g)).toHaveLength(4);
    expect(html).toContain('aria-checked="true"');
    expect(html).toContain('data-theme="nord"');
    expect(html).toContain("石墨");
    expect(html).toContain("纸墨");
    expect(html).toContain("高对比");
  });
});
