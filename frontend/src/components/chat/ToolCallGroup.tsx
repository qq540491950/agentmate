import { useEffect, useRef, useState, type ReactNode } from "react";
import {
  CheckCircle2, ChevronRight, Loader2, ShieldAlert, XCircle,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { TooltipHint } from "@/components/ui/tooltip";
import { ToolCallCard } from "@/components/ToolCallCard";
import { ConfirmCard, ThinkingBlock } from "@/components/chat/MessageBubble";
import { MessageMarkdown } from "@/components/MessageMarkdown";
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

/** STATUS_META maps an execution block's state to its header badge. */
const STATUS_META: Record<ToolGroupStatus, { label: string; icon: ReactNode; cls: string }> = {
  confirm: { label: "等待确认", icon: <ShieldAlert className="h-3.5 w-3.5 text-warning" />, cls: "bg-warning-soft text-warning" },
  running: { label: "运行中", icon: <Loader2 className="h-3.5 w-3.5 animate-spin text-accent-text" />, cls: "bg-accent-soft text-accent-text" },
  failed: { label: "失败", icon: <XCircle className="h-3.5 w-3.5 text-danger-text" />, cls: "bg-danger-soft text-danger-text" },
  done: { label: "完成", icon: <CheckCircle2 className="h-3.5 w-3.5 text-success" />, cls: "bg-success-soft text-success" },
};

/**
 * ToolCallGroup collapses one turn's whole process — thinking entries, tool
 * calls/results, HITL confirmations and the assistant narration between tool
 * batches — into a single summary row, keeping only the final reply outside
 * as the result block. The display mode picks its default expansion: auto
 * follows the run state, concise stays folded (except HITL), detailed stays
 * open, and none hides the block. A user click always wins for that block.
 * Expanding shows the narration inline, the thinking text, and one compact
 * row per invocation; clicking a row keeps its input/output detail and
 * artifact previews without turning the whole run into a stack of cards.
 */
export function ToolCallGroup({ group, live, runningTools, durations, sessionID, filterQ, displayMode, onConfirm }: {
  /** group carries the turn's messages, its final-reply timestamp and outcome
   *  (answered/errored) so the header reports the run, not a single step. */
  group: ToolGroup;
  /** live keeps the summary ticking while the current turn still streams. */
  live?: boolean;
  runningTools: Set<string>;
  durations: Map<string, number>;
  sessionID: string;
  filterQ: string;
  displayMode: ExecutionDisplayMode;
  onConfirm: (msgId: string, callId: string | undefined, approved: boolean) => void;
}) {
  // null means "follow the run state": expanded while running/awaiting a
  // confirmation, collapsed once settled. An explicit click wins from then on.
  const [userExpanded, setUserExpanded] = useState<boolean | null>(null);
  useEffect(() => { setUserExpanded(null); }, [displayMode]);
  const messages = group.messages;
  const status = toolGroupStatus(group, runningTools, !!live);
  const autoExpanded = executionAutoExpanded(displayMode, status);
  const expanded = userExpanded ?? autoExpanded;
  const hidden = executionHidden(displayMode, status);
  const meta = STATUS_META[status];
  const trace = buildToolTrace(messages);
  // The expanded process list is a bounded scroll region: a long run must not
  // push the final answer (or the composer) off the window. While the turn is
  // live, follow appended steps only when the reader is already at the
  // bottom; scrolling up is treated as "I am reading this part".
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

  // The whole-turn clock keeps ticking while the turn is live (including the
  // final answer's own generation) and freezes once it settles.
  const ticking = !hidden && (live || status === "running" || status === "confirm");
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!ticking) return;
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [ticking]);
  const computedMs = toolGroupElapsedMs(messages, group.endTs, now, !!live, durations);
  // Live timestamps are "now", but a settled turn falls back to the final
  // reply's creation time; keep the peak so the summary never shrinks.
  const [peakMs, setPeakMs] = useState(0);
  useEffect(() => {
    if (computedMs != null && computedMs > peakMs) setPeakMs(computedMs);
  }, [computedMs, peakMs]);
  const elapsedMs = computedMs == null ? null : Math.max(computedMs, peakMs);

  // "无" hides the process block, but a pending HITL confirmation must remain
  // actionable; executionHidden intentionally exempts status === "confirm".
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
      {/* Collapse is animated with the grid-rows 0fr→1fr trick: the content stays
          mounted so streaming appends don't remount it, and CSS does the height. */}
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
              ) : m.kind === "assistant" ? (
                // Inter-tool narration: process detail, so it stays muted inside
                // the block instead of rendering another assistant bubble header.
                <div key={m.id} className="px-2 py-1 text-muted-foreground" data-toolgroup-narration>
                  <MessageMarkdown content={m.text} className="text-sm leading-[1.7]" />
                </div>
              ) : null;
            })}
          </div>
        </div>
      </div>
    </div>
  );
}
