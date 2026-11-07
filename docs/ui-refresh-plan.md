# UI 质感重构方案

> 目标：把当前「能用但廉价」的界面升级为线性 / Raycast / Claude Desktop 一档的桌面工具质感。
> 本文只覆盖视觉与交互层，不改后端契约与 Wails 绑定。

## 一、结论先行

当前 UI 的「差」不是某一个组件没做好，而是**设计体系缺失**导致的三类问题同时存在：

1. **没有视觉身份**：默认主题是纯中性灰（`--primary` 深色下是近白色），强调色只在用户在设置里手动选一个后才出现。全屏找不到一个属于产品的主色。
2. **没有层级**：全站只有 3 层表面（背景 / `card` / `muted`），深浅相差 0.04–0.06 L；浅色主题下 `card` 与 `background` 都是纯白。所有分隔都靠 1px、10% 透明度的描边，于是界面看起来像线框图，而不是一个有空间的工具。
3. **没有排版节奏**：`text-xs`（12px）出现 138 次，10/11px 固定字号 63 处，页面标题只有 18px。中文没有独立的字体栈（Geist 不含 CJK 字形），中文回落到系统默认字体，字号又小，观感直接掉档。

再叠加两个放大问题：**框套框**（一条消息里最多 4 层嵌套边框）和**动效几乎为零**（只有 hover 变色，展开/收起是瞬间 mount/unmount）。

## 二、诊断证据（可复核）

| 维度 | 现状 | 证据 |
|---|---|---|
| 默认强调色 | 无。`ACCENTS.blue.color === ""`，`--primary` 保持近白/近黑 | `src/lib/theme.ts:12`、`src/index.css:58,93` |
| 表面层级 | 仅 bg 0.145 / card 0.205 / muted 0.269；浅色 card=bg=白 | `src/index.css:51-118` |
| 边框依赖 | `border` = 白 10%；Card 用 `ring-1 ring-foreground/10` | `src/components/ui/card.tsx:14` |
| 字号 | `text-xs`×138、`text-sm`×94、`text-[11px]`×44、`text-[10px]`×19 | 全库统计 |
| 中文适配 | `--font-sans: 'Geist Variable', sans-serif`，无 CJK 回退栈 | `src/index.css:10` |
| 字号设置 | 小/中/大改 root font-size，但 63 处 px 字号不跟随 | `src/lib/theme.ts:19-23` |
| 圆角 | 同时存在 4 / 8 / 10 / 18 / 26 / full 六档，另有 `rounded-[min(...)]` | 全库统计 |
| 状态色 | 48 处硬编码 amber/emerald/red；`ConfirmCard` 无 `dark:` 变体，`ToolCallGroup` 有 | `MessageBubble.tsx:35-45` vs `ToolCallGroup.tsx:18-21` |
| 阴影 | 全站 14 处 shadow-*，其中 3 处 `shadow-none` | 全库统计 |
| 动效 | `transition-colors`×22、`transition-all`×8；无进入/展开动画；仅 1 处 reduced-motion | 全库统计、`index.css:174` |
| 空态 | 💬 emoji + 一行 12px 灰字；引导卡 👋 | `ChatPage.tsx:2568-2576`、`Onboarding.tsx:11` |
| 布局缺陷 | Agents 远程区标题行 4 个子元素挤在 `justify-between`；设置页用 CSS `columns` 瀑布流 | `AgentsPage.tsx:381-428`、`SettingsPage.tsx:225-231` |
| 对话层级倒置 | 用户气泡 `bg-primary`（深色下近白），助手气泡 `bg-muted`（灰） | `MessageBubble.tsx:222,336` |

## 三、视觉方向

**Tinted Graphite + 单一品牌色**：微冷灰石墨底 + 一个青蓝主色，克制使用；靠表面色阶、发丝线、内高光建立空间，而不是靠描边和阴影堆叠。

- 参考气质：Linear（表面与描边纪律）、Raycast（密度与强调色克制）、Claude Desktop（正文与对话排版）。
- 明确不做：渐变堆叠、玻璃拟态、发光描边、彩色图标满天飞。

