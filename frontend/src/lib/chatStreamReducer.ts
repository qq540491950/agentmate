import type { AttachmentOut, UsageInfo } from "@/lib/api";
import {
  insertThinkingBeforeStreamingReply,
  type UIMessage,
} from "@/lib/chatUtils";
import { replaceMessageByID } from "@/lib/chatTimeline";

export type PlanStepUpdate = {
  id: string;
  content: string;
  status: string;
  dependsOn?: string[] | null;
  acceptance?: string[] | null;
  evidence?: string[] | null;
};

export type PlanUpdate = {
  version: string;
  steps: PlanStepUpdate[] | null;
};

export type VerificationInfo = {
  ok: boolean;
  status: string;
  attempts?: number;
  artifact?: string;
  output?: string;
  reason?: string;
  exempt?: boolean;
};

export type BudgetPauseInfo = {
  reason: string;
  limit: number;
  used: number;
  toolCalls: number;
  modelCalls: number;
  turnTokens: number;
};

export type ChatRunStatus =
  | "queued"
  | "preparing"
  | "running"
  | "waiting_approval"
  | "verifying"
  | "budget_paused"
  | "completed"
  | "failed"
  | "cancelled"
  | "interrupted"
  | "";

export type RetryInfo = {
  stepId: string;
  reason?: string;
  attempt: number;
};

export type ChatRunState = {
  sessionId: string;
  turnId: string;
  status: ChatRunStatus;
  running: boolean;
  terminal: boolean;
  waitingApproval: boolean;
  budgetPaused: boolean;
  verifying: boolean;
  lastSeq: number;
  seqByTurn: Record<string, number>;
  lastWarning?: string;
  lastError?: string;
  plan?: PlanUpdate | null;
  budget?: BudgetPauseInfo | null;
  verification?: VerificationInfo | null;
  retry?: RetryInfo | null;
  delegation?: string;
  terminalStatus?: ChatRunStatus;
};

export type ChatStreamEventLike = {
  version?: number;
  seq?: number;
  sessionId: string;
  turnId?: string;
  kind: string;
  turnStatus?: string;
  text?: string;
  plan?: PlanUpdate | null;
  budget?: BudgetPauseInfo | null;
  verification?: VerificationInfo | null;
  retry?: RetryInfo | null;
  author?: string;
  toolName?: string;
  toolArgs?: { [k: string]: any } | null;
  toolResp?: { [k: string]: any } | null;
  callId?: string;
  hint?: string;
  risk?: string;
  diff?: string;
  policy?: string;
  usage?: UsageInfo | null;
  attachments?: AttachmentOut[] | null;
  reasoning?: string;
};

export function initialChatRunState(sessionId = ""): ChatRunState {
  return {
    sessionId,
    turnId: "",
    status: "",
    running: false,
    terminal: false,
    waitingApproval: false,
    budgetPaused: false,
    verifying: false,
    lastSeq: 0,
    seqByTurn: {},
  };
}

const terminalStatuses = new Set(["completed", "failed", "cancelled", "interrupted"]);

function isLegacy(event: ChatStreamEventLike): boolean {
  return !event.version || event.version < 2;
}

function statusFromEvent(event: ChatStreamEventLike): ChatRunStatus {
  const raw = (event.turnStatus ?? "") as ChatRunStatus;
  switch (raw) {
    case "queued":
    case "preparing":
    case "running":
    case "waiting_approval":
    case "verifying":
    case "budget_paused":
    case "completed":
    case "failed":
    case "cancelled":
    case "interrupted":
      return raw;
    default:
      return "";
  }
}

/**
 * reduceChatStreamEvent 是纯协议 reducer：只维护回合生命周期、计划、预算暂停、
 * 验证、重试与警告。消息内容仍由 ChatPage 的渲染层处理，但任何“终止/运行中”
 * 判断都必须消费这里的 turn_state 结果。
 */
