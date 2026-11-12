// Pure helpers shared by the chat page and its unit tests (vitest).
import type { AttachmentOut, ChatSession, Project, ReasoningLevel, UsageInfo } from "@/lib/api";

export type UIMessage = {
  id: string;
  kind: "user" | "assistant" | "thinking" | "tool_call" | "tool_result" | "confirm" | "error";
  author: string;
  text: string;
  toolName?: string;
  toolArgs?: { [k: string]: any } | null;
  toolResp?: { [k: string]: any } | null;
  callId?: string;
  hint?: string;
  confirmStatus?: "pending" | "approved" | "rejected";
  timestamp?: string;
  durationMs?: number;
  streaming?: boolean;
  usage?: UsageInfo | null;
  attachments?: AttachmentOut[] | null;
  /** reasoning is the thinking level this turn was sent with. */
  reasoning?: string;
};

/**
 * effectiveReasoningLevel picks the level a turn will use: explicit choices in
 * order (per-turn pick, then session default), then the model default. Names
 * the model's table does not offer are ignored so a stale choice from another
 * model cannot leak into a request. Returns "" when the model has no table.
 */
export function effectiveReasoningLevel(
  levels: ReasoningLevel[],
  ...choices: (string | null | undefined)[]
): string {
  for (const choice of choices) {
    if (choice && levels.some((l) => l.level === choice)) return choice;
  }
  return levels[0]?.level ?? "";
}

/**
 * sessionTargetBadgeLabel returns the header badge text for a session target.
 * Direct chats (empty target) have no badge: showing "直聊" made it look like a
 * selectable mode next to Agent/team targets. Returns null when no badge
 * should render.
 */
export function sessionTargetBadgeLabel(targetType?: string): string | null {
  switch (targetType) {
    case "agent":
      return "Agent";
    case "team":
      return "团队";
    case "remote":
      return "远程";
    default:
      return null;
  }
}

/** mdToSpeech converts markdown to a speakable plain string. */
export function mdToSpeech(md: string): string {
  return md
    .replace(/```[\s\S]*?```/g, "（代码块已省略）")
    .replace(/`([^`]+)`/g, "$1")
    .replace(/!\[[^\]]*\]\([^)]*\)/g, "")
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/^#{1,6}\s+/gm, "")
    .replace(/[*_~>|]/g, "")
    .replace(/\s+\n/g, "\n")
    .trim();
}

/** fmtTime renders an ISO timestamp for hover display: HH:MM for today,
 * MM-DD HH:MM otherwise (long-lived sessions span days). */
export function fmtTime(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  const time = d.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
  const today = new Date();
  if (d.getFullYear() === today.getFullYear() && d.getMonth() === today.getMonth() && d.getDate() === today.getDate()) {
    return time;
  }
  const day = `${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  return `${day} ${time}`;
}

/**
 * computeToolDurations pairs tool_result with its call to derive durations.
 * A HITL confirmation parks the run between the call and the result, so the
 * confirmed call's clock restarts at the confirmation card: otherwise the
 * displayed time is how long the human took to click, not how long the tool ran.
 */
export function computeToolDurations(messages: UIMessage[]): Map<string, number> {
  const map = new Map<string, number>();
  const pending: { id: string; toolName: string; ts: number }[] = [];
  for (const m of messages) {
    const ts = m.timestamp ? Date.parse(m.timestamp) : Date.now();
    if (m.kind === "confirm") {
      // The paused tool is the most recent call of the same name.
      if (m.timestamp) {
        const idx = [...pending].reverse().findIndex((p) => p.toolName === m.toolName);
        if (idx >= 0) pending[pending.length - 1 - idx].ts = ts;
      }
      continue;
    }
    if (m.kind !== "tool_call" && m.kind !== "tool_result") continue;
    if (m.kind === "tool_call") {
      pending.push({ id: m.id, toolName: m.toolName ?? "", ts });
    } else {
      const idx = [...pending].reverse().findIndex((p) => p.toolName === m.toolName);
      if (idx >= 0) {
        const call = pending.splice(pending.length - 1 - idx, 1)[0];
        map.set(m.id, Math.max(0, ts - call.ts));
      }
    }
  }
  return map;
}

