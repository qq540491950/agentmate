import { describe, expect, it } from "vitest";
import {
  filterSlashCommands,
  isSkillCommand,
  skillCommandName,
  skillCommandNameFor,
  slashCandidates,
  slashSourceLabel,
  type SlashCommand,
} from "./slashSkills";

const BUILTINS: SlashCommand[] = [
  { cmd: "/export", desc: "导出" },
  { cmd: "/new", desc: "新建" },
];
const SKILLS = [
  { name: "Weekly Report", description: "写周报" },
  { name: "翻译" },
  { name: "new" }, // collides with the built-in: the built-in must win
];

describe("skillCommandName", () => {
  it("mirrors agentkit.MaterializedSkillName", () => {
    expect(skillCommandName("Weekly Report")).toBe("weekly-report");
    expect(skillCommandName("weekly_report")).toBe("weekly-report");
    expect(skillCommandName("  --Hello__World-- ")).toBe("hello-world");
    expect(skillCommandName("翻译")).toBe("skill");
    expect(skillCommandName("")).toBe("skill");
  });
});

describe("slashCandidates", () => {
  it("lists built-ins first, then skills with their descriptions", () => {
    const out = slashCandidates(BUILTINS, SKILLS);
    expect(out.map((c) => c.cmd)).toEqual([
      "/export", "/new", "/weekly-report", "/skill",
    ]);
    expect(out[2].desc).toBe("写周报");
    expect(out[3].desc).toBe("技能");
  });

  it("keeps the built-in behavior when a skill has the same name", () => {
    const out = slashCandidates(BUILTINS, SKILLS);
    expect(out.filter((c) => c.cmd === "/new")).toHaveLength(1);
    expect(out.find((c) => c.cmd === "/new")?.desc).toBe("新建");
  });

  it("tags discovered skills with their source", () => {
    const out = slashCandidates(BUILTINS, [
      { id: "fs:project:weekly-report", name: "Weekly Report", description: "项目周报" },
      { id: "fs:global:style", name: "Style", description: "全局风格" },
      { id: "skill_app", name: "App Skill", description: "应用技能" },
    ]);
    expect(out.find((c) => c.cmd === "/weekly-report")?.source).toBe("project");
    expect(out.find((c) => c.cmd === "/style")?.source).toBe("global");
    expect(out.find((c) => c.cmd === "/app-skill")?.source).toBe("app");
    expect(slashSourceLabel("project")).toBe("项目");
    expect(slashSourceLabel("global")).toBe("全局");
    expect(slashSourceLabel("app")).toBe("应用内");
    expect(slashSourceLabel("builtin")).toBe("内置");
  });
});

describe("filterSlashCommands", () => {
  const all = slashCandidates(BUILTINS, SKILLS);

  it("filters by case-insensitive prefix", () => {
    expect(filterSlashCommands(all, "/EX")).toEqual([BUILTINS[0]]);
    expect(filterSlashCommands(all, "/week")).toEqual([
      { cmd: "/weekly-report", desc: "写周报", source: "app" },
    ]);
  });

  it("closes once the input moves past the command token", () => {
    expect(filterSlashCommands(all, "/export 会话")).toEqual([]);
    expect(filterSlashCommands(all, "export")).toEqual([]);
    expect(filterSlashCommands(all, "")).toEqual([]);
  });
});

describe("skill commands in the send path", () => {
  it("recognizes a skill token case-insensitively", () => {
    expect(skillCommandNameFor("/Weekly_Report 本周进展")).toBe("weekly-report");
    expect(isSkillCommand("/Weekly_Report 本周进展", SKILLS)).toBe(true);
    expect(isSkillCommand("/weekly-report", SKILLS)).toBe(true);
    expect(isSkillCommand("/翻译 你好", SKILLS)).toBe(true);
  });

  it("rejects built-ins, unknown tokens and non-commands", () => {
    expect(skillCommandNameFor("/new")).toBe("new");
    // "/new" 在这里也命名了一个技能，但发送路径会先检查内置项。
    expect(isSkillCommand("/nope", SKILLS)).toBe(false);
    expect(isSkillCommand("hello /weekly-report", SKILLS)).toBe(false);
    expect(isSkillCommand("/", SKILLS)).toBe(false);
  });
});