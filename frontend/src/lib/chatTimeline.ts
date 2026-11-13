import type { UIMessage } from "@/lib/chatUtils";

/**
 * PendingUserTurn
 * 是后端用户事件到达之前显示的本地用户气泡。该事件会确认或替换它；发送被拒绝时
 * 会移除它。
 */
export type PendingUserTurn = {
  sessionID: string;
  message: UIMessage;
  startedAt: number;
  /** 一旦后端用户事件或历史快照确认该回合，即设置。 */
  confirmed?: boolean;
  /** 当最新历史快照已包含此用户回合时设置。 */
  confirmedFromHistory?: boolean;
};

/**
 * historySnapshotIsCurrent
 * 用于防止在较新的请求或另一个活跃会话胜出后，较早的会话加载替换实时时间线。
 */
export function historySnapshotIsCurrent(params: {
  requestSessionID: string;
  activeSessionID: string;
  requestGeneration: number;
  currentGeneration: number;
}): boolean {
  return params.requestSessionID === params.activeSessionID
    && params.requestGeneration === params.currentGeneration;
}

/**
 * historyHasUserTurnAfter
 * 报告历史快照是否已包含由本地乐观气泡所代表的用户回合。当时间戳与浏览器时钟相
 * 差几秒时，容差可避免将刚刚持久化的回合再次追加。
 */
export function historyHasUserTurnAfter(
  messages: UIMessage[],
  startedAt: number,
  toleranceMs = 15_000,
): boolean {
  const threshold = startedAt - Math.max(0, toleranceMs);
  return messages.some((message) => {
    if (message.kind !== "user" || !message.timestamp) return false;
    const timestamp = Date.parse(message.timestamp);
    return !Number.isNaN(timestamp) && timestamp >= threshold;
  });
}

/** 仅当历史快照尚未包含该回合时，reconcilePendingUser 才会追加乐观气泡。 */
export function reconcilePendingUser(
  history: UIMessage[],
  pending: PendingUserTurn | null,
  activeSessionID: string,
): { messages: UIMessage[]; confirmedFromHistory: boolean } {
  if (!pending || pending.sessionID !== activeSessionID) {
    return { messages: history, confirmedFromHistory: false };
  }
  if (historyHasUserTurnAfter(history, pending.startedAt)) {
    return { messages: history, confirmedFromHistory: true };
  }
  return {
    messages: [...history, pending.message],
    confirmedFromHistory: false,
  };
}

/** replaceMessageByID 原地更新一条乐观消息，并在后端确认它时保留其列表位置。 */
export function replaceMessageByID(
  messages: UIMessage[],
  id: string,
  patch: Partial<UIMessage>,
): UIMessage[] {
  let changed = false;
  const next = messages.map((message) => {
    if (message.id !== id) return message;
    changed = true;
    return { ...message, ...patch };
  });
  return changed ? next : messages;
}

/** dropMessageByID 在发送被拒绝后移除临时消息。 */
export function dropMessageByID(messages: UIMessage[], id: string): UIMessage[] {
  return messages.filter((message) => message.id !== id);
}
