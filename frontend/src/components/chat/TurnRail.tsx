import { memo, useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { tooltipCardClassName } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

export type RailTurn = {
  id: string;
  /** text is the truncated snippet used for the bar width and a11y label. */
  text: string;
  /** fullText feeds the one-line hover/focus label. */
  fullText: string;
};

type RailLabel = {
  turn: RailTurn;
  index: number;
  top: number;
  left: number;
  maxWidth: number;
};

// Bar widths are a fixed class ladder: dynamic w-* classes would be purged.
const BAR_WIDTHS = ["w-3", "w-3.5", "w-4", "w-5", "w-6", "w-7"] as const;
const EXPANDED_BAR_WIDTH = "w-10";
const EXPANDED_BAR_WIDTH_REM = 2.5;
const LABEL_MAX_WIDTH = 280;

/** railBarWidth returns the static width class for a bar. A hovered/focused
 *  bar uses the fixed w-10 tier so it visibly extends to the right; every
 *  other bar keeps its text-length width. */
export function railBarWidth(text: string, expanded: boolean): string {
  return expanded ? EXPANDED_BAR_WIDTH : BAR_WIDTHS[Math.min(BAR_WIDTHS.length - 1, Math.floor(text.length / 60))];
}

/** railLabelText is the compact one-line label shown beside a rail bar. It
 *  prefers the input's short first line, then falls back to a truncated
 *  single-line prompt. Empty inputs still get a turn number. */
export function railLabelText(text: string, index: number): string {
  const trimmed = text.trim();
  if (!trimmed) return `第 ${index + 1} 轮`;
  const firstLine = trimmed.split(/\r?\n/, 1)[0]?.trim() ?? "";
  const line = firstLine && firstLine.length <= 60 ? firstLine : trimmed;
  return line.length > 60 ? `${line.slice(0, 60)}…` : line;
}

/** railLabelGeometry places the compact label just to the right of its bar,
 *  vertically centred on it. The label floats over the output column instead
 *  of reserving layout space, and is clamped to the viewport. */
export function railLabelGeometry(
  barRight: number,
  barTop: number,
  viewportWidth: number,
  viewportHeight: number,
): { top: number; left: number; maxWidth: number } {
  const left = barRight + 8;
  const top = Math.max(8, Math.min(barTop - 12, viewportHeight - 36));
  const maxWidth = Math.max(80, Math.min(LABEL_MAX_WIDTH, viewportWidth - left - 16));
  return { top, left, maxWidth };
}

/** TurnRail is the slim left-edge navigator: one bar per user input, the turn
 *  the reader is currently at highlighted. Clicking a bar scrolls that turn
 *  into view. Hovering/focusing a bar grows it to the right and shows a compact
 *  one-line label beside it, floating over the output column; leaving the bar
 *  restores its original width. It hides below md and for trivial chats
 *  (fewer than two turns). */
export const TurnRail = memo(function TurnRail({ turns, activeId, onSelect, className }: {
  turns: RailTurn[];
  activeId: string | null;
  onSelect: (msgId: string) => void;
  className?: string;
}) {
  const closeTimer = useRef<number | null>(null);
  const [label, setLabel] = useState<RailLabel | null>(null);
  const [hoveredId, setHoveredId] = useState<string | null>(null);

  const cancelClose = useCallback(() => {
    if (closeTimer.current !== null) {
      window.clearTimeout(closeTimer.current);
      closeTimer.current = null;
    }
  }, []);

  const scheduleClose = useCallback(() => {
    cancelClose();
    closeTimer.current = window.setTimeout(() => {
      setLabel(null);
      setHoveredId(null);
    }, 120);
  }, [cancelClose]);

  useEffect(() => () => cancelClose(), [cancelClose]);

  // A sidebar toggle re-centres the chat column without firing resize/scroll;
  // close the fixed label so it cannot linger over the output.
  useEffect(() => {
    const close = () => {
      setLabel(null);
      setHoveredId(null);
    };
    window.addEventListener("wb-sidebar-toggled", close);
    return () => window.removeEventListener("wb-sidebar-toggled", close);
  }, []);

  // A viewport scroll/resize invalidates the fixed portal coordinates; close
  // instead of leaving the label detached from its bar.
  useEffect(() => {
    if (!label) return;
    const close = () => {
      setLabel(null);
      setHoveredId(null);
    };
    window.addEventListener("resize", close);
    window.addEventListener("scroll", close, true);
    return () => {
      window.removeEventListener("resize", close);
      window.removeEventListener("scroll", close, true);
    };
  }, [label]);

  const openLabel = useCallback((turn: RailTurn, index: number, el: HTMLElement) => {
    cancelClose();
    setHoveredId(turn.id);
    const barRect = el.getBoundingClientRect();
    // The hovered/focused bar expands to w-10 (2.5rem). Read the final width
    // from the root font size instead of barRect.width: the transition means
    // getBoundingClientRect() still reports the old width on the frame that
    // focus/mouse-enter fires.
    const rootFontSize = Number.parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;
    const labelRight = barRect.left + Math.max(barRect.width, rootFontSize * EXPANDED_BAR_WIDTH_REM);
    setLabel({
      turn,
      index,
      ...railLabelGeometry(labelRight, barRect.top, window.innerWidth, window.innerHeight),
    });
  }, [cancelClose]);

  if (turns.length < 2) return null;
  return (
    <div data-slot="turn-rail" className={cn("flex max-h-[70%] flex-col", className)}>
      <nav
        aria-label="轮次导航"
        className="flex min-h-0 flex-col items-start gap-2.5 overflow-x-hidden overflow-y-auto py-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
      >
        {turns.map((t, i) => (
          <button
            key={t.id}
            type="button"
            aria-label={`第 ${i + 1} 轮：${t.text}`}
            aria-current={t.id === activeId ? "true" : undefined}
            onClick={() => {
              onSelect(t.id);
              setLabel(null);
            }}
            onMouseEnter={(e) => openLabel(t, i, e.currentTarget)}
            onMouseLeave={scheduleClose}
            onFocus={(e) => openLabel(t, i, e.currentTarget)}
            onBlur={scheduleClose}
            className={cn(
              "relative h-[3px] shrink-0 rounded-full transition-all duration-150 after:absolute after:-inset-y-1 after:left-0 after:right-0 after:content-['']",
              railBarWidth(t.text, t.id === hoveredId),
              t.id === activeId
                ? "bg-foreground"
                : t.id === hoveredId
                  ? "bg-foreground/80"
                  : "bg-muted-foreground/30 hover:bg-muted-foreground/60",
            )}
          />
        ))}
      </nav>
      {label && typeof document !== "undefined" && createPortal(
        <div
          data-slot="turn-rail-label"
          aria-hidden="true"
          className={cn(
            "pointer-events-none fixed z-[80] truncate animate-in fade-in-0 duration-100",
            tooltipCardClassName,
          )}
          style={{ top: label.top, left: label.left, maxWidth: label.maxWidth }}
        >
          {railLabelText(label.turn.fullText, label.index)}
        </div>,
        document.body,
      )}
    </div>
  );
});
