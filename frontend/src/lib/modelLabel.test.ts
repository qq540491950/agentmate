import { describe, expect, it } from "vitest";
import { levelLabel, modelLabel, reasoningLevels, validateModelReasoning } from "./modelLabel";
import type { ModelInfo } from "@/lib/api";

describe("modelLabel", () => {
  it("prefers the display name", () => {
    expect(modelLabel({ id: "codearts/glm-5.3-flash", displayName: "GLM-5.3 Flash" })).toBe("GLM-5.3 Flash");
  });

  it("falls back to the id for blank or missing display names", () => {
    expect(modelLabel({ id: "gpt-5", displayName: "   " })).toBe("gpt-5");
    expect(modelLabel({ id: "gpt-5" })).toBe("gpt-5");
    expect(modelLabel(null)).toBe("");
    expect(modelLabel(undefined)).toBe("");
  });
});

describe("reasoningLevels", () => {
  const model = (enabled: boolean, levels: { level: string; value: string }[]) =>
    ({ id: "m", reasoning: { enabled, levels } }) as unknown as ModelInfo;

  it("returns levels only when the table is enabled and non-empty", () => {
    expect(reasoningLevels(model(true, [{ level: "high", value: "high" }]))).toHaveLength(1);
    expect(reasoningLevels(model(false, [{ level: "high", value: "high" }]))).toEqual([]);
    expect(reasoningLevels(model(true, []))).toEqual([]);
    expect(reasoningLevels(null)).toEqual([]);
  });
});

describe("levelLabel", () => {
  it("maps common levels and keeps custom labels", () => {
    expect(levelLabel("off")).toBe("关闭");
    expect(levelLabel("HIGH")).toBe("高");
    expect(levelLabel("turbo")).toBe("turbo");
  });
});

describe("validateModelReasoning", () => {
  const model = (reasoning: ModelInfo["reasoning"]) => ({ id: "m", reasoning }) as ModelInfo;

  it("accepts disabled and empty tables", () => {
    expect(validateModelReasoning(model(undefined))).toBeNull();
    expect(validateModelReasoning(model({ enabled: false, levels: [] }))).toBeNull();
  });

  it("requires at least one level when enabled", () => {
    expect(validateModelReasoning(model({ enabled: true, levels: [] }))).toContain("至少添加一个档位");
  });

  it("validates the JSON-object form for an empty parameter name", () => {
    expect(validateModelReasoning(model({
      enabled: true,
      levels: [{ level: "high", value: "high" }],
    }))).toContain("JSON 对象");
    expect(validateModelReasoning(model({
      enabled: true,
      levels: [{ level: "high", value: `{"type":"enabled"}` }],
    }))).toBeNull();
  });

  it("rejects duplicate levels and a dangling default", () => {
    expect(validateModelReasoning(model({
      enabled: true,
      levels: [
        { level: "high", param: "reasoning_effort", value: "high" },
        { level: "HIGH", param: "reasoning_effort", value: "high" },
      ],
    }))).toContain("重复");
    expect(validateModelReasoning(model({
      enabled: true,
      defaultLevel: "max",
      levels: [{ level: "high", param: "reasoning_effort", value: "high" }],
    }))).toContain("默认档");
  });
});