/** computeRunningTools marks tool_call messages that have no matching result yet.
 * Pairing is by tool name (same as computeToolDurations): parallel/团队 tool
 * rounds emit call A, call B, result B, result A — a FIFO queue would end A's
 * spinner early and leave B spinning forever. */
export function computeRunningTools(messages: UIMessage[]): Set<string> {
  const running = new Set<string>();
  const pending: { id: string; toolName: string }[] = [];
  for (const m of messages) {
    if (m.kind === "tool_call") {
      running.add(m.id);
      pending.push({ id: m.id, toolName: m.toolName ?? "" });
    } else if (m.kind === "tool_result") {
      const idx = [...pending].reverse().findIndex((p) => p.toolName === (m.toolName ?? ""));
      if (idx >= 0) {
        const call = pending.splice(pending.length - 1 - idx, 1)[0];
        running.delete(call.id);
      }
    }
  }
  return running;
}

/** toolResultFailed reports whether a tool result payload signals an error.
 * Envelope-level fields only: matching substrings used to flag successful
 * results that merely carried an `"error": null` field, and missed MCP's
 * camelCase `isError`. */
export function toolResultFailed(resp?: { [k: string]: any } | null): boolean {
  if (!resp) return false;
  if (resp.error !== undefined && resp.error !== null && resp.error !== "") return true;
  return resp.isError === true || resp.is_error === true;
}

/** ToolGroup is one collapsible "execution" block for a whole turn: the
 *  process events (thinking entries, tool calls, results, HITL confirmations)
 *  plus the assistant narration the model emits between tool batches, all
 *  from the same author. The turn's final reply stays outside as the result. */
export type ToolGroup = {
  kind: "tool_group";
  /** id is the first message's id, stable while the run keeps streaming. */
  id: string;
  author: string;
  messages: UIMessage[];
  /** endTs is the final reply's timestamp: the summary counts the whole turn,
   *  including the time spent writing the answer, not just the tool runtime. */
  endTs?: string;
  /** answered records whether the turn produced its final reply. Until it
   *  exists a live turn is still generating; a settled turn without one never
   *  completed. */
  answered: boolean;
  /** errored records a turn that ended in an error bubble instead of a reply
   *  (live streams only — history does not persist error events). */
  errored: boolean;
};

/** ChatItem is one entry in the message list: a standalone message or a
 *  grouped execution block. */
export type ChatItem =
  | { kind: "message"; id: string; message: UIMessage }
  | ToolGroup;

/** TurnItem wraps one conversation turn: the user message as its quote
 *  header plus every entry the run produced until the next user message.
 *  The turn rail and the turn card both key off the header's id. */
export type TurnItem = {
  kind: "turn";
  id: string;
  header: UIMessage;
  body: ChatItem[];
};

/** RenderItem is one top-level entry in the message pane: a turn card, or an
 *  orphan entry that precedes the first user message. */
export type RenderItem = ChatItem | TurnItem;

/** groupTurns folds the flat item list into turn cards keyed by user
 *  messages. Orphan entries before the first user message (error bubbles a
 *  failed send can leave behind) render standalone so they never vanish. */
export function groupTurns(items: ChatItem[]): RenderItem[] {
  const out: RenderItem[] = [];
  let header: UIMessage | null = null;
  let body: ChatItem[] = [];
  const flush = () => {
    if (header) out.push({ kind: "turn", id: header.id, header, body });
    else out.push(...body);
    header = null;
    body = [];
  };
  for (const item of items) {
    if (item.kind === "message" && item.message.kind === "user") {
      // User events that carried text plus images used to be rendered as
      // several messages with the same id. Merge those parts instead of
      // creating phantom turns: duplicate ids also collapse the turn rail's
      // hover/active matching and React keys.
      if (header && item.message.id === header.id) {
        const current = header;
        const text = [current.text, item.message.text].filter(Boolean).join("\n");
        header = {
          ...current,
          text,
          attachments: [...(current.attachments ?? []), ...(item.message.attachments ?? [])],
          reasoning: current.reasoning || item.message.reasoning,
          timestamp: current.timestamp || item.message.timestamp,
        };
        continue;
      }
      flush();
      header = item.message;
      continue;
    }
    body.push(item);
  }
  flush();
  return out;
}

