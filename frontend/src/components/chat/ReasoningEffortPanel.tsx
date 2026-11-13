import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { ChevronRight } from "lucide-react";
import { Slider } from "@/components/ui/slider";
import type { ReasoningLevel } from "@/lib/api";
import { levelLabel } from "@/lib/modelLabel";
import { cn } from "@/lib/utils";

type ReasoningEffortPanelProps = {
  levels: ReasoningLevel[];
  value: string;
  modelName?: string;
  onChange: (level: string) => void;
  onValueCommit?: (level: string) => void;
  onOpenModelPicker?: () => void;
  className?: string;
};

/**
 * ReasoningEffortPanel 是 Codex
 * 风格的推理卡片：当前级别作为主标题，模型是打开模型列表的低调一行，各级别组成
 * 一个粗滑块，每个停靠点对应一个圆点。该组件仅负责展示，因此弹出层外壳和持久化
 * 仍留在 ChatPage。
 */
export function ReasoningEffortPanel({
  levels,
  value,
  modelName,
  onChange,
  onValueCommit,
  onOpenModelPicker,
  className,
}: ReasoningEffortPanelProps) {
  if (levels.length === 0) return null;
  const found = levels.findIndex((level) => level.level === value);
  const activeIndex = found >= 0 ? found : 0;
  const maxIndex = levels.length - 1;
  const clampIndex = (index: number) => Math.min(maxIndex, Math.max(0, index));
  const [visualIndex, setVisualIndex] = useState(activeIndex);
  const [dragging, setDragging] = useState(false);
  const emittedIndexRef = useRef(activeIndex);
  const dragStartIndexRef = useRef(activeIndex);

  useEffect(() => {
    emittedIndexRef.current = activeIndex;
    if (!dragging) setVisualIndex(activeIndex);
  }, [activeIndex, dragging]);

  const displayIndex = clampIndex(Math.round(visualIndex));
  const displayLevel = levels[displayIndex];

  const commitIndex = (index: number) => {
    const nextIndex = clampIndex(Math.round(index));
    setVisualIndex(nextIndex);
    setDragging(false);
    const next = levels[nextIndex];
    if (next && nextIndex !== dragStartIndexRef.current) onValueCommit?.(next.level);
  };

  const onSliderKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (levels.length < 2) return;
    let nextIndex = activeIndex;
    switch (event.key) {
      case "ArrowLeft":
      case "ArrowDown":
      case "PageDown":
        nextIndex -= 1;
        break;
      case "ArrowRight":
      case "ArrowUp":
      case "PageUp":
        nextIndex += 1;
        break;
      case "Home":
        nextIndex = 0;
        break;
      case "End":
        nextIndex = maxIndex;
        break;
      default:
        return;
    }
    event.preventDefault();
    event.stopPropagation();
    nextIndex = clampIndex(nextIndex);
    if (nextIndex === activeIndex) return;
    const next = levels[nextIndex];
    if (!next) return;
    emittedIndexRef.current = nextIndex;
    setVisualIndex(nextIndex);
    setDragging(false);
    onChange(next.level);
    onValueCommit?.(next.level);
  };

  return (
    <div data-slot="reasoning-effort-panel" className={cn("p-2", className)}>
      <button
        type="button"
        onClick={onOpenModelPicker}
        disabled={!onOpenModelPicker}
        aria-label={`选择模型，当前 ${levelLabel(displayLevel.level)}${modelName ? `，${modelName}` : ""}`}
        className="mx-auto flex w-full flex-col items-center rounded-lg px-3 py-2 text-center transition-colors enabled:hover:bg-accent-soft disabled:cursor-default"
      >
        <span
          data-slot="reasoning-effort-value"
          aria-live="polite"
          className="text-sm font-semibold text-foreground"
        >
          <span
            key={displayLevel.level}
            className="inline-block animate-in fade-in-0 slide-in-from-bottom-1 duration-150"
          >
            {levelLabel(displayLevel.level)}
          </span>
        </span>
        {modelName && (
          <span className="mt-0.5 flex max-w-full items-center gap-1 text-xs text-muted-foreground">
            <span className="truncate">{modelName}</span>
            {onOpenModelPicker && <ChevronRight className="h-3 w-3 shrink-0" aria-hidden="true" />}
          </span>
        )}
      </button>
      <div
        className="relative mt-1 flex h-8 items-center px-2"
        onKeyDownCapture={onSliderKeyDown}
      >
        <Slider
          min={0}
          max={maxIndex}
          step={0.01}
          value={[visualIndex]}
          disabled={levels.length < 2}
          dragging={dragging}
          onPointerDown={() => {
            dragStartIndexRef.current = emittedIndexRef.current;
            setDragging(true);
          }}
          onPointerUp={() => setDragging(false)}
          onPointerCancel={() => {
            setDragging(false);
            setVisualIndex(activeIndex);
          }}
          onValueChange={([rawIndex]) => {
            setVisualIndex(rawIndex);
            const nearestIndex = clampIndex(Math.round(rawIndex));
            if (nearestIndex !== emittedIndexRef.current) {
              emittedIndexRef.current = nearestIndex;
              const next = levels[nearestIndex];
              if (next) onChange(next.level);
            }
          }}
          onValueCommit={([rawIndex]) => commitIndex(rawIndex)}
          thumbAriaLabel="思考强度"
          thumbAriaValueText={levelLabel(displayLevel.level)}
        />
        <span
          aria-hidden="true"
          className="pointer-events-none absolute inset-x-6 top-1/2 flex -translate-y-1/2 justify-between"
        >
          {levels.map((level, index) => (
            <span
              key={level.level}
              data-slot="reasoning-tick"
              className={cn(
                "size-1 rounded-full transition-colors motion-reduce:transition-none",
                index <= displayIndex ? "bg-primary/75" : "bg-muted-foreground/35",
              )}
            />
          ))}
        </span>
      </div>
    </div>
  );
}