export function reduceChatStreamEvent(
  prev: ChatRunState,
  event: ChatStreamEventLike,
): ChatRunState {
  if (!event || event.sessionId !== prev.sessionId) return prev;
  const turnId = event.turnId || prev.turnId || "";
  const seq = event.seq ?? 0;
  if (turnId && seq > 0) {
    const seen = prev.seqByTurn[turnId] ?? 0;
    if (seq <= seen) return prev;
  }

  let next: ChatRunState = {
    ...prev,
    turnId,
    lastSeq: seq || prev.lastSeq,
    seqByTurn: turnId && seq > 0
      ? { ...prev.seqByTurn, [turnId]: seq }
      : prev.seqByTurn,
  };

  // 新回合的第一个事件重置上一回合的短暂状态，但保留每回合游标。
  if (turnId && prev.turnId && turnId !== prev.turnId) {
    next = {
      ...initialChatRunState(prev.sessionId),
      turnId,
      lastSeq: seq,
      seqByTurn: next.seqByTurn,
    };
  }

  switch (event.kind) {
    case "turn_state": {
      const status = statusFromEvent(event);
      if (!status) return next;
      const terminal = terminalStatuses.has(status);
      return {
        ...next,
        status,
        terminal,
        terminalStatus: terminal ? status : undefined,
        running: status === "running" || status === "preparing" || status === "verifying",
        waitingApproval: status === "waiting_approval",
        budgetPaused: status === "budget_paused",
        verifying: status === "verifying",
      };
    }
    case "budget_pause":
      return {
        ...next,
        status: "budget_paused",
        running: false,
        waitingApproval: false,
        budgetPaused: true,
        budget: event.budget ?? null,
      };
    case "warning":
    case "delegation_progress":
      return event.kind === "warning"
        ? { ...next, lastWarning: event.text ?? "" }
        : { ...next, delegation: event.text ?? "" };
    case "error":
      // v2 的 error 是可展示错误，不是终止信号；终止只看 turn_state。
      return {
        ...next,
        lastError: event.text || "error",
        ...(isLegacy(event)
          ? { status: "failed" as ChatRunStatus, terminal: true, terminalStatus: "failed" as ChatRunStatus, running: false }
          : {}),
      };
    case "done":
      if (!isLegacy(event)) return next;
      return next.lastError
        ? { ...next, status: "failed", terminal: true, terminalStatus: "failed", running: false }
        : { ...next, status: "completed", terminal: true, terminalStatus: "completed", running: false };
    case "plan":
      return { ...next, plan: event.plan ?? null };
    case "verification":
      return {
        ...next,
        verification: event.verification ?? null,
        verifying: event.verification?.status === "running",
      };
    case "retry":
      return { ...next, retry: event.retry ?? null };
    case "user":
      return { ...next, running: true, terminal: false, waitingApproval: false, budgetPaused: false };
    default:
      return next;
  }
}

/** shouldFireQueued 只在用户可见的成功完成时恢复队列；失败/取消/等待保持暂停。 */
export function shouldFireQueued(state: ChatRunState): boolean {
  return state.terminal && state.terminalStatus === "completed";
}

export type PendingUserForReducer = {
  message: UIMessage;
  confirmed?: boolean;
  confirmedFromHistory?: boolean;
};

export type ChatMessageReduceContext = {
  pendingUser?: PendingUserForReducer | null;
  /** 由 ChatPage 在确认乐观消息后计算；reducer 不依赖可变的确认标志。 */
  pendingUserID?: string;
  thinkingStreamed: boolean;
  createID: () => string;
  timestamp: string;
};

/**
 * reduceChatMessages 是消息时间线的纯 reducer。ChatPage 只负责流式缓冲和
 * refs 的生命周期，所有最终消息、工具对、确认卡、错误和重试提示都从这里追加，
 * 避免页面与历史渲染出现两套事件语义。
 */
