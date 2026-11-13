import type { ModelInfo, ReasoningLevel } from "@/lib/api";

type LabeledModel = { id?: string; displayName?: string } | null | undefined;

/** modelLabel 是模型的人类可读名称。显示名称为空时回退到模型 ID，因此新获取的行绝不渲染为空。 */
export function modelLabel(m: LabeledModel): string {
  return m?.displayName?.trim() || m?.id?.trim() || "";
}

/**
 * modelPickerLabel 为模型行添加供应商，与 Codex 选择器的 "model · provider"
 * 呈现方式一致。供应商仅为显示元数据；请求和持久化选择仍使用模型 ID。
 */
export function modelPickerLabel(m: LabeledModel, providerName?: string): string {
  const name = modelLabel(m);
  const provider = providerName?.trim();
  return name && provider ? `${name} · ${provider}` : name;
}

/** reasoningLevels 返回模型已启用的档位表。禁用或缺失的表会产生 []，从而隐藏聊天级思考选择器。 */
export function reasoningLevels(model: ModelInfo | null | undefined): ReasoningLevel[] {
  const r = model?.reasoning;
  if (!r?.enabled || !r.levels?.length) return [];
  return r.levels;
}

/**
 * composerReasoningModel 解析出为输入区的思考选择器提供数据的
 * ModelInfo。目标模型上已保存的表优先；在档位表存在之前保存的模型，仍会针对其精
 * 确 ID
 * 获取建议。草稿聊天读取首次发送将运行的模型（draftModelFor），因此选择器在会话
 * 行存在之前仍可选择——发送路径已经将每回合档位应用到新创建的会话。
 */
export function composerReasoningModel(input: {
  isDraft: boolean;
  activeModel: ModelInfo | null | undefined;
  draftModel: ModelInfo | null | undefined;
  suggestion: { id: string; info: ModelInfo } | null | undefined;
}): ModelInfo | null {
  const model = input.isDraft ? input.draftModel : input.activeModel;
  if (model?.reasoning) return model;
  if (model?.id && input.suggestion?.id === model.id) return input.suggestion.info;
  return null;
}

/** levelLabel 渲染常见档位名称（如 Codex），自定义名称则按原样显示。 */
export function levelLabel(level: string): string {
  const labels: Record<string, string> = {
    none: "无",
    off: "无",
    minimal: "极低",
    low: "轻度",
    medium: "中",
    high: "高",
    xhigh: "极高",
    "extra-high": "极高",
    max: "Max",
    ultra: "Ultra",
    persistent: "持续",
  };
  const key = level.trim().toLowerCase();
  return labels[key] ?? level;
}

/**
 * reasoningValueError 校验一个档位行。空的 Param 表示该值会作为 JSON
 * 对象合并，因此它必须解析为非数组对象；具名参数接受任意标量或对象。
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
 * validateModelReasoning 返回模型思考表中的第一个问题；当可安全保存时返回
 * null。禁用或为空的表是有效的。
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