/**
 * isAwaitingReply reports the silent gap after the user's message and before
 * the model's first streamed event. The reply bubble only exists once a delta
 * arrives, so this is the only signal that the turn is still starting.
 */
export function isAwaitingReply(messages: UIMessage[], running: boolean): boolean {
  if (!running) return false;
  const tail = messages[messages.length - 1];
  return !tail || tail.kind === "user";
}

/** ToolStep pairs one tool_call with its matching tool_result. Keeping the
 *  pair together is what makes an execution block read as N tools instead of
 *  2N call/result rows. */
export type ToolStep = {
  kind: "tool";
  id: string;
  name: string;
  args?: UIMessage["toolArgs"];
  resp?: UIMessage["toolResp"];
  call?: UIMessage;
  result?: UIMessage;
};

/** ToolTraceItem preserves the turn's event order while folding calls and
 *  results into logical steps. */
export type ToolTraceItem =
  | { kind: "message"; message: UIMessage }
  | ToolStep;

/** buildToolTrace pairs a call with the most recent unpaired result of the
 *  same tool name (the same LIFO rule the duration/timer helpers use). A
 *  result without a retained call still renders as its own step. */
export function buildToolTrace(messages: UIMessage[]): ToolTraceItem[] {
  const out: ToolTraceItem[] = [];
  const pending: ToolStep[] = [];
  for (const m of messages) {
    if (m.kind === "tool_call") {
      const step: ToolStep = {
        kind: "tool",
        id: m.id,
        name: m.toolName ?? "tool",
        args: m.toolArgs,
        call: m,
      };
      out.push(step);
      pending.push(step);
      continue;
    }
    if (m.kind === "tool_result") {
      const name = m.toolName ?? "tool";
      let pendingIdx = -1;
      for (let i = pending.length - 1; i >= 0; i--) {
        if (pending[i].name === name && pending[i].resp == null) {
          pendingIdx = i;
          break;
        }
      }
      if (pendingIdx >= 0) {
        const step = pending.splice(pendingIdx, 1)[0];
        step.resp = m.toolResp;
        step.result = m;
      } else {
        out.push({ kind: "tool", id: m.id, name, resp: m.toolResp, result: m });
      }
      continue;
    }
    out.push({ kind: "message", message: m });
  }
  return out;
}

export type ToolStepKind = "command" | "read" | "write" | "search" | "other";

export type ToolStepDisplay = {
  kind: ToolStepKind;
  /** Short verb shown before the target, e.g. 运行 / 读取 / 搜索知识库. */
  action: string;
  /** Command, path or query extracted from the call arguments. */
  target?: string;
  /** Commands read best in monospace; labels such as 读取 do not. */
  mono?: boolean;
};

const toolArgString = (value: unknown): string =>
  typeof value === "string" ? value.trim() : "";

/**
 * toolStepDisplay turns a tool invocation into one scannable line. Keep this
 * in the pure helper layer: the execution list and its tests must agree on
 * how unknown/MCP tools fall back to their first useful argument.
 */
export function toolStepDisplay(
  name: string,
  args?: { [k: string]: any } | null,
): ToolStepDisplay {
  const a = args ?? {};
  switch (name) {
    case "run_command": {
      const command = toolArgString(a.command) || toolArgString(a.cmd);
      return { kind: "command", action: "运行", target: command || "命令", mono: true };
    }
    case "read_workspace_file":
      return { kind: "read", action: "读取", target: toolArgString(a.path) || "文件" };
    case "write_workspace_file":
      return { kind: "write", action: "写入", target: toolArgString(a.path) || "文件" };
    case "edit_workspace_file":
      return { kind: "write", action: "修改", target: toolArgString(a.path) || "文件" };
    case "list_workspace_files":
      return { kind: "read", action: "列出", target: toolArgString(a.path) || "工作区" };
    case "search_knowledge":
      return { kind: "search", action: "搜索知识库", target: toolArgString(a.query) || "内容" };
    case "get_current_time":
      return { kind: "other", action: "获取当前时间" };
    default: {
      const target =
        toolArgString(a.query) ||
        toolArgString(a.path) ||
        toolArgString(a.command) ||
        toolArgString(a.url);
      return { kind: "other", action: name, target: target || undefined };
    }
  }
}

export type ToolExecutionSummary = {
  total: number;
  failed: number;
  counts: Record<ToolStepKind, number>;
};

