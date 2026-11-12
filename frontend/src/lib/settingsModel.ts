import type { Settings, TerminalSettings } from "@/lib/api";

export type ApprovalPolicy = "request-approval" | "auto-approve" | "never";
export type SandboxProfile = "workspace-write" | "read-only" | "danger-full-access";
// SandboxMode is the legacy combined profile. Keep it only for migration.
export type SandboxMode = "request-approval" | "auto-approve" | "danger-full-access";

export type ExecutionPolicy = {
  approvalPolicy: ApprovalPolicy;
  sandboxProfile: SandboxProfile;
};

export const DEFAULT_EXECUTION_POLICY: ExecutionPolicy = {
  approvalPolicy: "request-approval",
  sandboxProfile: "workspace-write",
};

/** normalizeApprovalPolicy mirrors config.NormalizeApprovalPolicy. */
export function normalizeApprovalPolicy(value?: string | null): ApprovalPolicy {
  switch ((value ?? "").trim().toLowerCase()) {
    case "never":
    case "danger-full-access":
      return "never";
    case "auto":
    case "auto-approve":
      return "auto-approve";
    case "request":
    case "request-approval":
    case "workspace-write":
    default:
      return "request-approval";
  }
}

/** normalizeSandboxProfile mirrors config.NormalizeSandboxProfile. */
export function normalizeSandboxProfile(value?: string | null): SandboxProfile {
  switch ((value ?? "").trim().toLowerCase()) {
    case "read-only":
      return "read-only";
    case "danger-full-access":
      return "danger-full-access";
    default:
      return "workspace-write";
  }
}

/**
 * normalizeSandboxMode is retained for legacy config/session migration only.
 * New UI state must use ApprovalPolicy and SandboxProfile independently.
 */
