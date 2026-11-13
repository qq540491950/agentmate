// 会话行右边缘操作按钮组的可见性类名（归档 / 固定 / “…”
// 菜单）。在行被悬停或持有焦点之前，该按钮组不参与布局，从而使每行保持四个控件
// ，而不是永久预留一条条带。
//
// 此按钮组内的“…”按钮是 Radix
// 下拉菜单的锚点，因此在该菜单打开期间，按钮组必须重新获得其布局盒：Radix
// 的模态层会设置 `body { pointer-events: none }`（行不再匹配
// `:hover`），并将焦点移入 portal 渲染的菜单内容（行不再匹配
// `:focus-within`）。仅依靠 hover/focus 分支时，span 会保持
// display:none，popper 测量到 0×0 的锚点，菜单便绘制在窗口左上角（issue
// #28）。只要触发器报告 `data-state="open"`，group-has 变体就会让 span 保持
// display:flex。
export const SESSION_ROW_ACTIONS_CLASS =
  "hidden shrink-0 items-center gap-0.5 group-hover:flex group-focus-within:flex group-has-data-[state=open]:flex";