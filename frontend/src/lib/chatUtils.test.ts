import { describe, expect, it } from "vitest";
import {
  buildSidebarProjectGroups, buildToolTrace, chatItemEndTs, chatItemStartTs, computeEditFlags, computeRunningTools,
  computeToolDurations, dayLabel, effectiveReasoningLevel, fmtTime, formatWorkDuration, groupSessionsByProject,
  formatToolExecutionBreakdown, groupSessionsByTime, groupToolMessages, groupTurns, isAwaitingReply, isDayChange, mdToSpeech,
  projectSessionWindow, sessionTargetBadgeLabel, shouldExpandProjectForSession,
  summarizeToolTrace, toolStepDisplay,
  toolGroupDurationMs, toolGroupElapsedMs, toolGroupInvocationCount, toolGroupStatus,
  toolResultFailed, type ToolGroup, type TurnItem, type UIMessage,
} from "./chatUtils";
import type { ChatSession, Project } from "@/lib/api";

const msg = (over: Partial<UIMessage>): UIMessage => ({
  id: over.id ?? Math.random().toString(),
  kind: "assistant",
  author: "bot",
  text: "",
  ...over,
});

describe("groupTurns", () => {
  it("wraps each user message and its run into one turn", () => {
    const items = groupToolMessages([
      msg({ id: "u1", kind: "user", author: "user", text: "q1", timestamp: "2026-11-08T01:00:00Z" }),
      msg({ id: "c1", kind: "tool_call", toolName: "run_command", timestamp: "2026-11-08T01:00:05Z" }),
      msg({ id: "r1", kind: "tool_result", toolName: "run_command", toolResp: { ok: true }, timestamp: "2026-11-08T01:00:06Z" }),
      msg({ id: "a1", kind: "assistant", text: "done", timestamp: "2026-11-08T01:00:10Z" }),
      msg({ id: "u2", kind: "user", author: "user", text: "q2", timestamp: "2026-11-08T01:01:00Z" }),
      msg({ id: "a2", kind: "assistant", text: "ok", timestamp: "2026-11-08T01:01:05Z" }),
    ]);
    const turns = groupTurns(items);
    expect(turns.map((t) => t.kind)).toEqual(["turn", "turn"]);
    const t0 = turns[0] as TurnItem;
    expect(t0.id).toBe("u1");
    expect(t0.body.map((b) => b.kind)).toEqual(["tool_group", "message"]);
    const t1 = turns[1] as TurnItem;
    expect(t1.id).toBe("u2");
    expect(t1.body).toHaveLength(1);
  });

  it("renders orphan entries before the first user message standalone", () => {
    const items = groupToolMessages([
      msg({ id: "e1", kind: "error", text: "boom" }),
      msg({ id: "u1", kind: "user", author: "user", text: "q" }),
      msg({ id: "a1", kind: "assistant", text: "hi" }),
    ]);
    const rendered = groupTurns(items);
    expect(rendered[0].kind).toBe("message");
    expect(rendered[1].kind).toBe("turn");
  });

  it("spans a turn from its header to the last body entry for day separators", () => {
    const rendered = groupTurns(groupToolMessages([
      msg({ id: "u1", kind: "user", author: "user", text: "q", timestamp: "2026-11-08T01:00:00Z" }),
      msg({ id: "a1", kind: "assistant", text: "hi", timestamp: "2026-11-08T03:00:00Z" }),
    ]));
    const turn = rendered[0] as TurnItem;
    expect(chatItemStartTs(turn)).toBe("2026-11-08T01:00:00Z");
    expect(chatItemEndTs(turn)).toBe("2026-11-08T03:00:00Z");
    expect(chatItemEndTs({ ...turn, body: [] })).toBe("2026-11-08T01:00:00Z");
  });

  it("merges split user-event parts that carried the same id", () => {
    const items = groupToolMessages([
      msg({ id: "u1", kind: "user", author: "user", text: "看这两张图" }),
      msg({
        id: "u1", kind: "user", author: "user", text: "",
        attachments: [{ name: "a.png", mime: "image/png", dataUrl: "data:image/png;base64,AA==" }],
      }),
      msg({
        id: "u1", kind: "user", author: "user", text: "",
        attachments: [{ name: "b.png", mime: "image/png", dataUrl: "data:image/png;base64,AA==" }],
      }),
      msg({ id: "a1", kind: "assistant", text: "收到" }),
    ]);

    const turns = groupTurns(items);
    expect(turns).toHaveLength(1);
    const turn = turns[0] as TurnItem;
    expect(turn.id).toBe("u1");
    expect(turn.header.text).toBe("看这两张图");
    expect(turn.header.attachments?.map((a) => a.name)).toEqual(["a.png", "b.png"]);
    expect(turn.body).toHaveLength(1);
  });
});

describe("isAwaitingReply", () => {
  it("只在本轮最后一条仍是用户消息时显示首包等待态", () => {
    const user = msg({ id: "u1", kind: "user", author: "user", text: "开始" });
    expect(isAwaitingReply([], true)).toBe(true);
    expect(isAwaitingReply([user], true)).toBe(true);
    expect(isAwaitingReply([user], false)).toBe(false);
  });

  it("思考流、正文、工具或确认卡一到就交给各自的行内状态", () => {
    const user = msg({ id: "u1", kind: "user", author: "user", text: "开始" });
    expect(isAwaitingReply([user, msg({ id: "t1", kind: "thinking", streaming: true })], true)).toBe(false);
    expect(isAwaitingReply([user, msg({ id: "a1", kind: "assistant", streaming: true })], true)).toBe(false);
    expect(isAwaitingReply([user, msg({ id: "c1", kind: "tool_call", toolName: "read" })], true)).toBe(false);
    expect(isAwaitingReply([user, msg({ id: "r1", kind: "tool_result", toolName: "read" })], true)).toBe(false);
    expect(isAwaitingReply([user, msg({ id: "cf", kind: "confirm", toolName: "write" })], true)).toBe(false);
  });
});

