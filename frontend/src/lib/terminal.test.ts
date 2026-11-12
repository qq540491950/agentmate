import { describe, expect, it } from "vitest";
import type { TerminalInventory } from "@/lib/api";
import {
  currentDetectedTerminal,
  resolveTerminalSettings,
  terminalModeLabel,
  terminalSandboxLabel,
  terminalSettingsError,
  terminalSourceLabel,
} from "./terminal";

const inventory = {
  os: "windows",
  auto: {
    mode: "auto",
    displayName: "自动（Git Bash）",
    path: "C:\\Program Files\\Git\\bin\\bash.exe",
    source: "git",
    sandbox: "unknown",
    autoEligible: true,
  },
  shells: [
    {
      mode: "git-bash",
      displayName: "Git Bash",
      path: "C:\\Program Files\\Git\\bin\\bash.exe",
      source: "git",
      sandbox: "unknown",
      autoEligible: true,
    },
    {
      mode: "cmd",
      displayName: "命令提示符（cmd.exe）",
      path: "C:\\Windows\\System32\\cmd.exe",
      source: "system",
      sandbox: "ok",
      autoEligible: true,
    },
  ],
  wsl: [
    {
      mode: "wsl",
      displayName: "WSL：Ubuntu-24.04",
      path: "C:\\Windows\\System32\\wsl.exe",
      source: "wsl",
      sandbox: "requires-full-access",
      autoEligible: false,
      wslDistro: "Ubuntu-24.04",
    },
  ],
  wslDistros: ["Ubuntu-24.04"],
} as TerminalInventory;

describe("terminal model", () => {
  it("defaults missing settings to auto and resolves the detected shell", () => {
    expect(resolveTerminalSettings(undefined)).toEqual({
      mode: "auto",
      wslDistro: "",
      wslUser: "",
      wslShell: "",
      customPath: "",
      loadProfile: false,
    });
    expect(currentDetectedTerminal(inventory, undefined)?.path).toContain("bash.exe");
  });

  it("resolves explicit and WSL selections", () => {
    expect(currentDetectedTerminal(inventory, { mode: "cmd" })?.mode).toBe("cmd");
    expect(currentDetectedTerminal(inventory, {
      mode: "wsl",
      wslDistro: "Ubuntu-24.04",
    })?.wslDistro).toBe("Ubuntu-24.04");
  });

  it("reports missing explicit shells instead of silently falling back", () => {
    expect(terminalSettingsError(inventory, { mode: "pwsh" })).toContain("PowerShell 7+");
    expect(terminalSettingsError(inventory, {
      mode: "wsl",
      wslDistro: "Debian",
    })).toContain("Debian");
    expect(terminalSettingsError(inventory, { mode: "custom" })).toContain("自定义");
  });

  it("keeps labels and sandbox copy in sync with the backend contract", () => {
    expect(terminalModeLabel("git-bash")).toBe("Git Bash");
    expect(terminalSourceLabel("private-runtime")).toBe("应用私有运行时");
    expect(terminalSandboxLabel("requires-full-access")).toBe("需完全访问");
  });
});
