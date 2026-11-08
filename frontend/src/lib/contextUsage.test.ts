import { describe, expect, it } from "vitest";
import { contextTooltipLines, estimateContextTokens, resolveContextUsage, type ContextUsageView } from "./contextUsage";

// 口径回归：分子必须是「最后一次调用的 prompt tokens」（真实上下文），
// 不是历次调用的累计 totalTokens——后者在工具循环里重复计入共享前缀，
// 曾把 16.6% 的真实占用显示成 100%（issue「token 耗尽」）。
describe("resolveContextUsage", () => {
  it("真实占用 166k / 1M 窗口 ≈ 17%，绿色", () => {
    const v = resolveContextUsage(166145, 1_000_000);
    expect(v).not.toBeNull();
    expect(v!.pct).toBeCloseTo(16.6145, 4);
    expect(v!.label).toBe("17%");
    expect(v!.color).toBe("bg-success");
    expect(v!.used).toBe(166145);
  });

  it("≥80% 红色、≥50% 黄色", () => {
    expect(resolveContextUsage(900_000, 1_000_000)!.color).toBe("bg-destructive");
    expect(resolveContextUsage(500_000, 1_000_000)!.color).toBe("bg-warning");
  });

  it("钳制到 100%", () => {
    expect(resolveContextUsage(1_200_000, 1_000_000)!.pct).toBe(100);
  });

  it("极小占用显示 <1%", () => {
    expect(resolveContextUsage(500, 1_000_000)!.label).toBe("<1%");
  });

  it("无数据（模型未报 usage / 窗口未知）不渲染", () => {
    expect(resolveContextUsage(0, 1_000_000)).toBeNull();
    expect(resolveContextUsage(undefined, 1_000_000)).toBeNull();
    expect(resolveContextUsage(166145, 0)).toBeNull();
  });

  it("估算视图带 ≈ 前缀并标记 estimated", () => {
    const v = resolveContextUsage(500_000, 1_000_000, true);
    expect(v!.estimated).toBe(true);
    expect(v!.label).toBe("≈50%");
    expect(resolveContextUsage(500_000, 1_000_000)!.estimated).toBe(false);
  });
});

// 估算口径：CJK ≈0.7 token/字、其余（含每条消息 12 字符的信封开销）
// ≈0.25 token/字符；工具载荷按 96KB/条封顶——只给「上游从不报 usage」
// 的模型兜底。
describe("estimateContextTokens", () => {
  it("中文与 ASCII 分别计价，含信封开销", () => {
    expect(estimateContextTokens([{ kind: "user", text: "一二三四五六七八九十" }])).toBe(Math.ceil(10 * 0.7 + 12 * 0.25));
    expect(estimateContextTokens([{ kind: "user", text: "abcdefgh" }])).toBe(Math.ceil(8 * 0.25 + 12 * 0.25));
  });

  it("工具载荷计入并以 96KB/条封顶", () => {
    const small = { data: "x".repeat(1000) };
    expect(estimateContextTokens([{ kind: "tool_result", toolResp: small }]))
      .toBe(Math.ceil((JSON.stringify(small).length + 12) * 0.25));
    const big = { data: "x".repeat(200_000) };
    expect(estimateContextTokens([{ kind: "tool_result", toolResp: big }]))
      .toBe(Math.ceil((96 * 1024 + 12) * 0.25));
  });

  it("空消息列表为 0（新会话不显示圆环）", () => {
    expect(estimateContextTokens([])).toBe(0);
  });
});

// 悬停气泡（Codex 桌面端样式）：占用在前、累计计费在后，纯函数保证文案口径。
describe("contextTooltipLines", () => {
  const usage: ContextUsageView = {
    pct: 17, color: "bg-success", win: 950_000, label: "17%", used: 157_000, estimated: false,
  };

  it("占用 + 累计用量 + 费用齐全", () => {
    expect(contextTooltipLines(usage, { promptTokens: 150_000, completionTokens: 7_000, totalTokens: 157_000 }, "¥0.31"))
      .toEqual([
        "上下文窗口",
        "17% 已用（剩余 83%）",
        "已用 157,000，共 950,000 tokens",
        "累计 157,000 tokens · ¥0.31",
        "输入 150,000 · 输出 7,000",
      ]);
  });

  it("估算视图带 ≈ 标注与口径说明", () => {
    const est: ContextUsageView = { ...usage, label: "≈17%", estimated: true };
    const lines = contextTooltipLines(est, null);
    expect(lines[1]).toBe("≈17% 已用（剩余 83%）");
    expect(lines).toContain("按会话内容估算——当前模型未返回用量");
  });

  it("窗口未知时只保留累计用量（Coins 兜底图标）", () => {
    expect(contextTooltipLines(null, { promptTokens: 120, completionTokens: 80, totalTokens: 200 }))
      .toEqual(["累计 200 tokens", "输入 120 · 输出 80"]);
  });

  it("无数据不渲染、费用为空时省略、零用量省略", () => {
    expect(contextTooltipLines(null, null)).toEqual([]);
    expect(contextTooltipLines(null, { promptTokens: 0, completionTokens: 0, totalTokens: 0 })).toEqual([]);
    expect(contextTooltipLines(usage, { promptTokens: 1, completionTokens: 2, totalTokens: 3 }))
      .toEqual(["上下文窗口", "17% 已用（剩余 83%）", "已用 157,000，共 950,000 tokens", "累计 3 tokens", "输入 1 · 输出 2"]);
  });
});
