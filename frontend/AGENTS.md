# frontend/AGENTS.md — UI 实现硬约束

适用范围：`frontend/**`。本文件优先于根 `AGENTS.md` 中与 UI 冲突的描述；设计意图见 `docs/specs/ui-surface-redesign.md`（GitHub #80），令牌与组件规则以 `docs/design-system.md` 为准。实现时不要自行发明主题、表面、边框或强调色模式。

Nord 表面契约已落地；历史实现与旧文档只用于参考行为，不作为样式范例。新增或改动的 UI 必须按本文件执行。

## 1. 非协商项

1. **默认主题是 Nord**，主题预设与明暗模式是两个独立设置：`nord | graphite | paper | contrast` × `light | dark | system`。不要做“每个页面一套主题”或“每个 Agent 一套主题”。
2. **布局默认无线条**：顶栏、对话侧栏、设置导航、会话头、卡片、列表行、工具组、确认卡、composer、对话框、popover 不得用常驻 `border` / `border-b` / `border-r` / `ring-1` 做分区。
3. **用表面表达层级**：`canvas` → `chrome` / `panel` → `overlay`。相邻表面只差一到两级明度，目标对比比例约 1.05–1.30；不得用高反差色块或阴影堆叠替代布局。
4. **选中态不用左侧竖条**：当前会话、当前设置标签、选中候选统一使用 `accent-soft` 底纹 + `accent-text` 文字 + 可访问状态，不用 `border-l-2`、左侧色条或描边。分段控件例外：未选中用 `control` 面，选中用 `choice-selected` / `choice-selected-danger` + `foreground` 文字 + Check 图标（见 `docs/design-system.md` §5.4）。
5. **强调色稀缺**：`accent` 只用于选中、focus、运行态、少量链接和关键图标；主按钮用中性高对比 `primary`，不把强调色铺满页面。
6. **状态不能只靠颜色**：确认、错误、警告、成功、运行中必须同时有图标或文字，并使用 `*-soft` 底纹，不用“只有彩色边框”的状态卡。
7. **禁止组件内写死颜色**：不得新增 `#2E3440` 这类十六进制、`bg-zinc-*` / `text-amber-*` / `emerald-*` / `red-*` 调色板，或 `bg-[#...]` 任意值。颜色只能来自主题语义令牌。
8. **每个新主题必须完整**：同时提供 light/dark 两套令牌，并通过正文、元数据、状态文字、主按钮与 focus ring 的对比度测试；不允许半套主题。
9. **无障碍是硬门槛**：正文 ≥ 4.5:1，大字/图标 ≥ 3:1，focus ring ≥ 3:1；键盘可达、可见 focus、`aria-label`、`prefers-reduced-motion` 必须具备。
10. **不允许用规则例外绕过约束**：如果现有实现与规则冲突，先改实现；如果规则确实需要变更，先更新 `docs/design-system.md` 与规格，不要就地发明特例。
11. **触达即清理**：修改现有组件时，至少把它涉及的容器、行、选中态迁移到本契约；不得复制旁边的旧 border/ring 写法。

## 2. 令牌与主题映射

迁移期间允许保留以下别名，但新代码必须按“语义角色”理解，不能按旧 `border` 习惯使用：

| 语义角色 | 新命名 | 现有别名 | 用途 |
|---|---|---|---|
| 主画布 | `canvas` | `background` | 页面底色、聊天阅读区、设置内容区 |
| 全局 chrome | `chrome` | `surface-1` / `sidebar` | 顶栏、对话侧栏、设置导航 |
| 内容面板 | `panel` | `card` | 执行组、composer、设置分组、管理列表容器、任务结果面板 |
| 浮层 | `overlay` | `elevated` / `popover` | 菜单、对话框、toast、hover 浮层 |
| 控件面 | `control` | `control` | outline 按钮、复选框等可识别控件填充；不用于布局分区 |
| 强调 | `accent` / `accent-text` / `accent-soft` | `brand` 系列 | 选中、focus、运行态、少量链接 |
| 中性主操作 | `primary` / `primary-foreground` | 现有 `primary` | 主按钮，保持中性高对比 |
| 功能线 | `border` / `line` | 现有 `border` | 仅控件与数据结构的必要边界 |

