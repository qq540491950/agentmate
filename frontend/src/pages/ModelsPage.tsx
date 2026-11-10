import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Plus, Trash2, PlugZap, Star, Loader2, RefreshCw, Image as ImageIcon, Boxes, ChevronRight, Eye, EyeOff } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/EmptyState";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { ModelRowEditor } from "@/components/ModelRowEditor";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
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
import { cn } from "@/lib/utils";
import { PageHeader, PageShell } from "@/components/layout/PageShell";

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

/** Section is the provider editor's grouping primitive: one title row, optional
 *  description and right-aligned actions, then the fields. */
function Section({ title, description, actions, children }: {
  title: string;
  description?: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="grid gap-3">
      <div className="flex items-end justify-between gap-3">
        <div className="min-w-0">
          <h3 className="text-sm font-medium">{title}</h3>
          {description && <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>}
        </div>
        {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
      </div>
      {children}
    </section>
  );
}

export function ModelsPage() {
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [editing, setEditing] = useState<ModelProvider | null>(null);
  const [testing, setTesting] = useState(false);
  const [fetching, setFetching] = useState(false);
  const [saving, setSaving] = useState(false);
  const [openModel, setOpenModel] = useState<number | null>(null);
  const [showKey, setShowKey] = useState(false);
  const [nameError, setNameError] = useState("");
  const [urlError, setUrlError] = useState("");
  const bodyRef = useRef<HTMLDivElement>(null);

  // Reopening the editor should land on the first section, not wherever the
  // previous edit left the scroll position.
  const dialogOpen = !!editing;
  useEffect(() => {
    if (dialogOpen) bodyRef.current?.scrollTo({ top: 0 });
  }, [dialogOpen]);

  const reload = useCallback(() => {
    Config.GetConfig()
      .then((c) => setProviders(c.providers ?? []))
      .catch((e) => toast.error(`加载供应商失败: ${errText(e)}`));
  }, []);
  useEffect(reload, [reload]);

  const openEditor = (p?: ModelProvider) => {
    const item = p ? { ...p, models: (p.models ?? []).map((m) => ({ ...m })) } : blankProvider();
    setEditing(item);
    setOpenModel((item.models ?? []).length > 0 ? 0 : null);
    setShowKey(false);
    setNameError("");
    setUrlError("");
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
    setOpenModel((cur) => (cur === null ? null : cur === index ? null : cur > index ? cur - 1 : cur));
  };

  const addModel = () => {
    setOpenModel((editing?.models ?? []).length);
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
      if (fresh.length > 0 && openModel === null) setOpenModel((editing.models ?? []).length);
      toast.success(fresh.length > 0 ? `拉取成功，新增 ${fresh.length} 个模型` : `拉取成功，无新增模型（共 ${ids.length} 个）`);
    } catch (e) {
      toast.error(`拉取失败: ${errText(e)}，可手动输入`);
    } finally {
      setFetching(false);
    }
  };

  const save = async () => {
    if (!editing) return;
    // Field-level validation stays next to the input instead of a toast: the
    // dialog is long, and a toast at the top of the screen loses the context.
    const baseUrl = (editing.baseUrl ?? "").trim();
    const nextNameError = editing.name.trim() ? "" : "请填写供应商名称";
    const nextUrlError = baseUrl && !/^https?:\/\//.test(baseUrl) ? "需以 http:// 或 https:// 开头" : "";
    setNameError(nextNameError);
    setUrlError(nextUrlError);
    if (nextNameError || nextUrlError) return;
    // Pasted URLs often carry stray whitespace/newlines that make every
    // request fail opaquely.
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
    setSaving(true);
    try {
      await Config.SaveProvider({ ...editing, baseUrl, models });
      toast.success("已保存");
      setEditing(null);
      reload();
    } catch (e) {
      toast.error(`保存失败: ${errText(e)}`);
    } finally {
      setSaving(false);
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
    <PageShell>
      <PageHeader
        title="模型配置"
        description="配置 LLM 供应商与模型，供 Agent 使用"
        actions={
          <Button onClick={() => openEditor()}>
            <Plus className="h-4 w-4" /> 新增供应商
          </Button>
        }
      />

      <div className="grid gap-1.5 rounded-2xl bg-panel/40 p-1.5">
        {providers.length === 0 && (
          <Card>
            <CardContent className="p-8 text-center text-sm text-muted-foreground">
              <EmptyState icon={Boxes} title="还没有配置模型供应商" desc="点击右上角「新增供应商」，配置 OpenAI 兼容 / Anthropic / Gemini 接口。" />
            </CardContent>
          </Card>
        )}
        {providers.map((p) => (
          <div
            key={p.id}
            className="flex items-center gap-3.5 rounded-xl bg-panel/70 px-4 py-3.5"
          >
            <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-accent-soft text-accent-text">
              <Boxes className="h-4 w-4" />
            </span>
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-sm font-medium">{p.name}</span>
                {p.isDefault && (
                  <Badge variant="secondary" className="gap-1">
                    <Star className="h-3 w-3" /> 默认
                  </Badge>
                )}
              </div>
              <div className="mt-0.5 truncate text-xs text-muted-foreground">
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
                {testing ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <PlugZap className="h-3.5 w-3.5" />}
                测试
              </Button>
              <Button variant="outline" size="sm" onClick={() => openEditor(p)}>编辑</Button>
              <Button variant="ghost" size="icon-sm" onClick={() => remove(p)} title="删除供应商" aria-label="删除供应商">
                <Trash2 className="h-4 w-4 text-muted-foreground" />
              </Button>
            </div>
          </div>
        ))}
      </div>

      <Dialog open={!!editing} onOpenChange={(v) => !v && setEditing(null)}>
        <DialogContent className={PROVIDER_EDITOR_DIALOG_CLASS}>
          <DialogHeader className="px-6 py-4">
            <DialogTitle>{editing?.id ? "编辑供应商" : "新增供应商"}</DialogTitle>
            <DialogDescription className="text-xs">
              连接信息决定请求发往哪里；模型列表决定可选用哪些模型及其能力。
            </DialogDescription>
          </DialogHeader>
          {editing && (
            <div className={PROVIDER_EDITOR_BODY_CLASS} ref={bodyRef}>
              <Section title="连接" description="供应商名称、协议与访问凭据">
                <div className="grid gap-3 sm:grid-cols-2">
                  <label className="grid gap-1.5">
                    <span className="text-xs font-medium text-muted-foreground">名称</span>
                    <Input
                      value={editing.name}
                      aria-invalid={!!nameError}
                      onChange={(e) => {
                        setEditing({ ...editing, name: e.target.value });
                        if (nameError) setNameError("");
                      }}
                      onBlur={() => setNameError(editing.name.trim() ? "" : "请填写供应商名称")}
                      placeholder="例如 DeepSeek、OpenAI、本地 Ollama"
                    />
                    {nameError && <span className="text-2xs text-danger-text">{nameError}</span>}
                  </label>
                  <label className="grid gap-1.5">
                    <span className="text-xs font-medium text-muted-foreground">协议</span>
                    <Select
                      value={editing.protocol}
                      onValueChange={(v) => setEditing({ ...editing, protocol: v as Protocol })}
                    >
                      <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                      <SelectContent>
                        {Object.entries(protocolLabels).map(([v, label]) => (
                          <SelectItem key={v} value={v}>{label}</SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </label>
                </div>

                <label className="grid gap-1.5">
                  <span className="text-xs font-medium text-muted-foreground">Base URL</span>
                  <Input
                    value={editing.baseUrl}
                    aria-invalid={!!urlError}
                    onChange={(e) => {
                      setEditing({ ...editing, baseUrl: e.target.value });
                      if (urlError) setUrlError("");
                    }}
                    onBlur={() => {
                      const v = editing.baseUrl.trim();
                      setUrlError(v && !/^https?:\/\//.test(v) ? "需以 http:// 或 https:// 开头" : "");
                    }}
                    placeholder={
                      editing.protocol === Protocol.ProtocolOpenAI
                        ? "https://api.deepseek.com/v1（留空使用 OpenAI 官方）"
                        : editing.protocol === Protocol.ProtocolAnthropic
                          ? "https://api.minimaxi.com/anthropic（留空使用 Anthropic 官方）"
                          : "留空使用 Google 官方"
                    }
                  />
                  {urlError
                    ? <span className="text-2xs text-danger-text">{urlError}</span>
                    : <span className="text-2xs text-muted-foreground">留空则使用该协议的官方地址</span>}
                </label>

                <label className="grid gap-1.5">
                  <span className="text-xs font-medium text-muted-foreground">API Key</span>
                  <div className="relative">
                    <Input
                      type={showKey ? "text" : "password"}
                      value={editing.apiKey}
                      onChange={(e) => setEditing({ ...editing, apiKey: e.target.value })}
                      placeholder="sk-..."
                      className="pr-9"
                    />
                    <button
                      type="button"
                      onClick={() => setShowKey((v) => !v)}
                      className="absolute right-1 top-1/2 -translate-y-1/2 rounded p-1 text-muted-foreground transition-colors hover:text-foreground"
                      title={showKey ? "隐藏 API Key" : "显示 API Key"}
                      aria-label={showKey ? "隐藏 API Key" : "显示 API Key"}
                    >
                      {showKey ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
                    </button>
                  </div>
                </label>

                <div className="flex items-center justify-between rounded-lg bg-panel/60 px-3 py-2.5">
                  <div>
                    <div className="text-sm font-medium">设为默认供应商</div>
                    <div className="text-2xs text-muted-foreground">新会话默认使用该供应商的首个模型</div>
                  </div>
                  <Switch
                    checked={editing.isDefault}
                    onCheckedChange={(v) => setEditing({ ...editing, isDefault: v })}
                  />
                </div>
              </Section>

              <Section
                title={`模型（${(editing.models ?? []).length}）`}
                description="模型能力、价格与思考强度档位"
                actions={
                  <>
                    <Button variant="outline" size="sm" disabled={fetching} onClick={fetchModels}>
                      {fetching
                        ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
                        : <RefreshCw className="h-3.5 w-3.5" />}
                      拉取模型
                    </Button>
                    <Button variant="outline" size="sm" onClick={addModel}>
                      <Plus className="h-3.5 w-3.5" /> 添加
                    </Button>
                  </>
                }
              >
                {(editing.models ?? []).length === 0 ? (
                  <div className="flex flex-col items-center gap-1 rounded-xl bg-panel/50 px-4 py-8 text-center">
                    <Boxes className="h-5 w-5 text-muted-foreground" />
                    <div className="text-sm font-medium">还没有模型</div>
                    <p className="text-xs text-muted-foreground">从供应商接口拉取，或手动添加模型 ID。</p>
                  </div>
                ) : (
                  <div className="grid gap-2">
                    {(editing.models ?? []).map((m, i) => {
                      const open = openModel === i;
                      return (
                        <div
                          key={i}
                          className={cn(
                            "overflow-hidden rounded-xl bg-panel/60 transition-colors",
                            open && "bg-accent-soft",
                          )}
                        >
                          <div className="flex items-center gap-2 px-3 py-2.5">
                            <button
                              type="button"
                              onClick={() => setOpenModel(open ? null : i)}
                              aria-expanded={open}
                              className="flex min-w-0 flex-1 items-center gap-2 text-left"
                            >
                              <ChevronRight className={cn(
                                "h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform",
                                open && "rotate-90",
                              )} />
                              <span className="min-w-0 flex-1">
                                <span className="flex items-center gap-1.5">
                                  <span className="truncate text-sm font-medium">{modelLabel(m) || "未命名模型"}</span>
                                  {m.multimodal && <ImageIcon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
                                </span>
                                <span className="mt-0.5 block truncate font-mono text-2xs text-muted-foreground">
                                  {m.id || "未填写模型 ID"}
                                  {m.contextWindow ? ` · ${(m.contextWindow / 1000).toFixed(0)}K 上下文` : ""}
                                </span>
                              </span>
                            </button>
                            {m.reasoning?.enabled && (
                              <Badge variant="secondary" className="shrink-0 font-normal">
                                {m.reasoning.levels?.length ?? 0} 档
                              </Badge>
                            )}
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              className="shrink-0 text-muted-foreground hover:text-danger-text"
                              onClick={() => removeModel(i)}
                              title="删除模型"
                              aria-label="删除模型"
                            >
                              <Trash2 className="h-3.5 w-3.5" />
                            </Button>
                          </div>
                          {open && (
                            <div className="bg-panel/40 px-3 py-4">
                              <ModelRowEditor
                                model={m}
                                index={i}
                                onSuggest={(force) => { void suggestOne(i, force); }}
                                onChange={(patch) => updateModel(i, patch)}
                              />
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                )}
                <p className="text-xs leading-relaxed text-muted-foreground">
                  显示名称仅影响界面；价格用于成本估算；「图片输入」控制聊天页能否发送图片。
                </p>
              </Section>
            </div>
          )}
          <DialogFooter className="mx-0 mb-0 gap-2 rounded-none bg-transparent px-6 py-4">
            <Button variant="outline" onClick={() => setEditing(null)} disabled={saving}>取消</Button>
            <Button onClick={save} disabled={saving}>
              {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </PageShell>
  );
}
