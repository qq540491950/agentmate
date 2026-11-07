// 全局顶栏。issue #75：应用外壳顶部的全宽应用 chrome，承载品牌与扩展槽位；
// 平台差异全部来自 resolveTopbarLayout 的纯函数解析，组件本身不含平台判断。
import type { CSSProperties, ReactNode } from "react";
import { Sparkles } from "lucide-react";
import { resolveTopbarLayout, detectTopbarPlatform, type TopbarLayout } from "@/lib/topbarLayout";

interface TopBarProps {
  /** 注入布局以便测试；缺省按运行环境解析。 */
  layout?: TopbarLayout;
  /** 中部扩展槽位（v1 为空，后续挂应用菜单 / 全局搜索 / 状态指示）。 */
  centerSlot?: ReactNode;
  /** 右侧扩展槽位（v1 为空）。 */
  endSlot?: ReactNode;
}

export function TopBar({ layout, centerSlot, endSlot }: TopBarProps) {
  const resolved =
    layout ??
    resolveTopbarLayout(
      typeof window === "undefined"
        ? undefined
        : detectTopbarPlatform((window as unknown as { _wails?: { environment?: { os?: string } } })._wails?.environment),
    );

  return (
    <header
      data-slot="topbar"
      className="flex h-10 shrink-0 items-center gap-2 border-b border-border bg-surface-1 pr-1.5"
      style={resolved.dragRegion ? ({ "--wails-draggable": "drag" } as CSSProperties) : undefined}
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
        <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md bg-brand/15 text-brand">
          <Sparkles className="h-3.5 w-3.5" />
        </span>
        <span className="min-w0 truncate text-ui font-semibold">AgentMate</span>
      </div>
      <div data-slot="topbar-slot-center" className="min-w-0 flex-1">{centerSlot}</div>
      <div data-slot="topbar-slot-end" className="flex items-center">{endSlot}</div>
      {resolved.windowButtonsArea && (
        <div data-slot="topbar-window-buttons" className="flex items-center" />
      )}
    </header>
  );
}