export function normalizeSandboxMode(value?: string): SandboxMode {
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

/** executionPolicyFromLegacySandboxMode maps one old three-way choice to a pair. */
export function executionPolicyFromLegacySandboxMode(value?: string | null): ExecutionPolicy {
  switch (normalizeSandboxMode(value ?? undefined)) {
    case "danger-full-access":
      return { approvalPolicy: "never", sandboxProfile: "danger-full-access" };
    case "auto-approve":
      return { approvalPolicy: "auto-approve", sandboxProfile: "workspace-write" };
    default:
      return { ...DEFAULT_EXECUTION_POLICY };
  }
}

/**
 * resolveExecutionPolicy mirrors the Go precedence contract: explicit split
 * fields win; missing split fields fall back to legacy sandboxMode; when no
 * snapshot exists at all, use the caller's fallback (for example the global
 * default for an old session).
 */
export function resolveExecutionPolicy(
  approvalPolicy?: string | null,
  sandboxProfile?: string | null,
  legacySandboxMode?: string | null,
  fallback: ExecutionPolicy = DEFAULT_EXECUTION_POLICY,
): ExecutionPolicy {
  const hasApproval = !!approvalPolicy?.trim();
  const hasSandbox = !!sandboxProfile?.trim();
  const hasLegacy = !!legacySandboxMode?.trim();
  if (!hasApproval && !hasSandbox && !hasLegacy) {
    return {
      approvalPolicy: normalizeApprovalPolicy(fallback.approvalPolicy),
      sandboxProfile: normalizeSandboxProfile(fallback.sandboxProfile),
    };
  }
  const legacy = executionPolicyFromLegacySandboxMode(legacySandboxMode);
  return {
    approvalPolicy: hasApproval
      ? normalizeApprovalPolicy(approvalPolicy)
      : legacy.approvalPolicy,
    sandboxProfile: hasSandbox
      ? normalizeSandboxProfile(sandboxProfile)
      : legacy.sandboxProfile,
  };
}

export type SettingsTab =
  | "appearance"
  | "conversation"
  | "network"
  | "models"
  | "agents"
  | "teams"
  | "skills"
  | "mcp"
  | "knowledge"
  | "data"
  | "about";

export type SettingsTabKind = "config" | "management" | "about";
export type SettingsGroup = "basic" | "capabilities" | "system";

export type SettingsTabDef = {
  key: SettingsTab;
  label: string;
  kind: SettingsTabKind;
  group: SettingsGroup;
};

export type SettingsGroupDef = {
  key: SettingsGroup;
  label: string;
};

export type SettingsLeaveGuard = (after: () => void) => void;

export const DEFAULT_SETTINGS_TAB: SettingsTab = "appearance";
export const DEFAULT_COMPACTION_CONTEXT_PERCENT = 65;

export const SETTINGS_GROUPS: readonly SettingsGroupDef[] = [
  { key: "basic", label: "基础设置" },
  { key: "capabilities", label: "Agent 能力" },
  { key: "system", label: "数据与系统" },
] as const;

export const SETTINGS_TABS: readonly SettingsTabDef[] = [
  { key: "appearance", label: "外观与交互", kind: "config", group: "basic" },
  { key: "conversation", label: "对话与上下文", kind: "config", group: "basic" },
  { key: "models", label: "模型供应商", kind: "management", group: "basic" },
  { key: "network", label: "网络与服务", kind: "config", group: "basic" },
  { key: "agents", label: "Agents", kind: "management", group: "capabilities" },
  { key: "teams", label: "Agent 团队", kind: "management", group: "capabilities" },
  { key: "skills", label: "Skills", kind: "management", group: "capabilities" },
  { key: "mcp", label: "MCP 服务", kind: "management", group: "capabilities" },
  { key: "knowledge", label: "知识库", kind: "config", group: "system" },
  { key: "data", label: "数据与存储", kind: "config", group: "system" },
  { key: "about", label: "关于", kind: "about", group: "system" },
] as const;

const SETTINGS_TAB_KEYS = new Set<string>(SETTINGS_TABS.map((tab) => tab.key));

/** resolveSettingsTab keeps internal deep links working after the old
 * "general" tab was removed. Unknown values fall back to the default page. */
export function resolveSettingsTab(tab?: string | null): SettingsTab {
  if (tab === "general") return DEFAULT_SETTINGS_TAB;
  if (tab && SETTINGS_TAB_KEYS.has(tab)) return tab as SettingsTab;
  return DEFAULT_SETTINGS_TAB;
}

export function isConfigSettingsTab(tab: SettingsTab): boolean {
  return SETTINGS_TABS.find((entry) => entry.key === tab)?.kind === "config";
}

export function settingsTabsInGroup(group: SettingsGroup): SettingsTabDef[] {
  return SETTINGS_TABS.filter((tab) => tab.group === group);
}

/**
 * Go persists optional fields with omitempty, so a value equal to its default
 * may be absent from the loaded snapshot while the draft carries it
 * explicitly. Comparing raw objects would then keep a reverted field dirty
 * forever. These defaults mirror the Go normalization contract.
 */
const COMPARE_DEFAULTS: Partial<Record<keyof Settings, unknown>> = {
  workspaceDir: "",
  language: "zh-CN",
  themeMode: "dark",
  themePreset: "nord",
  accent: "blue",
  fontScale: "md",
  chatWidth: "medium",
  autoSpeak: false,
  embeddingProviderId: "",
  embeddingModel: "",
  a2aAutoStart: false,
  a2aAgentId: "",
  a2aPort: 0,
  toolsAudit: false,
  approvalPolicy: "request-approval",
  sandboxProfile: "workspace-write",
  sandboxMode: "request-approval",
  sandboxNetwork: "off",
  autoRemember: false,
  autoTitle: true,
  titleProviderId: "",
  titleModel: "",
  httpProxy: "",
  compactionInterval: 0,
  compactionContextPercent: DEFAULT_COMPACTION_CONTEXT_PERCENT,
  sidebarCollapsed: false,
  sidebarGrouping: "time",
  executionDisplayMode: "auto",
  autoCheckUpdate: true,
  skippedVersion: "",
  terminal: {},
};

function terminalFingerprint(value: unknown): Record<string, unknown> {
  const terminal = (value && typeof value === "object" ? value : {}) as TerminalSettings;
  return {
    mode: terminal.mode?.trim() || "auto",
    wslDistro: terminal.wslDistro?.trim() || "",
    wslUser: terminal.wslUser?.trim() || "",
    wslShell: terminal.wslShell?.trim() || "",
    customPath: terminal.customPath?.trim() || "",
    loadProfile: !!terminal.loadProfile,
  };
}

function settingsFingerprint(settings: Settings): string {
  // Settings is a flat object of primitives and nullable primitives. Sorting
  // keys keeps the comparison stable even if a caller builds the draft with a
  // different property insertion order. Missing optional fields are filled
  // with their persisted defaults so "absent" and "explicit default" compare
  // equal.
  // Union both sides so a future Settings field is still compared even before
  // its default is added here.
  const keys = Array.from(new Set([...Object.keys(settings), ...Object.keys(COMPARE_DEFAULTS)])).sort();
  const values = keys.map((key) => {
    const value = settings[key as keyof Settings];
    const fallback = COMPARE_DEFAULTS[key as keyof Settings];
    const comparable = key === "terminal"
      ? terminalFingerprint(value)
      : value === undefined || value === null
        ? fallback
        : value;
    return [key, comparable];
  });
  return JSON.stringify(values);
}

export function isSettingsDirty(saved: Settings | null, draft: Settings | null): boolean {
  if (!saved || !draft) return false;
  return settingsFingerprint(saved) !== settingsFingerprint(draft);
}

export function a2aAutoStartError(
  settings: Pick<Settings, "a2aAutoStart" | "a2aAgentId" | "a2aPort">,
): string | null {
  if (!settings.a2aAutoStart) return null;
  if (!settings.a2aAgentId?.trim()) {
    return "开启自动启动前请先选择要发布的 Agent";
  }
  const port = Number(settings.a2aPort);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    return "端口需为 1-65535 的整数";
  }
  return null;
}
