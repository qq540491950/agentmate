import { describe, expect, it } from "vitest";
import { SESSION_ROW_ACTIONS_CLASS } from "./sessionRowActions";

describe("SESSION_ROW_ACTIONS_CLASS", () => {
  it("keeps the dropdown trigger laid out while its menu is open", () => {
    // #28 的回归测试：一旦 Radix 的模态层使 :hover 失效并将焦点移入
    // portal，display:none 的操作簇会让 popper 得到 0×0
    // 的锚点矩形，导致菜单落在窗口左上角。
    expect(SESSION_ROW_ACTIONS_CLASS).toContain("group-has-data-[state=open]:flex");
  });

  it("still reveals the actions on hover and on keyboard focus", () => {
    // 该操作簇在静止时处于折叠状态；悬停和 focus-within
    // 才是使其可触达的机制（#24 / 引入 group-focus-within
    // 的键盘可访问性修复）。
    expect(SESSION_ROW_ACTIONS_CLASS.split(" ")).toContain("hidden");
    expect(SESSION_ROW_ACTIONS_CLASS).toContain("group-hover:flex");
    expect(SESSION_ROW_ACTIONS_CLASS).toContain("group-focus-within:flex");
  });
});