import { useState, type ReactNode } from "react";
import {
  CheckCircle2, ChevronRight, Loader2, ShieldAlert, XCircle,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { ToolCallCard, formatDuration } from "@/components/ToolCallCard";
import { ConfirmCard, ThinkingBlock } from "@/components/chat/MessageBubble";
import {
  toolGroupDurationMs, toolGroupInvocationCount, toolGroupStatus,
  type ToolGroupStatus, type UIMessage,
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
 * ToolCallGroup collapses one turn's consecutive process events — thinking
 * entries plus tool calls, results and HITL confirmations — into a single
 * summary row. It opens automatically while the run is executing (or waiting
 * for a HITL confirmation) and closes when the turn settles, unless the user
 * toggled it — their choice then persists across streamed appends. Expanding
 * shows the thinking text inline and the original per-call cards, so each
 * invocation keeps its own detail arrow and artifact previews.
 */
export function ToolCallGroup({ messages, runningTools, durations, sessionID, filterQ, onConfirm, onAllowAll, allowAllBusy }: {
  messages: UIMessage[];
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
  const status = toolGroupStatus(messages, runningTools);
  const count = toolGroupInvocationCount(messages);
  const totalMs = toolGroupDurationMs(messages, durations);
  const autoExpanded = status === "running" || status === "confirm";
  const expanded = userExpanded ?? autoExpanded;
  const meta = STATUS_META[status];
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
        <span className="font-medium">执行了 {count} 个工具</span>
        {totalMs != null && (
          <span className="text-[11px] text-muted-foreground">{formatDuration(totalMs)}</span>
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