import { Check } from "lucide-react";
import { cn } from "@/lib/utils";

export type ChoiceOption<T extends string> = {
  value: T;
  label: string;
  disabled?: boolean;
};

export function SegmentedControl<T extends string>({
  value,
  options,
  onChange,
  ariaLabel,
  className,
}: {
  value: T;
  options: ReadonlyArray<ChoiceOption<T>>;
  onChange: (value: T) => void;
  ariaLabel: string;
  className?: string;
}) {
  return (
    <div role="radiogroup" aria-label={ariaLabel} className={cn("inline-flex flex-wrap gap-1", className)}>
      {options.map((option) => {
        const selected = value === option.value;
        return (
          <button
            key={option.value}
            type="button"
            role="radio"
            aria-checked={selected}
            disabled={option.disabled}
            onClick={() => onChange(option.value)}
            onKeyDown={(event) => {
              const keys = ["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End"];
              if (!keys.includes(event.key)) return;
              event.preventDefault();
              const radios = Array.from(
                event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="radio"]') ?? [],
              ).filter((radio) => !radio.disabled);
              const current = radios.indexOf(event.currentTarget);
              if (current < 0) return;
              let next = current;
              if (event.key === "Home") next = 0;
              else if (event.key === "End") next = radios.length - 1;
              else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
                next = (current - 1 + radios.length) % radios.length;
              } else {
                next = (current + 1) % radios.length;
              }
              const nextOption = options.find((entry) => entry.value === radios[next]?.dataset.value);
              if (nextOption) {
                onChange(nextOption.value);
                radios[next]?.focus();
              }
            }}
            data-value={option.value}
            className={cn(
              "inline-flex h-8 min-w-16 items-center justify-center gap-1.5 rounded-lg px-3 text-[13px] transition-colors outline-none",
              "focus-visible:ring-2 focus-visible:ring-ring/60 disabled:pointer-events-none disabled:opacity-50",
              selected
                ? "bg-choice-selected font-medium text-foreground"
                : "bg-control text-muted-foreground hover:bg-overlay hover:text-foreground",
            )}
          >
            {selected && <Check className="h-3.5 w-3.5 text-accent-text" aria-hidden="true" />}
            {option.label}
          </button>
        );
      })}
    </div>
  );
}

export function choiceCardClass(selected: boolean, danger = false): string {
  return cn(
    "group flex flex-col gap-3 rounded-xl p-3 text-left transition-colors outline-none",
    "focus-visible:ring-2 focus-visible:ring-ring/60",
    selected
      ? danger
        ? "bg-choice-selected-danger"
        : "bg-choice-selected"
      : "bg-control hover:bg-overlay",
  );
}
