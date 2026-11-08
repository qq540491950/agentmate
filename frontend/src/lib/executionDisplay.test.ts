import { describe, expect, it } from "vitest";
import {
  effectiveExecutionDisplayMode,
  executionAutoExpanded,
  executionDisplayLabel,
  executionHidden,
  normalizeExecutionDisplayMode,
} from "./executionDisplay";

describe("execution display modes", () => {
  it("normalizes stale global values to auto", () => {
    expect(normalizeExecutionDisplayMode("")).toBe("auto");
    expect(normalizeExecutionDisplayMode("stale")).toBe("auto");
    expect(normalizeExecutionDisplayMode(" CONCISE ")).toBe("concise");
  });

  it("prefers a valid session override but falls back to the global mode", () => {
    expect(effectiveExecutionDisplayMode("concise", "detailed")).toBe("detailed");
    expect(effectiveExecutionDisplayMode("concise", "stale")).toBe("concise");
    expect(effectiveExecutionDisplayMode("stale", "")).toBe("auto");
  });

  it("expands according to each mode", () => {
    expect(executionAutoExpanded("auto", "running")).toBe(true);
    expect(executionAutoExpanded("auto", "done")).toBe(false);
    expect(executionAutoExpanded("concise", "running")).toBe(false);
    expect(executionAutoExpanded("concise", "confirm")).toBe(true);
    expect(executionAutoExpanded("detailed", "done")).toBe(true);
    expect(executionAutoExpanded("none", "confirm")).toBe(true);
  });

  it("only hides in none mode and never hides a pending confirmation", () => {
    expect(executionHidden("none", "running")).toBe(true);
    expect(executionHidden("none", "done")).toBe(true);
    expect(executionHidden("none", "confirm")).toBe(false);
    expect(executionHidden("concise", "done")).toBe(false);
  });

  it("has labels for every mode", () => {
    expect(executionDisplayLabel("auto")).toBe("自动");
    expect(executionDisplayLabel("none")).toBe("无");
  });
});
