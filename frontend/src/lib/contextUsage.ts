// 上下文占用条的口径：真实上下文 = 最后一次模型调用的提示词 token 数。
// 每次调用都会重复携带完整的历史前缀，把历次调用的用量累加（计费口径的
// totalTokens）再除以窗口，结果会是真实占用的数倍——工具循环较长的会话因此会误报
// 「上下文即将耗尽」，而实际占用可能只有窗口的一成多。
export interface ContextUsageView {
  pct: number;
  color: "bg-destructive" | "bg-warning" | "bg-success";
  win: number;
  label: string;
  used: number;
  /** estimated 表示 ≈ 近似值：模型的上游从未上报用量元数据，因此该视图由对话本身推导得出。 */
  estimated: boolean;
}

/** 真实上下文占用 → 视图；无数据（模型未上报用量）时不渲染，而不是猜测。 */
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

/** trimTrailingZero 将数值格式化为一位小数，并去掉 ".0" 后缀。 */
function trimTrailingZero(v: number): string {
  return (Math.round(v * 10) / 10).toFixed(1).replace(/\.0$/, "");
}

/**
 * compactTokens 会为悬停气泡简写 token 计数：265000 → "265k"，996000 →
 * "996k"，1500 → "1.5k"，1000000 → "1M"；低于 1000 时保持精确。 气泡位于 16px
 * 圆环上方，因此完整的千位分隔符（"996,000 tokens"）会让该行折成两行。
 */
export function compactTokens(n: number): string {
  const value = Math.max(0, Math.round(n || 0));
  if (value < 1000) return String(value);
  // 仅当小数位携带信息时保留一位小数：1.5k，但 265k（不是 265.0k）。
  const k = Math.round(value / 100) / 10;
  if (k < 1000) return `${trimTrailingZero(k)}k`;
  return `${trimTrailingZero(value / 1_000_000)}M`;
}

/** SessionUsageSummary 是工具提示追加的累计计费子集。 */
export interface SessionUsageSummary {
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
  /**
   * cachedTokens
   * 是当前提示词（最后一次调用）的缓存部分。当供应商未上报提示词缓存时为零——它
   * 本身绝不表示 "cache miss"。
   */
  cachedTokens?: number;
}

/** ContextTooltipContent 是悬停气泡的结构化文本。 */
export interface ContextTooltipContent {
  /** title 是气泡的标题行（"背景信息窗口："）。 */
  title: string;
  /** rows 是标题下方的明细行。 */
  rows: string[];
  /**
   * ariaLabel 将气泡内容压缩成一句话，供圆环的 aria-label
   * 使用——键盘用户和屏幕阅读器用户绝不会看到悬停卡片。
   */
  ariaLabel: string;
}

/**
 * contextTooltipLines
 * 在输入区的上下文圆环上方渲染悬停卡片。最多三行——窗口占用情况、供应商报告时的
 * 缓存占比，以及累计计费总额：
 *
 * 背景信息窗口： 11% 已用（剩余 89%） 已用 109k 标记，共 996k
 *
 * 缓存行仅在真实命中时出现：从未报告缓存的模型不得增加一行 "0
 * cached"。此函数是纯函数，因此文案仍可进行单元测试；null 表示 "nothing to
 * say"，调用方不渲染卡片。
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
    // 窗口占用以标记为单位（这是模型 token 预算的产品措辞），与参考气泡一致。
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

/** EstimateMessage 是估算器读取的逐消息子集。 */
export type EstimateMessage = {
  kind: string;
  text?: string;
  toolArgs?: { [k: string]: any } | null;
  toolResp?: { [k: string]: any } | null;
};

// 每条消息的上限：巨大的工具载荷无论如何都会使估算饱和，而该上限限制了工具调用
// 密集的会话中的字符串化/扫描工作量。
const estimatePerMessageCap = 96 * 1024;

/**
 * estimateContextTokens 会从对话本身估算下一次调用的提示词大小：CJK 字符按约
 * 0.7 个 token 计，其余字符按约 0.25 个 token
 * 计，另加每条消息的一点固定开销。它仅用于为上游从不报告用量的模型提供 ≈
 * 回退值（某些网关会忽略
 * stream_options.include_usage）——真实用量元数据始终优先。
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
