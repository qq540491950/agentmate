import { memo, useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { tooltipCardClassName } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

export type RailTurn = {
  id: string;
  /** text 是用于条形宽度和无障碍标签的截断片段。 */
  text: string;
  /** fullText 为单行悬停/聚焦标签提供内容。 */
  fullText: string;
};

type RailLabel = {
  turn: RailTurn;
  index: number;
  top: number;
  left: number;
  maxWidth: number;
};

// 条形宽度采用固定的 class 阶梯：动态的 w-* class 会被清除。
const BAR_WIDTHS = ["w-3", "w-3.5", "w-4", "w-5", "w-6", "w-7"] as const;
const EXPANDED_BAR_WIDTH = "w-10";
const EXPANDED_BAR_WIDTH_REM = 2.5;
const LABEL_MAX_WIDTH = 280;

/**
 * railBarWidth 返回条形的静态宽度 class。悬停/聚焦的条形使用固定的 w-10
 * 档位，因此会明显向右延伸；其余每个条形保持按文本长度计算的宽度。
 */
export function railBarWidth(text: string, expanded: boolean): string {
  return expanded ? EXPANDED_BAR_WIDTH : BAR_WIDTHS[Math.min(BAR_WIDTHS.length - 1, Math.floor(text.length / 60))];
}

/**
 * railLabelText
 * 是显示在导航栏条形旁的紧凑单行标签。它优先采用输入内容较短的首行，然后回退为
 * 截断的单行提示词。空输入仍会获得回合编号。
 */
export function railLabelText(text: string, index: number): string {
  const trimmed = text.trim();
  if (!trimmed) return `第 ${index + 1} 轮`;
  const firstLine = trimmed.split(/\r?\n/, 1)[0]?.trim() ?? "";
  const line = firstLine && firstLine.length <= 60 ? firstLine : trimmed;
  return line.length > 60 ? `${line.slice(0, 60)}…` : line;
}

/**
 * railLabelGeometry
 * 将紧凑标签放在其条形正右侧，并在垂直方向与其居中对齐。该标签浮在输出列上方，
 * 而不是预留布局空间，并且会被限制在视口内。
 */
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

/**
 * TurnRail
 * 是左侧细长的导航器：每个用户输入对应一个条形，读者当前所在的回合会高亮显示。
 * 点击某个条形会将该回合滚动到视野内。悬停/聚焦某个条形会使其向右变宽，并在其旁
 * 边显示紧凑的单行标签，该标签浮于输出列之上；离开该条形会恢复其原始宽度。它在
 * md 以下以及简单聊天（少于两个回合）中隐藏。
 */
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

  // 侧边栏开关会重新居中聊天列，而不会触发
  // resize/scroll；关闭固定标签，以免其滞留在输出内容上方。
  useEffect(() => {
    const close = () => {
      setLabel(null);
      setHoveredId(null);
    };
    window.addEventListener("wb-sidebar-toggled", close);
    return () => window.removeEventListener("wb-sidebar-toggled", close);
  }, []);

  // 视口滚动/调整大小会使固定定位的 portal
  // 坐标失效；应关闭标签，而不是让它与对应的条脱节。
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
    // 悬停/聚焦的条会展开到 w-10（2.5rem）。应从根字号读取最终宽度，而不是用
    // barRect.width：由于过渡动画的存在，在 focus/mouse-enter
    // 触发的那一帧，getBoundingClientRect() 仍会返回旧宽度。
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
