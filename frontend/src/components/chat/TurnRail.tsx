import { memo, useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { CopyButton } from "@/components/MessageMarkdown";
import { cn } from "@/lib/utils";

export type RailTurn = {
  id: string;
  /** text is the truncated snippet used for the bar width and a11y label. */
  text: string;
  /** fullText feeds the hover preview card and its copy action. */
  fullText: string;
};

type RailPreview = {
  turn: RailTurn;
  index: number;
  top: number;
  left: number;
  width: number;
  side: "left" | "right";
};

// Bar widths are a fixed class ladder: dynamic w-* classes would be purged.
const BAR_WIDTHS = ["w-3", "w-3.5", "w-4", "w-5", "w-6", "w-7"] as const;
const PREVIEW_MIN_WIDTH = 180;
const PREVIEW_MAX_WIDTH = 320;

function barWidth(text: string): string {
  return BAR_WIDTHS[Math.min(BAR_WIDTHS.length - 1, Math.floor(text.length / 60))];
}

/** railPreviewTitle uses the first line when the input has a distinct short
 *  heading; single-line inputs fall back to the turn number so the preview
 *  does not repeat the body verbatim. */
export function railPreviewTitle(text: string, index: number): string {
  const trimmed = text.trim();
  const firstLine = trimmed.split(/\r?\n/, 1)[0]?.trim() ?? "";
  if (firstLine && firstLine !== trimmed && firstLine.length <= 60) return firstLine;
  return `第 ${index + 1} 轮`;
}

/** railPreviewGeometry keeps the preview card out of the output column:
 *  it opens to the right when the gutter before outputLeft is wide enough,
 *  otherwise it opens over the sidebar on the left. It returns null when
 *  neither side has a usable gutter. */
export function railPreviewGeometry(
  barLeft: number,
  barTop: number,
  viewportHeight: number,
  outputLeft: number,
): { top: number; left: number; width: number; side: "left" | "right" } | null {
  const top = Math.max(16, Math.min(barTop - 8, viewportHeight - 220));
  const rightSpace = outputLeft - (barLeft + 12) - 16;
  if (rightSpace >= PREVIEW_MIN_WIDTH) {
    return { top, left: barLeft + 12, width: Math.min(PREVIEW_MAX_WIDTH, rightSpace), side: "right" };
  }
  const spaceLeft = barLeft - 16;
  if (spaceLeft < PREVIEW_MIN_WIDTH) return null;
  const width = Math.min(PREVIEW_MAX_WIDTH, spaceLeft - 12);
  return { top, left: barLeft - 12 - width, width, side: "left" };
}

/** TurnRail is the slim left-edge navigator: one bar per user input, the turn
 *  the reader is currently at highlighted. Clicking a bar scrolls that turn
 *  into view; hovering or focusing a bar previews the full input. The preview
 *  opens to the LEFT of the rail (over the session list) so it never covers
 *  the output column. It hides below md and for trivial chats (fewer than two
 *  turns), and suppresses the preview when the left gutter is too narrow. */
export const TurnRail = memo(function TurnRail({ turns, activeId, onSelect, className }: {
  turns: RailTurn[];
  activeId: string | null;
  onSelect: (msgId: string) => void;
  className?: string;
}) {
  const closeTimer = useRef<number | null>(null);
  const [preview, setPreview] = useState<RailPreview | null>(null);

  const cancelClose = useCallback(() => {
    if (closeTimer.current !== null) {
      window.clearTimeout(closeTimer.current);
      closeTimer.current = null;
    }
  }, []);

  const scheduleClose = useCallback(() => {
    cancelClose();
    closeTimer.current = window.setTimeout(() => setPreview(null), 140);
  }, [cancelClose]);

  useEffect(() => () => cancelClose(), [cancelClose]);

  // A sidebar toggle re-centres the chat column without firing resize/scroll;
  // close the fixed preview so it cannot linger over the output.
  useEffect(() => {
    const close = () => setPreview(null);
    window.addEventListener("wb-sidebar-toggled", close);
    return () => window.removeEventListener("wb-sidebar-toggled", close);
  }, []);

  // A viewport scroll/resize invalidates the fixed portal coordinates; close
  // instead of leaving the card detached from its bar.
  useEffect(() => {
    if (!preview) return;
    const close = () => setPreview(null);
    window.addEventListener("resize", close);
    window.addEventListener("scroll", close, true);
    return () => {
      window.removeEventListener("resize", close);
      window.removeEventListener("scroll", close, true);
    };
  }, [preview]);

  const openPreview = useCallback((turn: RailTurn, index: number, el: HTMLElement) => {
    cancelClose();
    const barRect = el.getBoundingClientRect();
    const column = document.querySelector<HTMLElement>('[data-slot="message-column"]');
    const geometry = column
      ? railPreviewGeometry(barRect.left, barRect.top, window.innerHeight, column.getBoundingClientRect().left)
      : null;
    if (!geometry) {
      setPreview(null);
      return;
    }
    setPreview({
      turn,
      index,
      ...geometry,
    });
  }, [cancelClose]);

  if (turns.length < 2) return null;
  return (
    <div data-slot="turn-rail" className={cn("flex max-h-[70%] flex-col", className)}>
      <nav
        aria-label="轮次导航"
        className="flex min-h-0 flex-col items-start gap-2.5 overflow-y-auto py-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
      >
        {turns.map((t, i) => (
          <button
            key={t.id}
            type="button"
            aria-label={`第 ${i + 1} 轮：${t.text}`}
            aria-current={t.id === activeId ? "true" : undefined}
            onClick={() => {
              onSelect(t.id);
              setPreview(null);
            }}
            onMouseEnter={(e) => openPreview(t, i, e.currentTarget)}
            onMouseLeave={scheduleClose}
            onFocus={(e) => openPreview(t, i, e.currentTarget)}
            onBlur={scheduleClose}
            className={cn(
              "relative h-[3px] shrink-0 rounded-full transition-all duration-150 after:absolute after:-inset-y-1 after:-left-1 after:-right-1 after:content-['']",
              barWidth(t.text),
              t.id === activeId ? "bg-foreground" : "bg-muted-foreground/30 hover:bg-muted-foreground/60",
            )}
          />
        ))}
      </nav>
      {preview && typeof document !== "undefined" && createPortal(
        <div
          data-slot="turn-rail-preview"
          className={cn(
            "fixed z-[80] animate-in rounded-xl border border-border-strong bg-popover p-3.5 text-popover-foreground shadow-pop fade-in-0 zoom-in-95 duration-150",
            preview.side === "right" ? "origin-left" : "origin-right",
          )}
          style={{ top: preview.top, left: preview.left, width: preview.width }}
          onMouseEnter={cancelClose}
          onMouseLeave={scheduleClose}
          onFocus={cancelClose}
          onBlur={scheduleClose}
        >
          <div className="mb-1.5 flex items-start justify-between gap-2">
            <div className="min-w-0 text-xs font-semibold leading-snug">
              {railPreviewTitle(preview.turn.fullText, preview.index)}
            </div>
            <CopyButton
              text={preview.turn.fullText}
              iconOnly
              title="复制这条输入"
              className="h-6 w-6 shrink-0 justify-center rounded-md border-0 bg-transparent p-0 text-muted-foreground hover:text-foreground"
            />
          </div>
          <div className="max-h-40 overflow-y-auto whitespace-pre-wrap break-words text-xs leading-relaxed text-muted-foreground">
            {preview.turn.fullText}
          </div>
        </div>,
        document.body,
      )}
    </div>
  );
});
