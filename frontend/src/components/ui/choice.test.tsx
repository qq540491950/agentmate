import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { SegmentedControl, choiceCardClass } from "./choice";

describe("choice controls", () => {
  it("renders a single-select group with a visible selected surface", () => {
    const html = renderToStaticMarkup(
      <SegmentedControl
        ariaLabel="明暗模式"
        value="dark"
        options={[
          { value: "light", label: "浅色" },
          { value: "dark", label: "深色" },
        ]}
        onChange={vi.fn()}
      />,
    );
    expect(html).toContain('role="radiogroup"');
    expect(html.match(/role="radio"/g)).toHaveLength(2);
    expect(html).toContain('aria-checked="true"');
    expect(html).toContain("bg-choice-selected");
    expect(html).toContain("bg-control");
  });

  it("uses distinct selected and danger-selected surfaces for choice cards", () => {
    expect(choiceCardClass(true)).toContain("bg-choice-selected");
    expect(choiceCardClass(true, true)).toContain("bg-choice-selected-danger");
    expect(choiceCardClass(false)).toContain("bg-control");
  });
});
