// 顶栏窗口控制：状态 → 图标语义映射（约定 seam）与窗口操作适配。
// 操作适配层统一做平台降级：非桌面环境（浏览器 / stub）下全部无操作，
// 保证 stub 视觉回归「点击按钮不报错」。
import { System, Window } from "@wailsio/runtime";

export type MaximiseIcon = "maximise" | "restore";

/** 最大化状态 → 按钮图标语义：已最大化显示「还原」，否则显示「最大化」。 */
export function resolveMaximiseIcon(isMaximised: boolean): MaximiseIcon {
  return isMaximised ? "restore" : "maximise";
}

export interface WindowOperations {
  minimise(): void;
  toggleMaximise(): void;
  hide(): void;
  /** 读取当前是否最大化；非桌面环境恒为 false。 */
  isMaximised(): Promise<boolean>;
  /** 读取当前是否全屏；非桌面环境恒为 false。 */
  isFullscreen(): Promise<boolean>;
}

export const wailsWindowOperations: WindowOperations = {
  minimise() {
    if (System.IsDesktop()) void Window.Minimise();
  },
  toggleMaximise() {
    if (System.IsDesktop()) void Window.ToggleMaximise();
  },
  hide() {
    if (System.IsDesktop()) void Window.Hide();
  },
  isMaximised() {
    return System.IsDesktop() ? Window.IsMaximised() : Promise.resolve(false);
  },
  isFullscreen() {
    return System.IsDesktop() ? Window.IsFullscreen() : Promise.resolve(false);
  },
};
