import * as React from "react";
import { TriangleAlert } from "lucide-react";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import {
  isCompleteNumericText,
  isPartialNumericText,
  numericDraft,
  validateNumericDraft,
  type NumericRule,
} from "@/lib/numericRules";

export type NumericFieldProps = Omit<
  React.ComponentProps<"input">,
  "value" | "onChange" | "type" | "inputMode"
> & {
  value: number | null | undefined;
  onValueChange: (value: number) => void;
  rule: NumericRule;
  suffix?: string;
  hint?: string;
  onValidityChange?: (valid: boolean) => void;
};

/**
 * Rule-driven numeric field. It deliberately renders a text input instead of
 * type="number": native number inputs expose spinner controls, accept
 * scientific notation and wheel/arrow changes, and ignore min/max for typed
 * values. This component owns the draft while editing and commits only a
 * validated number (or the rule's empty value on blur).
 */
export function NumericField({
  value,
  onValueChange,
  rule,
  suffix,
  hint,
  onValidityChange,
  className,
  id,
  ...props
}: NumericFieldProps) {
  const generatedId = React.useId();
  const inputId = id ?? generatedId;
  const hintId = `${inputId}-hint`;
  const errorId = `${inputId}-error`;
  const [draft, setDraft] = React.useState(() => numericDraft(value));
  const [error, setError] = React.useState<string | null>(null);
  const focused = React.useRef(false);
  const lastReported = React.useRef<boolean | null>(null);
  const validityCallback = React.useRef(onValidityChange);
  validityCallback.current = onValidityChange;

  const reportValidity = React.useCallback(
    (valid: boolean) => {
      if (lastReported.current === valid) return;
      lastReported.current = valid;
      onValidityChange?.(valid);
    },
    [onValidityChange],
  );

  React.useEffect(() => {
    if (focused.current) return;
    const next = numericDraft(value);
    setDraft(next);
    const result = validateNumericDraft(next, rule);
    setError(result.valid ? null : result.error);
    reportValidity(result.valid);
  }, [value]);

  React.useEffect(() => () => {
    validityCallback.current?.(true);
  }, []);

  const handleChange = (event: React.ChangeEvent<HTMLInputElement>) => {
    const raw = event.target.value;
    if (!isPartialNumericText(raw, rule)) {
      setError(rule.integer !== false ? "只能输入整数" : "只能输入数字");
      reportValidity(false);
      return;
    }
    setDraft(raw);
    if (!isCompleteNumericText(raw, rule)) {
      setError(null);
      reportValidity(true);
      return;
    }
    const result = validateNumericDraft(raw, rule);
    if (!result.valid) {
      setError(result.error);
      reportValidity(false);
      return;
    }
    setError(null);
    reportValidity(true);
    onValueChange(result.value);
  };

  const commit = () => {
    let raw = draft.trim();
    if (!rule.integer && raw.endsWith(".")) raw = raw.slice(0, -1);
    const result = validateNumericDraft(raw, rule);
    if (!result.valid) {
      setError(result.error);
      reportValidity(false);
      return;
    }
    setDraft(String(result.value));
    setError(null);
    reportValidity(true);
    onValueChange(result.value);
  };

  return (
    <div className="grid gap-1.5">
      <div className="relative">
        <Input
          {...props}
          id={inputId}
          type="text"
          inputMode={rule.integer !== false ? "numeric" : "decimal"}
          autoComplete="off"
          spellCheck={false}
          value={draft}
          onChange={handleChange}
          onFocus={(event) => {
            focused.current = true;
            props.onFocus?.(event);
          }}
          onBlur={(event) => {
            focused.current = false;
            commit();
            props.onBlur?.(event);
          }}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              commit();
            }
            props.onKeyDown?.(event);
          }}
          aria-invalid={error ? true : undefined}
          aria-describedby={cn(hint && hintId, error && errorId) || undefined}
          className={cn("tabular-nums", suffix && "pr-8", className)}
        />
        {suffix && (
          <span className="pointer-events-none absolute inset-y-0 right-2.5 flex items-center text-xs text-muted-foreground">
            {suffix}
          </span>
        )}
      </div>
      {hint && <p id={hintId} className="text-xs leading-relaxed text-muted-foreground">{hint}</p>}
      {error && (
        <p id={errorId} role="alert" className="flex items-center gap-1 text-xs text-danger-text">
          <TriangleAlert className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
          {error}
        </p>
      )}
    </div>
  );
}
