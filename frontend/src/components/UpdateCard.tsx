import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { Events } from "@wailsio/runtime";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Label } from "@/components/ui/label";
import { Update, errText, type UpdateStatus } from "@/lib/api";

// Every lifecycle event the Wails updater emits. We re-read the service
// snapshot on each one instead of reconstructing state from payloads: the
// updater owns the state machine, and mirroring it in the page is how the two
// end up disagreeing.
const UPDATE_EVENTS = [
  "wails:updater:check-started",
  "wails:updater:update-available",
  "wails:updater:no-update",
  "wails:updater:download-started",
  "wails:updater:download-progress",
  "wails:updater:download-complete",
  "wails:updater:verifying",
  "wails:updater:installing",
  "wails:updater:update-ready",
  "wails:updater:error",
] as const;

type Progress = { written: number; total: number; rate: number };

function stateLabel(s: UpdateStatus): string {
  switch (s.state) {
    case "unconfigured":
      return "更新服务未初始化";
    case "checking":
      return "正在检查更新…";
    case "up-to-date":
      return "已是最新版本";
    case "available":
      return `发现新版本 v${s.availableVersion}`;
    case "downloading":
      return "正在下载…";
    case "verifying":
      return "正在校验安装包…";
    case "installing":
      return "正在准备安装…";
    case "ready":
      return `v${s.availableVersion} 已就绪，重启后生效`;
    case "error":
      return s.error ? `更新失败：${s.error}` : "更新失败";
    default:
      return "可以检查更新";
  }
}

