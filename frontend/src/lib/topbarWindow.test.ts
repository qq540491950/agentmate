import { describe, expect, it } from "vitest";
import { resolveMaximiseIcon } from "./topbarWindow";

// Seam agreed in issue #76: 窗口状态 → 按钮图标语义。最大化时按钮必须显示
// 「还原」，未最大化时显示「最大化」（user story 5）。
describe("resolveMaximiseIcon", () => {
  it("未最大化 → 最大化图标", () => {
    expect(resolveMaximiseIcon(false)).toBe("maximise");
  });

  it("已最大化 → 还原图标", () => {
    expect(resolveMaximiseIcon(true)).toBe("restore");
  });
});
