// 聊天页面与其单元测试（vitest）共用的纯辅助函数。
import type { AttachmentOut, ChatSession, Project, ReasoningLevel, UsageInfo } from "@/lib/api";

export type UIMessage = {
  id: string;
  kind: "user" | "assistant" | "thinking" | "tool_call" | "tool_result" | "confirm" | "error" | "warning";
  /** phase 区分过程旁白与最终答复；旧历史没有该字段时按位置回退。 */
  phase?: "commentary" | "final";
  author: string;
  text: string;
  toolName?: string;
  toolArgs?: { [k: string]: any } | null;
  toolResp?: { [k: string]: any } | null;
  callId?: string;
  hint?: string;
  risk?: string;
  diff?: string;
  policy?: string;
  confirmStatus?: "pending" | "approved" | "rejected";
  timestamp?: string;
  durationMs?: number;
  streaming?: boolean;
  usage?: UsageInfo | null;
  attachments?: AttachmentOut[] | null;
  /** reasoning 是本回合发送时所使用的思考等级。 */
  reasoning?: string;
};

/**
 * effectiveReasoningLevel
 * 选取回合将要使用的等级：按顺序取显式选择（先是单回合选择，再是会话默认值），
 * 然后是模型默认值。模型表中未提供的名称会被忽略，因此其他模型的过期选择不会泄
 * 漏进请求。当模型没有表时返回 ""。
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
 * sessionTargetBadgeLabel
 * 返回会话目标的头部徽标文本。直聊（空目标）没有徽标：显示 "直聊"
 * 会让它看起来像 Agent/团队目标旁的一种可选模式。当不应渲染任何徽标时返回
 * null。
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

/** mdToSpeech 将 markdown 转换为可朗读的纯文本字符串。 */
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

/**
 * fmtTime 渲染用于悬停显示的 ISO 时间戳：今天显示为 HH:MM，其他情况显示为 MM-DD
 * HH:MM（长期存活的会话会跨越数天）。
 */
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
 * computeToolDurations 将 tool_result 与其调用配对以计算耗时。HITL
 * 确认会让运行在调用与结果之间挂起，因此被确认的调用的计时会从确认卡片处重新开
 * 始：否则显示的时间就是人点击所花的时间，而非工具运行的时间。
 */
export function computeToolDurations(messages: UIMessage[]): Map<string, number> {
  const map = new Map<string, number>();
  const pending: { id: string; callId?: string; toolName: string; ts: number }[] = [];
  for (const m of messages) {
    const ts = m.timestamp ? Date.parse(m.timestamp) : Date.now();
    if (m.kind === "confirm") {
      // 暂停的工具是最近一次同名调用。
      if (m.timestamp) {
        const idx = [...pending].reverse().findIndex((p) => p.toolName === m.toolName);
        if (idx >= 0) pending[pending.length - 1 - idx].ts = ts;
      }
      continue;
    }
    if (m.kind !== "tool_call" && m.kind !== "tool_result") continue;
    if (m.kind === "tool_call") {
      pending.push({ id: m.id, callId: m.callId, toolName: m.toolName ?? "", ts });
    } else {
      let idx = -1;
      if (m.callId) {
        idx = [...pending].reverse().findIndex((p) => p.callId === m.callId);
      } else {
        idx = [...pending].reverse().findIndex((p) => p.toolName === m.toolName);
      }
      if (idx >= 0) {
        const call = pending.splice(pending.length - 1 - idx, 1)[0];
        map.set(m.id, Math.max(0, ts - call.ts));
      }
    }
  }
  return map;
}

/**
 * computeRunningTools 会标记尚无匹配结果的 tool_call 消息。存在 ADK CallID
 * 时以其为准；对于较旧的历史记录和省略 ID 的供应商，同名 LIFO
 * 仍是回退方案。并行/团队回合可能发出 call A、call B、result B、result A，因此
 * FIFO 队列会使 A 的加载指示器提前结束，并让 B 永远旋转。
 */
export function computeRunningTools(messages: UIMessage[]): Set<string> {
  const running = new Set<string>();
  const pending: { id: string; callId?: string; toolName: string }[] = [];
  for (const m of messages) {
    if (m.kind === "tool_call") {
      running.add(m.id);
      pending.push({ id: m.id, callId: m.callId, toolName: m.toolName ?? "" });
    } else if (m.kind === "tool_result") {
      let idx = -1;
      if (m.callId) {
        idx = [...pending].reverse().findIndex((p) => p.callId === m.callId);
      } else {
        idx = [...pending].reverse().findIndex((p) => p.toolName === (m.toolName ?? ""));
      }
      if (idx >= 0) {
        const call = pending.splice(pending.length - 1 - idx, 1)[0];
        running.delete(call.id);
      }
    }
  }
  return running;
}

