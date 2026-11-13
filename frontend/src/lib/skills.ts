// 技能来源与 Go 发现层（internal/config/skills_discovery.go）保持一致：
// 应用技能位于 config.json 中，而项目/全局技能是从 <workspace>/.agents/skills
// 和 ~/.agents/skills 发现的只读条目。 后端将来源信息保留在 ID
// 前缀中，因此不需要额外的绑定字段。

export type SkillSource = "project" | "global" | "app";

export const PROJECT_SKILL_ID_PREFIX = "fs:project:";
export const GLOBAL_SKILL_ID_PREFIX = "fs:global:";

export function skillSource(id: string | null | undefined): SkillSource {
  const value = id ?? "";
  if (value.startsWith(PROJECT_SKILL_ID_PREFIX)) return "project";
  if (value.startsWith(GLOBAL_SKILL_ID_PREFIX)) return "global";
  return "app";
}

export function skillSourceLabel(source: SkillSource): string {
  switch (source) {
    case "project":
      return "项目";
    case "global":
      return "全局";
    default:
      return "应用内";
  }
}

/** 发现的技能是外部文件：应用仅读取它们。 */
export function isReadOnlySkill(id: string | null | undefined): boolean {
  return skillSource(id) !== "app";
}