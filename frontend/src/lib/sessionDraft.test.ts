import { describe, expect, it } from "vitest";
import { draftModelFor, draftUsesProject, shouldOpenLatestSession } from "./sessionDraft";

describe("shouldOpenLatestSession", () => {
  it("opens the newest session on first load", () => {
    expect(shouldOpenLatestSession({ activeID: "", draft: false, sessionCount: 3 })).toBe(true);
  });

  it("keeps an explicit new-chat draft blank (issue #30)", () => {
    // 点击「新聊天」不得被自动打开 effect
    // 撤销：空白输入区会保持空白，直到发送第一条消息。
    expect(shouldOpenLatestSession({ activeID: "", draft: true, sessionCount: 3 })).toBe(false);
    expect(shouldOpenLatestSession({ activeID: "", draft: true, sessionCount: 0 })).toBe(false);
  });

  it("never replaces an already open session", () => {
    expect(shouldOpenLatestSession({ activeID: "sess_1", draft: false, sessionCount: 3 })).toBe(false);
    expect(shouldOpenLatestSession({ activeID: "sess_1", draft: true, sessionCount: 1 })).toBe(false);
  });

  it("does nothing while the session list is empty", () => {
    expect(shouldOpenLatestSession({ activeID: "", draft: false, sessionCount: 0 })).toBe(false);
  });
});

describe("draftModelFor", () => {
  const models = [{ id: "model_a" }, { id: "model_b" }];

  it("shows the model picked before the session exists (issue #64)", () => {
    expect(draftModelFor(models, "model_b")?.id).toBe("model_b");
  });

  it("falls back to the provider default for an empty or stale pick", () => {
    expect(draftModelFor(models, "")?.id).toBe("model_a");
    // 从供应商中移除的模型不得使 chip 留空；后端会以相同方式钳制未知的覆盖项。
    expect(draftModelFor(models, "model_gone")?.id).toBe("model_a");
  });

  it("keeps the picker hidden when no provider is configured", () => {
    expect(draftModelFor([], "model_a")).toBeNull();
  });
});

describe("draftUsesProject", () => {
  it("binds a draft opened under a project to that project", () => {
    // 项目的「+」仅记录绑定；首次发送会通过 NewSessionWithProject
    // 创建会话（issue #60）。
    expect(draftUsesProject("proj_1")).toBe(true);
  });

  it("creates standalone task sessions for a global draft", () => {
    expect(draftUsesProject("")).toBe(false);
    expect(draftUsesProject("   ")).toBe(false);
  });
});