import { useCallback, useEffect, useRef, useState } from "react";
import {
  ArrowLeft, BookOpen, Bot, Boxes, Database, FolderOpen, Info,
  MessageSquare, Network, Palette, Plug, RefreshCw, ShieldCheck, Sparkles, TriangleAlert,
  Users, type LucideIcon,
} from "lucide-react";
import { Browser, Dialogs } from "@wailsio/runtime";
import { toast } from "sonner";
import { AgentsPage } from "@/pages/AgentsPage";
import { TeamsPage } from "@/pages/TeamsPage";
import { SkillsPage } from "@/pages/SkillsPage";
import { MCPPage } from "@/pages/MCPPage";
import { ModelsPage } from "@/pages/ModelsPage";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { NumericField } from "@/components/ui/numeric-field";
import { SegmentedControl } from "@/components/ui/choice";
import { PolicyChoiceGroup } from "@/components/PolicyChoiceGroup";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { ArtifactPreview } from "@/components/ToolCallCard";
import { UpdateCard } from "@/components/UpdateCard";
import { ThemeChoice } from "@/components/ThemeChoice";
import { cn } from "@/lib/utils";
import { SettingRow } from "@/components/layout/PageShell";
import { buildIssueReportURL } from "@/lib/issue";
import {
  CHAT_WIDTHS,
  FONT_SCALES,
  THEME_MODES,
  applyTheme,
  cacheTheme,
  prefsFromSettings,
  type ThemePrefs,
} from "@/lib/theme";
import {
  EXECUTION_DISPLAY_OPTIONS, normalizeExecutionDisplayMode,
} from "@/lib/executionDisplay";
import { isDialogCancelError, pickedDirectoryValue } from "@/lib/workspace";
import { modelLabel } from "@/lib/modelLabel";
import {
  currentDetectedTerminal,
  resolveTerminalSettings,
  terminalModeLabel,
  terminalSandboxWarning,
  terminalSettingsError,
} from "@/lib/terminal";
import {
  PERMISSION_PROFILE_OPTIONS,
  approvalPolicyLabel,
  executionPolicyHint,
  permissionProfileForPolicy,
  permissionProfilePolicy,
  sandboxNetworkCopy,
  sandboxProfileLabel,
  sandboxStatusSummary,
  type PermissionProfile,
} from "@/lib/sandboxMode";
import {
  DEFAULT_COMPACTION_CONTEXT_PERCENT,
  SETTINGS_GROUPS,
  a2aAutoStartError,
  isConfigSettingsTab,
  isSettingsDirty,
  resolveExecutionPolicy,
  resolveSettingsTab,
  settingsTabsInGroup,
  type ExecutionPolicy,
  type SettingsLeaveGuard,
  type SettingsTab,
} from "@/lib/settingsModel";
import { Chat, App, A2A, Config, Update, getSandboxStatus, type AgentConfig, type A2AStatus, type KnowledgeFile, type KnowledgeIndexStats, type ModelProvider, type SandboxStatusInfo, type Settings, type StorageInfo, type MemoryInfo, type TerminalInventory, errText, Protocol } from "@/lib/api";

export type { SettingsTab } from "@/lib/settingsModel";

const SETTINGS_ICONS: Record<SettingsTab, LucideIcon> = {
  appearance: Palette,
  conversation: MessageSquare,
  models: Boxes,
  network: Network,
  agents: Bot,
  teams: Users,
  skills: Sparkles,
  mcp: Plug,
  knowledge: BookOpen,
  data: Database,
  about: Info,
};

