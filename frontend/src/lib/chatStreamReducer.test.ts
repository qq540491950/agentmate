import { describe, expect, it } from "vitest";
import {
  initialChatRunState,
  reduceChatMessages,
  reduceChatStreamEvent,
  shouldFireQueued,
  type ChatStreamEventLike,
} from "@/lib/chatStreamReducer";
import type { UIMessage } from "@/lib/chatUtils";

const base = (overrides: Partial<ChatStreamEventLike>): ChatStreamEventLike => ({
  version: 2,
  sessionId: "s1",
  turnId: "t1",
  kind: "turn_state",
  ...overrides,
});

describe("chatStreamReducer", () => {
  it("tracks queued -> preparing -> running -> completed and only turn_state terminates", () => {
    let state = initialChatRunState("s1");
    state = reduceChatStreamEvent(state, base({ seq: 1, turnStatus: "queued" }));
    state = reduceChatStreamEvent(state, base({ seq: 2, turnStatus: "preparing" }));
    state = reduceChatStreamEvent(state, base({ seq: 3, turnStatus: "running" }));
    state = reduceChatStreamEvent(state, base({ seq: 4, kind: "error", text: "工具失败" }));
    expect(state.terminal).toBe(false);
    expect(state.running).toBe(true);
    state = reduceChatStreamEvent(state, base({ seq: 5, kind: "warning", text: "接近预算" }));
    expect(state.terminal).toBe(false);
    state = reduceChatStreamEvent(state, base({ seq: 6, kind: "done" }));
    expect(state.terminal).toBe(false);
    state = reduceChatStreamEvent(state, base({ seq: 7, turnStatus: "completed" }));
    expect(state.terminal).toBe(true);
    expect(state.running).toBe(false);
    expect(shouldFireQueued(state)).toBe(true);
  });

  it("ignores replayed and stale out-of-order events without regressing state", () => {
    let state = initialChatRunState("s1");
    state = reduceChatStreamEvent(state, base({ seq: 10, turnStatus: "running" }));
    const atRunning = state;
    state = reduceChatStreamEvent(state, base({ seq: 10, turnStatus: "queued" }));
    expect(state).toBe(atRunning);
    state = reduceChatStreamEvent(state, base({ seq: 9, turnStatus: "queued" }));
    expect(state).toBe(atRunning);
    state = reduceChatStreamEvent(state, base({ seq: 11, turnStatus: "completed" }));
    expect(state.status).toBe("completed");
  });

  it("keeps legacy done/error compatible but does not infer v2 termination", () => {
    let legacy = initialChatRunState("s1");
    legacy = reduceChatStreamEvent(legacy, base({ version: 0, kind: "error" }));
    expect(legacy.terminal).toBe(true);
    legacy = reduceChatStreamEvent(legacy, base({ version: 0, kind: "done" }));
    expect(legacy.status).toBe("failed");
  });

  it("captures budget, plan, verification and retry events", () => {
    let state = initialChatRunState("s1");
    state = reduceChatStreamEvent(state, base({
      seq: 1, kind: "budget_pause",
      budget: { reason: "tool_calls", limit: 2, used: 2, toolCalls: 2, modelCalls: 1, turnTokens: 10 },
    }));
    expect(state.budgetPaused).toBe(true);
    expect(shouldFireQueued(state)).toBe(false);
    state = reduceChatStreamEvent(state, base({
      seq: 2, kind: "plan",
      plan: { version: "v1", steps: [{ id: "a", content: "步骤", status: "blocked" }] },
    }));
    expect(state.plan?.steps?.[0].id).toBe("a");
    state = reduceChatStreamEvent(state, base({
      seq: 3, kind: "verification",
      verification: { ok: false, status: "failed", attempts: 1 },
    }));
    expect(state.verification?.status).toBe("failed");
    state = reduceChatStreamEvent(state, base({
      seq: 4, kind: "retry", retry: { stepId: "a", attempt: 1 },
    }));
    expect(state.retry?.stepId).toBe("a");
  });

  it("does not resume the queue after cancellation", () => {
    let state = initialChatRunState("s1");
    state = reduceChatStreamEvent(state, base({ seq: 1, turnStatus: "running" }));
    state = reduceChatStreamEvent(state, base({ seq: 2, turnStatus: "cancelled" }));
    expect(state.terminal).toBe(true);
    expect(shouldFireQueued(state)).toBe(false);
  });

  it("carries commentary/final phase onto assistant messages", () => {
    const ctx = {
      pendingUser: null,
      thinkingStreamed: false,
      createID: () => "m1",
      timestamp: "2026-11-17T00:00:00Z",
    };
    let messages = reduceChatMessages([], base({
      kind: "message", author: "助手", text: "我先检查。", phase: "commentary",
    }), ctx);
    expect(messages[0]).toMatchObject({ kind: "assistant", phase: "commentary" });
    messages = reduceChatMessages(messages, base({
      kind: "message", author: "助手", text: "已修复。", phase: "final",
    }), { ...ctx, createID: () => "m2" });
    expect(messages[messages.length - 1]).toMatchObject({ kind: "assistant", phase: "final" });
  });

  it("reduces optimistic users, tool pairs, confirmations and retries in one message reducer", () => {
    let seq = 0;
    const ctx = {
      pendingUser: null,
      thinkingStreamed: false,
      createID: () => `m${++seq}`,
      timestamp: "2026-11-17T00:00:00Z",
    };
    const pending: UIMessage = {
      id: "u1", kind: "user", author: "user", text: "临时",
    };
    let messages = reduceChatMessages([], base({
      kind: "user", text: "正式", attachments: null, reasoning: "high",
    }), { ...ctx, pendingUser: {
      message: pending, confirmed: false, confirmedFromHistory: false,
    }, pendingUserID: "u1" });
    expect(messages).toHaveLength(1);
    expect(messages[0].id).toBe("u1");
    expect(messages[0].text).toBe("正式");

    messages = reduceChatMessages(messages, base({
      kind: "tool_call", author: "worker", toolName: "read_workspace_file",
      toolArgs: { path: "a.txt" }, callId: "call_1",
    }), ctx);
    messages = reduceChatMessages(messages, base({
      kind: "tool_result", author: "worker", toolName: "read_workspace_file",
      toolResp: { ok: true }, callId: "call_1",
    }), ctx);
    messages = reduceChatMessages(messages, base({
      kind: "confirm_request", author: "worker", toolName: "write_workspace_file",
      callId: "call_2", hint: "写入", risk: "medium", diff: "+a", policy: "沙箱=workspace-write",
    }), ctx);
    messages = reduceChatMessages(messages, base({
      kind: "retry", retry: { stepId: "step-2", attempt: 2 },
    }), ctx);
    expect(messages.map((m) => m.kind)).toEqual([
      "user", "tool_call", "tool_result", "confirm", "warning",
    ]);
    expect(messages[2].callId).toBe(messages[1].callId);
    expect(messages[3].risk).toBe("medium");
    expect(messages[3].diff).toBe("+a");
    expect(messages[4].text).toContain("step-2");
  });

  it("closes streaming messages on done and replaces a streamed thinking block", () => {
    let seq = 0;
    const ctx = {
      pendingUser: null,
      thinkingStreamed: false,
      createID: () => `m${++seq}`,
      timestamp: "2026-11-17T00:00:00Z",
    };
    const streamedThinking: UIMessage = {
      id: "m1", kind: "thinking", author: "a1", text: "草稿", streaming: true,
    };
    const streamedReply: UIMessage = {
      id: "m2", kind: "assistant", author: "a1", text: "回答", streaming: true,
    };
    let messages = reduceChatMessages([streamedThinking, streamedReply], base({
      kind: "thinking", author: "a1", text: "最终思考",
    }), { ...ctx, thinkingStreamed: true });
    expect(messages[0].text).toBe("最终思考");
    expect(messages[0].streaming).toBe(false);
    messages = reduceChatMessages(messages, base({ kind: "done" }), ctx);
    expect(messages.every((m) => !m.streaming)).toBe(true);
  });
});