/** summarizeToolTrace counts logical steps and failed results for the group
 *  header. A failed step is process detail, not a turn failure; the header
 *  reports it separately so a successful answer does not hide a red step. */
export function summarizeToolTrace(trace: ToolTraceItem[]): ToolExecutionSummary {
  const counts: Record<ToolStepKind, number> = {
    command: 0,
    read: 0,
    write: 0,
    search: 0,
    other: 0,
  };
  let total = 0;
  let failed = 0;
  for (const item of trace) {
    if (item.kind !== "tool") continue;
    total++;
    counts[toolStepDisplay(item.name, item.args).kind]++;
    if (toolResultFailed(item.resp)) failed++;
  }
  return { total, failed, counts };
}

/** formatToolExecutionBreakdown renders the compact category summary used in
 *  the header tooltip. Only the largest categories are kept: the header must
 *  stay scannable, and the step rows below already expose the rest. */
export function formatToolExecutionBreakdown(
  counts: Record<ToolStepKind, number>,
  maxParts = 2,
): string {
  const ordered: [ToolStepKind, number, string][] = [
    ["command", counts.command, "命令"],
    ["read", counts.read, "读取"],
    ["write", counts.write, "写入"],
    ["search", counts.search, "搜索"],
    ["other", counts.other, "其他"],
  ];
  return ordered
    .filter(([, count]) => count > 0)
    .sort((a, b) => b[1] - a[1])
    .slice(0, Math.max(1, maxParts))
    .map(([, count, label]) => `${count} ${label}`)
    .join(" · ");
}

/** isProcessEvent reports messages that belong to one turn's execution run:
 *  thinking entries plus tool calls/results and HITL confirmations. plan_todos
 *  results are pinned UI (the todo list card), so they stay outside groups. */
function isProcessEvent(m: UIMessage): boolean {
  if (m.kind === "thinking") return true;
  if (m.kind !== "tool_call" && m.kind !== "tool_result" && m.kind !== "confirm") return false;
  return m.toolName !== "plan_todos";
}

/**
 * groupToolMessages turns each turn (everything between two user messages)
 * into at most one collapsible execution block plus its final reply. Process
 * events — thinking, tool calls/results, HITL confirmations — and the
 * assistant narration the model emits between tool batches all belong to the
 * same block: narration used to break the run, which split one turn into
 * several look-alike blocks (thinking card, empty skeleton, tool card, reply).
 * The turn's last assistant message after the last process event is the only
 * result and stays outside the block. Empty settled assistant shells are
 * dropped; plan_todos cards stay standalone without splitting the run (the
 * pinned plan must not shred a long turn into several look-alike groups);
 * errors keep breaking it. A single tool invocation also joins the block so
 * its call/result pair can render as one logical step.
 */
export function groupToolMessages(messages: UIMessage[]): ChatItem[] {
  const out: ChatItem[] = [];
  let start = 0;
  while (start < messages.length) {
    if (messages[start].kind === "user") {
      out.push(messageItem(messages[start]));
      start++;
      continue;
    }
    let end = start + 1;
    while (end < messages.length && messages[end].kind !== "user") end++;
    out.push(...groupTurn(messages.slice(start, end)));
    start = end;
  }
  return out;
}

function messageItem(m: UIMessage): ChatItem {
  return { kind: "message", id: m.id, message: m };
}

/** groupTurn packs one turn's process tail into a single execution block and
 *  keeps its final reply separate. */
