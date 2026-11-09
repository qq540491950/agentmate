import { describe, expect, it } from "vitest";
import { railBarWidth, railLabelGeometry, railLabelText } from "./TurnRail";

describe("railBarWidth", () => {
  it("悬停/聚焦横杠使用固定 w-10 档位，其余保留文本长度档位", () => {
    expect(railBarWidth("短", true)).toBe("w-10");
    expect(railBarWidth("短", false)).toBe("w-3");
    expect(railBarWidth("a".repeat(360), false)).toBe("w-7");
  });
});

describe("railLabelText", () => {
  it("多行输入取短首行作为标签", () => {
    expect(railLabelText("继续\nI'll continue the read-only check.", 1)).toBe("继续");
  });

  it("单行输入直接使用文本，不再退化为轮次编号", () => {
    expect(railLabelText("请检查项目并提交修复", 1)).toBe("请检查项目并提交修复");
  });

  it("首行过长时截断，空输入回退到轮次编号", () => {
    expect(railLabelText(`${"a".repeat(61)}\n正文`, 0)).toBe(`${"a".repeat(60)}…`);
    expect(railLabelText("", 2)).toBe("第 3 轮");
  });
});

describe("railLabelGeometry", () => {
  it("标签始终贴在横杠右侧并与横杠垂直居中", () => {
    const geometry = railLabelGeometry(40, 390, 1280, 900);
    expect(geometry).toEqual({ top: 378, left: 48, maxWidth: 280 });
  });

  it("顶部和底部位置会钳制在视口内", () => {
    expect(railLabelGeometry(40, 5, 1280, 900)?.top).toBe(8);
    expect(railLabelGeometry(40, 890, 1280, 900)?.top).toBe(864);
  });

  it("宽度受视口右边界约束", () => {
    expect(railLabelGeometry(1000, 390, 1280, 900).maxWidth).toBe(256);
    expect(railLabelGeometry(1100, 390, 1280, 900).maxWidth).toBe(156);
  });
});
