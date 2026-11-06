import { describe, expect, it } from "vitest";
import {
  chatItemEndTs, chatItemStartTs, computeEditFlags, computeRunningTools, computeToolDurations,
  dayLabel, effectiveReasoningLevel, fmtTime, formatWorkDuration, groupSessionsByProject,
  groupSessionsByTime, groupToolMessages, isDayChange, mdToSpeech, sessionTargetBadgeLabel,
  toolGroupDurationMs, toolGroupElapsedMs, toolGroupInvocationCount, toolGroupStatus,
  toolResultFailed, type UIMessage,
} from "./chatUtils";
import type { ChatSession, Project } from "@/lib/api";

const msg = (over: Partial<UIMessage>): UIMessage => ({
  id: over.id ?? Math.random().toString(),
  kind: "assistant",
  author: "bot",
  text: "",
  ...over,
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

  it("keeps a single invocation's call/result cards standalone", () => {
    const msgs: UIMessage[] = [
      msg({ id: "c1", kind: "tool_call", toolName: "read" }),
      msg({ id: "r1", kind: "tool_result", toolName: "read" }),
    ];
    expect(groupToolMessages(msgs).map((i) => i.kind)).toEqual(["message", "message"]);
  });

  it("keeps narration inside the turn block and splits at plan_todos cards and author changes", () => {
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
    // Narration between tool batches no longer breaks the run; plan_todos
    // still renders pinned and an author change still starts a new block.
    expect(items.map((i) => (i.kind === "tool_group" ? `group:${i.id}` : i.id))).toEqual([
      "group:c1", "todos", "group:c4",
    ]);
    const first = items[0];
    if (first.kind !== "tool_group") throw new Error("expected a tool group");
    expect(first.messages.map((m) => m.id)).toEqual(["c1", "r1", "mid", "c2", "r2", "c3", "r3"]);
    const third = items[2];
    if (third.kind !== "tool_group") throw new Error("expected a tool group");
    expect(third.author).toBe("sub-agent");
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

describe("toolGroupStatus", () => {
  const call = msg({ id: "c1", kind: "tool_call", toolName: "write" });
  const result = (resp: { [k: string]: any }) =>
    msg({ id: "r1", kind: "tool_result", toolName: "write", toolResp: resp });

  it("reports a pending HITL confirmation before anything else", () => {
    const confirm = msg({ id: "cf", kind: "confirm", toolName: "write", confirmStatus: "pending" });
    expect(toolGroupStatus([call, confirm], new Set(["c1"]))).toBe("confirm");
  });

  it("ignores confirmations that were already answered", () => {
    const approved = msg({ id: "cf", kind: "confirm", toolName: "write", confirmStatus: "approved" });
    expect(toolGroupStatus([call, approved, result({ ok: true })], new Set())).toBe("done");
  });

  it("reports running calls and failed results", () => {
    expect(toolGroupStatus([call], new Set(["c1"]))).toBe("running");
    expect(toolGroupStatus([call, result({ error: "boom" })], new Set())).toBe("failed");
    expect(toolGroupStatus([call, result({ isError: true })], new Set())).toBe("failed");
    expect(toolGroupStatus([call, result({ is_error: true })], new Set())).toBe("failed");
    expect(toolGroupStatus([call, result({ ok: true })], new Set())).toBe("done");
  });

  it("keeps the block running while a thinking entry still streams", () => {
    const streaming = msg({ id: "t1", kind: "thinking", text: "", streaming: true });
    const settled = msg({ id: "t2", kind: "thinking", text: "done" });
    expect(toolGroupStatus([result({ ok: true }), streaming], new Set())).toBe("running");
    // A finished thinking entry is process detail, not a reason to auto-expand.
    expect(toolGroupStatus([result({ ok: true }), settled], new Set())).toBe("done");
  });

  it("stays running while inter-tool narration still streams", () => {
    const narration = msg({ id: "mid", kind: "assistant", text: "我来读取模板", streaming: true });
    expect(toolGroupStatus([result({ ok: true }), narration], new Set())).toBe("running");
    expect(toolGroupStatus([result({ ok: true }), { ...narration, streaming: false }], new Set())).toBe("done");
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
