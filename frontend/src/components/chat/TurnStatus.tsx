import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";

/** turnStatusElapsed 为快速回复保持前两秒简洁。 */
export function turnStatusElapsed(seconds: number): string {
  return seconds >= 2 ? `${seconds}s` : "";
}

/**
 * TurnStatus 是回复侧的占位符，用于表示发送消息与首个流式思考/token
 * 之间的间隔。它有意不作为消息：它绝不进入历史、分组、导出或流式状态。
 */
export function TurnStatus({ label = "正在思考", className }: {
  label?: string;
  className?: string;
}) {
  const [elapsed, setElapsed] = useState(0);

  useEffect(() => {
    const started = Date.now();
    const timer = window.setInterval(() => {
      setElapsed(Math.floor((Date.now() - started) / 1000));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [label]);

  const elapsedLabel = turnStatusElapsed(elapsed);

  return (
    <div
      data-slot="turn-status"
      aria-hidden="true"
      className={cn("flex min-h-6 items-center gap-2 text-ui text-muted-foreground", className)}
    >
      <span className="wb-thinking-shimmer font-medium">{label}</span>
      <span className="flex items-center gap-1">
        <span className="wb-dot-pulse h-1 w-1 rounded-full bg-accent-text/70" />
        <span className="wb-dot-pulse h-1 w-1 rounded-full bg-accent-text/70 [animation-delay:160ms]" />
        <span className="wb-dot-pulse h-1 w-1 rounded-full bg-accent-text/70 [animation-delay:320ms]" />
      </span>
      {elapsedLabel && (
        <span className="tabular-nums text-2xs text-muted-foreground/60">
          {elapsedLabel}
        </span>
      )}
    </div>
  );
}
