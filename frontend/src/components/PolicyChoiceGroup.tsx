import type { KeyboardEvent as ReactKeyboardEvent } from "react";
import { Check, type LucideIcon } from "lucide-react";
import { choiceCardClass } from "@/components/ui/choice";
import { cn } from "@/lib/utils";

export type PolicyChoice<T extends string> = {
  value: T;
  label: string;
  description: string;
  danger?: boolean;
};

export function PolicyChoiceGroup<T extends string>({
  label,
  description,
  options,
  icons,
  value,
  columnsClass,
  onSelect,
}: {
  label: string;
  description: string;
  options: readonly PolicyChoice<T>[];
  icons: Record<T, LucideIcon>;
  value: T | null;
  columnsClass: string;
  onSelect: (value: T) => void;
}) {
  const onKeyDown = (event: ReactKeyboardEvent<HTMLButtonElement>) => {
    const keys = ["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End"];
    if (!keys.includes(event.key)) return;
    event.preventDefault();
    const radios = Array.from(
      event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="radio"]') ?? [],
    );
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
    const nextOption = options[next];
    if (nextOption) {
      onSelect(nextOption.value);
      radios[next]?.focus();
    }
  };

  return (
    <div className="grid gap-2">
      <div>
        <div className="text-sm font-medium">{label}</div>
        <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{description}</p>
      </div>
      <div role="radiogroup" aria-label={label} className={cn("grid gap-2", columnsClass)}>
        {options.map((option) => {
          const selected = value === option.value;
          const Icon = icons[option.value] as LucideIcon;
          return (
            <button
              key={option.value}
              type="button"
              role="radio"
              aria-checked={selected}
              onClick={() => onSelect(option.value)}
              onKeyDown={onKeyDown}
              className={choiceCardClass(selected, option.danger)}
            >
              <div className="flex items-center justify-between gap-2">
                <Icon
                  aria-hidden="true"
                  className={cn(
                    "h-4 w-4",
                    option.danger
                      ? "text-danger-text"
                      : selected ? "text-accent-text" : "text-muted-foreground",
                  )}
                />
                {selected && (
                  <Check
                    aria-hidden="true"
                    className={cn("h-3.5 w-3.5", option.danger ? "text-danger-text" : "text-accent-text")}
                  />
                )}
              </div>
              <div>
                <span className={cn("text-sm font-medium", selected && "text-foreground")}>
                  {option.label}
                </span>
                <p className={cn("mt-1 text-xs leading-relaxed", selected ? "text-foreground" : "text-muted-foreground")}>
                  {option.description}
                </p>
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
}
