import { useEffect, useRef, useState, type ReactNode } from "react";
import {
  CheckCircle2, ChevronRight, Loader2, ShieldAlert, XCircle,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { TooltipHint } from "@/components/ui/tooltip";
import { ToolCallCard } from "@/components/ToolCallCard";
import { ConfirmCard, ThinkingBlock } from "@/components/chat/MessageBubble";
import {
  buildToolTrace, formatToolExecutionBreakdown, formatWorkDuration, summarizeToolTrace,
  toolGroupElapsedMs, toolGroupStatus, toolStepDisplay,
  type ToolGroup, type ToolGroupStatus,
} from "@/lib/chatUtils";
import {
  executionAutoExpanded, executionHidden,
  type ExecutionDisplayMode,
} from "@/lib/executionDisplay";
import { cn } from "@/lib/utils";

/** STATUS_META 将执行块的状态映射到其头部徽标。 */
const STATUS_META: Record<ToolGroupStatus, { label: string; icon: ReactNode; cls: string }> = {
  confirm: { label: "等待确认", icon: <ShieldAlert className="h-3.5 w-3.5 text-warning" />, cls: "bg-warning-soft text-warning" },
  running: { label: "运行中", icon: <Loader2 className="h-3.5 w-3.5 animate-spin text-accent-text" />, cls: "bg-accent-soft text-accent-text" },
  failed: { label: "失败", icon: <XCircle className="h-3.5 w-3.5 text-danger-text" />, cls: "bg-danger-soft text-danger-text" },
  done: { label: "完成", icon: <CheckCircle2 className="h-3.5 w-3.5 text-success" />, cls: "bg-success-soft text-success" },
};

/**
 * ToolCallGroup 将一段连续执行过程——思考条目、工具调用/结果以及 HITL
 * 确认——折叠为一行摘要。助手叙述有意不属于该分组：后续工具调用不得吸收已经显示
 * 在会话时间线上的文本。显示模式决定其默认展开状态：auto 跟随运行状态，concise
 * 保持折叠（HITL 除外），detailed 保持展开，none
 * 则隐藏该块。对于该块，用户点击始终优先。展开后会显示思考行以及每次调用一行紧
 * 凑记录；点击某行会保留其输入/输出详情和产物预览。
 */
export function ToolCallGroup({ group, live, runningTools, durations, sessionID, filterQ, displayMode, onConfirm }: {
  /**
   * group
   * 携带该回合的消息、其最终回复的时间戳和结果（answered/errored），以便头部报
   * 告整个运行，而不是单个步骤。
   */
  group: ToolGroup;
  /** 在当前回合仍流式输出期间，live 使摘要持续计时。 */
  live?: boolean;
  runningTools: Set<string>;
  durations: Map<string, number>;
  sessionID: string;
  filterQ: string;
  displayMode: ExecutionDisplayMode;
  onConfirm: (msgId: string, callId: string | undefined, approved: boolean) => void;
}) {
  // null 表示 "follow the run
  // state"：在运行中/等待确认时展开，稳定后折叠。此后显式点击优先。
  const [userExpanded, setUserExpanded] = useState<boolean | null>(null);
  useEffect(() => { setUserExpanded(null); }, [displayMode]);
  const messages = group.messages;
  const status = toolGroupStatus(group, runningTools, !!live);
  const autoExpanded = executionAutoExpanded(displayMode, status);
  const expanded = userExpanded ?? autoExpanded;
  const hidden = executionHidden(displayMode, status);
  const meta = STATUS_META[status];
  const trace = buildToolTrace(messages);
  // 展开后的过程列表是有界滚动区域：长时间运行不得将最终答案（或输入框）挤出窗
  // 口。当回合仍在进行时，仅当读者已经位于底部，才跟随追加的步骤；向上滚动会被
  // 视为 "I am reading this part"。
  const executionScrollRef = useRef<HTMLDivElement>(null);
  const stickToBottomRef = useRef(true);
  const wasExpandedRef = useRef(false);
  useEffect(() => {
    const el = executionScrollRef.current;
    const wasExpanded = wasExpandedRef.current;
    const justExpanded = expanded && !wasExpanded;
    wasExpandedRef.current = expanded;
    if (!el) return;
    if (!expanded) {
      el.scrollTop = 0;
      stickToBottomRef.current = true;
      return;
    }
    if (justExpanded) {
      stickToBottomRef.current = true;
      el.scrollTop = live || status === "running" ? el.scrollHeight : 0;
      return;
    }
    if (!wasExpanded) return;
    if (el && stickToBottomRef.current) el.scrollTop = el.scrollHeight;
  }, [expanded, messages, live, status]);
  const onExecutionScroll = () => {
    const el = executionScrollRef.current;
    if (!el) return;
    stickToBottomRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
  };
  const summary = summarizeToolTrace(trace);
  const breakdown = formatToolExecutionBreakdown(summary.counts);
  const runningSteps = messages
    .filter((m) => m.kind === "tool_call" && runningTools.has(m.id))
    .map((m) => {
      const display = toolStepDisplay(m.toolName ?? "tool", m.toolArgs);
      return display.target ?? display.action;
    })
    .filter((label) => !!label);
  const activeToolName = runningSteps.length === 1
    ? runningSteps[0]
    : runningSteps.length > 1
      ? `${runningSteps.length} 个并行`
      : "";

  // 整回合时钟在回合进行期间持续计时（包括最终答案自身的生成过程），并在稳定后
  // 冻结。
  const ticking = !hidden && (live || status === "running" || status === "confirm");
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!ticking) return;
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [ticking]);
  const computedMs = toolGroupElapsedMs(messages, group.endTs, now, !!live, durations);
  // 实时时间戳为
  // "now"，但已稳定的回合会回退到最终回复的创建时间；保留峰值，以确保摘要绝不缩
  // 短。
  const [peakMs, setPeakMs] = useState(0);
  useEffect(() => {
    if (computedMs != null && computedMs > peakMs) setPeakMs(computedMs);
  }, [computedMs, peakMs]);
  const elapsedMs = computedMs == null ? null : Math.max(computedMs, peakMs);

  // "无" 会隐藏过程块，但待处理的 HITL 确认必须保持可操作；executionHidden
  // 有意豁免 status === "confirm"。
  if (hidden) return null;

  return (
    <div
      className="rounded-xl bg-panel/70 text-sm"
      data-toolgroup
      data-state={status}
      data-display-mode={displayMode}
    >
      <button
        type="button"
        className="flex w-full min-w-0 items-center gap-2 px-3.5 py-2.5 text-left"
        aria-expanded={expanded}
        onClick={() => setUserExpanded(!expanded)}
      >
        <ChevronRight className={cn("h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform", expanded && "rotate-90")} />
        {meta.icon}
        <span className="shrink-0 whitespace-nowrap font-medium">执行过程</span>
        {elapsedMs != null && (
          <span className="shrink-0 whitespace-nowrap text-xs text-muted-foreground">
            已工作 {formatWorkDuration(elapsedMs)}
          </span>
        )}
        {summary.total > 0 && (
          <TooltipHint content={`本回合执行了 ${summary.total} 个步骤${breakdown ? `：${breakdown}` : ""}`}>
            <span className="shrink-0 whitespace-nowrap text-xs text-muted-foreground/70">
              · {summary.total} 个步骤
            </span>
          </TooltipHint>
        )}
        {summary.failed > 0 && status !== "failed" && (
          <span className="flex shrink-0 items-center gap-1 whitespace-nowrap text-xs text-danger-text">
            <XCircle className="h-3.5 w-3.5" />
            {summary.failed} 失败
          </span>
        )}
        {displayMode === "concise" && status === "running" && activeToolName && (
          <span className="min-w-0 truncate text-xs text-muted-foreground/70">
            · {activeToolName}
          </span>
        )}
        <Badge variant="outline" className={cn("ml-auto shrink-0 font-normal", meta.cls)}>
          {meta.label}
        </Badge>
      </button>
      {/* 折叠动画使用 grid-rows 0fr→1fr 技巧：内容保持挂载，因此流式追加不会导致其重新挂载，高度由 CSS 负责。 */}
      <div
        aria-hidden={!expanded}
        className={cn(
          "grid transition-[grid-template-rows] duration-200 ease-standard",
          expanded ? "grid-rows-[1fr]" : "grid-rows-[0fr]",
        )}
      >
        <div className="overflow-hidden">
          <div
            ref={executionScrollRef}
            data-slot="execution-scroll"
            role="group"
            tabIndex={expanded ? 0 : -1}
            aria-label="执行过程步骤"
            onScroll={onExecutionScroll}
            className="max-h-64 space-y-1 overflow-y-auto overscroll-contain bg-panel/40 px-2 py-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/60"
          >
            {trace.map((item) => {
              if (item.kind === "tool") {
                return (
                  <ToolCallCard
                    key={`tool-${item.id}`}
                    name={item.name}
                    sessionID={sessionID}
                    args={item.args}
                    resp={item.resp}
                    running={!!item.call && !item.result && runningTools.has(item.call.id)}
                    durationMs={item.result ? durations.get(item.result.id) : undefined}
                    filterQ={filterQ}
                    compact
                  />
                );
              }
              const m = item.message;
              return m.kind === "confirm" ? (
                <ConfirmCard key={m.id} m={m} onConfirm={onConfirm} />
              ) : m.kind === "thinking" ? (
                <ThinkingBlock key={m.id} m={m} inline />
              ) : null;
            })}
          </div>
        </div>
      </div>
    </div>
  );
}
