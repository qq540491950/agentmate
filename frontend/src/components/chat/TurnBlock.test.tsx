import { renderToStaticMarkup } from "react-dom/server";
import type { ComponentProps } from "react";
import { describe, expect, it } from "vitest";
import { TurnBlock } from "./TurnBlock";
import { ToolCallGroup } from "./ToolCallGroup";
import {
  computeRunningTools,
  groupToolMessages,
  groupTurns,
  type TurnItem,
  type UIMessage,
} from "@/lib/chatUtils";

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

function render(active = false, overrides: Partial<ComponentProps<typeof TurnBlock>> = {}) {
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
      {...overrides}
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

    expect(bubbleTag).toContain("bg-accent-soft");
    expect(bubbleTag).not.toContain("ring-");
    expect(blockTag).not.toContain("ring-");
  });

  it("首包前在用户气泡下方显示思考状态，且不新增消息正文", () => {
    const html = render(false, {
      turn: { ...turn, body: [] },
      waiting: true,
    });

    expect(html).toContain('data-slot="turn-status"');
    expect(html).toContain("正在思考");
    expect(html).not.toContain('data-slot="turn-body"');
  });

  it("同一轮只渲染一个执行容器并保持运行中", () => {
    const messages: UIMessage[] = [
      { id: "u2", kind: "user", author: "user", text: "分析当前项目", timestamp: "2026-11-15T01:00:00Z" },
      { id: "c1", kind: "tool_call", author: "助手", text: "", toolName: "read_workspace_file", timestamp: "2026-11-15T01:00:01Z" },
      { id: "r1", kind: "tool_result", author: "助手", text: "", toolName: "read_workspace_file", toolResp: { ok: true }, timestamp: "2026-11-15T01:00:02Z" },
      { id: "mid1", kind: "assistant", author: "助手", text: "先看项目结构。", timestamp: "2026-11-15T01:00:03Z" },
      { id: "c2", kind: "tool_call", author: "助手", text: "", toolName: "run_command", timestamp: "2026-11-15T01:00:04Z" },
    ];
    const liveTurn = groupTurns(groupToolMessages(messages))[0];
    if (liveTurn.kind !== "turn") throw new Error("expected a turn");
    expect(liveTurn.body.filter((item) => item.kind === "tool_group")).toHaveLength(1);
    const runningTools = computeRunningTools(messages);
    const html = renderToStaticMarkup(
      <TurnBlock
        turn={liveTurn}
        active={false}
        live
        isLastTurn
        onEdit={() => {}}
        canEdit={false}
        filterQ=""
        renderBodyItem={(item, opts) => item.kind === "tool_group" ? (
          <ToolCallGroup
            group={item}
            live={opts.liveGroup}
            runningTools={runningTools}
            durations={new Map()}
            sessionID="s1"
            filterQ=""
            displayMode="auto"
            onConfirm={() => {}}
          />
        ) : <p>{item.message.text}</p>}
      />,
    );
    const states = [...html.matchAll(/data-state="(confirm|running|failed|done)"/g)].map((match) => match[1]);
    expect(states).toEqual(["running"]);
    expect(html.match(/>运行中</g)).toHaveLength(1);
  });
});
