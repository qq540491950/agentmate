import { describe, expect, it } from "vitest";
import {
  branchDisplayLabel,
  branchSwitchDisabledReason,
  dirtyFilesLabel,
  filterGitBranches,
} from "./gitBranch";

describe("filterGitBranches", () => {
  const branches = [{ name: "main" }, { name: "feature/login" }, { name: "lirt" }];

  it("filters locally and preserves list order", () => {
    expect(filterGitBranches(branches, "FEAT").map((b) => b.name)).toEqual(["feature/login"]);
    expect(filterGitBranches(branches, " ").map((b) => b.name)).toEqual(["main", "feature/login", "lirt"]);
  });

  it("returns an empty list for unmatched queries", () => {
    expect(filterGitBranches(branches, "missing")).toEqual([]);
  });
});

describe("branch labels", () => {
  it("formats dirty counts and detached HEAD", () => {
    expect(dirtyFilesLabel(3)).toBe("未提交：3 个文件");
    expect(dirtyFilesLabel(0)).toBe("");
    expect(dirtyFilesLabel(100)).toBe("未提交：99+ 个文件");
    expect(branchDisplayLabel("lirt")).toBe("lirt");
    expect(branchDisplayLabel("", true, "a1b2c3d")).toBe("分离 HEAD · a1b2c3d");
  });

  it("prioritizes server blocks and dirty worktree", () => {
    expect(branchSwitchDisabledReason({ canSwitch: false, blockedReason: "项目内有会话正在运行" }))
      .toBe("项目内有会话正在运行");
    expect(branchSwitchDisabledReason({ canSwitch: true, dirtyFiles: 2 }))
      .toBe("请先提交或暂存未提交改动");
    expect(branchSwitchDisabledReason({ canSwitch: true, dirtyFiles: 0 })).toBe("");
  });
});