- 主题颜色只允许在主题令牌定义中声明，组件只消费语义 token。
- `accent` 是装饰/底纹色；文字和链接使用 `accent-text`；不要假设二者相同。
- 状态色使用 `danger` / `warning` / `success` 及 `*-soft` / `*-text` 配对；Nord 原始 Aurora 色只能做填充/图标，文字使用可读变体。
- 主题切换统一由主题应用模块设置 `data-theme` 与 `.dark`，并同步 `color-scheme`；组件不要自己读写 localStorage 或监听系统主题。

## 3. Nord 基线

Nord 使用官方 Polar Night / Snow Storm / Frost / Aurora 作为身份基础。以下是实现基线，最终以 `docs/design-system.md` 的令牌表为准：

| 角色 | Nord 深色 | Nord 浅色 |
|---|---|---|
| canvas | `#2E3440` | `#ECEFF4` |
| chrome | `#3B4252` | `#E5E9F0` |
| panel | `#434C5E` | `#FFFFFF` |
| overlay | `#4C566A` | `#FFFFFF` |
| control | `#485265` | `#EBEFF4` |
| foreground | `#ECEFF4` | `#2E3440` |
| muted foreground | `#D8DEE9` | `#4C566A` |
| accent | `#88C0D0` | `#5E81AC` |
| accent text | `#9BD3E0` | `#4C6A91` |
| danger text | `#F5C2C7` | `#A63D4A` |
| warning text | `#EBCB8B` | `#7A5A12` |
| success text | `#BED6AB` | `#3F6B3F` |
| focus ring | `#88C0D0` | `#4C6A91` |

不要直接照搬 Nord 原始 Aurora 色作为正文：原始红色/橙色/绿色在部分表面上低于 4.5:1。需要新增文字色时，优先在同色相内调整明度并补测试。

## 4. 允许与禁止的线

**允许的功能线**：

- 输入框、Textarea、Select、Checkbox、Radio 的必要控件边界；优先用填充面 + focus ring，边界只用于可识别性或 forced-colors。
- focus ring（2px 强调色，3:1 以上）。
- 对话侧栏 resize 手柄；静止透明，hover/drag 才显示 2px 提示。
- Markdown 表格、代码块、diff 中的数据结构线。
- 高对比主题与系统 forced-colors 下的可访问性边界。

**禁止的布局线**：

- `TopBar` 的 `border-b`。
- 对话侧栏、设置侧栏的 `border-r`。
- 会话头、搜索栏、设置工具栏的 `border-b`。
- Card / Dialog / Popover 的 `ring-1 ring-border` 或常驻 `border`。
- 设置行、管理列表行、工具行、文件行的逐行 border。
- 选中项的 `border-l-2`、左侧色条、强调色描边。

## 5. 组件落地规则

