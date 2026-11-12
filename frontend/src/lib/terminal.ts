import type {
  DetectedTerminal,
  TerminalInventory,
  TerminalSettings,
} from "@/lib/api";

export type ResolvedTerminalSettings = Required<TerminalSettings>;

const MODE_LABELS: Record<string, string> = {
  auto: "自动（推荐）",
  pwsh: "PowerShell 7+",
  powershell: "Windows PowerShell 5.1",
  "git-bash": "Git Bash",
  cmd: "命令提示符（cmd.exe）",
  wsl: "WSL",
  bash: "bash",
  zsh: "zsh",
  sh: "sh",
  custom: "自定义路径",
};

const SOURCE_LABELS: Record<string, string> = {
  system: "系统安装",
  standard: "标准路径",
  path: "PATH",
  git: "Git 安装目录",
  SHELL: "登录 Shell",
  wsl: "WSL",
  "store-alias": "Store 别名",
  "private-runtime": "应用私有运行时",
  fallback: "最终兜底",
};

export function resolveTerminalSettings(
  settings?: TerminalSettings | null,
): ResolvedTerminalSettings {
  return {
    mode: settings?.mode?.trim() || "auto",
    wslDistro: settings?.wslDistro?.trim() || "",
    wslUser: settings?.wslUser?.trim() || "",
    wslShell: settings?.wslShell?.trim() || "",
    customPath: settings?.customPath?.trim() || "",
    loadProfile: !!settings?.loadProfile,
  };
}

export function terminalModeLabel(mode?: string | null): string {
  return MODE_LABELS[(mode ?? "").trim().toLowerCase()] ?? mode ?? "自动";
}

export function terminalSourceLabel(source?: string | null): string {
  return SOURCE_LABELS[source ?? ""] ?? source ?? "未知来源";
}

export function terminalSandboxLabel(sandbox?: string | null): string {
  switch (sandbox) {
    case "ok":
      return "沙箱兼容";
    case "requires-full-access":
      return "需完全访问";
    default:
      return "兼容性未知";
  }
}

export function terminalSandboxWarning(sandbox?: string | null): string {
  switch (sandbox) {
    case "requires-full-access":
      return "此 Shell 运行在 Windows AppContainer 之外，需要将会话权限设为完全访问。";
    case "unknown":
      return "此 Shell 在当前沙箱中的兼容性尚未确认；如果启动失败，请改用 PowerShell 或完全访问。";
    default:
      return "";
  }
}

export function currentDetectedTerminal(
  inventory: TerminalInventory | null | undefined,
  settings?: TerminalSettings | null,
): DetectedTerminal | null {
  if (!inventory) return null;
  const resolved = resolveTerminalSettings(settings);
  if (resolved.mode === "auto") {
    return inventory.auto?.path ? inventory.auto : null;
  }
  if (resolved.mode === "wsl") {
    const distro = resolved.wslDistro;
    return (inventory.wsl ?? []).find((item) =>
      distro ? item.wslDistro === distro : true
    ) ?? null;
  }
  return (inventory.shells ?? []).find((item) => item.mode === resolved.mode) ?? null;
}

export function terminalSettingsError(
  inventory: TerminalInventory | null | undefined,
  settings?: TerminalSettings | null,
): string | null {
  if (!inventory) return null;
  const resolved = resolveTerminalSettings(settings);
  if (resolved.mode === "auto") {
    return inventory.auto?.path ? null : "未检测到可用终端，请检查 PATH 或选择自定义路径。";
  }
  if (resolved.mode === "custom") {
    return resolved.customPath ? null : "请选择自定义 Shell 可执行文件。";
  }
  if (resolved.mode === "wsl") {
    const wsl = inventory.wsl ?? [];
    if (!wsl.length) return "未检测到 WSL 发行版，请先安装 WSL 或选择其他 Shell。";
    if (resolved.wslDistro && !wsl.some((item) => item.wslDistro === resolved.wslDistro)) {
      return `未检测到 WSL 发行版「${resolved.wslDistro}」，请重新检测。`;
    }
    return null;
  }
  return (inventory.shells ?? []).some((item) => item.mode === resolved.mode)
    ? null
    : `未检测到 ${terminalModeLabel(resolved.mode)}，请重新检测或选择自动。`;
}
