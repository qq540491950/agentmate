import { describe, expect, it } from "vitest";
import { shouldOpenLatestSession } from "./sessionDraft";

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