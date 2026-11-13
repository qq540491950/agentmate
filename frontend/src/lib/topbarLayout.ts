// 顶栏布局的平台解析。issue #75 的约定 seam：组件只渲染这个函数的输出，
// 平台差异全部收敛在这里，便于单测与后续扩展。
export type TopbarPlatform = "windows" | "macos" | "browser";

export interface TopbarLayout {
  /** Windows：顶栏右侧预留自绘窗口按钮区（按钮由窗口控制票挂载） */
  windowButtonsArea: boolean;
  /** macOS：红绿灯交通灯留白（px），顶栏内容为其让位 */
  trafficLightInset: number;
  /** Windows：顶栏空白区声明 Wails 拖拽区（--wails-draggable: drag） */
  dragRegion: boolean;
}
export function resolveTopbarLayout(platform?: TopbarPlatform): TopbarLayout {
  switch (platform) {
    case "windows":
      // 系统标题栏移除后，顶栏空白区即拖拽区；按钮区在渲染时再声明 no-drag。
      return { windowButtonsArea: true, trafficLightInset: 0, dragRegion: true };
    case "macos":
      // 红绿灯位于隐藏标题栏内（约 20px 边距 + 52px 宽度），顶栏内容让位约
      // 78px。
      return { windowButtonsArea: false, trafficLightInset: 78, dragRegion: false };
    case "browser":
      // stub / 浏览器渲染按钮以覆盖视觉回归（操作层降级为无操作）； Linux
      // 桌面同样走此分支（v1 无 Linux 自绘 chrome）。
      return { windowButtonsArea: true, trafficLightInset: 0, dragRegion: false };
    default:
      // SSR / 无 window 环境按 browser 降级，组件静态渲染不炸。
      return resolveTopbarLayout("browser");
  }
}

/**
 * 从 Wails 注入的运行环境推断顶栏平台；无注入（浏览器/stub）时回退为 browser。
 * 真实契约是大写字段：Wails v3 runtime 在页面脚本前同步注入
 * `window._wails.environment={"OS":…,"Arch":…,"Debug":false}` （wails
 * internal/runtime/runtime_prod.go），@wailsio/runtime 内部 （system.js /
 * drag.js）也按大写读取；小写 os 为手写环境兜底。
 */
export function detectTopbarPlatform(env: { OS?: string; os?: string } | undefined): TopbarPlatform {
  switch (env?.OS ?? env?.os) {
    case "windows": return "windows";
    case "darwin": return "macos";
    case "linux": return "browser";
    default: return "browser";
  }
}
