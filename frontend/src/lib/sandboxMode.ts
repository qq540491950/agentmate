import {
  normalizeApprovalPolicy,
  normalizeSandboxMode,
  normalizeSandboxProfile,
  type ApprovalPolicy,
  type ExecutionPolicy,
  type SandboxMode,
  type SandboxProfile,
} from "./settingsModel";

export type ApprovalPolicyOption = {
  value: ApprovalPolicy;
  label: string;
  shortLabel?: string;
  description: string;
  danger?: boolean;
};

export type SandboxProfileOption = {
  value: SandboxProfile;
  label: string;
  shortLabel?: string;
  description: string;
  danger?: boolean;
};

/** Shared approval labels/descriptions for Settings and the composer picker. */
export const APPROVAL_OPTIONS: ApprovalPolicyOption[] = [
  {
    value: "request-approval",
    label: "请求批准",
    description: "写文件和执行命令前始终询问；工作区外写入仍由沙箱拦截。",
  },
  {
    value: "auto-approve",
    label: "仅风险确认",
    shortLabel: "仅风险",
    description: "工作区内安全操作自动执行；删除、系统配置、凭据访问、发布等风险操作才询问。",
  },
  {
    value: "never",
    label: "从不询问",
    description: "不弹出确认；仍受工作区沙箱约束，越界写入会被拒绝。",
  },
];

/** Shared sandbox labels/descriptions for Settings and the composer picker. */
export const SANDBOX_PROFILE_OPTIONS: SandboxProfileOption[] = [
  {
    value: "workspace-write",
    label: "工作区可写",
    shortLabel: "工作区",
    description: "文件和命令限制在任务工作区内；越界写入由 OS 沙箱拦截。",
  },
  {
    value: "danger-full-access",
    label: "完全访问",
    description: "关闭 OS 沙箱，可访问任意文件；联网由完全访问隐含开启，仅用于可信任务。",
    danger: true,
  },
];

export function approvalPolicyOption(value?: string): ApprovalPolicyOption {
  const normalized = normalizeApprovalPolicy(value);
  return APPROVAL_OPTIONS.find((option) => option.value === normalized) ?? APPROVAL_OPTIONS[0];
}

export function approvalPolicyLabel(value?: string): string {
  return approvalPolicyOption(value).label;
}

export function approvalPolicyShortLabel(value?: string): string {
  const option = approvalPolicyOption(value);
  return option.shortLabel ?? option.label;
}

export function sandboxProfileOption(value?: string): SandboxProfileOption {
  const normalized = normalizeSandboxProfile(value);
  return SANDBOX_PROFILE_OPTIONS.find((option) => option.value === normalized) ?? SANDBOX_PROFILE_OPTIONS[0];
}

export function sandboxProfileLabel(value?: string): string {
  return sandboxProfileOption(value).label;
}

export function sandboxProfileShortLabel(value?: string): string {
  const option = sandboxProfileOption(value);
  return option.shortLabel ?? option.label;
}

/** Selecting full access always requires an explicit risk acknowledgement. */
export function sandboxNeedsRiskConfirmation(value?: string): boolean {
  return normalizeSandboxProfile(value) === "danger-full-access";
}

/** Explain the two cross-axis combinations that are easy to misread. */
export function executionPolicyHint(policy: ExecutionPolicy): string | null {
  const approval = normalizeApprovalPolicy(policy.approvalPolicy);
  const sandbox = normalizeSandboxProfile(policy.sandboxProfile);
  if (approval === "never" && sandbox === "workspace-write") {
    return "仍受工作区沙箱约束，只是不弹出确认；越界写入会被拒绝。";
  }
  if (approval === "request-approval" && sandbox === "danger-full-access") {
    return "每一步仍会请求批准，但完全访问已关闭沙箱；确认后终端可访问任意文件。";
  }
  return null;
}

/** @deprecated Use APPROVAL_OPTIONS and SANDBOX_PROFILE_OPTIONS. */
export type SandboxModeOption = {
  value: SandboxMode;
  label: string;
  shortLabel?: string;
  description: string;
  danger?: boolean;
};

/** @deprecated Legacy three-way metadata kept for older consumers/tests. */
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

/** @deprecated Use approvalPolicyOption. */
export function sandboxModeOption(mode?: string): SandboxModeOption {
  const normalized = normalizeSandboxMode(mode);
  return SANDBOX_MODE_OPTIONS.find((option) => option.value === normalized) ?? SANDBOX_MODE_OPTIONS[0];
}

/** @deprecated Use approvalPolicyLabel / sandboxProfileLabel. */
export function sandboxModeLabel(mode?: string): string {
  return sandboxModeOption(mode).label;
}

/** @deprecated Use approvalPolicyShortLabel / sandboxProfileShortLabel. */
export function sandboxModeShortLabel(mode?: string): string {
  const option = sandboxModeOption(mode);
  return option.shortLabel ?? option.label;
}
