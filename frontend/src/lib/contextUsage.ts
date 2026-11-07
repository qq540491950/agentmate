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
}

/** 真实上下文占用 → 视图；无数据（模型未报 usage）时不渲染而不是猜测。 */
export function resolveContextUsage(
  contextTokens: number | undefined | null,
  contextWindow: number,
): ContextUsageView | null {
  if (contextWindow <= 0 || !contextTokens || contextTokens <= 0) return null;
  const pct = Math.min(100, (contextTokens / contextWindow) * 100);
  const color = pct >= 80 ? "bg-destructive" : pct >= 50 ? "bg-warning" : "bg-success";
  const label = pct < 1 ? "<1%" : pct < 10 ? `${pct.toFixed(1)}%` : `${pct.toFixed(0)}%`;
  return { pct, color, win: contextWindow, label, used: contextTokens };
}
