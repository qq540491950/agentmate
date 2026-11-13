import { Fragment, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Dialogs, Events } from "@wailsio/runtime";
import {
  Plus, Square, Trash2, Pencil, MessageSquare, Bot, Users, Loader2, ArrowUp, ArrowDown,
  Search, Download,
  X, BarChart3, Brain, Network, GitFork, Pin, PinOff, FileCode2, Archive, ClipboardCopy,
  Folder, FolderOpen, FileText,
  PanelRightOpen,
  Check, ChevronRight, ChevronDown,
  Coins, Hand, ShieldCheck, TriangleAlert, SlidersHorizontal,
  PanelLeftClose, PanelLeftOpen,
  FolderPlus, MoreHorizontal, SquarePen,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Skeleton } from "@/components/ui/skeleton";
import { ConfirmDialog, type ConfirmRequest } from "@/components/ConfirmDialog";
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuSeparator, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";
import { PROJECT_SESSION_LIMIT, buildSidebarProjectGroups, chatItemEndTs, chatItemStartTs, computeEditFlags, computeRunningTools, computeToolDurations, effectiveReasoningLevel, groupToolMessages, groupTurns, insertThinkingBeforeStreamingReply, isAwaitingReply, isDayChange, latestPlanMessage, mdToSpeech, projectSessionWindow, sessionTargetBadgeLabel, shouldExpandProjectForSession, type ChatItem, type UIMessage } from "@/lib/chatUtils";
import {
  dropMessageByID,
  historySnapshotIsCurrent,
  reconcilePendingUser,
  replaceMessageByID,
  type PendingUserTurn,
} from "@/lib/chatTimeline";
import { SESSION_ROW_ACTIONS_CLASS } from "@/lib/sessionRowActions";
import {
  SIDEBAR_KEYBOARD_STEP, SIDEBAR_MIN_WIDTH,
  clampSidebarWidth, parseStoredSidebarWidth, sidebarMaxWidth,
} from "@/lib/sidebarWidth";
import { draftModelFor, draftUsesProject, shouldOpenLatestSession } from "@/lib/sessionDraft";
import { filterSlashCommands, isSkillCommand, slashCandidates, slashSourceLabel, type SlashCommand } from "@/lib/slashSkills";
import {
  applyMentionToken,
  buildMentionCandidates,
  isAssistantCandidate,
  mentionAtCaret,
  type AssistantCandidate,
  type MentionCandidate,
} from "@/lib/mentions";
import { isDialogCancelError } from "@/lib/workspace";
import { composerReasoningModel, levelLabel, modelLabel, modelPickerLabel, reasoningLevels } from "@/lib/modelLabel";
import { resolveAgentModel } from "@/lib/agentModel";
import { MessageBubble, DaySeparator } from "@/components/chat/MessageBubble";
import { TurnBlock, type TurnBodyOpts } from "@/components/chat/TurnBlock";
import { TurnStatus } from "@/components/chat/TurnStatus";
import { TurnRail, type RailTurn } from "@/components/chat/TurnRail";
import { ToolCallGroup } from "@/components/chat/ToolCallGroup";
import { SessionRowLabel } from "@/components/chat/SessionRowLabel";
import { ContextRing } from "@/components/chat/ContextRing";
import { Tooltip, TooltipContent, TooltipHint, TooltipTrigger } from "@/components/ui/tooltip";
import { PlanChip, type TodoItem } from "@/components/chat/TodoCards";
import { Onboarding } from "@/components/chat/Onboarding";
import { ReasoningEffortPanel } from "@/components/chat/ReasoningEffortPanel";
import { TaskWorkspacePanel } from "@/components/TaskWorkspacePanel";
import { SidebarFooter } from "@/components/SidebarFooter";
import type { SettingsTab } from "@/pages/SettingsPage";
import {
  Chat, Config, errText, setSessionExecutionPolicy,
  effectiveSkills, listContextPaths,
  type AgentConfig, type AttachmentIn, type ChatSession, type TeamConfig,
  type ChatStreamEvent,
  type RemoteAgentConfig, type UsageInfo, type ModelProvider, type ModelInfo, type SessionStats,
  type Project, type Settings, type SkillConfig, type WorkspaceEntry,
} from "@/lib/api";
import { contextTooltipLines, estimateContextTokens, resolveContextUsage } from "@/lib/contextUsage";
import {
  approvalPolicyOption,
  executionPolicyHint,
  PERMISSION_PROFILE_OPTIONS,
  permissionProfileForPolicy,
  permissionProfilePolicy,
  sandboxProfileOption,
  type PermissionProfile,
} from "@/lib/sandboxMode";
import {
  DEFAULT_EXECUTION_POLICY,
  normalizeApprovalPolicy,
  normalizeSandboxProfile,
  resolveExecutionPolicy,
  type ApprovalPolicy,
  type ExecutionPolicy,
  type SandboxProfile,
} from "@/lib/settingsModel";
import {
  normalizeExecutionDisplayMode, type ExecutionDisplayMode,
} from "@/lib/executionDisplay";

let mid = 0;
const nextId = () => `m${++mid}`;

/**
 * 在上一个回合流式输出期间被暂存的发送；它保留用户排队时选定的每条消息的思考等
 * 级，而不是之后选定的任何值。
 */
type QueuedMessage = { text: string; reasoning: string };

// RENDER_LIMIT 限制一次渲染多少条消息，以保证长会话流畅。
const RENDER_LIMIT = 200;
// 将最近打开的少数几个会话保留在内存中，这样切回时不会在本地 SQLite
// 读取返回前闪现空白/骨架面板。
const HISTORY_CACHE_LIMIT = 8;

const PERMISSION_PROFILE_ICONS: Record<PermissionProfile, typeof Hand> = {
  "request-approval": Hand,
  "auto-approve": ShieldCheck,
  "danger-full-access": TriangleAlert,
};

function rememberHistory(cache: Map<string, UIMessage[]>, sessionID: string, messages: UIMessage[]) {
  cache.delete(sessionID);
  cache.set(sessionID, messages);
  while (cache.size > HISTORY_CACHE_LIMIT) {
    const oldest = cache.keys().next().value;
    if (oldest == null) break;
    cache.delete(oldest);
  }
}

// 侧边栏宽度存放在 localStorage 而不是 config.json
// 中：它是每个窗口各自的视图偏好，而配置 schema 会通过 Go
// 绑定往返，没必要为一次分隔条拖拽而扩展它。
const SIDEBAR_WIDTH_STORAGE_KEY = "wb-chat-sidebar-width";

// 内置命令在本地运行，不经过模型往返。已配置的技能由 slashCandidates
// 追加（issue #53）。
const SLASH_COMMANDS: SlashCommand[] = [
  { cmd: "/export", desc: "导出会话为 Markdown", source: "builtin" },
  { cmd: "/exporthtml", desc: "导出会话为 HTML", source: "builtin" },
  { cmd: "/stats", desc: "查看会话统计", source: "builtin" },
  { cmd: "/todos", desc: "查看当前任务计划", source: "builtin" },
  { cmd: "/new", desc: "新建对话", source: "builtin" },
];

/** speak 为 text 合成语音，并取消先前的播放。 */
function speak(text: string) {
  if (!("speechSynthesis" in window)) return;
  window.speechSynthesis.cancel();
  const u = new SpeechSynthesisUtterance(mdToSpeech(text));
  u.lang = "zh-CN";
  u.rate = 1.05;
  window.speechSynthesis.speak(u);
}

