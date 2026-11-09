import { describe, expect, it } from "vitest";
import { railPreviewGeometry, railPreviewTitle } from "./TurnRail";

describe("railPreviewTitle", () => {
  it("多行输入取短首行作为标题", () => {
    expect(railPreviewTitle("继续\nI'll continue the read-only check.", 1)).toBe("继续");
  });

  it("单行输入回退到轮次编号，避免标题重复正文", () => {
    expect(railPreviewTitle("请检查项目并提交修复", 1)).toBe("第 2 轮");
  });

  it("首行过长或为空时回退到轮次编号", () => {
    expect(railPreviewTitle(`${"a".repeat(61)}\n正文`, 0)).toBe("第 1 轮");
    expect(railPreviewTitle("", 2)).toBe("第 3 轮");
  });
});

describe("railPreviewGeometry", () => {
  it("右侧沟槽不足时，预览卡翻到横杠左侧", () => {
    const geometry = railPreviewGeometry(367, 390, 900, 387);
    expect(geometry).toEqual({ top: 382, left: 35, width: 320, side: "left" });
    expect(geometry!.left + geometry!.width).toBeLessThanOrEqual(367 - 12);
    expect(geometry!.left).toBeGreaterThanOrEqual(16);
  });

  it("右侧沟槽充足时，预览卡停在输出区左侧", () => {
    const geometry = railPreviewGeometry(8, 390, 900, 269);
    expect(geometry).toEqual({ top: 382, left: 20, width: 233, side: "right" });
    expect(geometry!.left + geometry!.width).toBeLessThanOrEqual(269);
  });

  it("两侧空间都不足时返回 null，避免覆盖输出区", () => {
    expect(railPreviewGeometry(159, 390, 900, 176)).toBeNull();
  });

  it("顶部和底部位置会钳制在视口内", () => {
    expect(railPreviewGeometry(367, 5, 900, 387)?.top).toBe(16);
    expect(railPreviewGeometry(367, 890, 900, 387)?.top).toBe(680);
  });
});
