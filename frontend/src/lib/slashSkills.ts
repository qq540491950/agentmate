/** One entry of the composer's "/" suggestion popup. */
export type SlashCommand = { cmd: string; desc: string };

/** The configured-skill fields the slash menu needs (SkillConfig is wider). */
export type SkillSlashSource = { name: string; description?: string };

/**
 * skillCommandName mirrors agentkit.MaterializedSkillName: the lowercase,
 * hyphenated name ADK exposes for a skill. Commands must use exactly this
 * form, because the backend re-parses "/<name>" with the same normalization
 * (a skill named "Weekly Report" is invoked as "/weekly-report").
 */
export function skillCommandName(name: string): string {
  let out = "";
  let prevHyphen = true; // suppress leading hyphens
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
 * slashCandidates merges the built-in commands with the configured skills.
 * Built-ins win a name collision: they run locally and must keep that
 * behavior, while a skill of the same name could never be addressed anyway.
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
    out.push({ cmd, desc: skill.description?.trim() || "技能" });
  }
  return out;
}

/**
 * filterSlashCommands keeps the existing menu semantics: suggestions appear
 * only while the whole input still looks like one command token (no space
 * yet), and matching is a case-insensitive prefix match.
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
 * skillCommandNameFor extracts the materialized command name from the leading
 * "/<token>" of a message, or null when the message does not start with one.
 */
export function skillCommandNameFor(text: string): string | null {
  const m = /^\s*\/([^\s]+)/.exec(text);
  return m ? skillCommandName(m[1]) : null;
}

/** isSkillCommand reports whether the leading "/<token>" names a real skill. */
export function isSkillCommand(
  text: string,
  skills: readonly SkillSlashSource[],
): boolean {
  const cmd = skillCommandNameFor(text);
  if (cmd == null) return false;
  return skills.some((s) => skillCommandName(s.name) === cmd);
}