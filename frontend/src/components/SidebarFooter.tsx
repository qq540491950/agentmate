import { useCallback, useEffect, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { toast } from "sonner";
import {
  ArrowUp,
  Check,
  CircleHelp,
  Download,
  ExternalLink,
  Loader2,
  Monitor,
  Moon,
  Palette,
  RefreshCw,
  Settings as SettingsIcon,
  Sun,
  TriangleAlert,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Popover, PopoverAnchor, PopoverClose, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Update, errText, type UpdateStatus } from "@/lib/api";
import { cn } from "@/lib/utils";
import { UPDATE_EVENTS } from "@/lib/updateEvents";
import { THEME_MODES, THEME_OPTIONS } from "@/lib/theme";
import {
  formatUpdateBytes,
  sidebarUpdateVisible,
  updateBadge,
  updatePercent,
  updateProgressFromEvent,
  updatePhaseTitle,
  type UpdateBadgeTone,
  type UpdateProgress,
} from "@/lib/updateUi";

const BADGE_TONE: Record<UpdateBadgeTone, string> = {
  brand: "bg-accent-soft text-accent-text hover:bg-accent",
  progress: "bg-accent-soft text-accent-text hover:bg-accent",
  ready: "bg-success/10 text-success hover:bg-success/15",
  error: "bg-danger-soft text-danger-text hover:bg-danger/20",
};

type SidebarFooterProps = {
  version?: string;
  themePreset: string;
  themeMode: string;
  onOpenSettings: () => void;
  onOpenHelp: () => void;
  onThemePresetChange: (preset: string) => void;
  onThemeModeChange: (mode: string) => void;
};

/**
 * SidebarFooter keeps the version, updater entry and utility actions on one
 * compact row. The updater mirrors the backend state machine via Status() and
 * updater events; the same service still backs 设置 → 关于.
 */
export function SidebarFooter({
  version,
  themePreset,
  themeMode,
  onOpenSettings,
  onOpenHelp,
  onThemePresetChange,
  onThemeModeChange,
}: SidebarFooterProps) {
  const [status, setStatus] = useState<UpdateStatus | null>(null);
  const [progress, setProgress] = useState<UpdateProgress | null>(null);
  const [busy, setBusy] = useState(false);
  const [panelOpen, setPanelOpen] = useState(false);
  const [restartOpen, setRestartOpen] = useState(false);
  const [readyDismissed, setReadyDismissed] = useState(false);
  const mounted = useRef(true);
  const previousState = useRef("");

  const refresh = useCallback(() => {
    Update.Status()
      .then((next) => {
        if (mounted.current) setStatus(next);
      })
      .catch(() => {
        // The sidebar entry is an enhancement: an unavailable updater must not
        // turn the footer into a toast generator.
      });
  }, []);

  useEffect(() => {
    mounted.current = true;
    refresh();
    const offs = UPDATE_EVENTS.map((name) =>
      Events.On(name, (event: unknown) => {
        if (name === "wails:updater:download-progress") {
          const payload = updateProgressFromEvent(event);
          if (payload) setProgress(payload);
          return;
        }
        if (
          name === "wails:updater:update-available" ||
          name === "wails:updater:download-started" ||
          name === "wails:updater:update-ready"
        ) {
          setProgress(null);
        }
        refresh();
      }),
    );
    return () => {
      mounted.current = false;
      offs.forEach((off) => off());
    };
  }, [refresh]);

  // A staged update is a one-time prompt. Choosing「稍后重启」keeps the
  //「重启更新」chip so the prompt can be reopened without nagging.
  useEffect(() => {
    const state = status?.state ?? "";
    if (state === "ready" && previousState.current !== "ready" && !readyDismissed) {
      setRestartOpen(true);
    }
    if (state !== "ready" && readyDismissed) setReadyDismissed(false);
    previousState.current = state;
  }, [status?.state, readyDismissed]);

  useEffect(() => {
    if (status?.state === "ready") setPanelOpen(false);
  }, [status?.state]);

  const install = async () => {
    if (!status) return;
    if (!status.canAutoInstall) {
      try {
        await Update.OpenDownloadPage();
      } catch (error) {
        toast.error(errText(error));
      }
      return;
    }
    setBusy(true);
    setProgress(null);
    // Feedback starts immediately; updater events then replace this snapshot
    // with the authoritative downloading/verifying/installing states.
    setStatus((current) => (current ? { ...current, state: "downloading" } : current));
    try {
      const next = await Update.InstallUpdate();
      if (mounted.current) setStatus(next);
    } catch (error) {
      toast.error(`更新失败：${errText(error)}`);
      refresh();
    } finally {
      if (mounted.current) setBusy(false);
    }
  };

  const apply = async () => {
    setBusy(true);
    try {
      await Update.ApplyUpdate();
    } catch (error) {
      toast.error(`重启更新失败：${errText(error)}`);
      setBusy(false);
    }
  };

  const skip = async () => {
    if (!status?.availableVersion) return;
    try {
      await Update.Skip(status.availableVersion);
      setPanelOpen(false);
      toast.success(`已忽略 v${status.availableVersion}`);
      setStatus((current) => (current ? { ...current, state: "up-to-date" } : current));
      const next = await Update.CheckNow().catch(() => null);
      if (next) setStatus(next);
      else refresh();
    } catch (error) {
      toast.error(errText(error));
    }
  };

  const openDownloadPage = async () => {
    try {
      await Update.OpenDownloadPage();
    } catch (error) {
      toast.error(errText(error));
    }
  };

  const badge = updateBadge(status, progress, busy);
  const visible = sidebarUpdateVisible(status);
  const percent = updatePercent(progress);
  const title = status ? updatePhaseTitle(status) : "检查更新";
  const availableVersion = status?.availableVersion ?? "";
  const currentVersion = status?.currentVersion || version || "";

  const onPanelOpenChange = (open: boolean) => {
    if (open && status?.state === "ready") {
      setRestartOpen(true);
      return;
    }
    setPanelOpen(open);
  };

  return (
    <Popover open={panelOpen} onOpenChange={onPanelOpenChange}>
      <PopoverAnchor asChild>
        <div className="relative flex h-9 items-center gap-1 bg-chrome px-3 text-3xs text-muted-foreground">
          <span
            className="min-w-0 flex-1 truncate"
            title={
              currentVersion
                ? `AgentMate v${currentVersion} · 基于 Google ADK Go v2 · Wails v3`
                : "AgentMate · 基于 Google ADK Go v2 · Wails v3"
            }
          >
            {currentVersion ? `AgentMate v${currentVersion}` : "AgentMate"}
          </span>
          {visible && badge && (
            <PopoverTrigger asChild>
              <button
                type="button"
                title={availableVersion ? `${title} v${availableVersion}` : title}
                aria-label={
                  availableVersion ? `${badge.label}，${title}，v${availableVersion}` : title
                }
                className={cn(
                  "inline-flex h-[21px] shrink-0 items-center gap-1 rounded-full px-1.5 text-3xs font-semibold transition-colors",
                  BADGE_TONE[badge.tone],
                )}
              >
                {badge.tone === "ready" && <Check className="h-2.5 w-2.5" strokeWidth={2.4} />}
                {badge.tone === "progress" && status?.state === "downloading" && (
                  <span
                    className="h-2.5 w-2.5 rounded-full"
                    style={{
                      background: `conic-gradient(currentColor 0 ${percent ?? 12}%, color-mix(in oklab, currentColor 18%, transparent) ${percent ?? 12}% 100%)`,
                      mask: "radial-gradient(circle, transparent 42%, black 45%)",
                      WebkitMask: "radial-gradient(circle, transparent 42%, black 45%)",
                    }}
                    aria-hidden="true"
                  />
                )}
                {badge.tone === "progress" && status?.state !== "downloading" && (
                  <Loader2 className="h-2.5 w-2.5 animate-spin" />
                )}
                {badge.tone === "error" && <TriangleAlert className="h-2.5 w-2.5" />}
                {badge.tone === "brand" && (
                  <ArrowUp className="h-2.5 w-2.5" strokeWidth={2.4} />
                )}
                {badge.label}
              </button>
            </PopoverTrigger>
          )}
          <span className="flex shrink-0 items-center gap-0.5">
            <button
              title="设置（⌘,）"
              onClick={onOpenSettings}
              className="flex h-6 w-6 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              <SettingsIcon className="h-3.5 w-3.5" />
            </button>
            <button
              title="快捷键与命令"
              onClick={onOpenHelp}
              className="flex h-6 w-6 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              <CircleHelp className="h-3.5 w-3.5" />
            </button>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button
                  title="主题与外观"
                  aria-label="主题与外观"
                  className="flex h-6 w-6 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                >
                  <Palette className="h-3.5 w-3.5" />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent side="top" align="start" className="w-56">
                <DropdownMenuLabel>主题</DropdownMenuLabel>
                <DropdownMenuRadioGroup value={themePreset} onValueChange={onThemePresetChange}>
                  {THEME_OPTIONS.map((theme) => (
                    <DropdownMenuRadioItem key={theme.value} value={theme.value} className="text-xs">
                      {theme.label}
                    </DropdownMenuRadioItem>
                  ))}
                </DropdownMenuRadioGroup>
                <DropdownMenuSeparator />
                <DropdownMenuLabel>明暗模式</DropdownMenuLabel>
                <DropdownMenuRadioGroup value={themeMode} onValueChange={onThemeModeChange}>
                  {THEME_MODES.map((mode) => (
                    <DropdownMenuRadioItem key={mode.value} value={mode.value} className="text-xs">
                      {mode.value === "light" ? <Sun /> : mode.value === "dark" ? <Moon /> : <Monitor />}
                      {mode.label}
                    </DropdownMenuRadioItem>
                  ))}
                </DropdownMenuRadioGroup>
                <DropdownMenuSeparator />
                <DropdownMenuItem className="text-xs" onSelect={onOpenSettings}>
                  <SettingsIcon /> 外观设置…
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </span>
        </div>
      </PopoverAnchor>

      {visible && status && (
        <PopoverContent
          side="top"
          align="start"
          sideOffset={8}
          collisionPadding={12}
          className="w-[300px] max-w-[calc(100vw-24px)] overflow-hidden p-0"
        >
          <div className="flex items-start gap-2.5 p-3.5 pb-2.5">
            <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-[10px] bg-accent-soft text-accent-text">
              {status.state === "downloading" || status.state === "verifying" || status.state === "installing" ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : status.state === "error" ? (
                <TriangleAlert className="h-3.5 w-3.5" />
              ) : (
                <Download className="h-3.5 w-3.5" />
              )}
            </span>
            <div className="min-w-0 flex-1 pt-px">
              <div className="text-3xs font-semibold uppercase tracking-[0.12em] text-accent-text">
                {title}
              </div>
              <div className="mt-0.5 text-ui leading-tight font-semibold tracking-[-0.01em] text-foreground">
                AgentMate {availableVersion ? <span className="text-accent-text">v{availableVersion}</span> : ""}
              </div>
              <div className="mt-0.5 text-3xs text-muted-foreground">
                当前版本 {currentVersion ? `v${currentVersion}` : "未知"}
              </div>
            </div>
            <PopoverClose asChild>
              <button
                type="button"
                aria-label="关闭更新面板"
                className="-mr-1 -mt-1 flex h-5 w-5 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
              >
                <X className="h-3 w-3" />
              </button>
            </PopoverClose>
          </div>

          {status.state === "available" && status.notes && (
            <div className="mx-3.5 mb-3 rounded-lg bg-muted/50 p-2.5">
              <div className="mb-1 text-3xs font-medium text-foreground/80">本次更新</div>
              <div className="max-h-28 overflow-y-auto whitespace-pre-wrap text-2xs leading-relaxed text-muted-foreground">
                {status.notes}
              </div>
            </div>
          )}

          {status.state === "downloading" && (
            <div className="px-3.5 pb-3">
              <div className="flex items-baseline justify-between text-2xs text-muted-foreground">
                <span>正在下载安装包</span>
                <strong className="text-base leading-none font-semibold tracking-[-0.02em] text-foreground">
                  {percent ?? 0}%
                </strong>
              </div>
              <div
                className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-muted"
                role="progressbar"
                aria-label="更新下载进度"
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={percent ?? 0}
              >
                <div
                  className="h-full rounded-full bg-brand transition-[width] duration-300"
                  style={{ width: `${percent ?? 6}%` }}
                />
              </div>
              <div className="mt-1.5 flex items-center justify-between text-3xs text-muted-foreground">
                <span>
                  {progress
                    ? `${formatUpdateBytes(progress.written)} / ${progress.total ? formatUpdateBytes(progress.total) : "未知大小"}`
                    : "正在获取下载信息…"}
                </span>
                {progress?.rate ? <span>{formatUpdateBytes(progress.rate)}/s</span> : null}
              </div>
            </div>
          )}

          {(status.state === "verifying" || status.state === "installing") && (
            <div className="px-3.5 pb-3">
              <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                <div className="h-full w-2/3 animate-pulse rounded-full bg-brand" />
              </div>
              <div className="mt-1.5 text-3xs text-muted-foreground">
                {status.state === "verifying" ? "校验下载内容，请稍候…" : "正在准备安装，请稍候…"}
              </div>
            </div>
          )}

          {status.state === "error" && (
            <div className="mx-3.5 mb-3 rounded-lg bg-danger-soft p-2.5 text-2xs leading-relaxed text-muted-foreground">
              <div className="font-medium text-danger-text">更新未完成</div>
              <div className="mt-0.5">{status.error || "请稍后重试。"}</div>
            </div>
          )}

          {status.canAutoInstall === false && status.state === "available" && (
            <div className="mx-3.5 mb-3 rounded-lg bg-muted/50 p-2.5 text-2xs leading-relaxed text-muted-foreground">
              {status.reason || "当前安装方式不支持应用内更新，请打开发布页下载。"}
            </div>
          )}

          <div className="px-3.5 pb-3">
            {status.state === "available" && (
              <Button
                size="sm"
                className="h-8 w-full gap-1.5 text-xs"
                onClick={install}
                disabled={busy}
              >
                {status.canAutoInstall ? (
                  <>
                    <Download className="h-3.5 w-3.5" />
                    {busy ? "正在准备…" : "下载并更新"}
                  </>
                ) : (
                  <>
                    <ExternalLink className="h-3.5 w-3.5" />
                    打开发布页
                  </>
                )}
              </Button>
            )}
            {status.state === "downloading" && (
              <button
                type="button"
                onClick={() => setPanelOpen(false)}
                className="w-full rounded-md py-1.5 text-center text-3xs text-muted-foreground hover:bg-accent/50 hover:text-foreground"
              >
                更新在后台继续
              </button>
            )}
            {status.state === "error" && (
              <Button
                size="sm"
                variant="outline"
                className="h-8 w-full gap-1.5 text-xs"
                onClick={install}
                disabled={busy}
              >
                <RefreshCw className={cn("h-3.5 w-3.5", busy && "animate-spin")} />
                重试更新
              </Button>
            )}
          </div>

          <div className="flex items-center justify-between bg-panel/60 px-3.5 py-2 text-3xs text-muted-foreground">
            {status.state === "available" && status.canAutoInstall ? (
              <button type="button" className="hover:text-foreground" onClick={skip}>
                跳过此版本
              </button>
            ) : status.state === "available" ? (
              <span>请在新页面下载安装</span>
            ) : (
              <span>完成后会提示重启</span>
            )}
            <button
              type="button"
              className="inline-flex items-center gap-1 hover:text-foreground"
              onClick={openDownloadPage}
            >
              所有版本 <ExternalLink className="h-2.5 w-2.5" />
            </button>
          </div>
        </PopoverContent>
      )}

      <AlertDialog
        open={restartOpen}
        onOpenChange={(open) => {
          setRestartOpen(open);
          if (!open) setReadyDismissed(true);
        }}
      >
        <AlertDialogContent className="max-w-[360px] gap-0 overflow-hidden p-0">
          <div className="flex flex-col items-center px-5 pb-3 pt-5 text-center">
            <div className="flex h-10 w-10 items-center justify-center rounded-full bg-success/10 text-success">
              <Check className="h-4 w-4" strokeWidth={2.4} />
            </div>
            <AlertDialogTitle className="mt-3 text-base">更新已准备就绪</AlertDialogTitle>
            <AlertDialogDescription className="mt-1.5 text-xs leading-relaxed">
              新版本已下载并完成校验。重启 AgentMate 后即可使用。
            </AlertDialogDescription>
          </div>
          <div className="mx-5 rounded-lg bg-muted/50 px-3 py-2.5">
            <div className="flex items-center justify-between text-2xs">
              <div className="flex items-center gap-1.5 text-muted-foreground">
                <span>AgentMate</span>
                <strong className="font-semibold text-foreground">v{availableVersion}</strong>
              </div>
              <span className="text-success">已校验</span>
            </div>
            <div className="mt-2 h-1 overflow-hidden rounded-full bg-muted">
              <div className="h-full w-full rounded-full bg-success" />
            </div>
            <div className="mt-1 flex justify-between text-3xs text-muted-foreground">
              <span>下载与安装准备</span>
              <span className="text-success">100%</span>
            </div>
          </div>
          <div className="mx-5 mt-3 text-center text-2xs leading-relaxed text-muted-foreground">
            会话与设置会自动保存。若有正在运行的任务，建议先结束再重启。
          </div>
          <AlertDialogFooter className="mt-4 bg-muted/40 p-3 sm:flex-row">
            <AlertDialogCancel asChild>
              <Button size="sm" variant="outline" className="flex-1 text-xs" disabled={busy}>
                稍后重启
              </Button>
            </AlertDialogCancel>
            <AlertDialogAction asChild>
              <Button
                size="sm"
                className="flex-1 text-xs"
                disabled={busy}
                onClick={(event) => {
                  event.preventDefault();
                  void apply();
                }}
              >
                {busy ? "正在重启…" : "立即重启"}
              </Button>
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Popover>
  );
}
