import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { MessageBubble } from "./MessageBubble";
import type { UIMessage } from "@/lib/chatUtils";

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

  it("keeps the author label for named agents and team members", () => {
    const html = render({ ...assistant, author: "代码审查员" });
    expect(html).toContain("代码审查员");
  });
});
