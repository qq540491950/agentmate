import { memo, useEffect, useState } from "react";
import {
  Dialog, DialogContent, DialogTitle,
} from "@/components/ui/dialog";
import {
  Brain, Check, ChevronDown, ChevronRight, ChevronUp, Copy, Loader2, Pencil, RefreshCw, ShieldAlert, TriangleAlert, Volume2, X,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { TooltipHint } from "@/components/ui/tooltip";
import { MessageMarkdown, CopyButton } from "@/components/MessageMarkdown";
import { ToolCallCard } from "@/components/ToolCallCard";
import { TodoListCard } from "@/components/chat/TodoCards";
import { dayLabel, fmtTime, type UIMessage } from "@/lib/chatUtils";
import { highlightText } from "@/components/chat/highlightText";
import type { UsageInfo } from "@/lib/api";
import { cn } from "@/lib/utils";

/** ConfirmCard renders a HITL tool-confirmation request with approve/reject. */
export function ConfirmCard({ m, onConfirm }: {
  m: UIMessage;
  onConfirm: (msgId: string, callId: string | undefined, approved: boolean) => void;
  durationMs?: number;
}) {
  const status = m.confirmStatus ?? "pending";
  const hint = (m.hint ?? "").trim();
  // The backend owns the reason text; this only selects the warning
  // presentation for boundary-crossing / preflight failures.
  const riskyHint = /脱离沙箱|工作区外|受保护路径|只读模式|预检失败/.test(hint);
  return (
    <div className={cn(
      "rounded-xl px-4 py-3",
      status === "pending" && "bg-warning-soft",
      status === "approved" && "bg-success-soft",
      status === "rejected" && "bg-danger-soft",
    )}>
      <div className="flex items-center gap-2">
        <ShieldAlert className={cn(
          "h-4 w-4 shrink-0",
          status === "pending" && "text-warning",
          status === "approved" && "text-success",
        status === "rejected" && "text-danger-text",
        )} />
        <span className="text-sm font-medium">
          {status === "pending" ? "工具执行确认" : status === "approved" ? "已批准执行" : "已拒绝"}
        </span>
        <Badge variant="outline" className="font-mono text-2xs">{m.toolName}</Badge>
      </div>
      <div className={cn(
        "mt-1.5 flex items-start gap-1.5 rounded-md px-2 py-1.5 text-xs",
        riskyHint ? "bg-warning-soft text-warning-text" : "text-muted-foreground",
      )}>
        {riskyHint && <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />}
        <span>{hint || "该工具调用需要你的确认。"}</span>
      </div>
      {m.toolArgs && Object.keys(m.toolArgs).length > 0 && (
        <pre className="mt-2 max-h-32 overflow-auto rounded-md bg-control p-2 font-mono text-2xs leading-relaxed text-muted-foreground">
          {JSON.stringify(m.toolArgs, null, 2)}
        </pre>
      )}
      {status === "pending" ? (
        <div className="mt-2.5 flex flex-wrap items-center gap-2">
          <Button size="sm" className="h-7 px-3 text-xs" onClick={() => onConfirm(m.id, m.callId, true)}>
            <Check className="mr-1 h-3 w-3" /> 允许执行
          </Button>
          <Button size="sm" variant="outline" className="h-7 px-3 text-xs" onClick={() => onConfirm(m.id, m.callId, false)}>
            <X className="mr-1 h-3 w-3" /> 拒绝
          </Button>
        </div>
      ) : (
        <div className={cn(
          "mt-2 text-xs",
          status === "approved" ? "text-success" : "text-danger-text",
        )}>
          {status === "approved" ? "模型已继续执行该工具调用" : "模型已收到拒绝结果"}
        </div>
      )}
    </div>
  );
}

/**
 * ThinkingBlock shows a model's reasoning. It streams expanded while the
 * model is still thinking, then collapses to a one-line summary — the answer
 * is what the reader came for, but nothing is discarded. Inside an execution
 * group (inline) it renders as a one-line row with a short preview and opens
 * its full text on demand, so a long run stays scannable without losing the
 * reasoning.
 */
export const ThinkingBlock = memo(function ThinkingBlock({ m, inline = false }: { m: UIMessage; inline?: boolean }) {
  // The standalone block opens while streaming; inside an execution group the
  // row starts folded so a long run stays scannable (Codex-style timeline).
  const [open, setOpen] = useState(!inline && !!m.streaming);
  useEffect(() => {
    if (!m.streaming && !inline) setOpen(false);
  }, [m.streaming, inline]);
  if (inline) {
    const preview = m.text
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean)
      .pop() ?? "";
    return (
      <div data-slot="thinking-row" className="rounded-lg">
        <button
          type="button"
          className="flex w-full min-w-0 items-center gap-2 rounded-lg px-2 py-1.5 text-left text-xs text-muted-foreground transition-colors hover:bg-accent-soft focus-visible:bg-accent-soft focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
          aria-expanded={open}
          aria-label={open ? "收起思考过程" : "展开思考过程"}
          onClick={() => setOpen((v) => !v)}
        >
          <Brain className="h-3.5 w-3.5 shrink-0" />
          <span className="shrink-0 font-medium">思考过程</span>
          <span className="min-w-0 flex-1 truncate text-muted-foreground/70">
            {preview || (m.streaming ? "思考中…" : "")}
          </span>
          {m.streaming && <Loader2 className="h-3 w-3 shrink-0 animate-spin" />}
          <ChevronRight className={cn("h-3.5 w-3.5 shrink-0 transition-transform", open && "rotate-90")} />
        </button>
        {open && (
          <div
            data-slot="thinking-body"
            className="mt-1 max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-md bg-control/70 px-2 py-1.5 text-xs leading-[1.7] text-muted-foreground"
          >
            {m.text || (m.streaming ? "思考中…" : "")}
          </div>
        )}
      </div>
    );
  }
  return (
    <div className="rounded-lg bg-panel/60 px-3 py-2">
      <button
        type="button"
        className="flex w-full items-center gap-2 text-left text-xs text-muted-foreground transition-colors hover:text-foreground"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <Brain className="h-3.5 w-3.5 shrink-0" />
        <span className="font-medium">思考过程</span>
        {m.streaming ? (
          <Loader2 className="h-3 w-3 animate-spin" />
        ) : open ? (
          <ChevronUp className="ml-auto h-3.5 w-3.5" />
        ) : (
          <ChevronDown className="ml-auto h-3.5 w-3.5" />
        )}
      </button>
      <div
        aria-hidden={!open}
        className={cn(
          "grid transition-[grid-template-rows] duration-200 ease-standard",
          open ? "grid-rows-[1fr]" : "grid-rows-[0fr]",
        )}
      >
        <div className="overflow-hidden">
          <div className="mt-1.5 max-h-64 overflow-auto whitespace-pre-wrap break-words text-xs leading-[1.7] text-muted-foreground">
            {m.text || (m.streaming ? "思考中…" : "")}
          </div>
        </div>
      </div>
    </div>
  );
});

