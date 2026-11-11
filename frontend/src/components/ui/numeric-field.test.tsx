import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { NumericField } from "./numeric-field";

describe("NumericField", () => {
  it("renders a text field with rule-driven input mode instead of a number spinner", () => {
    const html = renderToStaticMarkup(
      <NumericField
        aria-label="A2A 端口"
        value={8080}
        rule={{ min: 0, max: 65535, integer: true }}
        onValueChange={vi.fn()}
        suffix="%"
      />,
    );
    expect(html).toContain('type="text"');
    expect(html).toContain('inputMode="numeric"');
    expect(html).toContain("A2A 端口");
    expect(html).not.toContain('type="number"');
    expect(html).toContain("%");
  });
});