export function ChatPage({ themePreset, themeMode, onAppearanceChange, onOpenHelp, onOpenSettings, version }: {
  themePreset: string;
  themeMode: string;
  onAppearanceChange: (patch: { themePreset?: string; themeMode?: string }) => void;
  onOpenHelp: () => void;
  onOpenSettings: (tab?: SettingsTab) => void;
  version?: string;
}) {
  const [sessions, setSessions] = useState<ChatSession[]>([]);
  const [activeID, setActiveID] = useState("");
  const [messages, setMessages] = useState<UIMessage[]>([]);
  // 按会话保存的输入草稿：切换会话绝不会丢失用户正在输入的内容（与并行后台会话
  // 配合使用）。
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  // 在任何会话存在之前输入的草稿：直接聊天在首次发送时才惰性创建，就像
  // Codex/ChatGPT 桌面端的输入框优先流程。
  const [freshInput, setFreshInput] = useState("");
  // 由「新聊天」设置：空白对话会保持为本地草稿，直到发送第一条消息，因此不会有
  // 空行落入侧边栏/数据库，自动打开最新会话的 effect 不得替换它（issue #30）。
  const [newChatDraft, setNewChatDraft] = useState(false);
  // 绑定到该待发送草稿的项目（"" =
  // 直接聊天）。仅首次发送会创建该行，因此项目的「+」不再每次点击都持久化一个空
  // 对话（issue #60）。
  const [draftProjectID, setDraftProjectID] = useState("");
  // 在该行存在之前于输入区所做的选择（issue
  // #64）。它们会保持为本地状态，直到首次发送创建会话，随后 send()
  // 会在第一个回合之前将它们应用到新对话——因此这里选择的模型和审批模式是真实的
  // 选择，而不是被丢弃的选择。
  const [draftModelOverride, setDraftModelOverride] = useState("");
  const [draftApprovalPolicy, setDraftApprovalPolicy] = useState<ApprovalPolicy | "">("");
  const [draftSandboxProfile, setDraftSandboxProfile] = useState<SandboxProfile | "">("");
  const input = activeID ? (drafts[activeID] ?? "") : freshInput;
  const setInput = useCallback(
    (v: string | ((prev: string) => string)) => {
      if (!activeID) {
        setFreshInput((cur) => (typeof v === "function" ? v(cur) : v));
        return;
      }
      setDrafts((prev) => {
        const cur = prev[activeID] ?? "";
        const next = typeof v === "function" ? v(cur) : v;
        return { ...prev, [activeID]: next };
      });
    },
    [activeID],
  );
  const [runningMap, setRunningMap] = useState<Record<string, boolean>>({});
  const running = !!activeID && !!runningMap[activeID];
  const [agents, setAgents] = useState<AgentConfig[]>([]);
  const [teams, setTeams] = useState<TeamConfig[]>([]);
  const [remotes, setRemotes] = useState<RemoteAgentConfig[]>([]);
  // 已配置的技能提供额外的 "/" 命令（issue
  // #53）。该列表的作用域为会话（见下方的 EffectiveSkills
  // effect），因此项目聊天也能看到 <project>/.agents/skills，而无需导入它们。
  const [skills, setSkills] = useState<SkillConfig[]>([]);
  // 输入区的 "@" 选择器所提供的工作区文件/文件夹。
  const [contextEntries, setContextEntries] = useState<WorkspaceEntry[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  const [renamingProject, setRenamingProject] = useState<Project | null>(null);
  const [renameProjectText, setRenameProjectText] = useState("");
  const [providerCount, setProviderCount] = useState(0);
  const [providerList, setProviderList] = useState<ModelProvider[]>([]);
  const [search, setSearch] = useState("");
  const [renaming, setRenaming] = useState<ChatSession | null>(null);
  const [renameText, setRenameText] = useState("");
  const [sessionUsage, setSessionUsage] = useState<UsageInfo | null>(null);
  const [statsOpen, setStatsOpen] = useState(false);
  const [stats, setStats] = useState<SessionStats | null>(null);
  const [autoSpeak, setAutoSpeak] = useState(false);
  const [globalExecutionDisplay, setGlobalExecutionDisplay] = useState<ExecutionDisplayMode>("auto");
  const [globalWorkspace, setGlobalWorkspace] = useState("");
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [sidebarWidth, setSidebarWidth] = useState(() =>
    parseStoredSidebarWidth(localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY), window.innerWidth));
  const [resizingSidebar, setResizingSidebar] = useState(false);
  // 其「展开显示」已打开的项目键。其他每个项目的前 5
  // 个聊天保持可见，而不会把侧边栏变成手风琴。
  const [expandedProjectKeys, setExpandedProjectKeys] = useState<Set<string>>(() => new Set());
  // 项目行会独立于上方五个会话的预览记住其折叠状态：点击某一行会关闭整个子树。
  const [collapsedGroupKeys, setCollapsedGroupKeys] = useState<Set<string>>(() => new Set());
  const expandedOwnerRef = useRef("");
  const [resolvedWorkspace, setResolvedWorkspace] = useState("");
  const [workspaceError, setWorkspaceError] = useState("");
  const [workspacePanelOpen, setWorkspacePanelOpen] = useState(false);
  const [workspaceRefresh, setWorkspaceRefresh] = useState(0);
  const [editing, setEditing] = useState(false);
  // 设置保存新会话的默认值；每个会话拥有自己的当前策略。输入区读取/写入该会话中
  // 的这对值，绝不读取/写入默认值。
  const [defaultExecutionPolicy, setDefaultExecutionPolicy] = useState<ExecutionPolicy>(DEFAULT_EXECUTION_POLICY);
  const [executionPolicyBusy, setExecutionPolicyBusy] = useState(false);
  // 风险对话框可以在会话切换期间保持打开；回调在操作可见聊天之前会与实时 ID
  // 进行比对。
  const activeIDRef = useRef(activeID);
  // 布局时序很重要：流式订阅必须在历史记录 effect
  // 开始排队其事件之前看到新的活动会话。
  useLayoutEffect(() => { activeIDRef.current = activeID; }, [activeID]);
  const [ctxBannerDismissed, setCtxBannerDismissed] = useState(false);
  const [pendingAtts, setPendingAtts] = useState<AttachmentIn[]>([]);
  const fileRef = useRef<HTMLInputElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const scrollFrame = useRef<number | null>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const messagesRef = useRef<UIMessage[]>([]);
  useEffect(() => { messagesRef.current = messages; }, [messages]);
  const sessionsRef = useRef<ChatSession[]>([]);
  useEffect(() => { sessionsRef.current = sessions; }, [sessions]);
  const autoSpeakRef = useRef(autoSpeak);
  useEffect(() => { autoSpeakRef.current = autoSpeak; }, [autoSpeak]);
  const sidebarWidthRef = useRef(sidebarWidth);
  useEffect(() => { sidebarWidthRef.current = sidebarWidth; }, [sidebarWidth]);
  // 在 pointerdown
  // 时获取快照：拖拽根据指针增量而非实时状态计算宽度，因此快速拖拽不会累积误差
  // 。
  const sidebarDragRef = useRef<{ startX: number; startWidth: number } | null>(null);
  const sidebarResizeFrame = useRef<number | null>(null);
  const pendingSidebarWidthRef = useRef<number | null>(null);
  // 当流式响应发出内联错误时设置，这样 send() 的 catch 也就不会通过 toast
  // 提示同一失败。
  const streamedErrorRef = useRef(false);
  // 当当前回合实际开始（用户气泡到达）时设置。后端会以 error+done 和一个已
  // resolve 的 promise 拒绝超出 token
  // 预算的回合——没有这个标志，输入的文本会消失得无影无踪。
  const turnStartedRef = useRef(false);
  // 在此窗口中启动了回合的会话。Chat.IsRunning
  // 可能与后端的运行注册发生竞态；探测结果为 false
  // 时，不得清除本地乐观运行状态并使实时回合看起来已经结束。
  const localRunsRef = useRef<Set<string>>(new Set());
  // 新会话由 send() 创建，尚无持久化历史。紧跟在 setActiveID 之后发出的
  // GetSessionMessages 可能在 ADK
  // 存储首个用户事件之前返回，且不得清除实时时间线。
  const freshSessionsRef = useRef<Set<string>>(new Set());
  // 只能有一个活动历史读取拥有消息窗格。在其进行期间到达的事件会在快照落地后重
  // 放。
  const historyLoadingSessionRef = useRef("");
  const historyGenerationRef = useRef(0);
  const pendingActivationRef = useRef("");
  const pendingStreamRef = useRef<Map<string, ChatStreamEvent[]>>(new Map());
  const flushPendingStreamEventsRef = useRef<(sessionID: string) => void>(() => {});
  // 本地用户气泡在 Chat.Send 到达后端之前渲染。
  // 其事件会确认/替换此条目，而不是追加重复项。
  const pendingUserRef = useRef<Map<string, PendingUserTurn>>(new Map());

  // markRunning 按会话跟踪进行中的回合，因此多个会话可以
  // 并行流式传输而不会相互阻塞。
  const markRunning = useCallback((id: string, on: boolean) => {
    setRunningMap((prev) => {
      if (!!prev[id] === on) return prev;
      const next = { ...prev };
      if (on) next[id] = true;
      else delete next[id];
      return next;
    });
  }, []);

  const active = sessions.find((s) => s.id === activeID);
  const activeExecutionPolicy = resolveExecutionPolicy(
    active?.approvalPolicy,
    active?.sandboxProfile,
    active?.sandboxMode,
    defaultExecutionPolicy,
  );
  const composerExecutionPolicy: ExecutionPolicy = activeID
    ? activeExecutionPolicy
    : {
        approvalPolicy: normalizeApprovalPolicy(draftApprovalPolicy || defaultExecutionPolicy.approvalPolicy),
        sandboxProfile: normalizeSandboxProfile(draftSandboxProfile || defaultExecutionPolicy.sandboxProfile),
      };
  const composerProfile = permissionProfileForPolicy(composerExecutionPolicy);
  const composerApprovalOption = approvalPolicyOption(composerExecutionPolicy.approvalPolicy);
  const composerSandboxOption = sandboxProfileOption(composerExecutionPolicy.sandboxProfile);
  const composerPolicyHint = executionPolicyHint(composerExecutionPolicy);
  const composerPolicyLabel = composerProfile
    ? composerProfile.label
    : `自定义（审批：${composerApprovalOption.label}；沙箱：${composerSandboxOption.label}）`;
  const ComposerProfileIcon = composerProfile
    ? PERMISSION_PROFILE_ICONS[composerProfile.value]
    : SlidersHorizontal;
  const executionDisplayMode = normalizeExecutionDisplayMode(globalExecutionDisplay);

  // fireQueued
  // 会弹出该会话的排队消息并自动发送（既用于回合结束之时，也用于用户返回到已结
  // 束的会话之时）。仍在运行的会话会保留其队列；消息会使用它自己的会话 ID
  // 投递，因此在计时器运行期间切换聊天不会导致其被错误路由。
  const fireQueued = useCallback((sessionID: string) => {
    const list = queuedRef.current[sessionID];
    if (!list || list.length === 0) return;
    if (runningMapRef.current[sessionID]) return;
    const [first, ...rest] = list;
    setQueued((prev) => ({ ...prev, [sessionID]: rest }));
    setTimeout(() => sendRef.current(first.text, sessionID, first.reasoning), 80);
  }, []);

  const reloadSessions = useCallback(() => {
    Chat.ListSessions()
      .then((ss) => setSessions(ss ?? []))
      .catch((e) => toast.error(`加载会话失败: ${errText(e)}`));
  }, []);
  const reloadProjects = useCallback(() => {
    Chat.ListProjects()
      .then((ps) => setProjects(ps ?? []))
      .catch(() => { /* project list is auxiliary; the chat list still loads */ });
  }, []);
  // 供全局 ⌘N 处理器使用的最新直接会话创建函数；该处理器的监听 effect
  // 在此定义进入渲染作用域之前就已注册。
  const createDirectSessionRef = useRef<() => void>(() => {});
  // 新聊天始终为直接模式（无目标）：回合在默认供应商的模型上运行，且开头的
  // @助手 提及会将各个回合路由到 Agent、团队或远程 A2A
  // Agent。没有对话框，也没有预先配置。
  // 「新聊天」和项目的「+」仅打开本地草稿；会话由 send()
  // 在首条消息实际发出时创建（issue #30、issue #60 ——
  // 项目路径过去每点击一次就会持久化一行）。
  const openNewChatDraft = useCallback((projectID: string) => {
    setActiveID("");
    setNewChatDraft(true);
    setDraftProjectID(projectID);
    setMessages([]);
    setFreshInput("");
    setEditing(false);
    setSessionUsage(null);
    setDraftModelOverride("");
    setDraftApprovalPolicy("");
    setDraftSandboxProfile("");
  }, []);
  const createDirectSession = useCallback(() => openNewChatDraft(""), [openNewChatDraft]);
  createDirectSessionRef.current = createDirectSession;
  const createProjectSession = useCallback(
    (projectID: string) => {
      setCollapsedGroupKeys((prev) => {
        const key = `proj:${projectID}`;
        if (!prev.has(key)) return prev;
        const next = new Set(prev);
        next.delete(key);
        return next;
      });
      openNewChatDraft(projectID);
    },
    [openNewChatDraft],
  );
  useEffect(() => {
    reloadSessions();
    reloadProjects();
    Config.GetConfig()
      .then((c) => {
        const settings = c.settings as Settings | undefined;
        setAgents(c.agents ?? []);
        setTeams(c.teams ?? []);
        setRemotes(c.remoteAgents ?? []);
        setProviderCount((c.providers ?? []).length);
        setProviderList(c.providers ?? []);
        setAutoSpeak(!!settings?.autoSpeak);
        setGlobalExecutionDisplay(normalizeExecutionDisplayMode(settings?.executionDisplayMode));
        setDefaultExecutionPolicy(resolveExecutionPolicy(
          settings?.approvalPolicy,
          settings?.sandboxProfile,
          settings?.sandboxMode,
        ));
        setGlobalWorkspace(settings?.workspaceDir ?? "");
        setSidebarCollapsed(!!settings?.sidebarCollapsed);
      })
      .catch((e) => toast.error(`加载配置失败: ${errText(e)}`));
  }, [reloadSessions]);

  // “/”技能遵循当前活动聊天的有效工作区。后端会将项目/全局 .agents/skills
  // 的发现结果与 config.json
  // 的技能合并，因此项目技能无需手动导入即可调用。当工作区在首个回合之前被重新
  // 绑定时会重新运行（resolvedWorkspace）。
  useEffect(() => {
    let cancelled = false;
    effectiveSkills(activeID, activeID ? "" : draftProjectID)
      .then((list) => {
        if (!cancelled) setSkills(list);
      })
      .catch(() => { /* keep the last known list */ });
    return () => { cancelled = true; };
  }, [activeID, draftProjectID, resolvedWorkspace]);

  // “@”选择器索引：与斜杠技能的工作区解析相同。
  useEffect(() => {
    let cancelled = false;
    listContextPaths(activeID, activeID ? "" : draftProjectID)
      .then((list) => {
        if (!cancelled) setContextEntries(list);
      })
      .catch(() => {
        if (!cancelled) setContextEntries([]);
      });
    return () => { cancelled = true; };
  }, [activeID, draftProjectID, resolvedWorkspace]);

  useEffect(() => {
    if (!activeID) {
      setResolvedWorkspace("");
      setWorkspaceError("");
      return;
    }
    let cancelled = false;
    setResolvedWorkspace("");
    setWorkspaceError("");
    Chat.SessionWorkspace(activeID)
      .then((ws) => {
        if (!cancelled) setResolvedWorkspace(ws);
      })
      .catch((e) => {
        if (cancelled) return;
        const msg = errText(e);
        setWorkspaceError(msg);
        toast.error(`任务工作区不可用: ${msg}`);
      });
    return () => {
      cancelled = true;
    };
  }, [activeID, active?.workspaceDir, active?.projectId]);

  // 首次加载时打开最新的会话——除非「新聊天」有意让页面停留在未保存的草稿上（iss
  // ue #30）。任何会话变为活动状态都会结束草稿模式。
  useEffect(() => {
    if (shouldOpenLatestSession({ activeID, draft: newChatDraft, sessionCount: sessions.length })) {
      setActiveID(sessions[0].id);
    }
  }, [sessions, activeID, newChatDraft]);

  useEffect(() => {
    if (activeID) {
      setNewChatDraft(false);
      setDraftProjectID("");
    }
  }, [activeID]);

  // 筛选器是会话作用域的：另一个聊天遗留的查询会在这里呈现为无法解释的空列表。
  useEffect(() => {
    setMsgFilter("");
    if (!activeID) return;
    setUnread((prev) => {
      if (!prev[activeID]) return prev;
      const next = { ...prev };
      delete next[activeID];
      return next;
    });
  }, [activeID]);

  // 切换会话时加载历史记录。layout effect 可避免旧对话在新标题下多渲染一帧。
  useLayoutEffect(() => {
    if (!activeID) return;
    // 防止 A->B 快速切换：上一个会话的慢响应不得把它的消息渲染到新会话之下。
    let cancelled = false;
    setSessionUsage(null);
    setCtxBannerDismissed(false);
    // 丢弃所有待处理的流式文本 —— 它属于上一个会话；其完整内容通过最终消息事件
    // / 持久化历史送达。
    const buf = streamBufRef.current;
    if (buf.timer != null) clearTimeout(buf.timer);
    buf.author = null;
    buf.text = "";
    buf.timer = null;
    const tbuf = thinkingBufRef.current;
    if (tbuf.timer != null) clearTimeout(tbuf.timer);
    tbuf.author = null;
    tbuf.text = "";
    tbuf.timer = null;
    thinkingStreamedRef.current = null;
    thinkingOpenRef.current = null;
    const requestGeneration = ++historyGenerationRef.current;
    const fresh = freshSessionsRef.current.delete(activeID);
    pendingActivationRef.current = "";
    setRenderLimit(RENDER_LIMIT);
    setActiveTurnId(null);
    setShowHistorySkeleton(false);
    let skeletonTimer: number | null = null;

    if (fresh) {
      // 延迟创建的会话尚无持久化历史。实时回合（包括其乐观的用户气泡）是唯一事
      // 实来源；在这里读取空快照正是过去会抹掉首个回合的竞态。
      historyLoadingSessionRef.current = "";
      setHistoryLoading(false);
      flushPendingStreamEventsRef.current(activeID);
    } else {
      // 返回的会话会立即渲染其缓存快照。首次访问从空开始，仅在本地获取缓慢时才
      // 显示骨架屏。
      const cached = historyCacheRef.current.get(activeID);
      setMessages(cached ?? []);
      setHistoryLoading(!cached);
      historyLoadingSessionRef.current = activeID;
      skeletonTimer = cached
        ? null
        : window.setTimeout(() => setShowHistorySkeleton(true), 160);
      Chat.GetSessionMessages(activeID)
        .then((ms) => {
          if (cancelled || !historySnapshotIsCurrent({
            requestSessionID: activeID,
            activeSessionID: activeIDRef.current,
            requestGeneration,
            currentGeneration: historyGenerationRef.current,
          })) return;
          // 把 confirm_request 与其 confirm_result
          // 配对，使待处理的审批保持可交互（例如应用重启后）。
          const answered = new Map<string, boolean>();
          for (const m of ms ?? []) {
            if (m.kind === "confirm_result" && m.callId) {
              answered.set(m.callId, m.toolResp?.confirmed === true);
            }
          }
          const nextMessages = (ms ?? [])
            .filter((m) => m.kind !== "confirm_result")
            .map((m) => ({
              id: m.id,
              kind: (m.kind === "user" ? "user" : m.kind === "thinking" ? "thinking" : m.kind === "tool_call" ? "tool_call" : m.kind === "tool_result" ? "tool_result" : m.kind === "confirm_request" ? "confirm" : "assistant") as UIMessage["kind"],
              author: m.author,
              text: m.text,
              toolName: m.toolName,
              toolArgs: m.toolArgs,
              toolResp: m.toolResp,
              callId: m.callId,
              hint: m.hint,
              confirmStatus: m.kind === "confirm_request" ? (answered.has(m.callId!) ? (answered.get(m.callId!) ? "approved" : "rejected") : "pending") as UIMessage["confirmStatus"] : undefined,
              timestamp: m.timestamp,
              attachments: m.attachments ?? null,
              reasoning: m.reasoning,
            }));
          const pendingUser = pendingUserRef.current.get(activeID) ?? null;
          const reconciled = reconcilePendingUser(nextMessages, pendingUser, activeID);
          if (reconciled.confirmedFromHistory && pendingUser) {
            pendingUserRef.current.set(activeID, {
              ...pendingUser,
              confirmedFromHistory: true,
            });
          }
          // 仅缓存权威快照。如果用户在其持久化之前返回，临时用户气泡会被再次协
          // 调。
          rememberHistory(historyCacheRef.current, activeID, nextMessages);
          setMessages(reconciled.messages);
          historyLoadingSessionRef.current = "";
          flushPendingStreamEventsRef.current(activeID);
          // 进入会话时始终会定位到其最新消息——否则共享的 ScrollArea
          // 会保留上一个会话的
          // scrollTop，可能使较长的历史记录在打开时停在其最旧的消息处。
          requestAnimationFrame(() => bottomRef.current?.scrollIntoView({ block: "end" }));
        })
        .catch((e) => {
          if (!cancelled && historySnapshotIsCurrent({
            requestSessionID: activeID,
            activeSessionID: activeIDRef.current,
            requestGeneration,
            currentGeneration: historyGenerationRef.current,
          })) {
            toast.error(`加载历史失败: ${errText(e)}`);
            historyLoadingSessionRef.current = "";
            flushPendingStreamEventsRef.current(activeID);
          }
        })
        .finally(() => {
          if (skeletonTimer != null) window.clearTimeout(skeletonTimer);
          if (!cancelled && historySnapshotIsCurrent({
            requestSessionID: activeID,
            activeSessionID: activeIDRef.current,
            requestGeneration,
            currentGeneration: historyGenerationRef.current,
          })) {
            setHistoryLoading(false);
            setShowHistorySkeleton(false);
          }
        });
    }
    Chat.IsRunning(activeID).then((r) => {
      if (cancelled) return;
      // 探测是异步的，可能会在 Chat.Send 到达后端的 registerRun
      // 之前完成。绝不能让那个过期的 false 覆盖此窗口刚刚启动的回合。
      if (!r && localRunsRef.current.has(activeID)) return;
      markRunning(activeID, r);
      // 因切换会话而被搁置的排队消息（其回合已在后台完成）现在会发出：用户已返
      // 回，且没有任何内容正在运行。
      if (!r && (queuedRef.current[activeID]?.length ?? 0) > 0) {
        fireQueued(activeID);
      }
    }).catch(() => {});
    // 用持久化的聚合数据初始化
    // token/上下文读数，以便打开已有聊天时能在下一回合完成前显示其用量。实时回
    // 合会在其 done 事件中用相同的累计数值覆盖此值。
    Chat.SessionStats(activeID)
      .then((st) => {
        if (cancelled || !st || st.tokensTotal <= 0) return;
        setSessionUsage({
          promptTokens: st.tokensIn,
          completionTokens: st.tokensOut,
          totalTokens: st.tokensTotal,
          contextTokens: st.contextTokens,
          cachedTokens: st.cachedTokens,
        });
      })
      .catch(() => { /* stats are best-effort */ });
    return () => {
      cancelled = true;
      if (skeletonTimer != null) window.clearTimeout(skeletonTimer);
      if (historyLoadingSessionRef.current === activeID) historyLoadingSessionRef.current = "";
      const discarded = pendingStreamRef.current.get(activeID);
      if (discarded?.some((ev) => ev.kind === "done")) {
        pendingUserRef.current.delete(activeID);
      }
      pendingStreamRef.current.delete(activeID);
      setHistoryLoading(false);
      setShowHistorySkeleton(false);
    };
  }, [activeID]);

  // 当后端在带外更改会话元数据（由模型异步生成的标题）时，会触发此事件，以便侧
  // 边栏接收到它。
  useEffect(() => {
    const off = Events.On("sessions:updated", () => reloadSessions());
    return off;
  }, [reloadSessions]);

  // 流式文本会被合并：delta 事件会累积在 ref 中，并且每个间隔最多刷新到 state
  // 一次，因此快速的 token 流每个间隔只产生一次渲染，而不是每个 token
  // 一次渲染。任何非 delta
  // 事件都会先刷新，以保留同一回合内的顺序；切换会话会丢弃缓冲区（最终消息事件
  // / 持久化历史会携带完整文本）。
  const STREAM_FLUSH_MS = 60;
  const streamBufRef = useRef<{ author: string | null; text: string; timer: number | null }>({
    author: null, text: "", timer: null,
  });
  const flushStreamBuf = useCallback(() => {
    const buf = streamBufRef.current;
    if (buf.timer != null) {
      clearTimeout(buf.timer);
      buf.timer = null;
    }
    const { author, text } = buf;
    if (!author || !text) return;
    buf.author = null;
    buf.text = "";
    setMessages((prev) => {
      const last = prev[prev.length - 1];
      if (!last || !last.streaming || last.author !== author) return prev;
      const copy = [...prev];
      copy[copy.length - 1] = { ...last, text: last.text + text };
      return copy;
    });
  }, []);

  // 推理内容通过自己的通道流式传输，并位于单独的可折叠气泡中，因此模型的草稿内
  // 容绝不会混入答案文本。
  const thinkingBufRef = useRef<{ author: string | null; text: string; timer: number | null }>({
    author: null, text: "", timer: null,
  });
  // 跟踪其 thinking_delta 流式传输仍在进行中的作者：最终 thinking
  // 事件会用它来决定是替换该块还是追加一个新块。
  const thinkingStreamedRef = useRef<string | null>(null);
  // 跟踪该块是否仍处于打开状态；finishThinking 会在每个答案 token
  // 上运行，因此首次关闭后不得重新扫描列表。
  const thinkingOpenRef = useRef<string | null>(null);
  const flushThinkingBuf = useCallback(() => {
    const buf = thinkingBufRef.current;
    if (buf.timer != null) {
      clearTimeout(buf.timer);
      buf.timer = null;
    }
    const { author, text } = buf;
    if (!author || !text) return;
    buf.author = null;
    buf.text = "";
    setMessages((prev) => {
      let idx = prev.length - 1;
      while (idx >= 0 && !(prev[idx].kind === "thinking" && prev[idx].streaming && prev[idx].author === author)) idx--;
      if (idx < 0) {
        return insertThinkingBeforeStreamingReply(prev, {
          id: nextId(), kind: "thinking", author, text, streaming: true, timestamp: new Date().toISOString(),
        });
      }
      const copy = [...prev];
      copy[idx] = { ...copy[idx], text: copy[idx].text + text };
      return copy;
    });
  }, []);
  const finishThinking = useCallback((author: string) => {
    if (thinkingOpenRef.current !== author) return;
    flushThinkingBuf();
    thinkingOpenRef.current = null;
    setMessages((prev) => {
      let idx = prev.length - 1;
      while (idx >= 0 && !(prev[idx].kind === "thinking" && prev[idx].streaming && prev[idx].author === author)) idx--;
      if (idx < 0) return prev;
      const copy = [...prev];
      copy[idx] = { ...copy[idx], streaming: false };
      return copy;
    });
  }, [flushThinkingBuf]);

  const applyChatStreamEvent = useCallback((ev: ChatStreamEvent, sessionID: string) => {
    if (ev.sessionId !== sessionID) return;
    // 重放事件会绕过网络封装，因此保证顺序的刷新应放在这里：排队的 delta
    // 必须在其最终的 message/tool 事件之前落地。
    if (ev.kind !== "delta") flushStreamBuf();
    if (ev.kind !== "thinking_delta") flushThinkingBuf();
    if (ev.kind !== "error" && ev.kind !== "done") turnStartedRef.current = true;
    if (ev.kind === "tool_result") setWorkspaceRefresh((n) => n + 1);
    if (ev.kind !== "thinking_delta" && ev.kind !== "thinking") finishThinking(ev.author);
    if (ev.kind === "thinking_delta") {
      const buf = thinkingBufRef.current;
      if (buf.author && buf.author !== ev.author) flushThinkingBuf();
      thinkingStreamedRef.current = ev.author;
      thinkingOpenRef.current = ev.author;
      buf.author = ev.author;
      buf.text += ev.text ?? "";
      if (buf.timer == null) {
        buf.timer = setTimeout(flushThinkingBuf, STREAM_FLUSH_MS) as unknown as number;
      }
      // 首个片段到达后立即显示该块（及其加载指示器）；刷新 tick 会填入文本。
      setMessages((prev) => {
        let idx = prev.length - 1;
        while (idx >= 0 && !(prev[idx].kind === "thinking" && prev[idx].streaming && prev[idx].author === ev.author)) idx--;
        if (idx >= 0) return prev;
        return insertThinkingBeforeStreamingReply(prev, {
          id: nextId(), kind: "thinking", author: ev.author, text: "", streaming: true, timestamp: new Date().toISOString(),
        });
      });
      return;
    }
    if (ev.kind === "delta") {
      // 回答开始意味着推理阶段已结束：在首个 token 落入回复气泡之前折叠思考块。
      finishThinking(ev.author);
      // 合并：缓冲文本，并确保该 author 的流式气泡存在；缓冲的文本会在刷新 tick
      // 时落地。
      const buf = streamBufRef.current;
      if (buf.author && buf.author !== ev.author) flushStreamBuf();
      buf.author = ev.author;
      buf.text += ev.text ?? "";
      if (buf.timer == null) {
        buf.timer = setTimeout(flushStreamBuf, STREAM_FLUSH_MS) as unknown as number;
      }
      setMessages((prev) => {
        const last = prev[prev.length - 1];
        if (last && last.streaming && last.author === ev.author) return prev;
        return [...prev, { id: nextId(), kind: "assistant", author: ev.author, text: "", streaming: true, timestamp: new Date().toISOString() }];
      });
      return;
    }
    // 在调度状态更新之前捕获待处理的用户。React 之后可能会调用函数式更新器（在
    // StrictMode 下甚至会调用两次）；此时在更新器内读取可变 ref
    // 就可能看到一个已被 done
    // 清除的值，并追加重复的用户气泡。该快照使更新器保持纯函数。
    const pendingUser = ev.kind === "user"
      ? pendingUserRef.current.get(ev.sessionId) ?? null
      : null;
    const pendingUserID = pendingUser && !pendingUser.confirmed && !pendingUser.confirmedFromHistory
      ? pendingUser.message.id
      : "";
    if (pendingUser && pendingUserID) pendingUser.confirmed = true;
    if (ev.kind === "done") pendingUserRef.current.delete(ev.sessionId);
    setMessages((prev) => {
      switch (ev.kind) {
        case "user": {
          if (pendingUser) {
            if (!pendingUserID) return prev;
            const next = replaceMessageByID(prev, pendingUserID, {
              text: ev.text,
              attachments: ev.attachments ?? null,
              reasoning: ev.reasoning,
            });
            return next === prev
              ? [...prev, { ...pendingUser.message, text: ev.text, attachments: ev.attachments ?? null, reasoning: ev.reasoning }]
              : next;
          }
          return [...prev, { id: nextId(), kind: "user", author: "user", text: ev.text, timestamp: new Date().toISOString(), attachments: ev.attachments ?? null, reasoning: ev.reasoning }];
        }
        case "thinking": {
          // 后端会将本次模型调用的每个 thought 部分聚合成一个事件：替换 delta
          // 打开的块（或在供应商仅在最终响应中返回推理时追加一个块）。
          const fromStream = thinkingStreamedRef.current === ev.author;
          thinkingStreamedRef.current = null;
          thinkingOpenRef.current = null;
          let idx = prev.length - 1;
          while (idx >= 0 && !(prev[idx].kind === "thinking" && prev[idx].author === ev.author)) idx--;
          if (fromStream && idx >= 0) {
            const copy = [...prev];
            copy[idx] = { ...copy[idx], text: ev.text || copy[idx].text, streaming: false };
            return copy;
          }
          if (!ev.text) return prev;
          return insertThinkingBeforeStreamingReply(prev, {
            id: nextId(), kind: "thinking", author: ev.author, text: ev.text, streaming: false, timestamp: new Date().toISOString(),
          });
        }
        case "message": {
          const idx = [...prev].reverse().findIndex((m) => m.streaming && m.author === ev.author);
          if (idx >= 0) {
            const real = prev.length - 1 - idx;
            const copy = [...prev];
            copy[real] = {
              ...copy[real],
              text: ev.text,
              streaming: false,
              usage: ev.usage ?? null,
              timestamp: new Date().toISOString(),
            };
            return copy;
          }
          return [...prev, { id: nextId(), kind: "assistant", author: ev.author, text: ev.text, usage: ev.usage ?? null, timestamp: new Date().toISOString() }];
        }
        case "tool_call":
            return [...prev, { id: nextId(), kind: "tool_call", author: ev.author, text: "", toolName: ev.toolName, toolArgs: ev.toolArgs, callId: ev.callId, timestamp: new Date().toISOString() }];
        case "tool_result":
            return [...prev, { id: nextId(), kind: "tool_result", author: ev.author, text: "", toolName: ev.toolName, toolResp: ev.toolResp, callId: ev.callId, timestamp: new Date().toISOString() }];
        case "confirm_request":
          return [...prev, {
            id: nextId(), kind: "confirm", author: ev.author, text: "",
            toolName: ev.toolName, toolArgs: ev.toolArgs,
            callId: ev.callId, hint: ev.hint, confirmStatus: "pending",
            timestamp: new Date().toISOString(),
          } as UIMessage];
        case "error":
          return [...prev, { id: nextId(), kind: "error", author: "system", text: ev.text }];
        case "done": {
          thinkingStreamedRef.current = null;
          thinkingOpenRef.current = null;
          const completedAt = new Date().toISOString();
          return prev.map((m) => (m.streaming ? { ...m, streaming: false, timestamp: completedAt } : m));
        }
        default:
          return prev;
      }
    });
    if (ev.kind === "done") {
      if (!streamedErrorRef.current) turnStartedRef.current = true;
      setSessionUsage(ev.sessionUsage ?? null);
      setWorkspaceRefresh((n) => n + 1);
      // 通过 ref
      // 读取最新的标志：此订阅每个会话只创建一次，否则会捕获启动时的值。
      if (autoSpeakRef.current) {
        const lastAssistant = [...messagesRef.current].reverse().find((m) => m.kind === "assistant" && m.text);
        if (lastAssistant) speak(lastAssistant.text);
      }
    }
  }, [finishThinking, flushStreamBuf, flushThinkingBuf]);

  const flushPendingStreamEvents = useCallback((sessionID: string) => {
    const pending = pendingStreamRef.current.get(sessionID);
    if (!pending || pending.length === 0) return;
    pendingStreamRef.current.delete(sessionID);
    for (const ev of pending) applyChatStreamEvent(ev, sessionID);
  }, [applyChatStreamEvent]);
  flushPendingStreamEventsRef.current = flushPendingStreamEvents;

  const handleChatStream = useCallback((ev: ChatStreamEvent) => {
    const activeSession = activeIDRef.current;
    const foreground = ev.sessionId === activeSession || ev.sessionId === pendingActivationRef.current;
    // 完成状态记录会对每一个会话执行：即使用户正处于另一个聊天中，后台运行也必
    // 须清除其 spinner 和 toast。
    if (ev.kind === "done" || ev.kind === "error") {
      if (ev.kind === "error" && foreground) streamedErrorRef.current = true;
      // 排在历史记录加载之后的 done 事件，不得在排队的 user
      // 事件有机会确认它之前清除乐观用户；重放的 done
      // 分支会在所有事件落地后清除它。
      const keepPendingForReplay = foreground && (
        ev.sessionId === pendingActivationRef.current
        || historyLoadingSessionRef.current === ev.sessionId
      );
      if (ev.kind === "done" && !keepPendingForReplay) {
        pendingUserRef.current.delete(ev.sessionId);
      }
      localRunsRef.current.delete(ev.sessionId);
      markRunning(ev.sessionId, false);
      reloadSessions();
      // 排队消息会在回合一结束就触发——包括在出错之后，因此用户的后续消息绝不会
      // 被搁置。
      if ((queuedRef.current[ev.sessionId]?.length ?? 0) > 0 && foreground) {
        fireQueued(ev.sessionId);
      }
      if (!foreground) {
        const title = sessionsRef.current.find((x: { id: string; title: string }) => x.id === ev.sessionId)?.title ?? "后台会话";
        // 点击 toast 会直接跳转到已完成的后台聊天。
        const open = () => setActiveID(ev.sessionId);
        const action = { label: "查看", onClick: open };
        if (ev.kind === "done") toast.success(`「${title}」已完成回复`, { action });
        else toast.error(`「${title}」本轮出错`, { action });
        setUnread((prev) => ({ ...prev, [ev.sessionId]: true }));
      }
    }
    if (ev.kind === "user") {
      localRunsRef.current.add(ev.sessionId);
      markRunning(ev.sessionId, true);
    }
    if (!foreground) return;
    const awaitingActivation = ev.sessionId === pendingActivationRef.current
      && ev.sessionId !== activeSession;
    if (awaitingActivation || historyLoadingSessionRef.current === ev.sessionId) {
      const queued = pendingStreamRef.current.get(ev.sessionId) ?? [];
      if (queued.length >= 500) queued.shift();
      queued.push(ev);
      pendingStreamRef.current.set(ev.sessionId, queued);
      return;
    }
    applyChatStreamEvent(ev, ev.sessionId);
  }, [applyChatStreamEvent, fireQueued, markRunning, reloadSessions]);

  // 只订阅一次。旧的 effect 会在每次 activeID
  // 变化时重新订阅，从而留下一个后端事件没有监听器的窗口。最新的处理函数通过
  // ref 进行路由，因此订阅本身绝不会反复变动。
  const chatStreamHandlerRef = useRef(handleChatStream);
  useLayoutEffect(() => {
    chatStreamHandlerRef.current = handleChatStream;
  }, [handleChatStream]);
  useEffect(() => {
    const off = Events.On("chat:stream", (e: any) => {
      const ev = Array.isArray(e?.data) ? e.data[0] : e?.data;
      if (ev) chatStreamHandlerRef.current(ev);
    });
    return off;
  }, []);

  // 仅当阅读者已经位于底部时才自动滚动，并按动画帧合并：对每个 token
  // 都平滑滚动会与用户对抗，且动画入队速度快于其完成速度。
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 120;
    if (!nearBottom) return;
    if (scrollFrame.current != null) return;
    scrollFrame.current = requestAnimationFrame(() => {
      scrollFrame.current = null;
      bottomRef.current?.scrollIntoView({ behavior: "auto", block: "end" });
    });
  }, [messages, running]);

  useEffect(() => () => {
    if (scrollFrame.current != null) cancelAnimationFrame(scrollFrame.current);
  }, []);

  // Cmd/Ctrl+N 启动直接聊天；Cmd/Ctrl+K 聚焦输入区。keydown 监听器位于 App
  // 中（帮助面板会在每个页面说明它们）；ChatPage 会响应派发的自定义事件。
  useEffect(() => {
    const onNew = () => createDirectSessionRef.current();
    const onFocus = () => inputRef.current?.focus();
    window.addEventListener("wb-new-chat", onNew);
    window.addEventListener("wb-focus-composer", onFocus);
    return () => {
      window.removeEventListener("wb-new-chat", onNew);
      window.removeEventListener("wb-focus-composer", onFocus);
    };
  }, []);

  // ⌘⇧[ / ⌘⇧]（由 App
  // 派发）按会话列表在侧边栏中的顺序（最近的在前）遍历，并在两端回绕。
  useEffect(() => {
    const step = (dir: number) => {
      if (sessions.length === 0) return;
      const idx = sessions.findIndex((s) => s.id === activeID);
      const next = idx < 0
        ? (dir > 0 ? 0 : sessions.length - 1)
        : (idx + dir + sessions.length) % sessions.length;
      setActiveID(sessions[next].id);
    };
    const onPrev = () => step(-1);
    const onNext = () => step(1);
    window.addEventListener("wb-prev-session", onPrev);
    window.addEventListener("wb-next-session", onNext);
    return () => {
      window.removeEventListener("wb-prev-session", onPrev);
      window.removeEventListener("wb-next-session", onNext);
    };
  }, [sessions, activeID]);

  // 窗口级拖拽防护：在输入区外拖放文件时，不得 让 webview 导航离开应用。
  useEffect(() => {
    const prevent = (e: DragEvent) => e.preventDefault();
    window.addEventListener("dragover", prevent);
    window.addEventListener("drop", prevent);
    return () => {
      window.removeEventListener("dragover", prevent);
      window.removeEventListener("drop", prevent);
    };
  }, []);

  // 未读标记：后台会话在用户身处别处时完成（或出错），
  // 侧边栏会显示一个圆点，直到用户打开该会话。
  const [unread, setUnread] = useState<Record<string, boolean>>({});
  // 尾部条目的渲染数量（工具组只计一次）； 省略提示横幅可以按 RENDER_LIMIT
  // 的步长提高该数量，以便在超长会话中 往回翻页。
  const [renderLimit, setRenderLimit] = useState(RENDER_LIMIT);
  // 阅读位置：未处于底部的状态驱动「回到底部」悬浮按钮。
  const [atBottom, setAtBottom] = useState(true);
  // 回合轨道位置：阅读者当前所在的回合（随滚动跟随；
  // 点击轨道会覆盖该位置，下一次滚动事件会重新同步）。
  const [activeTurnId, setActiveTurnId] = useState<string | null>(null);
  // 底部哨兵是悬浮按钮的权威依据。仅靠滚动监听会漏掉
  // 布局变化（窗口/侧边栏大小变化、输入区展开或收起、筛选/分页、 Markdown
  // 重排），这些变化会移动对话记录的末尾，却不会触发滚动事件。
  useEffect(() => {
    const root = scrollRef.current;
    const target = bottomRef.current;
    if (!root || !target || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver(
      ([entry]) => setAtBottom(entry.isIntersecting),
      { root, rootMargin: "0px 0px 120px 0px", threshold: 0 },
    );
    observer.observe(target);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const onScroll = () => {
      const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 120;
      // 位于视口上边缘之上的最后一个回合标题即为 "current"。
      const elTop = el.getBoundingClientRect().top;
      let current: string | null = null;
      const nodes = el.querySelectorAll<HTMLElement>("[data-turn-anchor]");
      for (const node of nodes) {
        if (node.getBoundingClientRect().top - elTop > 200) break;
        current = node.dataset.turnAnchor ?? current;
      }
      // 在底部时，即使最新回合的标题在较矮视口中位于 200px 扫描线之下，
      // 阅读者仍处于最新回合。
      if (nearBottom && nodes.length > 0) current = nodes[nodes.length - 1].dataset.turnAnchor ?? current;
      if (current == null && nodes.length > 0) current = nodes[0].dataset.turnAnchor ?? null;
      setActiveTurnId(current);
    };
    el.addEventListener("scroll", onScroll, { passive: true });
    return () => el.removeEventListener("scroll", onScroll);
  }, []);
  // 导航栏 / 引用点击：将该回合滚动到可视区域并标记为活跃。
  const selectTurn = useCallback((msgId: string) => {
    setActiveTurnId(msgId);
    document.getElementById(`turn-${msgId}`)?.scrollIntoView({ behavior: "smooth", block: "start" });
  }, []);
  // 会话切换后历史记录正在请求中。快速本地读取不应闪现骨架屏，因此仅在短暂宽限
  // 期后才显示。
  const [historyLoading, setHistoryLoading] = useState(false);
  const [showHistorySkeleton, setShowHistorySkeleton] = useState(false);
  // 返回到已打开过的会话时，会在新请求落地前先绘制其最后一次快照；这消除了切换
  // 时的空白/骨架帧。
  const historyCacheRef = useRef<Map<string, UIMessage[]>>(new Map());
  // 共享的破坏性操作确认对话框（替代 window.confirm）。
  const [confirmRequest, setConfirmRequest] = useState<ConfirmRequest | null>(null);

  // 按会话的消息队列：在回复流式输出期间发送会把文本暂存，并在该回合结束后自动
  // 发送（Codex
  // 风格排队）。一个队列可容纳多条消息——两次排队不得静默丢弃第一条；投递落到队
  // 首，用户发送追加到队尾。
  const [queued, setQueued] = useState<Record<string, QueuedMessage[]>>({});
  const queuedRef = useRef(queued);
  useEffect(() => { queuedRef.current = queued; }, [queued]);
  // 输入框本地的 effort
  // 状态可驱动滑块的实时反馈。活跃聊天会把已提交的级别持久化到会话；草稿则保留
  // 该级别，直到第一条消息创建会话记录。
  const [turnReasoning, setTurnReasoning] = useState("");
  const [intelligenceOpen, setIntelligenceOpen] = useState(false);
  const [intelligenceView, setIntelligenceView] = useState<"effort" | "model">("effort");
  const runningMapRef = useRef(runningMap);
  useEffect(() => { runningMapRef.current = runningMap; }, [runningMap]);

  const send = async (override?: string, targetID?: string, reasoningOverride?: string) => {
    // targetID 让 fireQueued 将消息投递到其被排队时所对应的会话，而不是 80ms
    // 后恰好处于活跃状态的聊天。
    let sid = targetID ?? activeID;
    const text = (override ?? input).trim();
    if (!text) return;
    // 本次发送是否源自活跃的输入框（或尚待创建的聊天）：仅在这种情况下，待处理
    // 的图片附件才属于它。
    const fromComposer = !sid || sid === activeID;
    // 排队投递会携带其入队时所选的级别；从输入框发送时，会在按下发送的那一刻读
    // 取当前滑块级别。
    const reasoning = reasoningOverride !== undefined ? reasoningOverride : fromComposer ? turnReasoning : "";
    // 输入框优先流程：尚无会话时，第一条消息会即时创建一个直接聊天。仅需要供应
    // 商——绝不需要 Agent。
    if (!sid) {
      if (providerCount === 0) {
        toast.error("请先在「模型配置」页添加供应商并填入 API Key");
        return;
      }
      const draftPolicy: ExecutionPolicy = {
        approvalPolicy: normalizeApprovalPolicy(draftApprovalPolicy || defaultExecutionPolicy.approvalPolicy),
        sandboxProfile: normalizeSandboxProfile(draftSandboxProfile || defaultExecutionPolicy.sandboxProfile),
      };
      try {
        const s = draftUsesProject(draftProjectID)
          ? await Chat.NewSessionWithProject("", "", "", draftProjectID)
          : await Chat.NewSessionWithWorkspace("", "", "", "");
        sid = s.id;
        // 在刚创建的行成为活动聊天之前，将草稿选择应用到该行：这样输入框随后会
        // 读取已持久化的这对值，而不是与一次在首次渲染之后才落地的写入发生竞态
        // 。
        try {
          if (draftModelOverride) await Chat.SetSessionModel(s.id, draftModelOverride);
          if (reasoning) await Chat.SetSessionReasoningLevel(s.id, reasoning);
          await setSessionExecutionPolicy(
            s.id,
            draftPolicy.approvalPolicy,
            draftPolicy.sandboxProfile,
          );
        } catch (e) {
          // 该行已存在，因此第一回合仍必须发出；应暴露丢失的选择，而不是静默地
          // 假装它已应用。
          toast.error(`应用草稿设置失败: ${errText(e)}`);
        }
        setDraftModelOverride("");
        setDraftApprovalPolicy("");
        setDraftSandboxProfile("");
        setSessions((prev) => [
          {
            ...s,
            approvalPolicy: draftPolicy.approvalPolicy,
            sandboxProfile: draftPolicy.sandboxProfile,
          },
          ...prev.filter((item) => item.id !== s.id),
        ]);
        setFreshInput("");
        setDraftProjectID("");
        freshSessionsRef.current.add(s.id);
        pendingActivationRef.current = s.id;
        setActiveID(s.id);
        setMessages([]);
        reloadSessions();
      } catch (e) {
        toast.error(`创建会话失败: ${errText(e)}`);
        return;
      }
    }
    if (runningMap[sid] && !override) {
      setQueued((prev) => ({ ...prev, [sid]: [...(prev[sid] ?? []), { text, reasoning }] }));
      if (sid === activeID) {
        setInput("");
        setTurnReasoning("");
      }
      toast("已排队：当前回复完成后自动发送");
      return;
    }
    if (runningMap[sid]) {
      // 与刚变为忙碌的会话发生竞态的排队投递会回到队列中（在下一回合结束时再次
      // 触发）——绝不会被丢弃。它们会重新入队到队首，以保留用户的顺序。
      if (override !== undefined) {
        setQueued((prev) => ({ ...prev, [sid]: [{ text, reasoning }, ...(prev[sid] ?? [])] }));
      }
      return;
    }
    // 斜杠命令在本地运行，无需模型往返（仅限活动聊天——发往后台会话的排队发送项
    // 会作为消息发出）。
    if (sid === activeID && text.startsWith("/")) {
      const cmd = text.split(/\s+/)[0].toLowerCase();
      switch (cmd) {
        case "/export":
          setInput("");
          exportSession();
          return;
        case "/exporthtml":
          setInput("");
          exportHTML();
          return;
        case "/stats":
          setInput("");
          openStats();
          return;
        case "/todos": {
          setInput("");
          const planMessage = latestPlanMessage(messages, false);
          const todos = planMessage?.toolResp?.todos;
          if (Array.isArray(todos) && todos.length > 0) {
            const done = todos.filter((t: TodoItem) => t.status === "completed").length;
            const lines = todos.map((t: TodoItem) =>
              `${t.status === "completed" ? "✓" : t.status === "in_progress" ? "▶" : "○"} ${t.content}`,
            );
            toast(`${done}/${todos.length} 完成`, { description: lines.join("\n") });
            return;
          }
          toast.info("当前会话还没有任务计划（Agent 挂载「任务计划」内置工具后可用）");
          return;
        }
        case "/new":
          setInput("");
          createDirectSession();
          return;
        default:
          // "/<skill-name>" 命令会作为普通消息发送：后端仅会将对应技能的
          // SKILL.md 注入本回合（issue
          // #53）。未知标记保持旧约定——提示，不发送。
          if (isSkillCommand(text, skills)) break;
          toast.error(
            `未知命令 ${cmd}。可用：${SLASH_COMMANDS.map((c) => c.cmd).join(" ")}` +
              (skills.length > 0 ? `；或输入 / 选择 ${skills.length} 个 Skill` : ""),
          );
          return;
      }
    }
    if (sid === activeID) setInput("");
    localRunsRef.current.add(sid);
    markRunning(sid, true);
    const wasEditing = editing && sid === activeID;
    setEditing(false);
    // 普通发送会清除临时的滑块值；活动聊天会通过其已持久化的会话级值保留它。编
    // 辑会按其记录的级别重新运行原回合，因此待定的选择仍保持待定。
    if (fromComposer && !wasEditing) setTurnReasoning("");
    const atts = fromComposer ? pendingAtts : [];
    if (fromComposer) setPendingAtts([]);
    turnStartedRef.current = false;
    if (fromComposer) streamedErrorRef.current = false;
    let optimisticUserID = "";
    if (fromComposer && !wasEditing) {
      // 立即显示用户气泡，而不是等待后端的用户事件。该事件稍后会在原地确认这条
      // 确切的消息。
      const optimistic: UIMessage = {
        id: nextId(),
        kind: "user",
        author: "user",
        text,
        timestamp: new Date().toISOString(),
        attachments: atts.map((a) => ({
          name: a.name,
          mime: a.mime,
          dataUrl: `data:${a.mime};base64,${a.data}`,
        })),
        reasoning,
      };
      optimisticUserID = optimistic.id;
      pendingUserRef.current.set(sid, {
        sessionID: sid,
        message: optimistic,
        startedAt: Date.now(),
      });
      // 在本回合仍在进行时重新加载，不得渲染较旧的快照。
      historyCacheRef.current.delete(sid);
      setMessages((prev) => [...prev, optimistic]);
    }
    if (wasEditing) {
      setMessages((prev) => {
        const out = [...prev];
        while (out.length > 0 && (out[out.length - 1].kind === "assistant" || out[out.length - 1].kind === "thinking" || out[out.length - 1].kind === "tool_call" || out[out.length - 1].kind === "tool_result")) {
          out.pop();
        }
        return out;
      });
    }
    try {
      if (wasEditing) {
        await Chat.EditAndResend(sid, text);
      } else {
        await Chat.SendWithReasoning(sid, text, atts.length > 0 ? atts : null, reasoning);
      }
      // Promise 已兑现但回合未启动，意味着后端拒绝了该回合（例如 token
      // 预算耗尽）：恢复用户输入的内容，而不是静默吞掉它。如果 Promise
      // 在流式事件派发之前就已兑现，则保持乐观回合继续运行，直到其 done/error
      // 事件到达。
      const refused = !turnStartedRef.current && streamedErrorRef.current;
      if (refused) {
        localRunsRef.current.delete(sid);
        markRunning(sid, false);
        if (optimisticUserID) {
          setMessages((prev) => dropMessageByID(prev, optimisticUserID));
        }
        if (pendingUserRef.current.get(sid)?.message.id === optimisticUserID) {
          pendingUserRef.current.delete(sid);
        }
      }
      if (fromComposer && refused) {
        setInput((cur) => (cur === "" ? text : cur));
        setPendingAtts((prev) => [...atts, ...prev]);
        if (!wasEditing) setTurnReasoning((cur) => (cur === "" ? reasoning : cur));
      }
      if (fromComposer) streamedErrorRef.current = false;
    } catch (e) {
      // 流式通道已经为运行失败发出了内联错误气泡；Chat.Send 返回同一错误以使
      // promise 结算，但仅当未流式输出任何内容（例如校验失败）时，才显示
      // toast。
      const msg = errText(e);
      if (!streamedErrorRef.current) toast.error(`发送失败: ${msg}`);
      streamedErrorRef.current = false;
      localRunsRef.current.delete(sid);
      markRunning(sid, false);
      if (!turnStartedRef.current && optimisticUserID) {
        setMessages((prev) => dropMessageByID(prev, optimisticUserID));
      }
      if (pendingUserRef.current.get(sid)?.message.id === optimisticUserID) {
        pendingUserRef.current.delete(sid);
      }
      if (fromComposer) {
        // 恢复草稿和附件（合并，而非覆盖：用户可能在请求进行期间又重新开始输入
        // 了）。
        setPendingAtts((prev) => [...atts, ...prev]);
        setInput((cur) => (cur === "" ? text : cur));
      }
    }
  };

  // 流式订阅使用最新的 send：该订阅每个会话仅创建一次，不得触发过期的闭包。
  const sendRef = useRef(send);
  useEffect(() => { sendRef.current = send; });

  const cancel = () => {
    if (activeID) Chat.Cancel(activeID);
  };

  // answerConfirm 用于完成 HITL
  // 工具确认卡片。状态会乐观翻转；恢复失败会将卡片回退为 pending。
  const answerConfirm = useCallback(async (msgId: string, callId: string | undefined, approved: boolean) => {
    if (!activeID || !callId || running) return;
    localRunsRef.current.add(activeID);
    markRunning(activeID, true);
    setMessages((prev) => prev.map((m) => (m.id === msgId ? { ...m, confirmStatus: approved ? "approved" : "rejected" } : m)));
    try {
      await Chat.ConfirmToolCall(activeID, callId, approved);
    } catch (e) {
      if (!streamedErrorRef.current) toast.error(`确认失败: ${errText(e)}`);
      streamedErrorRef.current = false;
      setMessages((prev) => prev.map((m) => (m.id === msgId && m.kind === "confirm" ? { ...m, confirmStatus: "pending" } : m)));
      localRunsRef.current.delete(activeID);
      markRunning(activeID, false);
    }
  }, [activeID, running, markRunning]);

  const applyExecutionPolicy = useCallback(async (next: ExecutionPolicy) => {
    const normalized: ExecutionPolicy = {
      approvalPolicy: normalizeApprovalPolicy(next.approvalPolicy),
      sandboxProfile: normalizeSandboxProfile(next.sandboxProfile),
    };
    if (!activeID) {
      setDraftApprovalPolicy(normalized.approvalPolicy);
      setDraftSandboxProfile(normalized.sandboxProfile);
      if (normalized.sandboxProfile === "danger-full-access") {
        toast.success("已为本会话选择完全访问权限；发送首条消息后生效");
      }
      return;
    }
    if (
      (normalized.approvalPolicy === activeExecutionPolicy.approvalPolicy
        && normalized.sandboxProfile === activeExecutionPolicy.sandboxProfile)
      || executionPolicyBusy
    ) return;
    const sid = activeID;
    const previousPolicy = activeExecutionPolicy;
    setExecutionPolicyBusy(true);
    // 乐观更新：输入区标签必须立即切换，而不是在一次往返之后、菜单仍在关闭时才
    // 切换。
    setSessions((prev) => prev.map((s) => (
      s.id === sid
        ? {
            ...s,
            approvalPolicy: normalized.approvalPolicy,
            sandboxProfile: normalized.sandboxProfile,
          }
        : s
    )));
    try {
      await setSessionExecutionPolicy(
        sid,
        normalized.approvalPolicy,
        normalized.sandboxProfile,
      );
      if (sid === activeIDRef.current) {
        const appliedProfile = permissionProfileForPolicy(normalized);
        toast.success(appliedProfile
          ? `本会话权限：${appliedProfile.label}`
          : `本会话权限：自定义（${approvalPolicyOption(normalized.approvalPolicy).label} / ${sandboxProfileOption(normalized.sandboxProfile).label}）`);
      }
    } catch (e) {
      setSessions((prev) => prev.map((s) => (
        s.id === sid
          ? {
              ...s,
              approvalPolicy: previousPolicy.approvalPolicy,
              sandboxProfile: previousPolicy.sandboxProfile,
            }
          : s
      )));
      toast.error(`切换执行策略失败: ${errText(e)}`);
    } finally {
      setExecutionPolicyBusy(false);
    }
  }, [activeID, activeExecutionPolicy, executionPolicyBusy]);

  const requestPermissionProfile = useCallback((profile: PermissionProfile) => {
    const next = permissionProfilePolicy(profile);
    if (
      (next.approvalPolicy === composerExecutionPolicy.approvalPolicy
        && next.sandboxProfile === composerExecutionPolicy.sandboxProfile)
      || executionPolicyBusy
    ) return;
    if (profile === "danger-full-access") {
      setConfirmRequest({
        title: "切换到完全访问权限？",
        description: "本会话将不再经过 OS 沙箱，可访问任意文件；联网也会隐含开启，审批策略同时设为「从不询问」。",
        confirmText: "切换",
        cancelText: "取消",
        destructive: true,
        action: () => { void applyExecutionPolicy(next); },
      });
      return;
    }
    void applyExecutionPolicy(next);
  }, [applyExecutionPolicy, composerExecutionPolicy, executionPolicyBusy]);

  const MAX_ATT_BYTES = 6 * 1024 * 1024;
  const allowedMIME = ["image/png", "image/jpeg", "image/webp", "image/gif"];

  const addFiles = async (files: FileList | File[]) => {
    let added = 0;
    if (active && !modelMultimodal) {
      // 多模态标记是用户可能尚未开启的元数据：保持上传路径开放，但警告模型可能
      // 拒绝图片。
      toast.warning("当前模型未标记多模态，图片可能无法被识别；可在「模型配置」中开启多模态");
    }
    for (const f of Array.from(files)) {
      if (!allowedMIME.includes(f.type)) {
        toast.error(`不支持的类型 ${f.type || f.name}（仅图片）`);
        continue;
      }
      if (f.size > MAX_ATT_BYTES) {
        toast.error(`${f.name} 超过 6MB`);
        continue;
      }
      let binary = "";
      try {
        const buf = await f.arrayBuffer();
        const bytes = new Uint8Array(buf);
        const chunk = 0x8000;
        for (let i = 0; i < bytes.length; i += chunk) {
          binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
        }
      } catch {
        toast.error(`读取 ${f.name} 失败`);
        continue;
      }
      if (pendingAtts.length + added >= 4) {
        toast.warning("最多附带 4 张图片");
        break;
      }
      added++;
      setPendingAtts((prev) => (prev.length >= 4 ? prev : [...prev, { name: f.name, mime: f.type, data: btoa(binary) }]));
    }
  };

  const regenerate = useCallback(async () => {
    if (!activeID || running) return;
    localRunsRef.current.add(activeID);
    markRunning(activeID, true);
    setSessionUsage(null);
    setMessages((prev) => {
      const out = [...prev];
      while (out.length > 0 && (out[out.length - 1].kind === "assistant" || out[out.length - 1].kind === "thinking" || out[out.length - 1].kind === "tool_call" || out[out.length - 1].kind === "tool_result")) {
        out.pop();
      }
      return out;
    });
    try {
      await Chat.Regenerate(activeID);
    } catch (e) {
      if (!streamedErrorRef.current) toast.error(`重新生成失败: ${errText(e)}`);
      streamedErrorRef.current = false;
      localRunsRef.current.delete(activeID);
      markRunning(activeID, false);
    }
  }, [activeID, running, markRunning]);

  /** speakMessage 在每个调用点都保持稳定，因此经过 memo 记忆化的气泡可以跳过它。 */
  const speakMessage = useCallback((text: string) => speak(text), []);

  const openStats = async () => {
    if (!activeID) return;
    try {
      setStats(await Chat.SessionStats(activeID));
      setStatsOpen(true);
    } catch (e) {
      toast.error(`加载统计失败: ${errText(e)}`);
    }
  };

  const exportSession = async () => {
    if (!activeID) return;
    try {
      const path = await Chat.ExportSession(activeID);
      toast.success(`已导出: ${path}`);
    } catch (e) {
      toast.error(`导出失败: ${errText(e)}`);
    }
  };

  const exportHTML = async () => {
    if (!activeID) return;
    try {
      const path = await Chat.ExportSessionHTML(activeID);
      toast.success(`已导出 HTML: ${path}`);
    } catch (e) {
      toast.error(`导出失败: ${errText(e)}`);
    }
  };

  const copyConversation = async () => {
    if (!activeID || messages.length === 0) return;
    const lines = messages.map((m) => {
      switch (m.kind) {
        case "user":
          return `🧑 用户：${m.text}`;
        case "assistant":
          return `🤖 ${m.author}：${m.text}`;
        case "tool_call":
          return `🔧 调用 ${m.toolName} ${JSON.stringify(m.toolArgs ?? {})}`;
        case "tool_result":
          return `✅ 结果 ${m.toolName} ${JSON.stringify(m.toolResp ?? {})}`;
        case "confirm":
          return `🛡️ 确认 ${m.toolName}（${m.confirmStatus ?? "pending"}）`;
        case "error":
          return `⚠️ ${m.text}`;
        default:
          return "";
      }
    }).filter(Boolean);
    try {
      await navigator.clipboard.writeText(lines.join("\n\n"));
      toast.success("已复制整段对话到剪贴板");
    } catch {
      toast.error("复制失败（剪贴板不可用）");
    }
  };

  const pickWorkspace = async () => {
    try {
      const picked = await Dialogs.OpenFile({
        CanChooseDirectories: true,
        CanChooseFiles: false,
        CanCreateDirectories: true,
        ResolvesAliases: true,
        Title: "选择目录",
        ButtonText: "选择此文件夹",
      });
      return typeof picked === "string" ? picked : "";
    } catch (e) {
      // 关闭原生选择器并非失败：Wails 会将其报告为错误（"cancelled by
      // user"）。仅暴露真正的对话框失败。
      if (isDialogCancelError(e)) return "";
      throw e;
    }
  };

  const addProject = async () => {
    try {
      const picked = await pickWorkspace();
      if (!picked) return;
      const p = await Chat.SaveProject("", "", picked);
      reloadProjects();
      toast.success(`已添加项目「${p.name}」`);
    } catch (e) {
      toast.error(`添加项目失败: ${errText(e)}`);
    }
  };

  const deleteProject = async (p: Project) => {
    setConfirmRequest({
      title: `删除项目「${p.name}」？`,
      description: "其中的对话将回到默认项目，项目文件不会删除。",
      confirmText: "删除",
      action: () => {
        Chat.DeleteProject(p.id)
          .then(() => {
            reloadProjects();
            reloadSessions();
            toast.success(`已删除项目「${p.name}」`);
          })
          .catch((e) => toast.error(`删除项目失败: ${errText(e)}`));
      },
    });
  };

  const forkSession = async (s: ChatSession) => {
    try {
      const fork = await Chat.ForkSession(s.id);
      toast.success(`已分叉: ${fork.title}`);
      reloadSessions();
      setActiveID(fork.id);
      setMessages([]);
    } catch (e) {
      toast.error(`分叉失败: ${errText(e)}`);
    }
  };

  const removeSession = (s: ChatSession) => {
    setConfirmRequest({
      title: `删除会话「${s.title}」？`,
      description: "会话与消息将不可恢复，任务工作区中的文件会保留。",
      confirmText: "删除",
      action: async () => {
        try {
          await Chat.DeleteSession(s.id);
        } catch (e) {
          toast.error(`删除失败: ${errText(e)}`);
        }
        if (s.id === activeID) {
          setActiveID("");
          setMessages([]);
        }
        reloadSessions();
      },
    });
  };

    const [contentMatches, setContentMatches] = useState<Set<string> | null>(null);

  // 防抖的内容搜索：存在查询时，侧边栏会列出标题或存储的历史记录匹配的会话（后
  // 端对事件负载执行
  // LIKE）。单调序列会丢弃过期响应：较早查询的慢速回复不得覆盖最新查询的结果。
  const searchSeqRef = useRef(0);
  useEffect(() => {
    const q = search.trim();
    if (!q) {
      setContentMatches(null);
      return;
    }
    const t = setTimeout(() => {
      const seq = ++searchSeqRef.current;
      Chat.SearchSessions(q)
        .then((ss) => {
          if (seq === searchSeqRef.current) setContentMatches(new Set((ss ?? []).map((x) => x.id)));
        })
        .catch(() => {
          if (seq === searchSeqRef.current) setContentMatches(null);
        });
    }, 250);
    return () => clearTimeout(t);
  }, [search]);

  const [showArchived, setShowArchived] = useState(false);

  const filteredSessions = useMemo(() => {
    const q = search.trim().toLowerCase();
    const matches = (s: ChatSession) => {
      if (!q) return true;
      if (s.title.toLowerCase().includes(q)) return true;
      return contentMatches ? contentMatches.has(s.id) : false;
    };
    // 已归档会话会经过相同的查询过滤；分组会将它们归入末尾的 "已归档"
    // 分组，其可见性由 showArchived 控制。
    return sessions.filter((s) => matches(s));
  }, [sessions, search, contentMatches]);

  const persistSidebarPrefs = (collapsed: boolean) => {
    setSidebarCollapsed(collapsed);
    Config.GetConfig()
      .then((c) => {
        c.settings.sidebarCollapsed = collapsed;
        return Config.SaveSettings(c.settings);
      })
      .catch(() => { /* offline: keep the UI state, retry on next toggle */ });
  };
  const toggleSidebar = useCallback(() => {
    const next = !sidebarCollapsed;
    persistSidebarPrefs(next);
    // 侧轨的固定预览是根据切换前的几何尺寸定位的；关闭它，以免它悬浮在刚加宽的
    // 聊天窗格上方。
    window.dispatchEvent(new CustomEvent("wb-sidebar-toggled"));
  }, [sidebarCollapsed]);
  // ⌘B 是类似 ⌘N 的 shell 级快捷键：App
  // 会派发该自定义事件，使其在视图切换期间仍然有效。
  useEffect(() => {
    const onToggle = () => toggleSidebar();
    window.addEventListener("wb-toggle-sidebar", onToggle);
    return () => window.removeEventListener("wb-toggle-sidebar", onToggle);
  }, [toggleSidebar]);

  const persistSidebarWidth = (width: number) => {
    sidebarWidthRef.current = width;
    setSidebarWidth(width);
    try {
      localStorage.setItem(SIDEBAR_WIDTH_STORAGE_KEY, String(width));
    } catch { /* storage unavailable: keep the in-memory width */ }
  };

  // 分隔条用于拖动侧边栏边缘。指针捕获可让手势在光标离开 6px
  // 手柄时仍保持有效，而 body 级的光标 +
  // 选区抑制可防止拖动选中会话行或聊天区域中的文本（issue #31）。宽度更新通过
  // rAF 进行，因此拖动不会在每次 pointermove 时重新渲染消息列表。
  const commitSidebarWidthFrame = () => {
    sidebarResizeFrame.current = null;
    const next = pendingSidebarWidthRef.current;
    if (next == null) return;
    pendingSidebarWidthRef.current = null;
    sidebarWidthRef.current = next;
    setSidebarWidth(next);
  };
  const startSidebarResize = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    sidebarDragRef.current = { startX: e.clientX, startWidth: sidebarWidth };
    setResizingSidebar(true);
    e.currentTarget.setPointerCapture?.(e.pointerId);
  };
  const moveSidebarResize = (e: React.PointerEvent<HTMLDivElement>) => {
    const drag = sidebarDragRef.current;
    if (!drag) return;
    e.preventDefault();
    pendingSidebarWidthRef.current = clampSidebarWidth(drag.startWidth + (e.clientX - drag.startX), window.innerWidth);
    if (sidebarResizeFrame.current == null) {
      sidebarResizeFrame.current = requestAnimationFrame(commitSidebarWidthFrame);
    }
  };
  const endSidebarResize = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!sidebarDragRef.current) return;
    sidebarDragRef.current = null;
    if (sidebarResizeFrame.current != null) {
      cancelAnimationFrame(sidebarResizeFrame.current);
      commitSidebarWidthFrame();
    }
    pendingSidebarWidthRef.current = null;
    setResizingSidebar(false);
    if (e.currentTarget.hasPointerCapture?.(e.pointerId)) e.currentTarget.releasePointerCapture(e.pointerId);
    persistSidebarWidth(sidebarWidthRef.current);
  };
  const resizeSidebarWithKeyboard = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    e.preventDefault();
    const delta = e.key === "ArrowLeft" ? -SIDEBAR_KEYBOARD_STEP : SIDEBAR_KEYBOARD_STEP;
    persistSidebarWidth(clampSidebarWidth(sidebarWidthRef.current + delta, window.innerWidth));
  };

  useEffect(() => () => {
    if (sidebarResizeFrame.current != null) cancelAnimationFrame(sidebarResizeFrame.current);
  }, []);

  useEffect(() => {
    if (!resizingSidebar) return;
    const prevCursor = document.body.style.cursor;
    const prevUserSelect = document.body.style.userSelect;
    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
    return () => {
      document.body.style.cursor = prevCursor;
      document.body.style.userSelect = prevUserSelect;
    };
  }, [resizingSidebar]);

  // 窗口缩小到低于已存储的宽度时：钳制回可见范围， 而不是让侧边栏挤占聊天区域。
  useEffect(() => {
    const onResize = () => setSidebarWidth((w) => clampSidebarWidth(w, window.innerWidth));
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);

  // 紧凑侧边栏是一棵项目树：置顶聊天先作为一个全局
  // 分组，然后是默认聊天、按稳定创建顺序排列的已注册项目，以及临时工作区文件夹
  // 。每个分组预览五个聊天，并通过「展开显示」
  // 展示其余聊天；已归档聊天留在其专属的末尾分区中。搜索查询会将树折叠成一个扁
  // 平列表。
  const searching = search.trim().length > 0;
  const sidebarGroups = useMemo(
    () => buildSidebarProjectGroups(filteredSessions, projects, globalWorkspace),
    [filteredSessions, projects, globalWorkspace],
  );
  const archivedSessions = useMemo(() => filteredSessions.filter((s) => s.archived), [filteredSessions]);
  const searchResults = useMemo(
    () => [...filteredSessions].sort((a, b) => (b.updatedAt ?? "").localeCompare(a.updatedAt ?? "")),
    [filteredSessions],
  );
  // 选中状态仅属于会话行。浏览某个项目的会话时，项目行保持中性；
  // 唯一的例外是待定的项目草稿，它会在第一个会话行出现之前标记其目标。
  const draftProjectKey = draftProjectID ? `proj:${draftProjectID}` : "";
  // 打开位于五行预览之外的聊天时，必须展开其所属项目； 否则活动行将不可见。
  useEffect(() => {
    if (!activeID) return;
    const owner = sidebarGroups.find((g) => g.sessions.some((s) => s.id === activeID));
    if (!owner) return;
    // 每个活动会话仅展开一次活动项目。此处不得依赖
    // collapsedGroupKeys：否则对活动项目的有意折叠会立即自行撤销。
    if (expandedOwnerRef.current !== activeID) {
      expandedOwnerRef.current = activeID;
      setCollapsedGroupKeys((prev) => {
        if (!prev.has(owner.key)) return prev;
        const next = new Set(prev);
        next.delete(owner.key);
        return next;
      });
    }
    if (!shouldExpandProjectForSession(owner.sessions, activeID)) return;
    setExpandedProjectKeys((prev) => {
      if (prev.has(owner.key)) return prev;
      const next = new Set(prev);
      next.add(owner.key);
      return next;
    });
  }, [activeID, sidebarGroups]);

  // 活动目标的供应商 + 模型（Agent → 其模型；团队 → 自动时为协调者，否则为牵头
  // Agent），用于成本估算和 多模态门控。
  const target = useMemo(() => {
    if (!active) return { provider: null as ModelProvider | null, model: null as ModelInfo | null };
    // 会话级模型覆盖与后端 EffectiveModel 同口径：覆盖 ID 必须在解析出的
    // 供应商模型列表内，否则回退到默认解析（直聊 = 默认供应商的第一个模型）。
    const override = active.modelOverride;
    const withOverride = (provider: ModelProvider | null, fallback: ModelInfo | null): ModelInfo | null => {
      if (override && provider?.models) {
        const hit = provider.models.find((m) => m.id === override);
        if (hit) return hit;
      }
      return fallback;
    };
    if (active.targetType === "") {
      // 直聊：默认供应商的第一个模型（与后端对无目标会话采用的 解析方式相同）。
      const provider = providerList.find((p) => p.isDefault) ?? providerList[0] ?? null;
      return { provider, model: withOverride(provider, provider?.models?.[0] ?? null) };
    }
    let providerId = "";
    let modelId = "";
    if (active.targetType === "team") {
      const team = teams.find((t) => t.id === active.targetId);
      if (team?.autoCoordinate) {
        providerId = team.providerId;
        modelId = team.model;
      } else {
        const lead = agents.find((a) => a.id === team?.leadAgentId);
        const resolved = resolveAgentModel(lead, providerList);
        return { provider: resolved.provider, model: withOverride(resolved.provider, resolved.model) };
      }
    } else {
      const agent = agents.find((a) => a.id === active.targetId);
      const resolved = resolveAgentModel(agent, providerList);
      return { provider: resolved.provider, model: withOverride(resolved.provider, resolved.model) };
    }
    const provider = providerList.find((p) => p.id === providerId) ?? null;
    const model = withOverride(provider, provider?.models?.find((m) => m.id === modelId) ?? null);
    return { provider, model };
  }, [active, agents, teams, providerList]);

  /**
   * modelMultimodal
   * 仅调节附件提示：选择器保持打开，因此用户即使在切换模型的元数据标志之前也能
   * 上传图片。
   */
  const modelMultimodal = !!target.model?.multimodal;

  /** costOf 按模型费率（每 1M）根据 token 用量计算 ¥ 成本。 */
  const costOf = useCallback((u?: UsageInfo | null): string => {
    if (!u || !target.model) return "";
    const pin = target.model.priceIn ?? 0;
    const pout = target.model.priceOut ?? 0;
    if (pin <= 0 && pout <= 0) return "";
    const cost = (u.promptTokens / 1e6) * pin + (u.completionTokens / 1e6) * pout;
    if (cost <= 0) return "";
    return cost < 0.01 ? "<¥0.01" : `¥${cost.toFixed(2)}`;
  }, [target.model]);

  const startEdit = useCallback((text: string) => {
    if (running || !activeID) return;
    setInput(text);
    setEditing(true);
    inputRef.current?.focus();
  }, [running, activeID, setInput]);

  const cancelEdit = () => {
    setEditing(false);
    setInput("");
  };

  const targetLabel = useMemo(() => {
    if (!active) return "";
    if (active.targetType === "") return "助手";
    if (active.targetType === "team") {
      return teams.find((t) => t.id === active.targetId)?.name ?? "团队";
    }
    return agents.find((a) => a.id === active.targetId)?.name ?? "Agent";
  }, [active, agents, teams]);

  // 按会话的模型选择器（Agent 与直接会话）：对应 Claude/ChatGPT
  // 桌面端每个聊天的模型选择器。
  const sessionModels = useMemo(() => {
    if (!active) return [];
    if (active.targetType === "") {
      const provider = providerList.find((p) => p.isDefault) ?? providerList[0];
      return provider?.models ?? [];
    }
    if (active.targetType !== "agent") return [];
    const agent = agents.find((a) => a.id === active.targetId);
    return resolveAgentModel(agent, providerList).provider?.models ?? [];
  }, [active, agents, providerList]);

  // 草稿输入区（issue
  // #64）：首次发送前没有可读取的会话行，因此选择器会依据新聊天将使用的默认供应
  // 商进行解析，选择结果保存在组件状态中，直到 send()
  // 将其持久化。新聊天始终是直接会话（无目标），因此这与创建出的会话所解析到的
  // 结果一致。
  const draftProvider = useMemo(
    () => providerList.find((p) => p.isDefault) ?? providerList[0] ?? null,
    [providerList],
  );
  const draftModels = draftProvider?.models ?? [];
  const draftModel = draftModelFor(draftModels, draftModelOverride);
  // 同一组值会提供给两种状态下的工具栏，因此草稿和已保存聊天渲染出的控件完全相
  // 同。
  const composerModels = active ? sessionModels : draftModels;
  // 选择器没有合成的 "default model"
  // 行。其选中值就是实际将要运行的模型；未设置的覆盖项会解析为真实模型（直接聊
  // 天：供应商的第一个模型；Agent：其配置的模型）。
  const composerModel = active ? (target.model ?? composerModels[0] ?? null) : draftModel;
  const composerModelValue = composerModel?.id ?? "";
  const composerProviderName = active ? (target.provider?.name ?? "") : (draftProvider?.name ?? "");
  const composerModelDisplay = modelPickerLabel(composerModel, composerProviderName);
  const chooseComposerModel = (value: string) => {
    if (!active) {
      setDraftModelOverride(value);
      return;
    }
    void setSessionModel(value);
  };

  // 思考选择器。已保存的表优先；在级别表存在之前保存的模型仍会获得内置的系列回
  // 退（从后端获取，使映射只存在于一处），而不是隐藏该控件——显式禁用的表保持关
  // 闭（#42）。每回合的选择仅存在于此输入区：它绝不持久化，也绝不修改会话默认值
  // 。
  // 输入区模型：草稿的解析方式与首次发送时完全相同（draftModelFor），因此在会话
  // 行存在之前，思考选择器就是可选择的——同一模型、同一级别表，发送时不会发生漂
  // 移。
  const modelHasReasoningTable = !!composerModel?.reasoning;
  const composerModelID = composerModel?.id ?? "";
  const [suggestedReasoning, setSuggestedReasoning] = useState<{ id: string; info: ModelInfo } | null>(null);
  useEffect(() => {
    if (!composerModelID || modelHasReasoningTable) {
      setSuggestedReasoning(null);
      return;
    }
    let cancelled = false;
    Config.SuggestModels([composerModelID])
      .then((list) => {
        if (cancelled || !list?.[0]) return;
        setSuggestedReasoning({ id: composerModelID, info: list[0] });
      })
      .catch(() => { /* offline: the picker simply stays hidden */ });
    return () => { cancelled = true; };
  }, [composerModelID, modelHasReasoningTable]);
  // 只有为该模型获取的建议才可以提供给选择器：在模型切换后才解析完成的获取，不
  // 得渲染另一个模型的级别。
  const reasoningModel = composerReasoningModel({
    isDraft: !active,
    activeModel: target.model,
    draftModel,
    suggestion: suggestedReasoning,
  });
  const availableLevels = useMemo(() => reasoningLevels(reasoningModel), [reasoningModel]);
  const sessionReasoning = active?.reasoningLevel && availableLevels.some((l) => l.level === active.reasoningLevel)
    ? active.reasoningLevel
    : "";
  const defaultReasoningLevel = reasoningModel?.reasoning?.defaultLevel || availableLevels[0]?.level || "";
  // 待定选项仅对下一次发送生效；未知名称（例如模型切换后已过期的选择）会被忽略
  // 并继续走后续逻辑。
  const pendingReasoning = availableLevels.some((l) => l.level === turnReasoning) ? turnReasoning : "";
  const nextReasoningLevel = effectiveReasoningLevel(availableLevels, pendingReasoning, sessionReasoning, defaultReasoningLevel);
  const nextReasoningLabel = levelLabel(nextReasoningLevel);
  // 一个选项只属于一个聊天 +
  // 模型；切换其中任一项都会重新从会话默认值开始（对旧表有效的级别可能不存在）
  // 。 一个选项只属于一个聊天 + 模型；切换其中任一项（包括草稿的模型
  // chip）都会重新从会话默认值开始——对旧表有效的级别在新表上可能不存在。
  useEffect(() => {
    setTurnReasoning("");
    setIntelligenceOpen(false);
    setIntelligenceView("effort");
  }, [activeID, composerModelID]);

  const setSessionModel = async (modelID: string) => {
    if (!activeID) return;
    const picked = sessionModels.find((m) => m.id === modelID);
    const label = picked ? modelLabel(picked) : modelID;
    try {
      await Chat.SetSessionModel(activeID, modelID);
      reloadSessions();
      toast.success(`本会话已切换到 ${label}`);
    } catch (e) {
      toast.error(`切换模型失败: ${errText(e)}`);
    }
  };

  // 模型/推理控件会为会话持久化推理力度，与 Codex
  // 保持一致。滑块会持续更新本地状态以提供反馈，并在指针/键盘手势结束后提交最终
  // 档位。
  const commitReasoning = async (level: string) => {
    setTurnReasoning(level);
    if (!activeID) return;
    const previous = active?.reasoningLevel ?? "";
    setSessions((prev) => prev.map((session) => (
      session.id === activeID ? { ...session, reasoningLevel: level } : session
    )));
    try {
      await Chat.SetSessionReasoningLevel(activeID, level);
    } catch (e) {
      setSessions((prev) => prev.map((session) => (
        session.id === activeID ? { ...session, reasoningLevel: previous } : session
      )));
      setTurnReasoning(previous);
      toast.error(`设置会话思考强度失败: ${errText(e)}`);
    }
  };

  // ⌘F（由 App 派发）会打开会话内筛选器。
  useEffect(() => {
    const onToggle = () => setFilterOpen(true);
    window.addEventListener("wb-toggle-filter", onToggle);
    return () => window.removeEventListener("wb-toggle-filter", onToggle);
  }, []);

  const toolDurations = useMemo(() => computeToolDurations(messages), [messages]);
  const runningToolsSet = useMemo(() => computeRunningTools(messages), [messages]);
  // 每次消息变化时预计算一次，而不是为每个渲染行扫描尾部（此前每个 token 都是
  // O(n^2)）。
  const editFlags = useMemo(() => computeEditFlags(messages), [messages]);
  // 当前回合的最新计划。作用域限定在回合内，因此较早回合中已完成或已中断的计划
  // 不会一直固定显示。
  const activePlanMessage = useMemo(() => latestPlanMessage(messages), [messages]);
  const activePlan = useMemo(
    () => (activePlanMessage ? activePlanMessage.toolResp?.todos as TodoItem[] : null),
    [activePlanMessage],
  );
  // 该 chip 是实时进度提示控件，而非永久记录。回合运行或等待 HITL
  // 时应保留它；已中断的历史回合会保留其内联卡片，但不得留下过期的头部条目。
  const pendingConfirm = useMemo(() => {
    for (let i = messages.length - 1; i >= 0; i--) {
      const m = messages[i];
      if (m.kind === "user") break;
      if (m.kind === "confirm" && (m.confirmStatus ?? "pending") === "pending") return true;
    }
    return false;
  }, [messages]);
  const planDone = activePlan?.filter((t) => t.status === "completed").length ?? 0;
  const planKey = activeID && activePlan ? `${activeID}:${JSON.stringify(activePlan)}` : "";
  const [dismissedPlanKey, setDismissedPlanKey] = useState("");
  const showPlanChip = !!activePlan &&
    planDone < activePlan.length &&
    (running || pendingConfirm) &&
    dismissedPlanKey !== planKey;
  // 会话内消息筛选器（⌘F /
  // 头部搜索图标）：仅显示与查询匹配的消息，类似桌面客户端的聊天搜索。
  const [filterOpen, setFilterOpen] = useState(false);
  const [msgFilter, setMsgFilter] = useState("");
  const filterInputRef = useRef<HTMLInputElement>(null);
  const filterQ = msgFilter.trim().toLowerCase();
  // 为每条消息预计算小写搜索 blob：按键时过滤不得对整个历史重新 JSON.stringify
  // 大型工具载荷。
  const searchIndex = useMemo(() => {
    const map = new Map<string, string>();
    // 仅在过滤栏打开时才会查询：在流式回复的每个 token 上跳过逐消息的
    // JSON.stringify 处理。
    if (!filterOpen) return map;
    for (const m of messages) {
      const parts = [m.text, m.hint, m.toolName];
      if (m.toolResp) parts.push(JSON.stringify(m.toolResp));
      if (m.toolArgs) parts.push(JSON.stringify(m.toolArgs));
      map.set(m.id, parts.filter(Boolean).join("\n").toLowerCase());
    }
    return map;
  }, [messages, filterOpen]);
  const filteredMessages = useMemo(() => {
    if (!filterQ) return messages;
    return messages.filter((m) => (searchIndex.get(m.id) ?? "").includes(filterQ));
  }, [messages, filterQ, searchIndex]);
  // 切片来源以及渲染窗口上方有多少条目：首个可见项的日期分隔符必须与真实的上一
  // 项比较，否则截断窗口会绘制出虚假的日期标题。
  const renderSource = filterQ ? filteredMessages : messages;
  // 切片前将连续工具事件折叠为执行块：渲染上限随后限制渲染的条目（包括块），且
  // 一次运行绝不会在截断边界处被拆分。
  const renderItems = useMemo(() => groupTurns(groupToolMessages(renderSource)), [renderSource]);
  const visibleItems = useMemo(() => renderItems.slice(-renderLimit), [renderItems, renderLimit]);
  // 首个 token
  // 之前的间隙：正在运行，但最新的原始消息仍属于用户。状态必须依据消息
  // id（而不仅仅是最后一个回合）来确定，这样生效中的消息过滤才不会把它错误地归
  // 到更早的回合下。
  const awaitingReply = !historyLoading && isAwaitingReply(messages, running);
  const awaitingTurnId = awaitingReply && messages.length > 0
    ? messages[messages.length - 1].id
    : null;
  const omittedItems = Math.max(0, renderItems.length - visibleItems.length);
  // 最后一个回合拥有实时时钟：其执行块会持续计时，直到该回合结束。
  const firstVisibleIdx = renderItems.length - visibleItems.length;
  const lastTurnIdx = useMemo(() => {
    for (let k = renderItems.length - 1; k >= 0; k--) {
      if (renderItems[k].kind === "turn") return k;
    }
    return -1;
  }, [renderItems]);
  // 横幅统计的是隐藏的消息数（一个折叠块或回合涵盖多条消息），而分页按钮以
  // RENDER_LIMIT 为步长显示完整条目。
  const omittedMessages = useMemo(() => {
    let n = 0;
    for (const it of renderItems.slice(0, omittedItems)) {
      if (it.kind === "tool_group") n += it.messages.length;
      else if (it.kind === "turn") {
        n += 1;
        for (const b of it.body) n += b.kind === "tool_group" ? b.messages.length : 1;
      } else n += 1;
    }
    return n;
  }, [renderItems, omittedItems]);
  // 侧栏条目与渲染内容一致：被分页移出的回合不可导航。
  const railTurns = useMemo(() => {
    const turns: RailTurn[] = [];
    for (const it of visibleItems) {
      if (it.kind !== "turn") continue;
      const fullText = it.header.text.trim() || "（仅附件）";
      const text = fullText.length > 160 ? `${fullText.slice(0, 160)}…` : fullText;
      turns.push({
        id: it.header.id,
        text,
        fullText,
      });
    }
    return turns;
  }, [visibleItems]);
  // 当尚未触发任何滚动事件（短会话从不滚动），或活动回合被过滤/分页移出时，初始
  // 化 / 修复活动栏。
  useEffect(() => {
    if (railTurns.length === 0) return;
    if (activeTurnId && railTurns.some((t) => t.id === activeTurnId)) return;
    setActiveTurnId(railTurns[railTurns.length - 1].id);
  }, [railTurns, activeTurnId]);
  // TurnBlock 的主体复用扁平列表所用的完全相同 prop 连接方式。
  const renderBodyItem = (item: ChatItem, opts: TurnBodyOpts) => {
    if (item.kind === "tool_group") {
      return (
        <ToolCallGroup
          group={item}
          live={opts.liveGroup}
          runningTools={runningToolsSet}
          durations={toolDurations}
          sessionID={activeID}
          filterQ={filterQ}
          displayMode={executionDisplayMode}
          onConfirm={answerConfirm}
        />
      );
    }
    return (
      <MessageBubble
        m={item.message}
        isLastAssistant={opts.isLastAssistant}
        onRegenerate={regenerate}
        canRegenerate={!running && !!activeID}
        toolRunning={item.message.kind === "tool_call" ? runningToolsSet.has(item.message.id) : false}
        durationMs={toolDurations.get(item.message.id)}
        onSpeak={speakMessage}
        costOf={costOf}
        onEdit={startEdit}
        canEdit={editFlags.get(item.message.id) === true && !running && !!activeID}
        onConfirm={answerConfirm}
        sessionID={activeID}
        filterQ={filterQ}
      />
    );
  };

  const filterOpenRef = useRef(false);
  const toggleFilter = useCallback(() => {
    const next = !filterOpenRef.current;
    filterOpenRef.current = next;
    setFilterOpen(next);
    if (next) setTimeout(() => filterInputRef.current?.focus(), 50);
    else setMsgFilter("");
  }, []);
  const slashCommands = useMemo(() => slashCandidates(SLASH_COMMANDS, skills), [skills]);
  const slashSuggestions = useMemo(
    () => filterSlashCommands(slashCommands, input),
    [slashCommands, input],
  );
  // 斜杠建议与提及弹窗的交互一致：完整的键盘导航，Enter/Tab 选择，Esc
  // 关闭，直至文本变化。
  const [slashIdx, setSlashIdx] = useState(0);
  const [slashDismissed, setSlashDismissed] = useState<string | null>(null);
  const slashOpen = slashSuggestions.length > 0 && input !== slashDismissed;
  useEffect(() => {
    setSlashIdx(0);
    if (input !== slashDismissed) setSlashDismissed(null);
  }, [input, slashDismissed]);
  const applySlash = (cmd: string) => {
    const next = cmd + " ";
    setInput(next);
    setSlashDismissed(null);
    requestAnimationFrame(() => {
      const ta = inputRef.current;
      if (!ta) return;
      ta.focus();
      ta.setSelectionRange(next.length, next.length);
      setCaret(next.length);
    });
  };

  // 让输入区随内容自动增高，上限为 max-h-48（192px）——超过上限后，textarea
  // 在内部滚动，消息列表保留剩余空间。
  useEffect(() => {
    const ta = inputRef.current;
    if (!ta) return;
    ta.style.height = "auto";
    ta.style.height = `${Math.min(ta.scrollHeight, 192)}px`;
  }, [input, activeID]);
  // 文件悬停在输入区拖放区域时提供视觉反馈。
  const [dragOver, setDragOver] = useState(false);

  // @ 提及自动补全。开头的 "@名称" 会将该回合路由到指定的
  // agent/team/remote（由后端解析），同时每个 token
  // 还提供工作区文件/文件夹作为上下文。该 token 会感知光标位置，因此 "@"
  // 可在消息中间使用，而在 token 中间选择时只会重写该 token。
  const [caret, setCaret] = useState(0);
  useEffect(() => {
    // 切换会话会替换草稿；应将光标保持在末尾。在同一个会话内输入时，不得将光标
    // 拉回末尾。
    setCaret(input.length);
  }, [activeID]);
  const mentionToken = useMemo(() => mentionAtCaret(input, caret), [input, caret]);
  const mentionAssistants = useMemo<AssistantCandidate[]>(() => {
    if (!mentionToken?.leading) return [];
    return [
      ...agents.map((a) => ({ kind: "agent" as const, id: a.id, name: a.name })),
      ...teams.map((t) => ({ kind: "team" as const, id: t.id, name: t.name })),
      ...remotes.map((r) => ({ kind: "remote" as const, id: r.id, name: r.name })),
    ];
  }, [mentionToken?.leading, agents, teams, remotes]);
  const mentionCandidates = useMemo(
    () => (mentionToken ? buildMentionCandidates(mentionToken, mentionAssistants, contextEntries) : []),
    [mentionToken, mentionAssistants, contextEntries],
  );
  const [mentionIdx, setMentionIdx] = useState(0);
  const [mentionDismissed, setMentionDismissed] = useState<string | null>(null);
  const mentionKey = mentionToken ? `${mentionToken.start}:${mentionToken.query}` : null;
  useEffect(() => {
    setMentionIdx(0);
    if (mentionKey !== mentionDismissed) setMentionDismissed(null);
  }, [mentionKey, mentionDismissed]);
  // 工作区索引可能在弹窗打开期间到达；绝不能让高亮指向新候选列表末尾之外。
  useEffect(() => {
    if (mentionCandidates.length > 0 && mentionIdx >= mentionCandidates.length) setMentionIdx(0);
  }, [mentionCandidates.length, mentionIdx]);
  const mentionOpen = mentionKey != null && mentionKey !== mentionDismissed && mentionCandidates.length > 0;
  const applyMention = (c: MentionCandidate) => {
    if (!mentionToken) return;
    const value = isAssistantCandidate(c) ? c.name : c.kind === "directory" ? `${c.path}/` : c.path;
    // 选中文件夹时，令牌保持开放式结尾（`@src/`），以便菜单逐层深入；文件与助手
    // 则用空格结束令牌。
    const next = applyMentionToken(input, mentionToken, value, { trailingSpace: !(c.kind === "directory") });
    setInput(next.text);
    setMentionDismissed(null);
    requestAnimationFrame(() => {
      const ta = inputRef.current;
      if (!ta) return;
      ta.focus();
      ta.setSelectionRange(next.caret, next.caret);
      setCaret(next.caret);
    });
  };

  // contextUsage：会话目前已消耗目标模型上下文窗口的多少。分子是真实的上下文占
  // 用——最后一次模型调用的提示词 token
  // 数（contextTokens）——而非累计计费用量：每次调用都会重发完整历史，因此一个工
  // 具调用循环内的总和会是真实大小的数倍，曾出现某会话真实占用为 16.6% 却显示
  // 6.4M tokens（100%）的情况。
  const contextWindow = target.model?.contextWindow ?? 0;
  const realContextUsage = useMemo(
    () => resolveContextUsage(sessionUsage?.contextTokens, contextWindow),
    [contextWindow, sessionUsage],
  );
  // ≈ 回退方案，用于上游绝不回报用量元数据的模型（部分网关会忽略
  // stream_options.include_usage）：从对话本身近似估算下一次提示词。已做防抖——
  // 该估算很粗糙，不得在每次流式刷新时都重新执行 JSON.stringify。
  const [estimatedTokens, setEstimatedTokens] = useState(0);
  useEffect(() => {
    const t = window.setTimeout(() => setEstimatedTokens(estimateContextTokens(messages)), 250);
    return () => window.clearTimeout(t);
  }, [messages]);
  const contextUsage = useMemo(() => {
    if (realContextUsage) return realContextUsage;
    if (contextWindow <= 0 || messages.length === 0 || estimatedTokens <= 0) return null;
    return resolveContextUsage(estimatedTokens, contextWindow, true);
  }, [realContextUsage, contextWindow, messages.length, estimatedTokens]);
  // 环形下方的悬停卡片：窗口在前（"背景信息窗口："），累计计费在后。Null
  // 表示"无话可说"，触发元素随之消失。
  const contextTip = useMemo(
    () => contextTooltipLines(contextUsage, sessionUsage, costOf(sessionUsage)),
    [contextUsage, sessionUsage, costOf],
  );

  // 统计对话框的整会话成本估算（标签/值的顺序必须与其他统计单元格一致：值在上且
  // 弱化显示，标签在下且加粗）。
  const statsCost = useMemo(
    () => (stats ? costOf({ promptTokens: stats.tokensIn, completionTokens: stats.tokensOut, totalTokens: stats.tokensTotal }) : ""),
    [stats, costOf],
  );

  // renderSessionRow
  // 绘制一行紧凑的聊天行；由项目树、搜索结果和归档区共用，以保证悬停操作保持一
  // 致。
  const renderSessionRow = (s: ChatSession, indented = true) => (
    <div
      key={s.id}
      role="button"
      tabIndex={0}
      aria-current={s.id === activeID ? "page" : undefined}
      data-session-id={s.id}
      className={cn(
        "group relative flex cursor-pointer items-center gap-1.5 rounded-md py-[3px] text-md leading-5 transition-colors",
        indented ? "pl-7 pr-1.5" : "px-2.5",
        s.id === activeID
          ? "bg-accent-soft font-medium text-foreground"
          : "text-muted-foreground hover:bg-accent hover:text-foreground",
      )}
      onClick={() => setActiveID(s.id)}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          setActiveID(s.id);
        }
      }}
    >
      {indented && (
        <span
          aria-hidden="true"
          className={cn(
            "pointer-events-none absolute left-[13px] top-1/2 h-[3px] w-[3px] -translate-y-1/2 rounded-full",
            s.id === activeID ? "bg-accent-text" : "bg-muted-foreground/40 group-hover:bg-foreground/50",
          )}
        />
      )}
      <span className="min-w-0 flex-1">
        <SessionRowLabel
          title={s.title}
          preview={s.preview}
          typeLabel={sessionTargetBadgeLabel(s.targetType) ?? ""}
          forkedFromTitle={s.forkedFrom
            ? sessions.find((x) => x.id === s.forkedFrom)?.title ?? s.forkedFrom
            : ""}
        />
      </span>
      {runningMap[s.id] && <Loader2 className="h-3 w-3 shrink-0 animate-spin text-accent-text" aria-label="生成中" />}
      {!runningMap[s.id] && unread[s.id] && <span className="h-2 w-2 shrink-0 rounded-full bg-brand" aria-label="有新回复未查看" />}
      {/* 该控件簇承载 "…" 下拉菜单的锚点，因此在菜单打开期间必须保留其布局盒——参见 SESSION_ROW_ACTIONS_CLASS（#28）。 */}
      <span className={SESSION_ROW_ACTIONS_CLASS} data-slot="session-row-actions">
        {/* 高频状态切换保持单击可达；其余项收进一个菜单，使该行只保留四个控件而非六个。 */}
        <TooltipHint content={s.archived ? "取消归档" : "归档此会话"}>
          <button
            className="rounded p-0.5 hover:bg-foreground/10"
            aria-label={s.archived ? "取消归档" : "归档此会话"}
            onClick={(e) => {
              e.stopPropagation();
              Chat.ArchiveSession(s.id, !s.archived).then(reloadSessions).catch((e2) => toast.error(`归档失败: ${errText(e2)}`));
            }}
          >
            <Archive className="h-3.5 w-3.5" />
          </button>
        </TooltipHint>
        <TooltipHint content={s.pinned ? "取消置顶" : "置顶此会话"}>
          <button
            className="rounded p-0.5 hover:bg-foreground/10"
            aria-label={s.pinned ? "取消置顶" : "置顶此会话"}
            onClick={(e) => {
              e.stopPropagation();
              Chat.PinSession(s.id, !s.pinned).then(reloadSessions).catch((e2) => toast.error(`置顶失败: ${errText(e2)}`));
            }}
          >
            {s.pinned ? <PinOff className="h-3.5 w-3.5" /> : <Pin className="h-3.5 w-3.5" />}
          </button>
        </TooltipHint>
        <DropdownMenu>
          <TooltipHint content="会话操作" wrap>
            <DropdownMenuTrigger asChild>
              <button
                className="rounded p-0.5 hover:bg-foreground/10"
                aria-label="会话操作"
                onClick={(e) => e.stopPropagation()}
              >
                <MoreHorizontal className="h-3.5 w-3.5" />
              </button>
            </DropdownMenuTrigger>
          </TooltipHint>
          <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
            <DropdownMenuItem onClick={() => forkSession(s)}>
              <GitFork className="mr-2 h-3.5 w-3.5" /> 分叉此会话
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => { setRenaming(s); setRenameText(s.title); }}>
              <Pencil className="mr-2 h-3.5 w-3.5" /> 重命名
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem className="text-danger-text focus:text-danger-text" onClick={() => removeSession(s)}>
              <Trash2 className="mr-2 h-3.5 w-3.5" /> 删除会话…
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </span>
    </div>
  );

  return (
    <div className="flex h-full">
      {/* 会话 */}
      {!sidebarCollapsed && (
      <>
      <div className="flex shrink-0 flex-col bg-chrome font-sans" style={{ width: sidebarWidth }}>
        <div className="flex items-center gap-1 px-3 pb-2 pt-3">
          <span className="text-xs font-semibold text-muted-foreground">
            项目
          </span>
          <TooltipHint content="添加项目（选择一个目录）">
            <button
              aria-label="添加项目"
              onClick={addProject}
              className="flex h-5 w-5 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              <FolderPlus className="h-3.5 w-3.5" />
            </button>
          </TooltipHint>
          <div className="ml-auto flex items-center gap-1">
            <TooltipHint content="新建对话（⌘N）">
              <button
                aria-label="新建对话"
                onClick={createDirectSession}
                className="inline-flex h-7 items-center gap-1.5 rounded-lg bg-panel/60 px-2 text-sm leading-5 font-medium text-foreground transition-colors hover:bg-accent"
              >
                <SquarePen className="h-3 w-3" />
                新对话
              </button>
            </TooltipHint>
            <TooltipHint content="折叠对话列表（⌘B）">
              <button
                aria-label="折叠对话列表"
                aria-expanded={!sidebarCollapsed}
                onClick={toggleSidebar}
                className="flex h-7 w-6 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              >
                <PanelLeftClose className="h-3.5 w-3.5" />
              </button>
            </TooltipHint>
          </div>
        </div>
        <div className="px-3 pb-3">
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="h-7 rounded-md pl-8 text-sm leading-5 placeholder:text-muted-foreground/70"
              placeholder="搜索对话"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
        </div>
        {/* 普通滚动容器：Radix ScrollArea 会将其子元素包裹在一个 `display: table` div 中，因此过长的会话标题/预览会把表格宽度撑到其完整的 nowrap 长度，并且每一行右对齐的操作都会被裁剪到侧边栏之外。块级容器可让 `truncate` 发挥作用，并将悬停操作保持在行内。 */}
        <div className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto px-2.5">
          <div className="grid grid-cols-1 gap-0.5 pb-3">
            {searching ? (
              <>
                <div className="px-2 pb-0.5 pt-2 text-xs font-medium text-muted-foreground">搜索结果（{searchResults.length}）</div>
                {searchResults.map((s) => renderSessionRow(s, false))}
                {searchResults.length === 0 && (
                  <div className="px-2 py-6 text-center text-xs text-muted-foreground">没有匹配的对话</div>
                )}
              </>
            ) : (
              <>
                {sidebarGroups.map((g) => {
                  const expanded = expandedProjectKeys.has(g.key);
                  const collapsed = collapsedGroupKeys.has(g.key);
                  const sessionWindow = projectSessionWindow(g.sessions, expanded);
                  const project = g.projectId ? projects.find((p) => p.id === g.projectId) : undefined;
                  const isDefault = g.key === "default";
                  const isPinned = g.key === "pinned";
                  const hasActions = !!project || (!isDefault && !!g.path);
                  return (
                    <div key={g.key} className="mb-2 last:mb-0">
                      <div
                        role="group"
                        className={cn(
                          "group flex items-center rounded-md pr-1 text-muted-foreground transition-colors",
                          draftProjectKey === g.key
                            ? "bg-accent-soft text-foreground"
                            : "hover:bg-accent hover:text-foreground",
                        )}
                      >
                        <TooltipHint content={collapsed ? `展开「${g.label}」` : `收起「${g.label}」`}>
                          <button
                            type="button"
                            aria-expanded={!collapsed}
                            aria-label={
                              isDefault
                                ? `对话分组，${collapsed ? "已收起" : "已展开"}`
                                : isPinned
                                  ? `置顶对话，${collapsed ? "已收起" : "已展开"}`
                                  : `项目「${g.label}」，${collapsed ? "已收起" : "已展开"}`
                            }
                            data-project-state={collapsed ? "closed" : "open"}
                            onClick={() => setCollapsedGroupKeys((prev) => {
                              const next = new Set(prev);
                              if (next.has(g.key)) next.delete(g.key);
                              else next.add(g.key);
                              return next;
                            })}
                            className="flex min-w-0 flex-1 cursor-pointer items-center gap-1.5 rounded-md px-2 py-1 text-left text-md leading-5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
                          >
                            {isPinned
                              ? <Pin className="h-3.5 w-3.5 shrink-0 opacity-70" />
                              : isDefault
                                ? <MessageSquare className="h-3.5 w-3.5 shrink-0 opacity-70" />
                                : collapsed
                                  ? <Folder className="h-4 w-4 shrink-0 opacity-70" />
                                  : <FolderOpen className="h-4 w-4 shrink-0 opacity-70" />}
                            <span className="min-w-0 flex-1 truncate font-medium">{g.label}</span>
                          </button>
                        </TooltipHint>
                        {hasActions && (
                          <span className={SESSION_ROW_ACTIONS_CLASS} data-slot="project-row-actions">
                            {project && (
                              <TooltipHint content="在此项目中新建对话">
                                <button
                                  className="rounded p-0.5 hover:bg-foreground/10"
                                  aria-label={`在项目「${g.label}」中新建对话`}
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    createProjectSession(project.id);
                                  }}
                                >
                                  <Plus className="h-3.5 w-3.5" />
                                </button>
                              </TooltipHint>
                            )}
                            <DropdownMenu>
                              <TooltipHint content={project ? "管理项目" : "目录操作"} wrap>
                                <DropdownMenuTrigger asChild>
                                  <button
                                    aria-label={project ? `管理项目「${g.label}」` : `目录「${g.label}」操作`}
                                    className="rounded p-0.5 hover:bg-foreground/10"
                                    onClick={(e) => e.stopPropagation()}
                                  >
                                    <MoreHorizontal className="h-3.5 w-3.5" />
                                  </button>
                                </DropdownMenuTrigger>
                              </TooltipHint>
                              <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
                                {project ? (
                                  <>
                                    <DropdownMenuItem onClick={() => createProjectSession(project.id)}>在此项目中新建对话</DropdownMenuItem>
                                    <DropdownMenuItem onClick={() => { setRenamingProject(project); setRenameProjectText(g.label); }}>重命名项目</DropdownMenuItem>
                                    <DropdownMenuItem onClick={async () => {
                                      try {
                                        const picked = await pickWorkspace();
                                        if (!picked) return;
                                        await Chat.SaveProject(project.id, "", picked);
                                        reloadProjects();
                                        toast.success("项目目录已更新");
                                      } catch (e) {
                                        toast.error(`更改目录失败: ${errText(e)}`);
                                      }
                                    }}>
                                      更改项目目录…
                                    </DropdownMenuItem>
                                    <DropdownMenuSeparator />
                                    <DropdownMenuItem className="text-danger-text" onClick={() => deleteProject(project)}>
                                      删除项目（保留对话与文件）
                                    </DropdownMenuItem>
                                  </>
                                ) : (
                                  <DropdownMenuItem onClick={async () => {
                                    try {
                                      const p = await Chat.SaveProject("", "", g.path ?? "");
                                      reloadProjects();
                                      toast.success(`已添加项目「${p.name}」`);
                                    } catch (e) {
                                      toast.error(`添加项目失败: ${errText(e)}`);
                                    }
                                  }}>
                                    注册为项目
                                  </DropdownMenuItem>
                                )}
                              </DropdownMenuContent>
                            </DropdownMenu>
                          </span>
                        )}
                      </div>
                      {!collapsed && g.sessions.length > 0 && (
                        <div className="mt-0.5 grid grid-cols-1 gap-0.5">
                          {sessionWindow.visible.map((s) => renderSessionRow(s))}
                          {g.sessions.length > PROJECT_SESSION_LIMIT && (
                            <button
                              className="flex w-full items-center rounded-md py-1 pl-7 pr-2 text-left text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
                              aria-expanded={expanded}
                              aria-label={expanded
                                ? `收起「${g.label}」的全部对话`
                                : `展开显示「${g.label}」的其余 ${sessionWindow.hiddenCount} 条对话`}
                              onClick={() => setExpandedProjectKeys((prev) => {
                                const next = new Set(prev);
                                if (next.has(g.key)) next.delete(g.key);
                                else next.add(g.key);
                                return next;
                              })}
                            >
                              {expanded ? "收起" : "展开显示"}
                            </button>
                          )}
                        </div>
                      )}
                    </div>
                  );
                })}
                {sidebarGroups.length === 0 && archivedSessions.length === 0 && (
                  <div className="px-2 py-4 text-center text-xs text-muted-foreground">还没有对话，点击上方「新对话」开始</div>
                )}
                {archivedSessions.length > 0 && (
                  showArchived ? (
                    <>
                      <button
                        className="mt-1 flex w-full items-center gap-1 rounded-md px-2 py-1 text-left text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
                        onClick={() => setShowArchived(false)}
                      >
                        <ChevronDown className="h-3 w-3" /> 已归档会话（{archivedSessions.length}）
                      </button>
                      <TooltipHint content="删除全部已归档会话（任务文件保留）">
                        <button
                          className="ml-auto shrink-0 rounded px-1.5 py-0.5 text-2xs text-muted-foreground hover:bg-accent hover:text-danger-text"
                          onClick={() => setConfirmRequest({
                            title: `清空全部已归档会话（${archivedSessions.length} 条）？`,
                            description: "会话与消息将不可恢复，任务工作区中的文件会保留。",
                            confirmText: "清空",
                            action: async () => {
                              try {
                                const n = await Chat.DeleteArchivedSessions();
                                toast.success(`已清空 ${n} 条归档会话`);
                                reloadSessions();
                              } catch (e) {
                                toast.error(`清空失败: ${errText(e)}`);
                              }
                            },
                          })}
                        >
                          清空
                        </button>
                      </TooltipHint>
                      {archivedSessions.map((s) => renderSessionRow(s, false))}
                    </>
                  ) : (
                    <button
                      className="mt-1 flex w-full items-center gap-1 rounded-md px-2 py-1 text-left text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
                      onClick={() => setShowArchived(true)}
                    >
                      <ChevronRight className="h-3 w-3" /> 已归档会话（{archivedSessions.length}）
                    </button>
                  )
                )}
              </>
            )}
          </div>
        </div>
        <SidebarFooter
          version={version}
          themePreset={themePreset}
          themeMode={themeMode}
          onOpenSettings={() => onOpenSettings()}
          onOpenHelp={onOpenHelp}
          onThemePresetChange={(themePreset) => onAppearanceChange({ themePreset })}
          onThemeModeChange={(themeMode) => onAppearanceChange({ themeMode })}
        />
      </div>
      {/* 调整大小手柄：界面外壳与画布之间的一条透明 6px 条带。指针捕获 可使拖动在条带之外仍然有效；键盘路径让 分隔条无需鼠标也可访问。 */}
      <TooltipHint content="拖动调整对话列表宽度" side="right">
        <div
          role="separator"
          aria-orientation="vertical"
          aria-label="拖动调整对话列表宽度"
          aria-valuemin={SIDEBAR_MIN_WIDTH}
          aria-valuemax={sidebarMaxWidth(window.innerWidth)}
          aria-valuenow={sidebarWidth}
          tabIndex={0}
          onPointerDown={startSidebarResize}
        onPointerMove={moveSidebarResize}
        onPointerUp={endSidebarResize}
        onPointerCancel={endSidebarResize}
        onLostPointerCapture={endSidebarResize}
        onKeyDown={resizeSidebarWithKeyboard}
        className={cn(
          "w-1.5 shrink-0 cursor-col-resize touch-none select-none bg-transparent transition-colors",
          "hover:bg-ring/50 focus-visible:bg-ring/60 focus-visible:outline-none",
          resizingSidebar && "bg-ring/60",
        )}
        />
      </TooltipHint>
      </>
      )}

      {/* 聊天区域 */}
      <div className="relative flex min-w-0 flex-1 flex-col">
        <div className="flex h-12 items-center justify-between bg-canvas px-4">
          <div className="flex min-w-0 items-center gap-2">
            {sidebarCollapsed && (
              <TooltipHint content="展开对话列表（⌘B）">
                <button
                  aria-label="展开对话列表"
                  aria-expanded={false}
                  onClick={toggleSidebar}
                  className="inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                >
                  <PanelLeftOpen className="h-4 w-4" />
                </button>
              </TooltipHint>
            )}
            <MessageSquare className="h-4 w-4 shrink-0 text-muted-foreground" />
            <span className="max-w-[360px] truncate text-sm font-semibold">{active ? active.title : "AgentMate"}</span>
            {active && (
              (() => {
                const label = sessionTargetBadgeLabel(active.targetType);
                // 直接聊天不携带目标，因此不会显示徽标：一个 "直聊" 标签在
                // Agent/团队目标旁读起来像一种可选模式。
                if (!label) return null;
                // 可配置目标会深链接到设置中心。
                const tab: SettingsTab | undefined = active.targetType === "agent" ? "agents" : active.targetType === "team" ? "teams" : undefined;
                return (
                  <TooltipHint content={tab ? "管理此配置" : ""}>
                    <span
                      className={cn(
                        "shrink-0 rounded-md bg-panel px-1.5 py-0.5 text-2xs text-muted-foreground",
                        tab && "cursor-pointer transition-colors hover:bg-accent hover:text-foreground",
                      )}
                      onClick={tab ? () => onOpenSettings(tab) : undefined}
                    >
                      {label}: {targetLabel}
                    </span>
                  </TooltipHint>
                );
              })()
            )}
            {active && (
              <TooltipHint content="在会话内搜索消息（⌘F）">
                <button
                  className={cn(
                    "inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground",
                    filterOpen && "bg-accent-soft text-accent-text",
                  )}
                  aria-label="在会话内搜索消息"
                  onClick={toggleFilter}
                >
                  <Search className="h-3.5 w-3.5" />
                </button>
              </TooltipHint>
            )}
            {active && (
              <TooltipHint content="重命名会话">
                <button
                  className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                  aria-label="重命名会话"
                  onClick={() => { setRenaming(active); setRenameText(active.title); }}
                >
                  <Pencil className="h-3.5 w-3.5" />
                </button>
              </TooltipHint>
            )}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {active && activePlan && showPlanChip && (
              <PlanChip
                todos={activePlan}
                running={running}
                waiting={pendingConfirm}
                onDismiss={() => setDismissedPlanKey(planKey)}
              />
            )}
            {realContextUsage && realContextUsage.pct >= 80 && (
              <span className="shrink-0 rounded-md bg-danger-soft px-1.5 py-0.5 text-2xs text-danger-text">
                上下文即将耗尽（{realContextUsage.pct.toFixed(0)}%）
              </span>
            )}
            {active && (
              <>
                {/* 导出/记忆/统计都放在一个溢出菜单中：六个图标按钮在窄窗口中挤满了页头。 */}
                <DropdownMenu>
                  <TooltipHint content="更多操作" wrap>
                    <DropdownMenuTrigger asChild>
                      <Button variant="ghost" size="icon-sm" aria-label="更多操作">
                        <MoreHorizontal className="h-4 w-4" />
                      </Button>
                    </DropdownMenuTrigger>
                  </TooltipHint>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem onClick={copyConversation}>
                      <ClipboardCopy className="h-4 w-4" /> 复制整段对话
                    </DropdownMenuItem>
                    <DropdownMenuItem onClick={exportSession}>
                      <Download className="h-4 w-4" /> 导出为 Markdown
                    </DropdownMenuItem>
                    <DropdownMenuItem onClick={exportHTML}>
                      <FileCode2 className="h-4 w-4" /> 导出为 HTML
                    </DropdownMenuItem>
                    <DropdownMenuItem onClick={openStats}>
                      <BarChart3 className="h-4 w-4" /> 会话统计
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      onClick={async () => {
                        try {
                          const n = await Chat.RememberSession(activeID);
                          toast.success(`已存入长期记忆（共 ${n} 条）`);
                        } catch (e) {
                          toast.error(`存入失败: ${errText(e)}`);
                        }
                      }}
                    >
                      <Brain className="h-4 w-4" /> 存入长期记忆
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
                <TooltipHint content={workspacePanelOpen ? "收起任务结果区" : "打开任务结果区"}>
                  <Button
                    variant={workspacePanelOpen ? "secondary" : "ghost"}
                    size="icon-sm"
                    aria-label={workspacePanelOpen ? "收起任务结果区" : "打开任务结果区"}
                    onClick={() => setWorkspacePanelOpen((v) => !v)}
                  >
                    <PanelRightOpen className="h-4 w-4" />
                  </Button>
                </TooltipHint>
              </>
            )}
          </div>
        </div>

        {filterOpen && (
          <div className="flex items-center gap-2 bg-canvas px-4 py-1.5">
            <Search className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            <input
              ref={filterInputRef}
              value={msgFilter}
              onChange={(e) => setMsgFilter(e.target.value)}
              placeholder="过滤当前会话的消息（匹配文本/工具名/参数/结果）"
              aria-label="过滤当前会话的消息"
              className="h-7 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
            />
            {filterQ && (
              <span className="shrink-0 text-xs text-muted-foreground">{filteredMessages.length}/{messages.length} 条</span>
            )}
            <TooltipHint content="清除并关闭">
              <button
                className="shrink-0 rounded p-1 text-muted-foreground hover:text-foreground"
                onClick={toggleFilter}
              >
                <X className="h-3.5 w-3.5" />
              </button>
            </TooltipHint>
          </div>
        )}
        {/* 浮动按钮锚定到消息视口：输入框变高时会 缩小此包裹容器，而不是滑到按钮下方。 */}
        <div className="relative flex min-h-0 flex-1 flex-col">
          {!atBottom && messages.length > 0 && (
            // 消息区域内的圆形悬浮控件：空闲时显示箭头，回合运行期间显示呼吸式
            // 琥珀色圆点，悬停时再次显示箭头。
            <TooltipHint content={running ? "正在生成，回到底部" : "回到底部"}>
              <button
                type="button"
                aria-label="回到底部"
                onClick={() => bottomRef.current?.scrollIntoView({ block: "end" })}
              className={cn(
                "group absolute bottom-4 left-1/2 z-20 flex h-10 w-10 -translate-x-1/2 items-center justify-center rounded-full bg-overlay text-muted-foreground shadow-pop transition-all duration-200 hover:scale-105 hover:bg-accent hover:text-foreground active:scale-95",
                running && "bg-warning-soft text-warning",
              )}
            >
              {running && (
                <span
                  aria-hidden="true"
                  className="absolute inset-0 flex items-center justify-center gap-1 transition-opacity duration-150 group-hover:opacity-0"
                >
                  <span className="wb-dot-pulse h-1 w-1 rounded-full bg-warning" />
                  <span className="wb-dot-pulse h-1 w-1 rounded-full bg-warning [animation-delay:160ms]" />
                  <span className="wb-dot-pulse h-1 w-1 rounded-full bg-warning [animation-delay:320ms]" />
                </span>
              )}
              <ArrowDown
                className={cn(
                  "h-4 w-4 transition-opacity duration-150",
                  running && "opacity-0 group-hover:opacity-100",
                )}
              />
            </button>
            </TooltipHint>
          )}
          <div className="pointer-events-none absolute inset-0 z-10 hidden md:block">
            <TurnRail
              turns={railTurns}
              activeId={activeTurnId}
              onSelect={selectTurn}
              className="pointer-events-auto absolute left-2 top-1/2 -translate-y-1/2"
            />
          </div>
          <ScrollArea className="flex-1" viewportRef={scrollRef} aria-label="会话消息">
            <div data-slot="message-column" className="mx-auto chat-col space-y-4 px-5 py-6">
              {(() => {
                if (omittedItems <= 0) return null;
                return (
                  <div className="rounded-lg bg-panel/60 px-3 py-2 text-center text-xs text-muted-foreground">
                    <div className="flex items-center justify-center gap-2">
                      <span>
                        已省略较早的 {omittedMessages} 条{filterQ ? "匹配" : ""}消息（完整历史已保存，可导出查看）
                      </span>
                      <Button
                        size="sm"
                        variant="outline"
                        className="h-6 px-2 text-2xs"
                        onClick={() => setRenderLimit((l) => l + RENDER_LIMIT)}
                      >
                        显示更早 {Math.min(RENDER_LIMIT, omittedItems)} 条
                      </Button>
                    </div>
                  </div>
                );
              })()}
              {realContextUsage && realContextUsage.pct >= 80 && !ctxBannerDismissed && (
                <div className="flex items-center justify-between rounded-lg bg-warning-soft px-4 py-2.5 text-sm">
                  <span>上下文窗口已使用 {realContextUsage.pct.toFixed(0)}%，模型可能开始遗忘早期内容。建议导出会话后开启新对话。</span>
                  <div className="flex shrink-0 gap-2">
                    <Button size="sm" variant="outline" onClick={exportSession}>导出 Markdown</Button>
                    <Button size="sm" variant="ghost" onClick={() => setCtxBannerDismissed(true)}>知道了</Button>
                  </div>
                </div>
              )}
              {messages.length === 0 && !active && providerCount === 0 && <Onboarding onOpenSettings={onOpenSettings} />}
              {messages.length === 0 && !active && providerCount > 0 && (
                // 已有供应商但未选择会话：此前这里会渲染一个空面板，且不提示该
                // 做什么。
                <div className="flex min-h-[55vh] flex-col items-center justify-center gap-3 text-center">
                  <span className="flex h-12 w-12 items-center justify-center rounded-full bg-accent-soft text-accent-text">
                    <MessageSquare className="h-6 w-6" />
                  </span>
                  <div className="text-md font-medium">直接开始对话</div>
                  <p className="max-w-sm text-ui leading-relaxed text-muted-foreground">
                    输入消息即可开始；输入 <span className="font-medium text-foreground">@</span> 可指定助手、团队或远程 Agent。
                  </p>
                </div>
              )}
              {messages.length === 0 && active && !awaitingReply && !historyLoading && (
                <div className="flex min-h-[55vh] flex-col items-center justify-center gap-3 text-center">
                  <span className="flex h-12 w-12 items-center justify-center rounded-full bg-accent-soft text-accent-text">
                    <MessageSquare className="h-6 w-6" />
                  </span>
                  <div className="text-md font-medium">发送第一条消息</div>
                  <p className="max-w-sm text-ui leading-relaxed text-muted-foreground">
                    {active.targetType
                      ? <>向 <span className="font-medium text-foreground">{targetLabel}</span> 发送消息开始任务</>
                      : <>直接输入开始对话；输入 <span className="font-medium text-foreground">@</span> 可指定助手</>}
                  </p>
                </div>
              )}
              {visibleItems.map((item, i, arr) => {
                // 窗口被截断时，首个可见项会与真实的上一项进行比较（否则不显示
                // 分隔符）。
                const ts = chatItemStartTs(item);
                const prevTs = i === 0
                  ? (omittedItems > 0 ? chatItemEndTs(renderItems[renderItems.length - visibleItems.length - 1]) : undefined)
                  : chatItemEndTs(arr[i - 1]);
                const dayChanged = isDayChange(prevTs, ts);
                const renderIdx = firstVisibleIdx + i;
                return (
                  <Fragment key={item.id}>
                    {dayChanged && <DaySeparator ts={ts} />}
                    {item.kind === "turn" ? (
                      <TurnBlock
                        turn={item}
                        active={item.header.id === activeTurnId}
                        live={running && renderIdx === lastTurnIdx}
                        isLastTurn={renderIdx === renderItems.length - 1}
                        waiting={item.header.id === awaitingTurnId}
                        onEdit={startEdit}
                        canEdit={editFlags.get(item.header.id) === true && !running && !!activeID}
                        filterQ={filterQ}
                        renderBodyItem={renderBodyItem}
                      />
                    ) : (
                      // 孤儿项位于首条用户消息之前：此时没有进行中的回合，且共
                      // 享接线只保留一处 prop 传递位置。
                      renderBodyItem(item, {
                        isLastAssistant: item.kind === "message"
                          && item.message.kind === "assistant"
                          && renderIdx === renderItems.length - 1,
                        // 当用户消息头暂时缺失时（流式事件尚未送达），最新的孤
                        // 儿进程块属于进行中的回合。若没有这一处理，它会在后端
                        // 仍在运行时被判定为 "failed"。
                        liveGroup: running && renderIdx === renderItems.length - 1,
                      })
                    )}
                  </Fragment>
                );
              })}
              {awaitingReply && messages.length === 0 && (
                <div className="flex justify-start">
                  <TurnStatus />
                </div>
              )}
              {historyLoading && showHistorySkeleton && messages.length === 0 && (
                <div className="space-y-3" aria-hidden>
                  {[80, 60, 70].map((w, i) => (
                    <div key={i} className={cn("flex", i % 2 ? "justify-end" : "justify-start")}>
                      <div className="max-w-[80%] space-y-1.5">
                        <Skeleton className="h-3 w-24" />
                        <Skeleton className="h-14 rounded-2xl" style={{ width: `${w}%` }} />
                      </div>
                    </div>
                  ))}
                  <div className="flex items-center justify-center gap-2 pt-2 text-xs text-muted-foreground">
                    <Loader2 className="h-3.5 w-3.5 animate-spin" /> 正在加载会话历史…
                  </div>
                </div>
              )}
              <div ref={bottomRef} />
            </div>
          </ScrollArea>
        </div>

        {/* 供屏幕阅读器读取的回合状态：逐个播报每个流式 token 会造成噪音，因此仅播报阶段切换。 */}
        <div className="sr-only" role="status" aria-live="polite">
          {running ? "助手正在回复…" : messages[messages.length - 1]?.kind === "error" ? "本轮回复出错，可点击重试本轮" : ""}
        </div>
        <div className="shrink-0 bg-canvas py-3">
          {(queued[activeID] ?? []).map((q, qi) => (
            <div key={qi} className="mx-auto mb-2 flex chat-col items-center gap-2 rounded-lg bg-accent-soft px-3 py-1.5 text-xs">
              <span className="shrink-0 text-muted-foreground">{qi === 0 ? "已排队" : `排队 #${qi + 1}`}</span>
              <span className="min-w-0 flex-1 truncate">{q.text}</span>
              {q.reasoning && (
                <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">
                  思考：{levelLabel(q.reasoning)}
                </span>
              )}
              <button
                className="shrink-0 underline underline-offset-2 hover:text-foreground"
                onClick={() => setQueued((prev) => {
                  const next = { ...prev, [activeID]: (prev[activeID] ?? []).filter((_, i) => i !== qi) };
                  return next;
                })}
              >
                取消
              </button>
            </div>
          ))}
          <div className="mx-auto composer-col px-3">
            {editing && (
              <div className="mb-2 flex items-center justify-between rounded-md bg-accent-soft px-3 py-1.5 text-xs text-muted-foreground">
                <span>正在编辑最后一条消息，发送后将替换该轮对话</span>
                <button className="underline underline-offset-2 hover:text-foreground" onClick={cancelEdit}>
                  取消编辑
                </button>
              </div>
            )}
          </div>
          <div className="mx-auto composer-col px-3">
            {pendingAtts.length > 0 && (
              <div className="mb-2 flex flex-wrap gap-2">
                {pendingAtts.map((a, i) => (
                  <span key={i} className="group/att relative flex items-center gap-1.5 rounded-md bg-panel/80 py-1 pl-1 pr-5 text-xs">
                    <img src={`data:${a.mime};base64,${a.data}`} alt={a.name} className="h-8 w-8 rounded object-cover" />
                    <span className="max-w-[120px] truncate">{a.name}</span>
                    <button
                      className="absolute right-1 top-1/2 -translate-y-1/2 rounded p-0.5 text-muted-foreground hover:text-foreground"
                      onClick={() => setPendingAtts((prev) => prev.filter((_, j) => j !== i))}
                    >
                      <X className="h-3 w-3" />
                    </button>
                  </span>
                ))}
              </div>
            )}
          </div>
          <div className="mx-auto composer-col px-3">
            <div
              className={cn(
                "rounded-2xl bg-panel shadow-composer transition-colors",
                dragOver && "bg-accent-soft",
              )}
              onDragOver={(e) => {
                e.preventDefault();
                setDragOver(true);
              }}
              onDragLeave={(e) => {
                // 子元素在指针经过时会触发
                // dragleave：仅当指针实际离开放置区时才清除状态。
                if (!e.currentTarget.contains(e.relatedTarget as Node)) setDragOver(false);
              }}
              onDrop={(e) => {
                e.preventDefault();
                setDragOver(false);
                if (e.dataTransfer.files.length > 0) addFiles(e.dataTransfer.files);
              }}
            >
              <div className="relative">
              {mentionOpen && (
                <div className="absolute bottom-full left-0 right-0 z-10 mb-2 max-h-80 overflow-y-auto rounded-xl bg-overlay p-1 shadow-pop" role="listbox" aria-label="@ 引用候选">
                  {mentionCandidates.map((c, i) => {
                    const key = isAssistantCandidate(c) ? `${c.kind}-${c.id}` : `${c.kind}-${c.path}`;
                    const label = isAssistantCandidate(c) ? `@${c.name}` : `@${c.path}${c.kind === "directory" ? "/" : ""}`;
                    const tag = c.kind === "team" ? "团队" : c.kind === "remote" ? "远程"
                      : c.kind === "agent" ? "助手" : c.kind === "directory" ? "目录" : "文件";
                    return (
                      <button
                        key={key}
                        type="button"
                        role="option"
                        aria-selected={i === mentionIdx}
                        className={cn(
                          "flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs",
                          i === mentionIdx ? "bg-accent-soft text-accent-text" : "hover:bg-accent",
                        )}
                        onMouseEnter={() => setMentionIdx(i)}
                        onMouseDown={(e) => { e.preventDefault(); applyMention(c); }}
                      >
                        {isAssistantCandidate(c)
                          ? c.kind === "team" ? <Users className="h-3.5 w-3.5 shrink-0 text-accent-text" />
                            : c.kind === "remote" ? <Network className="h-3.5 w-3.5 shrink-0 text-accent-text" />
                              : <Bot className="h-3.5 w-3.5 shrink-0 text-accent-text" />
                          : c.kind === "directory" ? <Folder className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                            : <FileText className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
                        <span className="min-w-0 flex-1 truncate">{label}</span>
                        <span className="shrink-0 text-xs text-muted-foreground">{tag}</span>
                      </button>
                    );
                  })}
                  {mentionToken?.leading && (
                    <button
                      type="button"
                      className="flex w-full items-center gap-2 bg-panel/50 px-3 py-1.5 text-left text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
                      onMouseDown={(e) => { e.preventDefault(); onOpenSettings("agents"); }}
                    >
                      <Bot className="h-3.5 w-3.5 shrink-0" /> 管理助手与团队…
                    </button>
                  )}
                </div>
              )}
              {slashOpen && (
                <div className="absolute bottom-full left-0 right-0 z-10 mb-2 max-h-80 overflow-y-auto rounded-xl bg-overlay p-1 shadow-pop" role="listbox" aria-label="斜杠命令建议">
                  {slashSuggestions.map((c, i) => (
                    <button
                      key={c.cmd}
                      role="option"
                      aria-selected={i === slashIdx}
                      className={cn(
                        "flex w-full flex-col gap-0.5 rounded-md px-2.5 py-1.5 text-left text-xs transition-colors",
                        i === slashIdx ? "bg-accent-soft text-accent-text" : "hover:bg-accent",
                      )}
                      onMouseEnter={() => setSlashIdx(i)}
                      onMouseDown={(e) => { e.preventDefault(); applySlash(c.cmd); }}
                    >
                      <span className="flex w-full items-center gap-2">
                        <span className="font-mono font-medium">{c.cmd}</span>
                        {c.source && c.source !== "builtin" && (
                          <span className="shrink-0 rounded bg-panel px-1 text-xs font-normal text-muted-foreground">
                            {slashSourceLabel(c.source)}
                          </span>
                        )}
                      </span>
                      <span className="w-full truncate text-xs font-normal text-muted-foreground">{c.desc}</span>
                    </button>
                  ))}
                </div>
              )}
              <Textarea
                ref={inputRef}
                rows={2}
                value={input}
                onChange={(e) => {
                  setInput(e.target.value);
                  setCaret(e.target.selectionStart ?? e.target.value.length);
                }}
                onSelect={(e) => setCaret(e.currentTarget.selectionStart ?? 0)}
                onKeyDown={(e) => {
                  if (mentionOpen) {
                    if (e.key === "ArrowDown") {
                      e.preventDefault();
                      setMentionIdx((i) => (i + 1) % mentionCandidates.length);
                      return;
                    }
                    if (e.key === "ArrowUp") {
                      e.preventDefault();
                      setMentionIdx((i) => (i - 1 + mentionCandidates.length) % mentionCandidates.length);
                      return;
                    }
                    if ((e.key === "Enter" || e.key === "Tab") && !e.shiftKey && !e.nativeEvent.isComposing) {
                      e.preventDefault();
                      const pick = mentionCandidates[mentionIdx];
                      if (pick) applyMention(pick);
                      return;
                    }
                    if (e.key === "Escape") {
                      e.preventDefault();
                      setMentionDismissed(mentionKey);
                      return;
                    }
                  }
                  // 斜杠建议的优先级低于提及弹窗（二者绝不会同时打开：前缀分别
                  // 为 "/" 与 "@"）。
                  if (slashOpen && !mentionOpen) {
                    if (e.key === "ArrowDown") {
                      e.preventDefault();
                      setSlashIdx((i) => (i + 1) % slashSuggestions.length);
                      return;
                    }
                    if (e.key === "ArrowUp") {
                      e.preventDefault();
                      setSlashIdx((i) => (i - 1 + slashSuggestions.length) % slashSuggestions.length);
                      return;
                    }
                    if ((e.key === "Enter" || e.key === "Tab") && !e.shiftKey && !e.nativeEvent.isComposing) {
                      e.preventDefault();
                      const pick = slashSuggestions[slashIdx];
                      if (pick) applySlash(pick.cmd);
                      return;
                    }
                    if (e.key === "Escape") {
                      e.preventDefault();
                      setSlashDismissed(input);
                      return;
                    }
                  }
                  // Esc 会沿 UI 栈逐层回退，而不是什么都不做： 取消编辑 →
                  // 停止生成 → 关闭筛选栏。
                  if (e.key === "Escape") {
                    if (editing) {
                      e.preventDefault();
                      cancelEdit();
                      return;
                    }
                    if (running) {
                      e.preventDefault();
                      cancel();
                      return;
                    }
                    if (filterOpen) {
                      e.preventDefault();
                      setFilterOpen(false);
                      return;
                    }
                  }
                  // 在空的输入框中按 ↑ 会编辑最后一条用户消息 （Codex
                  // 风格）——这是修正提示词中拼写错误的最快方式。
                  if (e.key === "ArrowUp" && input.length === 0 && !editing && !running && !e.nativeEvent.isComposing && !e.shiftKey && !e.altKey && !e.metaKey && !e.ctrlKey) {
                    for (let i = messages.length - 1; i >= 0; i--) {
                      const m = messages[i];
                      if (m.kind === "user" && m.text) {
                        e.preventDefault();
                        startEdit(m.text);
                        return;
                      }
                      // 用户消息与当前时刻之间有确认卡片，意味着编辑会错误地呈
                      // 现待处理的交互。
                      if (m.kind === "confirm") return;
                    }
                  }
                  if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                    e.preventDefault();
                    send();
                  }
                }}
                onPaste={(e) => {
                  if (!active && providerCount === 0) return;
                  const files = Array.from(e.clipboardData.files || []);
                  if (files.length === 0) return;
                  e.preventDefault();
                  addFiles(files);
                }}
                placeholder={active ? "输入消息，@ 引用文件或指定助手，/ 调用技能" : "输入消息开始新对话"}
                aria-label="消息输入框"
                // 回复流式输出期间仍可继续输入（与本应用所模仿的桌面端一致）；s
                // end() 会对运行中状态加以防护。
                disabled={!active && providerCount === 0}
                // 限制不断增高的文本域，使长内容粘贴时缩小消息列表，而不是把输
                // 入框挤出视口。
                className="max-h-48 min-h-[64px] resize-none overflow-y-auto border-0 bg-transparent px-3.5 pb-1 pt-3 text-md leading-[1.65] shadow-none hover:bg-transparent focus:bg-transparent focus-visible:border-0 focus-visible:ring-0"
              />
              <input
                ref={fileRef}
                type="file"
                accept="image/png,image/jpeg,image/webp,image/gif"
                multiple
                className="hidden"
                onChange={(e) => {
                  if (e.target.files) addFiles(e.target.files);
                  e.target.value = "";
                }}
              />
              </div>
              {composerPolicyHint && (
                <div className="mx-2 mb-1 flex items-start gap-1.5 rounded-lg bg-warning-soft px-2.5 py-1.5 text-xs leading-relaxed text-warning-text">
                  <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                  <span>{composerPolicyHint}</span>
                </div>
              )}
              <div className="flex items-center justify-between gap-2 px-1.5 pb-1.5">
                <div className="flex items-center gap-0.5">
                  <TooltipHint
                    content={!active || modelMultimodal
                      ? "添加图片附件（png/jpeg/webp/gif，最大 6MB）"
                      : "添加图片附件；当前模型未标记多模态，图片可能无法被识别"}
                    wrap
                  >
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label="添加图片附件"
                      disabled={(!active && providerCount === 0) || running}
                      onClick={() => fileRef.current?.click()}
                    >
                      <Plus className="h-4 w-4" />
                    </Button>
                  </TooltipHint>
                  <DropdownMenu>
                    <TooltipHint content={`本会话权限：${composerPolicyLabel}。点击切换`} wrap>
                      <DropdownMenuTrigger asChild>
                        <button
                          type="button"
                          disabled={(!active && providerCount === 0) || executionPolicyBusy}
                          aria-label={`本会话权限：${composerPolicyLabel}`}
                          className={cn(
                            "flex h-7 max-w-[148px] items-center gap-1 rounded-md px-2 text-xs transition-colors",
                            composerExecutionPolicy.sandboxProfile === "danger-full-access"
                              ? "bg-danger-soft text-danger-text hover:bg-overlay"
                              : "text-muted-foreground hover:bg-overlay hover:text-foreground",
                            ((!active && providerCount === 0) || executionPolicyBusy) && "cursor-not-allowed opacity-50",
                          )}
                        >
                          <ComposerProfileIcon className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                          <span className="truncate">
                            {composerProfile ? (composerProfile.shortLabel ?? composerProfile.label) : "自定义"}
                          </span>
                          <ChevronDown className="h-3 w-3 shrink-0 opacity-60" aria-hidden="true" />
                        </button>
                      </DropdownMenuTrigger>
                    </TooltipHint>
                    <DropdownMenuContent align="start" className="w-80">
                      <DropdownMenuLabel>本会话权限</DropdownMenuLabel>
                      <DropdownMenuRadioGroup
                        value={composerProfile?.value ?? ""}
                        onValueChange={(value) => requestPermissionProfile(value as PermissionProfile)}
                      >
                        {PERMISSION_PROFILE_OPTIONS.map((option) => {
                          const Icon = PERMISSION_PROFILE_ICONS[option.value];
                          return (
                            <DropdownMenuRadioItem
                              key={option.value}
                              value={option.value}
                              className="items-start py-2"
                            >
                              <div className="flex gap-2">
                                <Icon
                                  aria-hidden="true"
                                  className={cn(
                                    "mt-0.5 h-4 w-4 shrink-0",
                                    option.danger ? "text-danger-text" : "text-muted-foreground",
                                  )}
                                />
                                <div>
                                  <div className={cn("text-sm font-medium", option.danger && "text-danger-text")}>
                                    {option.label}
                                  </div>
                                  <p className="mt-0.5 whitespace-normal text-xs leading-relaxed text-muted-foreground">
                                    {option.description}
                                  </p>
                                </div>
                              </div>
                            </DropdownMenuRadioItem>
                          );
                        })}
                      </DropdownMenuRadioGroup>
                      {!composerProfile && (
                        <div className="px-2 py-1.5 text-xs leading-relaxed text-muted-foreground">
                          当前为自定义组合：审批「{composerApprovalOption.label}」/ 沙箱「{composerSandboxOption.label}」。选择任一预设会覆盖。
                        </div>
                      )}
                      <div className="px-2 py-1.5 text-xs leading-relaxed text-muted-foreground">
                        只影响当前会话；新会话的默认权限在设置中配置。
                      </div>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>
                <div className="flex min-w-0 items-center gap-1.5">
                  {contextTip && (
                    <Tooltip>
                      <TooltipTrigger asChild>
                        {/* 使用 button（而不是裸 div），让这些数字可通过键盘访问： Radix 会在获得焦点时打开卡片，而扁平化后的 aria-label 即使在悬停卡片绝不渲染时也会包含这些数字。 */}
                        <button
                          type="button"
                          aria-label={contextTip.ariaLabel}
                          className="hidden shrink-0 items-center rounded-md px-1 py-0.5 transition-colors hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 sm:flex"
                        >
                          {contextUsage
                            ? <ContextRing usage={contextUsage} />
                            : <Coins className="h-3.5 w-3.5 text-muted-foreground" />}
                        </button>
                      </TooltipTrigger>
                      <TooltipContent
                        side="top"
                        className="flex-col items-center gap-0.5 text-center"
                      >
                        <span className="font-medium">{contextTip.title}</span>
                        {contextTip.rows.map((line) => (
                          <span key={line}>{line}</span>
                        ))}
                      </TooltipContent>
                    </Tooltip>
                  )}
                  {composerModels.length > 0 && (
                    <Popover
                      open={intelligenceOpen}
                      onOpenChange={(open) => {
                        setIntelligenceOpen(open);
                        if (open) {
                          setIntelligenceView(availableLevels.length > 0 ? "effort" : "model");
                        }
                      }}
                    >
                      <TooltipHint
                        content={`${active ? "本会话" : "新会话"}模型与思考强度：${composerModelDisplay}${availableLevels.length > 0 ? ` · ${levelLabel(nextReasoningLevel)}` : ""}。点击调整`}
                        wrap
                      >
                        <PopoverTrigger asChild>
                          <button
                            type="button"
                            className={cn(
                              "flex h-8 min-w-0 max-w-[360px] items-center gap-1.5 rounded-lg bg-control px-2.5 text-ui text-foreground transition-colors hover:bg-overlay focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 md:min-w-[180px]",
                              intelligenceOpen && "text-muted-foreground",
                            )}
                            aria-label={availableLevels.length > 0 ? "选择推理强度" : "选择模型"}
                          >
                            {intelligenceOpen ? (
                              <span className="min-w-0 flex-1 truncate text-center">
                                {intelligenceView === "model" ? "选择模型" : "选择推理强度"}
                              </span>
                            ) : (
                              <>
                                <span className="min-w-0 truncate">{composerModelDisplay}</span>
                                {availableLevels.length > 0 && (
                                  <span className="shrink-0 text-muted-foreground">{nextReasoningLabel}</span>
                                )}
                              </>
                            )}
                            <ChevronDown className="h-3 w-3 shrink-0 opacity-60" aria-hidden="true" />
                          </button>
                        </PopoverTrigger>
                      </TooltipHint>
                      <PopoverContent
                        side="top"
                        align="end"
                        sideOffset={6}
                        aria-label={intelligenceView === "model" ? "选择模型" : "选择思考强度"}
                        className={cn(
                          "rounded-xl p-0",
                          intelligenceView === "model"
                            ? "w-max min-w-56 max-w-72"
                            : "w-72",
                        )}
                      >
                        {intelligenceView === "model" ? (
                          <div className="p-1.5">
                            <div className="px-2 pb-1 pt-1.5 text-xs font-medium text-muted-foreground">
                              选择模型
                            </div>
                            <div role="listbox" aria-label="选择模型" className="max-h-80 overflow-y-auto">
                              {composerModels.map((m) => {
                                const selected = m.id === composerModelValue;
                                const rowLabel = modelPickerLabel(m, composerProviderName);
                                return (
                                  <button
                                    key={m.id}
                                    type="button"
                                    role="option"
                                    aria-selected={selected}
                                    className={cn(
                                      "flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-ui transition-colors hover:bg-accent-soft",
                                      selected && "bg-accent-soft text-accent-text",
                                    )}
                                    onClick={() => {
                                      if (!selected) chooseComposerModel(m.id);
                                      setIntelligenceOpen(false);
                                    }}
                                  >
                                    <TooltipHint content={`${rowLabel} · ${m.id}`}>
                                      <span className="min-w-0 flex-1 truncate">
                                        {modelLabel(m)}
                                        {composerProviderName && (
                                          <span className={cn("text-muted-foreground", selected && "text-accent-text/80")}>
                                            {` · ${composerProviderName}`}
                                          </span>
                                        )}
                                      </span>
                                    </TooltipHint>
                                    {selected && <Check className="h-4 w-4 shrink-0" aria-hidden="true" />}
                                  </button>
                                );
                              })}
                            </div>
                          </div>
                        ) : (
                          <ReasoningEffortPanel
                            levels={availableLevels}
                            value={nextReasoningLevel}
                            modelName={composerModelDisplay}
                            onChange={setTurnReasoning}
                            onValueCommit={commitReasoning}
                            onOpenModelPicker={() => setIntelligenceView("model")}
                          />
                        )}
                      </PopoverContent>
                    </Popover>
                  )}
                  {running ? (
                    <TooltipHint content="停止生成（Esc）">
                      <Button
                        variant="destructive"
                        size="icon"
                        className="h-7 w-7 rounded-full"
                        aria-label="停止生成"
                        onClick={cancel}
                      >
                        <Square className="h-3.5 w-3.5 fill-current" />
                      </Button>
                    </TooltipHint>
                  ) : (
                    <TooltipHint content={editing ? "重新发送（Enter）" : "发送（Enter）"} wrap>
                      <Button
                        size="icon"
                        className="h-7 w-7 rounded-full"
                        aria-label={editing ? "重新发送" : "发送"}
                        onClick={() => send()}
                        disabled={(!active && providerCount === 0) || !input.trim()}
                      >
                        <ArrowUp className="h-3.5 w-3.5" />
                      </Button>
                    </TooltipHint>
                  )}
                </div>
              </div>
            </div>
            </div>
        </div>
      </div>

      {active && (
        <TaskWorkspacePanel
          sessionID={activeID}
          workspace={resolvedWorkspace || (workspaceError ? "工作区不可用" : "")}
          open={workspacePanelOpen}
          running={running}
          refreshNonce={workspaceRefresh}
          onClose={() => setWorkspacePanelOpen(false)}
        />
      )}

      {/* 会话统计对话框 */}
      <ConfirmDialog request={confirmRequest} onClose={() => setConfirmRequest(null)} />
      <Dialog open={statsOpen} onOpenChange={setStatsOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base">会话统计</DialogTitle>
          </DialogHeader>
          {stats && (
            <>
            <div className="grid grid-cols-2 gap-2 text-sm">
              {[
                ["用户消息", stats.messages],
                ["助手轮次", stats.assistantTurns],
                ["工具调用", stats.toolCalls],
                ["工具失败", stats.toolFailures],
                ["输入 tokens", stats.tokensIn.toLocaleString()],
                ["输出 tokens", stats.tokensOut.toLocaleString()],
                ["总 tokens", stats.tokensTotal.toLocaleString()],
                [statsCost ? "估算成本" : "未配置价格", statsCost || "—"],
              ].map(([k, v], i) => (
                <div key={i} className="rounded-lg bg-panel p-2.5">
                  <div className="text-xs text-muted-foreground">{v}</div>
                  <div className="text-sm font-medium">{k}</div>
                </div>
              ))}
            </div>
            {(stats.authors?.length ?? 0) > 1 && (
              <div className="rounded-lg bg-panel p-2.5">
                <div className="mb-1 text-xs text-muted-foreground">成员时间线</div>
                <table className="w-full text-xs">
                  <thead>
                    <tr className="text-muted-foreground">
                      <th className="text-left font-normal">成员</th>
                      <th className="text-right font-normal">轮次</th>
                      <th className="text-right font-normal">工具调用</th>
                      <th className="text-right font-normal">tokens</th>
                    </tr>
                  </thead>
                  <tbody>
                    {stats.authors!.map((a) => (
                      <tr key={a.author}>
                        <td className="py-0.5">{a.author}</td>
                        <td className="text-right">{a.turns}</td>
                        <td className="text-right">{a.toolCalls}</td>
                        <td className="text-right">{a.tokens.toLocaleString()}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {(stats.daily?.length ?? 0) > 0 && (
              <div className="rounded-lg bg-panel p-2.5">
                <div className="mb-1 text-xs text-muted-foreground">按日明细（成本按当前模型价格估算）</div>
                <table className="w-full text-xs">
                  <thead>
                    <tr className="text-muted-foreground">
                      <th className="text-left font-normal">日期</th>
                      <th className="text-right font-normal">输入</th>
                      <th className="text-right font-normal">输出</th>
                      <th className="text-right font-normal">估算成本</th>
                    </tr>
                  </thead>
                  <tbody>
                    {stats.daily!.map((d) => (
                      <tr key={d.date}>
                        <td className="py-0.5">{d.date}</td>
                        <td className="text-right">{d.promptTokens.toLocaleString()}</td>
                        <td className="text-right">{d.completionTokens.toLocaleString()}</td>
                        <td className="text-right">¥{d.estimatedCost.toFixed(4)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            </>
          )}
        </DialogContent>
      </Dialog>

      {/* 重命名对话框 */}
      <Dialog open={!!renaming} onOpenChange={(v) => !v && setRenaming(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>重命名对话</DialogTitle>
          </DialogHeader>
          <Input value={renameText} onChange={(e) => setRenameText(e.target.value)} />
          <DialogFooter>
            <Button variant="outline" onClick={() => setRenaming(null)}>取消</Button>
            <Button
              onClick={async () => {
                if (renaming) {
                  await Chat.RenameSession(renaming.id, renameText).catch((e) => toast.error(errText(e)));
                  setRenaming(null);
                  reloadSessions();
                }
              }}
            >
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 项目重命名对话框 */}
      <Dialog open={!!renamingProject} onOpenChange={(v) => !v && setRenamingProject(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>重命名项目</DialogTitle>
          </DialogHeader>
          <Input value={renameProjectText} onChange={(e) => setRenameProjectText(e.target.value)} />
          <DialogFooter>
            <Button variant="outline" onClick={() => setRenamingProject(null)}>取消</Button>
            <Button
              onClick={async () => {
                if (renamingProject) {
                  await Chat.SaveProject(renamingProject.id, renameProjectText, "")
                    .then(reloadProjects)
                    .catch((e) => toast.error(errText(e)));
                  setRenamingProject(null);
                  reloadSessions();
                }
              }}
            >
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

/** 新手引导参照 Codex/Claude 桌面端的做法，为首次运行的用户提供指引。 */
