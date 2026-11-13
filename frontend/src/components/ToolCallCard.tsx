import { memo, useEffect, useMemo, useState } from "react";
import {
  CheckCircle2, ChevronRight, FileImage, FileText, Loader2, XCircle,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { TooltipHint } from "@/components/ui/tooltip";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { CopyButton, MessageMarkdown } from "@/components/MessageMarkdown";
import { DiffView } from "@/components/TaskWorkspacePanel";
import { highlightText } from "@/components/chat/highlightText";
import { toolResultFailed, toolStepDisplay } from "@/lib/chatUtils";
import { Artifact, errText, type Artifact as ArtifactData } from "@/lib/api";
import { cn } from "@/lib/utils";

/** extractArtifactPaths 在工具参数/结果中查找相对于工作区的文件引用，以便预览它们的输出。 */
export function extractArtifactPaths(...objs: (unknown | null | undefined)[]): string[] {
  const out = new Set<string>();
  const re = /"(?:path|file|file_path|filename|image_path|artifact|output|refs)"\s*:\s*"([^"]+\.(?:png|jpe?g|gif|webp|svg|bmp|ico|pdf|md|markdown|txt|log|csv|json|ya?ml|xml|html?|go|ts|tsx|js|py|rs|java|sh|sql|toml|ini|css))"/gi;
  for (const o of objs) {
    if (o == null) continue;
    try {
      for (const m of JSON.stringify(o).matchAll(re)) out.add(m[1]);
    } catch { /* ignore */ }
  }
  return [...out];
}

type ToolCallProps = {
  name: string;
  sessionID?: string;
  args?: { [k: string]: any } | null;
  resp?: { [k: string]: any } | null;
  running: boolean;
  durationMs?: number;
  filterQ?: string;
  /** compact 渲染一条轻量级执行行，而不是一张卡片。 */
  compact?: boolean;
};

/**
 * formatDuration 渲染工具的运行时长。HITL
 * 确认可能待处理数天，因此长时间等待会使用粗粒度单位，而不是 "2804084.6s"。
 */
