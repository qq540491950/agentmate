import type { ContextUsageView } from "@/lib/contextUsage";
import { cn } from "@/lib/utils";

const RING_COLOR: Record<ContextUsageView["color"], string> = {
  "bg-destructive": "text-danger-text",
  "bg-warning": "text-warning",
  "bg-success": "text-success",
};

/**
 * ContextRing 是输入区的上下文用量指示器，采用 Codex
 * 桌面端风格：一个小型进度圆环，随会话的上下文占用情况填充。数值位于外层容器的
 * 工具提示中——圆环本身保持静默，从而让输入区工具栏保持其占用空间。
 */
export function ContextRing({ usage }: { usage: ContextUsageView }) {
  const r = 6.5;
  const c = 2 * Math.PI * r;
  const dash = (Math.min(100, usage.pct) / 100) * c;
  return (
    <svg viewBox="0 0 16 16" className={cn("h-3.5 w-3.5", RING_COLOR[usage.color])} aria-hidden>
      <circle cx="8" cy="8" r={r} fill="none" stroke="currentColor" strokeWidth="2" className="opacity-20" />
      {usage.pct >= 0.5 && (
        <circle
          cx="8"
          cy="8"
          r={r}
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeDasharray={usage.pct >= 99.5 ? undefined : `${dash} ${c}`}
          transform="rotate(-90 8 8)"
        />
      )}
    </svg>
  );
}