describe("computeToolDurations", () => {
  it("pairs results by tool name and restarts the clock at confirmation", () => {
    const t0 = "2026-10-30T01:00:00Z";
    const msgs: UIMessage[] = [
      msg({ id: "c1", kind: "tool_call", toolName: "write", timestamp: t0 }),
      msg({ id: "c2", kind: "tool_call", toolName: "read", timestamp: t0 }),
      // Parallel round: result B arrives before result A — name pairing must
      // not end A's clock early.
      msg({ id: "r2", kind: "tool_result", toolName: "read", timestamp: "2026-10-30T01:00:10Z" }),
      msg({ id: "r1", kind: "tool_result", toolName: "write", timestamp: "2026-10-30T01:00:20Z" }),
    ];
    const d = computeToolDurations(msgs);
    expect(d.get("r2")).toBe(10_000);
    expect(d.get("r1")).toBe(20_000);
  });

  it("re-arms the duration clock at a HITL confirmation card", () => {
    const msgs: UIMessage[] = [
      msg({ id: "c1", kind: "tool_call", toolName: "write", timestamp: "2026-10-30T01:00:00Z" }),
      msg({ id: "cf", kind: "confirm", toolName: "write", timestamp: "2026-10-30T01:05:00Z" }),
      msg({ id: "r1", kind: "tool_result", toolName: "write", timestamp: "2026-10-30T01:05:03Z" }),
    ];
    // The 5 minutes of human thinking must not count into the tool duration.
    expect(computeToolDurations(msgs).get("r1")).toBe(3_000);
  });
});

describe("computeRunningTools", () => {
  it("keeps parallel calls spinning until their own result arrives", () => {
    const msgs: UIMessage[] = [
      msg({ id: "a", kind: "tool_call", toolName: "A" }),
      msg({ id: "b", kind: "tool_call", toolName: "B" }),
      msg({ id: "rb", kind: "tool_result", toolName: "B" }),
    ];
    const running = computeRunningTools(msgs);
    expect(running.has("a")).toBe(true); // A still running
    expect(running.has("b")).toBe(false); // B finished
  });
});

