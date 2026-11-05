import { describe, expect, it } from "vitest";
import { isReadOnlySkill, skillSource, skillSourceLabel } from "./skills";

describe("skillSource", () => {
  it("recognizes project and global discovered IDs", () => {
    expect(skillSource("fs:project:github-bug-relay")).toBe("project");
    expect(skillSource("fs:global:weekly-report")).toBe("global");
  });

  it("treats config-backed IDs as app skills", () => {
    expect(skillSource("skill-weekly-report")).toBe("app");
    expect(skillSource("skill_ab12cd34")).toBe("app");
    expect(skillSource("")).toBe("app");
    expect(skillSource(undefined)).toBe("app");
  });
});

describe("skillSourceLabel", () => {
  it("labels every source in Chinese", () => {
    expect(skillSourceLabel("project")).toBe("项目");
    expect(skillSourceLabel("global")).toBe("全局");
    expect(skillSourceLabel("app")).toBe("应用内");
  });
});

describe("isReadOnlySkill", () => {
  it("marks discovered skills read-only", () => {
    expect(isReadOnlySkill("fs:project:a")).toBe(true);
    expect(isReadOnlySkill("fs:global:b")).toBe(true);
    expect(isReadOnlySkill("skill-weekly-report")).toBe(false);
  });
});