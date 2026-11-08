// 上下文占用条口径：真实上下文 = 最后一次模型调用的 prompt tokens。
// 每次调用都重复携带完整历史前缀，把历次调用的用量累加（计费口径的
// totalTokens）再除以窗口，会数倍于真实占用——长工具循环会话因此误报
// 「上下文即将耗尽」而实际占用可能只有窗口的一成多。
export interface ContextUsageView {
  pct: number;
  color: "bg-destructive" | "bg-warning" | "bg-success";
  win: number;
  label: string;
  used: number;
  /** estimated marks a ≈ approximation: the model's upstream never reported
   *  usage metadata, so the view is derived from the conversation itself. */
  estimated: boolean;
}

/** 真实上下文占用 → 视图；无数据（模型未报 usage）时不渲染而不是猜测。 */
export function resolveContextUsage(
  contextTokens: number | undefined | null,
  contextWindow: number,
  estimated = false,
): ContextUsageView | null {
  if (contextWindow <= 0 || !contextTokens || contextTokens <= 0) return null;
  const pct = Math.min(100, (contextTokens / contextWindow) * 100);
  const color = pct >= 80 ? "bg-destructive" : pct >= 50 ? "bg-warning" : "bg-success";
  const approx = estimated ? "≈" : "";
  const label = approx + (pct < 1 ? "<1%" : pct < 10 ? `${pct.toFixed(1)}%` : `${pct.toFixed(0)}%`);
  return { pct, color, win: contextWindow, label, used: contextTokens, estimated };
}

/** SessionUsageSummary is the cumulative-billing subset the tooltip appends. */
export interface SessionUsageSummary {
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
}

/**
 * contextTooltipLines renders the hover bubble under the composer's context
 * ring (Codex desktop style): real occupancy first, cumulative billed usage
 * last. Pure so the bubble wording stays unit-tested.
 */
export function contextTooltipLines(
  usage: ContextUsageView | null,
  sessionUsage: SessionUsageSummary | null | undefined,
  cost?: string,
): string[] {
  const lines: string[] = [];
  if (usage) {
    const remain = Math.max(0, 100 - Math.min(100, usage.pct));
    lines.push("上下文窗口");
    lines.push(`${usage.label} 已用（剩余 ${remain.toFixed(0)}%）`);
    lines.push(`已用 ${usage.used.toLocaleString()}，共 ${usage.win.toLocaleString()} tokens`);
    if (usage.estimated) lines.push("按会话内容估算——当前模型未返回用量");
  }
  if (sessionUsage && sessionUsage.totalTokens > 0) {
    const costPart = cost ? ` · ${cost}` : "";
    lines.push(`累计 ${sessionUsage.totalTokens.toLocaleString()} tokens${costPart}`);
    lines.push(`输入 ${sessionUsage.promptTokens.toLocaleString()} · 输出 ${sessionUsage.completionTokens.toLocaleString()}`);
  }
  return lines;
}

/** EstimateMessage is the per-message subset the estimator reads. */
export type EstimateMessage = {
  kind: string;
  text?: string;
  toolArgs?: { [k: string]: any } | null;
  toolResp?: { [k: string]: any } | null;
};

// Per-message cap: a huge tool payload saturates the estimate either way, and
// the cap bounds the stringify/scan work on tool-heavy sessions.
const estimatePerMessageCap = 96 * 1024;

/**
 * estimateContextTokens approximates the next call's prompt size from the
 * conversation itself: prose chars count ≈0.7 tokens for CJK and ≈0.25 for
 * everything else, plus a small per-message envelope. It only feeds the ≈
 * fallback for models whose upstream never reports usage (some gateways
 * ignore stream_options.include_usage) — real usage metadata always wins.
 */
export function estimateContextTokens(messages: EstimateMessage[]): number {
  let cjk = 0;
  let other = 0;
  for (const m of messages) {
    const text = (m.text ?? "").slice(0, estimatePerMessageCap);
    for (const ch of text) {
      if ((ch.codePointAt(0) ?? 0) >= 0x2e80) cjk++;
      else other++;
    }
    let payload = 0;
    if (m.toolArgs) payload += JSON.stringify(m.toolArgs).length;
    if (m.toolResp) payload += JSON.stringify(m.toolResp).length;
    other += Math.min(payload, estimatePerMessageCap);
    other += 12;
  }
  return Math.ceil(cjk * 0.7 + other * 0.25);
}
