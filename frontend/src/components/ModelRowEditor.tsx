import type { ReactNode } from "react";
import { Image as ImageIcon, Plus, Trash2, Wand2 } from "lucide-react";
import type { ModelInfo, ReasoningConfig, ReasoningLevel } from "@/lib/api";
import { reasoningValueError } from "@/lib/modelLabel";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NumericField } from "@/components/ui/numeric-field";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { TooltipHint } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

const COMMON_LEVELS = ["off", "low", "medium", "high", "max"];

function reasoningOf(m: ModelInfo): ReasoningConfig {
  return m.reasoning ?? { enabled: false, defaultLevel: "", levels: [] };
}

function Field({ label, hint, error, className, children }: {
  label: string;
  hint?: string;
  error?: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <label className={cn("grid content-start gap-1.5", className)}>
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      {children}
      {error ? (
        <span className="text-2xs text-danger-text">{error}</span>
      ) : hint ? (
        <span className="text-2xs text-muted-foreground">{hint}</span>
      ) : null}
    </label>
  );
}

interface ModelRowEditorProps {
  model: ModelInfo;
  index: number;
  /** onSuggest asks the backend for built-in defaults for this model ID.
   * force=true (the button) also replaces an existing table; a blur only
   * fills missing defaults. */
  onSuggest: (force?: boolean) => void;
  onChange: (patch: Partial<ModelInfo>) => void;
  onNumericValidityChange?: (fieldName: string, valid: boolean) => void;
}

/**
 * ModelRowEditor is the expanded body of one model card: identity, limits,
 * input type, prices and the per-model thinking-level table. The table is
 * stored as data only — the request merge lives in the Go adapters.
 */