describe("groupToolMessages", () => {
  it("merges consecutive tool events of one author into one block", () => {
    const msgs: UIMessage[] = [
      msg({ id: "u", kind: "user", text: "hi" }),
      msg({ id: "c1", kind: "tool_call", toolName: "read" }),
      msg({ id: "r1", kind: "tool_result", toolName: "read", toolResp: { ok: true } }),
      msg({ id: "c2", kind: "tool_call", toolName: "write" }),
      msg({ id: "r2", kind: "tool_result", toolName: "write", toolResp: { ok: true } }),
      msg({ id: "a", kind: "assistant", text: "done" }),
    ];
    const items = groupToolMessages(msgs);
    expect(items.map((i) => i.kind)).toEqual(["message", "tool_group", "message"]);
    const group = items[1];
    if (group.kind !== "tool_group") throw new Error("expected a tool group");
    // The group key is the first event's id: it must survive streamed appends
    // or React would reset the user's expanded/collapsed choice every token.
    expect(group.id).toBe("c1");
    expect(group.messages.map((m) => m.id)).toEqual(["c1", "r1", "c2", "r2"]);
    expect(toolGroupInvocationCount(group.messages)).toBe(2);
  });

  it("groups a running parallel round before any result arrives", () => {
    const msgs: UIMessage[] = [
      msg({ id: "c1", kind: "tool_call", toolName: "read" }),
      msg({ id: "c2", kind: "tool_call", toolName: "grep" }),
    ];
    const items = groupToolMessages(msgs);
    expect(items).toHaveLength(1);
    expect(items[0]).toMatchObject({ kind: "tool_group", id: "c1" });
  });

  it("folds a single invocation into one execution block", () => {
    const msgs: UIMessage[] = [
      msg({ id: "c1", kind: "tool_call", toolName: "read" }),
      msg({ id: "r1", kind: "tool_result", toolName: "read" }),
    ];
    const items = groupToolMessages(msgs);
    expect(items.map((i) => i.kind)).toEqual(["tool_group"]);
    const group = items[0];
    if (group.kind !== "tool_group") throw new Error("expected a tool group");
    expect(toolGroupInvocationCount(group.messages)).toBe(1);
  });

  it("keeps narration inside the turn block and splits at author changes; plan_todos cards stay pinned without splitting", () => {
    const msgs: UIMessage[] = [
      msg({ id: "c1", kind: "tool_call", toolName: "read" }),
      msg({ id: "r1", kind: "tool_result", toolName: "read" }),
      msg({ id: "mid", kind: "assistant", text: "next" }),
      msg({ id: "c2", kind: "tool_call", toolName: "write" }),
      msg({ id: "r2", kind: "tool_result", toolName: "write" }),
      msg({ id: "c3", kind: "tool_call", toolName: "read" }),
      msg({ id: "r3", kind: "tool_result", toolName: "read" }),
      msg({ id: "todos", kind: "tool_result", toolName: "plan_todos", toolResp: { todos: [] } }),
      msg({ id: "c4", kind: "tool_call", toolName: "read", author: "sub-agent" }),
      msg({ id: "r4", kind: "tool_result", toolName: "read", author: "sub-agent" }),
      msg({ id: "c5", kind: "tool_call", toolName: "grep", author: "sub-agent" }),
      msg({ id: "r5", kind: "tool_result", toolName: "grep", author: "sub-agent" }),
    ];
    const items = groupToolMessages(msgs);
    // Narration between tool batches no longer breaks the run; the plan_todos
    // card stays standalone and floats above the block it used to split (the
    // author change at c4 still starts a new block).
    expect(items.map((i) => (i.kind === "tool_group" ? `group:${i.id}` : i.id))).toEqual([
      "todos", "group:c1", "group:c4",
    ]);
    const first = items.find((i) => i.kind === "tool_group" && i.id === "c1");
    if (!first || first.kind !== "tool_group") throw new Error("expected a tool group");
    expect(first.messages.map((m) => m.id)).toEqual(["c1", "r1", "mid", "c2", "r2", "c3", "r3"]);
    const third = items.find((i) => i.kind === "tool_group" && i.author === "sub-agent");
    if (!third || third.kind !== "tool_group") throw new Error("expected a tool group");
    expect(third.author).toBe("sub-agent");
  });

  it("keeps one execution block across repeated plan_todos updates", () => {
    // The reported render: a long analysis turn that refreshed its plan three
    // times was shredded into six look-alike failed groups — every plan_todos
    // call/result used to flush the run. The block must span the whole turn;
    // the todo cards stay standalone (pinned-plan semantics).
    const msgs: UIMessage[] = [
      msg({ id: "t1", kind: "thinking", text: "先看结构" }),
      msg({ id: "p1c", kind: "tool_call", toolName: "plan_todos" }),
      msg({ id: "p1r", kind: "tool_result", toolName: "plan_todos", toolResp: { todos: [{ content: "梳理问题", status: "in_progress" }] } }),
      msg({ id: "c1", kind: "tool_call", toolName: "run_command" }),
      msg({ id: "r1", kind: "tool_result", toolName: "run_command" }),
      msg({ id: "c2", kind: "tool_call", toolName: "read" }),
      msg({ id: "r2", kind: "tool_result", toolName: "read" }),
      msg({ id: "p2c", kind: "tool_call", toolName: "plan_todos" }),
      msg({ id: "p2r", kind: "tool_result", toolName: "plan_todos", toolResp: { todos: [{ content: "梳理问题", status: "completed" }] } }),
      msg({ id: "c3", kind: "tool_call", toolName: "run_command" }),
      msg({ id: "r3", kind: "tool_result", toolName: "run_command" }),
      msg({ id: "a", kind: "assistant", text: "结论" }),
    ];
    const items = groupToolMessages(msgs);
    const groups = items.filter((i) => i.kind === "tool_group");
    expect(groups).toHaveLength(1);
    if (groups[0].kind !== "tool_group") throw new Error("expected a tool group");
    expect(groups[0].messages.map((m) => m.id)).toEqual(["t1", "c1", "r1", "c2", "r2", "c3", "r3"]);
    const todoCardIds = items
      .filter((i) => i.kind === "message" && i.message.toolName === "plan_todos")
      .map((i) => (i.kind === "message" ? i.message.id : ""));
    expect(todoCardIds).toEqual(["p1c", "p1r", "p2c", "p2r"]);
  });

  it("folds an empty skeleton and inter-tool narration into one turn block", () => {
    // The reported render: thinking card, empty "助手" skeleton, tool card,
    // then the reply — four look-alike blocks for a single turn.
    const msgs: UIMessage[] = [
      msg({ id: "u", kind: "user", text: "处理缺陷" }),
      msg({ id: "t1", kind: "thinking", text: "先跑 gh", timestamp: "2026-10-30T01:00:00Z" }),
      msg({ id: "skel", kind: "assistant", text: "" }),
      msg({ id: "c1", kind: "tool_call", toolName: "terminal" }),
      msg({ id: "r1", kind: "tool_result", toolName: "terminal", toolResp: { ok: true } }),
      msg({ id: "mid", kind: "assistant", text: "我来读取模板" }),
      msg({ id: "c2", kind: "tool_call", toolName: "read" }),
      msg({ id: "r2", kind: "tool_result", toolName: "read", toolResp: { ok: true } }),
      msg({ id: "a", kind: "assistant", text: "提票完成", timestamp: "2026-10-30T01:02:04Z" }),
    ];
    const items = groupToolMessages(msgs);
    expect(items.map((i) => (i.kind === "tool_group" ? `group:${i.id}` : i.id))).toEqual([
      "u", "group:t1", "a",
    ]);
    const group = items[1];
    if (group.kind !== "tool_group") throw new Error("expected a tool group");
    expect(group.messages.map((m) => m.id)).toEqual(["t1", "c1", "r1", "mid", "c2", "r2"]);
    // endTs feeds the whole-turn 「已工作」 summary.
    expect(group.endTs).toBe("2026-10-30T01:02:04Z");
  });

  it("drops a settled empty assistant shell that ends a tool turn", () => {
    const msgs: UIMessage[] = [
      msg({ id: "u", kind: "user", text: "hi" }),
      msg({ id: "c1", kind: "tool_call", toolName: "read" }),
      msg({ id: "r1", kind: "tool_result", toolName: "read", toolResp: { ok: true } }),
      msg({ id: "c2", kind: "tool_call", toolName: "read" }),
      msg({ id: "r2", kind: "tool_result", toolName: "read", toolResp: { ok: true } }),
      msg({ id: "skel", kind: "assistant", text: "", streaming: false }),
    ];
    const items = groupToolMessages(msgs);
    expect(items.map((i) => (i.kind === "tool_group" ? `group:${i.id}` : i.id))).toEqual([
      "u", "group:c1",
    ]);
  });

  it("keeps the live placeholder while a turn has no process event yet", () => {
    const msgs: UIMessage[] = [
      msg({ id: "u", kind: "user", text: "hi" }),
      msg({ id: "live", kind: "assistant", text: "", streaming: true }),
    ];
    expect(groupToolMessages(msgs).map((i) => (i.kind === "tool_group" ? `group:${i.id}` : i.id))).toEqual([
      "u", "live",
    ]);
  });

  it("merges thinking entries with the turn's tool run and keeps the reply separate", () => {
    const msgs: UIMessage[] = [
      msg({ id: "u", kind: "user", text: "分析" }),
      msg({ id: "t1", kind: "thinking", text: "先读文件" }),
      msg({ id: "c1", kind: "tool_call", toolName: "read" }),
      msg({ id: "r1", kind: "tool_result", toolName: "read", toolResp: { ok: true } }),
      msg({ id: "t2", kind: "thinking", text: "再读一次" }),
      msg({ id: "c2", kind: "tool_call", toolName: "read" }),
      msg({ id: "r2", kind: "tool_result", toolName: "read", toolResp: { ok: true } }),
      msg({ id: "t3", kind: "thinking", text: "整理结论" }),
      msg({ id: "a", kind: "assistant", text: "结论如下" }),
    ];
    const items = groupToolMessages(msgs);
    expect(items.map((i) => (i.kind === "tool_group" ? `group:${i.id}` : i.id))).toEqual([
      "u", "group:t1", "a",
    ]);
    const group = items[1];
    if (group.kind !== "tool_group") throw new Error("expected a tool group");
    expect(group.messages.map((m) => m.id)).toEqual([
      "t1", "c1", "r1", "t2", "c2", "r2", "t3",
    ]);
    // Thinking entries are process detail, not tool invocations.
    expect(toolGroupInvocationCount(group.messages)).toBe(2);
  });

  it("groups thinking with a single tool invocation", () => {
    const msgs: UIMessage[] = [
      msg({ id: "t1", kind: "thinking", text: "先确认" }),
      msg({ id: "c1", kind: "tool_call", toolName: "read" }),
      msg({ id: "r1", kind: "tool_result", toolName: "read" }),
    ];
    const items = groupToolMessages(msgs);
    expect(items).toHaveLength(1);
    expect(items[0]).toMatchObject({ kind: "tool_group", id: "t1" });
  });

  it("folds thinking-only rounds into the process block too", () => {
    // Reasoning is process, not answer: even without tools it belongs to the
    // collapsed block so the reply is the only visible result.
    const msgs: UIMessage[] = [
      msg({ id: "u", kind: "user", text: "hi" }),
      msg({ id: "t1", kind: "thinking", text: "想了想" }),
      msg({ id: "a", kind: "assistant", text: "hello" }),
    ];
    expect(groupToolMessages(msgs).map((i) => (i.kind === "tool_group" ? `group:${i.id}` : i.id))).toEqual([
      "u", "group:t1", "a",
    ]);
  });

  it("splits thinking runs at an author change", () => {
    const msgs: UIMessage[] = [
      msg({ id: "t1", kind: "thinking", text: "main" }),
      msg({ id: "c1", kind: "tool_call", toolName: "read" }),
      msg({ id: "t2", kind: "thinking", text: "sub", author: "sub-agent" }),
      msg({ id: "c2", kind: "tool_call", toolName: "read", author: "sub-agent" }),
    ];
    expect(groupToolMessages(msgs).map((i) => (i.kind === "tool_group" ? `group:${i.id}` : i.id))).toEqual([
      "group:t1", "group:t2",
    ]);
  });
});

