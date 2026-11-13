// 供应商编辑器对话框布局 (#32)。模型列表没有天然上限 ——
// 抓取九个模型就足以把页脚挤出默认窗口 ——
// 因此对话框会加上视口高度上限，且只有表单主体可滚动。
//
// 这两半必须保持在一起：有高度上限的对话框如果缺少可滚动的主体，就会裁掉页脚；
// 而可滚动的主体如果没有 `min-h-0`，就无法在 flex 列内收缩（min-height
// 默认为内容高度），于是对话框仍会超出视口。不要把 `overflow-y-auto`
// 加在对话框自身上，这样「保存」/「取消」绝不会滚出可达范围。
export const PROVIDER_EDITOR_DIALOG_CLASS =
  "flex max-h-[85vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-3xl";

export const PROVIDER_EDITOR_BODY_CLASS =
  "grid min-h-0 flex-1 gap-6 overflow-y-auto px-6 py-5";