| 区域 | 必须 |
|---|---|
| 顶栏 | `chrome` 底、无底边线；品牌图标用 `accent-soft` 小底；窗口控制 hover 用 `overlay` / `danger-soft` |
| 对话侧栏 | `chrome` 底、无右边线；当前会话 `accent-soft` + `accent-text`；项目分组用间距与弱底纹 |
| 会话头 | `canvas` 底、无底边线；标题/徽标/操作靠间距组织 |
| 设置导航 | `chrome` 底、无右边线；选中项 `accent-soft`，不用左侧竖条 |
| 设置分组 | 标题在面板外或面板内均可，但面板用 `panel` 底、无 ring；设置行无 border，靠 12–16px 垂直内距 |
| 管理列表 | 一个 `panel` 容器包住连续行；行 hover 用 `overlay`，不要每项一张描边卡 |
| 对话正文 | 助手无气泡全宽正文；用户 `accent-soft` 气泡、无 ring；当前轮只加底纹浓度 |
| 执行组 | 单块 `panel`；组头、内容、工具行用底纹/留白/缩进分层，不再逐层套框 |
| 确认卡 | `warning-soft` / `success-soft` / `danger-soft` 底 + 状态图标 + 文字；无 border/左色条 |
| 思考过程 | 低对比底纹 + 图标 + 缩进；无左竖线 |
| Composer | `panel` 底、16px 圆角、无 border；聚焦不显示整块包边/ring，靠文本光标与内部控件自身焦点提示；拖拽用 `accent-soft` |
| 菜单/Popover/Dialog | `overlay` 底 + shadow；无 ring/border；选中项 `accent-soft` |
| 任务结果区 | `chrome` 独立底色；标签、文件行、预览区用底纹/间距；无 `border-l` |
| 空态 | `panel` / `overlay` 底 + 图标 + 标题 + 说明 + 主操作；禁止 dashed border |
| Toast/更新卡 | 只使用语义令牌；状态色只出现在图标、标签、进度和软底纹 |

## 6. 排版、间距与动效

- 保留 `Geist Variable` + 中文回退栈；不要换字体或图标库。
- 正文 15px/1.7；控件与列表 13px；元数据下限 12px；禁止 10/11px 中文。
- 间距节奏：页面 32、区块 24、组 16、行内 8/12；用间距承担分组，不要用边框补空间。
- 圆角：控件 8px、面板 12px、大面板/对话框 16px、用户气泡 16px；不新增任意圆角值。
- 过渡 120–200ms，统一 `--ease-standard`；不做全屏主题颜色渐变动画；`prefers-reduced-motion` 下关闭非必要动画。

## 7. 可访问性

- 所有正文/元数据/状态文字/主按钮达到 WCAG AA；focus ring 与相邻表面至少 3:1。
- 所有交互可用键盘操作；focus 必须可见且不被固定区域遮挡。
- 纯图标按钮必须有中文 `aria-label` 或 `title`；主题与权限选择使用分段单选组语义和 `aria-checked`。
- 状态不只靠颜色；选中态同时有 `aria-pressed` / `aria-checked` 或可见文字。
- 桌面指针目标建议 ≥ 28px；触控/粗指针 ≥ 44px。
- `forced-colors` 与高对比主题下允许恢复清晰边界，但不能把边界带回默认主题。

## 8. 明令禁止

- 不要给 Card、Dialog、Popover、设置行、列表行新增 `border` 或 `ring-1`。
- 不要用 `bg-brand/10` 这类散落透明度代替 `accent-soft`；不要用左竖条表示选中。
- 不要在组件里写 `#hex`、`rgb()`、`oklch()` 或 Tailwind 调色板类；主题预览也必须使用真实 token。
- 不要只实现深色主题、不要给主题加单页覆盖、不要保留旧的自由 accent 选择器。
- 不要在动画里做整页颜色淡入淡出、发光、玻璃拟态或大面积渐变。
- 不要把“无线条”理解成删除 focus ring、复选框边界或高对比模式边界。
- 不要在未更新规格的情况下新增第五套主题或自定义主题格式。

## 9. 提交前检查

1. 新增/修改的 UI 是否符合 `docs/design-system.md` 的语义令牌和表面映射？
2. 是否出现了任何布局级 border/ring？是否把选中态做成了左侧竖条？
3. 是否在组件里写死了颜色或调色板类？
4. 是否覆盖 light/dark，且对比度测试通过？
5. 键盘 Tab、focus ring、aria 标签、reduced-motion 是否走查？
6. 浏览器桩是否覆盖 Nord dark/light + 至少一个非 Nord 主题？
7. 运行 `cd frontend && npx tsc --noEmit && npm test && npm run build`；涉及视觉走查时运行 `npm run stub`。

如果某条规则无法满足，先停下并修订 `docs/design-system.md` 或规格，不要用一次性样式绕过。
