import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { TurnBlock } from "./TurnBlock";
import type { TurnItem } from "@/lib/chatUtils";

const turn: TurnItem = {
  kind: "turn",
  id: "u1",
  header: {
    id: "u1",
    kind: "user",
    author: "user",
    text: "请检查项目并提交修复",
    timestamp: "2026-11-09T10:00:00Z",
  },
  body: [{
    kind: "message",
    id: "a1",
    message: {
      id: "a1",
      kind: "assistant",
      author: "助手",
      text: "已检查并提交。",
      timestamp: "2026-11-09T10:01:00Z",
    },
  }],
};

function render(active = false) {
  return renderToStaticMarkup(
    <TurnBlock
      turn={turn}
      active={active}
      live={false}
      isLastTurn
      onEdit={() => {}}
      canEdit={false}
      filterQ=""
      renderBodyItem={() => <div data-body-item="a1" />}
    />,
  );
}

function openingTag(html: string, slot: string): string {
  return new RegExp(`<div[^>]*data-slot="${slot}"[^>]*>`).exec(html)?.[0] ?? "";
}

describe("TurnBlock", () => {
  it("用户气泡与助手正文独立渲染，不共用外卡", () => {
    const html = render();
    const blockTag = openingTag(html, "turn-block");
    const userTag = openingTag(html, "user-bubble");

    expect(blockTag).not.toContain("ring-1");
    expect(blockTag).not.toContain("rounded-2xl");
    expect(userTag).toContain("justify-end");
    expect(html).toContain('data-turn-anchor="u1"');
    expect(html.indexOf('data-slot="user-bubble"')).toBeLessThan(html.indexOf('data-slot="turn-body"'));
    expect(html).toContain('data-body-item="a1"');
  });

  it("当前轮只高亮用户气泡，不把助手正文包进高亮卡", () => {
    const html = render(true);
    const blockTag = openingTag(html, "turn-block");
    const bubbleTag = openingTag(html, "user-bubble-body");

    expect(bubbleTag).toContain("bg-brand/15");
    expect(bubbleTag).toContain("ring-brand/35");
    expect(blockTag).not.toContain("ring-brand");
  });
});
