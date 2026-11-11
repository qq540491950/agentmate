# AgentMate 设计系统（Nord 表面契约）

> 状态：规范（normative）。适用于 `frontend/**`；与根 `AGENTS.md`、`frontend/AGENTS.md` 一起阅读。设计意图与验收范围见 [UI 视觉系统重构规格](specs/ui-surface-redesign.md)（GitHub #80）。本文件取代旧的“发丝线分区 + 自由强调色”设计系统。
>
> 迁移状态：Nord 表面契约已落地；后续 UI 改动按本契约执行。历史 hairline/accent 文档不作为样式范例。

## 1. 北极星

**表面承担结构，线条只承担交互。** 界面不靠边框划分区域，而靠克制的表面明度差、间距、圆角和状态底纹建立层级。Nord 是默认推荐主题；主题是一套完整色板，不是给中性灰换一个强调色。

四条不可破坏的原则：

1. 布局容器默认没有常驻 border/ring；层级靠 `canvas` / `chrome` / `panel` / `overlay` 四类表面。
2. 相邻表面只差一到两级明度，目标对比比例 **1.05–1.30**；不能看不见，也不能色块跳变。
3. 强调色稀缺：当前项、focus、运行态、少量链接；主按钮保持中性高对比。
4. 每个主题都有完整 light/dark 令牌，并通过对比度与键盘走查才可交付。

## 2. 主题模型

| 主题预设 | 定位 | 允许 |
|---|---|---|
| `nord` | 默认推荐；Polar Night / Snow Storm / Frost | 完整 light/dark |
| `graphite` | 当前中性灰专业观感 | 完整 light/dark |
| `paper` | 暖象牙/米白 + 深墨 | 完整 light/dark |
| `contrast` | 可访问性高对比 | 完整 light/dark；允许明确控件描边 |

明暗模式 `light | dark | system` 与主题预设正交。`system` 只跟随操作系统明暗，不改变预设。不要新增按项目、Agent、会话或页面覆盖主题的机制，也不要恢复自由强调色选择器。

旧配置缺少主题预设时回退 `nord`；未知值回退 `nord`；旧 `accent` 不再参与渲染。迁移必须保留 `themeMode`、字号、对话栏宽等无关偏好。

## 3. 语义令牌

### 3.1 表面

| 语义角色 | 现有别名 | 用途 |
|---|---|---|
| `canvas` | `background` | 主阅读/工作画布、设置内容区 |
| `chrome` | `surface-1` / `sidebar` | 顶栏、对话侧栏、设置导航 |
| `panel` | `card` | 执行组、composer、设置分组、管理列表容器、任务结果面板 |
| `overlay` | `elevated` / `popover` | 菜单、对话框、toast、浮层 |
| `control` | `control` | outline 按钮、复选框等可识别控件填充；不用于布局分区 |
| `choice-selected` | 新增 | 分段控件、选择卡的选中填充；文字固定用 `foreground` |
| `choice-selected-danger` | 新增 | 危险选择卡的选中填充；危险色只用于图标/状态 |
| `accent-soft` | 由 `brand` 派生 | 选中行、候选选中、composer 拖拽、当前轮 |
| `accent-text` | 由 `brand` 派生 | 强调文字、链接、图标 |
| `primary` | `primary` | 中性主按钮 |
| `border` / `line` | `border` | 仅功能边界，不用于布局分区 |

### 3.2 状态

`danger` / `warning` / `success` 各有一组：

- `*-soft`：10% 左右的状态底纹，用于确认卡、错误条、警告条；
- `*-text`：达到 4.5:1 的文字/图标色；
- 原始 Nord Aurora 色只用于填充、进度和小面积图标，不直接做正文。

状态必须同时有图标或文字，不允许“只有彩色边框”的表达。

## 4. Nord 基线

使用官方 Polar Night / Snow Storm / Frost / Aurora 作为身份基础；文字态颜色按 WCAG 修正，保留原始色做底纹与图标。

| 语义角色 | Nord 深色 | Nord 浅色 |
|---|---|---|
| `canvas` | `#2E3440` | `#ECEFF4` |
| `chrome` | `#3B4252` | `#E5E9F0` |
| `panel` | `#434C5E` | `#FFFFFF` |
| `overlay` | `#4C566A` | `#FFFFFF` |
| `control` | `#485265` | `#EBEFF4` |
| `foreground` | `#ECEFF4` | `#2E3440` |
| `muted-foreground` | `#D8DEE9` | `#4C566A` |
| `accent`（装饰/底纹） | `#88C0D0` | `#5E81AC` |
| `accent-text` | `#9BD3E0` | `#4C6A91` |
| `primary` | `#ECEFF4` | `#2E3440` |
| `primary-foreground` | `#2E3440` | `#ECEFF4` |
| `danger-text` | `#F5C2C7` | `#A63D4A` |
| `warning-text` | `#EBCB8B` | `#7A5A12` |
| `success-text` | `#BED6AB` | `#3F6B3F` |
| `focus-ring` | `#88C0D0` | `#4C6A91` |