describe("buildToolTrace", () => {
  it("pairs one call and result into one logical tool step", () => {
    const trace = buildToolTrace([
      msg({ id: "c1", kind: "tool_call", toolName: "read", toolArgs: { path: "a.ts" } }),
      msg({ id: "r1", kind: "tool_result", toolName: "read", toolResp: { ok: true } }),
    ]);
    expect(trace).toHaveLength(1);
    expect(trace[0]).toMatchObject({
      kind: "tool",
      id: "c1",
      name: "read",
      args: { path: "a.ts" },
      resp: { ok: true },
    });
    const step = trace[0];
    if (step.kind !== "tool") throw new Error("expected a tool step");
    expect(step.call?.id).toBe("c1");
    expect(step.result?.id).toBe("r1");
  });

  it("keeps thinking/narration order around merged tool steps", () => {
    const trace = buildToolTrace([
      msg({ id: "t1", kind: "thinking", text: "想" }),
      msg({ id: "c1", kind: "tool_call", toolName: "read" }),
      msg({ id: "r1", kind: "tool_result", toolName: "read", toolResp: { ok: true } }),
      msg({ id: "mid", kind: "assistant", text: "继续" }),
      msg({ id: "c2", kind: "tool_call", toolName: "write" }),
    ]);
    expect(trace.map((item) => item.kind)).toEqual(["message", "tool", "message", "tool"]);
    expect(trace.map((item) => (item.kind === "tool" ? item.id : item.message.id))).toEqual([
      "t1", "c1", "mid", "c2",
    ]);
  });

  it("pairs parallel same-name results LIFO and keeps an orphan result", () => {
    const trace = buildToolTrace([
      msg({ id: "c1", kind: "tool_call", toolName: "read" }),
      msg({ id: "c2", kind: "tool_call", toolName: "read" }),
      msg({ id: "r2", kind: "tool_result", toolName: "read", toolResp: { n: 2 } }),
      msg({ id: "r1", kind: "tool_result", toolName: "read", toolResp: { n: 1 } }),
      msg({ id: "orphan", kind: "tool_result", toolName: "grep", toolResp: { ok: true } }),
    ]);
    expect(trace.map((item) => (item.kind === "tool" ? `${item.id}:${item.resp?.n ?? "orphan"}` : item.message.id))).toEqual([
      "c1:1", "c2:2", "orphan:orphan",
    ]);
  });
});

describe("toolStepDisplay", () => {
  it("turns built-in tools into a short action and target", () => {
    expect(toolStepDisplay("run_command", { command: "go test ./internal/..." })).toMatchObject({
      kind: "command",
      action: "运行",
      target: "go test ./internal/...",
      mono: true,
    });
    expect(toolStepDisplay("read_workspace_file", { path: "internal/agentkit/shell.go" })).toMatchObject({
      kind: "read",
      action: "读取",
      target: "internal/agentkit/shell.go",
    });
    expect(toolStepDisplay("write_workspace_file", { path: "notes/weekly.md" })).toMatchObject({
      kind: "write",
      action: "写入",
      target: "notes/weekly.md",
    });
    expect(toolStepDisplay("list_workspace_files", {})).toMatchObject({
      kind: "read",
      action: "列出",
      target: "工作区",
    });
    expect(toolStepDisplay("search_knowledge", { query: "工具调用显示" })).toMatchObject({
      kind: "search",
      action: "搜索知识库",
      target: "工具调用显示",
    });
    expect(toolStepDisplay("get_current_time", {})).toMatchObject({
      kind: "other",
      action: "获取当前时间",
    });
  });

  it("falls back to the tool name and first useful argument for MCP tools", () => {
    expect(toolStepDisplay("github_search", { query: "repo:openai/codex" })).toMatchObject({
      kind: "other",
      action: "github_search",
      target: "repo:openai/codex",
    });
    expect(toolStepDisplay("custom_tool", { url: "https://example.com" })).toMatchObject({
      action: "custom_tool",
      target: "https://example.com",
    });
    expect(toolStepDisplay("custom_tool", {})).toMatchObject({
      action: "custom_tool",
    });
  });
});

