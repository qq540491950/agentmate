import { describe, expect, it } from "vitest";
import { isDialogCancelError, pickedDirectoryValue, workspaceLockReason } from "./workspace";

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

describe("pickedDirectoryValue", () => {
  it("keeps the absolute path returned by the native picker", () => {
    expect(pickedDirectoryValue("D:\\WorkBuddyWorkspace")).toBe("D:\\WorkBuddyWorkspace");
    expect(pickedDirectoryValue("/Users/you/Documents/WorkBuddyWorkspace")).toBe("/Users/you/Documents/WorkBuddyWorkspace");
  });

  it("treats cancellation and malformed results as no selection", () => {
    expect(pickedDirectoryValue("")).toBe("");
    expect(pickedDirectoryValue("   ")).toBe("");
    expect(pickedDirectoryValue(null)).toBe("");
    expect(pickedDirectoryValue(undefined)).toBe("");
    expect(pickedDirectoryValue({ path: "/tmp" })).toBe("");
  });
});

describe("workspaceLockReason", () => {
  it("prioritizes the started-turn lock", () => {
    expect(workspaceLockReason({ hasProject: true, started: true })).toBe("首轮已开始，任务工作区已锁定");
    expect(workspaceLockReason({ hasProject: false, started: true })).toBe("首轮已开始，任务工作区已锁定");
  });

  it("blocks project chats before the first turn", () => {
    expect(workspaceLockReason({ hasProject: true, started: false })).toBe("项目会话使用项目根目录，不能单独指定工作区");
  });

  it("allows an explicit directory before the first turn", () => {
    expect(workspaceLockReason({ hasProject: false, started: false })).toBe("");
  });
});