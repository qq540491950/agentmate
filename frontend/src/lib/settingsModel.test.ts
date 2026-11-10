import { describe, expect, it } from "vitest";
import type { Settings } from "@/lib/api";
import {
  DEFAULT_SETTINGS_TAB,
  SETTINGS_GROUPS,
  SETTINGS_TABS,
  a2aAutoStartError,
  isConfigSettingsTab,
  isSettingsDirty,
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
  compactionTokenThreshold: 0,
} as Settings;

describe("settings model", () => {
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
    // null means "on" in the Go contract, so it must not read as dirty.
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
    } as Settings;
    const withoutOptional = { ...baseSettings } as Settings;
    delete (withoutOptional as Partial<Settings>).autoTitle;
    delete (withoutOptional as Partial<Settings>).a2aPort;
    delete (withoutOptional as Partial<Settings>).compactionInterval;
    delete (withoutOptional as Partial<Settings>).httpProxy;
    delete (withoutOptional as Partial<Settings>).toolsAudit;

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