### 3.1 颜色令牌（`src/index.css`）

```css
:root {
  --background: oklch(0.985 0.002 265);
  --surface-1:  oklch(0.968 0.003 265);   /* 侧栏 */
  --card:       oklch(1 0 0);
  --elevated:   oklch(1 0 0);             /* 浮层 */
  --foreground: oklch(0.21 0.006 265);
  --muted-foreground: oklch(0.50 0.008 265);
  --border:        oklch(0.21 0.006 265 / 9%);
  --border-strong: oklch(0.21 0.006 265 / 16%);
  --brand:      oklch(0.55 0.19 258);     /* 默认主色，不再是空值 */
  --brand-soft: color-mix(in oklab, var(--brand) 10%, transparent);
  --success: oklch(0.58 0.14 155);
  --warning: oklch(0.66 0.14 75);
  --primary: var(--foreground);           /* 主按钮保持中性，强调色只做高亮 */
  --ring: var(--brand);
}

.dark {
  --background: oklch(0.175 0.006 265);
  --surface-1:  oklch(0.152 0.006 265);
  --card:       oklch(0.212 0.007 265);
  --elevated:   oklch(0.248 0.008 265);
  --foreground: oklch(0.958 0.003 265);
  --muted-foreground: oklch(0.728 0.008 265);
  --border:        oklch(0.96 0.003 265 / 8%);
  --border-strong: oklch(0.96 0.003 265 / 14%);
  --brand:      oklch(0.70 0.14 255);
  --brand-soft: color-mix(in oklab, var(--brand) 16%, transparent);
  --success: oklch(0.75 0.14 155);
  --warning: oklch(0.80 0.13 80);
  --shadow-card: inset 0 1px 0 oklch(1 0 0 / 4%);   /* 暗色内高光 */
}
```

要点：

- `--primary` 与 `--brand` 分离。主按钮仍是高对比中性色（白底黑字 / 黑底白字），品牌色只用于：当前会话行、链接、运行中指示、focus ring、发送按钮、图表。这是「高级感」的关键——强调色稀缺才有效。
- `--surface-1` 让侧栏从主画布中退后一步；`--elevated` 给 popover/hover 行/输入框聚焦态。
- 所有现有 `amber-*` / `emerald-*` / `red-*` 替换为 `warning` / `success` / `destructive` 语义令牌，浅深两套一次定义。

### 3.2 字体系统

```css
--font-sans: 'Geist Variable', 'PingFang SC', 'Microsoft YaHei UI',
             'Noto Sans SC', 'Source Han Sans SC', system-ui, sans-serif;
--font-mono: 'Geist Mono', 'JetBrains Mono', ui-monospace, Consolas, monospace;
```

字号阶梯（全部 `rem`，让「正文字号」设置真正生效）：

| 令牌 | 值 | 用途 |
|---|---|---|
| `--text-xs` | 0.75rem (12px) | 元数据下限，**禁止 10/11px** |
| `--text-sm` | 0.8125rem (13px) | 控件、列表、侧栏 |
| `--text-base` | 0.875rem (14px) | 次要正文、卡片描述 |
| `--text-md` | 0.9375rem (15px) | **对话正文**、表单、设置正文 |
| `--text-lg` | 1.0625rem (17px) | 卡片/区段标题 |
| `--text-xl` | 1.25rem (20px) | 页面标题 |
| `--text-2xl` | 1.5rem (24px) | 引导页主标题 |

中文正文行高 1.7，界面文字 1.45，标题 1.3。正文从 12px 提到 15px 是体感提升最大的一步。

### 3.3 圆角 / 间距 / 阴影 / 动效

