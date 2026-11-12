import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ToolCallGroup } from "./ToolCallGroup";
import type { ToolGroup, UIMessage } from "@/lib/chatUtils";

const THINKING_TEXT = [
  "先确认工作区状态，避免覆盖未提交的修改。",
  "然后运行后端与前端测试，确认没有回归。",
  "最后把结果整理成回复。",
].join("\n");

function processMessages(): UIMessage[] {
  const messages: UIMessage[] = [{
    id: "think-1",
    kind: "thinking",
    author: "助手",
    text: THINKING_TEXT,
    timestamp: "2026-11-12T01:00:00Z",
  }];
  for (let i = 0; i < 12; i++) {
    messages.push({
      id: `call-${i}`,
      kind: "tool_call",
      author: "助手",
      text: "",
      toolName: "run_command",
      toolArgs: { command: `git status --short --untracked-files=all -- path/${i}` },
      timestamp: `2026-11-12T01:00:${String(i + 1).padStart(2, "0")}Z`,
    });
    messages.push({
      id: `result-${i}`,
      kind: "tool_result",
      author: "助手",
      text: "",
      toolName: "run_command",
      toolResp: { ok: true },
      timestamp: `2026-11-12T01:00:${String(i + 1).padStart(2, "0")}Z`,
    });
  }
  return messages;
}

function renderGroup(displayMode: "auto" | "concise" | "detailed" | "none" = "detailed") {
  const group: ToolGroup = {
    kind: "tool_group",
    id: "think-1",
    author: "助手",
    messages: processMessages(),
    answered: true,
    errored: false,
  };
  return renderToStaticMarkup(
    <ToolCallGroup
      group={group}
      runningTools={new Set()}
      durations={new Map()}
      sessionID="s1"
      filterQ=""
      displayMode={displayMode}
      onConfirm={vi.fn()}
    />,
  );
}

describe("ToolCallGroup", () => {
  it("caps the expanded step list in its own scroll region", () => {
    const html = renderGroup();
    expect(html).toContain('data-slot="execution-scroll"');
    expect(html).toMatch(/max-h-64/);
    expect(html).toMatch(/overflow-y-auto/);
  });

  it("keeps thinking as a one-line row instead of a prose block", () => {
    const html = renderGroup();
    expect(html).toContain('data-slot="thinking-row"');
    expect(html).not.toContain('data-slot="thinking-body"');
  });
});
