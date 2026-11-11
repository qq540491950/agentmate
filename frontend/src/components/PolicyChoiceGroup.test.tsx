import { renderToStaticMarkup } from "react-dom/server";
import { Eye, FolderLock, TriangleAlert, type LucideIcon } from "lucide-react";
import { describe, expect, it, vi } from "vitest";
import { SANDBOX_PROFILE_OPTIONS } from "@/lib/sandboxMode";
import type { SandboxProfile } from "@/lib/settingsModel";
import { PolicyChoiceGroup } from "./PolicyChoiceGroup";

const icons: Record<SandboxProfile, LucideIcon> = {
  "workspace-write": FolderLock,
  "read-only": Eye,
  "danger-full-access": TriangleAlert,
};

describe("PolicyChoiceGroup", () => {
  it("exposes read-only as the single checked radio option", () => {
    const html = renderToStaticMarkup(
      <PolicyChoiceGroup
        label="新会话默认沙箱"
        description="选择执行边界"
        options={SANDBOX_PROFILE_OPTIONS}
        icons={icons}
        value="read-only"
        columnsClass="sm:grid-cols-3"
        onSelect={vi.fn()}
      />,
    );
    expect(html.match(/role="radio"/g)).toHaveLength(3);
    expect(html.match(/aria-checked="true"/g)).toHaveLength(1);
    const buttons = html.split("</button>");
    expect(buttons.find((button) => button.includes("只读"))).toContain('aria-checked="true"');
    expect(buttons.find((button) => button.includes("工作区可写"))).toContain('aria-checked="false"');
  });
});
