import { Check } from "lucide-react";
import { cn } from "@/lib/utils";
import { THEME_OPTIONS, type ThemePreset } from "@/lib/theme";

function ThemePreview({ preset, dark }: { preset: ThemePreset; dark: boolean }) {
  return (
    <div
      data-theme={preset}
      className={cn("theme-preview overflow-hidden rounded-lg bg-canvas", dark && "dark")}
      aria-hidden="true"
    >
      <div className="flex h-[74px]">
        <div className="w-9 shrink-0 bg-chrome p-1.5">
          <div className="h-1.5 w-5 rounded-full bg-foreground/25" />
          <div className="mt-2 h-1.5 w-6 rounded-full bg-foreground/15" />
          <div className="mt-1 h-1.5 w-5 rounded-full bg-foreground/15" />
          <div className="mt-1 h-1.5 w-4 rounded-full bg-accent-text/70" />
        </div>
        <div className="min-w-0 flex-1 bg-canvas p-2">
          <div className="flex items-center justify-between">
            <div className="h-1.5 w-10 rounded-full bg-foreground/25" />
            <div className="h-2.5 w-2.5 rounded-full bg-brand/70" />
          </div>
          <div className="mt-2 rounded-md bg-panel p-2">
            <div className="h-1.5 w-16 rounded-full bg-foreground/25" />
            <div className="mt-1.5 h-1.5 w-24 max-w-full rounded-full bg-muted-foreground/25" />
            <div className="mt-2 flex items-center gap-1">
              <span className="h-1.5 w-5 rounded-full bg-success/70" />
              <span className="h-1.5 w-7 rounded-full bg-warning/70" />
              <span className="h-1.5 w-4 rounded-full bg-brand/60" />
            </div>
          </div>
          <div className="mt-1.5 flex gap-1">
            <div className="h-2 flex-1 rounded-sm bg-accent-soft" />
            <div className="h-2 w-6 rounded-sm bg-accent-soft" />
          </div>
        </div>
      </div>
    </div>
  );
}

/** ThemeGallery is a real-token preview radio group, not a row of color dots. */
export function ThemeGallery({
  value,
  dark,
  onChange,
}: {
  value: string;
  dark: boolean;
  onChange: (value: ThemePreset) => void;
}) {
  return (
    <div role="radiogroup" aria-label="主题" className="grid gap-2 sm:grid-cols-2">
      {THEME_OPTIONS.map((option) => {
        const selected = value === option.value;
        return (
          <button
            key={option.value}
            type="button"
            role="radio"
            aria-checked={selected}
            onClick={() => onChange(option.value)}
            onKeyDown={(event) => {
              const keys = ["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End"];
              if (!keys.includes(event.key)) return;
              event.preventDefault();
              const radios = Array.from(
                event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="radio"]') ?? [],
              );
              const current = radios.indexOf(event.currentTarget);
              let next = current;
              if (event.key === "Home") next = 0;
              else if (event.key === "End") next = radios.length - 1;
              else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
                next = (current - 1 + radios.length) % radios.length;
              } else {
                next = (current + 1) % radios.length;
              }
              const nextOption = THEME_OPTIONS[next];
              if (nextOption) {
                onChange(nextOption.value);
                radios[next]?.focus();
              }
            }}
            className={cn(
              "group rounded-xl p-2 text-left transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
              selected
                ? "bg-accent-soft"
                : "bg-panel hover:bg-accent",
            )}
          >
            <ThemePreview preset={option.value} dark={dark} />
            <div className="mt-2 flex items-center justify-between gap-2">
              <span className={cn("text-sm font-medium", selected && "text-accent-text")}>
                {option.label}
              </span>
              {selected && <Check className="h-3.5 w-3.5 text-accent-text" aria-hidden="true" />}
            </div>
            <p className="mt-0.5 text-xs leading-relaxed text-muted-foreground">
              {option.description}
            </p>
          </button>
        );
      })}
    </div>
  );
}
