import { Image as ImageIcon, Plus, Trash2, Wand2 } from "lucide-react";
import type { ModelInfo, ReasoningConfig, ReasoningLevel } from "@/lib/api";
import { reasoningValueError } from "@/lib/modelLabel";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";

const COMMON_LEVELS = ["off", "low", "medium", "high", "max"];

function reasoningOf(m: ModelInfo): ReasoningConfig {
  return m.reasoning ?? { enabled: false, defaultLevel: "", levels: [] };
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-1">
      <span className="text-[11px] text-muted-foreground">{label}</span>
      {children}
    </div>
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
  onRemove: () => void;
}

/**
 * ModelRowEditor is one row of the provider editor: identity, limits, input
 * type, prices and the per-model thinking-level table. The table is stored as
 * data only — the request merge lives in the Go adapters.
 */
export function ModelRowEditor({ model, index, onSuggest, onChange, onRemove }: ModelRowEditorProps) {
  const r = reasoningOf(model);
  const levels = r.levels ?? [];
  const listId = `reasoning-levels-${index}`;

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
    <div className="grid gap-2.5 rounded-lg border p-3">
      <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_28px] items-end gap-2">
        <Field label="模型 ID">
          <Input
            value={model.id}
            onChange={(e) => onIdChange(e.target.value)}
            onBlur={() => { if (model.id.trim()) onSuggest(false); }}
            placeholder="model-id"
            className="h-8 font-mono text-xs"
          />
        </Field>
        <Field label="显示名称">
          <Input
            value={model.displayName ?? ""}
            onChange={(e) => onChange({ displayName: e.target.value })}
            placeholder="留空显示模型 ID"
            className="h-8"
          />
        </Field>
        <Button variant="ghost" size="icon" className="h-8 w-7" onClick={onRemove} title="删除模型">
          <Trash2 className="h-3.5 w-3.5 text-muted-foreground" />
        </Button>
      </div>

      <div className="grid grid-cols-2 gap-2">
        <Field label="上下文窗口 (tokens)">
          <Input
            type="number" min="0"
            value={model.contextWindow ?? ""}
            onChange={(e) => onChange({ contextWindow: Math.max(0, Number(e.target.value) || 0) })}
            placeholder="如 128000"
            className="h-8"
          />
        </Field>
        <Field label="最大输出 token">
          <Input
            type="number" min="0"
            value={model.maxOutputTokens ?? ""}
            onChange={(e) => onChange({ maxOutputTokens: Math.max(0, Number(e.target.value) || 0) })}
            placeholder="如 8192（留空用模型默认）"
            className="h-8"
          />
        </Field>
      </div>

      <div className="grid grid-cols-[auto_minmax(0,1fr)_minmax(0,1fr)] items-end gap-2">
        <div className="flex h-8 items-center gap-2 rounded-md border px-2.5">
          <ImageIcon className="h-3.5 w-3.5 text-muted-foreground" />
          <span className="text-xs">图片输入</span>
          <Switch
            checked={!!model.multimodal}
            onCheckedChange={(v) => onChange({ multimodal: v })}
            title="支持图片输入（文本始终可用）"
          />
        </div>
        <Field label="¥/1M 输入">
          <Input
            type="number" step="0.1" min="0"
            value={model.priceIn ?? ""}
            onChange={(e) => onChange({ priceIn: Math.max(0, Number(e.target.value) || 0) })}
            placeholder="可选"
            className="h-8"
          />
        </Field>
        <Field label="¥/1M 输出">
          <Input
            type="number" step="0.1" min="0"
            value={model.priceOut ?? ""}
            onChange={(e) => onChange({ priceOut: Math.max(0, Number(e.target.value) || 0) })}
            placeholder="可选"
            className="h-8"
          />
        </Field>
      </div>

      <div className="grid gap-2 rounded-md border bg-muted/20 p-2.5">
        <div className="flex items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <Switch checked={!!r.enabled} onCheckedChange={toggleReasoning} title="该模型支持调整思考强度" />
            <Label className="text-xs">推理等级</Label>
          </div>
          <div className="flex items-center gap-1.5">
            <Button variant="outline" size="sm" className="h-7 text-xs" onClick={() => onSuggest(true)}>
              <Wand2 className="mr-1 h-3 w-3" /> 自动补全
            </Button>
            <Button variant="outline" size="sm" className="h-7 text-xs" onClick={addLevel} disabled={!r.enabled}>
              <Plus className="mr-1 h-3 w-3" /> 档位
            </Button>
          </div>
        </div>

        {r.enabled && (
          <>
            {levels.length === 0 ? (
              <p className="text-xs text-muted-foreground">暂无档位。点击「自动补全」按模型家族带出默认档位，或手动「档位」添加。</p>
            ) : (
              <div className="grid gap-1.5">
                <div className="grid grid-cols-[110px_minmax(0,1fr)_minmax(0,1.2fr)_28px] gap-2 px-0.5 text-[11px] text-muted-foreground">
                  <span>档位</span>
                  <span>参数名（可留空）</span>
                  <span>参数值</span>
                  <span />
                </div>
                {levels.map((l, i) => {
                  const err = reasoningValueError(l);
                  return (
                    <div key={i} className="grid gap-1">
                      <div className="grid grid-cols-[110px_minmax(0,1fr)_minmax(0,1.2fr)_28px] items-center gap-2">
                        <Input
                          list={listId}
                          value={l.level}
                          onChange={(e) => updateLevel(i, { level: e.target.value })}
                          placeholder="off / low / high"
                          className="h-8"
                        />
                        <Input
                          value={l.param ?? ""}
                          onChange={(e) => updateLevel(i, { param: e.target.value })}
                          placeholder="reasoning_effort"
                          className="h-8 font-mono text-xs"
                        />
                        <Input
                          value={l.value}
                          onChange={(e) => updateLevel(i, { value: e.target.value })}
                          placeholder={l.param?.trim() ? "high / 4096" : `{"type":"enabled"}`}
                          className="h-8 font-mono text-xs"
                        />
                        <Button variant="ghost" size="icon" className="h-8 w-7" onClick={() => removeLevel(i)}>
                          <Trash2 className="h-3.5 w-3.5 text-muted-foreground" />
                        </Button>
                      </div>
                      {err && <p className="pl-0.5 text-[11px] text-destructive">{err}</p>}
                    </div>
                  );
                })}
                <datalist id={listId}>
                  {COMMON_LEVELS.map((v) => <option key={v} value={v} />)}
                </datalist>
              </div>
            )}

            <div className="grid grid-cols-[auto_minmax(0,220px)] items-center gap-2">
              <span className="text-[11px] text-muted-foreground">默认档</span>
              <Select
                value={r.defaultLevel?.trim() || "__first"}
                onValueChange={(v) => updateReasoning({ defaultLevel: v === "__first" ? "" : v })}
                disabled={levels.length === 0}
              >
                <SelectTrigger className="h-8"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="__first">首个档位</SelectItem>
                  {levels.filter((l) => l.level.trim()).map((l) => (
                    <SelectItem key={l.level} value={l.level}>{l.level}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <p className="text-[11px] text-muted-foreground">
              参数名为空时，参数值按 JSON 对象合并到请求体顶层（可一次下发多个字段）；参数名支持点号路径，如 thinking.budget_tokens。
              档位仅在 OpenAI 兼容 / Anthropic 供应商的请求中注入。
            </p>
          </>
        )}
      </div>
    </div>
  );
}