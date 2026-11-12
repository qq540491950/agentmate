import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { MessageBubble } from "./MessageBubble";
import { fmtTime, type UIMessage } from "@/lib/chatUtils";

const assistant: UIMessage = {
  id: "a1",
  kind: "assistant",
  author: "助手",
  text: "已检查并提交。",
  timestamp: "2026-11-11T10:01:00Z",
};

function render(message: UIMessage = assistant) {
  return renderToStaticMarkup(
    <MessageBubble
      m={message}
      isLastAssistant
      filterQ=""
      onRegenerate={vi.fn()}
      canRegenerate
      toolRunning={false}
      onSpeak={vi.fn()}
      costOf={() => ""}
      onEdit={vi.fn()}
      canEdit={false}
      onConfirm={vi.fn()}
      sessionID="s1"
    />,
  );
}

describe("MessageBubble", () => {
  it("does not render the redundant direct-assistant header", () => {
    const html = render();
    expect(html).not.toContain("助手");
    expect(html).toContain('data-slot="assistant-actions"');
    expect(html).toContain("朗读这条回复");
  });

  it("shows the completion time after the reply actions", () => {
    const html = render();
    const speakButton = html.indexOf("朗读这条回复");
    const completionTime = html.indexOf('data-slot="completion-time"');
    expect(speakButton).toBeGreaterThan(-1);
    expect(completionTime).toBeGreaterThan(speakButton);
    expect(html).toMatch(
      new RegExp(`data-slot="completion-time"[^>]*>${fmtTime(assistant.timestamp)}<\\/time>`),
    );
  });

  it("reveals the completion time together with the reply actions", () => {
    const html = render();
    expect(html).toMatch(
      /data-slot="assistant-actions"[^>]*opacity-0[^>]*group-hover:opacity-100[^>]*group-focus-within:opacity-100/,
    );
  });

  it("keeps the author label for named agents and team members", () => {
    const html = render({ ...assistant, author: "代码审查员" });
    expect(html).toContain("代码审查员");
  });

  it("shows the backend hint and highlights boundary reasons", () => {
    const html = render({
      id: "c1",
      kind: "confirm",
      author: "助手",
      text: "",
      toolName: "run_command",
      callId: "call_1",
      hint: "脱离沙箱执行终端命令。理由：需要写入共享缓存",
      confirmStatus: "pending",
    });
    expect(html).toContain("脱离沙箱执行终端命令。理由：需要写入共享缓存");
    expect(html).toContain("bg-warning-soft");
    expect(html).toContain("lucide-triangle-alert");
  });

  it("never falls back to toolArgs alone when the hint is missing", () => {
    const html = render({
      id: "c2",
      kind: "confirm",
      author: "助手",
      text: "",
      toolName: "mcp_echo",
      callId: "call_2",
      toolArgs: { message: "hello" },
      confirmStatus: "pending",
    });
    expect(html).toContain("该工具调用需要你的确认。");
  });
});
