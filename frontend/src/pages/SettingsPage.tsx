import { useCallback, useEffect, useRef, useState } from "react";
import {
  ArrowLeft, BookOpen, Bot, Boxes, Check, Database, FolderOpen, Hand, Info,
  MessageSquare, Network, Palette, Plug, ShieldCheck, Sparkles, TriangleAlert,
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
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { ArtifactPreview } from "@/components/ToolCallCard";
import { UpdateCard } from "@/components/UpdateCard";
import { ThemeGallery } from "@/components/ThemeGallery";
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
  SETTINGS_GROUPS,
  a2aAutoStartError,
  isConfigSettingsTab,
  isSettingsDirty,
  normalizeSandboxMode,
  resolveSettingsTab,
  settingsTabsInGroup,
  type SettingsLeaveGuard,
  type SettingsTab,
} from "@/lib/settingsModel";
import { Chat, App, A2A, Config, Update, type AgentConfig, type A2AStatus, type KnowledgeFile, type KnowledgeIndexStats, type ModelProvider, type Settings, type StorageInfo, type MemoryInfo, errText, Protocol } from "@/lib/api";

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

const SANDBOX_MODE_OPTIONS: Array<{
  value: "request-approval" | "auto-approve" | "danger-full-access";
  title: string;
  description: string;
  icon: LucideIcon;
  danger?: boolean;
}> = [
  {
    value: "request-approval",
    title: "请求批准",
    description: "写文件和执行命令前始终询问；工作区外写入仍由沙箱拦截。",
    icon: Hand,
  },
  {
    value: "auto-approve",
    title: "帮我批准",
    description: "工作区内安全操作自动执行；删除、系统配置、凭据访问、发布等风险操作才询问。",
    icon: ShieldCheck,
  },
  {
    value: "danger-full-access",
    title: "完全访问权限",
    description: "不再询问并关闭 OS 沙箱；终端可访问互联网和任意文件，仅用于可信任务。",
    icon: TriangleAlert,
    danger: true,
  },
];

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
  const [pendingSandboxMode, setPendingSandboxMode] = useState<string | null>(null);
  const importRef = useRef<HTMLInputElement>(null);
  const [logOpen, setLogOpen] = useState(false);
  const [logText, setLogText] = useState("");
  const [logTitle, setLogTitle] = useState("应用日志");
  const [storage, setStorage] = useState<StorageInfo | null>(null);
  const [memoryInfo, setMemoryInfo] = useState<MemoryInfo | null>(null);
  const [a2aStatuses, setA2AStatuses] = useState<A2AStatus[]>([]);
  const [indexStats, setIndexStats] = useState<KnowledgeIndexStats | null>(null);
  const [kbTestQuery, setKbTestQuery] = useState("");
  const [kbTestResult, setKbTestResult] = useState("");
  const [kbFiles, setKbFiles] = useState<KnowledgeFile[]>([]);
  const [kbPreviewPath, setKbPreviewPath] = useState<string | null>(null);
  const [kbNewName, setKbNewName] = useState("");
  const [kbNewContent, setKbNewContent] = useState("");
  const dirtyRef = useRef(false);

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
  useEffect(() => {
    A2A.Status().then((sts) => setA2AStatuses(sts ?? [])).catch(() => {});
    Config.KnowledgeIndexStats().then(setIndexStats).catch(() => {});
    Config.ListKnowledgeFiles().then((fs) => setKbFiles(fs ?? [])).catch(() => {});
    App.StorageStats().then(setStorage).catch(() => {});
    App.MemoryStats().then(setMemoryInfo).catch(() => {});
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

  const selectSandboxMode = (mode: string) => {
    if (mode === "danger-full-access") {
      setPendingSandboxMode(mode);
      return;
    }
    update({ sandboxMode: mode });
  };

  const confirmDangerFullAccess = () => {
    update({ sandboxMode: "danger-full-access", sandboxNetwork: "on" });
    setPendingSandboxMode(null);
  };

  const dirty = isSettingsDirty(savedSettings, settings);
  dirtyRef.current = dirty;
  const sandboxMode = normalizeSandboxMode(settings?.sandboxMode);

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
    const a2aError = a2aAutoStartError(settings);
    if (a2aError) {
      toast.error(a2aError);
      setTab("network");
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
  const previewDark = typeof document !== "undefined" && document.documentElement.classList.contains("dark");
  const showConfigSaveBar = isConfigSettingsTab(tab);
  const configContentWidth = tab === "knowledge" || tab === "about" ? "max-w-3xl" : "max-w-4xl";

  return (
    <div className="flex h-full bg-canvas">
      <aside className="flex w-48 shrink-0 flex-col bg-chrome py-4 text-sidebar-foreground">
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
          <CardContent className="grid gap-5">
            <div className="grid gap-2">
              <Label>主题</Label>
              <ThemeGallery
                value={settings.themePreset || "nord"}
                dark={previewDark}
                onChange={(themePreset) => update({ themePreset })}
              />
            </div>

            <div className="grid gap-2">
              <Label>明暗模式</Label>
              <div role="radiogroup" aria-label="明暗模式" className="inline-flex w-fit rounded-lg bg-panel p-1">
                {THEME_MODES.map((mode) => {
                  const selected = (settings.themeMode || "dark") === mode.value;
                  return (
                    <button
                      key={mode.value}
                      type="button"
                      role="radio"
                      aria-checked={selected}
                      onClick={() => update({ themeMode: mode.value })}
                      onKeyDown={(event) => {
                        const keys = ["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End"];
                        if (!keys.includes(event.key)) return;
                        event.preventDefault();
                        const radios = Array.from(
                          event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="radio"]') ?? [],
                        );
                        const current = radios.indexOf(event.currentTarget);
                        let next = current;
                        if (event.key === "Home") next = 0;
                        else if (event.key === "End") next = radios.length - 1;
                        else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
                          next = (current - 1 + radios.length) % radios.length;
                        } else {
                          next = (current + 1) % radios.length;
                        }
                        const nextMode = THEME_MODES[next];
                        if (nextMode) {
                          update({ themeMode: nextMode.value });
                          radios[next]?.focus();
                        }
                      }}
                      className={cn(
                        "h-7 rounded-md px-3 text-xs transition-colors",
                        selected
                          ? "bg-accent-soft font-medium text-accent-text"
                          : "text-muted-foreground hover:bg-accent hover:text-foreground",
                      )}
                    >
                      {mode.label}
                    </button>
                  );
                })}
              </div>
            </div>

            <div className="grid gap-1.5">
              <Label>正文字号</Label>
              <div className="flex gap-2">
                {Object.entries(FONT_SCALES).map(([key, f]) => (
                  <button
                    key={key}
                    type="button"
                    aria-pressed={(settings.fontScale || "md") === key}
                    className={cn(
                      "h-7 w-16 rounded-md text-xs transition-colors",
                      (settings.fontScale || "md") === key
                        ? "bg-accent-soft font-medium text-accent-text"
                        : "text-muted-foreground hover:bg-accent hover:text-foreground",
                    )}
                    onClick={() => update({ fontScale: key })}
                  >
                    {f.label}
                  </button>
                ))}
              </div>
            </div>

            <div className="grid gap-1.5">
              <Label>对话栏宽</Label>
              <div className="flex gap-2">
                {Object.entries(CHAT_WIDTHS).map(([key, w]) => (
                  <button
                    key={key}
                    type="button"
                    aria-pressed={(settings.chatWidth || "medium") === key}
                    className={cn(
                      "h-7 w-16 rounded-md text-xs transition-colors",
                      (settings.chatWidth || "medium") === key
                        ? "bg-accent-soft font-medium text-accent-text"
                        : "text-muted-foreground hover:bg-accent hover:text-foreground",
                    )}
                    onClick={() => update({ chatWidth: key })}
                  >
                    {w.label}
                  </button>
                ))}
              </div>
            </div>
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
              <CardTitle className="text-base">本地工具沙箱</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-3">
              <div className="grid gap-2">
                <div>
                  <div className="text-sm font-medium">审批模式</div>
                  <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                    同时决定内置文件/终端工具是否逐次询问，以及是否保留 OS 沙箱。
                  </p>
                </div>
                <div role="radiogroup" aria-label="本地工具审批模式" className="grid gap-2 sm:grid-cols-3">
                  {SANDBOX_MODE_OPTIONS.map((option) => {
                    const selected = sandboxMode === option.value;
                    const Icon = option.icon;
                    return (
                      <button
                        key={option.value}
                        type="button"
                        role="radio"
                        aria-checked={selected}
                        onClick={() => selectSandboxMode(option.value)}
                        onKeyDown={(event) => {
                          const keys = ["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End"];
                          if (!keys.includes(event.key)) return;
                          event.preventDefault();
                          const radios = Array.from(
                            event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="radio"]') ?? [],
                          );
                          const current = radios.indexOf(event.currentTarget);
                          let next = current;
                          if (event.key === "Home") next = 0;
                          else if (event.key === "End") next = radios.length - 1;
                          else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
                            next = (current - 1 + radios.length) % radios.length;
                          } else {
                            next = (current + 1) % radios.length;
                          }
                          const nextOption = SANDBOX_MODE_OPTIONS[next];
                          if (nextOption) {
                            selectSandboxMode(nextOption.value);
                            radios[next]?.focus();
                          }
                        }}
                        className={cn(
                          "group flex flex-col gap-3 rounded-xl p-3 text-left transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
                          selected
                            ? option.danger ? "bg-danger-soft" : "bg-accent-soft"
                            : "bg-panel hover:bg-overlay",
                        )}
                      >
                        <div className="flex items-center justify-between gap-2">
                          <Icon
                            aria-hidden="true"
                            className={cn(
                              "h-4 w-4",
                              option.danger
                                ? "text-danger-text"
                                : selected ? "text-accent-text" : "text-muted-foreground",
                            )}
                          />
                          {selected && (
                            <Check
                              aria-hidden="true"
                              className={cn("h-3.5 w-3.5", option.danger ? "text-danger-text" : "text-accent-text")}
                            />
                          )}
                        </div>
                        <div>
                          <span
                            className={cn(
                              "text-sm font-medium",
                              selected && (option.danger ? "text-danger-text" : "text-accent-text"),
                            )}
                          >
                            {option.title}
                          </span>
                          <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                            {option.description}
                          </p>
                        </div>
                      </button>
                    );
                  })}
                </div>
              </div>
              <SettingRow
                label="允许子进程联网"
                description={sandboxMode === "danger-full-access"
                  ? "完全访问模式下终端联网始终开启。"
                  : "关闭时 run_command 子进程默认断网；需要 git push 等联网操作时再开启。"}
              >
                <Switch
                  checked={sandboxMode === "danger-full-access" || (settings.sandboxNetwork || "off") === "on"}
                  disabled={sandboxMode === "danger-full-access"}
                  onCheckedChange={(v) => update({ sandboxNetwork: v ? "on" : "off" })}
                />
              </SettingRow>
              {sandboxMode === "danger-full-access" && (
                <div className="flex items-start gap-2 rounded-lg bg-danger-soft p-3 text-sm text-danger-text">
                  <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                  <p>
                    完全访问已开启：终端不再受 OS 沙箱限制。会话内的「允许完全访问」只免审批，不会关闭这里的沙箱。
                  </p>
                </div>
              )}
              <p className="text-xs text-muted-foreground">
                Windows 使用 AppContainer 与低完整性边界；请求批准和帮我批准都会拦截工作区外写入。沙箱初始化失败时拒绝执行，不会回退到全权限。内置读写工具始终以任务工作区为根，完全访问主要放开终端命令；Agent 编辑页勾选的额外确认在「帮我批准」下仍会生效。
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
                <Label>滑动窗口压缩间隔（轮）</Label>
                <Input
                  type="number"
                  min={0}
                  value={settings.compactionInterval || ""}
                  onChange={(e) => update({ compactionInterval: Number(e.target.value) || 0 })}
                  placeholder="0（关闭）"
                />
              </div>
              <div className="grid gap-1.5">
                <Label>Token 阈值触发</Label>
                <Input
                  type="number"
                  min={0}
                  value={settings.compactionTokenThreshold || ""}
                  onChange={(e) => update({ compactionTokenThreshold: Number(e.target.value) || 0 })}
                  placeholder="0（关闭）"
                />
              </div>
            </div>
            <p className="text-xs text-muted-foreground">
              每 N 轮自动把更早的对话压缩为摘要（ADK 滑动窗口），或当提示 token 超过阈值时保留尾部、压缩其余。两者都填则同时生效；均为 0 表示关闭。远程 A2A 会话不支持。
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
                <Label>端口</Label>
                <Input
                  type="number"
                  min={1}
                  max={65535}
                  value={settings.a2aPort || ""}
                  onChange={(e) => update({ a2aPort: Number(e.target.value) || 0 })}
                  placeholder="8080"
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
      <Dialog open={pendingSandboxMode !== null} onOpenChange={(open) => { if (!open) setPendingSandboxMode(null); }}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2 text-base">
              <TriangleAlert className="h-4 w-4 text-danger-text" aria-hidden="true" />
              启用完全访问权限？
            </DialogTitle>
          </DialogHeader>
          <p className="text-sm leading-relaxed text-muted-foreground">
            终端命令将不再经过 AppContainer 沙箱，也不再逐次确认。它可以访问互联网、读取凭据目录并写入工作区外的文件。请仅在你信任当前任务和模型输出时启用。
          </p>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPendingSandboxMode(null)}>
              取消
            </Button>
            <Button variant="destructive" onClick={confirmDangerFullAccess}>
              启用完全访问
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