function groupTurn(turn: UIMessage[]): ChatItem[] {
  const out: ChatItem[] = [];
  let lastProcessIdx = -1;
  for (let i = turn.length - 1; i >= 0; i--) {
    if (isProcessEvent(turn[i])) { lastProcessIdx = i; break; }
  }

  // The result is the last assistant message after the last process event.
  // Empty settled shells (a streaming placeholder whose turn ended in a tool
  // call) are skipped so they cannot render as an empty assistant block.
  let resultIdx = -1;
  for (let i = turn.length - 1; i > lastProcessIdx; i--) {
    const m = turn[i];
    if (m.kind !== "assistant") continue;
    if (!m.text.trim() && !m.streaming) continue;
    resultIdx = i;
    break;
  }

  if (lastProcessIdx < 0) {
    // No execution events: render plain messages as before.
    for (const m of turn) {
      if (m.kind === "assistant" && !m.text.trim() && !m.streaming) continue;
      out.push(messageItem(m));
    }
    return out;
  }

  const endTs = resultIdx >= 0 ? turn[resultIdx].timestamp : undefined;
  // Turn outcome, not step outcome: a failed tool the model worked around is
  // process detail. Only a turn that ended in an error without ever writing
  // its reply failed — guardrail warnings before a later user message and
  // budget warnings after the reply both still have/leave an answer.
  const answered = resultIdx >= 0;
  const errored = !answered && turn.some((m) => m.kind === "error");
  let run: UIMessage[] = [];
  const flush = () => {
    if (run.length === 0) return;
    const hasProcess = run.some(isProcessEvent);
    // Even one invocation belongs here: its call and result are two events but
    // one logical step, and rendering them as two standalone cards duplicated
    // every simple tool call. Thinking/narration-only runs still group too.
    if (hasProcess) {
      out.push({ kind: "tool_group", id: run[0].id, author: run[0].author, messages: run, endTs, answered, errored });
    } else {
      for (const m of run) out.push(messageItem(m));
    }
    run = [];
  };

  for (let i = 0; i < turn.length; i++) {
    const m = turn[i];
    if (i === resultIdx) {
      flush();
      out.push(messageItem(m));
      continue;
    }
    if (m.kind === "assistant") {
      // Empty streaming placeholders inside a process run carry no text; the
      // block header already shows the turn is live.
      if (!m.text.trim()) continue;
      if (run.length > 0 && run[0].author !== m.author) flush();
      run.push(m);
      continue;
    }
    if (!isProcessEvent(m)) {
      // plan_todos updates render as their own pinned cards but must NOT
      // split the run: a long turn that refreshes its plan repeatedly used
      // to shred one execution block into several look-alike groups (each
      // plan_todos call/result flushed the run). Errors still break the run.
      if (m.toolName === "plan_todos") {
        out.push(messageItem(m));
        continue;
      }
      flush();
      out.push(messageItem(m));
      continue;
    }
    if (run.length > 0 && run[0].author !== m.author) flush();
    run.push(m);
  }
  flush();
  return out;
}

export type ToolGroupStatus = "confirm" | "running" | "failed" | "done";

/** toolGroupStatus derives the execution block's header state from the TURN's
 *  outcome, not from its individual steps: a tool call that failed is usually
 *  a step the model retried or worked around, and the turn still answered — it
 *  must not turn the whole run red. A pending HITL confirmation outranks
 *  everything: the backend ends the turn when it pauses for approval, so
 *  without this the block would collapse and hide the actionable card. A
 *  still-streaming thinking entry or narration also counts as running: the
 *  model may reason between tool batches while the turn continues, and the
 *  block must stay open so the live text is visible. A live turn with no
 *  in-flight signal at all is the silent gap between two tool batches —
 *  deriving "done" there made the block collapse and flip to 完成 between
 *  every batch, so turn-level liveness keeps it running until it settles.
 *  The run failed when it ended in an error without a final reply; a settled
 *  turn with no reply never completed either (history does not keep error
 *  bubbles, so the missing answer is the only durable failure signal left). */
export function toolGroupStatus(group: ToolGroup, runningTools: Set<string>, live: boolean): ToolGroupStatus {
  let running = false;
  let pendingConfirm = false;
  for (const m of group.messages) {
    if (m.kind === "confirm") {
      if ((m.confirmStatus ?? "pending") === "pending") pendingConfirm = true;
    } else if (m.kind === "tool_call") {
      if (runningTools.has(m.id)) running = true;
    } else if (m.kind === "thinking" || m.kind === "assistant") {
      // Reasoning or inter-tool narration still streaming means the turn is
      // live; the block must stay open so the new text is visible.
      if (m.streaming) running = true;
    }
  }
  if (pendingConfirm) return "confirm";
  if (running) return "running";
  if (group.errored) return "failed";
  if (!group.answered) return live ? "running" : "failed";
  return "done";
}

/** toolGroupInvocationCount counts logical tool steps after pairing calls with
 *  their results, so one invocation is one step rather than two rows. */
