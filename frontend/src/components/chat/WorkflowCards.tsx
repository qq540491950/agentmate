import { useMemo, useState } from "react";
import {
  CheckCircle2, CircleAlert, PauseCircle, Play, Plus, ShieldCheck,
  Square, Trash2, Undo2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import type { BudgetPauseInfo, VerificationInfo } from "@/lib/chatStreamReducer";
import type { TodoItem } from "@/components/chat/TodoCards";

function statusTone(tone: "warning" | "danger" | "success" | "neutral") {
  switch (tone) {
    case "warning":
      return "bg-warning-soft text-warning-text";
    case "danger":
      return "bg-danger-soft text-danger-text";
    case "success":
      return "bg-success-soft text-success-text";
    default:
      return "bg-panel text-muted-foreground";
  }
}

export function BudgetPauseCard({
  budget, busy, onContinue, onStop,
}: {
  budget: BudgetPauseInfo;
  busy?: boolean;
  onContinue: () => void;
  onStop: () => void;
}) {
  const reason = budget.reason === "tool_calls"
    ? "工具调用达到软阈值"
    : budget.reason === "model_calls"
      ? "模型调用达到软阈值"
      : "token 用量达到软阈值";
  return (
    <div className="mx-4 mb-3 rounded-xl bg-warning-soft p-3 text-sm text-warning-text" role="status">
      <div className="flex items-start gap-2">
        <PauseCircle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
        <div className="min-w-0 flex-1">
          <div className="font-medium">本轮已按预算暂停</div>
          <p className="mt-1 text-xs">
            {reason}：{budget.used.toLocaleString()} / {budget.limit.toLocaleString()}。
            工具 {budget.toolCalls} 次，模型 {budget.modelCalls} 次，token {budget.turnTokens.toLocaleString()}。
          </p>
          <div className="mt-2 flex flex-wrap gap-2">
            <Button size="sm" onClick={onContinue} disabled={busy}>
              <Play className="h-3.5 w-3.5" /> 继续
            </Button>
            <Button size="sm" variant="outline" onClick={onStop} disabled={busy}>
              <Square className="h-3.5 w-3.5" /> 停止
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}

export function VerificationCard({
  verification, busy, onRetry, onExempt,
}: {
  verification: VerificationInfo;
  busy?: boolean;
  onRetry?: () => void;
  onExempt?: () => void;
}) {
  const blocked = verification.status === "blocked" || verification.status === "failed";
  const exempt = verification.status === "exempt" || verification.exempt;
  const tone = blocked ? "danger" : exempt ? "success" : "warning";
  const Icon = blocked ? CircleAlert : exempt ? ShieldCheck : CheckCircle2;
  return (
    <div className={cn("mx-4 mb-3 rounded-xl p-3 text-sm", statusTone(tone))} role="status">
      <div className="flex items-start gap-2">
        <Icon className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
        <div className="min-w-0 flex-1">
          <div className="font-medium">
            {exempt ? "验证已由用户豁免" : blocked ? "验证未通过，任务已阻塞" : "验证结果"}
          </div>
          {verification.reason && <p className="mt-1 text-xs">{verification.reason}</p>}
          {verification.artifact && (
            <p className="mt-1 break-all text-xs opacity-90">输出 artifact：{verification.artifact}</p>
          )}
          {blocked && (
            <div className="mt-2 flex flex-wrap gap-2">
              {onRetry && <Button size="sm" onClick={onRetry} disabled={busy}><Undo2 className="h-3.5 w-3.5" /> 重试失败步骤</Button>}
              {onExempt && <Button size="sm" variant="outline" onClick={onExempt} disabled={busy}>显式豁免</Button>}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

export function RecoveryBanner({
  status, busy, onResume, onViewChanges, onAbandon,
}: {
  status: string;
  busy?: boolean;
  onResume: () => void;
  onViewChanges: () => void;
  onAbandon: () => void;
}) {
  const label = status === "interrupted"
    ? "上次运行中断"
    : status === "cancelled"
      ? "上次运行已停止"
      : status === "budget_paused"
        ? "上次运行因预算暂停"
        : "上次运行未完成";
  return (
    <div className="mx-4 mb-3 rounded-xl bg-warning-soft p-3 text-sm text-warning-text" role="status">
      <div className="flex flex-wrap items-center gap-2">
        <CircleAlert className="h-4 w-4 shrink-0" aria-hidden="true" />
        <span className="min-w-0 flex-1">{label}。可从检查点继续，或先查看工作区变更。</span>
        <Button size="sm" onClick={onResume} disabled={busy}>继续</Button>
        <Button size="sm" variant="outline" onClick={onViewChanges} disabled={busy}>查看变更</Button>
        <Button size="sm" variant="ghost" onClick={onAbandon} disabled={busy}>放弃</Button>
      </div>
    </div>
  );
}

export type ApprovalItem = {
  id?: string;
  ID?: string;
  turnId?: string;
  turnID?: string;
  toolName?: string;
  ToolName?: string;
  hint?: string;
  Hint?: string;
  risk?: string;
  Risk?: string;
  diff?: string;
  Diff?: string;
  policyJson?: string;
  PolicyJSON?: string;
  argsJson?: string;
  ArgsJSON?: string;
};

export function approvalID(item: ApprovalItem): string {
  return item.id ?? item.ID ?? "";
}

export function approvalTool(item: ApprovalItem): string {
  return item.toolName ?? item.ToolName ?? "tool";
}

export function ApprovalQueueDialog({
  open, onOpenChange, approvals, busy, onConfirm, onGrantScope,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  approvals: ApprovalItem[];
  busy?: boolean;
  onConfirm: (callIDs: string[], approved: boolean) => void;
  onGrantScope: (callID: string) => void;
}) {
  const ids = approvals.map(approvalID).filter(Boolean);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle className="text-base">审批队列 · {approvals.length} 项待处理</DialogTitle>
        </DialogHeader>
        {approvals.length === 0 ? (
          <div className="rounded-xl bg-recess/60 p-3 text-sm text-muted-foreground">当前没有待批准的调用。</div>
        ) : (
          <>
            <div className="flex flex-wrap gap-2">
              <Button size="sm" onClick={() => onConfirm(ids, true)} disabled={busy}>全部批准</Button>
              <Button size="sm" variant="outline" onClick={() => onConfirm(ids, false)} disabled={busy}>全部拒绝</Button>
            </div>
            <div className="max-h-[60vh] space-y-2 overflow-y-auto">
              {approvals.map((item) => {
                const id = approvalID(item);
                const risk = item.risk ?? item.Risk ?? "medium";
                const diff = item.diff ?? item.Diff ?? "";
                const hint = item.hint ?? item.Hint ?? "";
                const policy = item.policyJson ?? item.PolicyJSON ?? "";
                return (
                  <div key={id} className="surface-inset rounded-xl p-3 text-sm">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-medium">{approvalTool(item)}</span>
                      <span className={cn("rounded-md px-1.5 py-0.5 text-xs", risk === "high" ? "bg-danger-soft text-danger-text" : "bg-warning-soft text-warning-text")}>
                        风险 {risk}
                      </span>
                      <span className="text-xs text-muted-foreground">{id}</span>
                    </div>
                    {hint && <p className="mt-1 text-xs text-muted-foreground">{hint}</p>}
                    {policy && <p className="mt-1 break-all text-xs text-muted-foreground">{policy}</p>}
                    {diff && (
                      <pre className="surface-inset mt-2 max-h-48 overflow-auto rounded-lg p-2 text-xs leading-relaxed text-foreground">
                        {diff}
                      </pre>
                    )}
                    <div className="mt-2 flex flex-wrap gap-2">
                      <Button size="sm" onClick={() => onConfirm([id], true)} disabled={busy}>批准</Button>
                      <Button size="sm" variant="outline" onClick={() => onConfirm([id], false)} disabled={busy}>拒绝</Button>
                      {risk !== "high" && (
                        <Button size="sm" variant="ghost" onClick={() => onGrantScope(id)} disabled={busy}>本会话自动批准同类</Button>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

export function PlanEditorDialog({
  open, onOpenChange, steps, busy, onSave,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  steps: TodoItem[];
  busy?: boolean;
  onSave: (steps: TodoItem[]) => void;
}) {
  const initial = useMemo<PlanDraftStep[]>(
    () => steps.map((step, index) => ({
      ...step,
      id: step.id || `step-${index + 1}`,
      dependsOnText: (step.dependsOn ?? []).join(", "),
      acceptanceText: (step.acceptance ?? []).join("\n"),
      evidenceText: (step.evidence ?? []).join("\n"),
    })),
    [steps],
  );
  const [draft, setDraft] = useState(initial);
  const update = (index: number, patch: Record<string, unknown>) => {
    setDraft((current) => current.map((step, i) => i === index ? { ...step, ...patch } : step));
  };
  const save = () => {
    onSave(planDraftToSteps(draft));
  };
  return (
    <Dialog open={open} onOpenChange={(next) => { if (next) setDraft(initial); onOpenChange(next); }}>
      <DialogContent className="sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle className="text-base">编辑计划</DialogTitle>
        </DialogHeader>
        <div className="max-h-[62vh] space-y-2 overflow-y-auto">
          {draft.length === 0 && <div className="rounded-xl bg-recess/60 p-3 text-sm text-muted-foreground">还没有计划步骤。</div>}
          {draft.map((step, index) => (
            <div key={step.id || index} className="surface-raised rounded-xl bg-panel p-3">
              <div className="flex flex-wrap items-center gap-2">
                <input
                  value={step.id}
                  onChange={(e) => update(index, { id: e.target.value })}
                  aria-label="步骤 ID"
                  className="h-8 w-28 rounded-lg bg-control px-2 text-xs text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring"
                />
                <input
                  value={step.content}
                  onChange={(e) => update(index, { content: e.target.value })}
                  aria-label="步骤标题"
                  className="h-8 min-w-48 flex-1 rounded-lg bg-control px-2 text-xs text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring"
                />
                <select
                  value={step.status}
                  onChange={(e) => update(index, { status: e.target.value })}
                  aria-label="步骤状态"
                  className="h-8 rounded-lg bg-control px-2 text-xs text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  {["pending", "in_progress", "blocked", "completed", "failed", "skipped"].map((status) => (
                    <option key={status} value={status}>{status}</option>
                  ))}
                </select>
                <Button size="icon-sm" variant="ghost" aria-label="删除步骤" onClick={() => setDraft((current) => current.filter((_, i) => i !== index))}>
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
              <div className="mt-2 grid gap-2 md:grid-cols-3">
                <label className="text-xs text-muted-foreground">
                  依赖（逗号分隔）
                  <input
                    value={step.dependsOnText}
                    onChange={(e) => update(index, { dependsOnText: e.target.value })}
                    className="mt-1 h-8 w-full rounded-lg bg-control px-2 text-xs text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  />
                </label>
                <label className="text-xs text-muted-foreground">
                  验收标准（每行一条）
                  <Textarea
                    value={step.acceptanceText}
                    onChange={(e) => update(index, { acceptanceText: e.target.value })}
                    className="mt-1 min-h-16 text-xs"
                  />
                </label>
                <label className="text-xs text-muted-foreground">
                  证据（每行一条）
                  <Textarea
                    value={step.evidenceText}
                    onChange={(e) => update(index, { evidenceText: e.target.value })}
                    className="mt-1 min-h-16 text-xs"
                  />
                </label>
              </div>
            </div>
          ))}
        </div>
        <DialogFooter className="flex-wrap">
          <Button
            variant="outline"
            onClick={() => setDraft((current) => [...current, {
              id: `step-${current.length + 1}`, content: "", status: "pending",
              dependsOnText: "", acceptanceText: "", evidenceText: "",
            }])}
          >
            <Plus className="h-3.5 w-3.5" /> 添加步骤
          </Button>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>取消</Button>
          <Button onClick={save} disabled={busy}>保存计划</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export type PlanDraftStep = TodoItem & {
  dependsOnText: string;
  acceptanceText: string;
  evidenceText: string;
};

export function planDraftToSteps(draft: PlanDraftStep[]): TodoItem[] {
  return draft.map((step) => ({
    id: step.id,
    content: step.content,
    status: step.status,
    dependsOn: step.dependsOnText.split(",").map((value) => value.trim()).filter(Boolean),
    acceptance: step.acceptanceText.split("\n").map((value) => value.trim()).filter(Boolean),
    evidence: step.evidenceText.split("\n").map((value) => value.trim()).filter(Boolean),
  }));
}
