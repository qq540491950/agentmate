export type NumericRule = {
  min?: number;
  max?: number;
  integer?: boolean;
  decimals?: number;
  allowEmpty?: boolean;
  emptyValue?: number;
};

export type NumericValidation =
  | { valid: true; value: number; empty: boolean }
  | { valid: false; error: string };

/** True while the text is a legal prefix of a number of this rule. */
export function isPartialNumericText(raw: string, rule: NumericRule): boolean {
  if (raw === "") return true;
  return rule.integer !== false ? /^\d*$/.test(raw) : /^\d*(?:\.\d*)?$/.test(raw);
}

export function isCompleteNumericText(raw: string, rule: NumericRule): boolean {
  return rule.integer !== false ? /^\d+$/.test(raw) : /^(?:\d+|\d*\.\d+)$/.test(raw);
}

function rangeLabel(rule: NumericRule): string {
  if (rule.min !== undefined && rule.max !== undefined) return `${rule.min}–${rule.max}`;
  if (rule.min !== undefined) return `≥${rule.min}`;
  if (rule.max !== undefined) return `≤${rule.max}`;
  return "";
}

/** Strict validation used on blur/Enter; partial text is handled by the field. */
export function validateNumericDraft(raw: string, rule: NumericRule): NumericValidation {
  if (raw === "") {
    if (rule.allowEmpty === false) return { valid: false, error: "不能为空" };
    return { valid: true, value: rule.emptyValue ?? 0, empty: true };
  }

  const integer = rule.integer !== false;
  if (!isCompleteNumericText(raw, rule)) {
    return { valid: false, error: integer ? "请输入整数" : "请输入有效数字" };
  }

  const value = Number(raw);
  if (!Number.isFinite(value)) return { valid: false, error: "请输入有效数字" };
  if (integer && !Number.isSafeInteger(value)) return { valid: false, error: "数字超出范围" };

  if (!integer && rule.decimals !== undefined) {
    const fraction = raw.includes(".") ? raw.split(".")[1] ?? "" : "";
    if (fraction.length > rule.decimals) {
      return { valid: false, error: `最多 ${rule.decimals} 位小数` };
    }
  }

  if (rule.min !== undefined && value < rule.min) {
    return { valid: false, error: `请输入${rangeLabel(rule)}之间的${integer ? "整数" : "数字"}` };
  }
  if (rule.max !== undefined && value > rule.max) {
    return { valid: false, error: `请输入${rangeLabel(rule)}之间的${integer ? "整数" : "数字"}` };
  }

  return { valid: true, value, empty: false };
}

/** Formats a stored value into the controlled input's draft text. */
export function numericDraft(value: number | null | undefined): string {
  return value === null || value === undefined || Number.isNaN(value) ? "" : String(value);
}