export function toolGroupInvocationCount(messages: UIMessage[]): number {
  return buildToolTrace(messages).filter((item) => item.kind === "tool").length;
}

/** toolGroupDurationMs sums the completed tools' runtimes. Per-tool durations
 *  (instead of wall clock) keep HITL waits out of the per-tool chips: the pause
 *  between a confirmation card and its result is human time, not tool time. */
export function toolGroupDurationMs(messages: UIMessage[], durations: Map<string, number>): number | null {
  let total = 0;
  let found = false;
  for (const m of messages) {
    const d = m.kind === "tool_result" ? durations.get(m.id) : undefined;
    if (d == null) continue;
    total += d;
    found = true;
  }
  return found ? total : null;
}

/** toolGroupElapsedMs is the wall-clock span of the whole turn: from the first
 *  process event to the final reply (endTs), or to now while the turn is still
 *  live. Timestamp-less histories fall back to the summed tool runtimes. */
export function toolGroupElapsedMs(
  messages: UIMessage[],
  endTs: string | undefined,
  nowMs: number,
  live: boolean,
  durations: Map<string, number>,
): number | null {
  const startTs = messages.find((m) => !!m.timestamp)?.timestamp;
  const start = startTs ? Date.parse(startTs) : NaN;
  if (isNaN(start)) return toolGroupDurationMs(messages, durations);
  let end = NaN;
  if (live) {
    end = nowMs;
  } else if (endTs) {
    end = Date.parse(endTs);
  }
  if (isNaN(end)) {
    const lastTs = [...messages].reverse().find((m) => !!m.timestamp)?.timestamp;
    end = lastTs ? Date.parse(lastTs) : NaN;
  }
  if (isNaN(end)) return toolGroupDurationMs(messages, durations);
  return Math.max(0, end - start);
}

/** formatWorkDuration renders an execution block's elapsed time in Chinese:
 *  19 秒 / 2 分 4 秒 / 1 小时 2 分. */