describe("summarizeToolTrace", () => {
  it("counts logical steps, categories and failed results", () => {
    const trace = buildToolTrace([
      msg({ id: "c1", kind: "tool_call", toolName: "run_command", toolArgs: { command: "git status" } }),
      msg({ id: "r1", kind: "tool_result", toolName: "run_command", toolResp: { ok: true } }),
      msg({ id: "c2", kind: "tool_call", toolName: "read_workspace_file", toolArgs: { path: "a.go" } }),
      msg({ id: "r2", kind: "tool_result", toolName: "read_workspace_file", toolResp: { error: "not found" } }),
      msg({ id: "c3", kind: "tool_call", toolName: "search_knowledge", toolArgs: { query: "display" } }),
      msg({ id: "r3", kind: "tool_result", toolName: "search_knowledge", toolResp: { ok: true } }),
    ]);
    const summary = summarizeToolTrace(trace);
    expect(summary).toEqual({
      total: 3,
      failed: 1,
      counts: { command: 1, read: 1, write: 0, search: 1, other: 0 },
    });
    expect(formatToolExecutionBreakdown(summary.counts, 3)).toBe("1 命令 · 1 读取 · 1 搜索");
  });

  it("keeps the header breakdown to the largest categories", () => {
    expect(formatToolExecutionBreakdown({ command: 0, read: 0, write: 0, search: 0, other: 0 })).toBe("");
    expect(formatToolExecutionBreakdown({ command: 9, read: 0, write: 0, search: 0, other: 0 })).toBe("9 命令");
    expect(formatToolExecutionBreakdown({ command: 4, read: 2, write: 1, search: 1, other: 1 })).toBe("4 命令 · 2 读取");
  });
});

describe("toolGroupStatus", () => {
  const call = msg({ id: "c1", kind: "tool_call", toolName: "write" });
  const result = (resp: { [k: string]: any }) =>
    msg({ id: "r1", kind: "tool_result", toolName: "write", toolResp: resp });
  // group wraps process messages in the turn-outcome metadata groupTurn
  // attaches, so the tests feed exactly what the component feeds.
  const group = (messages: UIMessage[], over: Partial<ToolGroup> = {}): ToolGroup => ({
    kind: "tool_group",
    id: messages[0]?.id ?? "g",
    author: "bot",
    messages,
    answered: true,
    errored: false,
    ...over,
  });

  it("reports a pending HITL confirmation before anything else", () => {
    const confirm = msg({ id: "cf", kind: "confirm", toolName: "write", confirmStatus: "pending" });
    expect(toolGroupStatus(group([call, confirm], { answered: false }), new Set(["c1"]), true)).toBe("confirm");
  });

  it("ignores confirmations that were already answered", () => {
    const approved = msg({ id: "cf", kind: "confirm", toolName: "write", confirmStatus: "approved" });
    expect(toolGroupStatus(group([call, approved, result({ ok: true })]), new Set(), false)).toBe("done");
  });

  it("reports running calls, but a failed step no longer fails the run", () => {
    expect(toolGroupStatus(group([call]), new Set(["c1"]), true)).toBe("running");
    // A step that failed inside a turn that still answered is process detail:
    // the per-card badge keeps the red X, the block header reports the run.
    expect(toolGroupStatus(group([call, result({ error: "boom" })]), new Set(), false)).toBe("done");
    expect(toolGroupStatus(group([call, result({ isError: true })]), new Set(), false)).toBe("done");
    expect(toolGroupStatus(group([call, result({ is_error: true })]), new Set(), false)).toBe("done");
    expect(toolGroupStatus(group([call, result({ ok: true })]), new Set(), false)).toBe("done");
  });

  it("fails the run when the turn errored without a reply", () => {
    const failed = group([call, result({ ok: true })], { answered: false, errored: true });
    expect(toolGroupStatus(failed, new Set(), true)).toBe("failed");
    expect(toolGroupStatus(failed, new Set(), false)).toBe("failed");
  });

  it("fails a settled turn without a reply; keeps a live gap running", () => {
    const missing = group([call, result({ ok: true })], { answered: false });
    expect(toolGroupStatus(missing, new Set(), false)).toBe("failed");
    // Between two tool batches nothing streams and no tool is running, but
    // the turn is still live: the block must stay open instead of flapping
    // done→running on every batch gap.
    expect(toolGroupStatus(missing, new Set(), true)).toBe("running");
  });

  it("keeps the run successful when a step failed but the turn answered", () => {
    // The reported render: one search step timed out, the model retried with
    // another source and wrote the full answer — the header still said 失败.
    const msgs: UIMessage[] = [
      msg({ id: "u", kind: "user", text: "查一下西安明天的天气" }),
      msg({ id: "c1", kind: "tool_call", toolName: "web_search" }),
      msg({ id: "r1", kind: "tool_result", toolName: "web_search", toolResp: { error: "timeout" } }),
      msg({ id: "c2", kind: "tool_call", toolName: "web_search" }),
      msg({ id: "r2", kind: "tool_result", toolName: "web_search", toolResp: { results: [] } }),
      msg({ id: "a", kind: "assistant", text: "西安明天小雨转阴，8~12℃。" }),
    ];
    const group = groupToolMessages(msgs).find((i) => i.kind === "tool_group");
    if (!group || group.kind !== "tool_group") throw new Error("expected a tool group");
    expect(group.answered).toBe(true);
    expect(group.errored).toBe(false);
    expect(toolGroupStatus(group, new Set(), false)).toBe("done");
  });

  it("records a failed turn: no reply plus an error bubble", () => {
    const msgs: UIMessage[] = [
      msg({ id: "u", kind: "user", text: "hi" }),
      msg({ id: "c1", kind: "tool_call", toolName: "read" }),
      msg({ id: "r1", kind: "tool_result", toolName: "read", toolResp: { ok: true } }),
      msg({ id: "c2", kind: "tool_call", toolName: "read" }),
      msg({ id: "r2", kind: "tool_result", toolName: "read", toolResp: { ok: true } }),
      msg({ id: "e", kind: "error", text: "运行出错：模型超时" }),
    ];
    const group = groupToolMessages(msgs).find((i) => i.kind === "tool_group");
    if (!group || group.kind !== "tool_group") throw new Error("expected a tool group");
    expect(group.answered).toBe(false);
    expect(group.errored).toBe(true);
    expect(toolGroupStatus(group, new Set(), false)).toBe("failed");
  });

  it("does not fail a turn that answered before a trailing warning", () => {
    // Budget warnings arrive as error events after a complete answer; the
    // answer, not the warning, decides the run's state.
    const msgs: UIMessage[] = [
      msg({ id: "u", kind: "user", text: "hi" }),
      msg({ id: "c1", kind: "tool_call", toolName: "read" }),
      msg({ id: "r1", kind: "tool_result", toolName: "read", toolResp: { ok: true } }),
      msg({ id: "c2", kind: "tool_call", toolName: "read" }),
      msg({ id: "r2", kind: "tool_result", toolName: "read", toolResp: { ok: true } }),
      msg({ id: "a", kind: "assistant", text: "答案" }),
      msg({ id: "e", kind: "error", text: "已达会话 token 预算" }),
    ];
    const group = groupToolMessages(msgs).find((i) => i.kind === "tool_group");
    if (!group || group.kind !== "tool_group") throw new Error("expected a tool group");
    expect(group.answered).toBe(true);
    expect(group.errored).toBe(false);
    expect(toolGroupStatus(group, new Set(), false)).toBe("done");
  });

  it("keeps the block running while a thinking entry still streams", () => {
    const streaming = msg({ id: "t1", kind: "thinking", text: "", streaming: true });
    const settled = msg({ id: "t2", kind: "thinking", text: "done" });
    expect(toolGroupStatus(group([result({ ok: true }), streaming]), new Set(), true)).toBe("running");
    // A finished thinking entry is process detail, not a reason to auto-expand.
    expect(toolGroupStatus(group([result({ ok: true }), settled]), new Set(), false)).toBe("done");
  });

  it("stays running while inter-tool narration still streams", () => {
    const narration = msg({ id: "mid", kind: "assistant", text: "我来读取模板", streaming: true });
    expect(toolGroupStatus(group([result({ ok: true }), narration]), new Set(), true)).toBe("running");
    expect(toolGroupStatus(group([result({ ok: true }), { ...narration, streaming: false }]), new Set(), false)).toBe("done");
    // A finished narration followed by silence is the batch gap: the turn is
    // live, so the block keeps its running state until it settles.
    expect(toolGroupStatus(group([result({ ok: true }), { ...narration, streaming: false }], { answered: false }), new Set(), true)).toBe("running");
  });
});

