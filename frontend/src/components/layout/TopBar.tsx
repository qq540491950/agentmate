// 全局顶栏。issue #75/#76：应用外壳顶部的全宽应用 chrome，承载品牌、窗口
// 控制与扩展槽位；平台差异全部来自 resolveTopbarLayout 的纯函数解析，组件
// 本身不含平台判断。窗口状态经 Wails 窗口事件同步，最大化/还原切换图标。
import { useEffect, useState, type CSSProperties, type ReactNode } from "react";
import { Copy, Minus, Sparkles, Square, X } from "lucide-react";
import { Events } from "@wailsio/runtime";
import { toast } from "sonner";
import { TooltipHint } from "@/components/ui/tooltip";
import { resolveTopbarLayout, detectTopbarPlatform, type TopbarLayout } from "@/lib/topbarLayout";
import { resolveMaximiseIcon, wailsWindowOperations, type WindowOperations } from "@/lib/topbarWindow";

interface TopBarProps {
  /** 注入布局以便测试；缺省按运行环境解析。 */
  layout?: TopbarLayout;
  /** 注入窗口操作以便测试；缺省为 Wails 窗口适配（浏览器降级无操作）。 */
  ops?: WindowOperations;
  /** 中部扩展槽位（v1 为空，后续挂应用菜单 / 全局搜索 / 状态指示）。 */
  centerSlot?: ReactNode;
  /** 右侧扩展槽位（v1 为空）。 */
  endSlot?: ReactNode;
}

// 首次隐藏到托盘的一次性提示（#77）：本地标记去重，自绘关闭按钮与
// Alt+F4（Go 关闭钩子经 shell:hidden-to-tray 事件）两条路径共用。
function toastHiddenToTrayOnce() {
  try {
    if (localStorage.getItem("wb-hidden-to-tray-once")) return;
    localStorage.setItem("wb-hidden-to-tray-once", "1");
  } catch {
    return;
  }
  toast.info("AgentMate 已隐藏到托盘", {
    description: "应用仍在托盘运行：点托盘图标或全局快捷键可唤回，退出请用托盘菜单。",
  });
}

export function TopBar({ layout, ops, centerSlot, endSlot }: TopBarProps) {
  const resolved =
    layout ??
    resolveTopbarLayout(
      typeof window === "undefined"
        ? undefined
        : detectTopbarPlatform(
            (window as unknown as { _wails?: { environment?: { OS?: string; os?: string } } })._wails?.environment,
          ),
    );
  const resolvedOps = ops ?? wailsWindowOperations;
  const [isMaximised, setIsMaximised] = useState(false);
  const [isFullscreen, setIsFullscreen] = useState(false);

  // 初始化读取窗口状态，并订阅最大化/还原与全屏事件同步图标（#76）。
  useEffect(() => {
    let disposed = false;
    resolvedOps.isMaximised().then((v) => { if (!disposed) setIsMaximised(v); }).catch(() => {});
    resolvedOps.isFullscreen().then((v) => { if (!disposed) setIsFullscreen(v); }).catch(() => {});
    const offs = [
      Events.On("common:WindowMaximise", () => setIsMaximised(true)),
      Events.On("common:WindowUnMaximise", () => setIsMaximised(false)),
      Events.On("common:WindowRestore", () => setIsMaximised(false)),
      Events.On("common:WindowFullscreen", () => setIsFullscreen(true)),
      Events.On("common:WindowUnFullscreen", () => setIsFullscreen(false)),
      // Alt+F4 / 系统关闭被 Go 钩子拦截隐藏时，前端经此事件弹一次性提示。
      Events.On("shell:hidden-to-tray", () => toastHiddenToTrayOnce()),
    ];
    return () => {
      disposed = true;
      offs.forEach((off) => off());
    };
  }, [resolvedOps]);

  const dragStyle = resolved.dragRegion ? ({ "--wails-draggable": "drag" } as CSSProperties) : undefined;
  const nodragStyle = resolved.dragRegion ? ({ "--wails-draggable": "no-drag" } as CSSProperties) : undefined;
  const maximiseLabel = resolveMaximiseIcon(isMaximised) === "restore" ? "还原" : "最大化";

  return (
    <header
      data-slot="topbar"
      className="flex h-10 shrink-0 items-center gap-2 bg-chrome pr-1.5"
      style={dragStyle}
      onDoubleClick={(e) => {
        if (!resolved.dragRegion) return;
        const target = e.target as HTMLElement | null;
        if (target?.closest("button") || target?.closest('[data-slot="topbar-window-buttons"]')) return;
        resolvedOps.toggleMaximise();
      }}
    >
      {resolved.trafficLightInset > 0 && (
        <div
          data-slot="topbar-traffic-light-inset"
          aria-hidden
          className="shrink-0"
          style={{ width: resolved.trafficLightInset }}
        />
      )}
      <div data-slot="topbar-brand" className="flex items-center gap-2 pl-3">
        <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md bg-accent-soft text-accent-text">
          <Sparkles className="h-3.5 w-3.5" />
        </span>
        <span className="min-w-0 truncate text-ui font-semibold">AgentMate</span>
      </div>
      <div data-slot="topbar-slot-center" className="min-w-0 flex-1">{centerSlot}</div>
      <div data-slot="topbar-slot-end" className="flex items-center">{endSlot}</div>
      {resolved.windowButtonsArea && (
        <div data-slot="topbar-window-buttons" className="flex items-center" style={nodragStyle}>
          {!isFullscreen && (
            <>
              <TooltipHint content="最小化">
                <button
                  type="button"
                  aria-label="最小化"
                  onClick={resolvedOps.minimise}
                  className="flex h-10 w-11 items-center justify-center text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                >
                  <Minus className="h-4 w-4" />
                </button>
              </TooltipHint>
              <TooltipHint content={maximiseLabel}>
                <button
                  type="button"
                  aria-label={maximiseLabel}
                  onClick={resolvedOps.toggleMaximise}
                  className="flex h-10 w-11 items-center justify-center text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                >
                  {resolveMaximiseIcon(isMaximised) === "restore" ? (
                    <Copy className="h-3.5 w-3.5" />
                  ) : (
                    <Square className="h-3.5 w-3.5" />
                  )}
                </button>
              </TooltipHint>
              <TooltipHint content="关闭">
                <button
                  type="button"
                  aria-label="关闭"
                  onClick={() => {
                    resolvedOps.hide();
                    toastHiddenToTrayOnce();
                  }}
                  className="flex h-10 w-11 items-center justify-center text-muted-foreground transition-colors hover:bg-destructive hover:text-white"
                >
                  <X className="h-4 w-4" />
                </button>
              </TooltipHint>
            </>
          )}
        </div>
      )}
    </header>
  );
}