export function formatWorkDuration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s} 秒`;
  if (s < 3600) return `${Math.floor(s / 60)} 分 ${s % 60} 秒`;
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  return m > 0 ? `${h} 小时 ${m} 分` : `${h} 小时`;
}

/** chatItemStartTs / chatItemEndTs expose an item's visible time span so day
 *  separators compare against the real previous entry (a group spans from its
 *  first event to its last; a turn spans from its user header to its last
 *  body entry). */
export function chatItemStartTs(item: RenderItem): string | undefined {
  if (item.kind === "tool_group") return item.messages[0]?.timestamp;
  if (item.kind === "turn") return item.header.timestamp ?? chatItemStartTs(item.body[0]);
  return item.message.timestamp;
}

export function chatItemEndTs(item: RenderItem): string | undefined {
  if (item.kind === "tool_group") return item.messages[item.messages.length - 1]?.timestamp;
  if (item.kind === "turn") {
    const last = item.body[item.body.length - 1];
    return last ? chatItemEndTs(last) : item.header.timestamp;
  }
  return item.message.timestamp;
}

/**
 * computeEditFlags marks the user messages that may be edited: only the last
 * one, because editing replaces that turn and everything after it. Doing this
 * per row was O(n^2) on every streamed token.
 */
export function computeEditFlags(messages: UIMessage[]): Map<string, boolean> {
  const flags = new Map<string, boolean>();
  let lastUserID: string | null = null;
  for (const m of messages) {
    if (m.kind === "user") lastUserID = m.id;
  }
  if (lastUserID) flags.set(lastUserID, true);
  return flags;
}


/** isDayChange reports whether the two timestamps fall on different calendar
 * days (used to render day separators in long-lived conversations). */
export function isDayChange(prevIso: string | undefined, iso: string | undefined): boolean {
  if (!iso) return false;
  if (!prevIso) return true; // first message always opens a day header
  const a = new Date(prevIso);
  const b = new Date(iso);
  if (isNaN(a.getTime()) || isNaN(b.getTime())) return false;
  return a.getFullYear() !== b.getFullYear() || a.getMonth() !== b.getMonth() || a.getDate() !== b.getDate();
}

/** dayLabel formats a timestamp as "10月30日 星期四" style day header. */
export function dayLabel(iso: string | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  return d.toLocaleDateString("zh-CN", { month: "long", day: "numeric", weekday: "long" });
}

export type SessionGroupKey = "pinned" | "today" | "yesterday" | "week" | "earlier" | "archived";

export interface SessionGroup {
  key: SessionGroupKey;
  label: string;
  sessions: ChatSession[];
}

export interface ProjectGroup {
  /** key is "pinned", "default", "proj:<id>", an ad-hoc dir root or "archived". */
  key: string;
  label: string;
  /** Full workspace root for tooltips; undefined for the default project. */
  path?: string;
  /** Registered project the group belongs to, if any. */
  projectId?: string;
  sessions: ChatSession[];
}

/** How many chats a project shows before「展开显示」. */
export const PROJECT_SESSION_LIMIT = 5;

export interface ProjectSessionWindow {
  visible: ChatSession[];
  hiddenCount: number;
}

const SESSION_GROUP_LABELS: Record<SessionGroupKey, string> = {
  pinned: "置顶",
  today: "今天",
  yesterday: "昨天",
  week: "7 天内",
  earlier: "更早",
  archived: "已归档",
};

const SESSION_GROUP_ORDER: SessionGroupKey[] = ["pinned", "today", "yesterday", "week", "earlier", "archived"];

const byRecency = (a: ChatSession, b: ChatSession): number =>
  (b.updatedAt ?? "").localeCompare(a.updatedAt ?? "");

/** groupSessionsByTime buckets sidebar sessions: pinned first (age-agnostic),
 * then by local calendar day of updatedAt, archived always last. Sessions with
 * missing or invalid timestamps land in "earlier"; empty buckets are omitted. */
export function groupSessionsByTime(sessions: ChatSession[], now: Date): SessionGroup[] {
  const startOf = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const today = startOf(now);
  const buckets = new Map<SessionGroupKey, ChatSession[]>();
  for (const s of sessions) {
    let key: SessionGroupKey = "earlier";
    if (s.archived) {
      key = "archived";
    } else if (s.pinned) {
      key = "pinned";
    } else {
      const t = s.updatedAt ? new Date(s.updatedAt).getTime() : NaN;
      if (!isNaN(t)) {
        const days = Math.round((today - startOf(new Date(t))) / 86400000);
        if (days <= 0) key = "today";
        else if (days === 1) key = "yesterday";
        else if (days < 7) key = "week";
      }
    }
    const list = buckets.get(key);
    if (list) list.push(s);
    else buckets.set(key, [s]);
  }
  return SESSION_GROUP_ORDER
    .filter((key) => buckets.has(key))
    .map((key) => ({ key, label: SESSION_GROUP_LABELS[key], sessions: (buckets.get(key) ?? []).sort(byRecency) }));
}

/** groupSessionsByProject buckets sidebar sessions for the "项目" grouping.
 * Registered projects come first: sessions bound via projectId — and legacy
 * sessions whose explicit workspace equals a project root — join that
 * project's group. Remaining explicit workspaces outside the global one keep
 * their ad-hoc folder groups; auto task dirs under the global workspace and
 * workspace-less sessions form the default project. Archived sessions trail
 * in their own group; groups sort by their newest member. */
export function groupSessionsByProject(sessions: ChatSession[], projects: Project[], globalWorkspace: string): ProjectGroup[] {
  const norm = (p: string) => p.replace(/\\/g, "/").replace(/\/+$/, "").toLowerCase();
  const root = norm(globalWorkspace);
  const underGlobal = (dir: string) => {
    const d = norm(dir);
    return root !== "" && (d === root || d.startsWith(root + "/"));
  };
  const baseName = (dir: string) => {
    const parts = norm(dir).split("/").filter(Boolean);
    return parts[parts.length - 1] ?? dir;
  };

  const groups = new Map<string, ProjectGroup>();
  const push = (key: string, label: string, path: string | undefined, s: ChatSession, projectId?: string) => {
    const g = groups.get(key);
    if (g) g.sessions.push(s);
    else groups.set(key, { key, label, path, projectId, sessions: [s] });
  };
  const byId = new Map(projects.map((p) => [p.id, p]));
  const byRoot = new Map(projects.map((p) => [norm(p.rootDir), p]));
  for (const s of sessions) {
    if (s.archived) {
      push("archived", SESSION_GROUP_LABELS.archived, undefined, s);
      continue;
    }
    const registered = (s.projectId ? byId.get(s.projectId) : undefined)
      ?? (s.workspaceDir ? byRoot.get(norm(s.workspaceDir)) : undefined);
    if (registered) {
      push(`proj:${registered.id}`, registered.name, registered.rootDir, s, registered.id);
      continue;
    }
    const dir = s.workspaceDir || "";
    if (!dir || underGlobal(dir)) {
      push("default", "默认项目", undefined, s);
    } else {
      // Dedupe case-insensitively (Windows paths) but keep the first-seen
      // original spelling as the display key/path.
      const existing = [...groups.values()].find((g) => norm(g.key) === norm(dir));
      if (existing) existing.sessions.push(s);
      else push(dir, baseName(dir), dir, s);
    }
  }
  const newest = (g: ProjectGroup) => g.sessions.reduce((max, s) => (s.updatedAt ?? "") > max ? s.updatedAt ?? "" : max, "");
  return [...groups.values()]
    .sort((a, b) => (a.key === "archived" ? 1 : b.key === "archived" ? -1 : newest(b).localeCompare(newest(a))))
    .map((g) => ({ ...g, sessions: g.sessions.sort(byRecency) }));
}

/** sortSidebarSessions orders each project by recent activity. Pinned chats
 * are extracted into the leading global group before project grouping. */
function sortSidebarSessions(sessions: ChatSession[]): ChatSession[] {
  return [...sessions].sort(byRecency);
}

/** buildSidebarProjectGroups turns the raw workspace grouping into the stable
 * tree used by the compact sidebar:
 *   pinned chats → default chats → registered projects (including empty ones,
 *   in project order) → ad-hoc workspace folders → (archived is rendered
 *   separately). Pinned chats leave their project group so each chat appears
 *   exactly once and stays reachable at the top regardless of its workspace.
 * Registered projects keep their creation order instead of jumping around
 * whenever one of their chats receives a new message. */
export function buildSidebarProjectGroups(sessions: ChatSession[], projects: Project[], globalWorkspace: string): ProjectGroup[] {
  const pinned = sessions.filter((s) => s.pinned && !s.archived).sort(byRecency);
  const grouped = groupSessionsByProject(
    sessions.filter((s) => !s.pinned || s.archived),
    projects,
    globalWorkspace,
  );
  const byKey = new Map(grouped.map((g) => [g.key, g]));
  const out: ProjectGroup[] = [];

  if (pinned.length > 0) out.push({ key: "pinned", label: SESSION_GROUP_LABELS.pinned, sessions: pinned });

  const defaultGroup = byKey.get("default");
  if (defaultGroup) out.push({ ...defaultGroup, label: "对话" });

  for (const p of projects) {
    const group = byKey.get(`proj:${p.id}`);
    out.push(group
      ? { ...group, label: p.name, path: p.rootDir, projectId: p.id }
      : { key: `proj:${p.id}`, label: p.name, path: p.rootDir, projectId: p.id, sessions: [] });
  }

  for (const group of grouped) {
    if (group.key === "default" || group.key === "archived" || group.projectId) continue;
    out.push(group);
  }

  return out.map((group) => ({ ...group, sessions: sortSidebarSessions(group.sessions) }));
}

/** projectSessionWindow returns the compact preview for one project. Expanded
 * groups show every chat; collapsed groups stop at `limit` and report how many
 * rows「展开显示」will reveal. */
export function projectSessionWindow(
  sessions: ChatSession[],
  expanded: boolean,
  limit = PROJECT_SESSION_LIMIT,
): ProjectSessionWindow {
  if (expanded) return { visible: sessions, hiddenCount: 0 };
  const count = Math.max(0, Math.floor(limit));
  return {
    visible: sessions.slice(0, count),
    hiddenCount: Math.max(0, sessions.length - count),
  };
}

/** shouldExpandProjectForSession keeps the active chat visible when it sits
 * beyond a project's five-row preview. Unknown IDs stay collapsed. */
export function shouldExpandProjectForSession(
  sessions: ChatSession[],
  activeID: string,
  limit = PROJECT_SESSION_LIMIT,
): boolean {
  if (!activeID) return false;
  const index = sessions.findIndex((s) => s.id === activeID);
  return index >= Math.max(0, Math.floor(limit));
}
