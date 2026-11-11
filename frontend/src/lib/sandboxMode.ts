import { normalizeSandboxMode, type SandboxMode } from "./settingsModel";

export type SandboxModeOption = {
  value: SandboxMode;
  label: string;
  shortLabel?: string;
  description: string;
  danger?: boolean;
};

/** Shared labels/descriptions for the Settings default and the composer picker. */
export const SANDBOX_MODE_OPTIONS: SandboxModeOption[] = [
  {
    value: "request-approval",
    label: "请求批准",
    description: "写文件和执行命令前始终询问；工作区外写入仍由沙箱拦截。",
  },
  {
    value: "auto-approve",
    label: "帮我批准",
    description: "工作区内安全操作自动执行；删除、系统配置、凭据访问、发布等风险操作才询问。",
  },
  {
    value: "danger-full-access",
    label: "完全访问权限",
    shortLabel: "完全访问",
    description: "不再询问并关闭 OS 沙箱；终端可访问互联网和任意文件，仅用于可信任务。",
    danger: true,
  },
];

export function sandboxModeOption(mode?: string): SandboxModeOption {
  const normalized = normalizeSandboxMode(mode);
  return SANDBOX_MODE_OPTIONS.find((option) => option.value === normalized) ?? SANDBOX_MODE_OPTIONS[0];
}

export function sandboxModeLabel(mode?: string): string {
  return sandboxModeOption(mode).label;
}

export function sandboxModeShortLabel(mode?: string): string {
  const option = sandboxModeOption(mode);
  return option.shortLabel ?? option.label;
}
