import { describe, expect, it } from "vitest";
import type { Settings } from "@/lib/api";
import {
  DEFAULT_SETTINGS_TAB,
  SETTINGS_GROUPS,
  SETTINGS_TABS,
  a2aAutoStartError,
  executionPolicyFromLegacySandboxMode,
  isConfigSettingsTab,
  isSettingsDirty,
  normalizeApprovalPolicy,
  normalizeSandboxProfile,
  normalizeSandboxMode,
  resolveExecutionPolicy,
  resolveSettingsTab,
  settingsTabsInGroup,
} from "./settingsModel";

const baseSettings = {
  workspaceDir: "/tmp/agentmate-workspace",
  language: "zh-CN",
  themeMode: "dark",
  themePreset: "nord",
  fontScale: "md",
  chatWidth: "medium",
  autoSpeak: false,
  autoTitle: true,
  autoRemember: false,
  toolsAudit: false,
  a2aAutoStart: false,
  a2aAgentId: "",
  a2aPort: 0,
  httpProxy: "",
  compactionInterval: 0,
  compactionContextPercent: 65,
} as Settings;

describe("settings model", () => {
  it("normalizes approval and sandbox profile axes independently", () => {
    expect(normalizeApprovalPolicy(undefined)).toBe("request-approval");
    expect(normalizeApprovalPolicy("request")).toBe("request-approval");
    expect(normalizeApprovalPolicy("workspace-write")).toBe("request-approval");
    expect(normalizeApprovalPolicy("auto")).toBe("auto-approve");
    expect(normalizeApprovalPolicy("auto-approve")).toBe("auto-approve");
    expect(normalizeApprovalPolicy("never")).toBe("never");
    expect(normalizeApprovalPolicy("danger-full-access")).toBe("never");
    expect(normalizeApprovalPolicy("unknown")).toBe("request-approval");

    expect(normalizeSandboxProfile(undefined)).toBe("workspace-write");
    expect(normalizeSandboxProfile("request-approval")).toBe("workspace-write");
    expect(normalizeSandboxProfile("auto-approve")).toBe("workspace-write");
    expect(normalizeSandboxProfile("workspace-write")).toBe("workspace-write");
    expect(normalizeSandboxProfile("read-only")).toBe("read-only");
    expect(normalizeSandboxProfile("  READ-ONLY ")).toBe("read-only");
    expect(normalizeSandboxProfile("danger-full-access")).toBe("danger-full-access");
    expect(normalizeSandboxProfile("unknown")).toBe("workspace-write");
  });

  it("normalizes legacy combined sandbox modes only for migration", () => {
    expect(normalizeSandboxMode(undefined)).toBe("request-approval");
    expect(normalizeSandboxMode("workspace-write")).toBe("request-approval");
    expect(normalizeSandboxMode("request")).toBe("request-approval");
    expect(normalizeSandboxMode("auto")).toBe("auto-approve");
    expect(normalizeSandboxMode("auto-approve")).toBe("auto-approve");
    expect(normalizeSandboxMode("danger-full-access")).toBe("danger-full-access");
    expect(normalizeSandboxMode("unknown")).toBe("request-approval");
  });

  it("maps legacy profiles and resolves the split pair with backend precedence", () => {
    expect(executionPolicyFromLegacySandboxMode("request-approval")).toEqual({
      approvalPolicy: "request-approval",
      sandboxProfile: "workspace-write",
    });
    expect(executionPolicyFromLegacySandboxMode("auto-approve")).toEqual({
      approvalPolicy: "auto-approve",
      sandboxProfile: "workspace-write",
    });
    expect(executionPolicyFromLegacySandboxMode("danger-full-access")).toEqual({
      approvalPolicy: "never",
      sandboxProfile: "danger-full-access",
    });
    expect(executionPolicyFromLegacySandboxMode("unknown")).toEqual({
      approvalPolicy: "request-approval",
      sandboxProfile: "workspace-write",
    });

    expect(resolveExecutionPolicy(undefined, undefined, "auto-approve")).toEqual({
      approvalPolicy: "auto-approve",
      sandboxProfile: "workspace-write",
    });
    expect(resolveExecutionPolicy("never", "workspace-write", "danger-full-access")).toEqual({
      approvalPolicy: "never",
      sandboxProfile: "workspace-write",
    });
    expect(resolveExecutionPolicy("auto-approve", undefined, "danger-full-access")).toEqual({
      approvalPolicy: "auto-approve",
      sandboxProfile: "danger-full-access",
    });
    expect(resolveExecutionPolicy("request-approval", "read-only", "danger-full-access")).toEqual({
      approvalPolicy: "request-approval",
      sandboxProfile: "read-only",
    });
    expect(resolveExecutionPolicy(undefined, undefined, undefined, {
      approvalPolicy: "auto-approve",
      sandboxProfile: "danger-full-access",
    })).toEqual({
      approvalPolicy: "auto-approve",
      sandboxProfile: "danger-full-access",
    });
  });

  it("defines the eleven tabs in navigation order with no General tab", () => {
    expect(SETTINGS_TABS.map((tab) => tab.key)).toEqual([
      "appearance",
      "conversation",
      "models",
      "network",
      "agents",
      "teams",
      "skills",
      "mcp",
      "knowledge",
      "data",
      "about",
    ]);
    expect(SETTINGS_TABS.map((tab) => tab.label)).toEqual([
      "外观与交互",
      "对话与上下文",
      "模型供应商",
      "网络与服务",
      "Agents",
      "Agent 团队",
      "Skills",
      "MCP 服务",
      "知识库",
      "数据与存储",
      "关于",
    ]);
    expect(SETTINGS_TABS.some((tab) => tab.key === ("general" as never))).toBe(false);
  });

  it("treats an absent terminal config and its auto default as the same draft", () => {
    const saved = { ...baseSettings } as Settings;
    const draft = { ...saved, terminal: { mode: "auto" } };
    expect(isSettingsDirty(saved, draft)).toBe(false);
    draft.terminal = { ...draft.terminal, mode: "cmd" };
    expect(isSettingsDirty(saved, draft)).toBe(true);
  });

  it("groups the sidebar into basic, capability, and system sections", () => {
    expect(SETTINGS_GROUPS.map((group) => group.label)).toEqual([
      "基础设置",
      "Agent 能力",
      "数据与系统",
    ]);
    expect(settingsTabsInGroup("basic").map((tab) => tab.key)).toEqual([
      "appearance",
      "conversation",
      "models",
      "network",
    ]);
    expect(settingsTabsInGroup("capabilities").map((tab) => tab.key)).toEqual([
      "agents",
      "teams",
      "skills",
      "mcp",
    ]);
    expect(settingsTabsInGroup("system").map((tab) => tab.key)).toEqual([
      "knowledge",
      "data",
      "about",
    ]);
  });

  it("resolves the default tab and keeps the old general deep link working", () => {
    expect(DEFAULT_SETTINGS_TAB).toBe("appearance");
    expect(resolveSettingsTab()).toBe("appearance");
    expect(resolveSettingsTab("general")).toBe("appearance");
    expect(resolveSettingsTab("models")).toBe("models");
    expect(resolveSettingsTab("stale")).toBe("appearance");
  });

  it("classifies only the five shared-save tabs as config pages", () => {
    expect(SETTINGS_TABS.filter((tab) => tab.kind === "config").map((tab) => tab.key)).toEqual([
      "appearance",
      "conversation",
      "network",
      "knowledge",
      "data",
    ]);
    for (const tab of ["appearance", "conversation", "network", "knowledge", "data"] as const) {
      expect(isConfigSettingsTab(tab)).toBe(true);
    }
    for (const tab of ["models", "agents", "teams", "skills", "mcp", "about"] as const) {
      expect(isConfigSettingsTab(tab)).toBe(false);
    }
  });

  it("detects dirty drafts and clears when the value returns to the saved snapshot", () => {
    expect(isSettingsDirty(baseSettings, { ...baseSettings })).toBe(false);
    expect(isSettingsDirty(baseSettings, null)).toBe(false);
    expect(isSettingsDirty(null, baseSettings)).toBe(false);

    const changedTheme = { ...baseSettings, themePreset: "paper" };
    expect(isSettingsDirty(baseSettings, changedTheme)).toBe(true);

    const changedToggle = { ...baseSettings, autoRemember: true };
    expect(isSettingsDirty(baseSettings, changedToggle)).toBe(true);

    const changedNestedShape = {
      ...baseSettings,
      autoTitle: null,
    };
    // null 在 Go 契约中表示 "on"，因此不得将其判定为已修改。
    expect(isSettingsDirty(baseSettings, changedNestedShape)).toBe(false);
  });

  it("treats omitted omitempty fields as their persisted defaults", () => {
    const withDefaults = {
      ...baseSettings,
      autoTitle: true,
      a2aPort: 0,
      compactionInterval: 0,
      httpProxy: "",
      toolsAudit: false,
      approvalPolicy: "request-approval",
      sandboxProfile: "workspace-write",
      sandboxMode: "request-approval",
      sandboxNetwork: "off",
    } as Settings;
    const withoutOptional = { ...baseSettings } as Settings;
    delete (withoutOptional as Partial<Settings>).autoTitle;
    delete (withoutOptional as Partial<Settings>).a2aPort;
    delete (withoutOptional as Partial<Settings>).compactionInterval;
    delete (withoutOptional as Partial<Settings>).compactionContextPercent;
    delete (withoutOptional as Partial<Settings>).httpProxy;
    delete (withoutOptional as Partial<Settings>).toolsAudit;
    delete (withoutOptional as Partial<Settings>).approvalPolicy;
    delete (withoutOptional as Partial<Settings>).sandboxProfile;
    delete (withoutOptional as Partial<Settings>).sandboxMode;
    delete (withoutOptional as Partial<Settings>).sandboxNetwork;

    expect(isSettingsDirty(withoutOptional, withDefaults)).toBe(false);
    expect(isSettingsDirty(withDefaults, { ...withDefaults, a2aPort: 8080 })).toBe(true);
  });

  it("validates A2A auto-start only when it is enabled", () => {
    expect(a2aAutoStartError({ ...baseSettings, a2aAutoStart: false, a2aAgentId: "", a2aPort: 0 })).toBeNull();
    expect(a2aAutoStartError({ ...baseSettings, a2aAutoStart: true, a2aAgentId: "", a2aPort: 8080 }))
      .toBe("开启自动启动前请先选择要发布的 Agent");
    expect(a2aAutoStartError({ ...baseSettings, a2aAutoStart: true, a2aAgentId: "agent_1", a2aPort: 0 }))
      .toBe("端口需为 1-65535 的整数");
    expect(a2aAutoStartError({ ...baseSettings, a2aAutoStart: true, a2aAgentId: "agent_1", a2aPort: 70000 }))
      .toBe("端口需为 1-65535 的整数");
    expect(a2aAutoStartError({ ...baseSettings, a2aAutoStart: true, a2aAgentId: "agent_1", a2aPort: 8080 }))
      .toBeNull();
  });
});