- 圆角只保留四档：`6px`（控件）、`10px`（输入框/小卡）、`14px`（卡片）、`20px`（composer/对话框/用户气泡）。清除 `rounded-[min(...)]`、`rounded-[4px]`、`rounded-[2px]`。
- 间距按分区执行：页面 32、区块 24、组 16、行内 8/12。当前 `gap-2` 占绝对多数，说明所有东西都被压在同一密度里。
- 阴影只给浮层：
  - `--shadow-pop`: `0 4px 16px oklch(0 0 0 / 16%)`
  - `--shadow-dialog`: `0 16px 48px oklch(0 0 0 / 28%)`
  - 卡片在浅色下加 `0 1px 2px oklch(0 0 0 / 4%)`，深色下用内高光代替阴影。
- 动效令牌：`--dur-fast: 120ms`、`--dur: 180ms`、`--dur-slow: 240ms`；缓动 `cubic-bezier(0.2, 0, 0, 1)`。
- 动效清单：消息进场（opacity + 4px 上移，180ms）、执行组展开（`grid-template-rows: 0fr → 1fr`，220ms）、侧栏折叠（width，200ms）、行 hover（背景，120ms）、按钮按下（scale 0.98）。全局补 `prefers-reduced-motion` 归零。

## 四、组件级改造

### 4.1 对话流（体感权重最高）

| 元素 | 现状 | 改为 |
|---|---|---|
| 助手消息 | 灰色气泡，`bg-muted` | 去掉气泡，全宽正文（15px / 1.7），作者行在上方 12px 灰字 |
| 用户消息 | 近白高亮气泡，是全屏最亮元素 | 右对齐、`--brand-soft` 或 `--elevated` 底、20px 圆角、最大宽 640px |
| 执行组 | 外框 + 每条工具卡再套边框 | 一个平面容器，组头 + 发丝线分隔的行，行内不再各自描边 |
| 确认卡 | 独立琥珀边框 + 参数块再套框 | `warning-soft` 底 + 左侧 2px 警示条，参数区无边框、仅背景差 |
| 思考过程 | 虚线边框盒子 | 左侧 2px 竖线 + 静音文字，标题行可折叠 |
| 代码块 | `bg-muted` + 边框 + 11px 语言标签 | 独立代码底色、无边框、头部悬停显示复制；Phase 3 引入语法高亮 |
| 日期分隔 | 灰底胶囊 + 两条线 | 一条发丝线 + 居中 12px 标签 |

### 4.2 侧栏

- `bg-surface-1` + 右侧发丝线；主画布 `bg-background`，两层从此分开。
- 顶部加产品标识（14px 半粗 + 小 logo），「新聊天」降为中性描边按钮（32px 高），不再抢注意力。
- 会话行 34px 高、13px 字；当前行 `bg-brand-soft` + 左侧 2px 品牌色条；悬停操作 24px 目标。
- 分组标题 12px，统一带计数；空态给一句 13px 说明 + 一个文字按钮，不再 11px。

### 4.3 顶部栏

- 48px 高；标题 14px 半粗；目标徽章降为 `bg-elevated` 无边框。
- 右侧遥测收敛为一组：tokens 12px、上下文条 48×3px 品牌色、运行中胶囊。图标按钮 28px。

### 4.4 Composer

- 容器 `rounded-[20px] bg-card` + 发丝线 + 聚焦时 `border-brand/40` + `ring-4 ring-brand/10`。
- 输入 15px；占位文本缩到一句。
- 底行控件 28px 高、12px 字；发送按钮 28px 圆形，品牌色；停止用 destructive。
- 拖拽时整块变为 `brand-soft` 底 + 虚线品牌边框 + 居中提示。
- 底部提示行 12px，字号设置跟随。

### 4.5 管理页统一外壳

新建 `src/components/layout/`：

- `PageShell`：`mx-auto max-w-6xl px-8 py-7`。
- `PageHeader`：标题 20px / 描述 13px / 右侧动作区，底部 24px 留白。
- `Section`：标题 15px + 可选描述 + 右侧动作。
- `SettingRow`：左标签+描述、右控件，行间发丝线，垂直 16px。

逐页修复：

