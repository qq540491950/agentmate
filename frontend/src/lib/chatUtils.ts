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

/** ToolGroup is one collapsible "execution" block: a run of consecutive
 *  process events (thinking entries, tool calls, results, HITL confirmations)
 *  from the same author. */
export type ToolGroup = {
  kind: "tool_group";
  /** id is the first message's id, stable while the run keeps streaming. */
  id: string;
  author: string;
  messages: UIMessage[];
};

/** ChatItem is one entry in the message list: a standalone message or a
 *  grouped execution block. */
export type ChatItem =
  | { kind: "message"; id: string; message: UIMessage }
  | ToolGroup;

/** isProcessEvent reports messages that belong to one turn's execution run:
 *  thinking entries plus tool calls/results and HITL confirmations. plan_todos
 *  results are pinned UI (the todo list card), so they stay outside groups. */
function isProcessEvent(m: UIMessage): boolean {
  if (m.kind === "thinking") return true;
  if (m.kind !== "tool_call" && m.kind !== "tool_result" && m.kind !== "confirm") return false;
  return m.toolName !== "plan_todos";
}

/**
 * groupToolMessages merges runs of consecutive process events from the same
 * author into one collapsible execution block, so a round with thinking plus
 * six tool calls renders as one summary line instead of stacked cards. A run
 * becomes a group when it holds at least two invocations, or when at least one
 * thinking entry accompanies a single invocation — the common single-tool
 * round without thinking keeps its existing standalone cards. User/assistant/
 * error messages, plan_todos cards and author changes break runs, preserving
 * the visible reading order; a final assistant reply therefore stays outside
 * the process block.
 */
export function groupToolMessages(messages: UIMessage[]): ChatItem[] {
  const out: ChatItem[] = [];
  let run: UIMessage[] = [];
  let runAuthor: string | null = null;
  const flush = () => {
    if (run.length === 0) return;
    const calls = run.filter((m) => m.kind === "tool_call").length;
    const results = run.filter((m) => m.kind === "tool_result").length;
    const invocations = Math.max(calls, results);
    const hasThinking = run.some((m) => m.kind === "thinking");
    if (invocations >= 2 || (invocations >= 1 && hasThinking)) {
      out.push({ kind: "tool_group", id: run[0].id, author: run[0].author, messages: run });
    } else {
      for (const m of run) out.push({ kind: "message", id: m.id, message: m });
    }
    run = [];
    runAuthor = null;
  };
  for (const m of messages) {
    if (!isProcessEvent(m)) {
      flush();
      out.push({ kind: "message", id: m.id, message: m });
      continue;
    }
    if (run.length > 0 && runAuthor !== m.author) flush();
    run.push(m);
    runAuthor = m.author;
  }
  flush();
  return out;
}

export type ToolGroupStatus = "confirm" | "running" | "failed" | "done";

/** toolGroupStatus derives the execution block's header state. A pending HITL
 *  confirmation outranks everything: the backend ends the turn when it pauses
 *  for approval, so without this the block would collapse and hide the
 *  actionable card. A still-streaming thinking entry also counts as running:
 *  the model may reason between tool batches while the turn continues, and the
 *  block must stay open so the live text is visible. */
export function toolGroupStatus(messages: UIMessage[], runningTools: Set<string>): ToolGroupStatus {
  let running = false;
  let failed = false;
  let pendingConfirm = false;
  for (const m of messages) {
    if (m.kind === "confirm") {
      if ((m.confirmStatus ?? "pending") === "pending") pendingConfirm = true;
    } else if (m.kind === "tool_call") {
      if (runningTools.has(m.id)) running = true;
    } else if (m.kind === "thinking") {
      if (m.streaming) running = true;
    } else if (m.kind === "tool_result" && toolResultFailed(m.toolResp)) {
      failed = true;
    }
  }
  if (pendingConfirm) return "confirm";
  if (running) return "running";
  if (failed) return "failed";
  return "done";
}

/** toolGroupInvocationCount counts tool invocations in a run: call and result
 *  messages pair up, so the larger count is the round's tool count (a running
 *  parallel round has calls before results). */
export function toolGroupInvocationCount(messages: UIMessage[]): number {
  let calls = 0;
  let results = 0;
  for (const m of messages) {
    if (m.kind === "tool_call") calls++;
    else if (m.kind === "tool_result") results++;
  }
  return Math.max(calls, results);
}

/** toolGroupDurationMs sums the completed tools' runtimes. Per-tool durations
 *  (instead of wall clock) keep HITL waits out of the header total: the pause
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

/** chatItemStartTs / chatItemEndTs expose an item's visible time span so day
 *  separators compare against the real previous entry (a group spans from its
 *  first event to its last). */
export function chatItemStartTs(item: ChatItem): string | undefined {
  return item.kind === "tool_group" ? item.messages[0]?.timestamp : item.message.timestamp;
}

export function chatItemEndTs(item: ChatItem): string | undefined {
  return item.kind === "tool_group"
    ? item.messages[item.messages.length - 1]?.timestamp
    : item.message.timestamp;
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
  /** key is "proj:<id>", an ad-hoc dir root, "default" or "archived". */
  key: string;
  label: string;
  /** Full workspace root for tooltips; undefined for the default project. */
  path?: string;
  /** Registered project the group belongs to, if any. */
  projectId?: string;
  sessions: ChatSession[];
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
