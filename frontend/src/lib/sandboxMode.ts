import {
  normalizeApprovalPolicy,
  normalizeSandboxMode,
  normalizeSandboxProfile,
  type ApprovalPolicy,
  type ExecutionPolicy,
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

/**
 * 权限配置是唯一面向用户的执行设置。每个配置由后端/API
 * 层展开为两个内部维度（审批 + 沙箱），与 Codex 的预设式权限选择器保持一致。
 */
export type PermissionProfile =
  | "request-approval"
  | "auto-approve"
  | "danger-full-access";

export type PermissionProfileOption = {
  value: PermissionProfile;
  label: string;
  shortLabel?: string;
  description: string;
  policy: ExecutionPolicy;
  danger?: boolean;
};

/** 用于设置页与输入区选择器的共享审批标签/描述。 */
export const APPROVAL_OPTIONS: ApprovalPolicyOption[] = [
  {
    value: "request-approval",
    label: "请求批准",
    description: "写文件和执行命令前始终询问；工作区外写入仍由沙箱拦截。",
  },
  {
    value: "auto-approve",
    label: "帮我批准",
    shortLabel: "帮我批准",
    description: "工作区内安全操作自动执行；删除、系统配置、凭据访问、发布等风险操作才询问。",
  },
  {
    value: "never",
    label: "从不询问",
    description: "不弹出确认；仍受工作区沙箱约束，越界写入会被拒绝。",
  },
];

/** 用于设置页与输入区选择器的共享沙箱标签/描述。 */
export const SANDBOX_PROFILE_OPTIONS: SandboxProfileOption[] = [
  {
    value: "workspace-write",
    label: "工作区可写",
    shortLabel: "工作区",
    description: "文件和命令限制在任务工作区内；越界写入由 OS 沙箱拦截。",
  },
  {
    value: "read-only",
    label: "只读",
    shortLabel: "只读",
    description: "终端只能读，不能写工作区；文件写入会被拒绝。",
  },
  {
    value: "danger-full-access",
    label: "完全访问",
    description: "关闭 OS 沙箱，可访问任意文件；联网由完全访问隐含开启，仅用于可信任务。",
    danger: true,
  },
];

/** 输入区与设置页中展示的三个 Codex 风格预设。 */
export const PERMISSION_PROFILE_OPTIONS: PermissionProfileOption[] = [
  {
    value: "request-approval",
    label: "请求批准",
    description: "写文件和执行命令前始终询问；工作区外写入仍由沙箱拦截。",
    policy: {
      approvalPolicy: "request-approval",
      sandboxProfile: "workspace-write",
    },
  },
  {
    value: "auto-approve",
    label: "帮我批准",
    shortLabel: "帮我批准",
    description: "工作区内安全操作自动执行；删除、系统配置、凭据访问、发布等风险操作才询问。",
    policy: {
      approvalPolicy: "auto-approve",
      sandboxProfile: "workspace-write",
    },
  },
  {
    value: "danger-full-access",
    label: "完全访问权限",
    shortLabel: "完全访问",
    description: "不再询问并关闭 OS 沙箱；终端可访问互联网和任意文件，仅用于可信任务。",
    policy: {
      approvalPolicy: "never",
      sandboxProfile: "danger-full-access",
    },
    danger: true,
  },
];

function normalizePermissionProfile(value?: string | null): PermissionProfile {
  switch ((value ?? "").trim().toLowerCase()) {
    case "auto":
    case "auto-approve":
      return "auto-approve";
    case "danger-full-access":
      return "danger-full-access";
    default:
      return "request-approval";
  }
}

export function permissionProfileOption(value?: string | null): PermissionProfileOption {
  const normalized = normalizePermissionProfile(value);
  return PERMISSION_PROFILE_OPTIONS.find((option) => option.value === normalized)
    ?? PERMISSION_PROFILE_OPTIONS[0];
}

export function permissionProfilePolicy(value: PermissionProfile): ExecutionPolicy {
  return { ...permissionProfileOption(value).policy };
}

/**
 * 仅对完全匹配的组合返回预设。自定义组合（例如 never + workspace-write）返回
 * null，且必须保持自定义；把它吸附到最近的预设可能静默削弱沙箱。
 */
export function permissionProfileForPolicy(policy: ExecutionPolicy): PermissionProfileOption | null {
  const approval = normalizeApprovalPolicy(policy.approvalPolicy);
  const sandbox = normalizeSandboxProfile(policy.sandboxProfile);
  return PERMISSION_PROFILE_OPTIONS.find((option) => (
    option.policy.approvalPolicy === approval
    && option.policy.sandboxProfile === sandbox
  )) ?? null;
}

export function permissionProfileShortLabel(value?: string | null): string {
  const option = permissionProfileOption(value);
  return option.shortLabel ?? option.label;
}

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

/** 选择完全访问权限始终需要明确的风险确认。 */
export function sandboxNeedsRiskConfirmation(value?: string): boolean {
  return normalizeSandboxProfile(value) === "danger-full-access";
}

/** 解释两种容易误读的跨轴组合。 */
export function executionPolicyHint(policy: ExecutionPolicy): string | null {
  const approval = normalizeApprovalPolicy(policy.approvalPolicy);
  const sandbox = normalizeSandboxProfile(policy.sandboxProfile);
  if (sandbox === "read-only") {
    return "只读沙箱：终端只能读，不能写工作区；审批策略仍独立，文件写入会被拒绝。";
  }
  if (approval === "never" && sandbox === "workspace-write") {
    return "仍受工作区沙箱约束，只是不弹出确认；越界写入会被拒绝。";
  }
  if (approval === "request-approval" && sandbox === "danger-full-access") {
    return "每一步仍会请求批准，但完全访问已关闭沙箱；确认后终端可访问任意文件。";
  }
  return null;
}

export const SANDBOX_UNAVAILABLE_MESSAGE =
  "当前平台未实现 OS 沙箱：工作区可写与只读会拒绝执行终端命令；只有完全访问可运行";

export type SandboxStatusLike = {
  available: boolean;
  backend?: string | null;
  error?: string | null;
};

export type SandboxStatusView = {
  title: string;
  detail: string;
  warning: boolean;
};

export function sandboxBackendLabel(backend?: string | null): string {
  switch ((backend ?? "").trim()) {
    case "windows-appcontainer":
      return "Windows AppContainer";
    case "unsupported":
      return "当前平台未实现";
    case "":
      return "未知后端";
    default:
      return (backend ?? "").trim();
  }
}

/** sandboxStatusSummary 是 AppService.SandboxStatus 的展示契约。 */
export function sandboxStatusSummary(status?: SandboxStatusLike | null): SandboxStatusView {
  if (!status) {
    return {
      title: "本地工具沙箱",
      detail: "正在检查当前平台的 OS 沙箱后端…",
      warning: false,
    };
  }
  if (status.available) {
    return {
      title: `本地工具沙箱 · ${sandboxBackendLabel(status.backend)}`,
      detail: "终端与文件工具由当前平台的 OS 沙箱强制执行。",
      warning: false,
    };
  }
  return {
    title: "本地工具沙箱不可用",
    detail: SANDBOX_UNAVAILABLE_MESSAGE,
    warning: true,
  };
}

export type SandboxNetworkCopy = {
  label: string;
  description: string;
  note: string;
  checked: boolean;
  disabled: boolean;
  notice: string | null;
};

/** sandboxNetworkCopy 将 danger 隐含的网络状态排除在设置之外。 */
export function sandboxNetworkCopy(
  policy: ExecutionPolicy,
  persistedNetwork?: string | null,
): SandboxNetworkCopy {
  const approval = normalizeApprovalPolicy(policy.approvalPolicy);
  const dangerous = normalizeSandboxProfile(policy.sandboxProfile) === "danger-full-access";
  const notice = dangerous
    ? "联网由完全访问隐含开启；保存的联网偏好保持不变，切回其他沙箱后恢复。"
      + (approval === "never" ? " 当前“从不询问 + 完全访问”组合不会出现二次确认。" : "")
    : null;
  return {
    label: "允许联网（不限制目标）",
    description: dangerous
      ? "完全访问会隐含开启联网；这里不会改写已保存的联网偏好。"
      : "关闭时新会话的终端子进程默认断网；开启后目标不受限制。",
    note: "联网没有域名白名单（domain allowlist），也不隔离 localhost 或私网地址。",
    checked: dangerous || normalizeSandboxNetwork(persistedNetwork) === "on",
    disabled: dangerous,
    notice,
  };
}

function normalizeSandboxNetwork(value?: string | null): "on" | "off" {
  return (value ?? "").trim().toLowerCase() === "on" ? "on" : "off";
}

/** @deprecated 遗留别名；新 UI 使用 PermissionProfileOption。 */
export type SandboxModeOption = PermissionProfileOption;

/** @deprecated 遗留的三路元数据；新 UI 使用 PERMISSION_PROFILE_OPTIONS。 */
export const SANDBOX_MODE_OPTIONS: SandboxModeOption[] = PERMISSION_PROFILE_OPTIONS;

/** @deprecated 使用 approvalPolicyOption。 */
export function sandboxModeOption(mode?: string): SandboxModeOption {
  const normalized = normalizeSandboxMode(mode);
  return SANDBOX_MODE_OPTIONS.find((option) => option.value === normalized) ?? SANDBOX_MODE_OPTIONS[0];
}

/** @deprecated 使用 approvalPolicyLabel / sandboxProfileLabel。 */
export function sandboxModeLabel(mode?: string): string {
  return sandboxModeOption(mode).label;
}

/** @deprecated 使用 approvalPolicyShortLabel / sandboxProfileShortLabel。 */
export function sandboxModeShortLabel(mode?: string): string {
  const option = sandboxModeOption(mode);
  return option.shortLabel ?? option.label;
}
