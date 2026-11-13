import type { UIMessage } from "@/lib/chatUtils";

/** PendingUserTurn is the local user bubble shown before the backend's user
 * event arrives. The event confirms/replaces it; a rejected send removes it. */
export type PendingUserTurn = {
  sessionID: string;
  message: UIMessage;
  startedAt: number;
  /** Set once the backend user event or a history snapshot confirms the turn. */
  confirmed?: boolean;
  /** Set when the latest history snapshot already contains this user turn. */
  confirmedFromHistory?: boolean;
};

/** historySnapshotIsCurrent prevents an older session load from replacing the
 * live timeline after a newer request or a different active chat won. */
export function historySnapshotIsCurrent(params: {
  requestSessionID: string;
  activeSessionID: string;
  requestGeneration: number;
  currentGeneration: number;
}): boolean {
  return params.requestSessionID === params.activeSessionID
    && params.requestGeneration === params.currentGeneration;
}

/** historyHasUserTurnAfter reports whether a history snapshot already contains
 * the user turn represented by a local optimistic bubble. The tolerance keeps
 * a just-persisted turn from being appended a second time when its timestamp
 * is a few seconds off the browser clock. */
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

/** reconcilePendingUser appends the optimistic bubble only when the history
 * snapshot does not already carry that turn. */
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

/** replaceMessageByID updates one optimistic message in place, preserving its
 * list position when the backend confirms it. */
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

/** dropMessageByID removes a provisional message after a rejected send. */
export function dropMessageByID(messages: UIMessage[], id: string): UIMessage[] {
  return messages.filter((message) => message.id !== id);
}