/** DaySeparator marks calendar-day boundaries in long conversations. */
export function DaySeparator({ ts }: { ts: string | undefined }) {
  return (
    <div role="separator" aria-label={dayLabel(ts)} className="flex items-center justify-center py-2">
      <span className="shrink-0 px-2 text-xs text-muted-foreground">{dayLabel(ts)}</span>
    </div>
  );
}

/** UserQuote renders a user input as a right-aligned bubble. It is the turn
 *  anchor (data-turn-anchor) that both scroll-follow and the turn rail key
 *  off; copy and edit-and-resend actions stay on hover. */
export function UserQuote({ m, active, onEdit, canEdit, filterQ }: {
  m: UIMessage;
  active?: boolean;
  onEdit?: (text: string) => void;
  canEdit?: boolean;
  filterQ: string;
}) {
  const [lightbox, setLightbox] = useState<string | null>(null);
  return (
    <div
      id={`turn-${m.id}`}
      data-turn-anchor={m.id}
      data-slot="user-bubble"
      className="flex justify-end"
    >
      <div className="group flex max-w-[85%] flex-col items-end gap-1.5 sm:max-w-[640px]">
        {(m.attachments?.length ?? 0) > 0 && (
          <div className="flex flex-wrap justify-end gap-1.5">
            {m.attachments!.map((a, i) => (
              <TooltipHint key={i} content={`查看 ${a.name} 大图`}>
                <button
                  type="button"
                  onClick={() => setLightbox(a.dataUrl)}
                >
                  <img src={a.dataUrl} alt={a.name} className="h-20 cursor-zoom-in rounded-lg object-cover" />
                </button>
              </TooltipHint>
            ))}
          </div>
        )}
        {m.text && (
          <div className="flex flex-col items-end gap-1">
            <div
              data-slot="user-bubble-body"
              className={cn(
                "whitespace-pre-wrap [overflow-wrap:anywhere] rounded-2xl rounded-br-md px-4 py-2.5 text-md leading-[1.65] text-foreground transition-colors",
                active ? "bg-accent-soft" : "bg-accent-soft/70",
              )}
            >
              {highlightText(m.text, filterQ, "bg-accent-soft text-foreground rounded-sm")}
            </div>
            <div className="flex items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100">
              {canEdit && onEdit && (
                <TooltipHint content="编辑并重发这条消息">
                  <button
                    type="button"
                    className="inline-flex h-5 w-5 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                    aria-label="编辑并重发这条消息"
                    onClick={() => onEdit(m.text)}
                  >
                    <Pencil className="h-3 w-3" />
                  </button>
                </TooltipHint>
              )}
              <CopyButton
                text={m.text}
                iconOnly
                label="复制这条消息"
                className="h-5 w-5 shrink-0 justify-center rounded border-0 bg-transparent p-0"
              />
            </div>
          </div>
        )}
      </div>
      <Dialog open={!!lightbox} onOpenChange={(v) => !v && setLightbox(null)}>
        <DialogContent className="sm:max-w-4xl">
          <DialogTitle className="sr-only">附件图片</DialogTitle>
          {lightbox && (
            <img src={lightbox} alt="附件大图" className="max-h-[80vh] w-full rounded object-contain" />
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}

// MessageBubble is memoized: streaming appends a new message object for the
// last bubble each token, and every other bubble must keep the same props to
// skip re-rendering. Callers therefore pass stable callbacks and precomputed
// flags instead of per-row closures.
export const MessageBubble = memo(function MessageBubble({ m, isLastAssistant, onRegenerate, canRegenerate, toolRunning, onSpeak, costOf, onEdit, canEdit, onConfirm, durationMs, sessionID, filterQ }: {
  m: UIMessage;
  isLastAssistant: boolean;
  filterQ: string;
  onRegenerate: () => void;
  canRegenerate: boolean;
  toolRunning: boolean;
  onSpeak: (text: string) => void;
  costOf: (u?: UsageInfo | null) => string;
  onEdit: (text: string) => void;
  canEdit: boolean;
  onConfirm: (msgId: string, callId: string | undefined, approved: boolean) => void;
  durationMs?: number;
  sessionID: string;
}) {
  const [copied, setCopied] = useState(false);
  const copyMsg = async () => {
    try {
      await navigator.clipboard.writeText(m.text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch { /* ignore */ }
  };
  if (m.kind === "error") {
    return (
      <div className="rounded-xl bg-danger-soft px-4 py-2.5 text-sm text-danger-text">
        {highlightText(m.text, filterQ, "bg-danger-soft text-danger-text rounded-sm")}
        {canRegenerate && (
          <div className="mt-2">
            <TooltipHint content="裁剪本轮并重试（不重复消耗这条错误）">
              <Button
                size="sm"
                variant="outline"
                className="h-7 px-3 text-xs"
                onClick={onRegenerate}
              >
                <RefreshCw className="mr-1 h-3 w-3" /> 重试本轮
              </Button>
            </TooltipHint>
          </div>
        )}
      </div>
    );
  }
  if (m.kind === "confirm") {
    return <ConfirmCard m={m} onConfirm={onConfirm} />;
  }
  if (m.kind === "tool_result" && m.toolName === "plan_todos") {
    return <TodoListCard resp={m.toolResp ?? null} />;
  }
  if (m.kind === "tool_call" || m.kind === "tool_result") {
    return (
      <ToolCallCard
        name={m.toolName ?? "tool"}
        sessionID={sessionID}
        args={m.toolArgs}
        resp={m.toolResp}
        running={m.kind === "tool_call" && toolRunning}
        // durationMs comes from the parent's pairing pass; m.durationMs is
        // never populated, so reading it silently hid every timing chip.
        durationMs={durationMs}
      />
    );
  }
  if (m.kind === "thinking") {
    return (
      <div className="flex justify-start">
        <div className="w-full">
          <ThinkingBlock m={m} />
        </div>
      </div>
    );
  }
  // Direct-chat replies are self-evidently assistant output; the author/time
  // header was pure chrome. Keep the author label only for named agents/team
  // members, where attribution matters. All actions live at the output's
  // bottom-right so they follow the content instead of competing with it.
  const showAuthor = !!m.author && m.author !== "助手";
  return (
    <div className="group flex justify-start">
      <div className="min-w-0 flex-1">
        {showAuthor && (
          <div className="mb-1 flex items-center gap-2">
            <span className="text-xs font-medium text-muted-foreground">{m.author}</span>
          </div>
        )}
        <div className="min-w-0">
          {m.streaming && !m.text ? (
            <span className="inline-flex items-center gap-1.5 text-sm text-muted-foreground">
              <Loader2 className="h-3.5 w-3.5 animate-spin" /> 思考中…
            </span>
          ) : m.kind === "assistant" ? (
            <>
              <MessageMarkdown content={m.text} />
              {m.streaming && <span className="ml-0.5 inline-block h-4 w-1.5 animate-pulse rounded-xs bg-brand align-middle" />}
            </>
          ) : (
            <div className="whitespace-pre-wrap break-words text-md leading-[1.7]">{m.text}</div>
          )}
        </div>
        {/* Copy/regenerate/speak/usage work on every finished assistant
            message. Only regenerate stays bound to the last reply. */}
        {!m.streaming && m.text && (
          <div
            data-slot="assistant-actions"
            className="mt-1.5 flex items-center justify-end gap-1.5 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100"
          >
            <div className="flex items-center gap-0.5">
              {m.usage && m.usage.totalTokens > 0 && (
                <TooltipHint content={`输入 ${m.usage.promptTokens} + 输出 ${m.usage.completionTokens}`}>
                  <span className="mr-1 text-2xs tabular-nums text-muted-foreground/70">
                    {m.usage.totalTokens.toLocaleString()} tokens{costOf(m.usage) && ` · ${costOf(m.usage)}`}
                  </span>
                </TooltipHint>
              )}
              <TooltipHint content={copied ? "已复制" : "复制"}>
                <button
                  className="inline-flex h-6 w-6 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                  aria-label={copied ? "已复制" : "复制这条回复"}
                  onClick={copyMsg}
                >
                  {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
                </button>
              </TooltipHint>
              {isLastAssistant && canRegenerate && (
                <TooltipHint content="重新生成最后一条回复">
                  <button
                    className="inline-flex h-6 w-6 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                    aria-label="重新生成最后一条回复"
                    onClick={onRegenerate}
                  >
                    <RefreshCw className="h-3.5 w-3.5" />
                  </button>
                </TooltipHint>
              )}
              <TooltipHint content="朗读这条回复">
                <button
                  className="inline-flex h-6 w-6 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                  aria-label="朗读这条回复"
                  onClick={() => onSpeak(m.text)}
                >
                  <Volume2 className="h-3.5 w-3.5" />
                </button>
              </TooltipHint>
            </div>
            {m.timestamp && (
              <TooltipHint content={`输出完成于 ${fmtTime(m.timestamp)}`}>
                <time
                  data-slot="completion-time"
                  dateTime={m.timestamp}
                  className="whitespace-nowrap text-xs tabular-nums text-muted-foreground"
                >
                  {fmtTime(m.timestamp)}
                </time>
              </TooltipHint>
            )}
          </div>
        )}
      </div>
    </div>
  );
});
