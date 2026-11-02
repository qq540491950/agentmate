import { describe, expect, it } from "vitest";
import { isDialogCancelError, workspaceLockReason } from "./workspace";

describe("isDialogCancelError", () => {
  it("treats the Wails picker cancellation as no selection", () => {
    expect(isDialogCancelError(
      new Error("Invalid dialog call: Dialog.OpenFile failed, error getting selection: cancelled by user"),
    )).toBe(true);
    expect(isDialogCancelError("cancelled by user")).toBe(true);
    expect(isDialogCancelError(new Error("canceled by user"))).toBe(true);
  });

  it("keeps real dialog failures as errors", () => {
    expect(isDialogCancelError(new Error("permission denied"))).toBe(false);
    expect(isDialogCancelError(undefined)).toBe(false);
    expect(isDialogCancelError({})).toBe(false);
  });
});

describe("workspaceLockReason", () => {
  it("prioritizes the started-turn lock", () => {
    expect(workspaceLockReason({ hasProject: true, started: true })).toBe("首轮已开始，任务工作区已锁定");
    expect(workspaceLockReason({ hasProject: false, started: true })).toBe("首轮已开始，任务工作区已锁定");
  });

  it("blocks project chats before the first turn", () => {
    expect(workspaceLockReason({ hasProject: true, started: false })).toContain("项目会话使用项目根目录");
  });

  it("allows an explicit directory before the first turn", () => {
    expect(workspaceLockReason({ hasProject: false, started: false })).toBe("");
  });
});