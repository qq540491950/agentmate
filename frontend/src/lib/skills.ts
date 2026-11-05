// Skill sources mirror the Go discovery layer (internal/config/skills_discovery.go):
// app skills live in config.json, while project/global skills are read-only
// entries discovered from <workspace>/.agents/skills and ~/.agents/skills.
// The backend keeps the source in the ID prefix so no extra binding field is
// needed.

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

/** Discovered skills are external files: the app only reads them. */
export function isReadOnlySkill(id: string | null | undefined): boolean {
  return skillSource(id) !== "app";
}