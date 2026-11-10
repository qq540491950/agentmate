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

/** trimTrailingZero formats one decimal, dropping a ".0" suffix. */
function trimTrailingZero(v: number): string {
  return (Math.round(v * 10) / 10).toFixed(1).replace(/\.0$/, "");
}

/**
 * compactTokens shortens a token count for the hover bubble: 265000 → "265k",
 * 996000 → "996k", 1500 → "1.5k", 1000000 → "1M"; below 1000 stays exact.
 * The bubble sits above a 16px ring, so full thousands separators
 * ("996,000 tokens") wrapped the line in two.
 */
export function compactTokens(n: number): string {
  const value = Math.max(0, Math.round(n || 0));
  if (value < 1000) return String(value);
  // One decimal only while it carries information: 1.5k, but 265k (not 265.0k).
  const k = Math.round(value / 100) / 10;
  if (k < 1000) return `${trimTrailingZero(k)}k`;
  return `${trimTrailingZero(value / 1_000_000)}M`;
}

/** SessionUsageSummary is the cumulative-billing subset the tooltip appends. */
export interface SessionUsageSummary {
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
  /** cachedTokens is the cached share of the CURRENT prompt (last call). Zero
   *  when the provider does not report prompt caching — it never means
   *  "cache miss" on its own. */
  cachedTokens?: number;
}

/** ContextTooltipContent is the hover bubble's structured text. */
export interface ContextTooltipContent {
  /** title is the bubble's heading line ("背景信息窗口："). */
  title: string;
  /** rows are the detail lines under the heading. */
  rows: string[];
  /** ariaLabel flattens the bubble into one sentence for the ring's
   *  aria-label — keyboard and screen-reader users never see the hover card. */
  ariaLabel: string;
}

/**
 * contextTooltipLines renders the hover card above the composer's context
 * ring. Three lines at most — window occupancy, the cached share when the
 * provider reports one, and the cumulative billed total:
 *
 *   背景信息窗口：
 *   11% 已用（剩余 89%）
 *   已用 109k 标记，共 996k
 *
 * The cache line only appears for a real hit: a model that never reports
 * caching must not gain a "0 cached" row. Pure so the wording stays
 * unit-tested; null means "nothing to say" and the caller renders no card.
 */
export function contextTooltipLines(
  usage: ContextUsageView | null,
  sessionUsage: SessionUsageSummary | null | undefined,
  cost?: string,
): ContextTooltipContent | null {
  const rows: string[] = [];
  if (usage) {
    const remain = Math.max(0, 100 - Math.min(100, usage.pct));
    rows.push(`${usage.label} 已用（剩余 ${remain.toFixed(0)}%）`);
    // Window occupancy is measured in 标记 (the product wording for the
    // model's token budget), matching the reference bubble.
    rows.push(`已用 ${compactTokens(usage.used)} 标记，共 ${compactTokens(usage.win)}`);
    if (usage.estimated) rows.push("按会话内容估算——当前模型未返回用量");
  }
  const cached = sessionUsage?.cachedTokens ?? 0;
  if (cached > 0) {
    rows.push(`缓存命中 ${compactTokens(cached)} 标记`);
  }
  if (sessionUsage && sessionUsage.totalTokens > 0) {
    const costPart = cost ? ` · ${cost}` : "";
    rows.push(`累计 ${compactTokens(sessionUsage.totalTokens)} tokens${costPart}`);
  }
  if (rows.length === 0) return null;
  return {
    title: "背景信息窗口：",
    rows,
    ariaLabel: ["背景信息窗口", ...rows].join("；"),
  };
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