function formatBytes(n: number): string {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

/**
 * UpdateCard renders the in-app updater inside 设置 → 关于.
 *
 * It is deliberately inline rather than the framework's standalone update
 * window: the surrounding settings page already owns version display,
 * persistence and toast conventions here.
 */
export function UpdateCard({ version }: { version?: string }) {
  const [status, setStatus] = useState<UpdateStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<Progress | null>(null);
  const [notesOpen, setNotesOpen] = useState(false);
  const mounted = useRef(true);

  const refresh = useCallback(() => {
    Update.Status()
      .then((s) => {
        if (mounted.current) setStatus(s);
      })
      .catch((e) => toast.error(errText(e)));
  }, []);

  useEffect(() => {
    mounted.current = true;
    refresh();
    const offs = UPDATE_EVENTS.map((name) =>
      Events.On(name, (e: any) => {
        if (name === "wails:updater:download-progress") {
          if (e && typeof e.written === "number") setProgress(e as Progress);
          return;
        }
        if (name === "wails:updater:update-available") setNotesOpen(false);
        if (name === "wails:updater:update-ready") setProgress(null);
        refresh();
      }),
    );
    return () => {
      mounted.current = false;
      offs.forEach((off) => off());
    };
  }, [refresh]);

  const check = async () => {
    setBusy(true);
    setProgress(null);
    try {
      const s = await Update.CheckNow();
      setStatus(s);
      if (s.state === "up-to-date") {
        toast.success(
          s.skippedVersion ? `已是最新（已忽略 v${s.skippedVersion}）` : "已是最新版本",
        );
      } else if (s.availableVersion) {
        toast.success(`发现新版本 v${s.availableVersion}`);
      }
    } catch (e) {
      toast.error(errText(e));
      refresh();
    } finally {
      setBusy(false);
    }
  };

  const install = async () => {
    setBusy(true);
    try {
      const s = await Update.InstallUpdate();
      setStatus(s);
      toast.success("下载并校验完成，重启即可生效");
    } catch (e) {
      toast.error(errText(e));
      refresh();
    } finally {
      setBusy(false);
    }
  };

  const apply = async () => {
    try {
      await Update.ApplyUpdate();
      // The helper takes over once this process exits; nothing to do here.
    } catch (e) {
      toast.error(errText(e));
    }
  };

  const skip = async () => {
    if (!status?.availableVersion) return;
    try {
      await Update.Skip(status.availableVersion);
      toast.success(`已忽略 v${status.availableVersion}`);
      check();
    } catch (e) {
      toast.error(errText(e));
    }
  };

  const openDownloadPage = async () => {
    try {
      await Update.OpenDownloadPage();
    } catch (e) {
      toast.error(errText(e));
    }
  };

  const toggleAutoCheck = async (on: boolean) => {
    try {
      await Update.SetAutoCheck(on);
      refresh();
    } catch (e) {
      toast.error(errText(e));
    }
  };

  const current = status?.currentVersion || version || "";
  const available = status?.state === "available" || status?.state === "ready";
  const downloaded = status?.state === "ready";
  const downloading = status?.state === "downloading";
  const percent =
    progress && progress.total > 0
      ? Math.min(100, Math.round((progress.written / progress.total) * 100))
      : null;

  return (
    <div className="mt-3 rounded-md border p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="text-foreground">
          {stateLabel(status ?? ({ state: "idle" } as UpdateStatus))}
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={check} disabled={busy || downloading}>
            {status?.state === "checking" ? "检查中…" : "检查更新"}
          </Button>
          {available && status?.canAutoInstall && (
            <Button size="sm" onClick={downloaded ? apply : install} disabled={busy || downloading}>
              {downloaded ? "重启完成更新" : "下载并安装"}
            </Button>
          )}
          {available && !status?.canAutoInstall && (
            <Button size="sm" onClick={openDownloadPage}>
              打开下载页
            </Button>
          )}
        </div>
      </div>

      {status?.canAutoInstall === false && (
        <div className="mt-2 text-xs text-muted-foreground">{status.reason}</div>
      )}

      {downloading && (
        <div className="mt-2">
          <div className="h-1.5 w-full overflow-hidden rounded bg-muted">
            <div
              className="h-full bg-primary transition-all"
              style={{ width: `${percent ?? 5}%` }}
            />
          </div>
          <div className="mt-1 text-xs text-muted-foreground">
            {progress
              ? `${formatBytes(progress.written)} / ${progress.total ? formatBytes(progress.total) : "未知大小"}${
                  progress.rate ? ` · ${formatBytes(progress.rate)}/s` : ""
                }`
              : "开始下载…"}
          </div>
        </div>
      )}

      {status?.notes && available && (
        <div className="mt-2">
          <button
            type="button"
            className="text-xs text-muted-foreground underline-offset-2 hover:underline"
            onClick={() => setNotesOpen((v) => !v)}
          >
            {notesOpen ? "收起更新说明" : "查看更新说明"}
          </button>
          {notesOpen && (
            <pre className="mt-1 max-h-56 overflow-auto whitespace-pre-wrap rounded bg-muted/40 p-2 text-xs text-muted-foreground">
              {status.notes}
            </pre>
          )}
        </div>
      )}

      <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t pt-2">
        <div className="flex items-center gap-2">
          <Switch
            id="auto-check-update"
            checked={status?.autoCheck ?? true}
            onCheckedChange={toggleAutoCheck}
          />
          <Label htmlFor="auto-check-update" className="text-xs font-normal">
            启动后自动检查更新（每 24 小时一次，仅提示不自动安装）
          </Label>
        </div>
        <div className="flex items-center gap-3 text-xs">
          {status?.skippedVersion && (
            <span className="text-muted-foreground">已忽略 v{status.skippedVersion}</span>
          )}
          {available && status?.availableVersion && (
            <button
              type="button"
              className="text-muted-foreground underline-offset-2 hover:underline"
              onClick={skip}
            >
              跳过此版本
            </button>
          )}
          <button
            type="button"
            className="text-muted-foreground underline-offset-2 hover:underline"
            onClick={openDownloadPage}
          >
            所有版本
          </button>
        </div>
      </div>
      {current && <div className="mt-1 text-xs text-muted-foreground">当前版本 v{current}</div>}
    </div>
  );
}