describe("toolResultFailed", () => {
  it("flags error envelopes without false positives", () => {
    expect(toolResultFailed(null)).toBe(false);
    expect(toolResultFailed({ error: null })).toBe(false);
    expect(toolResultFailed({ error: "" })).toBe(false);
    expect(toolResultFailed({ error: "denied" })).toBe(true);
    expect(toolResultFailed({ isError: true })).toBe(true);
    expect(toolResultFailed({ is_error: true })).toBe(true);
  });
});

describe("toolGroupDurationMs", () => {
  it("sums completed tool runtimes and ignores pending calls", () => {
    const msgs: UIMessage[] = [
      msg({ id: "c1", kind: "tool_call", toolName: "A" }),
      msg({ id: "r1", kind: "tool_result", toolName: "A" }),
      msg({ id: "c2", kind: "tool_call", toolName: "B" }),
      msg({ id: "r2", kind: "tool_result", toolName: "B" }),
    ];
    const durations = new Map([["r1", 1200], ["r2", 800]]);
    expect(toolGroupDurationMs(msgs, durations)).toBe(2000);
    expect(toolGroupDurationMs([msgs[0], msgs[2]], durations)).toBeNull();
  });
});

describe("toolGroupElapsedMs / formatWorkDuration", () => {
  const msgs: UIMessage[] = [
    msg({ id: "t1", kind: "thinking", text: "想", timestamp: "2026-10-30T01:00:00Z" }),
    msg({ id: "r1", kind: "tool_result", toolName: "A", timestamp: "2026-10-30T01:00:20Z" }),
  ];

  it("counts the whole turn to the final reply or to now while live", () => {
    const reply = "2026-10-30T01:02:04Z";
    expect(toolGroupElapsedMs(msgs, reply, 0, false, new Map())).toBe(124_000);
    expect(toolGroupElapsedMs(msgs, undefined, Date.parse("2026-10-30T01:00:30Z"), true, new Map()))
      .toBe(30_000);
    // Settled without a reply: stop at the last event.
    expect(toolGroupElapsedMs(msgs, undefined, 0, false, new Map())).toBe(20_000);
    // No timestamps at all: fall back to the summed tool runtimes.
    const noTs = [msg({ id: "c1", kind: "tool_call", toolName: "A" }), msg({ id: "r1", kind: "tool_result", toolName: "A" })];
    expect(toolGroupElapsedMs(noTs, undefined, 0, false, new Map([["r1", 3200]]))).toBe(3200);
  });

  it("renders the summary in Chinese units", () => {
    expect(formatWorkDuration(19_000)).toBe("19 秒");
    expect(formatWorkDuration(124_000)).toBe("2 分 4 秒");
    expect(formatWorkDuration(3_660_000)).toBe("1 小时 1 分");
    expect(formatWorkDuration(3_600_000)).toBe("1 小时");
  });
});

describe("chatItem timestamps", () => {
  it("spans a group from its first to its last event", () => {
    const items = groupToolMessages([
      msg({ id: "c1", kind: "tool_call", toolName: "A", timestamp: "2026-10-30T01:00:00Z" }),
      msg({ id: "c2", kind: "tool_call", toolName: "B", timestamp: "2026-10-30T01:00:05Z" }),
      msg({ id: "r1", kind: "tool_result", toolName: "A", timestamp: "2026-10-30T01:00:09Z" }),
    ]);
    expect(chatItemStartTs(items[0])).toBe("2026-10-30T01:00:00Z");
    expect(chatItemEndTs(items[0])).toBe("2026-10-30T01:00:09Z");
  });
});

describe("computeEditFlags", () => {
  it("marks only the last user message as editable", () => {
    const msgs: UIMessage[] = [
      msg({ id: "u1", kind: "user", text: "one" }),
      msg({ id: "a1", kind: "assistant", text: "two" }),
      msg({ id: "u2", kind: "user", text: "three" }),
    ];
    const flags = computeEditFlags(msgs);
    expect(flags.get("u2")).toBe(true);
    expect(flags.get("u1")).toBeUndefined();
  });
});

describe("fmtTime", () => {
  it("shows only the time for today and adds the date otherwise", () => {
    const now = new Date();
    const today = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 9, 5);
    // Locale data varies across Node ICU builds — anchor on the stable parts.
    expect(fmtTime(today.toISOString())).toContain("09:05");
    const old = new Date(2020, 0, 15, 9, 5);
    expect(fmtTime(old.toISOString())).toContain("01-15");
    expect(fmtTime(old.toISOString())).toContain("09:05");
    expect(fmtTime("garbage")).toBe("");
  });
});

