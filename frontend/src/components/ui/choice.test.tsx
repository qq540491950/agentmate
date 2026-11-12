import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { SegmentedControl } from "./choice";

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

  it("supports a danger-selected option without using danger text for the label", () => {
    const html = renderToStaticMarkup(
      <SegmentedControl
        ariaLabel="权限模式"
        value="danger-full-access"
        options={[
          { value: "request-approval", label: "请求批准" },
          { value: "danger-full-access", label: "完全访问", danger: true },
        ]}
        onChange={vi.fn()}
      />,
    );
    expect(html).toContain("bg-choice-selected-danger");
    expect(html).toContain('aria-checked="true"');
    expect(html).toContain("text-danger-text");
  });

  it("can render a custom policy with no selected option", () => {
    const html = renderToStaticMarkup(
      <SegmentedControl
        ariaLabel="权限模式"
        value={null}
        options={[
          { value: "request-approval", label: "请求批准" },
          { value: "auto-approve", label: "帮我批准" },
        ]}
        onChange={vi.fn()}
      />,
    );
    expect(html.match(/aria-checked="true"/g)).toBeNull();
  });
});
