import { describe, expect, it } from "vitest";
import { resolveContextUsage } from "./contextUsage";

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
});
