import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Dialogs, Events } from "@wailsio/runtime";
import {
  Plus, Square, Trash2, Pencil, MessageSquare, Bot, Users, Loader2, ArrowUp, ArrowDown,
  Search, Download,
  X, BarChart3, Brain, Network, GitFork, Pin, PinOff, FileCode2, Archive, ClipboardCopy,
  FolderOpen, Folder, FileText,
  PanelRightOpen,
  ChevronRight, ChevronDown,
  Settings as SettingsIcon, CircleHelp, Sun, Moon,
  Boxes, Coins, LockOpen,
  ListTree,
  PanelLeftClose, PanelLeftOpen,
  FolderPlus, MoreHorizontal, SquarePen,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
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
import { cn } from "@/lib/utils";
import { chatItemEndTs, chatItemStartTs, computeEditFlags, computeRunningTools, computeToolDurations, effectiveReasoningLevel, groupSessionsByProject, groupSessionsByTime, groupToolMessages, groupTurns, isDayChange, mdToSpeech, sessionTargetBadgeLabel, type ChatItem, type UIMessage } from "@/lib/chatUtils";
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
import { composerReasoningModel, levelLabel, modelLabel, reasoningLevels } from "@/lib/modelLabel";
import { resolveAgentModel } from "@/lib/agentModel";
import { MessageBubble, DaySeparator } from "@/components/chat/MessageBubble";
import { TurnCard, type TurnBodyOpts } from "@/components/chat/TurnCard";
import { TurnRail, type RailTurn } from "@/components/chat/TurnRail";
import { ToolCallGroup } from "@/components/chat/ToolCallGroup";
import { ContextRing } from "@/components/chat/ContextRing";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { PlanStrip, type TodoItem } from "@/components/chat/TodoCards";
import { Onboarding } from "@/components/chat/Onboarding";
import { TaskWorkspacePanel } from "@/components/TaskWorkspacePanel";
import type { SettingsTab } from "@/pages/SettingsPage";
import {
  Chat, Config, errText, sessionAllowAllTools, setSessionAllowAllTools,
  effectiveSkills, listContextPaths,
  type AgentConfig, type AttachmentIn, type ChatSession, type TeamConfig,
  type RemoteAgentConfig, type UsageInfo, type ModelProvider, type ModelInfo, type SessionStats,
  type Project, type SkillConfig, type WorkspaceEntry,
} from "@/lib/api";
import { contextTooltipLines, estimateContextTokens, resolveContextUsage } from "@/lib/contextUsage";
import {
  ALLOW_ALL_RISK_DESCRIPTION, ALLOW_ALL_RISK_TITLE,
  allowAllRiskAlreadyAcknowledged, allowAllToggleHint, allowAllToggleLabel,
  rememberAllowAllRiskAcknowledged, type AllowAllSource,
} from "@/lib/allowAll";
import {
  effectiveExecutionDisplayMode, executionDisplayLabel,
  normalizeExecutionDisplayMode, EXECUTION_DISPLAY_OPTIONS,
  type ExecutionDisplayMode,
} from "@/lib/executionDisplay";

let mid = 0;
const nextId = () => `m${++mid}`;

/** A send parked while the previous turn streams; it keeps the per-message
 * thinking level the user picked when queueing, not whatever is picked later. */
type QueuedMessage = { text: string; reasoning: string };

// RENDER_LIMIT caps how many messages render at once for smooth long sessions.
const RENDER_LIMIT = 200;

// The sidebar width lives in localStorage rather than config.json: it is a
// per-window view preference, and the config schema round-trips through Go
// bindings that do not need to grow for a splitter drag.
const SIDEBAR_WIDTH_STORAGE_KEY = "wb-chat-sidebar-width";

// Built-in commands run locally, without a model round-trip. Configured
// skills are appended by slashCandidates (issue #53).
const SLASH_COMMANDS: SlashCommand[] = [
  { cmd: "/export", desc: "导出会话为 Markdown", source: "builtin" },
  { cmd: "/exporthtml", desc: "导出会话为 HTML", source: "builtin" },
  { cmd: "/stats", desc: "查看会话统计", source: "builtin" },
  { cmd: "/todos", desc: "查看当前任务计划", source: "builtin" },
  { cmd: "/new", desc: "新建对话", source: "builtin" },
];

/** speak synthesizes speech for text, cancelling previous playback. */
function speak(text: string) {
  if (!("speechSynthesis" in window)) return;
  window.speechSynthesis.cancel();
  const u = new SpeechSynthesisUtterance(mdToSpeech(text));
  u.lang = "zh-CN";
  u.rate = 1.05;
  window.speechSynthesis.speak(u);
}

export function ChatPage({ isDark, onToggleTheme, onOpenHelp, onOpenSettings, version }: {
  isDark: boolean;
  onToggleTheme: () => void;
  onOpenHelp: () => void;
  onOpenSettings: (tab?: SettingsTab) => void;
  version?: string;
}) {
  const [sessions, setSessions] = useState<ChatSession[]>([]);
  const [activeID, setActiveID] = useState("");
  const [messages, setMessages] = useState<UIMessage[]>([]);
  // Per-session input drafts: switching sessions never loses what the user
  // was typing (pairs with parallel background sessions).
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  // Draft typed before any session exists: direct chats are created lazily
  // on the first send, like the composer-first flow of Codex/ChatGPT desktop.
  const [freshInput, setFreshInput] = useState("");
  // Set by「新聊天」: the blank conversation stays a local draft until its
  // first message is sent, so no empty row lands in the sidebar/database and
  // the auto-open-newest effect must not replace it (issue #30).
  const [newChatDraft, setNewChatDraft] = useState(false);
  // Project bound to that pending draft ("" = a direct chat). Only the first
  // send creates the row, so a project's「+」no longer persists an empty
  // conversation per click (issue #60).
  const [draftProjectID, setDraftProjectID] = useState("");
  // Composer picks made before that row exists (issue #64). They stay local
  // until the first send creates the session, then send() applies them to the
  // new chat BEFORE the first turn — so choosing a model or granting「完全访问」
  // here is a real choice, not a discarded one.
  const [draftModelOverride, setDraftModelOverride] = useState("");
  const [draftAllowAll, setDraftAllowAll] = useState(false);
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
  // Configured skills furnish the extra "/" commands (issue #53). The list is
  // session-scoped (see the EffectiveSkills effect below), so a project chat
  // also sees <project>/.agents/skills without importing them.
  const [skills, setSkills] = useState<SkillConfig[]>([]);
  // Workspace files/folders offered by the composer's "@" picker.
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
  const [projectsOpen, setProjectsOpen] = useState(true);
  const [recentOpen, setRecentOpen] = useState(true);
  const [expandedFolderKey, setExpandedFolderKey] = useState<string | null>(null);
  const [resolvedWorkspace, setResolvedWorkspace] = useState("");
  const [workspaceError, setWorkspaceError] = useState("");
  const [workspacePanelOpen, setWorkspacePanelOpen] = useState(false);
  const [workspaceRefresh, setWorkspaceRefresh] = useState(0);
  const [editing, setEditing] = useState(false);
  // Session-level「允许完全访问」: the composer chip and the confirmation card
  // share this one state, whose source of truth is the backend session field.
  // allowAllBusy covers the two-step card action (grant, then approve).
  const [allowAll, setAllowAll] = useState(false);
  const [allowAllBusy, setAllowAllBusy] = useState(false);
  // The composer chip reads the draft grant before the session exists; once a
  // chat is active its persisted grant is the single source of truth.
  const composerAllowAll = activeID ? allowAll : draftAllowAll;
  const allowAllIntentRef = useRef<{ sessionId: string; source: AllowAllSource; msgId?: string; callId?: string } | null>(null);
  // The risk dialog can stay open across a session switch; callbacks compare
  // against the live ID before touching the visible chat.
  const activeIDRef = useRef(activeID);
  useEffect(() => { activeIDRef.current = activeID; }, [activeID]);
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
  // Snapshot taken on pointerdown: the drag computes width from the pointer
  // delta, not from the live state, so a fast drag cannot accumulate error.
  const sidebarDragRef = useRef<{ startX: number; startWidth: number } | null>(null);
  const sidebarResizeFrame = useRef<number | null>(null);
  const pendingSidebarWidthRef = useRef<number | null>(null);
  // Set when the stream emitted an inline error, so send()'s catch does not
  // also toast the same failure.
  const streamedErrorRef = useRef(false);
  // Set when the current turn actually started (a user bubble arrived). The
  // backend refuses turns past the token budget with error+done and a resolved
  // promise — without this flag the typed text would vanish without a trace.
  const turnStartedRef = useRef(false);

  // markRunning tracks in-flight turns PER SESSION so several sessions can
  // stream in parallel without blocking each other.
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
  const executionDisplayMode = effectiveExecutionDisplayMode(
    globalExecutionDisplay,
    active?.executionDisplayMode,
  );

  // fireQueued pops the session's queued message and auto-sends it (used both
  // when a turn settles and when the user returns to a settled session). A
  // still-running session keeps its queue; the message is delivered with its
  // OWN session id so switching chats mid-timer cannot misroute it.
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
  // Latest direct-session creator for the global ⌘N handler, whose listener
  // effect is registered before this definition exists in the render scope.
  const createDirectSessionRef = useRef<() => void>(() => {});
  // New chats are ALWAYS direct (no target): turns run on the default
  // provider's model, and leading @助手 mentions route individual turns to
  // agents, teams or remote A2A agents. No dialog, no pre-configuration.
  // 「新聊天」and a project's「+」only open a local draft; the session is
  // created by send() when the first message actually goes out (issue #30,
  // issue #60 — the project path used to persist a row per click).
  const openNewChatDraft = useCallback((projectID: string) => {
    setActiveID("");
    setNewChatDraft(true);
    setDraftProjectID(projectID);
    setMessages([]);
    setFreshInput("");
    setEditing(false);
    setSessionUsage(null);
    setDraftModelOverride("");
    setDraftAllowAll(false);
  }, []);
  const createDirectSession = useCallback(() => openNewChatDraft(""), [openNewChatDraft]);
  createDirectSessionRef.current = createDirectSession;
  const createProjectSession = useCallback(
    (projectID: string) => openNewChatDraft(projectID),
    [openNewChatDraft],
  );
  useEffect(() => {
    reloadSessions();
    reloadProjects();
    Config.GetConfig()
      .then((c) => {
        setAgents(c.agents ?? []);
        setTeams(c.teams ?? []);
        setRemotes(c.remoteAgents ?? []);
        setProviderCount((c.providers ?? []).length);
        setProviderList(c.providers ?? []);
        setAutoSpeak(!!c.settings?.autoSpeak);
        setGlobalExecutionDisplay(normalizeExecutionDisplayMode(c.settings?.executionDisplayMode));
        setGlobalWorkspace(c.settings?.workspaceDir ?? "");
        setSidebarCollapsed(!!c.settings?.sidebarCollapsed);
      })
      .catch((e) => toast.error(`加载配置失败: ${errText(e)}`));
  }, [reloadSessions]);

  // "/" skills follow the active chat's effective workspace. The backend
  // merges project/global .agents/skills discovery with config.json skills, so
  // a project skill is invocable without a manual import. Re-runs when the
  // workspace is rebound before the first turn (resolvedWorkspace).
  useEffect(() => {
    let cancelled = false;
    effectiveSkills(activeID, activeID ? "" : draftProjectID)
      .then((list) => {
        if (!cancelled) setSkills(list);
      })
      .catch(() => { /* keep the last known list */ });
    return () => { cancelled = true; };
  }, [activeID, draftProjectID, resolvedWorkspace]);

  // "@" picker index: same workspace resolution as the slash skills.
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

  // Each chat carries its own「允许完全访问」grant. Reading it from the
  // backend on every switch keeps the composer chip, the confirmation card
  // and a restart in agreement (the field is persisted on the session).
  useEffect(() => {
    setAllowAll(false);
    setAllowAllBusy(false);
    if (!activeID) return;
    let cancelled = false;
    sessionAllowAllTools(activeID)
      .then((granted) => {
        if (!cancelled) setAllowAll(granted);
      })
      .catch(() => {
        // A session that cannot be read stays on the safe side (ask).
        if (!cancelled) setAllowAll(false);
      });
    return () => {
      cancelled = true;
    };
  }, [activeID]);

  // Open the newest session on first load — unless「新聊天」deliberately left
  // the page on an unsaved draft (issue #30). Any session becoming active
  // ends draft mode.
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

  // The filter is session-scoped: a query left over from another chat would
  // present an unexplained empty list here.
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

  // Load history when switching sessions.
  useEffect(() => {
    if (!activeID) return;
    // Guards against A->B fast switches: a slow response for the previous
    // session must not paint its messages under the new one.
    let cancelled = false;
    setSessionUsage(null);
    setCtxBannerDismissed(false);
    // Drop any pending streamed text — it belongs to the previous session;
    // its full content arrives via the final message event / persisted history.
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
    // Clear immediately: showing the previous session's messages while the
    // next history loads is a correctness-level wrongness (stale flash).
    setMessages([]);
    setRenderLimit(RENDER_LIMIT);
    setActiveTurnId(null);
    setHistoryLoading(true);
    Chat.GetSessionMessages(activeID)
      .then((ms) => {
        if (cancelled) return;
        // Pair confirm_request with its confirm_result so pending approvals
        // stay interactive (e.g. after an app restart).
        const answered = new Map<string, boolean>();
        for (const m of ms ?? []) {
          if (m.kind === "confirm_result" && m.callId) {
            answered.set(m.callId, m.toolResp?.confirmed === true);
          }
        }
        setMessages(
          (ms ?? [])
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
            })),
        );
        // Entering a session always lands on its newest message — the shared
        // ScrollArea keeps the previous session's scrollTop otherwise, which
        // could open a long history at its oldest message.
        requestAnimationFrame(() => bottomRef.current?.scrollIntoView({ block: "end" }));
      })
      .catch((e) => {
        if (!cancelled) toast.error(`加载历史失败: ${errText(e)}`);
      })
      .finally(() => {
        if (!cancelled) setHistoryLoading(false);
      });
    Chat.IsRunning(activeID).then((r) => {
      if (cancelled) return;
      markRunning(activeID, r);
      // A queued message stranded by a session switch (its turn finished in
      // the background) fires now: the user is back and nothing is running.
      if (!r && (queuedRef.current[activeID]?.length ?? 0) > 0) {
        fireQueued(activeID);
      }
    }).catch(() => {});
    // Seed the token/context readout from persisted aggregates so opening an
    // existing chat shows its usage before the next turn completes. A live
    // turn overwrites this with the same cumulative figure on its done event.
    Chat.SessionStats(activeID)
      .then((st) => {
        if (cancelled || !st || st.tokensTotal <= 0) return;
        setSessionUsage({
          promptTokens: st.tokensIn,
          completionTokens: st.tokensOut,
          totalTokens: st.tokensTotal,
          contextTokens: st.contextTokens,
        });
      })
      .catch(() => { /* stats are best-effort */ });
    return () => {
      cancelled = true;
      setHistoryLoading(false);
    };
  }, [activeID]);

  // The backend nudges this event when it changes session metadata out of
  // band (async model-generated titles), so the sidebar picks it up.
  useEffect(() => {
    const off = Events.On("sessions:updated", () => reloadSessions());
    return off;
  }, [reloadSessions]);

  // Streamed text is coalesced: delta events accumulate in a ref and flush to
  // state at most once per interval, so a fast token stream costs one render
  // per interval instead of one per token. Any non-delta event flushes first
  // to preserve ordering within a turn; switching sessions drops the buffer
  // (the final message event / persisted history carries the full text).
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

  // Reasoning streams on its own channel and lives in a separate collapsible
  // bubble, so the model's scratchpad never mixes into the answer text.
  const thinkingBufRef = useRef<{ author: string | null; text: string; timer: number | null }>({
    author: null, text: "", timer: null,
  });
  // Tracks the author whose thinking_delta stream is live: the final thinking
  // event uses it to decide whether to replace that block or append a new one.
  const thinkingStreamedRef = useRef<string | null>(null);
  // Tracks whether the block is still open; finishThinking runs on every
  // answer token, so it must not rescan the list after the first close.
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
        return [...prev, { id: nextId(), kind: "thinking", author, text, streaming: true, timestamp: new Date().toISOString() }];
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

  // Stream events.
  useEffect(() => {
    const off = Events.On("chat:stream", (e: any) => {
      const ev = Array.isArray(e?.data) ? e.data[0] : e?.data;
      if (!ev) return;
      if (ev.kind !== "delta") flushStreamBuf();
      if (ev.kind !== "thinking_delta") flushThinkingBuf();
      // Completion bookkeeping runs for EVERY session: background runs must
      // clear their spinner and toast even while the user is in another chat.
      // (This block used to sit after the active-session filter below, which
      // made it unreachable for non-active sessions.)
      if (ev.kind === "done" || ev.kind === "error") {
        if (ev.kind === "error" && ev.sessionId === activeID) streamedErrorRef.current = true;
        markRunning(ev.sessionId, false);
        reloadSessions();
        // A queued message fires as soon as the turn settles — including
        // after an error, so the user's follow-up is never stranded.
        if ((queuedRef.current[ev.sessionId]?.length ?? 0) > 0 && ev.sessionId === activeID) {
          fireQueued(ev.sessionId);
        }
        if (ev.sessionId !== activeID) {
          const title = sessionsRef.current.find((x: { id: string; title: string }) => x.id === ev.sessionId)?.title ?? "后台会话";
          // Clicking the toast jumps straight to the finished background chat.
          const open = () => setActiveID(ev.sessionId);
          const action = { label: "查看", onClick: open };
          if (ev.kind === "done") toast.success(`「${title}」已完成回复`, { action });
          else toast.error(`「${title}」本轮出错`, { action });
          setUnread((prev) => ({ ...prev, [ev.sessionId]: true }));
        }
      }
      if (ev.sessionId !== activeID) return;
      if (ev.kind === "user") turnStartedRef.current = true;
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
        // Show the block (with its spinner) as soon as the first fragment
        // arrives; the flush tick fills in the text.
        setMessages((prev) => {
          let idx = prev.length - 1;
          while (idx >= 0 && !(prev[idx].kind === "thinking" && prev[idx].streaming && prev[idx].author === ev.author)) idx--;
          if (idx >= 0) return prev;
          return [...prev, { id: nextId(), kind: "thinking", author: ev.author, text: "", streaming: true, timestamp: new Date().toISOString() }];
        });
        return;
      }
      if (ev.kind === "delta") {
        // The answer starting means the reasoning phase is over: collapse the
        // thinking block before the first token lands in the reply bubble.
        finishThinking(ev.author);
        // Coalesce: buffer the text and make sure a streaming bubble exists
        // for this author; the buffered text lands on the flush tick.
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
      setMessages((prev) => {
        switch (ev.kind) {
          case "user":
            return [...prev, { id: nextId(), kind: "user", author: "user", text: ev.text, timestamp: new Date().toISOString(), attachments: ev.attachments ?? null, reasoning: ev.reasoning }];
          case "thinking": {
            // The backend aggregates every thought part of this model call
            // into one event: replace the block the deltas opened (or append
            // one when the provider only returned reasoning in the final
            // response).
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
            return [...prev, { id: nextId(), kind: "thinking", author: ev.author, text: ev.text, streaming: false, timestamp: new Date().toISOString() }];
          }
          case "message": {
            const idx = [...prev].reverse().findIndex((m) => m.streaming && m.author === ev.author);
            if (idx >= 0) {
              const real = prev.length - 1 - idx;
              const copy = [...prev];
              copy[real] = { ...copy[real], text: ev.text, streaming: false, usage: ev.usage ?? null };
              return copy;
            }
            return [...prev, { id: nextId(), kind: "assistant", author: ev.author, text: ev.text, usage: ev.usage ?? null, timestamp: new Date().toISOString() }];
          }
          case "tool_call":
            return [...prev, { id: nextId(), kind: "tool_call", author: ev.author, text: "", toolName: ev.toolName, toolArgs: ev.toolArgs, timestamp: new Date().toISOString() }];
          case "tool_result":
            return [...prev, { id: nextId(), kind: "tool_result", author: ev.author, text: "", toolName: ev.toolName, toolResp: ev.toolResp, timestamp: new Date().toISOString() }];
          case "confirm_request":
            return [...prev, {
              id: nextId(), kind: "confirm", author: ev.author, text: "",
              toolName: ev.toolName, toolArgs: ev.toolArgs,
              callId: ev.callId, hint: ev.hint, confirmStatus: "pending",
              timestamp: new Date().toISOString(),
            } as UIMessage];
          case "error":
            return [...prev, { id: nextId(), kind: "error", author: "system", text: ev.text }];
          case "done":
            thinkingStreamedRef.current = null;
            thinkingOpenRef.current = null;
            return prev.map((m) => (m.streaming ? { ...m, streaming: false } : m));
          default:
            return prev;
        }
      });
      if (ev.kind === "done") {
        setSessionUsage(ev.sessionUsage ?? null);
        setWorkspaceRefresh((n) => n + 1);
        // Read the latest flag through a ref: this subscription is created
        // once per session and would otherwise capture the startup value.
        if (autoSpeakRef.current) {
          const lastAssistant = [...messagesRef.current].reverse().find((m) => m.kind === "assistant" && m.text);
          if (lastAssistant) speak(lastAssistant.text);
        }
      }
    });
    return off;
  }, [activeID, reloadSessions, markRunning, speak]);

  // Auto-scroll only when the reader is already at the bottom, and coalesce
  // per animation frame: scrolling smoothly on every token fights the user
  // and queues animations faster than they finish.
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
  }, [messages]);

  useEffect(() => () => {
    if (scrollFrame.current != null) cancelAnimationFrame(scrollFrame.current);
  }, []);

  // Cmd/Ctrl+N starts a direct chat; Cmd/Ctrl+K focuses the composer.
  // The keydown listener lives in App (the help panel documents them on every
  // page); ChatPage reacts to the dispatched custom events.
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

  // ⌘⇧[ / ⌘⇧] (dispatched by App) walk the session list in its sidebar
  // order (most recent first), wrapping at the ends.
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

  // Window-level drag guard: dropping a file outside the composer must not
  // let the webview navigate away from the app.
  useEffect(() => {
    const prevent = (e: DragEvent) => e.preventDefault();
    window.addEventListener("dragover", prevent);
    window.addEventListener("drop", prevent);
    return () => {
      window.removeEventListener("dragover", prevent);
      window.removeEventListener("drop", prevent);
    };
  }, []);

  // Unread flags: a background session that finished (or errored) while the
  // user was elsewhere gets a sidebar dot until they open it.
  const [unread, setUnread] = useState<Record<string, boolean>>({});
  // How many trailing entries render (tool groups count once); the omitted
  // banner can raise it in RENDER_LIMIT steps to page backwards through very
  // long sessions.
  const [renderLimit, setRenderLimit] = useState(RENDER_LIMIT);
  // Reader position: off-bottom state drives the「回到底部」floater.
  const [atBottom, setAtBottom] = useState(true);
  // Turn rail position: the turn the reader is currently at (scroll-followed;
  // rail clicks override it and the next scroll event re-syncs).
  const [activeTurnId, setActiveTurnId] = useState<string | null>(null);
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const onScroll = () => {
      const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 120;
      setAtBottom(nearBottom);
      // The last turn header above the upper viewport edge is "current".
      const elTop = el.getBoundingClientRect().top;
      let current: string | null = null;
      const nodes = el.querySelectorAll<HTMLElement>("[data-turn-anchor]");
      for (const node of nodes) {
        if (node.getBoundingClientRect().top - elTop > 200) break;
        current = node.dataset.turnAnchor ?? current;
      }
      // At the bottom the reader is on the latest turn even when its header
      // sits below the 200px scan line on a short viewport.
      if (nearBottom && nodes.length > 0) current = nodes[nodes.length - 1].dataset.turnAnchor ?? current;
      if (current == null && nodes.length > 0) current = nodes[0].dataset.turnAnchor ?? null;
      setActiveTurnId(current);
    };
    el.addEventListener("scroll", onScroll, { passive: true });
    return () => el.removeEventListener("scroll", onScroll);
  }, []);
  // Rail / quote click: scroll the turn into view and mark it active.
  const selectTurn = useCallback((msgId: string) => {
    setActiveTurnId(msgId);
    document.getElementById(`turn-${msgId}`)?.scrollIntoView({ behavior: "smooth", block: "start" });
  }, []);
  // History fetch in flight after a session switch: render skeletons instead
  // of an empty-looking conversation.
  const [historyLoading, setHistoryLoading] = useState(false);
  // Shared destructive-confirmation dialog (replaces window.confirm).
  const [confirmRequest, setConfirmRequest] = useState<ConfirmRequest | null>(null);

  // Per-session message queue: sending while a reply streams parks the text
  // and auto-sends it when the turn settles (Codex-style queueing). A queue
  // holds MULTIPLE messages — queueing twice must not silently drop the
  // first; deliveries bounce to the front, user sends append to the back.
  const [queued, setQueued] = useState<Record<string, QueuedMessage[]>>({});
  const queuedRef = useRef(queued);
  useEffect(() => { queuedRef.current = queued; }, [queued]);
  // Per-turn thinking level: a composer-local choice that applies ONLY to the
  // next message. Empty follows the session default; it is never persisted
  // (that is what the session-level default is for).
  const [turnReasoning, setTurnReasoning] = useState("");
  const runningMapRef = useRef(runningMap);
  useEffect(() => { runningMapRef.current = runningMap; }, [runningMap]);

  const send = async (override?: string, targetID?: string, reasoningOverride?: string) => {
    // targetID lets fireQueued deliver to the session the message was queued
    // for, not whichever chat happens to be active 80ms later.
    let sid = targetID ?? activeID;
    const text = (override ?? input).trim();
    if (!text) return;
    // Whether this send originates from the active composer (or a chat yet to
    // be created): only then do the pending image attachments belong to it.
    const fromComposer = !sid || sid === activeID;
    // A queued delivery carries the level picked when it was queued; a
    // composer send reads the per-turn pick at the moment Send is pressed.
    const reasoning = reasoningOverride !== undefined ? reasoningOverride : fromComposer ? turnReasoning : "";
    // Composer-first flow: with no session yet, the first message creates a
    // direct chat on the fly. Only a provider is required — never an Agent.
    if (!sid) {
      if (providerCount === 0) {
        toast.error("请先在「模型配置」页添加供应商并填入 API Key");
        return;
      }
      try {
        const s = draftUsesProject(draftProjectID)
          ? await Chat.NewSessionWithProject("", "", "", draftProjectID)
          : await Chat.NewSessionWithWorkspace("", "", "", "");
        sid = s.id;
        // Apply the draft picks to the freshly created row BEFORE it becomes
        // the active chat: the allow-all reader effect then sees the real
        // grant instead of racing a read that predates the write (issue #64).
        try {
          if (draftModelOverride) await Chat.SetSessionModel(s.id, draftModelOverride);
          if (draftAllowAll) {
            await setSessionAllowAllTools(s.id, true, "composer");
            // The write is done, so the reader effect that runs when the chat
            // becomes active will confirm this instead of flashing it off.
            setAllowAll(true);
          }
        } catch (e) {
          // The row exists, so the first turn must still go out; surface the
          // lost pick rather than silently pretending it was applied.
          toast.error(`应用草稿设置失败: ${errText(e)}`);
        }
        setDraftModelOverride("");
        setDraftAllowAll(false);
        setFreshInput("");
        setDraftProjectID("");
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
      // Queued deliveries that raced with a newly-busy session go back into
      // the queue (fired again when the next turn settles) — never dropped.
      // They re-enter at the FRONT to preserve the user's ordering.
      if (override !== undefined) {
        setQueued((prev) => ({ ...prev, [sid]: [{ text, reasoning }, ...(prev[sid] ?? [])] }));
      }
      return;
    }
    // Slash commands run locally without a model round-trip (active chat
    // only — queued sends to background sessions go through as messages).
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
          for (let i = messages.length - 1; i >= 0; i--) {
            const m = messages[i];
            const todos = m.toolResp?.todos;
            if (m.kind === "tool_result" && m.toolName === "plan_todos" && Array.isArray(todos) && todos.length > 0) {
              const done = todos.filter((t: TodoItem) => t.status === "completed").length;
              const lines = todos.map((t: TodoItem) =>
                `${t.status === "completed" ? "✓" : t.status === "in_progress" ? "▶" : "○"} ${t.content}`,
              );
              toast(`${done}/${todos.length} 完成`, { description: lines.join("\n") });
              return;
            }
          }
          toast.info("当前会话还没有任务计划（Agent 挂载「任务计划」内置工具后可用）");
          return;
        }
        case "/new":
          setInput("");
          createDirectSession();
          return;
        default:
          // A "/<skill-name>" command is sent as a normal message: the
          // backend injects that skill's SKILL.md into this turn only
          // (issue #53). Unknown tokens keep the old contract — hint, no send.
          if (isSkillCommand(text, skills)) break;
          toast.error(
            `未知命令 ${cmd}。可用：${SLASH_COMMANDS.map((c) => c.cmd).join(" ")}` +
              (skills.length > 0 ? `；或输入 / 选择 ${skills.length} 个 Skill` : ""),
          );
          return;
      }
    }
    if (sid === activeID) setInput("");
    markRunning(sid, true);
    const wasEditing = editing && sid === activeID;
    setEditing(false);
    // The per-turn choice is consumed by a normal send. Editing re-runs the
    // original turn with its recorded level, so a pending pick stays pending.
    if (fromComposer && !wasEditing) setTurnReasoning("");
    const atts = fromComposer ? pendingAtts : [];
    if (fromComposer) setPendingAtts([]);
    turnStartedRef.current = false;
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
      // A resolved promise without a started turn means the backend refused
      // the round (e.g. token budget exhausted): restore what the user typed
      // instead of silently swallowing it. Only the active chat has drafts to
      // restore — background deliveries carry no UI state.
      if (fromComposer && !turnStartedRef.current) {
        setInput((cur) => (cur === "" ? text : cur));
        setPendingAtts((prev) => [...atts, ...prev]);
        if (!wasEditing) setTurnReasoning((cur) => (cur === "" ? reasoning : cur));
      }
    } catch (e) {
      // The stream already emitted an inline error bubble for run failures;
      // Chat.Send returns the same error so the promise settles, but only
      // surface a toast when nothing was streamed (e.g. validation failure).
      const msg = errText(e);
      if (!streamedErrorRef.current) toast.error(`发送失败: ${msg}`);
      streamedErrorRef.current = false;
      markRunning(sid, false);
      if (fromComposer) {
        // Restore the draft and attachments (merging, not clobbering: the
        // user may have started typing again while the request was in flight).
        setPendingAtts((prev) => [...atts, ...prev]);
        setInput((cur) => (cur === "" ? text : cur));
      }
    }
  };

  // Latest send for the stream subscription: the subscription is created
  // once per session and must not fire a stale closure.
  const sendRef = useRef(send);
  useEffect(() => { sendRef.current = send; });

  const cancel = () => {
    if (activeID) Chat.Cancel(activeID);
  };

  // answerConfirm resolves a HITL tool-confirmation card. The status flips
  // optimistically; a failed resume reverts the card to pending.
  const answerConfirm = useCallback(async (msgId: string, callId: string | undefined, approved: boolean) => {
    if (!activeID || !callId || running) return;
    markRunning(activeID, true);
    setMessages((prev) => prev.map((m) => (m.id === msgId ? { ...m, confirmStatus: approved ? "approved" : "rejected" } : m)));
    try {
      await Chat.ConfirmToolCall(activeID, callId, approved);
    } catch (e) {
      if (!streamedErrorRef.current) toast.error(`确认失败: ${errText(e)}`);
      streamedErrorRef.current = false;
      setMessages((prev) => prev.map((m) => (m.id === msgId && m.kind === "confirm" ? { ...m, confirmStatus: "pending" } : m)));
      markRunning(activeID, false);
    }
  }, [activeID, running, markRunning]);

  // applyAllowAll is the single place that turns the session grant on. The
  // card action must persist the grant BEFORE approving the pending call:
  // the resumed run is rebuilt from the session and would otherwise ask
  // again. Both entry points funnel through here so they cannot disagree.
  const applyAllowAll = useCallback(async (intent: { sessionId: string; source: AllowAllSource; msgId?: string; callId?: string }) => {
    const sid = intent.sessionId;
    rememberAllowAllRiskAcknowledged((k, v) => localStorage.setItem(k, v));
    if (!sid) {
      // The draft has no session row yet (issue #64). Remember the grant on the
      // composer; send() persists it when the first message creates the chat.
      setDraftAllowAll(true);
      toast.success("已开启完全访问：本会话不再逐次确认写文件与执行命令");
      return;
    }
    const visible = () => sid === activeIDRef.current;
    if (visible()) setAllowAll(true);
    if (intent.source === "composer") {
      try {
        await setSessionAllowAllTools(sid, true, "composer");
        toast.success("已开启完全访问：本会话不再逐次确认写文件与执行命令");
      } catch (e) {
        if (visible()) setAllowAll(false);
        toast.error(`开启完全访问失败: ${errText(e)}`);
      }
      return;
    }
    if (!intent.callId) return;
    setAllowAllBusy(true);
    markRunning(sid, true);
    if (visible()) {
      setMessages((prev) => prev.map((m) => (m.id === intent.msgId ? { ...m, confirmStatus: "approved" } : m)));
    }
    let granted = false;
    try {
      await setSessionAllowAllTools(sid, true, "confirm_card");
      granted = true;
      await Chat.ConfirmToolCall(sid, intent.callId, true);
    } catch (e) {
      if (!streamedErrorRef.current) toast.error(`确认失败: ${errText(e)}`);
      streamedErrorRef.current = false;
      if (visible()) {
        setAllowAll(false);
        setMessages((prev) => prev.map((m) => (m.id === intent.msgId && m.kind === "confirm" ? { ...m, confirmStatus: "pending" } : m)));
      }
      markRunning(sid, false);
      // Best-effort rollback so a half-applied card action cannot leave the
      // backend granting access while the UI shows it off.
      if (granted) {
        try {
          await setSessionAllowAllTools(sid, false, "confirm_card");
        } catch { /* the toggle can still revoke it later */ }
      }
    } finally {
      setAllowAllBusy(false);
    }
  }, [markRunning]);

  // requestAllowAll shows the one-time risk notice before the grant takes
  // effect; an already acknowledged device goes straight through. The session
  // is captured now, so switching chats while the dialog is open still acts on
  // the chat whose card/switch was used.
  const requestAllowAll = useCallback((intent: { source: AllowAllSource; msgId?: string; callId?: string }) => {
    // An empty activeID is the draft composer: the risk gate runs now and the
    // grant is carried into the session created by the first send (issue #64).
    const full = { ...intent, sessionId: activeID };
    if (allowAllRiskAlreadyAcknowledged((k) => localStorage.getItem(k))) {
      void applyAllowAll(full);
      return;
    }
    allowAllIntentRef.current = full;
    setConfirmRequest({
      title: ALLOW_ALL_RISK_TITLE,
      description: ALLOW_ALL_RISK_DESCRIPTION,
      confirmText: "确认开启",
      cancelText: "取消",
      destructive: false,
      action: () => {
        const pending = allowAllIntentRef.current;
        allowAllIntentRef.current = null;
        if (pending) void applyAllowAll(pending);
      },
    });
  }, [activeID, applyAllowAll]);

  // allowAllFromCard answers the current confirmation and grants the session
  // in one click (issue #48): approve once, then stop asking.
  const allowAllFromCard = useCallback((msgId: string, callId: string | undefined) => {
    if (!activeID || !callId || running) return;
    requestAllowAll({ source: "confirm_card", msgId, callId });
  }, [activeID, running, requestAllowAll]);

  // toggleAllowAll is the composer switch: turning the grant off restores
  // per-call approval for the following turns.
  const toggleAllowAll = useCallback(() => {
    if (allowAllBusy) return;
    const granted = activeID ? allowAll : draftAllowAll;
    if (!granted) {
      requestAllowAll({ source: "composer" });
      return;
    }
    if (!activeID) {
      // Draft grant: there is nothing to revoke on the backend yet.
      setDraftAllowAll(false);
      toast.success("已关闭完全访问：写文件与执行命令恢复逐次确认");
      return;
    }
    const sid = activeID;
    setAllowAll(false);
    setSessionAllowAllTools(sid, false, "composer")
      .then(() => toast.success("已关闭完全访问：写文件与执行命令恢复逐次确认"))
      .catch((e) => {
        setAllowAll(true);
        toast.error(`关闭完全访问失败: ${errText(e)}`);
      });
  }, [activeID, allowAll, draftAllowAll, allowAllBusy, requestAllowAll]);

  const MAX_ATT_BYTES = 6 * 1024 * 1024;
  const allowedMIME = ["image/png", "image/jpeg", "image/webp", "image/gif"];

  const addFiles = async (files: FileList | File[]) => {
    let added = 0;
    if (active && !modelMultimodal) {
      // The multimodal flag is metadata the user may not have flipped yet:
      // keep the upload path open, but warn that the model may reject images.
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
      markRunning(activeID, false);
    }
  }, [activeID, running, markRunning]);

  /** speakMessage is stable per call site so memoized bubbles can skip it. */
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
      // Closing the native picker is not a failure: Wails reports it as an
      // error ("cancelled by user"). Only surface genuine dialog failures.
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

  // Debounced content search: with a query present, the sidebar lists sessions
  // whose title OR stored history matches (backend LIKE over event payloads).
  // A monotonic sequence drops stale responses: a slow reply for an earlier
  // query must not overwrite the result of the latest one.
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
    // Archived sessions pass the same query filter; grouping buckets them
    // into the trailing "已归档" group whose visibility showArchived controls.
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
  }, [sidebarCollapsed]);
  // ⌘B is a shell-level shortcut like ⌘N: App dispatches the custom event so
  // it keeps working across view switches.
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

  // The splitter drags the sidebar edge. Pointer capture keeps the gesture
  // alive when the cursor leaves the 6px handle, and body-level cursor +
  // selection suppression stop the drag from selecting session rows or text
  // in the chat area (issue #31). Width updates ride rAF so a drag does not
  // re-render the message list on every pointermove.
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

  // Window shrinks past the stored width: clamp back into the visible range
  // instead of letting the sidebar eat the chat area.
  useEffect(() => {
    const onResize = () => setSidebarWidth((w) => clampSidebarWidth(w, window.innerWidth));
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);

  // Sidebar sections: 「项目」 folders derive from chat workspaces —
  // registered projects first, then ad-hoc explicit directories (basename
  // labels). Chats filed into a folder leave the 「最近」 recency list, the
  // way chat apps tuck project chats away; auto task dirs under the global
  // workspace stay in 最近. Archived trails last. With a search query the
  // sections collapse into one flat result list.
  const searching = search.trim().length > 0;
  const folderGroups = useMemo(() => {
    const groups = groupSessionsByProject(filteredSessions, projects, globalWorkspace)
      .filter((g) => g.key !== "default" && g.key !== "archived");
    // Registered projects with no chats yet still deserve a row (expanded it
    // shows empty; the row menu is the way to start its first chat).
    const seen = new Set(groups.map((g) => g.projectId));
    for (const p of projects) {
      if (!seen.has(p.id)) groups.push({ key: `proj:${p.id}`, label: p.name, path: p.rootDir, projectId: p.id, sessions: [] });
    }
    return groups;
  }, [filteredSessions, projects, globalWorkspace]);
  const folderSessionIds = useMemo(() => {
    const ids = new Set<string>();
    for (const g of folderGroups) for (const s of g.sessions) ids.add(s.id);
    return ids;
  }, [folderGroups]);
  const folderKeyBySession = useMemo(() => {
    const map = new Map<string, string>();
    for (const g of folderGroups) for (const s of g.sessions) map.set(s.id, g.key);
    return map;
  }, [folderGroups]);
  // `now` is captured per render: time buckets age with the wall clock across
  // long-lived windows without needing a timer.
  const timeGroups = useMemo(
    () => groupSessionsByTime(
      filteredSessions.filter((s) => !s.archived && !folderSessionIds.has(s.id)),
      new Date(),
    ),
    [filteredSessions, folderSessionIds],
  );
  const archivedSessions = useMemo(() => filteredSessions.filter((s) => s.archived), [filteredSessions]);
  const searchResults = useMemo(
    () => [...filteredSessions].sort((a, b) => (b.updatedAt ?? "").localeCompare(a.updatedAt ?? "")),
    [filteredSessions],
  );
  const folderKeyRef = useRef<Map<string, string>>(new Map());
  // Opening a chat auto-expands the folder that owns it so the row is visible
  // inside the accordion. The map flows through a ref so only chat switches
  // trigger this; unrelated session reloads must not re-expand.
  useEffect(() => {
    folderKeyRef.current = folderKeyBySession;
  }, [folderKeyBySession]);
  useEffect(() => {
    const key = folderKeyRef.current.get(activeID);
    if (key) setExpandedFolderKey(key);
  }, [activeID]);

  // Provider + model of the active target (agent → its model; team →
  // coordinator when auto, lead agent otherwise) for cost estimation and
  // multimodal gating.
  const target = useMemo(() => {
    if (!active) return { provider: null as ModelProvider | null, model: null as ModelInfo | null };
    // 会话级模型覆盖与后端 EffectiveModel 同口径：覆盖 ID 必须在解析出的
    // 供应商模型列表内，否则回落默认解析（直聊=默认供应商第一个模型）。
    const override = active.modelOverride;
    const withOverride = (provider: ModelProvider | null, fallback: ModelInfo | null): ModelInfo | null => {
      if (override && provider?.models) {
        const hit = provider.models.find((m) => m.id === override);
        if (hit) return hit;
      }
      return fallback;
    };
    if (active.targetType === "") {
      // Direct chat: default provider's first model (the same resolution the
      // backend uses for target-less sessions).
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

  /** modelMultimodal only tunes the attachment hint: the picker stays open so
 * users can upload images even before flipping the model's metadata flag. */
  const modelMultimodal = !!target.model?.multimodal;

  /** costOf computes ¥ cost from token usage at model rates (per 1M). */
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

  // Per-session model picker (agent and direct sessions): mirrors the per-chat
  // model selector of Claude/ChatGPT desktops.
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

  // Draft composer (issue #64): before the first send there is no session row
  // to read, so the picker resolves against the default provider the new chat
  // will use and the pick lives in component state until send() persists it.
  // New chats are always direct (targetless), so this matches what the
  // created session resolves to.
  const draftProvider = useMemo(
    () => providerList.find((p) => p.isDefault) ?? providerList[0] ?? null,
    [providerList],
  );
  const draftModels = draftProvider?.models ?? [];
  const draftModel = draftModelFor(draftModels, draftModelOverride);
  // One set of values feeds the toolbar for both states, so the draft and the
  // saved chat render exactly the same control.
  const composerModels = active ? sessionModels : draftModels;
  // The picker has no synthetic "default model" row. Its checked value is the
  // model that will actually run; an unset override resolves to a real model
  // (direct chats: the provider's first; agents: their configured model).
  const composerModel = active ? (target.model ?? composerModels[0] ?? null) : draftModel;
  const composerModelValue = composerModel?.id ?? "";
  const composerModelLabel = composerModel ? modelLabel(composerModel) : "";
  const composerProviderName = active ? (target.provider?.name ?? "") : (draftProvider?.name ?? "");
  const chooseComposerModel = (value: string) => {
    if (!active) {
      setDraftModelOverride(value);
      return;
    }
    void setSessionModel(value);
  };

  // Thinking picker. A saved table wins; a model saved before level tables
  // existed still gets the built-in family fallback (fetched from the backend
  // so the mapping lives in one place) instead of hiding the control — an
  // explicitly disabled table stays off (#42). The per-turn pick lives only
  // in this composer: it is never persisted and never mutates the session
  // default.
  // Composer model: a draft resolves exactly like the first send will
  // (draftModelFor), so the thinking picker is selectable before the session
  // row exists — same model, same level table, no drift at send time.
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
  // Only a suggestion fetched FOR this model may feed the picker: a fetch
  // resolving after a model switch must not render another model's levels.
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
  // The pending pick applies to the next send only; unknown names (e.g. a
  // stale pick after a model switch) are ignored and fall through.
  const pendingReasoning = availableLevels.some((l) => l.level === turnReasoning) ? turnReasoning : "";
  const nextReasoningLevel = effectiveReasoningLevel(availableLevels, pendingReasoning, sessionReasoning, defaultReasoningLevel);
  const nextReasoningLabel = pendingReasoning
    ? `本轮 ${levelLabel(pendingReasoning)}`
    : sessionReasoning ? `会话 ${levelLabel(sessionReasoning)}` : levelLabel(nextReasoningLevel);
  // A pick belongs to one chat+model; switching either starts from the
  // session default again (a level valid for the old table may not exist).
  // A pick belongs to one chat+model; switching either (including a draft's
  // model chip) starts from the session default again — a level valid for the
  // old table may not exist on the new one.
  useEffect(() => { setTurnReasoning(""); }, [activeID, composerModelID]);

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

  const setSessionReasoning = async (level: string) => {
    if (!activeID) return;
    try {
      await Chat.SetSessionReasoningLevel(activeID, level);
      setTurnReasoning("");
      reloadSessions();
      toast.success(level === "" ? "已清除会话默认思考强度" : `会话默认思考强度：${levelLabel(level)}`);
    } catch (e) {
      toast.error(`设置会话思考强度失败: ${errText(e)}`);
    }
  };

  const setSessionExecutionDisplay = async (mode: string) => {
    if (!activeID) return;
    try {
      await Chat.SetSessionExecutionDisplay(activeID, mode);
      reloadSessions();
      toast.success(mode === ""
        ? "执行过程显示已跟随全局设置"
        : `本会话执行过程：${executionDisplayLabel(normalizeExecutionDisplayMode(mode))}`);
    } catch (e) {
      toast.error(`设置执行过程显示失败: ${errText(e)}`);
    }
  };

  // ⌘F (dispatched by App) opens the in-conversation filter.
  useEffect(() => {
    const onToggle = () => setFilterOpen(true);
    window.addEventListener("wb-toggle-filter", onToggle);
    return () => window.removeEventListener("wb-toggle-filter", onToggle);
  }, []);

  const toolDurations = useMemo(() => computeToolDurations(messages), [messages]);
  const runningToolsSet = useMemo(() => computeRunningTools(messages), [messages]);
  // Precompute once per message change instead of scanning the tail for every
  // rendered row (that was O(n^2) per token).
  const editFlags = useMemo(() => computeEditFlags(messages), [messages]);
  // Latest session plan (plan_todos result): pinned above the composer so
  // multi-step progress stays visible after later messages scroll by.
  const activePlan = useMemo(() => {
    for (let i = messages.length - 1; i >= 0; i--) {
      const m = messages[i];
      const todos = m.toolResp?.todos;
      if (m.kind === "tool_result" && m.toolName === "plan_todos" && Array.isArray(todos) && todos.length > 0) {
        return todos as TodoItem[];
      }
    }
    return null;
  }, [messages]);
  // In-conversation message filter (⌘F / the header search icon): shows only
  // the messages matching the query, like the desktop clients' chat search.
  const [filterOpen, setFilterOpen] = useState(false);
  const [msgFilter, setMsgFilter] = useState("");
  const filterInputRef = useRef<HTMLInputElement>(null);
  const filterQ = msgFilter.trim().toLowerCase();
  // Precomputed lowercase search blob per message: keystroke-time filtering
  // must not re-JSON.stringify large tool payloads for the whole history.
  const searchIndex = useMemo(() => {
    const map = new Map<string, string>();
    // Only consulted while the filter bar is open: skip the per-message
    // JSON.stringify pass on every token of a streaming reply.
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
  // The slice source and how many entries sit above the render window: the
  // day separator of the first visible item must compare against the REAL
  // previous entry, or a truncation window would paint a fake day header.
  const renderSource = filterQ ? filteredMessages : messages;
  // Collapse consecutive tool events into execution blocks before slicing:
  // the render limit then caps rendered entries (blocks included), and a run
  // is never split across the truncation boundary.
  const renderItems = useMemo(() => groupTurns(groupToolMessages(renderSource)), [renderSource]);
  const visibleItems = useMemo(() => renderItems.slice(-renderLimit), [renderItems, renderLimit]);
  const omittedItems = Math.max(0, renderItems.length - visibleItems.length);
  // The last turn owns the live clock: its execution blocks keep ticking
  // until the turn settles.
  const firstVisibleIdx = renderItems.length - visibleItems.length;
  const lastTurnIdx = useMemo(() => {
    for (let k = renderItems.length - 1; k >= 0; k--) {
      if (renderItems[k].kind === "turn") return k;
    }
    return -1;
  }, [renderItems]);
  // The banner counts hidden MESSAGES (a collapsed block or turn covers
  // many), while the paging button reveals whole entries in RENDER_LIMIT steps.
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
  // Rail entries mirror what is rendered: paged-out turns are not navigable.
  const railTurns = useMemo(() => {
    const turns: RailTurn[] = [];
    for (const it of visibleItems) {
      if (it.kind !== "turn") continue;
      const text = it.header.text.trim() || "（仅附件）";
      turns.push({ id: it.header.id, text: text.length > 160 ? `${text.slice(0, 160)}…` : text });
    }
    return turns;
  }, [visibleItems]);
  // Seed / repair the active bar when no scroll event has fired yet (a short
  // session never scrolls) or the active turn got filtered/paged out.
  useEffect(() => {
    if (railTurns.length === 0) return;
    if (activeTurnId && railTurns.some((t) => t.id === activeTurnId)) return;
    setActiveTurnId(railTurns[railTurns.length - 1].id);
  }, [railTurns, activeTurnId]);
  // TurnCard bodies reuse the exact prop wiring the flat list used.
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
          onAllowAll={allowAllFromCard}
          allowAllBusy={allowAllBusy}
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
        onAllowAll={allowAllFromCard}
        allowAllBusy={allowAllBusy}
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
  // Slash suggestions match the mention popup's interaction: full keyboard
  // navigation, Enter/Tab to pick, Esc to dismiss until the text changes.
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

  // Auto-grow the composer with its content, capped by max-h-48 (192px) —
  // beyond the cap the textarea scrolls internally and the message list
  // keeps the remaining space.
  useEffect(() => {
    const ta = inputRef.current;
    if (!ta) return;
    ta.style.height = "auto";
    ta.style.height = `${Math.min(ta.scrollHeight, 192)}px`;
  }, [input, activeID]);
  // Visual feedback while a file hovers the composer drop zone.
  const [dragOver, setDragOver] = useState(false);

  // @-mention autocomplete. A leading "@名称" routes that turn to the named
  // agent/team/remote (the backend parses it), while every token also offers
  // workspace files/folders as context. The token is caret-aware, so "@"
  // works mid-message and a mid-token pick rewrites only that token.
  const [caret, setCaret] = useState(0);
  useEffect(() => {
    // A session switch replaces the draft; keep the caret at its end. Typing
    // inside one session must not be pulled back to the end.
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
  // The workspace index can arrive while the popup is open; never leave the
  // highlight pointing past the end of the new candidate list.
  useEffect(() => {
    if (mentionCandidates.length > 0 && mentionIdx >= mentionCandidates.length) setMentionIdx(0);
  }, [mentionCandidates.length, mentionIdx]);
  const mentionOpen = mentionKey != null && mentionKey !== mentionDismissed && mentionCandidates.length > 0;
  const applyMention = (c: MentionCandidate) => {
    if (!mentionToken) return;
    const value = isAssistantCandidate(c) ? c.name : c.kind === "directory" ? `${c.path}/` : c.path;
    // A folder pick keeps the token open-ended (`@src/`) so the menu drills in;
    // files and assistants finish the token with a space.
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

  // contextUsage: how much of the target model's context window the session
  // has consumed so far. The numerator is the REAL context occupancy — the
  // last model call's prompt token count (contextTokens) — not the cumulative
  // billed usage: every call re-sends the full history, so the sum over a
  // tool loop is a multiple of the real size and once showed 6.4M tokens
  // (100%) for a session whose true occupancy was 16.6%.
  const contextWindow = target.model?.contextWindow ?? 0;
  const realContextUsage = useMemo(
    () => resolveContextUsage(sessionUsage?.contextTokens, contextWindow),
    [contextWindow, sessionUsage],
  );
  // ≈ fallback for models whose upstream never reports usage metadata (some
  // gateways ignore stream_options.include_usage): approximate the next
  // prompt from the conversation itself. Debounced — the estimate is coarse
  // and must not re-run JSON.stringify on every streamed flush.
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

  // Whole-session cost estimate for the stats dialog (label/value order must
  // match the other stat cells: value muted on top, label bold below).
  const statsCost = useMemo(
    () => (stats ? costOf({ promptTokens: stats.tokensIn, completionTokens: stats.tokensOut, totalTokens: stats.tokensTotal }) : ""),
    [stats, costOf],
  );

  // renderSessionRow draws one chat list row; shared by the project accordion,
  // the 最近 list and search results so hover actions stay identical.
  const renderSessionRow = (s: ChatSession) => (
    <div
      key={s.id}
      role="button"
      tabIndex={0}
      title={s.title}
      className={cn(
        "group relative flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-1.5 text-ui",
        s.id === activeID ? "bg-brand/10 text-foreground" : "text-muted-foreground hover:bg-accent/50 hover:text-foreground",
      )}
      onClick={() => setActiveID(s.id)}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          setActiveID(s.id);
        }
      }}
    >
      {s.id === activeID && (
        <span aria-hidden className="absolute left-0 top-1/2 h-4 w-0.5 -translate-y-1/2 rounded-full bg-brand" />
      )}
      {s.targetType === "team" ? <Users className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        : s.targetType === "remote" ? <Network className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          : s.targetType === "" ? <MessageSquare className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            : <Bot className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
      <span className="min-w-0 flex-1">
        <span className="block truncate" title={s.forkedFrom ? `分叉自「${sessions.find((x) => x.id === s.forkedFrom)?.title ?? s.forkedFrom}」` : s.title}>
          {s.forkedFrom && <GitFork className="mr-1 inline h-3 w-3 text-muted-foreground" />}
          {s.title}
        </span>
        {s.preview && <span className="block truncate text-xs text-muted-foreground/80">{s.preview}</span>}
      </span>
      {s.pinned && <Pin className="h-3 w-3 shrink-0 text-brand" aria-label="已置顶" />}
      {s.archived && <Badge variant="outline" className="shrink-0 px-1 py-0 text-2xs text-muted-foreground">归档</Badge>}
      {runningMap[s.id] && <Loader2 className="h-3 w-3 shrink-0 animate-spin text-brand" aria-label="生成中" />}
      {!runningMap[s.id] && unread[s.id] && <span className="h-2 w-2 shrink-0 rounded-full bg-brand" aria-label="有新回复未查看" />}
      {/* The cluster hosts the "…" dropdown anchor, so it must keep its layout
          box while the menu is open — see SESSION_ROW_ACTIONS_CLASS (#28). */}
      <span className={SESSION_ROW_ACTIONS_CLASS} data-slot="session-row-actions">
        {/* Frequent state toggles stay one click; the rest collapse into one
            menu so the row keeps four controls instead of six. */}
        <button
          className="rounded p-0.5 hover:bg-foreground/10"
          title={s.archived ? "取消归档" : "归档此会话"}
          aria-label={s.archived ? "取消归档" : "归档此会话"}
          onClick={(e) => {
            e.stopPropagation();
            Chat.ArchiveSession(s.id, !s.archived).then(reloadSessions).catch((e2) => toast.error(`归档失败: ${errText(e2)}`));
          }}
        >
          <Archive className="h-3.5 w-3.5" />
        </button>
        <button
          className="rounded p-0.5 hover:bg-foreground/10"
          title={s.pinned ? "取消置顶" : "置顶此会话"}
          aria-label={s.pinned ? "取消置顶" : "置顶此会话"}
          onClick={(e) => {
            e.stopPropagation();
            Chat.PinSession(s.id, !s.pinned).then(reloadSessions).catch((e2) => toast.error(`置顶失败: ${errText(e2)}`));
          }}
        >
          {s.pinned ? <PinOff className="h-3.5 w-3.5" /> : <Pin className="h-3.5 w-3.5" />}
        </button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              className="rounded p-0.5 hover:bg-foreground/10"
              title="会话操作"
              aria-label="会话操作"
              onClick={(e) => e.stopPropagation()}
            >
              <MoreHorizontal className="h-3.5 w-3.5" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
            <DropdownMenuItem onClick={() => forkSession(s)}>
              <GitFork className="mr-2 h-3.5 w-3.5" /> 分叉此会话
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => { setRenaming(s); setRenameText(s.title); }}>
              <Pencil className="mr-2 h-3.5 w-3.5" /> 重命名
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem className="text-destructive focus:text-destructive" onClick={() => removeSession(s)}>
              <Trash2 className="mr-2 h-3.5 w-3.5" /> 删除会话…
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </span>
    </div>
  );

  return (
    <div className="flex h-full">
      {/* Sessions */}
      {!sidebarCollapsed && (
      <>
      <div className="flex shrink-0 flex-col border-r border-border bg-surface-1" style={{ width: sidebarWidth }}>
        <div className="flex items-center justify-end px-3 pb-1 pt-3">
          <button
            title="折叠对话列表（⌘B）"
            aria-label="折叠对话列表"
            aria-expanded={!sidebarCollapsed}
            onClick={toggleSidebar}
            className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <PanelLeftClose className="h-4 w-4" />
          </button>
        </div>
        <div className="grid gap-2 px-3 pb-3 pt-2">
          <Button
            className="w-full justify-start gap-1.5 border-border bg-transparent text-ui"
            size="sm"
            variant="outline"
            onClick={createDirectSession}
          >
            <SquarePen className="h-3.5 w-3.5" /> 新聊天
          </Button>
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="h-8 bg-background/40 pl-8 text-ui"
              placeholder="搜索对话"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
        </div>
        {/* Plain scroll container: Radix ScrollArea wraps its children in a
            `display: table` div, so a long session title/preview sets the table
            width to its full nowrap length and every row's right-aligned
            actions get clipped outside the sidebar. A block container lets
            `truncate` do its job and keeps the +/edit/… buttons visible no
            matter which project is expanded. */}
        <div className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto px-2">
          <div className="grid grid-cols-1 gap-1 pb-3">
            {searching ? (
              <>
                <div className="px-2 pb-0.5 pt-2 text-xs font-medium text-muted-foreground">搜索结果（{searchResults.length}）</div>
                {searchResults.map(renderSessionRow)}
                {searchResults.length === 0 && (
                  <div className="px-2 py-6 text-center text-xs text-muted-foreground">没有匹配的对话</div>
                )}
              </>
            ) : (
              <>
                <div className="flex items-center justify-between px-2 pb-0.5 pt-1 text-xs font-medium text-muted-foreground">
                  <button
                    className="flex items-center gap-1 rounded px-1 py-0.5 hover:text-foreground"
                    onClick={() => setProjectsOpen((o) => !o)}
                  >
                    {projectsOpen ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />} 项目
                  </button>
                  <button
                    title="添加项目（选择一个目录）"
                    onClick={addProject}
                    className="rounded p-0.5 hover:text-foreground"
                  >
                    <FolderPlus className="h-3 w-3" />
                  </button>
                </div>
                {projectsOpen && (
                  <div className="grid gap-0.5">
                    {folderGroups.map((g) => {
                      const expanded = expandedFolderKey === g.key;
                      const project = g.projectId ? projects.find((p) => p.id === g.projectId) : undefined;
                      return (
                        <div key={g.key}>
                          <div
                            role="button"
                            tabIndex={0}
                            title={g.path ?? g.label}
                            className={cn(
                              "group flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-1.5 text-ui",
                              expanded ? "bg-accent/60 text-accent-foreground" : "text-muted-foreground hover:bg-accent/50 hover:text-foreground",
                            )}
                            onClick={() => setExpandedFolderKey(expanded ? null : g.key)}
                            onKeyDown={(e) => {
                              if (e.key === "Enter" || e.key === " ") {
                                e.preventDefault();
                                setExpandedFolderKey(expanded ? null : g.key);
                              }
                            }}
                          >
                            {expanded
                              ? <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                              : <FolderOpen className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
                            <span className="min-w-0 flex-1 truncate">{g.label}</span>
                            <span className="flex shrink-0 items-center gap-0.5">
                              {project && (
                                <>
                                  {/* The new-chat affordance must stay visible while
                                      the folder is collapsed or expanded, not
                                      only on hover. */}
                                  <button
                                    className="rounded p-0.5 hover:bg-foreground/10"
                                    title="在此项目中新建对话"
                                    aria-label={`在项目「${g.label}」中新建对话`}
                                    onClick={(e) => { e.stopPropagation(); createProjectSession(project.id); }}
                                  >
                                    <Plus className="h-3.5 w-3.5" />
                                  </button>
                                  <button
                                    className="rounded p-0.5 hover:bg-foreground/10"
                                    title="重命名项目"
                                    onClick={(e) => { e.stopPropagation(); setRenamingProject(project); setRenameProjectText(g.label); }}
                                  >
                                    <Pencil className="h-3.5 w-3.5" />
                                  </button>
                                </>
                              )}
                              <DropdownMenu>
                                <DropdownMenuTrigger asChild>
                                  <button title={project ? "管理项目" : "目录操作"} onClick={(e) => e.stopPropagation()} className="rounded p-0.5 hover:bg-foreground/10">
                                    <MoreHorizontal className="h-3.5 w-3.5" />
                                  </button>
                                </DropdownMenuTrigger>
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
                                      <DropdownMenuItem className="text-destructive" onClick={() => deleteProject(project)}>
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
                          </div>
                          {expanded && (
                            <div className="ml-3 grid grid-cols-1 gap-0.5 border-l pl-1">
                              {g.sessions.map(renderSessionRow)}
                            </div>
                          )}
                        </div>
                      );
                    })}
                    {folderGroups.length === 0 && (
                      <div className="px-2 py-1 text-xs leading-relaxed text-muted-foreground">
                        暂无项目：对话绑定到工作区目录后自动归类到这里，也可点右上角 + 添加
                      </div>
                    )}
                  </div>
                )}
                <button
                  className="flex items-center gap-1 rounded px-2 pb-0.5 pt-2 text-xs font-medium text-muted-foreground hover:text-foreground"
                  onClick={() => setRecentOpen((o) => !o)}
                >
                  {recentOpen ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />} 最近
                </button>
                {recentOpen && timeGroups.map((group) => (
                  <div key={group.key}>
                    <div className="px-2 pb-0.5 pt-1.5 text-xs text-muted-foreground/80">{group.label}</div>
                    {group.sessions.map(renderSessionRow)}
                  </div>
                ))}
                {recentOpen && sessions.length > 0 && timeGroups.length === 0 && archivedSessions.length === 0 && (
                  <div className="px-2 py-4 text-center text-xs text-muted-foreground">还没有对话，点击上方「新聊天」开始</div>
                )}
                {archivedSessions.length > 0 && (
                  showArchived ? (
                    <>
                      <button
                        className="mt-1 flex w-full items-center gap-1 rounded-md px-2 py-1 text-left text-xs text-muted-foreground hover:bg-accent/50 hover:text-foreground"
                        onClick={() => setShowArchived(false)}
                      >
                        <ChevronDown className="h-3 w-3" /> 已归档会话（{archivedSessions.length}）
                      </button>
                      <button
                        className="ml-auto shrink-0 rounded px-1.5 py-0.5 text-2xs text-muted-foreground hover:bg-accent/60 hover:text-destructive"
                        title="删除全部已归档会话（任务文件保留）"
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
                      {archivedSessions.map(renderSessionRow)}
                    </>
                  ) : (
                    <button
                      className="mt-1 flex w-full items-center gap-1 rounded-md px-2 py-1 text-left text-xs text-muted-foreground hover:bg-accent/50 hover:text-foreground"
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
        <div className="flex items-center justify-between border-t border-border px-3 py-2 text-xs text-muted-foreground">
          <span className="truncate" title="基于 Google ADK Go v2 · Wails v3">{version ? `AgentMate v${version}` : "AgentMate"}</span>
          <span className="flex shrink-0 items-center gap-1">
            <button title="设置（⌘,）" onClick={() => onOpenSettings()} className="rounded p-1 hover:bg-accent hover:text-foreground">
              <SettingsIcon className="h-3.5 w-3.5" />
            </button>
            <button title="快捷键与命令" onClick={onOpenHelp} className="rounded p-1 hover:bg-accent hover:text-foreground">
              <CircleHelp className="h-3.5 w-3.5" />
            </button>
            <button title="切换深色/浅色主题" onClick={onToggleTheme} className="rounded p-1 hover:bg-accent hover:text-foreground">
              {isDark ? <Sun className="h-3.5 w-3.5" /> : <Moon className="h-3.5 w-3.5" />}
            </button>
          </span>
        </div>
      </div>
      {/* Resize handle: a 6px strip on the sidebar border. Pointer capture
          keeps the drag alive outside the strip; the keyboard path keeps the
          splitter reachable without a mouse. */}
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label="拖动调整对话列表宽度"
        aria-valuemin={SIDEBAR_MIN_WIDTH}
        aria-valuemax={sidebarMaxWidth(window.innerWidth)}
        aria-valuenow={sidebarWidth}
        tabIndex={0}
        title="拖动调整对话列表宽度"
        onPointerDown={startSidebarResize}
        onPointerMove={moveSidebarResize}
        onPointerUp={endSidebarResize}
        onPointerCancel={endSidebarResize}
        onLostPointerCapture={endSidebarResize}
        onKeyDown={resizeSidebarWithKeyboard}
        className={cn(
          "w-1.5 shrink-0 cursor-col-resize touch-none select-none bg-transparent transition-colors",
          "hover:bg-brand/40 focus-visible:bg-brand/50 focus-visible:outline-none",
          resizingSidebar && "bg-brand/50",
        )}
      />
      </>
      )}

      {/* Chat area */}
      <div className="relative flex min-w-0 flex-1 flex-col">
        <div className="flex h-12 items-center justify-between border-b border-border px-4">
          <div className="flex min-w-0 items-center gap-2">
            {sidebarCollapsed && (
              <button
                title="展开对话列表（⌘B）"
                aria-label="展开对话列表"
                aria-expanded={false}
                onClick={toggleSidebar}
                className="inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              >
                <PanelLeftOpen className="h-4 w-4" />
              </button>
            )}
            <MessageSquare className="h-4 w-4 shrink-0 text-muted-foreground" />
            <span className="max-w-[360px] truncate text-sm font-semibold">{active ? active.title : "AgentMate"}</span>
            {active && (
              (() => {
                const label = sessionTargetBadgeLabel(active.targetType);
                // Direct chats carry no target, so they get no badge: a "直聊"
                // chip read like a selectable mode next to Agent/team targets.
                if (!label) return null;
                // Configurable targets deep-link into the settings hub.
                const tab: SettingsTab | undefined = active.targetType === "agent" ? "agents" : active.targetType === "team" ? "teams" : undefined;
                return (
                  <span
                    className={cn(
                      "shrink-0 rounded-md bg-elevated px-1.5 py-0.5 text-2xs text-muted-foreground ring-1 ring-border",
                      tab && "cursor-pointer transition-colors hover:text-foreground hover:ring-border-strong",
                    )}
                    title={tab ? "管理此配置" : undefined}
                    onClick={tab ? () => onOpenSettings(tab) : undefined}
                  >
                    {label}: {targetLabel}
                  </span>
                );
              })()
            )}
            {active && (
              <button
                className={cn(
                  "inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground",
                  filterOpen && "bg-accent text-brand",
                )}
                title="在会话内搜索消息（⌘F）"
                aria-label="在会话内搜索消息"
                onClick={toggleFilter}
              >
                <Search className="h-3.5 w-3.5" />
              </button>
            )}
            {active && (
              <button
                className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                title="重命名会话"
                aria-label="重命名会话"
                onClick={() => { setRenaming(active); setRenameText(active.title); }}
              >
                <Pencil className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {realContextUsage && realContextUsage.pct >= 80 && (
              <span className="shrink-0 rounded-md bg-destructive/10 px-1.5 py-0.5 text-2xs text-destructive">
                上下文即将耗尽（{realContextUsage.pct.toFixed(0)}%）
              </span>
            )}
            {active && (
              <>
                {/* Export/memory/stats live in one overflow menu: six icon
                    buttons crowded the header on narrow windows. */}
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button variant="ghost" size="icon-sm" title="更多操作" aria-label="更多操作">
                      <MoreHorizontal className="h-4 w-4" />
                    </Button>
                  </DropdownMenuTrigger>
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
                <Button
                  variant={workspacePanelOpen ? "secondary" : "ghost"}
                  size="icon-sm"
                  title={workspacePanelOpen ? "收起任务结果区" : "打开任务结果区"}
                  aria-label={workspacePanelOpen ? "收起任务结果区" : "打开任务结果区"}
                  onClick={() => setWorkspacePanelOpen((v) => !v)}
                >
                  <PanelRightOpen className="h-4 w-4" />
                </Button>
              </>
            )}
          </div>
        </div>

        {filterOpen && (
          <div className="flex items-center gap-2 border-b px-4 py-1.5">
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
            <button
              className="shrink-0 rounded p-1 text-muted-foreground hover:text-foreground"
              title="清除并关闭"
              onClick={toggleFilter}
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
        )}
        {/* The floater anchors to the message viewport: a growing composer
            shrinks this wrapper instead of sliding under the button. */}
        <div className="relative flex min-h-0 flex-1 flex-col">
          {!atBottom && messages.length > 0 && (
            // Circular floater inside the message area: an arrow when idle,
            // breathing amber dots while the turn runs, arrow again on hover.
            <button
              type="button"
              title={running ? "正在生成，回到底部" : "回到底部"}
              aria-label="回到底部"
              onClick={() => bottomRef.current?.scrollIntoView({ block: "end" })}
              className={cn(
                "group absolute bottom-4 left-1/2 z-20 flex h-10 w-10 -translate-x-1/2 items-center justify-center rounded-full border border-border bg-popover text-muted-foreground shadow-pop transition-all duration-200 hover:scale-105 hover:bg-accent hover:text-foreground active:scale-95",
                running && "border-warning/30",
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
          )}
          <TurnRail turns={railTurns} activeId={activeTurnId} onSelect={selectTurn} />
          <ScrollArea className="flex-1" viewportRef={scrollRef} aria-label="会话消息">
            <div className="mx-auto max-w-[var(--chat-col-width)] space-y-4 px-5 py-6">
              {(() => {
                if (omittedItems <= 0) return null;
                return (
                  <div className="rounded-lg border border-border bg-muted/20 px-3 py-2 text-center text-xs text-muted-foreground">
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
                <div className="flex items-center justify-between rounded-lg border border-warning/50 bg-warning/10 px-4 py-2.5 text-sm">
                  <span>上下文窗口已使用 {realContextUsage.pct.toFixed(0)}%，模型可能开始遗忘早期内容。建议导出会话后开启新对话。</span>
                  <div className="flex shrink-0 gap-2">
                    <Button size="sm" variant="outline" onClick={exportSession}>导出 Markdown</Button>
                    <Button size="sm" variant="ghost" onClick={() => setCtxBannerDismissed(true)}>知道了</Button>
                  </div>
                </div>
              )}
              {messages.length === 0 && !active && providerCount === 0 && <Onboarding onOpenSettings={onOpenSettings} />}
              {messages.length === 0 && !active && providerCount > 0 && (
                // Providers exist but no chat is selected: previously this
                // rendered an empty pane with no hint about what to do.
                <div className="flex min-h-[55vh] flex-col items-center justify-center gap-3 text-center">
                  <span className="flex h-12 w-12 items-center justify-center rounded-full bg-brand/10 text-brand">
                    <MessageSquare className="h-6 w-6" />
                  </span>
                  <div className="text-md font-medium">直接开始对话</div>
                  <p className="max-w-sm text-ui leading-relaxed text-muted-foreground">
                    输入消息即可开始；输入 <span className="font-medium text-foreground">@</span> 可指定助手、团队或远程 Agent。
                  </p>
                </div>
              )}
              {messages.length === 0 && active && (
                <div className="flex min-h-[55vh] flex-col items-center justify-center gap-3 text-center">
                  <span className="flex h-12 w-12 items-center justify-center rounded-full bg-brand/10 text-brand">
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
                // The first visible item compares against the real previous
                // entry when the window is truncated (else no separator).
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
                      <TurnCard
                        turn={item}
                        active={item.header.id === activeTurnId}
                        live={running && renderIdx === lastTurnIdx}
                        isLastTurn={renderIdx === renderItems.length - 1}
                        onSelectTurn={selectTurn}
                        onEdit={startEdit}
                        canEdit={editFlags.get(item.header.id) === true && !running && !!activeID}
                        filterQ={filterQ}
                        renderBodyItem={renderBodyItem}
                      />
                    ) : (
                      // Orphans precede the first user message: no live turn,
                      // and the shared wiring keeps one prop-passing site.
                      renderBodyItem(item, {
                        isLastAssistant: item.kind === "message"
                          && item.message.kind === "assistant"
                          && renderIdx === renderItems.length - 1,
                        liveGroup: false,
                      })
                    )}
                  </Fragment>
                );
              })}
              {historyLoading && messages.length === 0 && (
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

        {/* Screen-reader status for turn state: announcing every streamed
            token would be noise, so only the phase transitions speak. */}
        <div className="sr-only" role="status" aria-live="polite">
          {running ? "助手正在回复…" : messages[messages.length - 1]?.kind === "error" ? "本轮回复出错，可点击重试本轮" : ""}
        </div>
        <div className="shrink-0 border-t py-3">
          {activePlan && (
            <div className="mx-auto mb-2 max-w-[var(--chat-col-width)] px-3">
              <PlanStrip todos={activePlan} />
            </div>
          )}
          {(queued[activeID] ?? []).map((q, qi) => (
            <div key={qi} className="mx-auto mb-2 flex max-w-[var(--chat-col-width)] items-center gap-2 rounded-lg border border-brand/30 bg-brand/5 px-3 py-1.5 text-xs">
              <span className="shrink-0 text-muted-foreground">{qi === 0 ? "已排队" : `排队 #${qi + 1}`}</span>
              <span className="min-w-0 flex-1 truncate">{q.text}</span>
              {q.reasoning && (
                <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-3xs text-muted-foreground">
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
          <div className="mx-auto max-w-[var(--chat-col-width)] px-3">
            {editing && (
              <div className="mb-2 flex items-center justify-between rounded-md border border-brand/40 bg-brand/5 px-3 py-1.5 text-xs text-muted-foreground">
                <span>正在编辑最后一条消息，发送后将替换该轮对话</span>
                <button className="underline underline-offset-2 hover:text-foreground" onClick={cancelEdit}>
                  取消编辑
                </button>
              </div>
            )}
          </div>
          <div className="mx-auto max-w-[var(--chat-col-width)] px-3">
            {pendingAtts.length > 0 && (
              <div className="mb-2 flex flex-wrap gap-2">
                {pendingAtts.map((a, i) => (
                  <span key={i} className="group/att relative flex items-center gap-1.5 rounded-md border bg-muted/40 py-1 pl-1 pr-5 text-xs">
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
          <div className="mx-auto max-w-[var(--chat-col-width)] px-3">
            <div
              className={cn(
                "rounded-2xl border border-border bg-card shadow-card transition-colors focus-within:border-brand/40 focus-within:ring-4 focus-within:ring-brand/10",
                dragOver && "border-brand bg-brand/5 ring-4 ring-brand/15",
              )}
              onDragOver={(e) => {
                e.preventDefault();
                setDragOver(true);
              }}
              onDragLeave={(e) => {
                // Child elements fire dragleave on the way through: only clear
                // when the pointer actually left the drop zone.
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
                <div className="absolute bottom-full left-0 right-0 z-10 mb-2 max-h-80 overflow-y-auto rounded-lg border border-border bg-popover p-1 shadow-pop" role="listbox" aria-label="@ 引用候选">
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
                          i === mentionIdx ? "bg-accent text-accent-foreground" : "hover:bg-accent/60",
                        )}
                        onMouseEnter={() => setMentionIdx(i)}
                        onMouseDown={(e) => { e.preventDefault(); applyMention(c); }}
                      >
                        {isAssistantCandidate(c)
                          ? c.kind === "team" ? <Users className="h-3.5 w-3.5 shrink-0 text-brand" />
                            : c.kind === "remote" ? <Network className="h-3.5 w-3.5 shrink-0 text-brand" />
                              : <Bot className="h-3.5 w-3.5 shrink-0 text-brand" />
                          : c.kind === "directory" ? <Folder className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                            : <FileText className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
                        <span className="min-w-0 flex-1 truncate">{label}</span>
                        <span className="shrink-0 text-3xs text-muted-foreground">{tag}</span>
                      </button>
                    );
                  })}
                  {mentionToken?.leading && (
                    <button
                      type="button"
                      className="flex w-full items-center gap-2 border-t px-3 py-1.5 text-left text-xs text-muted-foreground hover:bg-accent/60 hover:text-foreground"
                      onMouseDown={(e) => { e.preventDefault(); onOpenSettings("agents"); }}
                    >
                      <Bot className="h-3.5 w-3.5 shrink-0" /> 管理助手与团队…
                    </button>
                  )}
                </div>
              )}
              {slashOpen && (
                <div className="absolute bottom-full left-0 right-0 z-10 mb-2 max-h-80 overflow-y-auto rounded-lg border border-border bg-popover p-1 shadow-pop" role="listbox" aria-label="斜杠命令建议">
                  {slashSuggestions.map((c, i) => (
                    <button
                      key={c.cmd}
                      role="option"
                      aria-selected={i === slashIdx}
                      className={cn(
                        "flex w-full flex-col gap-0.5 rounded-md px-2.5 py-1.5 text-left text-xs transition-colors",
                        i === slashIdx ? "bg-accent text-accent-foreground" : "hover:bg-accent/60",
                      )}
                      onMouseEnter={() => setSlashIdx(i)}
                      onMouseDown={(e) => { e.preventDefault(); applySlash(c.cmd); }}
                    >
                      <span className="flex w-full items-center gap-2">
                        <span className="font-mono font-medium">{c.cmd}</span>
                        {c.source && c.source !== "builtin" && (
                          <span className="shrink-0 rounded border px-1 text-3xs font-normal text-muted-foreground">
                            {slashSourceLabel(c.source)}
                          </span>
                        )}
                      </span>
                      <span className="w-full truncate text-2xs font-normal text-muted-foreground">{c.desc}</span>
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
                  // Slash suggestions sit below the mention popup in priority
                  // (they never open together: "/" vs "@" prefixes).
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
                  // Esc walks back down the UI stack instead of doing nothing:
                  // cancel edit → stop generation → close the filter bar.
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
                  // ↑ on an empty composer edits the last user message
                  // (Codex-style) — the fastest way to fix a typo in a prompt.
                  if (e.key === "ArrowUp" && input.length === 0 && !editing && !running && !e.nativeEvent.isComposing && !e.shiftKey && !e.altKey && !e.metaKey && !e.ctrlKey) {
                    for (let i = messages.length - 1; i >= 0; i--) {
                      const m = messages[i];
                      if (m.kind === "user" && m.text) {
                        e.preventDefault();
                        startEdit(m.text);
                        return;
                      }
                      // A confirm card between the user message and now means
                      // editing would misrepresent the pending interaction.
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
                // Typing stays available while a reply streams (matching the
                // desktops this app mirrors); send() guards the running state.
                disabled={!active && providerCount === 0}
                // Cap the growing textarea so a long paste shrinks the message
                // list instead of squeezing the composer out of the viewport.
                className="max-h-48 min-h-[64px] resize-none overflow-y-auto border-0 bg-transparent px-3.5 pb-1 pt-3 text-md leading-[1.65] shadow-none focus-visible:border-0 focus-visible:ring-0 dark:bg-transparent"
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
              <div className="flex items-center justify-between gap-2 px-1.5 pb-1.5">
                <div className="flex items-center gap-0.5">
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    title={!active || modelMultimodal
                      ? "添加图片附件（png/jpeg/webp/gif，最大 6MB）"
                      : "添加图片附件；当前模型未标记多模态，图片可能无法被识别"}
                    aria-label="添加图片附件"
                    disabled={(!active && providerCount === 0) || running}
                    onClick={() => fileRef.current?.click()}
                  >
                    <Plus className="h-4 w-4" />
                  </Button>
                  <button
                    type="button"
                    onClick={toggleAllowAll}
                    disabled={(!active && providerCount === 0) || allowAllBusy}
                    aria-pressed={composerAllowAll}
                    aria-label={allowAllToggleLabel(composerAllowAll)}
                    title={allowAllToggleHint(composerAllowAll)}
                    className={cn(
                      "flex h-7 max-w-[168px] items-center gap-1 rounded-md px-2 text-xs transition-colors",
                      composerAllowAll
                        ? "bg-warning/15 text-warning hover:bg-warning/25 dark:text-warning"
                        : "text-muted-foreground hover:bg-accent hover:text-foreground",
                      ((!active && providerCount === 0) || allowAllBusy) && "cursor-not-allowed opacity-50",
                    )}
                  >
                    <LockOpen className="h-3.5 w-3.5 shrink-0" />
                    <span className="truncate">{allowAllToggleLabel(composerAllowAll)}</span>
                    {composerAllowAll && <X className="h-3 w-3 shrink-0 opacity-70" />}
                  </button>
                  {active && (
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <button
                          type="button"
                          className="flex h-7 min-w-0 max-w-[112px] items-center gap-1 rounded-md px-2 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                          title={`执行过程显示：${executionDisplayLabel(executionDisplayMode)}`}
                          aria-label="选择执行过程显示方式"
                        >
                          <ListTree className="h-3.5 w-3.5 shrink-0" />
                          <span className="hidden truncate sm:inline">{executionDisplayLabel(executionDisplayMode)}</span>
                          <ChevronDown className="h-3 w-3 shrink-0 opacity-60" />
                        </button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="start" className="w-64">
                        <DropdownMenuLabel>执行过程显示</DropdownMenuLabel>
                        <DropdownMenuRadioGroup
                          value={executionDisplayMode}
                          onValueChange={setSessionExecutionDisplay}
                        >
                          {EXECUTION_DISPLAY_OPTIONS.map((option) => (
                            <DropdownMenuRadioItem key={option.value} value={option.value} className="items-start py-1.5">
                              <span className="flex flex-col gap-0.5">
                                <span className="text-xs">{option.label}</span>
                                <span className="text-3xs text-muted-foreground">{option.description}</span>
                              </span>
                            </DropdownMenuRadioItem>
                          ))}
                        </DropdownMenuRadioGroup>
                        {active.executionDisplayMode && (
                          <>
                            <DropdownMenuSeparator />
                            <DropdownMenuItem
                              className="text-xs"
                              onSelect={() => setSessionExecutionDisplay("")}
                            >
                              跟随全局设置
                            </DropdownMenuItem>
                          </>
                        )}
                      </DropdownMenuContent>
                    </DropdownMenu>
                  )}
                </div>
                <div className="flex min-w-0 items-center gap-1.5">
                  {(contextUsage || (sessionUsage && sessionUsage.totalTokens > 0)) && (
                    <TooltipProvider>
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <div className="hidden shrink-0 items-center px-1 sm:flex">
                            {contextUsage
                              ? <ContextRing usage={contextUsage} />
                              : <Coins className="h-3.5 w-3.5 text-muted-foreground" />}
                          </div>
                        </TooltipTrigger>
                        <TooltipContent side="top" className="flex-col items-start gap-0.5">
                          {contextTooltipLines(contextUsage, sessionUsage, costOf(sessionUsage)).map((line) => (
                            <span key={line}>{line}</span>
                          ))}
                        </TooltipContent>
                      </Tooltip>
                    </TooltipProvider>
                  )}
                  {availableLevels.length > 0 && (
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <button
                          className="flex h-7 min-w-0 max-w-[136px] items-center gap-1 rounded-md px-2 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                          title={`本条消息思考强度：${levelLabel(nextReasoningLevel)}（仅下一条消息，不影响正在进行的回复）`}
                          aria-label="选择本条消息的思考强度"
                        >
                          <Brain className="h-3.5 w-3.5 shrink-0" />
                          <span className="truncate">{nextReasoningLabel}</span>
                          <ChevronDown className="h-3 w-3 shrink-0 opacity-60" />
                        </button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end" className="w-60">
                        <DropdownMenuLabel>本条消息思考强度</DropdownMenuLabel>
                        <DropdownMenuRadioGroup
                          value={nextReasoningLevel}
                          onValueChange={(v) => setTurnReasoning(v)}
                        >
                          {availableLevels.map((l) => (
                            <DropdownMenuRadioItem key={l.level} value={l.level} className="text-xs">
                              <span className="flex-1">{levelLabel(l.level)}</span>
                              <span className="font-mono text-3xs text-muted-foreground">{l.level}</span>
                            </DropdownMenuRadioItem>
                          ))}
                        </DropdownMenuRadioGroup>
                        <DropdownMenuSeparator />
                        <DropdownMenuLabel className="text-2xs font-normal text-muted-foreground">
                          会话默认（影响后续所有轮次）
                        </DropdownMenuLabel>
                        {active && (
                          <DropdownMenuItem
                            className="text-xs"
                            disabled={!nextReasoningLevel || nextReasoningLevel === sessionReasoning}
                            onSelect={() => setSessionReasoning(nextReasoningLevel)}
                          >
                            将「{levelLabel(nextReasoningLevel || defaultReasoningLevel)}」设为会话默认
                          </DropdownMenuItem>
                        )}
                        {sessionReasoning && (
                          <DropdownMenuItem className="text-xs" onSelect={() => setSessionReasoning("")}>
                            清除会话默认，回到模型默认
                          </DropdownMenuItem>
                        )}
                      </DropdownMenuContent>
                    </DropdownMenu>
                  )}
                  {composerModels.length > 0 && (
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <button
                          className="flex h-7 min-w-0 max-w-[200px] items-center gap-1 rounded-md px-2 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                          title={`${active ? "本会话使用的模型" : "新会话使用的模型"}：${composerModelLabel}（首条消息发出后沿用，仅影响当前对话）`}
                          aria-label="选择模型"
                        >
                          <Boxes className="h-3.5 w-3.5 shrink-0" />
                          <span className="truncate">{composerModelLabel}</span>
                          <ChevronDown className="h-3 w-3 shrink-0 opacity-60" />
                        </button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end" className="w-64">
                        <DropdownMenuLabel className="truncate">
                          本会话模型{composerProviderName ? ` · ${composerProviderName}` : ""}
                        </DropdownMenuLabel>
                        <DropdownMenuRadioGroup
                          value={composerModelValue}
                          onValueChange={chooseComposerModel}
                        >
                          {composerModels.map((m) => (
                            <DropdownMenuRadioItem key={m.id} value={m.id} className="text-xs">
                              <span className="truncate" title={`${modelLabel(m)} · ${m.id}`}>{modelLabel(m)}</span>
                            </DropdownMenuRadioItem>
                          ))}
                        </DropdownMenuRadioGroup>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem onClick={() => onOpenSettings("models")}>
                          <Boxes className="h-3.5 w-3.5 shrink-0" /> 管理模型供应商…
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  )}
                  {running ? (
                    <Button
                      variant="destructive"
                      size="icon"
                      className="h-7 w-7 rounded-full"
                      title="停止生成（Esc）"
                      aria-label="停止生成"
                      onClick={cancel}
                    >
                      <Square className="h-3.5 w-3.5 fill-current" />
                    </Button>
                  ) : (
                    <Button
                      size="icon"
                      className="h-7 w-7 rounded-full"
                      title={editing ? "重新发送（Enter）" : "发送（Enter）"}
                      aria-label={editing ? "重新发送" : "发送"}
                      onClick={() => send()}
                      disabled={(!active && providerCount === 0) || !input.trim()}
                    >
                      <ArrowUp className="h-3.5 w-3.5" />
                    </Button>
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

      {/* Session stats dialog */}
      <ConfirmDialog request={confirmRequest} onClose={() => {
        setConfirmRequest(null);
        // Cancelling the risk notice must also drop the pending grant, or a
        // later unrelated dialog could silently enable full access.
        allowAllIntentRef.current = null;
      }} />
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
                <div key={i} className="rounded-lg border p-2.5">
                  <div className="text-xs text-muted-foreground">{v}</div>
                  <div className="text-sm font-medium">{k}</div>
                </div>
              ))}
            </div>
            {(stats.authors?.length ?? 0) > 1 && (
              <div className="rounded-lg border p-2.5">
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
              <div className="rounded-lg border p-2.5">
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

      {/* Rename dialog */}
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

      {/* Project rename dialog */}
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

/** Onboarding guides first-run users like Codex/Claude desktops do. */