- **Settings**：废弃 CSS `columns` 瀑布流（顺序不可预测、卡片参差）。通用页改 `grid-cols-12`：左 8 列设置卡、右 4 列 sticky「外观预览」；其余标签页单列 `max-w-3xl`。
- **Agents**：修复远程区标题行动作错位（4 个直接子元素挤在 `justify-between`），改为标题左、按钮组 `flex gap-2` 右；Agent 列表从 2 列卡片改为单列行式列表（图标 32px + 名称 14px + 标签 12px + 悬停动作）。
- **Models / Teams / Skills / MCP**：同样的行式列表与 `PageHeader`，空态统一 `EmptyState`（56px 圆形图标 + 15px 标题 + 13px 描述 + 主按钮）。

### 4.6 空态与引导

- 删除 💬 / 👋 emoji，全部用 lucide 图标（`MessageSquare`、`Sparkles`）。
- 空态结构：图标圆 56px（`brand-soft` 底 + 品牌色图标）→ 标题 15px → 描述 13px → 主按钮。
- 引导卡：标题 24px、步骤行 44px 高、步骤号用品牌色。

## 五、实施阶段

| 阶段 | 内容 | 产出文件 | 预估 |
|---|---|---|---|
| **P0 令牌层** | 颜色/字体/圆角/阴影/动效令牌；CJK 字体栈；px 字号改 rem；语义状态色；清除硬编码调色板 | `index.css`、`theme.ts`、`tailwind` 主题映射 | 0.5–1 天 |
| **P1 对话主界面** | 侧栏、顶栏、消息流、执行组、确认卡、composer、回到底部 | `ChatPage.tsx`、`MessageBubble.tsx`、`ToolCallGroup.tsx`、`ToolCallCard.tsx` | 2–3 天 |
| **P2 管理页外壳** | `PageShell/PageHeader/Section/SettingRow`；6 个管理页重排；空态统一 | 新增 `components/layout/*`，`pages/*` | 2 天 |
| **P3 动效与精修** | 进场/展开/折叠动效；focus-visible 全覆盖；reduced-motion；代码高亮；图标尺寸统一 | 各组件 + `index.css` | 1–2 天 |
| **P4 验证与固化** | stub 多视口/双主题截图基线；`docs/design-system.md`；对比度检查 | `docs/`、`stub/` | 0.5–1 天 |

建议顺序：P0 → P1（先做对话页，用户感知 70% 来自这里）→ P2 → P3 → P4。P0 与 P2 的布局外壳可以并行，但同一文件不能并发改。

## 六、验收标准

1. 默认深色主题下，全屏只有一个品牌色焦点（当前会话行 / 发送按钮 / 运行指示），主按钮保持中性。
2. 任意界面中，正文 ≥ 14px、元数据 ≥ 12px；「正文字号」三档切换对所有文字生效。
3. 表面层级 ≥ 4 层（背景 / 侧栏 / 卡片 / 浮层），浅色主题下卡片与背景可区分（不依赖描边）。
4. 单条消息内嵌套容器 ≤ 2 层；无「框套框」。
5. 所有可交互元素有 hover + focus-visible；图标按钮有 `aria-label`。
6. `prefers-reduced-motion` 下无位移动画。
7. 浅色/深色 × 1280/1440 四组截图人工走查通过；`npx tsc --noEmit && npm test && npm run build` 全绿。
8. 对比度：正文 ≥ 4.5:1，大字与图标 ≥ 3:1（用浏览器取色复核关键组合）。

## 七、风险与边界

- 只改样式与前端结构，**不动 Wails 绑定、后端 RPC、`frontend/bindings/**`**。
- 主题令牌与 shadcn 变量同名，避免组件层大改；`--primary` 语义调整需同步检查所有主色用法（当前 `bg-primary` 26 处、`text-primary` 33 处、`border-primary` 13 处）：其中「强调/选中/链接/运行态」类实例迁移到 `brand` 系列，其余（主按钮、用户气泡若保留填充）保持中性。
- 字号整体上调会改变既有布局密度，必须配合 padding 调整，否则会显得拥挤；建议在 P1 一次性完成，避免分批漂移。
- 浅色主题当前是「白纸」状态，改动后需单独走查，不能只验证深色。