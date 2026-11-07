import type { ContextUsageView } from "@/lib/contextUsage";
import { cn } from "@/lib/utils";

const RING_COLOR: Record<ContextUsageView["color"], string> = {
  "bg-destructive": "text-destructive",
  "bg-warning": "text-warning",
  "bg-success": "text-success",
};

/**
 * ContextRing is the composer's context-usage indicator, in the Codex desktop
 * style: a small progress circle that fills with the session's context
 * occupancy. The numbers live in the wrapper's tooltip — the ring itself
 * stays mute so the composer toolbar keeps its footprint.
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
