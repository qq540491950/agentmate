import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { ReasoningLevel } from "@/lib/api";
import { ReasoningEffortPanel } from "./ReasoningEffortPanel";

const levels: ReasoningLevel[] = [
  { level: "low", param: "reasoning_effort", value: "low" },
  { level: "medium", param: "reasoning_effort", value: "medium" },
  { level: "high", param: "reasoning_effort", value: "high" },
  { level: "max", param: "reasoning_effort", value: "max" },
];

describe("ReasoningEffortPanel", () => {
  it("renders the Codex-style current level, model subtitle and one tick per stop", () => {
    const html = renderToStaticMarkup(
      <ReasoningEffortPanel
        levels={levels}
        value="max"
        modelName="DeepSeek V4.1 Flash · 腾讯云服务器"
        onChange={vi.fn()}
        onOpenModelPicker={vi.fn()}
      />,
    );
    expect(html).toContain('data-slot="reasoning-effort-panel"');
    expect(html).toContain(">Max<");
    expect(html).toContain("DeepSeek V4.1 Flash · 腾讯云服务器");
    expect(html.match(/data-slot="reasoning-tick"/g)).toHaveLength(4);
    expect(html).toContain('aria-valuetext="Max"');
  });

  it("hides the model shortcut when no model picker is provided", () => {
    const html = renderToStaticMarkup(
      <ReasoningEffortPanel levels={levels} value="high" onChange={vi.fn()} />,
    );
    expect(html).toContain(">高<");
    expect(html).not.toContain("lucide-chevron-right");
  });
});
