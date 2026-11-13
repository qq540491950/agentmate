import { describe, expect, it } from "vitest";
import type { UIMessage } from "@/lib/chatUtils";
import {
  dropMessageByID,
  historyHasUserTurnAfter,
  historySnapshotIsCurrent,
  reconcilePendingUser,
  replaceMessageByID,
  type PendingUserTurn,
} from "@/lib/chatTimeline";

const msg = (over: Partial<UIMessage>): UIMessage => ({
  id: over.id ?? "m",
  kind: "assistant",
  author: "bot",
  text: "",
  ...over,
});

describe("historySnapshotIsCurrent", () => {
  it("rejects a stale generation or a session that is no longer active", () => {
    expect(historySnapshotIsCurrent({
      requestSessionID: "s1",
      activeSessionID: "s1",
      requestGeneration: 3,
      currentGeneration: 3,
    })).toBe(true);
    expect(historySnapshotIsCurrent({
      requestSessionID: "s1",
      activeSessionID: "s2",
      requestGeneration: 3,
      currentGeneration: 3,
    })).toBe(false);
    expect(historySnapshotIsCurrent({
      requestSessionID: "s1",
      activeSessionID: "s1",
      requestGeneration: 2,
      currentGeneration: 3,
    })).toBe(false);
  });
});

describe("pending user reconciliation", () => {
  const pending: PendingUserTurn = {
    sessionID: "s1",
    startedAt: Date.parse("2026-11-13T10:00:00Z"),
    message: msg({ id: "local-user", kind: "user", author: "user", text: "你好" }),
  };

  it("keeps an optimistic user bubble when the stale history has no turn", () => {
    const result = reconcilePendingUser([], pending, "s1");
    expect(result.messages.map((m) => m.id)).toEqual(["local-user"]);
    expect(result.confirmedFromHistory).toBe(false);
  });

  it("does not duplicate a user turn already present in history", () => {
    const historical = msg({
      id: "event-user-1",
      kind: "user",
      author: "user",
      text: "你好",
      timestamp: "2026-11-13T10:00:02Z",
    });
    const result = reconcilePendingUser([historical], pending, "s1");
    expect(result.messages).toEqual([historical]);
    expect(result.confirmedFromHistory).toBe(true);
  });

  it("ignores a pending user that belongs to another session", () => {
    const history = [msg({ id: "old" })];
    expect(reconcilePendingUser(history, pending, "s2")).toEqual({
      messages: history,
      confirmedFromHistory: false,
    });
  });

  it("detects the user turn using the local send time with tolerance", () => {
    expect(historyHasUserTurnAfter([
      msg({ id: "u", kind: "user", author: "user", timestamp: "2026-11-13T09:59:50Z" }),
    ], pending.startedAt)).toBe(true);
    expect(historyHasUserTurnAfter([
      msg({ id: "u", kind: "user", author: "user", timestamp: "2026-11-13T09:59:00Z" }),
    ], pending.startedAt)).toBe(false);
  });
});

describe("message replacement", () => {
  it("updates the optimistic bubble in place", () => {
    const messages = [
      msg({ id: "u1", kind: "user", author: "user", text: "old" }),
      msg({ id: "a1", text: "answer" }),
    ];
    expect(replaceMessageByID(messages, "u1", { text: "new" })).toEqual([
      messages[0] && { ...messages[0], text: "new" },
      messages[1],
    ]);
  });

  it("returns the same array when the message is absent", () => {
    const messages = [msg({ id: "u1" })];
    expect(replaceMessageByID(messages, "missing", { text: "x" })).toBe(messages);
  });

  it("drops a rejected optimistic bubble", () => {
    const messages = [msg({ id: "u1" }), msg({ id: "u2" })];
    expect(dropMessageByID(messages, "u1").map((m) => m.id)).toEqual(["u2"]);
  });
});
