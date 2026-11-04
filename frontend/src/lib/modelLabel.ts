import type { ModelInfo, ReasoningLevel } from "@/lib/api";

type LabeledModel = { id?: string; displayName?: string } | null | undefined;

/**
 * modelLabel is the human-readable name for a model. A blank display name
 * falls back to the model ID, so a newly fetched row never renders empty.
 */
export function modelLabel(m: LabeledModel): string {
  return m?.displayName?.trim() || m?.id?.trim() || "";
}

/**
 * reasoningLevels returns the model's enabled level table. Disabled or
 * missing tables yield [], which hides the chat-level thinking picker.
 */
export function reasoningLevels(model: ModelInfo | null | undefined): ReasoningLevel[] {
  const r = model?.reasoning;
  if (!r?.enabled || !r.levels?.length) return [];
  return r.levels;
}

/** levelLabel renders the common level names in Chinese, custom ones as-is. */
export function levelLabel(level: string): string {
  const labels: Record<string, string> = {
    off: "关闭",
    low: "低",
    medium: "中",
    high: "高",
    max: "最高",
  };
  const key = level.trim().toLowerCase();
  return labels[key] ?? level;
}

/**
 * reasoningValueError validates one level row. An empty Param means the value
 * is merged as a JSON object, so it must parse as a non-array object; a named
 * parameter accepts any scalar or object.
 */
export function reasoningValueError(level: ReasoningLevel): string | null {
  if (!level.level?.trim()) return "档位名称不能为空";
  if (!level.value?.trim()) return "参数值不能为空";
  if (!level.param?.trim()) {
    try {
      const v = JSON.parse(level.value);
      if (v === null || typeof v !== "object" || Array.isArray(v)) {
        return "参数名为空时，参数值必须是 JSON 对象（如 {\"type\":\"enabled\"}）";
      }
    } catch {
      return "参数名为空时，参数值必须是 JSON 对象（如 {\"type\":\"enabled\"}）";
    }
  }
  return null;
}

/**
 * validateModelReasoning returns the first problem in a model's thinking
 * table, or null when it is safe to save. A disabled or empty table is valid.
 */
export function validateModelReasoning(m: ModelInfo): string | null {
  const r = m.reasoning;
  if (!r?.enabled) return null;
  const levels = r.levels ?? [];
  if (levels.length === 0) return "已开启推理档位，请至少添加一个档位";
  const seen = new Set<string>();
  for (const l of levels) {
    const err = reasoningValueError(l);
    if (err) return err;
    const key = l.level.trim().toLowerCase();
    if (seen.has(key)) return `档位「${l.level.trim()}」重复`;
    seen.add(key);
  }
  const def = r.defaultLevel?.trim();
  if (def && !levels.some((l) => l.level.trim().toLowerCase() === def.toLowerCase())) {
    return `默认档「${def}」不在档位表中`;
  }
  return null;
}