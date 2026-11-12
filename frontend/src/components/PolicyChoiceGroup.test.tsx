import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { SANDBOX_PROFILE_OPTIONS } from "@/lib/sandboxMode";
import { PolicyChoiceGroup } from "./PolicyChoiceGroup";

describe("PolicyChoiceGroup", () => {
  it("exposes read-only as the single checked compact option", () => {
    const html = renderToStaticMarkup(
      <PolicyChoiceGroup
        label="新会话默认沙箱"
        description="选择执行边界"
        options={SANDBOX_PROFILE_OPTIONS}
        value="read-only"
        onSelect={vi.fn()}
      />,
    );
    expect(html.match(/role="radio"/g)).toHaveLength(3);
    expect(html.match(/aria-checked="true"/g)).toHaveLength(1);
    const buttons = html.split("</button>");
    expect(buttons.find((button) => button.includes("只读"))).toContain('aria-checked="true"');
    expect(buttons.find((button) => button.includes("工作区可写"))).toContain('aria-checked="false"');
    expect(html).toContain("终端只能读，不能写工作区；文件写入会被拒绝。");
  });

  it("leaves every option unchecked for a custom policy pair", () => {
    const html = renderToStaticMarkup(
      <PolicyChoiceGroup
        label="新会话默认权限"
        description="选择执行边界"
        options={SANDBOX_PROFILE_OPTIONS}
        value={null}
        onSelect={vi.fn()}
      />,
    );
    expect(html.match(/aria-checked="true"/g)).toBeNull();
  });
});
