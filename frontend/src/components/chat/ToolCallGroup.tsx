import { useEffect, useState, type ReactNode } from "react";
import {
  CheckCircle2, ChevronRight, Loader2, ShieldAlert, XCircle,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { ToolCallCard } from "@/components/ToolCallCard";
import { ConfirmCard, ThinkingBlock } from "@/components/chat/MessageBubble";
import { MessageMarkdown } from "@/components/MessageMarkdown";
import {
  formatWorkDuration, toolGroupElapsedMs,
  toolGroupInvocationCount, toolGroupStatus,
  type ToolGroup, type ToolGroupStatus,
} from "@/lib/chatUtils";
import { cn } from "@/lib/utils";

/** STATUS_META maps an execution block's state to its header badge. */
const STATUS_META: Record<ToolGroupStatus, { label: string; icon: ReactNode; cls: string }> = {
  confirm: { label: "等待确认", icon: <ShieldAlert className="h-3.5 w-3.5 text-amber-600" />, cls: "border-amber-500/50 text-amber-700 dark:text-amber-400" },
  running: { label: "运行中", icon: <Loader2 className="h-3.5 w-3.5 animate-spin text-primary" />, cls: "border-primary/40 text-primary" },
  failed: { label: "失败", icon: <XCircle className="h-3.5 w-3.5 text-destructive" />, cls: "border-destructive/40 text-destructive" },
  done: { label: "完成", icon: <CheckCircle2 className="h-3.5 w-3.5 text-emerald-500" />, cls: "border-emerald-500/40 text-emerald-600 dark:text-emerald-400" },
};

/**
 * ToolCallGroup collapses one turn's whole process — thinking entries, tool
 * calls/results, HITL confirmations and the assistant narration between tool
 * batches — into a single summary row, keeping only the final reply outside
 * as the result block. It opens automatically while the run is executing (or
 * waiting for a HITL confirmation) and closes when the turn settles, unless
 * the user toggled it — their choice then persists across streamed appends.
 * Expanding shows the narration inline, the thinking text, and the original
 * per-call cards, so each invocation keeps its own detail arrow and artifact
 * previews.
 */
export function ToolCallGroup({ group, live, runningTools, durations, sessionID, filterQ, onConfirm, onAllowAll, allowAllBusy }: {
  /** group carries the turn's messages, its final-reply timestamp and outcome
   *  (answered/errored) so the header reports the run, not a single step. */
  group: ToolGroup;
  /** live keeps the summary ticking while the current turn still streams. */
  live?: boolean;
  runningTools: Set<string>;
  durations: Map<string, number>;
  sessionID: string;
  filterQ: string;
  onConfirm: (msgId: string, callId: string | undefined, approved: boolean) => void;
  onAllowAll: (msgId: string, callId: string | undefined) => void;
  allowAllBusy: boolean;
}) {
  // null means "follow the run state": expanded while running/awaiting a
  // confirmation, collapsed once settled. An explicit click wins from then on.
  const [userExpanded, setUserExpanded] = useState<boolean | null>(null);
  const messages = group.messages;
  const status = toolGroupStatus(group, runningTools, !!live);
  const count = toolGroupInvocationCount(messages);
  const autoExpanded = status === "running" || status === "confirm";
  const expanded = userExpanded ?? autoExpanded;
  const meta = STATUS_META[status];

  // The whole-turn clock keeps ticking while the turn is live (including the
  // final answer's own generation) and freezes once it settles.
  const ticking = live || status === "running" || status === "confirm";
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

  return (
    <div className="rounded-lg border bg-muted/20 text-sm" data-toolgroup data-state={status}>
      <button
        type="button"
        className="flex w-full items-center gap-2 px-3 py-2 text-left"
        aria-expanded={expanded}
        onClick={() => setUserExpanded(!expanded)}
      >
        <ChevronRight className={cn("h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform", expanded && "rotate-90")} />
        {meta.icon}
        <span className="font-medium">执行过程</span>
        {elapsedMs != null && (
          <span className="text-[11px] text-muted-foreground">已工作 {formatWorkDuration(elapsedMs)}</span>
        )}
        {count > 0 && (
          <span className="text-[11px] text-muted-foreground/70" title={`本回合调用了 ${count} 个工具`}>
            · {count} 个工具
          </span>
        )}
        <Badge variant="outline" className={cn("ml-auto shrink-0 font-normal", meta.cls)}>
          {meta.label}
        </Badge>
      </button>
      {expanded && (
        <div className="space-y-1.5 border-t p-2">
          {messages.map((m) => m.kind === "confirm" ? (
            <ConfirmCard key={m.id} m={m} onConfirm={onConfirm} onAllowAll={onAllowAll} allowAllBusy={allowAllBusy} />
          ) : m.kind === "thinking" ? (
            <ThinkingBlock key={m.id} m={m} inline />
          ) : m.kind === "assistant" ? (
            // Inter-tool narration: process detail, so it stays muted inside
            // the block instead of rendering another assistant bubble header.
            <div key={m.id} className="px-1 py-0.5 text-muted-foreground" data-toolgroup-narration>
              <MessageMarkdown content={m.text} />
            </div>
          ) : (
            <ToolCallCard
              key={m.id}
              name={m.toolName ?? "tool"}
              sessionID={sessionID}
              args={m.toolArgs}
              resp={m.toolResp}
              running={m.kind === "tool_call" && runningTools.has(m.id)}
              durationMs={durations.get(m.id)}
              filterQ={filterQ}
            />
          ))}
        </div>
      )}
    </div>
  );
}