export function SettingsPage({
  initialTab,
  onClose,
  version,
  onAppearanceChange,
  onRegisterLeaveGuard,
}: {
  initialTab?: SettingsTab;
  onClose?: () => void;
  version?: string;
  onAppearanceChange?: (prefs: ThemePrefs) => void;
  onRegisterLeaveGuard?: (guard: SettingsLeaveGuard | null) => void;
} = {}) {
  const [tab, setTab] = useState<SettingsTab>(() => resolveSettingsTab(initialTab));
  const [settings, setSettings] = useState<Settings | null>(null);
  const [savedSettings, setSavedSettings] = useState<Settings | null>(null);
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [agents, setAgents] = useState<AgentConfig[]>([]);
  const [saving, setSaving] = useState(false);
  const [leaveDialogOpen, setLeaveDialogOpen] = useState(false);
  const [pendingLeave, setPendingLeave] = useState<(() => void) | null>(null);
  const [pendingPolicy, setPendingPolicy] = useState<ExecutionPolicy | null>(null);
  const importRef = useRef<HTMLInputElement>(null);
  const [logOpen, setLogOpen] = useState(false);
  const [logText, setLogText] = useState("");
  const [logTitle, setLogTitle] = useState("应用日志");
  const [storage, setStorage] = useState<StorageInfo | null>(null);
  const [memoryInfo, setMemoryInfo] = useState<MemoryInfo | null>(null);
  const [sandboxStatus, setSandboxStatus] = useState<SandboxStatusInfo | null>(null);
  const [sandboxStatusError, setSandboxStatusError] = useState("");
  const [terminalInventory, setTerminalInventory] = useState<TerminalInventory | null>(null);
  const [terminalLoading, setTerminalLoading] = useState(false);
  const [terminalError, setTerminalError] = useState("");
  const settingsLoaded = settings !== null;
  const [a2aStatuses, setA2AStatuses] = useState<A2AStatus[]>([]);
  const [indexStats, setIndexStats] = useState<KnowledgeIndexStats | null>(null);
  const [kbTestQuery, setKbTestQuery] = useState("");
  const [kbTestResult, setKbTestResult] = useState("");
  const [kbFiles, setKbFiles] = useState<KnowledgeFile[]>([]);
  const [kbPreviewPath, setKbPreviewPath] = useState<string | null>(null);
  const [kbNewName, setKbNewName] = useState("");
  const [kbNewContent, setKbNewContent] = useState("");
  const dirtyRef = useRef(false);
  const numericValidity = useRef<Record<string, boolean>>({});
  const reportNumericValidity = useCallback((key: string, valid: boolean) => {
    numericValidity.current[key] = valid;
  }, []);

  // Deep links (empty state, @ popup, model selector) reopen the hub on a
  // specific tab; keep the active tab in sync when the prop changes.
  useEffect(() => {
    if (initialTab) setTab(resolveSettingsTab(initialTab));
  }, [initialTab]);
  const reload = useCallback(() => {
    Config.GetConfig()
      .then((c) => {
        const next = c.settings ?? null;
        setSettings(next);
        setSavedSettings(next);
        setProviders(c.providers ?? []);
        setAgents(c.agents ?? []);
      })
      .catch((e) => toast.error(errText(e)));
  }, []);
  useEffect(reload, [reload]);
  const reloadTerminals = useCallback((sandboxProfile = "workspace-write") => {
    setTerminalLoading(true);
    setTerminalError("");
    Config.DetectTerminals(sandboxProfile)
      .then((inventory) => setTerminalInventory(inventory ?? null))
      .catch((e) => setTerminalError(errText(e)))
      .finally(() => setTerminalLoading(false));
  }, []);
  useEffect(() => {
    if (!settings) return;
    reloadTerminals(settings.sandboxProfile || "workspace-write");
  }, [reloadTerminals, settingsLoaded, settings?.sandboxProfile]);
  useEffect(() => {
    A2A.Status().then((sts) => setA2AStatuses(sts ?? [])).catch(() => {});
    Config.KnowledgeIndexStats().then(setIndexStats).catch(() => {});
    Config.ListKnowledgeFiles().then((fs) => setKbFiles(fs ?? [])).catch(() => {});
    App.StorageStats().then(setStorage).catch(() => {});
    App.MemoryStats().then(setMemoryInfo).catch(() => {});
    getSandboxStatus()
      .then((status) => setSandboxStatus(status))
      .catch((error) => setSandboxStatusError(errText(error)));
  }, []);

  const update = (patch: Partial<Settings>) => {
    if (!settings) return;
    const next = { ...settings, ...patch };
    setSettings(next);
    // Appearance is the only group that previews before save. Persisting and
    // caching happen in save(); this keeps the saved snapshot authoritative.
    if ("themePreset" in patch || "themeMode" in patch || "fontScale" in patch || "chatWidth" in patch) {
      const prefs = prefsFromSettings(next);
      applyTheme(prefs);
      onAppearanceChange?.(prefs);
    }
  };

  const defaultExecutionPolicy = resolveExecutionPolicy(
    settings?.approvalPolicy,
    settings?.sandboxProfile,
    settings?.sandboxMode,
  );
  const defaultProfile = permissionProfileForPolicy(defaultExecutionPolicy);
  const terminalSettings = resolveTerminalSettings(settings?.terminal);
  const terminalCurrent = currentDetectedTerminal(terminalInventory, settings?.terminal);
  const terminalConfigError = terminalSettingsError(terminalInventory, settings?.terminal);
  const terminalSandboxCopy = terminalSandboxWarning(terminalCurrent?.sandbox);
  const terminalShells = terminalInventory?.shells ?? [];
  const terminalWSL = terminalInventory?.wsl ?? [];
  const terminalKnownModes = new Set([
    "auto",
    "wsl",
    "custom",
    ...terminalShells.map((shell) => shell.mode),
  ]);
  const terminalAutoLabel = terminalInventory?.auto?.displayName?.replace(/^自动（|）$/g, "")
    || (terminalLoading ? "检测中…" : "未检测到");

  const updateDefaultPolicy = (next: ExecutionPolicy) => {
    update({ approvalPolicy: next.approvalPolicy, sandboxProfile: next.sandboxProfile });
  };

  const updateTerminal = (patch: Partial<typeof terminalSettings>) => {
    update({ terminal: { ...terminalSettings, ...patch } });
  };

  const updateTerminalMode = (mode: string) => {
    const patch: Partial<typeof terminalSettings> = { mode };
    if (mode === "wsl" && !terminalSettings.wslDistro && terminalWSL[0]?.wslDistro) {
      patch.wslDistro = terminalWSL[0].wslDistro;
    }
    updateTerminal(patch);
  };

  const pickTerminalPath = async () => {
    try {
      const picked = pickedDirectoryValue(await Dialogs.OpenFile({
        CanChooseDirectories: false,
        CanChooseFiles: true,
        ResolvesAliases: true,
        Title: "选择 Shell 可执行文件",
        ButtonText: "选择",
      }));
      if (picked) updateTerminal({ customPath: picked });
    } catch (e) {
      if (isDialogCancelError(e)) return;
      toast.error(`选择 Shell 失败: ${errText(e)}`);
    }
  };

  const testTerminal = async () => {
    try {
      const result = await Config.TestTerminal(
        resolveTerminalSettings(settings?.terminal),
        settings?.sandboxProfile || "workspace-write",
      );
      if (result.exitCode === 0) {
        toast.success("终端测试通过");
      } else {
        toast.error(`终端测试退出码 ${result.exitCode}`);
      }
    } catch (e) {
      toast.error(`终端测试失败: ${errText(e)}`);
    }
  };

  const selectPermissionProfile = (profile: PermissionProfile) => {
    const next = permissionProfilePolicy(profile);
    if (next.approvalPolicy === defaultExecutionPolicy.approvalPolicy
      && next.sandboxProfile === defaultExecutionPolicy.sandboxProfile) return;
    if (profile === "danger-full-access") {
      setPendingPolicy(next);
      return;
    }
    updateDefaultPolicy(next);
  };

  const confirmDangerFullAccess = () => {
    if (pendingPolicy) updateDefaultPolicy(pendingPolicy);
    setPendingPolicy(null);
  };

  const dirty = isSettingsDirty(savedSettings, settings);
  dirtyRef.current = dirty;
  const defaultPolicyHint = executionPolicyHint(defaultExecutionPolicy);
  const networkCopy = sandboxNetworkCopy(defaultExecutionPolicy, settings?.sandboxNetwork);
  const sandboxStatusView = sandboxStatusError
    ? { title: "无法读取本地工具沙箱", detail: sandboxStatusError, warning: true }
    : sandboxStatusSummary(sandboxStatus);
  const SandboxStatusIcon = sandboxStatusView.warning
    ? TriangleAlert
    : sandboxStatus?.available
      ? ShieldCheck
      : Info;

  const applySavedAppearance = (next: Settings) => {
    const prefs = prefsFromSettings(next);
    applyTheme(prefs);
    onAppearanceChange?.(prefs);
  };

  const requestLeave = useCallback((after: () => void) => {
    if (!dirtyRef.current) {
      after();
      return;
    }
    setPendingLeave(() => after);
    setLeaveDialogOpen(true);
  }, []);

  useEffect(() => {
    onRegisterLeaveGuard?.(requestLeave);
    return () => onRegisterLeaveGuard?.(null);
  }, [onRegisterLeaveGuard, requestLeave]);

  // Esc leaves the hub, but open dialogs (log viewer, artifact preview,
  // leave confirmation) get the first chance to consume it.
  useEffect(() => {
    if (!onClose) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      if (document.querySelector("[role='dialog'][data-state='open']")) return;
      requestLeave(onClose);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, requestLeave]);

  // 反馈入口：版本和平台由应用自动带上，测试机不需要手填。
  const reportIssue = useCallback(async () => {
    try {
      const info = await App.AppInfo().catch(() => null);
      const url = buildIssueReportURL(version || info?.version || "", info?.platform ?? "");
      await Browser.OpenURL(url);
    } catch (e) {
      toast.error(`打开浏览器失败: ${errText(e)}`);
    }
  }, [version]);

  const openReleases = useCallback(async () => {
    try {
      await Update.OpenDownloadPage();
    } catch (e) {
      toast.error(`打开浏览器失败: ${errText(e)}`);
    }
  }, []);

  // 工作区目录：与对话页的「添加项目」共用原生目录选择器，Windows/macOS
  // 都能弹窗选择，不再要求用户手输路径。取消选择不是错误，保持原值。
  const pickWorkspaceDir = useCallback(async () => {
    try {
      const picked = pickedDirectoryValue(await Dialogs.OpenFile({
        CanChooseDirectories: true,
        CanChooseFiles: false,
        CanCreateDirectories: true,
        ResolvesAliases: true,
        Title: "选择工作区目录",
        ButtonText: "选择此文件夹",
      }));
      if (picked) {
        update({ workspaceDir: picked });
      }
    } catch (e) {
      if (isDialogCancelError(e)) return;
      toast.error(`选择目录失败: ${errText(e)}`);
    }
  }, [update]);

  const save = async (): Promise<boolean> => {
    if (!settings) return false;
    if (Object.values(numericValidity.current).some((valid) => !valid)) {
      toast.error("请修正数字输入后再保存");
      return false;
    }
    const a2aError = a2aAutoStartError(settings);
    if (a2aError) {
      toast.error(a2aError);
      setTab("network");
      return false;
    }
    const terminalConfigSaveError = terminalSettingsError(terminalInventory, settings.terminal);
    if (terminalConfigSaveError) {
      toast.error(terminalConfigSaveError);
      setTab("conversation");
      return false;
    }
    setSaving(true);
    try {
      await Config.SaveSettings(settings);
      setSavedSettings(settings);
      cacheTheme(settings);
      applySavedAppearance(settings);
      toast.success("设置已保存");
      return true;
    } catch (e) {
      toast.error(`保存失败: ${errText(e)}`);
      return false;
    } finally {
      setSaving(false);
    }
  };

  const discardDraft = () => {
    if (!savedSettings) return;
    setSettings(savedSettings);
    applySavedAppearance(savedSettings);
  };

  const closeLeaveDialog = () => {
    setLeaveDialogOpen(false);
    setPendingLeave(null);
  };

  const discardAndLeave = () => {
    const after = pendingLeave;
    discardDraft();
    closeLeaveDialog();
    after?.();
  };

  const saveAndLeave = async () => {
    const after = pendingLeave;
    const ok = await save();
    closeLeaveDialog();
    if (ok) after?.();
  };

  if (!settings) {
    // A failed load used to render nothing at all, leaving a blank pane with
    // no way out. Offer a retry instead.
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-sm text-muted-foreground">
        <div>设置加载失败</div>
        <Button size="sm" variant="outline" onClick={reload}>重试</Button>
      </div>
    );
  }

  const titleModelValue = settings.titleProviderId && settings.titleModel
    ? `${settings.titleProviderId}\t${settings.titleModel}`
    : "main";
  const showConfigSaveBar = isConfigSettingsTab(tab);
  const configContentWidth = tab === "knowledge" || tab === "about" ? "max-w-3xl" : "max-w-4xl";

  return (
    <div className="flex h-full bg-canvas">
      <aside className="flex w-60 shrink-0 flex-col bg-chrome py-4 text-sidebar-foreground">
        <div className="mb-2 flex items-center justify-between px-4">
          <span className="text-sm font-semibold">设置</span>
          {onClose && (
            <button
              title="返回对话（Esc）"
              onClick={() => requestLeave(onClose)}
              className="rounded p-1 hover:bg-sidebar-accent/60"
            >
              <ArrowLeft className="h-4 w-4" />
            </button>
          )}
        </div>
        <nav aria-label="设置分类" className="flex flex-1 flex-col gap-3 overflow-y-auto px-2 pb-3">
          {SETTINGS_GROUPS.map((group) => (
            <div
              key={group.key}
              role="group"
              aria-labelledby={`settings-group-${group.key}`}
              className="grid gap-1"
            >
              <div
                id={`settings-group-${group.key}`}
                className="px-3 pt-2 pb-1 text-xs font-medium text-muted-foreground"
              >
                {group.label}
              </div>
              {settingsTabsInGroup(group.key).map(({ key, label }) => {
                const Icon = SETTINGS_ICONS[key];
                return (
                  <button
                    key={key}
                    type="button"
                    aria-current={tab === key ? "page" : undefined}
                    onClick={() => setTab(key)}
                    className={cn(
                      "flex items-center gap-2.5 rounded-md px-3 py-2 text-left text-sm transition-colors",
                      tab === key
                        ? "bg-accent-soft font-medium text-accent-text"
                        : "text-muted-foreground hover:bg-accent hover:text-foreground",
                    )}
                  >
                    <Icon className="h-4 w-4 shrink-0" />
                    <span className="truncate">{label}</span>
                  </button>
                );
              })}
            </div>
          ))}
        </nav>
      </aside>

      <div className="min-w-0 flex-1 overflow-hidden">
        {tab === "agents" && <AgentsPage />}
        {tab === "teams" && <TeamsPage />}
        {tab === "skills" && <SkillsPage />}
        {tab === "mcp" && <MCPPage />}
        {tab === "models" && <ModelsPage />}
        {(isConfigSettingsTab(tab) || tab === "about") && (
        <div className="flex h-full flex-col bg-canvas">
        <div className={cn("min-h-0 flex-1 overflow-y-auto p-6", showConfigSaveBar && "pb-0")}>
      {/* Config pages read in one stable column; Knowledge/About keep their
          narrower single-column width. */}
      <div className={cn("mx-auto", configContentWidth)}>
      <div className="grid gap-4">
        {tab === "appearance" && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">外观</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-2">
            <ThemeChoice
              value={settings.themePreset || "nord"}
              onChange={(themePreset) => update({ themePreset })}
            />
            <SettingRow label="明暗模式" description="跟随系统时只改变明暗，不改变当前主题">
              <SegmentedControl
                ariaLabel="明暗模式"
                value={settings.themeMode || "dark"}
                options={THEME_MODES}
                onChange={(themeMode) => update({ themeMode })}
              />
            </SettingRow>
            <SettingRow label="正文字号" description="调整会话列表、聊天、设置与底部状态栏的基准字号">
              <SegmentedControl
                ariaLabel="正文字号"
                value={settings.fontScale || "md"}
                options={Object.entries(FONT_SCALES).map(([value, f]) => ({ value, label: f.label }))}
                onChange={(fontScale) => update({ fontScale })}
              />
            </SettingRow>
            <SettingRow label="对话栏宽" description="控制对话正文在窗口中的阅读宽度">
              <SegmentedControl
                ariaLabel="对话栏宽"
                value={settings.chatWidth || "medium"}
                options={Object.entries(CHAT_WIDTHS).map(([value, w]) => ({ value, label: w.label }))}
                onChange={(chatWidth) => update({ chatWidth })}
              />
            </SettingRow>
          </CardContent>
        </Card>
        )}
        {tab === "conversation" && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">新会话默认权限</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-4">
            <p className="text-xs leading-relaxed text-muted-foreground">
              只影响新建会话；已有会话在输入框左下角单独切换。权限预设会同时设置审批策略与沙箱边界。
            </p>
            <PolicyChoiceGroup
              label="权限模式"
              description="预设会同时设置审批策略与沙箱边界。"
              options={PERMISSION_PROFILE_OPTIONS}
              value={defaultProfile?.value ?? null}
              onSelect={selectPermissionProfile}
            />
            {!defaultProfile && (
              <div className="flex items-start gap-2 rounded-lg bg-control p-3 text-xs leading-relaxed text-muted-foreground">
                <Info className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                <span>
                  当前为自定义组合：审批「{approvalPolicyLabel(defaultExecutionPolicy.approvalPolicy)}」/ 沙箱「{sandboxProfileLabel(defaultExecutionPolicy.sandboxProfile)}」。选择任一预设会覆盖。
                </span>
              </div>
            )}
            <div
              className={cn(
                "flex items-start gap-3 rounded-lg p-3",
                sandboxStatusView.warning ? "bg-warning-soft text-warning-text" : "bg-control text-foreground",
              )}
            >
              <SandboxStatusIcon
                aria-hidden="true"
                className={cn(
                  "mt-0.5 h-4 w-4 shrink-0",
                  sandboxStatusView.warning ? "text-warning-text" : "text-accent-text",
                )}
              />
              <div className="min-w-0">
                <p className="text-sm font-medium">{sandboxStatusView.title}</p>
                <p
                  className={cn(
                    "mt-1 text-xs leading-relaxed",
                    sandboxStatusView.warning ? "text-warning-text" : "text-muted-foreground",
                  )}
                >
                  {sandboxStatusView.detail}
                </p>
              </div>
            </div>
            {defaultPolicyHint ? (
              <div className="flex items-start gap-2 rounded-lg bg-warning-soft p-3 text-sm text-warning-text">
                <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                <p>{defaultPolicyHint}</p>
              </div>
            ) : defaultExecutionPolicy.sandboxProfile === "danger-full-access" ? (
              <div className="flex items-start gap-2 rounded-lg bg-danger-soft p-3 text-sm text-danger-text">
                <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                <p>新会话默认完全访问权限：终端不再受 OS 沙箱限制，仅用于可信任务。</p>
              </div>
            ) : null}
            <p className="text-xs text-muted-foreground">
              「完全访问权限」会关闭 OS 沙箱并隐含开启联网。
            </p>
          </CardContent>
        </Card>
        )}
        {tab === "conversation" && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">默认命令执行器</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-4">
            <p className="text-xs leading-relaxed text-muted-foreground">
              用于本地 Agent 的 run_command。当前会话沿用已使用的 Shell。
            </p>
            <SettingRow
              label="默认 Shell"
              description="按当前平台检测可用项；未安装的 Shell 不显示。"
            >
              <div className="flex flex-wrap items-center justify-end gap-2">
                <Select value={terminalSettings.mode} onValueChange={updateTerminalMode}>
                  <SelectTrigger className="w-64" aria-label="默认 Shell">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="auto">
                      自动（{terminalAutoLabel}）
                    </SelectItem>
                    {terminalShells.filter((shell) => shell.mode !== "custom").map((shell) => (
                      <SelectItem key={`${shell.mode}:${shell.path}`} value={shell.mode}>
                        {terminalModeLabel(shell.mode)}
                      </SelectItem>
                    ))}
                    {terminalWSL.length > 0 && (
                      <SelectGroup>
                        <SelectLabel>WSL</SelectLabel>
                        <SelectItem value="wsl">
                          WSL · {terminalWSL.map((item) => item.wslDistro).filter(Boolean).join(" / ")}
                        </SelectItem>
                      </SelectGroup>
                    )}
                    {terminalWSL.length === 0 && terminalSettings.mode === "wsl" && (
                      <SelectItem value="wsl">WSL（未检测到发行版）</SelectItem>
                    )}
                    <SelectItem value="custom">自定义路径…</SelectItem>
                    {!terminalKnownModes.has(terminalSettings.mode) && (
                      <SelectItem value={terminalSettings.mode}>
                        {terminalModeLabel(terminalSettings.mode)}（未检测到）
                      </SelectItem>
                    )}
                  </SelectContent>
                </Select>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => reloadTerminals(settings.sandboxProfile || "workspace-write")}
                  disabled={terminalLoading}
                >
                  <RefreshCw className={cn("h-3.5 w-3.5", terminalLoading && "animate-spin")} aria-hidden="true" />
                  重新检测
                </Button>
                <Button size="sm" variant="outline" onClick={() => void testTerminal()}>
                  测试
                </Button>
              </div>
            </SettingRow>
            {terminalSettings.mode === "wsl" && (
              <>
                <SettingRow label="WSL 发行版" description="选择 Agent 命令实际运行的发行版">
                  <Select
                    value={terminalSettings.wslDistro || terminalWSL[0]?.wslDistro || ""}
                    onValueChange={(wslDistro) => updateTerminal({ wslDistro })}
                    disabled={terminalWSL.length === 0}
                  >
                    <SelectTrigger className="w-56" aria-label="WSL 发行版">
                      <SelectValue placeholder={terminalWSL.length ? "选择发行版" : "未检测到发行版"} />
                    </SelectTrigger>
                    <SelectContent>
                      {terminalWSL.map((item) => (
                        <SelectItem key={item.wslDistro} value={item.wslDistro ?? ""}>
                          {item.wslDistro}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </SettingRow>
                <SettingRow label="WSL 用户" description="留空使用发行版的默认用户">
                  <Input
                    className="w-56"
                    value={terminalSettings.wslUser}
                    onChange={(event) => updateTerminal({ wslUser: event.target.value })}
                    placeholder="默认用户"
                    aria-label="WSL 用户"
                  />
                </SettingRow>
                <SettingRow label="WSL Shell" description="留空使用 /bin/bash；Alpine 等可改为 /bin/sh">
                  <Input
                    className="w-56"
                    value={terminalSettings.wslShell}
                    onChange={(event) => updateTerminal({ wslShell: event.target.value })}
                    placeholder="/bin/bash"
                    aria-label="WSL Shell"
                  />
                </SettingRow>
              </>
            )}
            {terminalSettings.mode === "custom" && (
              <SettingRow label="Shell 路径" description="必须是可执行文件的绝对路径">
                <div className="flex w-full max-w-md items-center gap-2">
                  <Input
                    className="min-w-0 flex-1"
                    value={terminalSettings.customPath}
                    onChange={(event) => updateTerminal({ customPath: event.target.value })}
                    placeholder="C:\path\to\shell.exe"
                    aria-label="自定义 Shell 路径"
                  />
                  <Button size="sm" variant="outline" onClick={() => void pickTerminalPath()}>
                    <FolderOpen className="h-3.5 w-3.5" aria-hidden="true" />
                    选择
                  </Button>
                </div>
              </SettingRow>
            )}
            <SettingRow
              label="加载用户启动配置"
              description="默认关闭；开启后 profile 可能改变 PATH、输出内容或产生额外副作用"
            >
              <Switch
                checked={terminalSettings.loadProfile}
                onCheckedChange={(loadProfile) => updateTerminal({ loadProfile })}
                aria-label="加载用户启动配置"
              />
            </SettingRow>
            {terminalError && (
              <div className="flex items-start gap-2 rounded-lg bg-warning-soft p-3 text-sm text-warning-text">
                <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                <p>{terminalError}</p>
              </div>
            )}
            {terminalConfigError && (
              <div className="flex items-start gap-2 rounded-lg bg-warning-soft p-3 text-sm text-warning-text">
                <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                <p>{terminalConfigError}</p>
              </div>
            )}
            {terminalSandboxCopy && (
              <div className="flex items-start gap-2 rounded-lg bg-warning-soft p-3 text-sm text-warning-text">
                <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                <p>{terminalSandboxCopy}</p>
              </div>
            )}
          </CardContent>
        </Card>
        )}
        {tab === "appearance" && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">交互</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-2">
            <SettingRow
              label="执行过程显示"
              description="全局生效；自动按状态展开；简洁只留摘要；详细保持展开；无隐藏过程（确认与错误仍显示）"
            >
              <Select
                value={normalizeExecutionDisplayMode(settings.executionDisplayMode)}
                onValueChange={(v) => update({ executionDisplayMode: v })}
              >
                <SelectTrigger className="w-32"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {EXECUTION_DISPLAY_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </SettingRow>

            <SettingRow label="自动朗读回复" description="助手回复完成后使用语音朗读（TTS）">
              <Switch
          checked={!!settings.autoSpeak}
          onCheckedChange={(v) => update({ autoSpeak: v })}
          />
            </SettingRow>
          </CardContent>
        </Card>
        )}
        {tab === "conversation" && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">会话</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-2">
            <SettingRow label="智能会话命名" description="首轮对话后由主模型生成单行简短标题（关闭则截取首条消息前 30 字）">
              <Switch
          checked={settings.autoTitle !== false}
          onCheckedChange={(v) => update({ autoTitle: v })}
          />
            </SettingRow>
            <SettingRow label="标题提炼模型" description="默认跟随主模型；也可指定更快或更省的模型（仅在智能会话命名开启时生效）">
              <Select
                value={titleModelValue}
                disabled={settings.autoTitle === false}
                onValueChange={(v) => {
                  if (v === "main") {
                    update({ titleProviderId: "", titleModel: "" });
                    return;
                  }
                  const [providerId, modelId] = v.split("\t");
                  update({ titleProviderId: providerId, titleModel: modelId });
                }}
              >
                <SelectTrigger className="w-64"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="main">跟随主模型</SelectItem>
                  {providers.flatMap((p) => (p.models ?? []).map((m) => (
                    <SelectItem key={`${p.id}\t${m.id}`} value={`${p.id}\t${m.id}`}>
                      {p.name} · {modelLabel(m)}
                    </SelectItem>
                  )))}
                </SelectContent>
              </Select>
            </SettingRow>
          </CardContent>
        </Card>
        )}
        {tab === "data" && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">工作区</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-4">
            <div className="grid gap-1.5">
              <Label>工作区目录</Label>
              <div className="flex gap-2">
                <Input
                  className="flex-1"
                  value={settings.workspaceDir ?? ""}
                  onChange={(e) => update({ workspaceDir: e.target.value })}
                  placeholder="例如：D:\\AgentMateWorkspace"
                />
                <Button variant="outline" className="shrink-0" onClick={pickWorkspaceDir}>
                  <FolderOpen className="mr-1.5 h-4 w-4" />
                  浏览…
                </Button>
              </div>
              <p className="text-xs text-muted-foreground">
                点「浏览…」直接选择目录，也可手动输入；开启「工作区文件读写」内置工具的 Agent 只能访问该目录内的文件，「产物预览」也仅限该目录。修改后对新建任务生效。
              </p>
            </div>
          </CardContent>
        </Card>
        )}
        {tab === "network" && (
        <>
          <Card>
            <CardHeader>
              <CardTitle className="text-base">网络</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-1.5">
              <Label>HTTP 代理</Label>
              <Input
                value={settings.httpProxy ?? ""}
                onChange={(e) => update({ httpProxy: e.target.value })}
                placeholder="http://127.0.0.1:7890（留空直连）"
              />
              <p className="text-xs text-muted-foreground">
                模型请求、知识库向量、MCP 远程连接与 A2A 调用统一走该代理（含 Gemini SDK 与 a2a-go 客户端的默认传输层，契约测试钉住）；保存并重启应用后生效。
              </p>
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle className="text-base">本地工具网络</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-3">
              <SettingRow
                label={networkCopy.label}
                description={networkCopy.description}
              >
                <Switch
                  checked={networkCopy.checked}
                  disabled={networkCopy.disabled}
                  onCheckedChange={(v) => update({ sandboxNetwork: v ? "on" : "off" })}
                />
              </SettingRow>
              <p className="text-xs leading-relaxed text-muted-foreground">
                {networkCopy.note}
              </p>
              {networkCopy.notice && (
                <div className="flex items-start gap-2 rounded-lg bg-warning-soft p-3 text-sm text-warning-text">
                  <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                  <p>{networkCopy.notice}</p>
                </div>
              )}
              <p className="text-xs text-muted-foreground">
                Windows 使用 AppContainer 与 ACL 边界；只读档位不授予工作区写权限。权限预设会同时设置审批与沙箱，请求批准和帮我批准都不会绕过沙箱；初始化失败时拒绝执行，不会回退到全权限。
              </p>
            </CardContent>
          </Card>
        </>
        )}
        {tab === "knowledge" && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">知识库语义检索</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-4">
            <div className="grid gap-1.5">
              <Label>Embedding 供应商</Label>
              <Select
                value={settings.embeddingProviderId || "none"}
                onValueChange={(v) => update({ embeddingProviderId: v === "none" ? "" : v })}
              >
                <SelectTrigger className="w-72"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">关闭（仅关键词检索）</SelectItem>
                  {providers.filter((p) => (p.protocol ?? Protocol.ProtocolOpenAI) === Protocol.ProtocolOpenAI).map((p) => (
                    <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-1.5">
              <Label>Embedding 模型</Label>
              <Input
                value={settings.embeddingModel ?? ""}
                onChange={(e) => update({ embeddingModel: e.target.value })}
                placeholder="text-embedding-3-small / BAAI/bge-large-zh-v1.5"
              />
            </div>
            <div className="grid gap-1.5 rounded-lg bg-panel/60 p-2.5">
              <div className="grid grid-cols-2 gap-2">
                <Input value={kbNewName} onChange={(e) => setKbNewName(e.target.value)} placeholder="文件名（如 meeting-notes.md）" />
                <Input value={kbNewContent} onChange={(e) => setKbNewContent(e.target.value)} placeholder="初始内容（必填）" />
              </div>
              <div className="flex items-center gap-2">
                <Button
                  variant="outline"
                  disabled={!kbNewName.trim() || !kbNewContent.trim()}
                  onClick={async () => {
                    if (!kbNewName.trim() || !kbNewContent.trim()) {
                      toast.error("文件名与初始内容均为必填");
                      return;
                    }
                    try {
                      await Config.CreateKnowledgeFile(kbNewName.trim(), kbNewContent);
                      setKbNewName("");
                      setKbNewContent("");
                      const files = await Config.ListKnowledgeFiles();
                      setKbFiles(files ?? []);
                      toast.success("已创建");
                    } catch (e) {
                      toast.error(`创建失败: ${errText(e)}`);
                    }
                  }}
                >
                  新建知识文件
                </Button>
                {kbFiles.map((f) => (
                  <Button key={f.name} variant="ghost" size="sm" title="预览文件" onClick={() => setKbPreviewPath(`knowledge/${f.name}`)}>
                    {f.name}
                  </Button>
                ))}
              </div>
              <div className="grid gap-1.5">
                <div className="flex items-center gap-2">
                  <Input
                    value={kbTestQuery}
                    onChange={(e) => setKbTestQuery(e.target.value)}
                    placeholder="试一条检索词，验证检索效果…"
                  />
                  <Button variant="outline" className="shrink-0" onClick={async () => {
                    try {
                      setKbTestResult(await Config.TestKnowledgeSearch(kbTestQuery));
                    } catch (e) {
                      toast.error(`检索失败: ${errText(e)}`);
                    }
                  }}>
                    测试检索
                  </Button>
                </div>
                {kbTestResult && (
                  <pre className="max-h-40 overflow-auto rounded bg-muted/60 p-2 text-2xs leading-relaxed text-muted-foreground">{kbTestResult}</pre>
                )}
              </div>
              {kbFiles.length > 0 && (
                <div className="grid gap-1 rounded-lg bg-panel/60 p-2">
                  {kbFiles.map((f) => (
                    <div key={f.name} className="flex items-center justify-between gap-2 text-xs">
                      <button
                        className="truncate font-mono text-left underline-offset-2 hover:underline"
                        title="预览文件"
                        onClick={() => setKbPreviewPath(`knowledge/${f.name}`)}
                      >
                        {f.name}
                      </button>
                      <span className="shrink-0 text-muted-foreground">
                        {(f.sizeBytes / 1024).toFixed(1)} KB · {f.modTime}
                      </span>
                      <button
                        className="shrink-0 rounded p-0.5 text-muted-foreground hover:text-danger-text"
                        title={`删除 ${f.name}`}
                        onClick={async () => {
                          if (!window.confirm(`删除知识文件「${f.name}」？索引将同步移除该文件。`)) return;
                          try {
                            await Config.DeleteKnowledgeFile(f.name);
                            setKbFiles((prev) => (prev ?? []).filter((x) => x.name !== f.name));
                            toast.success("已删除（下次检索自动从索引移除）");
                          } catch (e) {
                            toast.error(`删除失败: ${errText(e)}`);
                          }
                        }}
                      >
                        删除
                      </button>
                    </div>
                  ))}
                </div>
              )}
              <p className="text-xs text-muted-foreground">
                配置后，知识库检索（search_knowledge）升级为「向量语义 + 关键词」混合检索；向量索引自动增量构建于本地 SQLite。留空则保持关键词检索。
                {indexStats && indexStats.files > 0 && ` 已索引 ${indexStats.files} 个文件 / ${indexStats.chunks} 个分块${indexStats.model ? `（模型 ${indexStats.model}）` : ""}。`}
              </p>
            </div>
          </CardContent>
        </Card>
        )}
        {tab === "conversation" && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">长会话上下文压缩</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-1.5">
            <div className="grid grid-cols-2 gap-3">
              <div className="grid gap-1.5">
                <Label htmlFor="compaction-interval">滑动窗口压缩间隔（轮）</Label>
                <NumericField
                  id="compaction-interval"
                  aria-label="滑动窗口压缩间隔（轮）"
                  value={settings.compactionInterval ?? 0}
                  rule={{ min: 0, max: 100000, integer: true }}
                  onValueChange={(compactionInterval) => update({ compactionInterval })}
                  onValidityChange={(valid) => reportNumericValidity("compactionInterval", valid)}
                  placeholder="0"
                  hint="0 表示关闭；每 N 轮把更早的对话压缩为摘要。"
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="compaction-context-percent">上下文占用阈值（%）</Label>
                <NumericField
                  id="compaction-context-percent"
                  aria-label="上下文占用阈值（%）"
                  value={settings.compactionContextPercent ?? DEFAULT_COMPACTION_CONTEXT_PERCENT}
                  rule={{ min: 0, max: 100, integer: true }}
                  onValueChange={(compactionContextPercent) => update({ compactionContextPercent })}
                  onValidityChange={(valid) => reportNumericValidity("compactionContextPercent", valid)}
                  suffix="%"
                  placeholder={String(DEFAULT_COMPACTION_CONTEXT_PERCENT)}
                  hint="按当前模型上下文窗口计算；0 表示关闭。"
                />
              </div>
            </div>
            <p className="text-xs text-muted-foreground">
              每 N 轮自动把更早的对话压缩为摘要（ADK 滑动窗口），或当提示 token 超过模型上下文窗口的设定比例时保留尾部、压缩其余。两者都填则同时生效；均为 0 表示关闭。模型未配置上下文窗口时，百分比触发不生效。远程 A2A 会话不支持。
            </p>
          </CardContent>
        </Card>
        )}
        {tab === "conversation" && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">长期记忆</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-2">
            <div className="flex items-center justify-between rounded-lg bg-panel/60 p-2.5">
              <div>
                <div className="text-sm font-medium">自动存入长期记忆</div>
                <div className="text-xs text-muted-foreground">每轮对话结束后自动将该会话存入记忆（含工具调用摘要）</div>
              </div>
              <Switch
                checked={!!settings.autoRemember}
                onCheckedChange={(v) => update({ autoRemember: v })}
              />
            </div>
            <div className="flex items-center justify-between gap-2">
              <p className="text-xs text-muted-foreground">
                {memoryInfo ? `当前存储 ${memoryInfo.count} 条记忆条目（按用户隔离，Agent 可跨会话召回）。` : "加载中…"}
              </p>
              {memoryInfo && memoryInfo.count > 0 && (
                <Button
                  variant="outline"
                  size="sm"
                  className="shrink-0"
                  onClick={async () => {
                    if (!window.confirm("确定清空全部长期记忆？该操作不可恢复。")) return;
                    try {
                      await App.MemoryClear();
                      setMemoryInfo({ count: 0 });
                      toast.success("长期记忆已清空");
                    } catch (e) {
                      toast.error(`清空失败: ${errText(e)}`);
                    }
                  }}
                >
                  清空记忆
                </Button>
              )}
            </div>
          </CardContent>
        </Card>
        )}
        {tab === "data" && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">诊断</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-2">
            <SettingRow label="工具调用审计日志" description="将每次工具调用/结果以 JSONL 追加到应用日志目录（tools-audit.jsonl）">
              <Switch
                checked={!!settings.toolsAudit}
                onCheckedChange={(v) => update({ toolsAudit: v })}
              />
            </SettingRow>
            <Button
              variant="outline"
              onClick={async () => {
                try {
                  const n = await Chat.ExportAllSessions();
                  toast.success(`已导出 ${n} 个会话到工作区 exports/`);
                } catch (e) {
                  toast.error(`导出失败: ${errText(e)}`);
                }
              }}
            >
              批量导出全部会话
            </Button>
            <Button
              variant="outline"
              onClick={async () => {
                try {
                  const lines = (await App.RecentToolAudit(200)) ?? [];
                  setLogText(lines.join("\n"));
                  setLogTitle("工具审计日志（最近 200 行）");
                  setLogOpen(true);
                } catch (e) {
                  toast.error(`读取审计失败: ${errText(e)}`);
                }
              }}
            >
              查看工具审计日志
            </Button>
            <Button
              variant="outline"
              onClick={async () => {
                try {
                  const lines = (await App.RecentLogs(200)) ?? [];
                  setLogText(lines.join("\n"));
                  setLogTitle("应用日志（最近 200 行）");
                  setLogOpen(true);
                } catch (e) {
                  toast.error(`读取日志失败: ${errText(e)}`);
                }
              }}
            >
              查看最近日志（200 行）
            </Button>
            <Dialog open={logOpen} onOpenChange={setLogOpen}>
              <DialogContent className="sm:max-w-2xl">
                <DialogHeader>
                  <DialogTitle>{logTitle}</DialogTitle>
                </DialogHeader>
                <pre className="max-h-[50vh] overflow-auto rounded bg-muted/60 p-3 font-mono text-2xs leading-relaxed">{logText}</pre>
              </DialogContent>
            </Dialog>
            <div className="flex items-center justify-between gap-2">
              <p className="text-xs text-muted-foreground">
                遇到问题时先查看日志（logs/app.log，5MB 自动轮转），反馈问题时可一并附上。
                {storage && storage.sessionsDbBytes >= 0 && (
                  <>
                    会话库 {(storage.sessionsDbBytes / 1024 / 1024).toFixed(1)} MB
                    {storage.knowledgeDbBytes >= 0 && ` · 知识索引 ${(storage.knowledgeDbBytes / 1024).toFixed(0)} KB`}
                  </>
                )}
              </p>
              <Button variant="outline" size="sm" className="shrink-0" onClick={async () => {
                try {
                  await App.VacuumSessions();
                  toast.success("会话库已压缩整理");
                } catch (e) {
                  toast.error(`压缩失败: ${errText(e)}`);
                }
              }}>
                压缩会话库
              </Button>
            </div>
          </CardContent>
        </Card>
        )}
        {tab === "data" && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">备份与迁移</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-3">
            <div className="flex flex-wrap gap-2">
              <Button
                variant="outline"
                onClick={async () => {
                  try {
                    const p = await App.ExportConfig(true);
                    toast.success(`已导出（含密钥）: ${p}`);
                  } catch (e) {
                    toast.error(`导出失败: ${errText(e)}`);
                  }
                }}
              >
                导出备份（含密钥）
              </Button>
              <Button
                variant="outline"
                onClick={async () => {
                  try {
                    const p = await App.ExportConfig(false);
                    toast.success(`已导出（脱敏）: ${p}`);
                  } catch (e) {
                    toast.error(`导出失败: ${errText(e)}`);
                  }
                }}
              >
                导出备份（脱敏）
              </Button>
              <Button variant="outline" onClick={() => importRef.current?.click()}>
                导入备份…
              </Button>
              <input
                ref={importRef}
                type="file"
                accept="application/json,.json"
                className="hidden"
                onChange={async (e) => {
                  const f = e.target.files?.[0];
                  e.target.value = "";
                  if (!f) return;
                  if (!window.confirm("导入将覆盖当前供应商/Agent/远程 Agent/团队/Skills/MCP/设置（会话历史保留），确定继续？")) return;
                  try {
                    const text = await f.text();
                    await App.ImportConfig(text);
                    toast.success("导入成功，已应用备份配置");
                    reload();
                  } catch (err) {
                    toast.error(`导入失败: ${errText(err)}`);
                  }
                }}
              />
            </div>
            <p className="text-xs text-muted-foreground">
              备份写入工作区 exports/ 目录。含密钥备份以 0600 权限保存且可直接恢复；脱敏备份不含 API Key，适合分享或迁移。
            </p>
          </CardContent>
        </Card>
        )}
        {tab === "network" && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">A2A 服务（对外提供远程 Agent）</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-3">
            <div className="grid grid-cols-2 gap-3">
              <div className="grid gap-1.5">
                <Label>发布的 Agent</Label>
                <Select
                  value={settings.a2aAgentId || undefined}
                  onValueChange={(agentId) => update({ a2aAgentId: agentId })}
                >
                  <SelectTrigger><SelectValue placeholder="选择 Agent" /></SelectTrigger>
                  <SelectContent>
                    {agents.map((a) => (
                      <SelectItem key={a.id} value={a.id}>{a.name}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="a2a-port">端口</Label>
                <NumericField
                  id="a2a-port"
                  aria-label="A2A 端口"
                  value={settings.a2aPort ?? 0}
                  rule={{ min: 0, max: 65535, integer: true }}
                  onValueChange={(a2aPort) => update({ a2aPort })}
                  onValidityChange={(valid) => reportNumericValidity("a2aPort", valid)}
                  placeholder="8080"
                  hint="1–65535；0 表示随机端口。"
                />
              </div>
            </div>
            <div className="flex items-center gap-2">
              <Button
                onClick={async () => {
                  if (!settings.a2aAgentId) { toast.error("请选择要发布的 Agent"); return; }
                  const port = Number(settings.a2aPort);
                  if (!Number.isInteger(port) || port < 1 || port > 65535) {
                    toast.error("端口需为 1-65535 的整数");
                    return;
                  }
                  try {
                    await A2A.Start(settings.a2aAgentId, port);
                    setA2AStatuses((await A2A.Status()) ?? []);
                    toast.success("A2A 服务已启动");
                  } catch (e) {
                    toast.error(`启动失败: ${errText(e)}`);
                  }
                }}
              >
                发布 Agent
              </Button>
            </div>
            {(a2aStatuses.length ?? 0) > 0 && (
              <div className="grid gap-1.5 rounded-lg bg-panel/60 p-2.5">
                {a2aStatuses.map((st) => (
                  <div key={st.agentId} className="flex items-center justify-between gap-2 text-xs">
                    <span className="truncate">
                      <span className="font-medium">{st.agentName}</span>
                      <span className="ml-1.5 font-mono text-muted-foreground">{st.url}</span>
                    </span>
                    <Button variant="ghost" size="sm" className="shrink-0" onClick={async () => {
                      try {
                        await A2A.StopAgent(st.agentId ?? "");
                        setA2AStatuses((await A2A.Status()) ?? []);
                      } catch (e) {
                        toast.error(`停止失败: ${errText(e)}`);
                      }
                    }}>
                      停止
                    </Button>
                  </div>
                ))}
              </div>
            )}
            <SettingRow label="开机自动启动 A2A 服务" description="使用上方选择的 Agent 与端口，应用启动时自动发布">
              <Switch
                checked={!!settings.a2aAutoStart}
                onCheckedChange={(v) => update({ a2aAutoStart: v })}
              />
            </SettingRow>
            <p className="text-xs text-muted-foreground">
              启动后其他 A2A 客户端可通过该地址发现并调用此 Agent（仅监听本机地址）。
            </p>
          </CardContent>
        </Card>
        )}
        {tab === "about" && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">关于</CardTitle>
          </CardHeader>
          <CardContent className="text-sm text-muted-foreground">
            <div className="flex flex-wrap items-baseline gap-2">
              <span className="text-base font-medium text-foreground">AgentMate</span>
              {version && <span className="text-xs text-muted-foreground">v{version}</span>}
            </div>
            <div className="mt-1 text-xs text-muted-foreground">本地 AI Agent 桌面应用</div>
            <UpdateCard />
            <div className="mt-4 grid gap-1">
              <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg bg-panel/40 px-3 py-3">
                <div>
                  <div className="text-sm font-medium text-foreground">版本记录</div>
                  <div className="text-xs text-muted-foreground">查看所有版本与更新说明</div>
                </div>
                <Button size="sm" variant="outline" onClick={openReleases}>
                  查看
                </Button>
              </div>
              <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg bg-panel/40 px-3 py-3">
                <div>
                  <div className="text-sm font-medium text-foreground">问题反馈</div>
                  <div className="text-xs text-muted-foreground">在浏览器打开 GitHub 表单，自动填入版本与环境</div>
                </div>
                <Button size="sm" variant="outline" onClick={reportIssue}>
                  打开表单
                </Button>
              </div>
            </div>
            <details className="mt-3 rounded-lg bg-panel/40 p-3 text-xs text-muted-foreground">
              <summary className="cursor-pointer select-none hover:text-foreground">技术信息</summary>
              <div className="mt-2 grid gap-1">
                <div>运行时：Google ADK Go v2（Runner / LLMAgent / AgentTool）</div>
                <div>会话存储：SQLite（ADK Database Session Service）</div>
                <div>工具：ADK FunctionTool、MCP Toolset（go-sdk）、Skill Toolset（agentskills.io 规范）</div>
              </div>
            </details>
          </CardContent>
        </Card>
        )}
      </div>
        {kbPreviewPath && (
          <ArtifactPreview path={kbPreviewPath} onClose={() => setKbPreviewPath(null)} />
        )}
      </div>
        </div>
        {showConfigSaveBar && (
        <div className="shrink-0 bg-canvas px-6 pb-6 pt-4">
          <div className={cn("mx-auto flex justify-end px-3", configContentWidth)}>
            <Button onClick={() => void save()} disabled={saving || !dirty}>
              {saving ? "保存中…" : "保存设置"}
            </Button>
          </div>
        </div>
        )}
    </div>
        )}
      </div>
      <Dialog open={pendingPolicy !== null} onOpenChange={(open) => { if (!open) setPendingPolicy(null); }}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2 text-base">
              <TriangleAlert className="h-4 w-4 text-danger-text" aria-hidden="true" />
              启用完全访问权限？
            </DialogTitle>
          </DialogHeader>
          <p className="text-sm leading-relaxed text-muted-foreground">
            终端命令将不再经过 AppContainer 沙箱，可以访问互联网并读写工作区外的文件；审批策略同时设为「从不询问」。请仅在你信任当前任务和模型输出时启用。
          </p>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPendingPolicy(null)}>
              取消
            </Button>
            <Button variant="destructive" onClick={confirmDangerFullAccess}>
              启用完全访问权限
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog open={leaveDialogOpen} onOpenChange={(open) => { if (!open) closeLeaveDialog(); }}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle className="text-base">有未保存的设置</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-foreground">
            离开设置中心前要保存这些修改吗？放弃后，未保存的外观预览也会恢复。
          </p>
          <DialogFooter>
            <Button variant="outline" onClick={closeLeaveDialog} disabled={saving}>
              继续编辑
            </Button>
            <Button variant="outline" onClick={discardAndLeave} disabled={saving}>
              放弃修改
            </Button>
            <Button onClick={() => void saveAndLeave()} disabled={saving}>
              {saving ? "保存中…" : "保存并退出"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
