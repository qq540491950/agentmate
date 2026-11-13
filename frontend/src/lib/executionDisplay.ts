import type { ToolGroupStatus } from "@/lib/chatUtils";

/** ExecutionDisplayMode 控制聊天显示一个回合过程的多少内容。 */
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

/** parseExecutionDisplayMode 对缺失或过期的值返回 null。 */
function parseExecutionDisplayMode(value: string | null | undefined): ExecutionDisplayMode | null {
  const mode = (value ?? "").trim().toLowerCase();
  return MODES.has(mode as ExecutionDisplayMode) ? (mode as ExecutionDisplayMode) : null;
}

/** normalizeExecutionDisplayMode 将全局设置限制为可渲染的模式。 */
export function normalizeExecutionDisplayMode(value: string | null | undefined): ExecutionDisplayMode {
  return parseExecutionDisplayMode(value) ?? "auto";
}

/** executionAutoExpanded 是该模式默认的展开/收起策略。 */
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

/** executionHidden 除待处理的确认外，隐藏整个过程块。 */
export function executionHidden(mode: ExecutionDisplayMode, status: ToolGroupStatus): boolean {
  return mode === "none" && status !== "confirm";
}