补充约束：

- Nord 深色 `canvas/chrome/panel/overlay` 相邻比例约 1.24 / 1.17 / 1.17；浅色 `canvas/chrome` 约 1.06、`chrome/panel` 约 1.22。
- 选择控件专用 `choice-selected` / `choice-selected-danger`：Nord 深色 `#4F6173` / `#5C5D6D`，Nord 浅色 `#D2DCE8` / `#EDD8DB`；其他主题在各自令牌块中校准，均通过 `themeContract` 的选择态对比度测试。
- 若浅色 chrome 在实际 100% 亮度下不可感知，只能在 Nord 色族内微调 chrome，不得加回分割线。
- 代码高亮使用 Frost/Aurora 语义槽位；每个主题定义自己的语法色，组件不写死紫色/绿色。
- Nord 官方色值来源记录在主题样式注释中；修改色值必须同步对比度测试。

## 5. 表面与线条

### 5.1 允许的线

- 输入框、Textarea、Select、Checkbox、Radio 的必要控件边界；优先填充面 + focus ring，边界只用于可识别性或 forced-colors。
- focus ring：2px，与相邻表面 ≥ 3:1。
- 对话侧栏 resize 手柄：静止透明，hover/drag 显示 2px 强调色。
- Markdown 表格、代码块、diff 的数据结构线。
- 高对比主题与系统 forced-colors 的可访问性边界。

### 5.2 禁止的线

- 顶栏 `border-b`、对话/设置侧栏 `border-r`、会话头/搜索栏/设置工具栏 `border-b`。
- Card / Dialog / Popover 的 `ring-1 ring-border` 或常驻 `border`。
- 设置行、管理列表行、工具行、文件行的逐行 border。
- 选中项的 `border-l-2`、左侧色条、强调色描边。
- 空态的 dashed border。

### 5.3 层级规则

- 深色主题：chrome/panel/overlay 相对 canvas 逐级更亮。
- 浅色主题：chrome 相对 canvas 略深，panel/overlay 相对 canvas 更亮。
- `canvas` / `chrome` / `panel` 主布局相邻表面比例 1.05–1.30；超过 1.35 视为跳变，低于 1.05 视为不可感知。`overlay` 可与 `panel` 同色，由阴影承担浮层分离。
- 阴影只用于 overlay 和必要的 composer；不要用阴影堆叠制造层级。

### 5.4 选择控件

- 分段控件和选择卡的**静止面必须使用 `control`**，不得使用与父级 `panel` 同色的 `panel`；否则点击区域会退化成纯文字。
- 选中面使用 `choice-selected` / `choice-selected-danger`，与 `control` 的对比度目标 ≥1.20 / ≥1.15；选中文字使用 `foreground`，不要用 `accent-text` 或 `danger-text` 作为正文（它们在软底纹上可能低于 4.5:1）。
- 选中态同时提供 Check 图标或 `aria-checked`，不能只靠颜色；危险状态用危险色图标 + 文字，不用危险色正文或强调色描边。
- 分段控件保持 8px 控件圆角、28–32px 高度和 ≥8px 间距；选择卡使用 12px 圆角与 12px 内距。

## 6. 排版、间距、圆角、动效

- 字体栈：`Geist Variable → PingFang SC → Microsoft YaHei UI → Noto Sans SC → Source Han Sans SC → system-ui`；不换字体。
- 正文 15px / 1.7；控件、列表、侧栏 13px；元数据下限 12px；禁止 10/11px 中文。
- 页面标题 20–24px，区块标题 15–16px，卡片标题 15px；使用既有 `text-*` 阶梯。
- 间距：页面 32、区块 24、组 16、行内 8/12；用间距替代边框。
- 圆角：控件 8px、面板 12px、大面板/对话框 16px、用户气泡 16px；不新增任意值。
- 过渡 120–200ms，统一 `--ease-standard`；不做全屏主题颜色渐变；`prefers-reduced-motion` 下关闭非必要动画。

## 7. 组件模式