describe("mdToSpeech", () => {
  it("strips markdown structures for speech synthesis", () => {
    const out = mdToSpeech("# 标题\n\n这是 **加粗** 与 `代码`\n\n```js\nconst x = 1;\n```");
    expect(out).not.toContain("#");
    expect(out).not.toContain("**");
    expect(out).not.toContain("const x");
    expect(out).toContain("标题");
    expect(out).toContain("这是 加粗 与 代码");
  });
});

describe("isDayChange / dayLabel", () => {
  it("marks calendar-day boundaries and labels them in Chinese", () => {
    const a = new Date(2026, 9, 29, 23, 0).toISOString();
    const b = new Date(2026, 9, 30, 0, 30).toISOString();
    expect(isDayChange(a, b)).toBe(true);
    expect(isDayChange(b, b)).toBe(false);
    expect(isDayChange(undefined, b)).toBe(true);
    // Full-icu renders 「10月30日 星期四」; small-icu builds degrade the
    // format — only assert a non-empty localized label.
    expect(dayLabel(b).length).toBeGreaterThan(0);
    expect(dayLabel("bad")).toBe("");
  });
});

describe('groupSessionsByTime', () => {
  const NOW = new Date('2026-11-01T12:00:00');
  const sess = (over: Partial<ChatSession>): ChatSession => ({
    id: over.id ?? Math.random().toString(),
    title: over.title ?? '会话',
    targetType: '',
    targetId: '',
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-11-01T08:00:00',
    ...over,
  });

  it('buckets pinned/today/yesterday/week/earlier/archived in fixed order, newest first within groups', () => {
    const groups = groupSessionsByTime(
      [
        sess({ id: 'old', updatedAt: '2026-09-01T10:00:00' }),
        sess({ id: 'edge7', updatedAt: '2026-10-25T12:00:00' }),
        sess({ id: 'arch', updatedAt: '2026-11-01T09:00:00', archived: true }),
        sess({ id: 'yest', updatedAt: '2026-10-31T22:00:00' }),
        sess({ id: 'week', updatedAt: '2026-10-30T08:00:00' }),
        sess({ id: 'pin', updatedAt: '2026-09-15T10:00:00', pinned: true }),
        sess({ id: 'today2', updatedAt: '2026-11-01T11:00:00' }),
        sess({ id: 'today1', updatedAt: '2026-11-01T07:00:00' }),
      ],
      NOW,
    );
    expect(groups.map((g) => [g.key, g.label, g.sessions.map((s) => s.id)])).toEqual([
      ['pinned', '置顶', ['pin']],
      ['today', '今天', ['today2', 'today1']],
      ['yesterday', '昨天', ['yest']],
      ['week', '7 天内', ['week']],
      ['earlier', '更早', ['edge7', 'old']],
      ['archived', '已归档', ['arch']],
    ]);
  });

  it('omits empty groups and falls back to earlier for missing or invalid timestamps', () => {
    const groups = groupSessionsByTime(
      [
        sess({ id: 'a', updatedAt: '' }),
        sess({ id: 'b', updatedAt: 'not-a-date' }),
        sess({ id: 'c', updatedAt: '2026-06-01T00:00:00' }),
      ],
      NOW,
    );
    expect(groups.map((g) => g.key)).toEqual(['earlier']);
  });
});

describe('groupSessionsByProject', () => {
  const proj = (over: Partial<Project>): Project => ({
    id: over.id ?? 'proj_x',
    name: over.name ?? 'alpha',
    rootDir: over.rootDir ?? 'D:/proj/alpha',
    createdAt: '2026-01-01T00:00:00Z',
    ...over,
  });
  const sess = (over: Partial<ChatSession>): ChatSession => ({
    id: over.id ?? Math.random().toString(),
    title: over.title ?? '会话',
    targetType: '',
    targetId: '',
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-11-01T08:00:00',
    ...over,
  });
  const WS = 'C:/ws';

  it('groups registered projects by projectId and root match; auto task dirs stay in the default project', () => {
    const alpha = proj({ id: 'proj_a', name: 'alpha', rootDir: 'D:/proj/alpha' });
    const groups = groupSessionsByProject(
      [
        sess({ id: 'bound', projectId: 'proj_a', updatedAt: '2026-11-01T10:00:00' }),
        sess({ id: 'legacy-root', workspaceDir: 'd:/PROJ/alpha/', updatedAt: '2026-11-01T09:00:00' }),
        sess({ id: 'adhoc', workspaceDir: 'E:/standalone', updatedAt: '2026-11-01T08:00:00' }),
        sess({ id: 'auto', workspaceDir: 'C:/ws/tasks/sess1', updatedAt: '2026-11-01T07:00:00' }),
        sess({ id: 'legacy' }),
        sess({ id: 'gone', archived: true }),
      ],
      [alpha, proj({ id: 'proj_b', name: 'beta', rootDir: 'D:/proj/beta' })],
      WS,
    );
    expect(groups.map((g) => [g.key, g.label, g.sessions.map((s) => s.id)])).toEqual([
      ['proj:proj_a', 'alpha', ['bound', 'legacy-root']],
      ['E:/standalone', 'standalone', ['adhoc']],
      ['default', '默认项目', ['legacy', 'auto']],
      ['archived', '已归档', ['gone']],
    ]);
    expect(groups[0].path).toBe('D:/proj/alpha');
    expect(groups[0].projectId).toBe('proj_a');
  });

  it('keeps ad-hoc dir dedupe and newest-first group order for non-project sessions', () => {
    const groups = groupSessionsByProject(
      [
        sess({ id: 'a1', workspaceDir: 'D:/a', updatedAt: '2026-10-01T00:00:00' }),
        sess({ id: 'b1', workspaceDir: 'D:/b', updatedAt: '2026-10-31T00:00:00' }),
        sess({ id: 'caseback', workspaceDir: 'c:\\other\\tasks\\x', updatedAt: '2026-11-01T07:00:00' }),
      ],
      [],
      WS,
    );
    expect(groups.map((g) => [g.key, g.label, g.sessions.map((s) => s.id)])).toEqual([
      ['c:\\other\\tasks\\x', 'x', ['caseback']],
      ['D:/b', 'b', ['b1']],
      ['D:/a', 'a', ['a1']],
    ]);
  });
  it('sorts project groups by newest member and archived trails', () => {
    const groups = groupSessionsByProject(
      [
        sess({ id: 'p1', projectId: 'proj_a', updatedAt: '2026-10-01T00:00:00' }),
        sess({ id: 'p2', projectId: 'proj_b', updatedAt: '2026-10-31T00:00:00' }),
        sess({ id: 'gone', archived: true }),
      ],
      [proj({ id: 'proj_a', name: 'a' }), proj({ id: 'proj_b', name: 'b' })],
      WS,
    );
    expect(groups.map((g) => g.label)).toEqual(['b', 'a', '已归档']);
  });
});

