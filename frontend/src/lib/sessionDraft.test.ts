import { describe, expect, it } from "vitest";
import { draftModelFor, draftUsesProject, shouldOpenLatestSession } from "./sessionDraft";

describe("shouldOpenLatestSession", () => {
  it("opens the newest session on first load", () => {
    expect(shouldOpenLatestSession({ activeID: "", draft: false, sessionCount: 3 })).toBe(true);
  });

  it("keeps an explicit new-chat draft blank (issue #30)", () => {
    // Clicking「新聊天」must not be undone by the auto-open effect: the blank
    // composer stays until the first message is sent.
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
    // A model removed from the provider must not leave the chip blank; the
    // backend clamps an unknown override the same way.
    expect(draftModelFor(models, "model_gone")?.id).toBe("model_a");
  });

  it("keeps the picker hidden when no provider is configured", () => {
    expect(draftModelFor([], "model_a")).toBeNull();
  });
});

describe("draftUsesProject", () => {
  it("binds a draft opened under a project to that project", () => {
    // The project's「+」only records the binding; the first send creates the
    // session through NewSessionWithProject (issue #60).
    expect(draftUsesProject("proj_1")).toBe(true);
  });

  it("creates standalone task sessions for a global draft", () => {
    expect(draftUsesProject("")).toBe(false);
    expect(draftUsesProject("   ")).toBe(false);
  });
});