/**
 * toolResultFailed
 * 报告工具结果负载是否表示错误。仅检查信封级字段：过去用匹配子串时，会把仅仅带
 * 有 `"error": null` 字段的成功结果标记为错误，并漏掉 MCP 采用 camelCase 的
 * `isError`。
 */
export function toolResultFailed(resp?: { [k: string]: any } | null): boolean {
  if (!resp) return false;
  if (resp.error !== undefined && resp.error !== null && resp.error !== "") return true;
  return resp.isError === true || resp.is_error === true;
}

/**
 * ToolGroup 是一个回合的执行过程容器：思考、过程旁白、工具调用/结果、HITL
 * 确认与计划快照按时间顺序放在同一个可折叠块里。最终答复留在容器之外。
 */
export type ToolGroup = {
  kind: "tool_group";
  /** id 是第一条消息的 id，在运行持续流式传输期间保持稳定。 */
  id: string;
  author: string;
  messages: UIMessage[];
  /** startedAt 是整轮开始时间，通常取用户消息时间，用于统一整轮计时。 */
  startedAt?: string;
  /**
   * endTs
   * 是该回合最终回复的时间戳：摘要统计的是整个回合，包括撰写答案所花的时间，而
   * 不只是工具运行时间。分组不再移动叙述内容，但它们都属于同一回合，并共享该回
   * 合的完成时钟。
   */
  endTs?: string;
  /** answered 记录该回合是否产出了最终回复。在它出现之前，仍在运行的回合表示
   *  正在生成；已结束却没有它的回合表示从未完成。 */
  answered: boolean;
  /** errored 记录以错误气泡而非回复结束的回合（仅限实时流式——历史记录不会持久化错误事件）。 */
  errored: boolean;
  /** terminal 保留为兼容字段；每回合唯一的执行容器始终为 true，并继承整轮
   *  运行/失败状态。 */
  terminal: boolean;
};

/** ChatItem 是消息列表中的一个条目：一条独立消息或一个分组执行块。 */
export type ChatItem =
  | { kind: "message"; id: string; message: UIMessage }
  | ToolGroup;

/**
 * TurnItem
 * 封装一个会话回合：用户消息作为其引用头，加上该次运行在下一个用户消息之前产生
 * 的每个条目。回合轨道和回合卡片都以该头信息的 id 作为键。
 */
export type TurnItem = {
  kind: "turn";
  id: string;
  header: UIMessage;
  body: ChatItem[];
};

/**
 * RenderItem
 * 是消息面板中的一个顶层条目：一张回合卡片，或一个位于第一个用户消息之前的孤立
 * 条目。
 */
export type RenderItem = ChatItem | TurnItem;

/**
 * groupTurns
 * 将扁平条目列表折叠为以用户消息为键的回合卡片。第一个用户消息之前的孤立条目（
 * 发送失败可能遗留的错误气泡）会独立渲染，因此绝不会消失。
 */
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
      // 过去，同时携带文本和图片的用户事件会被渲染成多条具有相同 id
      // 的消息。合并这些部分，而不是创建幽灵回合：重复 id
      // 还会使回合轨道的悬停/激活匹配和 React keys 失效。
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
 * isAwaitingReply
 * 报告用户消息之后、模型首个流式事件之前的静默间隙。回复气泡仅在增量到达后才存
 * 在，因此这是回合仍在启动的唯一信号。
 */
export function isAwaitingReply(messages: UIMessage[], running: boolean): boolean {
  if (!running) return false;
  const tail = messages[messages.length - 1];
  return !tail || tail.kind === "user";
}

/**
 * insertThinkingBeforeStreamingReply
 * 会保持迟到的最终推理事件位于其所属回答之前。回答增量会在聚合后的最终事件能够
 * 传递其思考之前打开流式助手气泡；若在尾部追加该思考，就会把推理放到回复之后，
 * 并使该回合在分组时看起来未得到回答。
 */
export function insertThinkingBeforeStreamingReply(messages: UIMessage[], thinking: UIMessage): UIMessage[] {
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (m.kind === "assistant" && m.streaming && m.author === thinking.author) {
      return [...messages.slice(0, i), thinking, ...messages.slice(i)];
    }
  }
  return [...messages, thinking];
}

