import { useCallback, useEffect, useState } from "react";
import { Plus, Trash2, PlugZap, Star, Loader2, RefreshCw, Image as ImageIcon, Boxes } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/EmptyState";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { ModelRowEditor } from "@/components/ModelRowEditor";
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import {
  Config, Protocol, type ModelProvider, type ModelInfo, errText } from "@/lib/api";
import { modelLabel, validateModelReasoning } from "@/lib/modelLabel";
import {
  PROVIDER_EDITOR_BODY_CLASS,
  PROVIDER_EDITOR_DIALOG_CLASS,
} from "@/lib/providerEditorLayout";

const protocolLabels: Record<string, string> = {
  [Protocol.ProtocolOpenAI]: "OpenAI 兼容 (DeepSeek/Kimi/Ollama...)",
  [Protocol.ProtocolAnthropic]: "Anthropic (Claude)",
  [Protocol.ProtocolGemini]: "Google Gemini",
};

function blankProvider(): ModelProvider {
  return {
    id: "",
    name: "",
    protocol: Protocol.ProtocolOpenAI,
    baseUrl: "",
    apiKey: "",
    models: [],
    isDefault: false,
  };
}

export function ModelsPage() {
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [editing, setEditing] = useState<ModelProvider | null>(null);
  const [testing, setTesting] = useState(false);
  const [fetching, setFetching] = useState(false);

  const reload = useCallback(() => {
    Config.GetConfig()
      .then((c) => setProviders(c.providers ?? []))
      .catch((e) => toast.error(`加载供应商失败: ${errText(e)}`));
  }, []);
  useEffect(reload, [reload]);

  const openEditor = (p?: ModelProvider) => {
    const item = p ? { ...p, models: (p.models ?? []).map((m) => ({ ...m })) } : blankProvider();
    setEditing(item);
  };

  /** updateModel patches one row of the model list being edited. */
  const updateModel = (index: number, patch: Partial<ModelInfo>) => {
    setEditing((prev) => prev ? {
      ...prev,
      models: (prev.models ?? []).map((m, i) => (i === index ? { ...m, ...patch } : m)),
    } : prev);
  };

  const removeModel = (index: number) => {
    setEditing((prev) => prev ? {
      ...prev,
      models: (prev.models ?? []).filter((_, i) => i !== index),
    } : prev);
  };

  const addModel = () => {
    setEditing((prev) => prev ? {
      ...prev,
      models: [...(prev.models ?? []), { id: "", displayName: "" }],
    } : prev);
  };

  /** suggest fetches built-in defaults (display name + level table) for IDs. */
  const suggest = async (ids: string[]): Promise<Map<string, ModelInfo>> => {
    const unique = [...new Set(ids.map((id) => id.trim()).filter(Boolean))];
    if (unique.length === 0) return new Map();
    try {
      const list = (await Config.SuggestModels(unique)) ?? [];
      return new Map(list.map((m) => [m.id, m]));
    } catch {
      return new Map(); // suggestions are a convenience; manual input stays valid
    }
  };

  /** mergeSuggestion only fills fields the user has not customized. */
  const mergeSuggestion = (cur: ModelInfo, sug: ModelInfo, force: boolean): ModelInfo => {
    const next = { ...cur };
    if (!cur.displayName?.trim() || (force && cur.displayName.trim() === cur.id.trim())) {
      next.displayName = sug.displayName || sug.id;
    }
    if (sug.reasoning && (force || !cur.reasoning || !(cur.reasoning.levels?.length))) {
      next.reasoning = sug.reasoning;
    }
    return next;
  };

  const suggestOne = async (index: number, force = false) => {
    const cur = editing?.models?.[index];
    if (!cur?.id.trim()) return;
    // A blur must never undo a table the user already built or disabled.
    if (!force && (cur.reasoning?.levels?.length ?? 0) > 0) return;
    const suggestions = await suggest([cur.id]);
    const sug = suggestions.get(cur.id.trim());
    if (!sug) return;
    setEditing((prev) => prev ? {
      ...prev,
      models: (prev.models ?? []).map((m, i) => (i === index ? mergeSuggestion(m, sug, force) : m)),
    } : prev);
  };

  const fetchModels = async () => {
    if (!editing) return;
    setFetching(true);
    try {
      const ids = (await Config.ListProviderModels(editing)) ?? [];
      // Compute from the closure value, not inside the state updater: React
      // may defer the updater past the toast below.
      const have = new Set((editing.models ?? []).map((m) => m.id));
      const freshIds = ids.filter((id) => !have.has(id));
      const suggestions = await suggest(freshIds);
      const fresh = freshIds.map((id): ModelInfo => {
        const sug = suggestions.get(id);
        return sug ? { ...sug, id, displayName: sug.displayName || id } : { id, displayName: id };
      });
      setEditing((prev) => (prev ? { ...prev, models: [...(prev.models ?? []), ...fresh] } : prev));
      toast.success(fresh.length > 0 ? `拉取成功，新增 ${fresh.length} 个模型` : `拉取成功，无新增模型（共 ${ids.length} 个）`);
    } catch (e) {
      toast.error(`拉取失败: ${errText(e)}，可手动输入`);
    } finally {
      setFetching(false);
    }
  };

  const save = async () => {
    if (!editing) return;
    if (!editing.name.trim()) {
      toast.error("请填写供应商名称");
      return;
    }
    // Pasted URLs often carry stray whitespace/newlines that make every
    // request fail opaquely.
    const baseUrl = (editing.baseUrl ?? "").trim();
    if (baseUrl && !/^https?:\/\//.test(baseUrl)) {
      toast.error("Base URL 需以 http:// 或 https:// 开头");
      return;
    }
    // Trim ids and drop empty / duplicate rows. Normalize the thinking table so
    // the Go resolver (exact level match, dotted parameter names) sees clean
    // values, and reject a table that would silently send nothing.
    const models: ModelInfo[] = [];
    for (const m of editing.models ?? []) {
      const id = m.id.trim();
      if (!id || models.some((x) => x.id === id)) continue;
      const normalized: ModelInfo = {
        ...m,
        id,
        displayName: (m.displayName ?? "").trim(),
        reasoning: m.reasoning ? {
          ...m.reasoning,
          defaultLevel: (m.reasoning.defaultLevel ?? "").trim(),
          levels: (m.reasoning.levels ?? []).map((l) => ({
            ...l,
            level: l.level.trim(),
            param: (l.param ?? "").trim(),
          })),
        } : undefined,
      };
      const reasoningErr = validateModelReasoning(normalized);
      if (reasoningErr) {
        toast.error(`模型「${modelLabel(normalized)}」：${reasoningErr}`);
        return;
      }
      models.push(normalized);
    }
    if (models.length === 0) {
      toast.error("请至少填写一个模型 ID");
      return;
    }
    try {
      await Config.SaveProvider({ ...editing, baseUrl, models });
      toast.success("已保存");
      setEditing(null);
      reload();
    } catch (e) {
      toast.error(`保存失败: ${errText(e)}`);
    }
  };

  const remove = async (p: ModelProvider) => {
    if (!window.confirm(`确定删除供应商「${p.name}」？`)) return;
    try {
      await Config.DeleteProvider(p.id);
      toast.success(`已删除 ${p.name}`);
    } catch (e) {
      toast.error(`删除失败: ${errText(e)}`);
    }
    reload();
  };

  const test = async (p: ModelProvider) => {
    const first = (p.models ?? [])[0];
    const model = first?.id;
    if (!model) {
      toast.error("请先在供应商里配置至少一个模型 ID");
      return;
    }
    setTesting(true);
    try {
      const ms = await Config.TestProvider(p, model);
      toast.success(`连接成功 (${modelLabel(first)}) · ${ms}ms`);
    } catch (e) {
      toast.error(`连接失败: ${errText(e)}`);
    } finally {
      setTesting(false);
    }
  };

  return (
    <div className="h-full overflow-y-auto p-6">
      <div className="mb-4 flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold">模型配置</h1>
          <p className="text-sm text-muted-foreground">配置 LLM 供应商与模型，供 Agent 使用</p>
        </div>
        <Button onClick={() => openEditor()}>
          <Plus className="mr-1 h-4 w-4" /> 新增供应商
        </Button>
      </div>

      <div className="grid gap-3">
        {providers.length === 0 && (
          <Card>
            <CardContent className="p-8 text-center text-sm text-muted-foreground">
              <EmptyState icon={Boxes} title="还没有配置模型供应商" desc="点击右上角「新增供应商」，配置 OpenAI 兼容 / Anthropic / Gemini 接口。" />
            </CardContent>
          </Card>
        )}
        {providers.map((p) => (
          <Card key={p.id}>
            <CardContent className="flex items-center justify-between p-4">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className="font-medium">{p.name}</span>
                  {p.isDefault && (
                    <Badge variant="secondary" className="gap-1">
                      <Star className="h-3 w-3" /> 默认
                    </Badge>
                  )}
                </div>
                <div className="mt-1 truncate text-xs text-muted-foreground">
                  {protocolLabels[p.protocol] ?? p.protocol}
                  {p.baseUrl && ` · ${p.baseUrl}`}
                </div>
                <div className="mt-1.5 flex flex-wrap gap-1">
                  {(p.models ?? []).map((m) => (
                    <Badge
                      key={m.id}
                      variant="outline"
                      className="gap-1 font-normal"
                      title={[m.id, m.multimodal ? "多模态" : "", m.contextWindow ? `上下文 ${m.contextWindow}` : ""]
                        .filter(Boolean).join(" · ")}
                    >
                      {m.multimodal && <ImageIcon className="h-3 w-3 text-muted-foreground" />}
                      {modelLabel(m)}
                    </Badge>
                  ))}
                </div>
              </div>
              <div className="flex shrink-0 items-center gap-2">
                <Button variant="outline" size="sm" disabled={testing} onClick={() => test(p)}>
                  {testing ? <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" /> : <PlugZap className="mr-1 h-3.5 w-3.5" />}
                  测试
                </Button>
                <Button variant="outline" size="sm" onClick={() => openEditor(p)}>编辑</Button>
                <Button variant="ghost" size="icon" onClick={() => remove(p)}>
                  <Trash2 className="h-4 w-4 text-muted-foreground" />
                </Button>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>

      <Dialog open={!!editing} onOpenChange={(v) => !v && setEditing(null)}>
        <DialogContent className={PROVIDER_EDITOR_DIALOG_CLASS}>
          <DialogHeader>
            <DialogTitle>{editing?.id ? "编辑供应商" : "新增供应商"}</DialogTitle>
          </DialogHeader>
          {editing && (
            <div className={PROVIDER_EDITOR_BODY_CLASS}>
              <div className="grid gap-1.5">
                <Label>名称</Label>
                <Input
                  value={editing.name}
                  onChange={(e) => setEditing({ ...editing, name: e.target.value })}
                  placeholder="例如 DeepSeek、OpenAI、本地 Ollama"
                />
              </div>
              <div className="grid gap-1.5">
                <Label>协议</Label>
                <Select
                  value={editing.protocol}
                  onValueChange={(v) => setEditing({ ...editing, protocol: v as Protocol })}
                >
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {Object.entries(protocolLabels).map(([v, label]) => (
                      <SelectItem key={v} value={v}>{label}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="grid gap-1.5">
                <Label>Base URL</Label>
                <Input
                  value={editing.baseUrl}
                  onChange={(e) => setEditing({ ...editing, baseUrl: e.target.value })}
                  placeholder={
                    editing.protocol === Protocol.ProtocolOpenAI
                      ? "https://api.deepseek.com/v1（留空使用 OpenAI 官方）"
                      : editing.protocol === Protocol.ProtocolAnthropic
                        ? "https://api.minimaxi.com/anthropic（留空使用 Anthropic 官方）"
                        : "留空使用 Google 官方"
                  }
                />
              </div>
              <div className="grid gap-1.5">
                <Label>API Key</Label>
                <Input
                  type="password"
                  value={editing.apiKey}
                  onChange={(e) => setEditing({ ...editing, apiKey: e.target.value })}
                  placeholder="sk-..."
                />
              </div>

              <div className="grid gap-2">
                <div className="flex items-center justify-between">
                  <Label>模型列表</Label>
                  <div className="flex gap-2">
                    <Button variant="outline" size="sm" disabled={fetching} onClick={fetchModels}>
                      {fetching
                        ? <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" />
                        : <RefreshCw className="mr-1 h-3.5 w-3.5" />}
                      拉取模型
                    </Button>
                    <Button variant="outline" size="sm" onClick={addModel}>
                      <Plus className="mr-1 h-3.5 w-3.5" /> 添加
                    </Button>
                  </div>
                </div>
                {(editing.models ?? []).length === 0 ? (
                  <p className="rounded-lg border border-dashed p-3 text-center text-xs text-muted-foreground">
                    暂无模型。点击「拉取模型」从供应商接口获取，或「添加」手动输入。
                  </p>
                ) : (
                  <div className="grid gap-2">
                    {(editing.models ?? []).map((m, i) => (
                      <ModelRowEditor
                        key={i}
                        model={m}
                        index={i}
                        onSuggest={(force) => { void suggestOne(i, force); }}
                        onChange={(patch) => updateModel(i, patch)}
                        onRemove={() => removeModel(i)}
                      />
                    ))}
                  </div>
                )}
                <p className="text-xs text-muted-foreground">
                  显示名称只影响界面展示，请求仍使用模型 ID；填写价格后对话中会显示成本估算。
                  「图片输入」决定聊天页是否允许发送图片；「推理等级」的档位表会随请求下发（OpenAI 兼容 / Anthropic）。
                </p>
              </div>

              <div className="flex items-center justify-between rounded-lg border p-3">
                <Label>设为默认供应商</Label>
                <Switch
                  checked={editing.isDefault}
                  onCheckedChange={(v) => setEditing({ ...editing, isDefault: v })}
                />
              </div>
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditing(null)}>取消</Button>
            <Button onClick={save}>保存</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