describe('buildSidebarProjectGroups', () => {
  const proj = (over: Partial<Project>): Project => ({
    id: over.id ?? 'proj_x',
    name: over.name ?? 'alpha',
    rootDir: over.rootDir ?? 'D:/proj/alpha',
    createdAt: '2026-01-01T00:00:00Z',
    ...over,
  });
  const sess = (over: Partial<ChatSession>): ChatSession => ({
    id: over.id ?? Math.random().toString(),
    title: over.title ?? '会话',
    targetType: '',
    targetId: '',
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-11-01T08:00:00',
    ...over,
  });
  const WS = 'C:/ws';

  it('orders the compact tree as default, project order, ad-hoc dirs; archived is excluded', () => {
    const groups = buildSidebarProjectGroups(
      [
        sess({ id: 'direct', updatedAt: '2026-11-01T10:00:00' }),
        sess({ id: 'beta', projectId: 'proj_b', updatedAt: '2026-11-01T09:00:00' }),
        sess({ id: 'adhoc', workspaceDir: 'E:/standalone', updatedAt: '2026-11-01T08:00:00' }),
        sess({ id: 'gone', archived: true }),
      ],
      [
        proj({ id: 'proj_empty', name: '空项目', rootDir: 'D:/proj/empty' }),
        proj({ id: 'proj_b', name: 'agentmate', rootDir: 'D:/proj/beta' }),
      ],
      WS,
    );

    expect(groups.map((g) => [g.key, g.label, g.sessions.map((s) => s.id)])).toEqual([
      ['default', '对话', ['direct']],
      ['proj:proj_empty', '空项目', []],
      ['proj:proj_b', 'agentmate', ['beta']],
      ['E:/standalone', 'standalone', ['adhoc']],
    ]);
  });

  it('keeps pinned chats first inside each project and then sorts by recency', () => {
    const groups = buildSidebarProjectGroups(
      [
        sess({ id: 'old-pinned', projectId: 'proj_a', pinned: true, updatedAt: '2026-10-01T00:00:00' }),
        sess({ id: 'newest', projectId: 'proj_a', updatedAt: '2026-11-01T10:00:00' }),
        sess({ id: 'middle', projectId: 'proj_a', updatedAt: '2026-10-15T00:00:00' }),
      ],
      [proj({ id: 'proj_a', name: 'alpha', rootDir: 'D:/proj/alpha' })],
      WS,
    );

    expect(groups[0].sessions.map((s) => s.id)).toEqual(['old-pinned', 'newest', 'middle']);
  });
});

describe('projectSessionWindow', () => {
  const sess = (id: string): ChatSession => ({
    id,
    title: id,
    targetType: '',
    targetId: '',
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-11-01T08:00:00',
  });

  it('shows five recent chats and reports the hidden tail', () => {
    const sessions = ['a', 'b', 'c', 'd', 'e', 'f', 'g'].map(sess);
    const window = projectSessionWindow(sessions, false);
    expect(window.visible.map((s) => s.id)).toEqual(['a', 'b', 'c', 'd', 'e']);
    expect(window.hiddenCount).toBe(2);
  });

  it('shows every chat once expanded and handles a zero limit', () => {
    const sessions = ['a', 'b', 'c', 'd', 'e', 'f'].map(sess);
    expect(projectSessionWindow(sessions, true).visible).toHaveLength(6);
    expect(projectSessionWindow(sessions, true).hiddenCount).toBe(0);
    expect(projectSessionWindow(sessions, false, 0).visible).toHaveLength(0);
    expect(projectSessionWindow(sessions, false, 0).hiddenCount).toBe(6);
  });
});

describe('shouldExpandProjectForSession', () => {
  const sessions = ['a', 'b', 'c', 'd', 'e', 'f', 'g'].map((id): ChatSession => ({
    id,
    title: id,
    targetType: '',
    targetId: '',
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-11-01T08:00:00',
  }));

  it('expands only when the active chat is beyond the preview', () => {
    expect(shouldExpandProjectForSession(sessions, 'e')).toBe(false);
    expect(shouldExpandProjectForSession(sessions, 'f')).toBe(true);
    expect(shouldExpandProjectForSession(sessions, 'missing')).toBe(false);
    expect(shouldExpandProjectForSession(sessions, '')).toBe(false);
  });
});

describe('sessionTargetBadgeLabel', () => {
  it('hides the badge for direct chats', () => {
    expect(sessionTargetBadgeLabel('')).toBeNull();
    expect(sessionTargetBadgeLabel(undefined)).toBeNull();
  });

  it('labels bound agent, team and remote targets', () => {
    expect(sessionTargetBadgeLabel('agent')).toBe('Agent');
    expect(sessionTargetBadgeLabel('team')).toBe('团队');
    expect(sessionTargetBadgeLabel('remote')).toBe('远程');
  });
});

describe('effectiveReasoningLevel', () => {
  const levels = [
    { level: 'off', value: '{}' },
    { level: 'low', value: '{}' },
    { level: 'high', value: '{}' },
  ];

  it('prefers the per-turn pick, then the session default, then the model default', () => {
    expect(effectiveReasoningLevel(levels, 'low', 'high', 'off')).toBe('low');
    expect(effectiveReasoningLevel(levels, '', 'high', 'off')).toBe('high');
    expect(effectiveReasoningLevel(levels, '', '', 'off')).toBe('off');
  });

  it('ignores names the table does not offer and falls back to its first level', () => {
    expect(effectiveReasoningLevel(levels, 'turbo', '', '')).toBe('off');
    expect(effectiveReasoningLevel([], 'high', 'low', 'off')).toBe('');
  });
});
