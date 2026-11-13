import { describe, expect, it } from "vitest";
import {
  compactTokens,
  contextTooltipLines,
  estimateContextTokens,
  resolveContextUsage,
  type ContextUsageView,
} from "./contextUsage";

// 口径回归：分子必须是「最后一次调用的 prompt
// tokens」（真实上下文），不是历次调用的累计
// totalTokens——后者在工具循环里重复计入共享前缀，曾把 16.6% 的真实占用显示成
// 100%（issue「token 耗尽」）。
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

// 数字缩写：气泡只有圆环那么宽，千分位全写（996,000）会把它折成两行。
describe("compactTokens", () => {
  it("千位以下原样输出", () => {
    expect(compactTokens(0)).toBe("0");
    expect(compactTokens(999)).toBe("999");
  });

  it("千位用 k、百万用 M，小数只在有效时保留", () => {
    expect(compactTokens(1000)).toBe("1k");
    expect(compactTokens(1500)).toBe("1.5k");
    expect(compactTokens(1249)).toBe("1.2k");
    expect(compactTokens(157_000)).toBe("157k");
    expect(compactTokens(265_000)).toBe("265k");
    expect(compactTokens(996_000)).toBe("996k");
    expect(compactTokens(1_000_000)).toBe("1M");
    expect(compactTokens(1_500_000)).toBe("1.5M");
  });

  it("负数、NaN 与小数为脏数据兜底", () => {
    expect(compactTokens(-5)).toBe("0");
    expect(compactTokens(Number.NaN)).toBe("0");
    expect(compactTokens(1249.6)).toBe("1.3k");
  });
});

// 估算口径：CJK ≈0.7 token/字、其余（含每条消息 12 字符的信封开销）≈0.25
// token/字符；工具载荷按 96KB/条封顶——仅给「上游从不报 usage」的模型兜底。
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

// 悬停气泡：标题「背景信息窗口：」+ 最多三行（占用 / 缓存命中 / 累计计费）；
// 纯函数保证文案口径与「无缓存不新增空行」的节制。
describe("contextTooltipLines", () => {
  const usage: ContextUsageView = {
    pct: 11, color: "bg-success", win: 996_000, label: "11%", used: 109_000, estimated: false,
  };

  it("标题 + 占用两行（对齐参考截图，不含计费行）", () => {
    expect(contextTooltipLines(usage, null)).toEqual({
      title: "背景信息窗口：",
      rows: ["11% 已用（剩余 89%）", "已用 109k 标记，共 996k"],
      ariaLabel: "背景信息窗口；11% 已用（剩余 89%）；已用 109k 标记，共 996k",
    });
  });

  it("缓存命中行只出现在真实命中时", () => {
    const tip = contextTooltipLines(usage, { promptTokens: 109_000, completionTokens: 0, totalTokens: 109_000, cachedTokens: 96_000 })!;
    expect(tip.rows[2]).toBe("缓存命中 96k 标记");
    // 0 / 缺失（模型不报缓存）都不加空行。
    expect(contextTooltipLines(usage, { promptTokens: 1, completionTokens: 1, totalTokens: 2, cachedTokens: 0 })!.rows)
      .toEqual(["11% 已用（剩余 89%）", "已用 109k 标记，共 996k", "累计 2 tokens"]);
  });

  it("累计与费用收在最后一行", () => {
    expect(contextTooltipLines(usage, { promptTokens: 150_000, completionTokens: 7_000, totalTokens: 157_000 }, "¥0.31")!.rows)
      .toEqual([
        "11% 已用（剩余 89%）",
        "已用 109k 标记，共 996k",
        "累计 157k tokens · ¥0.31",
      ]);
  });

  it("估算视图带 ≈ 标注与口径说明", () => {
    const est: ContextUsageView = { ...usage, label: "≈11%", estimated: true };
    const tip = contextTooltipLines(est, null)!;
    expect(tip.rows[0]).toBe("≈11% 已用（剩余 89%）");
    expect(tip.rows).toContain("按会话内容估算——当前模型未返回用量");
  });

  it("窗口未知时只保留缓存与累计（Coins 兜底图标）", () => {
    expect(contextTooltipLines(null, { promptTokens: 120, completionTokens: 80, totalTokens: 200, cachedTokens: 150 })!.rows)
      .toEqual(["缓存命中 150 标记", "累计 200 tokens"]);
    expect(contextTooltipLines(null, { promptTokens: 120, completionTokens: 80, totalTokens: 200 })!.rows)
      .toEqual(["累计 200 tokens"]);
  });

  it("无数据不渲染", () => {
    expect(contextTooltipLines(null, null)).toBeNull();
    expect(contextTooltipLines(null, { promptTokens: 0, completionTokens: 0, totalTokens: 0 })).toBeNull();
  });
});