export function reduceChatMessages(
  prev: UIMessage[],
  event: ChatStreamEventLike,
  ctx: ChatMessageReduceContext,
): UIMessage[] {
  const id = () => ctx.createID();
  switch (event.kind) {
    case "user": {
      const pendingUser = ctx.pendingUser ?? null;
      if (pendingUser) {
        const pendingUserID = ctx.pendingUserID ?? (
          pendingUser.confirmed || pendingUser.confirmedFromHistory ? "" : pendingUser.message.id
        );
        if (!pendingUserID) return prev;
        const next = replaceMessageByID(prev, pendingUserID, {
          text: event.text ?? "",
          attachments: event.attachments ?? null,
          reasoning: event.reasoning,
        });
        return next === prev
          ? [...prev, {
            ...pendingUser.message,
            text: event.text ?? "",
            attachments: event.attachments ?? null,
            reasoning: event.reasoning,
          }]
          : next;
      }
      return [...prev, {
        id: id(), kind: "user", author: "user", text: event.text ?? "",
        timestamp: ctx.timestamp, attachments: event.attachments ?? null,
        reasoning: event.reasoning,
      }];
    }
    case "thinking": {
      const author = event.author ?? "";
      let idx = prev.length - 1;
      while (idx >= 0 && !(prev[idx].kind === "thinking" && prev[idx].author === author)) idx--;
      if (ctx.thinkingStreamed && idx >= 0) {
        const copy = [...prev];
        copy[idx] = { ...copy[idx], text: event.text || copy[idx].text, streaming: false };
        return copy;
      }
      if (!event.text) return prev;
      return insertThinkingBeforeStreamingReply(prev, {
        id: id(), kind: "thinking", author, text: event.text,
        streaming: false, timestamp: ctx.timestamp,
      });
    }
    case "message": {
      const author = event.author ?? "";
      const idx = [...prev].reverse().findIndex((m) => m.streaming && m.author === author);
      if (idx >= 0) {
        const real = prev.length - 1 - idx;
        const copy = [...prev];
        copy[real] = {
          ...copy[real],
          text: event.text ?? "",
          streaming: false,
          usage: event.usage ?? null,
          timestamp: ctx.timestamp,
        };
        return copy;
      }
      return [...prev, {
        id: id(), kind: "assistant", author, text: event.text ?? "",
        usage: event.usage ?? null, timestamp: ctx.timestamp,
      }];
    }
    case "tool_call":
      return [...prev, {
        id: id(), kind: "tool_call", author: event.author ?? "", text: "",
        toolName: event.toolName, toolArgs: event.toolArgs,
        callId: event.callId, timestamp: ctx.timestamp,
      }];
    case "tool_result":
      return [...prev, {
        id: id(), kind: "tool_result", author: event.author ?? "", text: "",
        toolName: event.toolName, toolResp: event.toolResp,
        callId: event.callId, timestamp: ctx.timestamp,
      }];
    case "confirm_request":
      return [...prev, {
        id: id(), kind: "confirm", author: event.author ?? "", text: "",
        toolName: event.toolName, toolArgs: event.toolArgs,
        callId: event.callId, hint: event.hint, confirmStatus: "pending",
        risk: event.risk, diff: event.diff, policy: event.policy,
        timestamp: ctx.timestamp,
      } as UIMessage];
    case "error":
      return [...prev, {
        id: id(), kind: "error", author: "system", text: event.text ?? "",
        timestamp: ctx.timestamp,
      }];
    case "warning":
      return [...prev, {
        id: id(), kind: "warning", author: event.author || "system",
        text: event.text ?? "", timestamp: ctx.timestamp,
      }];
    case "delegation_progress":
      return event.text
        ? [...prev, {
          id: id(), kind: "warning", author: event.author || "team",
          text: event.text, timestamp: ctx.timestamp,
        }]
        : prev;
    case "retry": {
      const retry = event.retry;
      return [...prev, {
        id: id(), kind: "warning", author: "system",
        text: `正在重试步骤 ${retry?.stepId ?? ""}（第 ${retry?.attempt ?? 1} 次）`,
        timestamp: ctx.timestamp,
      }];
    }
    case "done": {
      const completedAt = ctx.timestamp;
      return prev.map((m) => (m.streaming ? { ...m, streaming: false, timestamp: completedAt } : m));
    }
    default:
      return prev;
  }
}
