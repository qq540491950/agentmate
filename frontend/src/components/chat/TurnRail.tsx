import { memo } from "react";
import { cn } from "@/lib/utils";

export type RailTurn = {
  id: string;
  /** text is the truncated input snippet shown in the hover tooltip. */
  text: string;
};

// Bar widths are a fixed class ladder: dynamic w-* classes would be purged.
const BAR_WIDTHS = ["w-3", "w-3.5", "w-4", "w-5", "w-6", "w-7"] as const;

function barWidth(text: string): string {
  return BAR_WIDTHS[Math.min(BAR_WIDTHS.length - 1, Math.floor(text.length / 60))];
}

/** TurnRail is the slim left-edge navigator: one bar per user input, the turn
 *  the reader is currently at highlighted. Clicking a bar scrolls that turn
 *  into view. It hides below md and for trivial chats (fewer than two turns)
 *  where there is nothing to jump between. */
export const TurnRail = memo(function TurnRail({ turns, activeId, onSelect }: {
  turns: RailTurn[];
  activeId: string | null;
  onSelect: (msgId: string) => void;
}) {
  if (turns.length < 2) return null;
  return (
    <nav
      aria-label="轮次导航"
      className="absolute left-2 top-1/2 z-10 hidden max-h-[70%] -translate-y-1/2 flex-col items-start gap-2.5 overflow-y-auto py-1 [scrollbar-width:none] md:flex [&::-webkit-scrollbar]:hidden"
    >
      {turns.map((t, i) => (
        <button
          key={t.id}
          type="button"
          title={t.text}
          aria-label={`第 ${i + 1} 轮：${t.text}`}
          aria-current={t.id === activeId ? "true" : undefined}
          onClick={() => onSelect(t.id)}
          className={cn(
            "h-[3px] shrink-0 rounded-full transition-all duration-150",
            barWidth(t.text),
            t.id === activeId ? "bg-foreground" : "bg-muted-foreground/30 hover:bg-muted-foreground/60",
          )}
        />
      ))}
    </nav>
  );
});