/**
 * ToolStep 将一个 tool_call 与其匹配的 tool_result
 * 配对。将这一对保持在一起，才能使执行块读起来像 N 个工具，而不是 2N
 * 行调用/结果。
 */
export type ToolStep = {
  kind: "tool";
  id: string;
  name: string;
  args?: UIMessage["toolArgs"];
  resp?: UIMessage["toolResp"];
  call?: UIMessage;
  result?: UIMessage;
};

/** ToolTraceItem 在将调用和结果折叠为逻辑步骤的同时，保留回合的事件顺序。 */
export type ToolTraceItem =
  | { kind: "message"; message: UIMessage }
  | ToolStep;

/**
 * buildToolTrace 首先按 ADK CallID
 * 将结果与其调用配对；对于较早的历史记录，则回退到同一工具名称最近一次未配对的
 * 调用。即使结果没有保留的调用，仍会作为其自身的步骤渲染。
 */
export function buildToolTrace(messages: UIMessage[]): ToolTraceItem[] {
  const out: ToolTraceItem[] = [];
  const pending: ToolStep[] = [];
  for (const m of messages) {
    // plan_todos 是状态快照，不是工具步骤；它由计划卡单独渲染。
    if (m.toolName === "plan_todos") {
      out.push({ kind: "message", message: m });
      continue;
    }
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
      if (m.callId) {
        for (let i = pending.length - 1; i >= 0; i--) {
          if (pending[i].call?.callId === m.callId && pending[i].resp == null) {
            pendingIdx = i;
            break;
          }
        }
      } else {
        for (let i = pending.length - 1; i >= 0; i--) {
          if (pending[i].name === name && pending[i].resp == null) {
            pendingIdx = i;
            break;
          }
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
  /** 显示在目标之前的简短动词，例如 运行 / 读取 / 搜索知识库。 */
  action: string;
  /** 从调用参数中提取的命令、路径或查询。 */
  target?: string;
  /** 命令用等宽字体阅读效果最佳；诸如 读取 之类的标签则不适合。 */
  mono?: boolean;
};

const toolArgString = (value: unknown): string =>
  typeof value === "string" ? value.trim() : "";

/**
 * toolStepDisplay
 * 将一次工具调用转换为一行可快速浏览的内容。将其保留在纯辅助函数层：执行列表及
 * 其测试必须就未知/MCP 工具如何回退到其第一个有用参数保持一致。
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

/**
 * summarizeToolTrace
 * 为分组标题统计逻辑步骤和失败结果。失败的步骤属于过程细节，而非回合失败；标题
 * 会单独报告它，以免成功的回答掩盖了红色步骤。
 */
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

/**
 * formatToolExecutionBreakdown
 * 渲染标题提示框中使用的紧凑分类摘要。仅保留最大的几个类别：标题必须保持便于浏
 * 览，而下方的步骤行已经展示了其余内容。
 */
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

/**
 * latestPlanMessage 返回最新的非空 plan_todos
 * 结果。调用方在需要会话计划（/todos 命令）时会传入
 * currentTurnOnly=false，而内联时间线和标题标签则保持限定在当前回合内。计划是一
 * 项状态变更而非工具步骤，因此时间线渲染一个快照，而不是每次更新渲染一张卡片；
 * 显式空列表会清除计划并抑制较旧的快照。
 */
export function latestPlanMessage(messages: UIMessage[], currentTurnOnly = true): UIMessage | null {
  let start = 0;
  if (currentTurnOnly) {
    for (let i = messages.length - 1; i >= 0; i--) {
      if (messages[i].kind === "user") {
        start = i + 1;
        break;
      }
    }
  }
  for (let i = messages.length - 1; i >= start; i--) {
    const m = messages[i];
    if (m.kind !== "tool_result" || m.toolName !== "plan_todos") continue;
    const todos = m.toolResp?.todos;
    if (!Array.isArray(todos)) continue;
    return todos.length > 0 ? m : null;
  }
  return null;
}

/**
 * isToolActivity
 * 报告一个回合中实际完成工作的事件。这里刻意排除思考：最终聚合的推理事件可能会
 * 在流式回答之后到达，且不得隐藏该回复。
 */
function isToolActivity(m: UIMessage): boolean {
  if (m.kind === "confirm") return true;
  if (m.kind !== "tool_call" && m.kind !== "tool_result") return false;
  return m.toolName !== "plan_todos";
}

/**
 * groupToolMessages 将一个回合内的过程事件折叠为唯一执行容器：思考、过程旁白、
 * 工具调用/结果、HITL 确认和计划快照按时间顺序放在一起；最终答复留在容器之外。
 *
 * 容器的 id 取自首个过程事件，因此流式追加不会改变 React key 或用户展开状态。
 * 这与旧实现的关键区别是：助手叙述不再作为顶层分隔符，而属于过程容器；最终答复
 * 由 phase 明确标记，不再依赖“最后一个 assistant”这种会被后续工具推翻的位置推断。
 */
export function groupToolMessages(messages: UIMessage[]): ChatItem[] {
  const out: ChatItem[] = [];
  let start = 0;
  let turnStartedAt: string | undefined;
  while (start < messages.length) {
    if (messages[start].kind === "user") {
      out.push(messageItem(messages[start]));
      turnStartedAt = messages[start].timestamp;
      start++;
      continue;
    }
    let end = start + 1;
    while (end < messages.length && messages[end].kind !== "user") end++;
    out.push(...groupTurn(messages.slice(start, end), turnStartedAt));
    start = end;
  }
  return out;
}

function messageItem(m: UIMessage): ChatItem {
  return { kind: "message", id: m.id, message: m };
}

/** groupTurn 将整个回合折叠为一个执行容器，并把最终答复独立放在容器之后。 */
function groupTurn(turn: UIMessage[], turnStartedAt?: string): ChatItem[] {
  // plan_todos
  // 更新是状态快照，而不是步骤。仅保留最新的非空快照，并将其插入到本轮中计划首
  // 次出现的位置；否则每次更新都会追加另一份完整清单，以及一张带有原始 JSON
  // 参数的通用工具调用卡片。
  const planMessage = latestPlanMessage(turn);
  let planAnchor = -1;
  if (planMessage) {
    planAnchor = turn.findIndex((m) =>
      m.kind === "tool_result" &&
      m.toolName === "plan_todos" &&
      Array.isArray(m.toolResp?.todos) &&
      m.toolResp.todos.length > 0,
    );
    if (planAnchor < 0) {
      planAnchor = turn.findIndex((m) => m.toolName === "plan_todos");
    }
  }
  const timeline: UIMessage[] = [];
  for (let i = 0; i < turn.length; i++) {
    const m = turn[i];
    if (m.toolName === "plan_todos") {
      if (planMessage && i === planAnchor) timeline.push(planMessage);
      continue;
    }
    timeline.push(m);
  }

  // 空 assistant 外壳只是流式占位，不应参与最终答复判定或过程计数。
  const meaningful = timeline.filter(
    (m) => !(m.kind === "assistant" && !m.text.trim() && !m.streaming),
  );

  // 结果边界是最后一次工具/确认活动，而不是最后一次思考事件：最终推理块可能在流
  // 式回答之后到达（回答气泡已经存在，因此思考会追加在它后面）。将该思考视为边
  // 界会把回答藏在执行块内，并使已完成的回合变红。
  let lastActivityIdx = -1;
  for (let i = meaningful.length - 1; i >= 0; i--) {
    if (isToolActivity(meaningful[i])) { lastActivityIdx = i; break; }
  }

  // 后端标记为 final 的文本是最终答复。旧历史没有 phase，则回退到“最后一次工具
  // 活动之后的最后一条 assistant”；仍在流式输出且已经发生工具活动时先留在过程
  // 中，等聚合事件补上 phase 或回合结束再提升，避免每个 token 都移动位置。
  let finalIdx = -1;
  for (let i = meaningful.length - 1; i >= 0; i--) {
    const m = meaningful[i];
    if (m.kind !== "assistant" || m.phase !== "final" || !m.text.trim()) continue;
    finalIdx = i;
    break;
  }
  if (finalIdx < 0) {
    for (let i = meaningful.length - 1; i > lastActivityIdx; i--) {
      const m = meaningful[i];
      if (m.kind !== "assistant") continue;
      if (!m.text.trim() && !m.streaming) continue;
      if (m.phase === "commentary") continue;
      if (m.streaming && lastActivityIdx >= 0 && !m.phase) continue;
      finalIdx = i;
      break;
    }
  }

  const final = finalIdx >= 0 ? meaningful[finalIdx] : undefined;
  const errors = meaningful.filter((m, i) => i !== finalIdx && m.kind === "error");
  const processEvents = meaningful.filter((m, i) => i !== finalIdx && m.kind !== "error");

  // 没有过程事件时保持普通消息：直聊回答不需要为了显示结构凭空生成执行块。
  if (processEvents.length === 0) {
    return meaningful.map(messageItem);
  }

  // 以回合结果为准，而非步骤结果：模型绕过的一个失败工具只是
  // 过程细节。只有以错误结束且从未写出
  // 其回复的回合才算失败——后续用户消息之前的护栏警告，以及
  // 回复之后的预算警告，两者仍然都有/留下答案。
  const answered = !!final;
  const errored = !answered && errors.length > 0;
  const startedAt = turnStartedAt || processEvents.find((m) => !!m.timestamp)?.timestamp;
  const out: ChatItem[] = [{
    kind: "tool_group",
    id: processEvents[0].id,
    author: processEvents[0].author,
    messages: processEvents,
    startedAt,
    endTs: final?.timestamp,
    answered,
    errored,
    terminal: true,
  }];
  if (final) out.push(messageItem(final));
  out.push(...errors.map(messageItem));
  return out;
}

export type ToolGroupStatus = "confirm" | "running" | "failed" | "done";

/**
 * toolGroupStatus 根据回合的结果，而不是根据各个步骤，来推导执行块的标题状态：
 * 失败的工具调用通常是模型重试过或绕过的步骤，而且回合仍然给出了回答——它
 * 不得把整个运行变成红色。待处理的 HITL 确认优先于一切：
 * 后端在暂停等待审批时会结束回合，因此
 * 若没有这一点，执行块会折叠并隐藏可操作的卡片。
 * 仍在流式输出的思考条目或叙述也算作运行中：
 * 模型可以在回合继续期间在工具批次之间进行推理，且
 * 执行块必须保持展开，以便实时文本可见。
 * 没有任何在途信号的活跃回合是两个工具批次之间的静默间隙—— 在那里推导出 "done"
 * 会让执行块在每个批次之间折叠并切换为完成，
 * 因此回合级活跃状态会让它保持运行，直到它落定。
 * 当运行以错误结束且没有最终回复时，该运行失败；
 * 已落定且没有回复的回合也从未完成（历史记录不保留错误
 * 气泡，因此缺失的回答是唯一留下的持久失败信号）。
 */
export function toolGroupStatus(group: ToolGroup, runningTools: Set<string>, live: boolean): ToolGroupStatus {
  let running = false;
  let pendingConfirm = false;
  for (const m of group.messages) {
    if (m.kind === "confirm") {
      if ((m.confirmStatus ?? "pending") === "pending") pendingConfirm = true;
    } else if (m.kind === "tool_call") {
      if (runningTools.has(m.id)) running = true;
    } else if (m.kind === "thinking" || m.kind === "assistant") {
      // 推理或工具间叙述仍在流式输出意味着回合仍然活跃；
      // 执行块必须保持展开，以便新文本可见。
      if (m.streaming) running = true;
    }
  }
  if (pendingConfirm) return "confirm";
  if (running) return "running";
  if (!group.terminal) return "done";
  if (group.errored) return "failed";
  if (!group.answered) return live ? "running" : "failed";
  return "done";
}

/**
 * toolGroupInvocationCount
 * 在将调用与其结果配对后统计逻辑工具步骤数，因此一次调用是一个步骤，而不是两行
 * 。
 */
export function toolGroupInvocationCount(messages: UIMessage[]): number {
  return buildToolTrace(messages).filter((item) => item.kind === "tool").length;
}

/**
 * toolGroupDurationMs
 * 累加已完成工具的运行时长。按工具统计时长（而非按墙钟时间）可将 HITL
 * 等待排除在各工具标签之外：确认卡片与其结果之间的停顿属于人工时间，而非工具时
 * 间。
 */
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

/**
 * toolGroupElapsedMs 是整个回合的墙钟时间跨度：优先从用户消息时间开始，到最终回复
 * （endTs）或当前时刻。旧调用没有 startedAt 时回退到首个 process 事件；无时间戳的
 * 历史记录回退到累加的工具运行时长。
 */
export function toolGroupElapsedMs(
  messages: UIMessage[],
  endTs: string | undefined,
  nowMs: number,
  live: boolean,
  durations: Map<string, number>,
  startedAt?: string,
): number | null {
  const startTs = startedAt ?? messages.find((m) => !!m.timestamp)?.timestamp;
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

/** formatWorkDuration 用中文显示执行块的已用时间：19 秒 / 2 分 4 秒 / 1 小时 2 分。 */
export function formatWorkDuration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s} 秒`;
  if (s < 3600) return `${Math.floor(s / 60)} 分 ${s % 60} 秒`;
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  return m > 0 ? `${h} 小时 ${m} 分` : `${h} 小时`;
}

/**
 * chatItemStartTs / chatItemEndTs
 * 暴露一个条目的可见时间跨度，以便日期分隔符与真正的前一条目比较（一个分组从其
 * 首个事件跨越到最后一个事件；一个回合从其用户消息头跨越到其最后一个正文条目）
 * 。
 */
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
 * computeEditFlags
 * 标记可以编辑的用户消息：仅最后一条，因为编辑会替换该回合及其之后的所有内容。
 * 逐行执行此操作会在每个流式 token 上达到 O(n^2)。
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


/** isDayChange 报告两个时间戳是否落在不同的日历日（用于在长期会话中渲染日期分隔符）。 */
export function isDayChange(prevIso: string | undefined, iso: string | undefined): boolean {
  if (!iso) return false;
  if (!prevIso) return true; // 第一条消息始终开启日期标题
  const a = new Date(prevIso);
  const b = new Date(iso);
  if (isNaN(a.getTime()) || isNaN(b.getTime())) return false;
  return a.getFullYear() !== b.getFullYear() || a.getMonth() !== b.getMonth() || a.getDate() !== b.getDate();
}

/** dayLabel 将时间戳格式化为 "10月30日 星期四" 风格的日期标题。 */
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
  /** key 为 "pinned"、"default"、"proj:<id>"、临时目录根或 "archived"。 */
  key: string;
  label: string;
  /** 用于提示框的完整工作区根路径；默认项目下为 undefined。 */
  path?: string;
  /** 该分组所属的已注册项目（若有）。 */
  projectId?: string;
  sessions: ChatSession[];
}

/** 项目在「展开显示」之前显示多少个会话。 */
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

/**
 * groupSessionsByTime 对侧边栏会话分桶：置顶在前（与时间无关）， 随后按
 * updatedAt 的本地日历日分组，归档始终排在最后。时间戳缺失或 无效的会话归入
 * "earlier"；空桶会被省略。
 */
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

/**
 * groupSessionsByProject 为 "项目" 分组对侧边栏会话分桶。 已注册项目优先：通过
 * projectId 绑定的会话，以及显式工作区等于某个项目根的
 * 遗留会话，都会加入该项目的分组。其余位于全局工作区之外的显式工作区保留
 * 各自的临时文件夹分组；全局工作区下的自动任务目录以及无工作区的会话构成默认项
 * 目。 归档会话单独成组排在最后；分组按其最新成员排序。
 */
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
      // 以不区分大小写的方式去重（Windows 路径），但保留首次出现的
      // 原始拼写作为展示用的 key/path。
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

/**
 * sortSidebarSessions
 * 按最近活动对每个项目排序。置顶会话在项目分组之前被提取到最前面的全局分组中。
 */
function sortSidebarSessions(sessions: ChatSession[]): ChatSession[] {
  return [...sessions].sort(byRecency);
}

/**
 * buildSidebarProjectGroups
 * 将原始工作区分组转换为紧凑侧边栏所使用的稳定树结构： 置顶会话 → 默认会话 →
 * 已注册项目（包括空项目，按项目顺序）→ 临时工作区文件夹
 * →（归档项单独渲染）。置顶会话会离开其项目分组，因此每个会话只出现一次，并且无
 * 论其所属工作区如何，都始终可在顶部访问。
 * 已注册项目保持其创建顺序，而不会在其某个会话收到新消息时来回跳动。
 */
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

/**
 * projectSessionWindow
 * 返回单个项目的紧凑预览。展开的分组显示所有会话；折叠的分组在 `limit`
 * 处停止，并报告「展开显示」将展示多少行。
 */
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

/**
 * shouldExpandProjectForSession
 * 在当前活动会话位于项目五行预览之外时保持其可见。未知 ID 保持折叠状态。
 */
export function shouldExpandProjectForSession(
  sessions: ChatSession[],
  activeID: string,
  limit = PROJECT_SESSION_LIMIT,
): boolean {
  if (!activeID) return false;
  const index = sessions.findIndex((s) => s.id === activeID);
  return index >= Math.max(0, Math.floor(limit));
}