export function ModelRowEditor({ model, index, onSuggest, onChange, onNumericValidityChange }: ModelRowEditorProps) {
  const r = reasoningOf(model);
  const levels = r.levels ?? [];
  const listId = `reasoning-levels-${index}`;
  const defaultLevel = r.defaultLevel?.trim() || levels[0]?.level || "";

  const updateReasoning = (patch: Partial<ReasoningConfig>) => {
    onChange({ reasoning: { ...r, ...patch } });
  };
  const updateLevel = (i: number, patch: Partial<ReasoningLevel>) => {
    updateReasoning({ levels: levels.map((l, j) => (j === i ? { ...l, ...patch } : l)) });
  };
  const addLevel = () => {
    updateReasoning({ enabled: true, levels: [...levels, { level: "high", param: "reasoning_effort", value: "high" }] });
  };
  const removeLevel = (i: number) => {
    updateReasoning({ levels: levels.filter((_, j) => j !== i) });
  };
  const toggleReasoning = (enabled: boolean) => {
    const next = enabled && levels.length === 0
      ? [{ level: "high", param: "reasoning_effort", value: "high" }]
      : levels;
    updateReasoning({ enabled, levels: next });
  };

  const onIdChange = (id: string) => {
    // Keep the display name in step with the ID until the user customizes it
    // (the same "ID as default label" rule used by 拉取模型).
    const follow = !model.displayName?.trim() || model.displayName.trim() === model.id.trim();
    onChange(follow ? { id, displayName: id } : { id });
  };

  return (
    <div className="grid gap-5">
      {/* Identity and limits */}
      <div className="grid gap-3">
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="模型 ID" hint="请求体中使用，区分大小写">
            <Input
              value={model.id}
              onChange={(e) => onIdChange(e.target.value)}
              onBlur={() => { if (model.id.trim()) onSuggest(false); }}
              placeholder="model-id"
              className="font-mono text-xs"
            />
          </Field>
          <Field label="显示名称" hint="仅界面展示">
            <Input
              value={model.displayName ?? ""}
              onChange={(e) => onChange({ displayName: e.target.value })}
              placeholder="留空显示模型 ID"
            />
          </Field>
        </div>

        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="上下文窗口" hint="tokens，0 表示未知">
            <NumericField
              aria-label="上下文窗口"
              value={model.contextWindow ?? 0}
              rule={{ min: 0, integer: true }}
              onValueChange={(contextWindow) => onChange({ contextWindow })}
              onValidityChange={(valid) => onNumericValidityChange?.("contextWindow", valid)}
              placeholder="如 128000"
            />
          </Field>
          <Field label="最大输出" hint="tokens，留空用模型默认">
            <NumericField
              aria-label="最大输出"
              value={model.maxOutputTokens ?? 0}
              rule={{ min: 0, integer: true }}
              onValueChange={(maxOutputTokens) => onChange({ maxOutputTokens })}
              onValidityChange={(valid) => onNumericValidityChange?.("maxOutputTokens", valid)}
              placeholder="如 8192"
            />
          </Field>
        </div>

        <div className="grid gap-3 sm:grid-cols-3">
          <Field label="输入类型" hint="文本始终可用">
            <div className="flex h-8 items-center justify-between gap-2 rounded-md bg-panel/60 px-2.5">
              <span className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
                <ImageIcon className="h-3.5 w-3.5" /> 图片输入
              </span>
              <TooltipHint content="支持图片输入（文本始终可用）" wrap>
                <Switch
                  checked={!!model.multimodal}
                  onCheckedChange={(v) => onChange({ multimodal: v })}
                />
              </TooltipHint>
            </div>
          </Field>
          <Field label="输入价格" hint="¥ / 1M tokens，可选">
            <NumericField
              aria-label="输入价格"
              value={model.priceIn ?? 0}
              rule={{ min: 0, integer: false, decimals: 2 }}
              onValueChange={(priceIn) => onChange({ priceIn })}
              onValidityChange={(valid) => onNumericValidityChange?.("priceIn", valid)}
              placeholder="0"
            />
          </Field>
          <Field label="输出价格" hint="¥ / 1M tokens，可选">
            <NumericField
              aria-label="输出价格"
              value={model.priceOut ?? 0}
              rule={{ min: 0, integer: false, decimals: 2 }}
              onValueChange={(priceOut) => onChange({ priceOut })}
              onValidityChange={(valid) => onNumericValidityChange?.("priceOut", valid)}
              placeholder="0"
            />
          </Field>
        </div>
      </div>

      {/* Thinking levels */}
      <section
        className="grid gap-3 rounded-xl bg-panel/60 p-3"
        aria-labelledby={`reasoning-title-${index}`}
      >
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex min-w-0 items-center gap-2">
            <TooltipHint content="该模型支持调整思考强度" wrap>
              <Switch
                checked={!!r.enabled}
                onCheckedChange={toggleReasoning}
              />
            </TooltipHint>
            <div className="min-w-0">
              <div id={`reasoning-title-${index}`} className="text-sm font-medium">推理等级</div>
              <div className="truncate text-2xs text-muted-foreground">
                {r.enabled
                  ? `${levels.length} 档${defaultLevel ? ` · 默认 ${defaultLevel}` : ""}`
                  : "未启用，菜单中不显示思考强度"}
              </div>
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-1.5">
            <Button variant="ghost" size="sm" className="text-xs" onClick={() => onSuggest(true)}>
              <Wand2 className="h-3.5 w-3.5" /> 自动补全
            </Button>
            <Button variant="outline" size="sm" className="text-xs" onClick={addLevel} disabled={!r.enabled}>
              <Plus className="h-3.5 w-3.5" /> 添加档位
            </Button>
          </div>
        </div>

        {r.enabled && (
          <>
            {levels.length === 0 ? (
              <p className="rounded-lg bg-panel/50 px-3 py-4 text-center text-xs text-muted-foreground">
                暂无档位。点击「自动补全」按模型家族带出默认档位，或手动添加。
              </p>
            ) : (
              <div className="grid gap-1.5">
                <div className="grid grid-cols-[minmax(80px,0.8fr)_minmax(0,1.1fr)_minmax(0,1.2fr)_28px] gap-2 px-0.5 text-2xs text-muted-foreground">
                  <span>档位</span>
                  <span>参数名（可留空）</span>
                  <span>参数值</span>
                  <span />
                </div>
                {levels.map((l, i) => {
                  const err = reasoningValueError(l);
                  return (
                    <div key={i} className="group/level grid gap-1">
                      <div className="grid grid-cols-[minmax(80px,0.8fr)_minmax(0,1.1fr)_minmax(0,1.2fr)_28px] items-center gap-2">
                        <Input
                          list={listId}
                          value={l.level}
                          onChange={(e) => updateLevel(i, { level: e.target.value })}
                          placeholder="off / low / high"
                          aria-label={`档位 ${i + 1}`}
                        />
                        <Input
                          value={l.param ?? ""}
                          onChange={(e) => updateLevel(i, { param: e.target.value })}
                          placeholder="reasoning_effort"
                          className="font-mono text-xs"
                          aria-label={`档位 ${i + 1} 参数名`}
                        />
                        <Input
                          value={l.value}
                          onChange={(e) => updateLevel(i, { value: e.target.value })}
                          placeholder={l.param?.trim() ? "high / 4096" : `{"type":"enabled"}`}
                          className="font-mono text-xs"
                          aria-label={`档位 ${i + 1} 参数值`}
                        />
                        <TooltipHint content="删除该档位">
                          <Button
                            variant="ghost"
                            size="icon"
                            className="h-8 w-7 text-muted-foreground opacity-40 transition-opacity hover:text-danger-text group-hover/level:opacity-100"
                            onClick={() => removeLevel(i)}
                            aria-label={`删除档位 ${i + 1}`}
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </Button>
                        </TooltipHint>
                      </div>
                      {err && <p className="pl-0.5 text-2xs text-danger-text">{err}</p>}
                    </div>
                  );
                })}
                <datalist id={listId}>
                  {COMMON_LEVELS.map((v) => <option key={v} value={v} />)}
                </datalist>
              </div>
            )}

            <div className="flex flex-wrap items-center justify-between gap-2 bg-panel/40 pt-3">
              <div className="flex items-center gap-2">
                <span className="text-xs font-medium text-muted-foreground">默认档</span>
                <Select
                  value={r.defaultLevel?.trim() || "__first"}
                  onValueChange={(v) => updateReasoning({ defaultLevel: v === "__first" ? "" : v })}
                  disabled={levels.length === 0}
                >
                  <SelectTrigger className="h-8 w-40"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="__first">首个档位</SelectItem>
                    {levels.filter((l) => l.level.trim()).map((l) => (
                      <SelectItem key={l.level} value={l.level}>{l.level}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <TooltipHint content="参数名为空时，参数值按 JSON 对象合并到请求体顶层，可一次下发多个字段；参数名支持点号路径，如 thinking.budget_tokens。档位仅在 OpenAI 兼容 / Anthropic 供应商的请求中注入。">
                <p className="text-2xs leading-relaxed text-muted-foreground">
                  参数名为空时按 JSON 合并到请求体；支持点号路径。
                </p>
              </TooltipHint>
            </div>
          </>
        )}
      </section>
    </div>
  );
}