export function formatDuration(ms: number): string {
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(1)}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m${String(Math.round(s % 60)).padStart(2, "0")}s`;
  const h = Math.floor(s / 3600);
  const m = Math.round((s % 3600) / 60);
  return `${h}h${String(m).padStart(2, "0")}m`;
}

/**
 * ToolCallCard 渲染单次工具调用，并带有实时状态可视化。之所以使用 memo
 * 缓存，是因为 extractArtifactPaths 会对 args/results 执行 JSON
 * 字符串化，否则每流式输出一个 token 时，它都会对每个可见工具运行一次。
 */
export const ToolCallCard = memo(function ToolCallCard({ name, sessionID, args, resp, running, durationMs, filterQ, compact = false }: ToolCallProps) {
  const [open, setOpen] = useState(false);
  // 检查解析后的 payload：子字符串匹配曾用于标记仅带有 "error": null
  // 字段的成功信封，并漏掉了 MCP 的 camelCase isError。
  const failed = useMemo(() => toolResultFailed(resp), [resp]);

  const display = useMemo(() => toolStepDisplay(name, args), [name, args]);

  const argChips = useMemo(() => {
    if (compact || !args) return [];
    return Object.entries(args).slice(0, 4).map(([k, v]) => {
      let text = typeof v === "string" ? v : JSON.stringify(v);
      if (text.length > 42) text = text.slice(0, 42) + "…";
      return { k, text };
    });
  }, [args, compact]);

  const paths = useMemo(
    () => extractArtifactPaths(args, resp),
    [args, resp],
  );
  const inputJSON = useMemo(() => (args == null ? "" : JSON.stringify(args, null, 2)), [args]);
  const outputJSON = useMemo(() => (resp == null ? "" : JSON.stringify(resp, null, 2)), [resp]);
  const showInput = args != null;
  const showOutput = resp != null;
  // 读取/列出工具的输入路径不是输出产物。在紧凑执行列表中，仅
  // write/search/unknown
  // 工具保留预览条；否则每个读取行都会多出一行，运行记录会显得杂乱。
  const showPaths = paths.length > 0 && (
    !compact || display.kind === "write" || display.kind === "search" || display.kind === "other"
  );

  return (
    <div
      className={cn(
        "text-sm transition-colors",
        compact ? "rounded-lg" : "rounded-xl bg-panel/60",
      )}
      data-toolcard={name}
    >
      <TooltipHint content={compact ? `${display.action}${display.target ? ` ${display.target}` : ""}` : ""}>
        <button
          className={cn(
            "flex w-full items-center gap-2 text-left",
            compact
              ? "rounded-lg px-2 py-1.5 transition-colors hover:bg-accent-soft focus-visible:bg-accent-soft"
              : "px-3 py-2.5",
          )}
          aria-label={`${running ? "正在运行" : failed ? "失败" : "完成"}：${display.action}${display.target ? ` ${display.target}` : ""}`}
          onClick={() => setOpen((v) => !v)}
        >
        <span className="shrink-0">
          {running ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin text-accent-text" />
          ) : failed ? (
            <XCircle className="h-3.5 w-3.5 text-danger-text" />
          ) : (
            <CheckCircle2 className="h-3.5 w-3.5 text-success" />
          )}
        </span>
        {compact ? (
          <>
            {failed && <span className="shrink-0 text-xs text-danger-text">失败</span>}
            <span className="flex min-w-0 flex-1 items-center gap-1.5">
              <span className="shrink-0 text-xs text-muted-foreground">{display.action}</span>
              {display.target && (
                <span
                  className={cn(
                    "min-w-0 truncate text-xs text-foreground",
                    display.mono && "font-mono",
                  )}
                >
                  {display.target}
                </span>
              )}
            </span>
            {durationMs != null && (
              <span className="shrink-0 text-xs text-muted-foreground">{formatDuration(durationMs)}</span>
            )}
          </>
        ) : (
          <>
            <Badge variant="outline" className="shrink-0 font-normal">
              {running ? "调用中" : failed ? "失败" : "完成"}
            </Badge>
            <span className="font-mono text-xs">{name}</span>
            {durationMs != null && (
              <span className="shrink-0 text-xs text-muted-foreground">{formatDuration(durationMs)}</span>
            )}
            <span className="hidden min-w-0 flex-1 items-center gap-1 overflow-hidden sm:flex">
              {argChips.map(({ k, text }) => (
                <TooltipHint key={k} content={`${k}: ${text}`}>
                  <span className="truncate rounded bg-control px-1.5 py-0.5 text-xs text-muted-foreground">
                    {text}
                  </span>
                </TooltipHint>
              ))}
            </span>
          </>
        )}
        <ChevronRight className={`ml-auto h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`} />
        </button>
      </TooltipHint>
      {open && (
        <div className={cn(
          "space-y-2",
          compact ? "mt-1 rounded-md bg-control/70 p-2" : "bg-panel/40 px-3 py-2",
        )}>
          {showInput && (
            <div>
              <div className="mb-1 flex items-center justify-between gap-2">
                <span className="text-xs font-medium text-muted-foreground">输入</span>
                <CopyButton
                  text={inputJSON}
                  label="复制输入参数"
                  className="border-0 bg-transparent px-1 py-0 text-3xs opacity-60 hover:opacity-100"
                />
              </div>
              <pre className="max-h-44 overflow-auto rounded bg-control px-2 py-1.5 font-mono text-xs leading-[1.7] text-muted-foreground">
                {highlightText(inputJSON, filterQ ?? "", "bg-accent-soft text-foreground rounded-sm")}
              </pre>
            </div>
          )}
          {showOutput && (
            <div>
              <div className="mb-1 flex items-center justify-between gap-2">
                <span className="text-xs font-medium text-muted-foreground">输出</span>
                <CopyButton
                  text={outputJSON}
                  label="复制输出结果"
                  className="border-0 bg-transparent px-1 py-0 text-3xs opacity-60 hover:opacity-100"
                />
              </div>
              <pre className="max-h-44 overflow-auto rounded bg-control px-2 py-1.5 font-mono text-xs leading-[1.7] text-muted-foreground">
                {highlightText(outputJSON, filterQ ?? "", "bg-accent-soft text-foreground rounded-sm")}
              </pre>
            </div>
          )}
          {!showInput && !showOutput && (
            <div className="px-1 text-xs text-muted-foreground">暂无详细内容</div>
          )}
        </div>
      )}
      {showPaths && (
        <div className={cn("flex flex-wrap items-center gap-1.5", compact ? "px-2 pb-1.5 pt-0.5" : "bg-panel/40 px-3 py-2")}>
          <span className="text-xs text-muted-foreground">产物：</span>
          {paths.map((p) => (
            <ArtifactOpenButton
              key={p}
              path={p}
              sessionID={name === "search_knowledge" ? undefined : sessionID}
            />
          ))}
        </div>
      )}
    </div>
  );
});

export function ArtifactOpenButton({ path, label, sessionID }: { path: string; label?: string; sessionID?: string }) {
  const [target, setTarget] = useState<string | null>(null);
  return (
    <>
      <Button
        variant="outline"
        size="sm"
        className="h-6 gap-1 px-2 text-xs"
        onClick={() => setTarget(path)}
      >
        <FileImage className="h-3 w-3" />
        {label ?? "预览"} {path.split("/").pop()}
      </Button>
      <ArtifactPreview path={target} sessionID={sessionID} onClose={() => setTarget(null)} />
    </>
  );
}

const kindIcon = (kind: string) =>
  kind === "image" ? <FileImage className="h-4 w-4" /> : <FileText className="h-4 w-4" />;

/**
 * ArtifactPreview 展示工作区文件：图片、PDF、Markdown
 * 和文本。会话范围内的文本文件可以切换到相对于任务基线的行级差异（Codex
 * 风格审查），而无需离开对话框。
 */
export function ArtifactPreview({ path, sessionID, onClose }: { path: string | null | undefined; sessionID?: string; onClose: () => void }) {
  const [art, setArt] = useState<ArtifactData | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [showDiff, setShowDiff] = useState(false);

  useEffect(() => {
    setShowDiff(false);
    if (!path) {
      setArt(null);
      setError("");
      return;
    }
    // 过期响应防护：当用户快速重新定位目标时，针对文件 A
    // 的缓慢打开操作不得在文件 B 下渲染（知识库预览会这样做）。
    let cancelled = false;
    setLoading(true);
    (sessionID ? Artifact.OpenForSession(sessionID, path) : Artifact.Open(path))
      .then((a) => {
        if (cancelled) return;
        if (!a) throw new Error("未找到文件");
        setArt(a);
      })
      .catch((e) => {
        if (!cancelled) setError(errText(e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [path, sessionID]);

  return (
    <Dialog open={!!path} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-base">
            {art && kindIcon(art.kind)}
            <span className="min-w-0 truncate">{art?.name ?? path}</span>
            {art && (
              <Badge variant="outline" className="font-normal">
                {art.kind}
              </Badge>
            )}
            {art?.truncated && (
              <Badge variant="secondary" className="font-normal">
                已截断
              </Badge>
            )}
          </DialogTitle>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-auto rounded-lg bg-control p-3">
          {showDiff && sessionID && path ? (
            <DiffView sessionID={sessionID} path={path} />
          ) : (
          <>
          {loading && (
            <div className="flex h-40 items-center justify-center text-sm text-muted-foreground">
              <Loader2 className="mr-2 h-4 w-4 animate-spin" /> 加载中…
            </div>
          )}
          {!loading && error && (
            <div className="text-sm text-danger-text">{error}</div>
          )}
          {!loading && !error && art?.kind === "image" && (
            <div className="flex justify-center">
              <img src={art.dataUrl} alt={art.name} className="max-h-[60vh] max-w-full rounded object-contain" />
            </div>
          )}
          {!loading && !error && art?.kind === "pdf" && (
            <iframe src={art.dataUrl ?? ""} aria-label={art.name} className="h-[60vh] w-full rounded" />
          )}
          {!loading && !error && art?.kind === "markdown" && (
            <MessageMarkdown content={art.text ?? ""} />
          )}
          {!loading && !error && art?.kind === "text" && (
            <pre className="whitespace-pre-wrap font-mono text-xs leading-relaxed">{art.text}</pre>
          )}
          </>
          )}
        </div>
        <div className="flex items-center justify-between gap-2 pt-2">
          {art && sessionID && (art.kind === "text" || art.kind === "markdown") ? (
            <Button
              variant="ghost"
              size="sm"
              className="h-7 px-2 text-xs"
              aria-pressed={showDiff}
              onClick={() => setShowDiff((v) => !v)}
            >
              {showDiff ? "返回预览" : "与任务基线对比"}
            </Button>
          ) : <span />}
          {art && (
            <div className="text-right text-xs text-muted-foreground">
              工作区路径: {art.path} · {(art.size / 1024).toFixed(1)} KB
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
