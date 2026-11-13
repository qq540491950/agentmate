import { skillSource, type SkillSource } from "./skills";

/** "/" 条目来自哪里；内置项在本地运行，其余的是技能。 */
export type SlashSource = "builtin" | SkillSource;

/** 输入框的 "/" 建议弹窗中的一个条目。 */
export type SlashCommand = { cmd: string; desc: string; source?: SlashSource };

/** 斜杠菜单所需的已配置技能字段（SkillConfig 更宽泛）。 */
export type SkillSlashSource = { id?: string; name: string; description?: string };

/**
 * skillCommandName 与 agentkit.MaterializedSkillName 保持一致：ADK
 * 为技能暴露的小写、连字符名称。命令必须使用完全相同的这种形式，因为后端会使用
 * 相同的规范化方式重新解析 "/<name>"（名为 "Weekly Report" 的技能会以
 * "/weekly-report" 被调用）。
 */
export function skillCommandName(name: string): string {
  let out = "";
  let prevHyphen = true; // 抑制前导连字符
  for (const ch of name.toLowerCase()) {
    if ((ch >= "a" && ch <= "z") || (ch >= "0" && ch <= "9")) {
      out += ch;
      prevHyphen = false;
    } else if ((ch === "-" || ch === "_" || ch === " ") && !prevHyphen) {
      out += "-";
      prevHyphen = true;
    }
  }
  out = out.replace(/-$/, "");
  return out || "skill";
}

/**
 * slashCandidates
 * 将内置命令与已配置技能合并。名称冲突时内置命令胜出：它们在本地运行，必须保持
 * 该行为，而同名技能无论如何都绝不可能被寻址。
 */
export function slashCandidates(
  builtins: readonly SlashCommand[],
  skills: readonly SkillSlashSource[],
): SlashCommand[] {
  const out: SlashCommand[] = [...builtins];
  const seen = new Set(out.map((c) => c.cmd));
  for (const skill of skills) {
    const cmd = "/" + skillCommandName(skill.name);
    if (seen.has(cmd)) continue;
    seen.add(cmd);
    out.push({ cmd, desc: skill.description?.trim() || "技能", source: skillSource(skill.id) });
  }
  return out;
}

/** slashSourceLabel 渲染命令的来源。 */
export function slashSourceLabel(source: SlashSource | undefined): string {
  switch (source) {
    case "project":
      return "项目";
    case "global":
      return "全局";
    case "app":
      return "应用内";
    default:
      return "内置";
  }
}

/**
 * filterSlashCommands
 * 保留现有菜单语义：建议仅在整段输入仍看起来像一个命令词元（尚无空格）时出现，
 * 并且匹配是不区分大小写的前缀匹配。
 */
export function filterSlashCommands(
  candidates: readonly SlashCommand[],
  input: string,
): SlashCommand[] {
  if (!input.startsWith("/")) return [];
  const q = input.toLowerCase();
  return candidates.filter((c) => c.cmd.startsWith(q));
}

/**
 * skillCommandNameFor 从消息开头的 "/<token>"
 * 中提取物化后的命令名，若消息不以它开头则返回 null。
 */
export function skillCommandNameFor(text: string): string | null {
  const m = /^\s*\/([^\s]+)/.exec(text);
  return m ? skillCommandName(m[1]) : null;
}

/** isSkillCommand 报告开头的 "/<token>" 是否指向真实技能。 */
export function isSkillCommand(
  text: string,
  skills: readonly SkillSlashSource[],
): boolean {
  const cmd = skillCommandNameFor(text);
  if (cmd == null) return false;
  return skills.some((s) => skillCommandName(s.name) === cmd);
}