| 区域 | 规范 |
|---|---|
| 应用壳 | 根 `canvas`；顶栏、对话侧栏、设置导航 `chrome`；内容面板 `panel`；浮层 `overlay` |
| 顶栏 | 40px 高，无底边线；品牌图标 `accent-soft`；窗口控制 hover 用 overlay/danger-soft |
| 对话侧栏 | `chrome`，无右边线；当前项 `accent-soft` + `accent-text`，不加左竖条；项目分组用间距/弱底纹 |
| 会话头 | `canvas`，无底边线；标题、目标徽标、操作靠间距组织 |
| 设置导航 | `chrome`，无右边线；选中项 `accent-soft` + `accent-text` |
| 设置分组 | `panel` 底，无 ring；标题可在面板外；设置行无 border，靠 12–16px 内距 |
| 选择控件 | 未选中 `control` 面；选中使用 `choice-selected` / `choice-selected-danger` + `foreground` 文字 + Check 图标；无布局边框 |
| 管理列表 | 一个 `panel` 容器包住连续行；行 hover 用 overlay；不要每项一张描边卡 |
| 对话正文 | 助手无气泡全宽；用户 `accent-soft` 气泡无 ring；当前轮只加底纹浓度 |
| 执行组 | 单块 `panel`；组头、内容、工具行用底纹/留白/缩进分层；不逐层套框 |
| 确认卡 | `*-soft` 底 + 状态图标 + 文字；无 border/左色条 |
| 思考过程 | 低对比底纹 + 图标 + 缩进；无左竖线 |
| Composer | `panel` 底、16px 圆角、无 border；聚焦不显示整块包边/ring，靠文本光标与内部控件自身焦点提示；拖拽用 `accent-soft` |
| 菜单/Popover/Dialog | overlay 底 + shadow；无 ring/border；选中项 `accent-soft` |
| 任务结果区 | `chrome` 独立底色；标签、文件行、预览区用底纹/间距；无 `border-l` |
| 空态 | panel/overlay 底 + 图标 + 标题 + 说明 + 主操作；禁止 dashed border |
| Toast/更新卡 | 只用语义令牌；状态色只出现在图标、标签、进度和软底纹 |

## 8. 主题应用

- 根元素使用 `data-theme` + `.dark`；主题应用模块集中处理，不在组件里重复。
- 切换时同步 `color-scheme`，让原生滚动条、表单控件和 WebView 默认控件跟随明暗。
- 首帧前从本地缓存同步应用主题预设/模式，再从后端校正；不得出现可见的闪白/闪黑。
- `system` 监听系统明暗变化；只改变 light/dark，不改变主题预设。
- 主题变化即时生效；不重新挂载 React 树，不做整页颜色动画。
- 主题设置沿用现有设置保存通道；旧明暗模式、字号、栏宽必须保留。

## 9. 可访问性门槛

| 配对 | 最低要求 |
|---|---|
| 正文 / canvas、chrome、panel、overlay | 4.5:1 |
| 元数据 / canvas、panel | 4.5:1 |
| accent-text / canvas、panel | 4.5:1 |
| 状态 text / 对应 soft 底纹 | 4.5:1 |
| primary-foreground / primary | 4.5:1 |
| focus ring / 相邻表面 | 3:1 |
| canvas / chrome / panel 相邻表面 | 1.05–1.30 |

补充：

- 所有交互可键盘操作；focus 不可被固定区域遮挡。
- 纯图标按钮必须有中文 `aria-label` 或 `title`。
- 主题卡片使用单选组语义和 `aria-checked`；选中态不能只靠颜色。
- 桌面指针目标建议 ≥ 28px；触控/粗指针 ≥ 44px。
- `prefers-reduced-motion`、`forced-colors` 与高对比主题必须走查。

## 10. 评审清单

- [ ] 是否只使用语义令牌，没有组件内 hex/palette 类？
- [ ] 布局 chrome、卡片、设置行、列表行、弹窗是否都没有常驻边框/ring？
- [ ] 导航/会话选中态是否使用 `accent-soft` + `accent-text`；分段/选择卡是否使用 `choice-selected` + `foreground`，而不是左竖条/描边？
- [ ] 确认卡/错误/警告是否同时有图标或文字，而不是只有彩色边框？
- [ ] 相邻表面在 100% 亮度下是否可感知，但没有明显色块跳变？
- [ ] light/dark 两套是否都完整且对比度通过？
- [ ] 键盘 focus、aria-label、reduced-motion 是否走查？
- [ ] 浏览器桩是否覆盖 Nord dark/light + 一个非 Nord 主题？
- [ ] 是否运行 `cd frontend && npx tsc --noEmit && npm test && npm run build`？
- [ ] 修改现有组件时，触达的容器/行/选中态是否已迁移，而不是复制旁边的旧 hairline？

任何规则冲突，先修订本文件或规格，再写代码；不要用一次性样式绕过。
