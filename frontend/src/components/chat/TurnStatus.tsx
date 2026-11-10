import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";

/** turnStatusElapsed keeps the first two seconds clean for fast replies. */
export function turnStatusElapsed(seconds: number): string {
  return seconds >= 2 ? `${seconds}s` : "";
}

/**
 * TurnStatus is the reply-side placeholder for the gap between sending a
 * message and the first streamed thought/token. It is intentionally not a
 * message: it never enters history, grouping, export, or the streaming state.
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
