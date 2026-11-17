import { describe, expect, it } from "vitest";
import {
  findProjectByRoot,
  isDialogCancelError,
  joinDisplayPath,
  pickedDirectoryValue,
  workspaceLockReason,
} from "./workspace";

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
    expect(pickedDirectoryValue("D:\\AgentMateWorkspace")).toBe("D:\\AgentMateWorkspace");
    expect(pickedDirectoryValue("/Users/you/Documents/AgentMateWorkspace")).toBe("/Users/you/Documents/AgentMateWorkspace");
  });

  it("treats cancellation and malformed results as no selection", () => {
    expect(pickedDirectoryValue("")).toBe("");
    expect(pickedDirectoryValue("   ")).toBe("");
    expect(pickedDirectoryValue(null)).toBe("");
    expect(pickedDirectoryValue(undefined)).toBe("");
    expect(pickedDirectoryValue({ path: "/tmp" })).toBe("");
  });
});

describe("joinDisplayPath", () => {
  it("keeps the separator style of the workspace root", () => {
    expect(joinDisplayPath("D:\\project\\agentmate", ".agents", "skills"))
      .toBe("D:\\project\\agentmate\\.agents\\skills");
    expect(joinDisplayPath("D:/project/agentmate/", ".agents", "skills"))
      .toBe("D:/project/agentmate/.agents/skills");
  });

  it("handles POSIX roots and terminal separators", () => {
    expect(joinDisplayPath("/srv/cloud/", ".agents", "skills"))
      .toBe("/srv/cloud/.agents/skills");
    expect(joinDisplayPath("C:\\", ".agents", "skills"))
      .toBe("C:\\.agents\\skills");
    expect(joinDisplayPath("/srv/cloud\\data", ".agents", "skills"))
      .toBe("/srv/cloud\\data/.agents/skills");
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

describe("findProjectByRoot", () => {
  const projects = [
    { id: "p1", name: "agentmate", rootDir: "D:\\Projects\\AgentMate\\" },
    { id: "p2", name: "cloud", rootDir: "/srv/cloud" },
  ];

  it("matches separators, trailing slashes and case", () => {
    expect(findProjectByRoot(projects, "d:/projects/agentmate")?.id).toBe("p1");
    expect(findProjectByRoot(projects, "/srv/cloud/")?.id).toBe("p2");
  });

  it("does not pick an unrelated or empty directory", () => {
    expect(findProjectByRoot(projects, "D:/Projects/agentmate-other")).toBeUndefined();
    expect(findProjectByRoot(projects, "   ")).toBeUndefined();
  });
});
