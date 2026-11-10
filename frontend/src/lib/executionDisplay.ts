import type { ToolGroupStatus } from "@/lib/chatUtils";

/** ExecutionDisplayMode controls how much of a turn's process the chat shows. */
export type ExecutionDisplayMode = "auto" | "concise" | "detailed" | "none";

export const EXECUTION_DISPLAY_OPTIONS: {
  value: ExecutionDisplayMode;
  label: string;
  description: string;
}[] = [
  { value: "auto", label: "自动", description: "运行中展开，完成后收起" },
  { value: "concise", label: "简洁", description: "只显示一行执行摘要" },
  { value: "detailed", label: "详细", description: "展开并保留完整过程" },
  { value: "none", label: "无", description: "隐藏过程，仅保留必要确认" },
];

const MODES = new Set<ExecutionDisplayMode>(["auto", "concise", "detailed", "none"]);

/** parseExecutionDisplayMode returns null for an absent or stale value. */
function parseExecutionDisplayMode(value: string | null | undefined): ExecutionDisplayMode | null {
  const mode = (value ?? "").trim().toLowerCase();
  return MODES.has(mode as ExecutionDisplayMode) ? (mode as ExecutionDisplayMode) : null;
}

/** normalizeExecutionDisplayMode clamps a global setting to a renderable mode. */
export function normalizeExecutionDisplayMode(value: string | null | undefined): ExecutionDisplayMode {
  return parseExecutionDisplayMode(value) ?? "auto";
}

/** executionAutoExpanded is the mode's default open/closed policy. */
export function executionAutoExpanded(mode: ExecutionDisplayMode, status: ToolGroupStatus): boolean {
  switch (mode) {
    case "auto":
      return status === "running" || status === "confirm";
    case "concise":
      return status === "confirm";
    case "detailed":
      return true;
    case "none":
      return status === "confirm";
  }
}

/** executionHidden hides the whole process block except a pending confirmation. */
export function executionHidden(mode: ExecutionDisplayMode, status: ToolGroupStatus): boolean {
  return mode === "none" && status !== "confirm";
}
