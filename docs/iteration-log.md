# 迭代日志

本文件按轮次记录每次功能迭代的依据、实现要点与测试证据。最新记录在最上方。

## 视觉测试与体验设计

### 第二百三十一轮迭代：模型首包前显示闪动思考状态（已实施）

依据：用户反馈发出消息后、模型首个事件到达前，消息区没有任何显示，只是侧栏转圈，难以判断任务是否仍在执行；期望参考 Codex 桌面端，在回复起始位置显示闪动的运行状态。

**实现要点**

- **判定口径**：新增 `isAwaitingReply`，仅在会话正在运行且最后一条消息仍是用户消息（或空会话）时成立；首个 `thinking_delta` / `delta`、工具调用、确认卡或错误到达后立即退出，避免与思考块、流式气泡和工具卡重复。
- **挂载位置**：`TurnBlock` 新增 `waiting` 行，紧贴用户气泡下方、占用未来助手回复的首行位置；按用户消息 ID 定位，消息过滤时不会把状态错挂到旧轮次。空会话首包前显示独立状态行，并暂时隐藏“发送第一条消息”空态。
- **视觉**：新增 `TurnStatus`，文字使用渐变微光扫过，后接三个复用 `wb-dot-pulse` 的呼吸点；2 秒后显示 `Ns` 耗时。状态不进入消息数组，因此不会被持久化、分组或导出。
- **可访问性与动效**：视觉状态标记 `aria-hidden`，沿用既有读屏状态区；`prefers-reduced-motion` 下关闭渐变并回落为静态灰色文字。
- **滚动**：自动滚底 effect 增加 `running` 依赖，首包等待行出现时也能跟随到可视区域。

**测试证据**

- `chatUtils.test.ts` 覆盖空会话、用户末条、非运行态，以及思考流/正文/工具调用/工具结果/确认卡到达后的退出。
- `TurnBlock.test.tsx` 断言等待态位于用户气泡下方、不产生消息正文；`TurnStatus.test.tsx` 断言微光类、三点呼吸、`aria-hidden` 与 2 秒计时阈值。
- `npx tsc --noEmit`、`npm test`（24 个测试文件、187 个用例）、`npm run build`、`go test ./internal/...` 全绿。本轮无后端改动。

### 第二百三十轮迭代：会话标题默认提炼与侧栏单行化（已实施）

依据：用户截图反馈左侧会话行显示 `/stats` 原样标题，第二行回复摘要让列表显得臃肿；期望标题由主模型提炼，或可指定标题模型，并且只占一行。

**实现要点**

- **根因**：`Settings.AutoTitle` 原先默认关闭，用户配置没有该键时完全不会发起标题侧调用；旧侧调用还复用会话目标模型，弱模型或远程目标会进一步降低标题质量。第二行是 `ChatSession.Preview` 的常驻渲染，与标题共同撑高行高。
- **默认与模型解析**：`AutoTitleEnabled` 改为未配置即开启（显式 `false` 才关闭）；新增 `Settings.TitleProviderID/TitleModel`，未指定时回落全局默认供应商的首个模型（主模型），不受会话 `ModelOverride` 或远程目标影响。
- **标题质量**：提示词明确要求一行、≤12 个汉字、不得把斜杠命令/代码原样作为标题；输出取首行并清理引号、书名号、Markdown 标记，斜杠开头视为无效并保留兜底，最终上限 16 字。
- **历史会话**：新增 `ChatService.RefineSessionTitle`，读取会话首条用户消息与最近助手回复重新提炼；会话行菜单与当前会话「更多操作」均提供「重新提炼标题」。
- **侧栏单行**：抽出门面组件 `SessionRowLabel` 只渲染标题，回复摘要仅保留在原生悬停提示中；设置页新增「标题提炼模型」选择器，默认「跟随主模型」。

**测试证据**

- 红灯→绿灯：`TestAutoTitleUsesMainModelByDefault` 修复前 2 秒后标题仍为 `/stats`；修复后请求主模型并替换为「会话统计命令」。
- `TestAutoTitleGeneratesTitle` 断言显式标题模型优先于会话覆盖模型；`TestRefineSessionTitleCleansExistingFallback` 覆盖历史 `/stats` 会话重提炼；`TestAutoTitleDisabledKeepsFallback` 保留显式关闭契约；`TestAutoTitleDefaultsOn` 锁定默认开启与 false 持久化。
- `SessionRowLabel.test.tsx` 断言可见文本只有标题、预览仅存在于 tooltip；`npm test` 23 文件 182 用例、`npx tsc --noEmit`、`go test ./internal/services`、`go test ./internal/config` 全绿。

### 第二百二十九轮迭代：已到底时自动隐藏「回到底部」浮钮（已实施）

依据：用户截图反馈，消息已经滚动到底部时向下箭头仍然常驻，让人误以为尚未到底。

**实现要点**

- **根因**：`atBottom` 原先只在消息视口触发 `scroll` 事件时重算。侧栏折叠、窗口缩放、输入区高度变化或 Markdown 重排会移动消息末尾、改变可视范围，却不产生 `scroll` 事件；旧的「离底」状态因此一直保留。已到底时再点按钮也不会产生滚动事件，无法自愈。
- **可见性来源**：`ChatPage` 改用 `IntersectionObserver` 观察消息末尾的 `bottomRef`，以其可见性驱动 `atBottom`；root 绑定消息视口，`rootMargin` 底部扩展 120px，保持与自动滚动一致的「近底」阈值。滚动监听仍保留，但只负责轮次导航当前项。

**测试证据**

- 视觉回归桩红→绿：长会话滚动至 `diff=160px` 时浮钮出现；收起侧栏后正文重排至 `diff=33px` 且 `scrollTop` 保持 693 不变。修复前浮钮仍显示，修复后自动隐藏。
- 常规滚动：初始近底 `diff=23px` 不显示；`PageUp` 后 `diff=610px` 显示；`End` 后 `diff=0` 隐藏。
- `npx tsc --noEmit`、`npm test`（23 个测试文件、182 个用例）与 `npm run build` 全绿。仓库无浏览器 e2e 基建，本轮交互回归以视觉桩实测为准。

### 第二百二十八轮迭代：托盘唤回不再跳到右下角（已实施）

依据：用户反馈窗口从托盘单击打开时会跳到屏幕右下角，而不是保持居中。

**实现要点**

- **根因**：Wails `AttachWindow` 的 smart default 在每次显示窗口前调用 `PositionWindow`；Windows 实现按任务栏托盘位置计算坐标，强制把窗口贴到右下角。
- **托盘接线**：移除 `AttachWindow` / `WindowOffset`，改用显式 `OnClick` 切换；托盘、菜单、全局快捷键与二次启动统一走 `toggleMainWindow` / `showMainWindow`，只恢复显隐与焦点，不修改窗口位置。
- **最小化语义**：Windows 上最小化窗口仍报告 `IsVisible`，切换逻辑增加 `IsMinimised` 判断，确保托盘单击恢复窗口而不是再次隐藏。

**测试证据**

- 回归红→绿：新增 AST 守护测试，修复前在 `main.go` 检出 `AttachWindow` / `WindowOffset` 并失败；移除后通过。新增 `TestToggleMainWindow` 覆盖隐藏恢复、普通可见隐藏、最小化恢复与 nil 安全。
- `go test ./internal/... .`、`go vet ./internal/... .`、`wails3 build` 全绿。
- 真实 Wails 窗口冒烟（临时源码覆盖，不落库）：初始矩形 `320,106,1280×820`；隐藏后 `IsVisible=false`；再次唤回后仍为 `320,106,1280×820`，未跳到右下角。

### 第二百二十七轮迭代：MCP 保存即连接并移除冗余入口（已实施）

依据：用户截图反馈 MCP 卡片同时提供「启用」开关与「连接」按钮，语义重复；配置内容保存后不应再要求手动点击连接，期望默认按启用状态自动连接，界面只保留启用/停用、编辑与删除。

**实现要点**

- **期望状态语义**：`Config.SaveMCPServer` 改为返回规范化后的 `MCPServerConfig`，前端新增服务时能立刻拿到后端生成的 ID；已启用配置保存成功后复用既有 `MCP.Connect` 立即应用，停用配置保存后仅刷新状态，沿用后端 changed 判定自动断开。
- **入口收敛**：删除 MCP 卡片行内「连接」按钮与相关 `Play` / `RefreshCw` 图标、`busy` 死状态；连接过程只通过「连接中 / 已连接 / 错误 / 等待浏览器授权」状态呈现。`toggleEnabled` 与编辑保存共用同一保存即应用路径。
- **视觉桩**：为 stub 增加已启用 `context7` 样例、保存返回规范化服务、`MCP.Status` / `Connect` 健康状态，使 MCP 页可独立浏览器走查。

**测试证据**

- 回归红→绿：新增 `TestSaveMCPServerReturnsNormalizedServer` 前目标测试因旧签名编译失败；实现后通过。源级断言从检测到行内连接按钮（`RED ... <Play ...>`）变为无匹配（GREEN）。
- 视觉桩实测：MCP 卡片显示 `context7 / 已连接 / stdio / 1 个工具`，按钮计数为 `connect=0、edit=1、switch=1`。
- `npx tsc --noEmit`、`npm test`（22 文件 180 用例）、`npm run build`、`go test ./internal/...`、`go vet ./internal/... .` 全绿；Wails 绑定已重新生成。

### 第二百二十六轮迭代：对话拆卡与轮次悬停预览（已实施）

依据：用户反馈第二百二十五轮的「整轮一张卡」不符合对话阅读习惯——用户输入应右侧独立气泡，助手回复左侧独立区域，两者不共用外卡；同时保留左侧轮次导航，并补齐参照截图中的「悬停横杠预览输入」效果。

**实现要点**

- **拆卡**：`TurnCard` 改为 `TurnBlock`——用户输入与助手回复成为两个兄弟块，外层不再有 `rounded-2xl ring-1` 卡片与整轮高亮；当前轮只给用户气泡加 brand 描边/底色。`groupTurns`、`TurnItem`、live 时钟与执行块归属不变。
- **用户气泡**：`UserQuote` 从卡内单行引题恢复为右对齐气泡（`max-w-[85%] sm:max-w-[640px]`、`rounded-2xl rounded-br-md`、`bg-brand/10 ring-brand/15`），保留 `id` / `data-turn-anchor` 供滚动跟随与导航定位；复制/编辑按钮回到气泡下方 hover 显示。
- **导航固定**：`TurnRail` 固定在会话区最左侧（消息视口 `left-2`），不再随居中的对话列左右漂移。新 `.chat-col` 在 md+ 为对话列保留每侧至少 60px 侧沟，宽栏/窄窗下优先收窄输出宽度，保证横杠不压正文。
- **悬停预览**：`RailTurn` 增加 `fullText`；`TurnRail` 增加 hover/focus 预览卡（首行标题或「第 N 轮」、全文、复制按钮）。预览卡通过 Portal 优先开在横杠**右侧的左侧沟槽**；右侧沟槽不足 180px 时翻到左侧、盖侧栏而不是输出区；两侧都不足时抑制预览。移出 140ms 延迟关闭，侧栏切换、窗口缩放或滚动时立即关闭；少于 2 轮或窄于 md 仍隐藏。
- **测试缝**：新增 `TurnBlock.test.tsx` 结构断言（用户气泡与正文为独立块、外层无卡、active 只高亮气泡）与 `TurnRail.test.ts` 标题规则 + 预览几何断言（右侧优先、左侧回退、两侧不足返回 null、卡片不越过输出列）；仓库无浏览器 e2e，交互仍以视觉桩走查为准。

**测试证据**

- 本轮相关类型检查通过（`npx tsc --noEmit --noUnusedLocals false`；工作区并行的 `MCPPage.tsx` 未使用 `busy` 会让常规 `tsc` 失败，不属本轮改动）；`npm test` 22 个文件 180 用例全绿；`npx vite build --mode production` 通过。
- 视觉桩（`npm run stub`）实测：1280 宽、侧栏展开、72% 栏宽时，导航条固定在 x=254（会话区最左 +8px），输出区 x=407.4，预览翻到左侧，右缘 242，未覆盖输出区；1280 宽、侧栏展开、56% 栏宽时，输出区 x=488.2，预览在右侧沟槽 x=266–452.2，未覆盖输出区；1280 宽、侧栏折叠、72% 栏宽时，两侧空间不足，预览抑制，横杠仍可点击（x=8，输出区 x=195.8）。
- `go test ./internal/...` 与 `go vet ./internal/... .` 通过。

### 第二百二十五轮迭代：轮次导航条与用户输入引题卡（已实施）

依据：用户两张参照截图——左侧竖排「轮次导航条」（每轮用户输入一个横杠，当前轮高亮白色）与选中轮的用户输入紧凑引题样式（单行截断省略号、行尾图标、与本轮响应同卡）。期望聊天页支持按轮导航，选中态按参照样式高亮。

**实现要点**

- **分组层**：`chatUtils.ts` 新增 `TurnItem` / `RenderItem` 与 `groupTurns`——用户消息作为轮头，其后到下一条用户消息之间的执行块与回复归入同一轮；首轮之前的孤儿条目（如失败发送残留的错误气泡）保持独立渲染不丢失。`chatItemStartTs` / `chatItemEndTs` 支持 turn 时间跨度，日分隔线语义不变。
- **渲染层**：新增 `TurnCard`（引题头 + 本轮执行块与回复渲染在同一张圆角卡内，选中轮加 brand 描边高亮）；`MessageBubble` 抽出 `UserQuote`——用户输入由右对齐完整气泡改为紧凑引题行：默认单行截断出省略号，点击展开/收起全文并选中本轮，复制按钮常驻，「编辑并重发」hover 保留，附件缩略图与灯箱原样迁移。
- **导航层**：新增 `TurnRail`——消息区左缘绝对定位窄条，横杠宽度按输入长度分档（固定 class 阶梯避免 Tailwind 清除），hover 以 title 显示该轮输入摘要，少于 2 轮或窄于 md 断点时隐藏；`ChatPage` 复用 scroll 监听（与 atBottom 同一 handler）以「视口上缘 200px 内最后一个轮锚点」跟随当前轮，rail/引题点击 `scrollIntoView` 平滑定位并标记 active，切会话时重置。
- **联动语义**：执行块 live 时钟由原「最后一条用户消息之后」改为「最后一个 turn」，renderBodyItem 复用平面列表的完整 prop 接线；渲染窗口省略计数按 turn 展开统计；消息数据模型与 `ChatStreamEvent` 未动，「空输入框 ↑ 编辑最后一条用户消息」等逻辑不受影响。

**测试证据**

- `chatUtils.test.ts` 新增 `groupTurns` 三用例（turn 折叠结构、孤儿条目独立渲染、day-separator 时间跨度含空 body）；`npx tsc --noEmit` 通过；`npm test` 20 个测试文件 171 个用例全绿；`npm run build` 通过。
- 视觉回归桩走查（`npm run stub`，iab 1280 视口）：`sess_direct` 增补长文本双轮样例后，轮次卡结构（引题 + 回复同卡）、rail 双杠渲染、点击第 1 轮平滑滚动且白色高亮随点击切换、引题单行截断与点击展开全文均实测通过；平滑滚动过程中高亮实时跟随（滚动跟随与点击共用同一监听），会话切换后高亮落在末轮。
- 修复走查中发现的 React key 告警：TurnCard body 列表补 `Fragment key={item.id}`，刷新后告警消失（保留的主干既有告警为 DropdownMenuTrigger + Button ref 模式，与本次无关）。
- 回归缝说明：仓库无浏览器 e2e 基建，rail 交互回归依赖桩走查人工复核；`frontend/stub/main.tsx` 样例仅服务视觉回归。

### 第二百二十四轮迭代：会话模型选择器移除「默认模型」伪选项（已实施）

依据：用户截图反馈（Windows，会话 composer 模型菜单）未设置会话覆盖时首行「默认模型」被选中，期望不显示该行，改为在真实模型后打勾；未显式选择时默认第一个模型。

**实现要点**

- **选中值改为生效模型**：`ChatPage` 的受控 `DropdownMenuRadioGroup` 不再使用 `__default` 哨兵，直接以 `target.model`（直聊无覆盖时即默认供应商第一个模型）或草稿解析出的首个模型作为 `value`；Agent 未覆盖时仍勾选其实际配置模型，与后端 `EffectiveModel` 口径一致。
- **移除伪选项**：删除「默认模型 / Agent 默认模型」菜单行，以及选择器与 toast 中的 `__default` 分支；选中模型由既有 `DropdownMenuRadioItem` 右侧 `CheckIcon` 标识。显式切换后仍通过 `SetSessionModel` 持久化真实模型 ID，后端空覆盖语义与其他调用方不受影响。
- **兜底**：覆盖为空或已失效、或模型解析缺失时，选择器回落 `composerModels[0]`，保证始终勾选一个真实模型；无供应商时整个入口保持隐藏。

**测试证据**

- 浏览器桩红→绿：未覆盖会话修复前菜单为「默认模型（Value: 1）/ GPT-5 Mini（Value: 0）」；修复后同一路径仅剩真实模型，GPT-5 Mini 默认 `Value: 1`，切到 GPT-5 Max 后勾选随选中项移动，新聊天草稿态同样默认勾选 GPT-5 Mini。
- `npx tsc --noEmit` 通过；`npm test` 20 个测试文件、168 个用例全绿；`npm run build` 通过。
- 回归缝说明：选择器为 Radix 受控单选组，仓库无浏览器 e2e 基建；本次以视觉回归桩完成红→绿走查，未新增依赖。

### 第二百二十三轮迭代：设置页内容宽度随窗口自适应（已实施）

依据：用户反馈设置页在宽窗口下内容仍等宽固定（截图：通用页卡片居中 1024px、两侧大片留白），期望内容宽度随实际窗口调整。上一轮（第二百二十一轮）已将对话栏改为比例自适应，设置页容器未同步。

**实现要点**

- **自适应宽度**：`SettingsPage.tsx` 通用/数据页容器由固定 `max-w-5xl`（64rem=1024px）改为 `max-w-[clamp(64rem,88%,90rem)]`——下限 64rem 保证中窗布局零回退，中间 88% 随窗口比例增长，上限 90rem（1440px）保可读性；知识库/关于页维持单列窄宽（既有注释声明的设计），管理子页 `PageShell` 的同模式固定宽留作后续处理。
- **视觉回归桩修复**：`stub/main.tsx` 全部 34 个硬编码方法 ID 因绑定重生成整体失效（GetConfig 等全部路由未命中返回 null，设置页在桩中加载失败），改为运行时按 FNV-1a32(`agentmate/internal/services.<Service>.<Method>`) 计算方法 ID（与 AGENTS.md 记录的生成器约定一致），根除整桩腐烂。

**测试证据**

- 红→绿回路（playwright-core + 系统 Edge 驱动 `npm run stub`，实测设置通用页容器宽度）：修复前 1280/2048 两视口均为 1024px（growth=0，RED「等宽」）；修复后 1280→1024px（零回退）、2048→1440px（growth=+416px，GREEN），并在 2048 视口截图走查双列布局无溢出。
- `npx tsc --noEmit`、`npm test`（168/168）、`npm run build` 全绿。
- 回归缝缺口说明：仓库无浏览器 e2e 基建且 `frontend/package-lock.json` 属 Integrator 专改文件，无法引入 playwright 依赖落库；本次以一次性回路脚本验证，宽度回归依赖桩走查人工复核。

### 第二百二十二轮迭代：运行时代理自动加载 AGENTS.md 工程指南（已实施）

依据：人工会话按 implement 技能实现 issue #79 与 ADR-0003（非自动修复流水线，故直接在主 checkout 追加本记录）。用户在项目根写的 AGENTS.md 此前对运行时代理完全不生效——系统指令唯一来源是用户配置的 SystemPrompt，后端与前端对 AGENTS.md 零命中；同一份文件在 Codex 等编码代理里却被自动遵循。设计经拷问会话定稿：全局 `~/.agents/AGENTS.md` 与有效工作区根两份叠加、冲突以后文为准、不做嵌套发现（ADR 记录的有意简化）、全文注入不截断、单份超 48KB 追加警告、缺失即跳过。

**实现要点**

- **解析函数**：新增 `config.GlobalAgentsFile`（`~/.agents/AGENTS.md`，Windows 走 `USERPROFILE`）与 `config.EffectiveAgentGuides(workspace, globalFile)`——全局在前、工作区在后；原文全文注入（不裁剪首尾），单份超过 48KB（与 `@文件` 单文件上限同源阈值，警告文案复用同一常量）在块末追加中文体积警告（KB 向上取整）；缺失、不可读、二进制/非法 UTF-8、空白文件一律跳过，不阻塞会话。
- **注入点**：Kit 构造时捕获 `globalAgentsFile`；新增 `Kit.appendAgentGuides`，`llmConfig` 在基础指令（含空回退）后叠加，团队自动协调员指令同样叠加——装配顺序为角色提示词 → 工程指南 → 技能正文（`skillAwareInstruction` 仍在其后追加）。团队负责人与成员经 `llmConfig` 自动生效；远程 A2A 客户端无本地指令提供方，结构性排除。
- **无前端改动**：不改页面、不生成绑定、无设置项（豁免方式为删除或重命名文件）。

**测试证据**

- 低缝：`internal/config/agents_guides_test.go` 六例（仅工作区、双份顺序、超限不截断+警告、缺失/不可读/二进制/空白跳过、空工作区保留全局、`GlobalAgentsFile` 路径）。
- 最高缝：`internal/services/chatservice_agents_guide_test.go` 七例——假模型服务器捕获请求：配置 Agent 双份注入且断言「角色提示词 → 全局 → 工作区」顺序、仅工作区、直连助手注入全局、180KB 文件内容逐字完整且带 176KB 警告与挤占提示、指南先于 `/skill` 正文、移除文件后请求不含任何指南内容而角色提示词仍在、远程 A2A 请求不含本地指南。
- 红→绿：解析器与端到端均先红后绿；`go test ./internal/... -count=1` 全绿（agentkit / config / mcpmgr / memory / services），`go vet ./internal/... .` 干净。

### 第二百二十一轮迭代：对话栏宽三档——按窗口比例自适应（已实施）

依据：用户反馈聊天输出列宽固定、宽屏留白过多，希望提供「窄 / 中 / 宽」三档，并明确要求三档随窗口自适应变化，而不是写死像素值。

**实现要点**

- **配置**：新增 `Settings.ChatWidth`（`narrow|medium|wide`，默认 `medium`），`NormalizeChatWidth` 将空值与未知值回退为中档；默认配置与旧配置归一化同步补齐。
- **自适应比例**：前端 `CHAT_WIDTHS` 将三档定义为聊天面板宽度的 `56% / 72% / 88%`，由 `applyTheme` 写入根级 CSS 变量 `--chat-col-width`（`:root` 默认 `72%`），窗口缩放时列宽自动跟随，不设像素上限。
- **统一列**：`ChatPage` 消息流、plan 条、排队提示、编辑横幅、附件条与 composer 全部改用 `max-w-[var(--chat-col-width)]`；composer 外层水平内边距移入各列容器，保证百分比基准与消息区一致、左右边缘对齐。
- **设置入口**：设置 → 通用 → 外观新增「对话栏宽」三按钮，沿用字号档位的交互；改动实时应用到根变量，保存后随 `ThemePrefs` 一并缓存，启动即生效。

**测试证据**

- Go 新增 `TestChatWidthNormalization`（三档、大小写/空白、空值与未知值回退、默认值）；`go test ./internal/config/...` 通过。
- 前端 `npx tsc --noEmit` 0 错、`vitest` 168/168 通过、`npm run build` 通过；产物 CSS 已确认生成 `max-width:var(--chat-col-width)` 与 `--chat-col-width:72%`。
- Wails bindings 已按新 `Settings` 字段重新生成。

### 第二百二十轮迭代：执行过程四档显示——调用/结果合并与「无」模式安全例外（已实施）

依据：用户对比 Codex 桌面端「推理摘要」四档菜单，反馈 AgentMate 执行过程运行时行数过多；进一步走读确认噪声来自两处：运行中自动展开全部步骤，以及同一工具的 `tool_call` / `tool_result` 各渲染一张卡。Codex 的四档是模型请求参数，AgentMate 本轮先落地同形态的纯展示四档，并保持事件完整持久化。

**实现要点**

- **四档显示策略**：新增 `Settings.ExecutionDisplayMode`（`auto|concise|detailed|none`，默认 `auto`）与 `ChatSession.ExecutionDisplayMode` 覆盖；`SetSessionExecutionDisplay` 走既有 `mutateSession`，空值恢复跟随全局，未知值拒绝。
- **语义**：自动=运行中展开、完成收起；简洁=只留摘要、运行中不展开、HITL 强制展开；详细=保持展开；无=隐藏执行过程块，但待确认、错误、`plan_todos`、最终回复不受影响。
- **入口**：设置 → 通用 → 外观增加全局选择；composer 左侧新增当前会话快捷切换，支持「跟随全局设置」。
- **去重**：新增 `buildToolTrace` 将同名工具的 call/result 按 LIFO 合并为一个逻辑步骤，执行块计数与渲染都按步骤而非事件；单工具轮次也进入执行块，避免一对裸卡。`ToolCallCard` 展开后分「输入 / 输出」两段并提供独立复制。

**测试证据**

- 前端新增 `executionDisplay.test.ts`（归一化、覆盖优先级、四档展开/隐藏、确认例外）与 `buildToolTrace` 配对/顺序/孤儿结果用例；单工具组断言同步更新。vitest 168/168 通过，`npx tsc --noEmit` 0 错，`npm run build` 通过。
- Go 新增配置归一化与会话覆盖测试；`go test ./internal/config ./internal/services -run "ExecutionDisplay|Normalize|SessionExecution"` 通过，`go test ./internal/...` 全绿；Wails bindings 已重新生成。
- 视觉回归桩补充多工具样例与会话级设置路由；当前桩的既有方法 ID 早于最新生成绑定，浏览器冒烟暂受该历史问题阻塞。

### 第二百一十九轮迭代：header 用量降噪——token 消耗移入 composer 悬停气泡（已实施）

依据：用户反馈（截图对比 Codex 桌面端）右上角常驻的「92,503 tokens」与「运行中」徽标属于冗余显示，token 消耗应像 Codex 一样悬停才显示。

**实现要点**

- **ChatPage header**：删除常驻 token/cost 文本与「运行中」徽标。运行状态仍由会话列表 spinner、composer 停止按钮与「回到底部」呼吸点表达；≥80% 的「上下文即将耗尽」告警徽标保留（告警态非常驻冗余）。
- **composer 悬停气泡**：ContextRing 的原生 `title` 换成项目 radix Tooltip（深色气泡，对齐图二样式）；新增 `contextTooltipLines` 纯函数统一文案——上下文窗口占用 %（含剩余）、已用/共 tokens、≈ 估算口径说明（延续 218 轮）、累计计费 tokens + ¥费用、输入/输出分项。
- **兜底**：模型未配置 contextWindow 时圆环不渲染，改用 Coins 图标承载同一气泡，保证累计用量仍可悬停查看；窄屏（`hidden sm:flex`）行为不变。MessageBubble 逐条用量与「会话统计」对话框不受影响。

**测试证据**

- 前端回归：contextUsage.test.ts 新增 contextTooltipLines 4 条用例（占用+累计+费用齐全、≈ 估算说明、窗口未知兜底、无数据/零用量/费用为空省略）；vitest 160/160 通过（新增 4 条全绿）。
- `npx tsc --noEmit` 0 错；`npm run build` 通过（5.4s）。Go 侧未改动。
- 待人工冒烟：跑一轮对话后右上角不再常驻 token 文本与「运行中」徽标；悬停 composer 右下角圆环出现深色多行气泡（百分比/剩余/已用与窗口/累计+费用）；无 contextWindow 的模型悬停 Coins 图标只显示累计用量。

### 第二百一十八轮迭代：思考强度标注移除与上下文占用圆环——未报用量模型的 ≈ 估算兜底（已实施）

依据：用户报告①用户消息上方的「思考强度：最高」标注没有必要显示；②上下文占用「又不显示了」，希望按 Codex 桌面端的圆圈样式实现。实证定位（本机 config + sessions.db + 对上游的最小 curl 实测）：占用条一直存在（composer 工具条），但天气会话 `sess_b020eba68fd2f49f`（catpaw/deepseek-v4.1-flash 路由）32 条事件 0 条 usage 元数据——该路由即使显式携带 `stream_options.include_usage` 也从不返回 usage 块（curl 实测 99 个 data 行无一含 usage），`resolveContextUsage` 收到空数据按「不猜测」原则返回 null，整个指示器随之消失；minimax 路由则正常返回 usage 块。

**实现要点**

- **MessageBubble**：删除用户气泡上方的「思考强度」标注（该轮 reasoning 仍随消息持久化，发送侧 composer 选择器与排队提示不变）。
- **composer 圆环**：新增 `ContextRing`（SVG 进度圆，成功/黄/红三档随占用变色，数字进 tooltip），替换原 16px 线性条 + 百分比文本；tooltip 区分真实值与估算值。
- **≈ 估算兜底**：`estimateContextTokens` 按会话内容估算下一轮 prompt 规模（CJK ≈0.7 token/字、其余 ≈0.25 token/字符、每条消息 +12 字符信封、工具载荷 96KB/条封顶），仅在模型未报 usage 时以「≈」标注展示；真实 usage 永远优先，「上下文即将耗尽」header 徽标与 80% 横幅仍只用真实值（延续 214 轮的反误报口径）。
- **llm.go 解析加固**：usage 原来只在 choices 为空的尾块被捕获，改为与 choices 无关一律捕获——覆盖「usage 附在最后一个内容块」的网关形态。

**测试证据**

- 回归闭环（Go）：新增 `TestOpenAICompatStreamUsageOnContentChunk`（usage 附在带 finish_reason 的内容块），先红（usage=nil）后绿；curl 实测证明 catpaw 路由属「完全不回」形态，由 ≈ 估算兜底覆盖。
- 回归闭环（前端）：contextUsage.test.ts 新增估算口径用例（中英分别计价、载荷 96KB 封顶、≈ 视图标记），9/9 通过；vitest 156/156；`npx tsc --noEmit` 0 错；`npm run build` 通过；`go test ./internal/...` 全绿；`go vet ./internal/... .` 通过。
- 待人工冒烟：打开天气会话应出现灰色 ≈ 圆环（tooltip 注明估算原因）；跑一轮 minimax 会话应出现真实值圆环；用户消息上方不再有思考强度标注。

### 第二百一十七轮迭代：会话历史重放 author 一致化——重启后执行过程不再拆碎（已实施）

依据：用户报告执行过程在首次会话显示正常，重启应用后同一会话被拆成多个「执行过程」块与成对裸工具卡。代码走读 + 失败测试实证：`renderEvent` 给 thinking/assistant 消息用 `displayAuthor`（如「写手」），但 tool_call/tool_result/confirm 四处回写原始作者（`agent_<hash>`）；前端 `groupTurn` 遇 author 变化即 flush，一轮在每个工具边界被撕开——thinking 段成无工具数的小组、单工具段成两张相邻裸卡，与截图逐一吻合。实时流（`chatservice_turn.go`）所有事件统一 `displayAuthor`，故首次会话正常。

**实现要点**

- **renderEvent**：删除 tool_call / tool_result / confirm_request / confirm_result 四处 `m.Author = ev.Author` 覆盖，统一沿用 base 的 `displayAuthor`。团队多代理场景不受影响：`displayAuthor` 仍按代理各自映射，前端拆组只要求同一代理的作者一致。
- 导出（Author 仅用于 assistant 消息）与统计（用原始 `ev.Author`）不受影响。

**测试证据**

- 回归闭环：新增 `TestRenderHistoryAuthorsConsistent`（假 SSE 模型：工具调用轮 + 文本回复轮，断言 tool_call/tool_result 与最终回复 author 相同且为显示名），先红（call/result=`agent_7995374d`、reply=`写手`）后绿。
- `go test ./internal/...` 全绿；`go vet ./internal/... .` 通过。
- 待人工冒烟：重启应用打开旧会话，含工具的轮次应整轮一个「执行过程」块，不再出现相邻裸工具卡。

### 第二百一十六轮迭代：执行过程空窗抖动修复与用户消息长 URL 溢出修复（已实施）

依据：用户报告①会话里粘贴的无空格长 URL（vless://…）把用户气泡横向撑出聊天栏被裁切；②执行过程在回合进行中反复"收缩→展开"，批次间隙徽章闪成绿色"完成"，多轮工具时持续抖动，明显未完成却显示完成。

**实现要点**

- **toolGroupStatus**：补回合级存活——live 且未答复（answered=false）时返回 running，即便当前没有任何 streaming 消息或运行中工具（两次工具批次之间的静默空窗）；errored 仍优先 failed，settled 且无答复维持 failed。修掉每个批次间隙 done↔running 翻转导致的折叠条收展抖动与徽章误报。
- **用户气泡**：`break-words`（overflow-wrap: break-word 不参与 min-content 计算）改为 `[overflow-wrap:anywhere]`——无空格长 token 的 min-content 收敛为约 1 字符，flex 链的 `max-w-[85%] / sm:max-w-[640px]` 上限重新生效，URL 在气泡内折行。
- ToolCallGroup 无需改动：autoExpanded 跟随 status，修复后空窗期保持展开直至回合 settled，与组件 doc 声明的"运行中展开、settled 收起"语义一致。

**测试证据**

- 回归闭环：chatUtils.test.ts 把"live 且未答复"的预期从 done 翻转为 running（先红后绿），并补 narration 流完后的静默空窗用例（同样先红后绿）；errored/settled 既有用例不变。
- 溢出差异回路：临时页链接构建产物 CSS，同屏渲染旧/新两类气泡 + 同一条长 URL，无头 Edge 截图——旧类气泡溢出 640px 上限被裁切（复现症状），新类完整收进气泡内折行（真实 Chromium 引擎验证；jsdom 无布局测不了宽度，仓库无 Playwright，故按诊断流程以截图差异回路代替）。
- `npx tsc --noEmit` 0 错；vitest 152/152；`npm run build` 通过。
- 待人工冒烟：真机重发含长 URL 的消息（应完全收进气泡内折行）；跑一轮多工具会话（执行过程应保持展开到回合结束，徽章不再中途闪"完成"）。

### 第二百一十五轮迭代：执行过程组拆组修复——plan_todos 更新不再打断整轮（已实施）

依据：用户报告一次"分析项目问题"的轮次把执行过程拆成 6 个"0 秒失败"卡片。sessions.db 实证：该轮仅 1 条用户消息、79 次工具调用、author 恒定；轮内 `plan_todos` 更新 3 次——`groupTurn` 对"非 process event"一律 `flush()`，每次 plan_todos 的 call/result（共 6 个事件）都把整轮撕开一段，拆点数与截图组数吻合。

**实现要点**

- **groupTurn**：plan_todos 单开分支——照旧作为独立卡片输出（置顶清单语义），但不再 `flush()` 当前的 run，组跨过它继续积累；error 仍打断 run。消息流里的清单卡随修复从"打断位置"移到执行组上方（与 composer 上方的 activePlan 置顶条同语义）。
- 注释同步：isProcessEvent / groupTurn doc 说明"standalone 但不拆组"。
- 无 Go 侧改动（事件流与置顶条均无涉）。

**测试证据**

- 回归闭环：新增"plan_todos 多次更新不拆组"用例（thinking + plan_todos×2 + 工具×6 + 回复，断言恰好 1 个 tool_group、4 张清单卡独立），先红（拆成 3 组）后绿；既有"narration/author 拆组"用例的顺序断言更新为新形态（清单卡浮到组上方）、查找断言改为按 id/author 定位。
- `npx tsc --noEmit` 0 错；vitest 152/152；`npm run build` 通过。
- 待人工冒烟：重跑一轮含任务计划的会话，执行过程应整轮一个组（失败时整组一个失败态），清单卡置顶显示。

### 第二百一十四轮迭代：上下文占用口径修正——「token 耗尽」误报治理（已实施）

依据：用户报告会话显示 6,435,098 tokens /「上下文即将耗尽 (100%)」且多轮失败。诊断（sessions.db 只读 SQL 回路）证实：该数字是 76 次模型调用的计费累计（每次 prompt 都重复携带完整历史前缀），而真实上下文峰值仅 166,145（1M 窗口的 16.6%）——口径错配（累计 ÷ 窗口）导致的误报；轮次失败与上下文无关（待错误文本裁决，疑为 provider 限流）。compaction 两项配置为 0（关闭）是放大器。

**实现要点**

- **UsageInfo.ContextTokens**：usageOf 提取时携带 PromptTokenCount，addUsage 累加时"最后一次覆盖"（永不累加）——done 事件的 SessionUsage 自动携带真实上下文占用；SessionStats.ContextTokens 从持久化 usage 事件按序取最后一条。
- **前端口径切换**：ChatPage contextUsage 分子从累计 totalTokens 改为 contextTokens，抽为 lib/contextUsage.ts 纯函数（无数据不渲染，不做累计口径 fallback）；tooltip 改用真实占用。头部「累计 tokens + 成本」保留计费口径——双口径各表其义。
- **失败落日志**：runTurn 错误路径补 log.Printf（此前失败只流式到 UI，事后不可诊断）。
- **控制字符过滤**：truncateRunes 剥离 C0 控制字符与 DEL（模型输出携带 U+0001 进入会话副标题显示为 ^A）。
- 压缩（compaction）默认值未改动：自动总结会产生额外模型调用成本，属行为变化，留待用户决策。

**测试证据**

- 回归闭环：round98 双调用 fixture（prompt 10→100）断言 done.SessionUsage.ContextTokens=100、SessionStats.ContextTokens=100 且 TotalTokens=154（累计语义不变）；控制字符 e2e 断言 preview 无 \x01（先红后绿）。
- `go vet ./internal/... .` 干净；`go test ./internal/...` 全绿；`npx tsc --noEmit` 0 错；vitest 151/151（新增 lib/contextUsage 5 用例）；`npm run build` 通过；绑定已重新生成（UsageInfo/SessionStats 新增 contextTokens）。
- 待人工冒烟：打开该长会话，上下文条应显示约 17% 而非 100%；头部累计 tokens 数字不变。

### 第二百一十三轮迭代：自绘顶栏窗口拖动修复——平台检测对齐 Wails runtime 真实契约（已实施）

依据：用户报告 Windows 无边框自绘顶栏（#75/#76/#77）不能拖动窗口，且窗口按钮工作正常。按诊断流程先建红色信号再修复。

**实现要点**

- **根因**：Wails v3 runtime 在页面脚本前同步注入 `window._wails.environment={"OS":…,"Arch":…,"Debug":false}`（大写字段，internal/runtime/runtime_prod.go）；TopBar 平台检测 `detectTopbarPlatform` 读小写 `env?.os`，恒为 undefined → 平台恒判 `browser` → `dragRegion: false` → 顶栏永不声明 `--wails-draggable: drag`，runtime 的拖动监听（@wailsio/runtime drag.js 的 mousedown/capture 检查 computed style）永远不触发 `wails:drag`。窗口按钮不受影响（browser 分支同样渲染按钮，操作走 runtime 内部的大写读取），与"只有拖动失效"症状吻合。
- **修复**：`detectTopbarPlatform` 改读 `env?.OS ?? env?.os`（大写真实契约优先，小写兜底），类型签名同步；TopBar 调用处类型同步；注释写明契约来源。Go 侧无涉。
- **测试契约更正**：topbarLayout.test.ts 原有用例用小写 `{os:"windows"}` 锁定了错误契约（所以此前全绿但真机失效）；改用真实注入契约 `{OS:…}`，并新增 TopBar 平台自动检测的端到端用例（stubGlobal window + 真实注入形态 → 断言拖拽区/no-drag 声明）。

**测试证据**

- 红色闭环：契约测试更正后 3 用例红（windows/darwin 平台检测 + Windows 自动检测端到端）；修复后 `vitest run` 13/13 绿。
- 全量：`npx tsc --noEmit` 0 错；`npm test` 146/146 通过；`npm run build` 通过。
- 待人工冒烟（用户执行）：真机拖动顶栏空白区移动窗口、双击顶栏最大化、按钮区不触发拖动。

依据：用户确认三项决策——GitHub 仓库走改名路线保留 issue/release 历史、应用数据目录全新开始（零迁移）、持久状态键直接改名；logo 采用已定稿的火花 A 设计（design/logo/）。执行范围仅本地修改，远端改名与推送由用户单独指令触发。

**实现要点**

- **代码身份**：Go module 改为 `agentmate`，71 个 Go/TS 文件的 import 与方法 ID 命名空间随动；绑定重新生成（`agentmate/` 目录就位，`github.com/` wails-typed-events 目录保留）；视觉桩方法 ID 与前端手写 import 同步。
- **数据身份（全新开始）**：应用数据目录改为 `agentmate`（旧目录永不读取、不自动删除）；默认工作区 `Documents\AgentMateWorkspace`；ADK 状态键 `agentmate.todos` / `agentmate.reasoning_level` / `agentmate.skill_context` 与本地存储风险确认键直接改名，零迁移代码。
- **桌面与打包身份**：`APP_NAME=agentmate`、productIdentifier `com.agentmate.app`；config.yml、darwin/iOS plist、windows info.json/manifest/MSIX/NSIS、linux desktop/nfpm 全部打包资产同步；发布产物更名 `AgentMate-windows-amd64.exe`（release 工作流硬编码名同步）。
- **图标接入**：火花 A 应用图标覆盖 `build/appicon.png`（托盘随内嵌联动）与前端 favicon；Windows exe 图标由构建从 appicon 自动生成的 icon.ico 承载，已目检确认。
- **品牌文案**：窗口标题、托盘 tooltip/菜单、OAuth 成功页、系统提示词、欢迎页、关于页、侧栏底部、占位符、前端包名全部换新；更新渠道常量与应用内反馈 URL 预置指向 `qq540491950/agentmate`（本地预置，远端改名前不 push）。
- **文档与流水线**：README/GLOSSARY/AGENTS/design-system 标题与正文、issue 模板示例、issue-fixer/pipeline 文档、bug-relay skill、脚本 REPO 常量、.gitignore 注释全部更新；全仓旧名检索零残留（白名单：历史迭代记录、issues 归档、本 spec 与 .scratch 工单）。

**测试证据**

- `go vet ./internal/... .` 干净；`go test ./internal/... .` 全绿（模块路径已是 `agentmate/internal/...`）。
- `npx tsc --noEmit`、vitest 131/131、`npm run build` 通过。
- `wails3 build`（含绑定 `-clean` 再生成）端到端成功，产出 `bin/agentmate.exe`（v0.5.28）；更新器/leftover 相关测试随品牌期望值更新后全绿。
- 全仓 `rg -in workbuddy --hidden` 仅命中白名单（历史迭代记录、issues-archive、agentmate-rename spec、.scratch 工单）。
- 待人工冒烟（用户执行）：托盘图标与提示、二次启动单实例转发、全新目录 onboarding、语义检索重建。

### 第二百一十一轮迭代：界面质感 P4——对比度实测、设计系统文档与截图基线（已实施）

依据：用户确认继续 [docs/ui-refresh-plan.md](../docs/ui-refresh-plan.md) 的 P4 收尾：把 P0–P3 的令牌与规则固化成文档，实测对比度并修复不达标项，留双主题截图基线。

**实现要点**

- **对比度实测**（浏览器内 OKLCH→sRGB 精确换算 + WCAG 公式，深浅两套 × 15 组关键配对）：深色全部 ≥ 6.56；浅色状态色在 10% 自色底上 4.19/4.31/3.82，不达 AA——将浅色 `--success/--warning/--destructive` 各加深一档（0.48/0.50/0.53），复测全部 ≥ 4.67。
- **设计系统速查**：新增 [docs/design-system.md](../docs/design-system.md)——四级表面、`--primary`/`--brand` 分离、语义色、字号阶梯（禁止 10/11px 中文）、圆角/阴影/动效令牌、组件模式（PageShell/PageHeader/Section/SettingRow、消息解剖、执行组、确认卡、代码块、空态）与对比度实测表。
- **截图基线**：新增 [docs/ui-baseline/](ui-baseline/README.md)——stub 1280×800 下对话页深/浅、设置通用、Agents 四张参考图 + 重新生成步骤 + 每轮 UI 走查清单。

**测试证据**

- 前端：`npx tsc --noEmit` 通过；`npm test` 131/131 通过；`npm run build` 通过。
- 对比度：深色 15 组配对全部 ≥ 6.56，浅色全部 ≥ 4.67（含状态文字 / 10% 自色底）。

### 第二百一十轮迭代：界面质感 P3——动效与精修（已实施）

依据：用户确认继续 [docs/ui-refresh-plan.md](../docs/ui-refresh-plan.md) 的 P3 阶段：此前界面只有 hover 变色，展开/收起是瞬间 mount/unmount，代码块无语法高亮，裸 `<button>` 缺可见焦点样式。

**实现要点**

- 消息进场：`wb-message-in`（opacity 0→1 + 4px 上移，180ms ease-standard）挂在消息与执行组根节点，流式新消息与「显示更早」补载时轻推入位；`prefers-reduced-motion` 下被 P0 的全局规则归零。
- 折叠展开：执行组与思考过程改用 grid-rows `0fr→1fr` 过渡（200ms），内容保持挂载、CSS 负责高度，流式追加不再触发重挂载；`aria-hidden` 跟随状态。
- 代码高亮：`MessageMarkdown` 内置零依赖分词器（注释/字符串/数字/关键字四类，组合正则 + 共享关键字表），仅对支持语言生效，分词失败时原样输出；四类语义色以 OKLCH 变量定义、深浅主题各自取值。
- 焦点环：`button:focus-visible` 元素级规则统一给裸按钮 2px 品牌色 outline；shadcn 组件在自身基类管理 outline，不受影响。
- 图标尺寸：会话行操作图标 3→3.5px，与消息区一致。

**测试证据**

- 前端：`npx tsc --noEmit` 通过；`npm test` 131/131 通过；`npm run build` 通过。
- stub 走查（深色）：ts 代码块关键词紫色/字符串绿色/数字蓝色/注释斜体灰，深浅两套色值随 `.dark` 切换；执行组收起后内容高度实测 0px、`transition: grid-template-rows 0.2s` 生效；消息根节点挂载 `wb-message-in` 动画；`button:focus-visible` 规则已注入样式表。

### 第二百零九轮迭代：界面质感 P2——管理页统一外壳（已实施）

依据：用户确认继续 [docs/ui-refresh-plan.md](../docs/ui-refresh-plan.md) 的 P2 阶段：管理页此前各自实现 18px 标题 + 自定宽度/内边距，设置页用 CSS `columns` 瀑布流导致卡片顺序不可预测，Agents 页远程区标题行 4 个直接子元素挤在 `justify-between` 里把按钮甩到页面中部。

**实现要点**

- 新增 `frontend/src/components/layout/PageShell.tsx`：`PageShell`（统一滚动容器 / max-w-5xl / px-8 py-7）、`PageHeader`（20px 标题 + 13px 描述 + 右对齐动作）、`Section`（16px 区段标题 + 动作）、`SettingRow`（左标签/说明、右控件的令牌化设置行）。
- Models / Skills / Teams / MCP / Agents 六个管理页全部换用 `PageShell + PageHeader`；标题 18→20px，动作区统一 32px 按钮高度。
- Models 与 Agents 列表从 2 列卡片改单列行式布局：品牌色圆角图标 + 名称/元信息 + 徽章 + 右侧动作（删除改 28px 图标并补 aria-label）。
- Agents 远程区改用 `Section`，导入/新增按钮归位到区段标题右侧，修复动作错位。
- Settings 通用/数据页弃用 `columns` 瀑布流，改 `grid items-start gap-4 lg:grid-cols-2`（窄窗口自动单列）；四处「边框行」统一替换为 `SettingRow`。

**测试证据**

- 前端：`npx tsc --noEmit` 通过；`npm test` 131/131 通过；`npm run build` 通过。
- stub 走查（1440 宽）：设置通用页双列网格顺序稳定、SettingRow 行样式一致；Agents/Models/Skills/MCP 页 20px 标题 + 右对齐动作，Agents 远程区按钮不再错位，行式列表带图标块与徽章。

### 第二百零八轮迭代：界面质感 P1——对话主界面重塑（已实施）

依据：用户确认继续 [docs/ui-refresh-plan.md](../docs/ui-refresh-plan.md) 的 P1 阶段：侧栏、顶栏、消息流、执行组、确认卡、composer 全面重塑，消除「框套框」并重建阅读层级。

**实现要点**

- 字号令牌补齐：`text-ui`（13px，控件/列表）与 `text-md`（15px/1.7，正文），对话正文从 14px 升到 15px，侧栏与控件统一 13px。
- 消息流（`MessageBubble` / `MessageMarkdown`）：助手消息去掉灰色气泡，改为全宽正文（作者行 12px 置顶，复制/重新生成/朗读收为 24px 图标按钮悬停显示）；用户消息改 `bg-brand/10` + 品牌描边、20px 圆角、最大宽 640px；思考过程由虚线盒改为左侧 2px 竖线；日期分隔去胶囊底色；代码块独立 `bg-muted/40` 底、复制按钮悬停显示；Markdown 标题/表格/引用按新令牌重排。
- 执行组（`ToolCallGroup` / `ToolCallCard`）：组容器统一 `rounded-xl` + 发丝线，展开后行间只留间距；新增 `flat` 形态让组内工具行不再各自描边，展开的参数/结果区改内衬底色；确认卡改左侧 2px 语义色条 + 10% 底色，参数区无边框。
- 侧栏：顶部新增品牌头（Sparkles 徽标 + WorkBuddy + 折叠按钮）；「新聊天」降为中性描边按钮；会话行 13px/更紧行高，当前会话加品牌底色与左侧 2px 色条，类型图标降噪为 muted；分组标题 11→12px。
- 顶栏：固定 48px，标题 14px 半粗，目标徽章改 `bg-elevated` 无边框，token 数改纯文本，运行中改品牌胶囊，图标按钮 28px。
- Composer：容器 `rounded-2xl bg-card shadow-card`，聚焦 `ring-4 ring-brand/10`；输入 15px；发送/停止 28px 圆形；@ 与 / 弹层换 `shadow-pop`；拖拽整块品牌色高亮。
- 空态：聊天空态改品牌圆形图标 + 15px 标题 + 13px 说明并垂直居中；Onboarding 去 emoji，改品牌徽标与步骤行。

**测试证据**

- 前端：`npx tsc --noEmit` 通过；`npm test` 131/131 通过；`npm run build` 通过。
- stub 走查（深/浅两套）：助手正文（标题/列表/行内代码/代码块/表格/引用）15px 阅读层级清晰、无气泡底色；用户消息品牌底色 + 右下角图标操作；执行组内工具行与确认卡无嵌套描边；侧栏品牌头/激活行色条/12px 分组标题；顶栏 48px 与遥测文本；composer 聚焦光环与 28px 控件；新聊天空态居中且无 emoji。

### 第二百零七轮迭代：界面质感 P0——设计令牌层与 composer 收敛（已实施）

依据：用户反馈整体界面「质感差」，要求给出方案并从 P0（设计令牌层）开始实施；同期提出四处交互收敛：用户消息操作改为区块右下角图标、删除 composer 下方工作区/快捷键整行、思考强度菜单去掉「默认」项直接选中生效档位、并在思考强度左侧显示上下文用量。完整方案见 `docs/ui-refresh-plan.md`。

**实现要点**

- `frontend/src/index.css`：建立四级表面（background / surface-1 / card / elevated）；拆分 `--brand`（强调/选中/焦点/运行态）与中性 `--primary`（填充按钮）；新增 `--success` / `--warning` 语义色；字体栈补中文回退（PingFang SC / {{SECRET_729ud52h}} UI / Noto Sans SC）；新增 `text-2xs` / `text-3xs` rem 字号令牌；统一圆角、阴影（浅色外投影、深色内高光）与过渡令牌；全局 `prefers-reduced-motion` 归零；`::selection` 使用品牌色。
- `frontend/src/lib/theme.ts`：强调色只写入 `--brand` / `--brand-foreground` / `--ring`，不再覆盖 `--primary` 与 `--sidebar-primary`。
- 全库清理：48 处 `amber/emerald/red` 硬编码改为 `warning/success/destructive`；63 处 `text-[10px]/[11px]` 改为 rem 令牌；组件内表示强调的 `text-primary` 等迁移为 `brand`；`Card` 增加 `shadow-card`，对话框/下拉菜单/Sheet 改用 `shadow-dialog` / `shadow-pop`；复选框与开关选中态改用品牌色。
- `ChatPage`：删除 composer 下方工作区路径与快捷键提示整行（含仅此处使用的 `changeSessionWorkspace`、`workspaceContext`、`workspaceLock`）；思考强度菜单去掉「跟随会话默认/模型默认」，菜单始终选中当前生效档位，触发文案直接显示档位名；上下文进度条与百分比移到思考强度左侧（`sm` 以下隐藏）。
- `MessageBubble`：用户消息的编辑/复制改为纯图标按钮，收进气泡区块右下角，悬停或键盘聚焦时显示；`CopyButton` 支持 `iconOnly`。
- `ChatPage` 上下文占比：打开既有会话时用 `SessionStats` 播种 `sessionUsage`（不必等下一轮结束），占比 <1% 显示 `<1%`、进度条最小 3px，切换会话时重置后重新拉取。
- 供应商编辑弹窗（`ModelsPage` / `ModelRowEditor`）：拆为「连接」「模型」两个分组；模型列表改为手风琴（摘要显示名称/ID/上下文/档位数，展开编辑）；字段标签统一 12px、输入 32px，图片输入与价格改为三列对齐；API Key 支持显示/隐藏；名称与 Base URL 内联校验、保存按钮带 loading；弹窗加宽至 3xl、遮罩加深、重开时滚动复位。
- 关于页：问题反馈从「左侧按钮 + 行内说明」改为分隔线上的设置行（左标题/说明、右按钮）。
- 侧栏页脚：由技术栈文案改为软件版本号（`WorkBuddy v<version>`），技术栈移入 title 提示。

**测试证据**

- 前端：`npx tsc --noEmit` 通过；`npm test` 131/131 通过；`npm run build` 通过。
- stub 走查（深/浅两套）：侧栏与画布分层、卡面深色内高光、当前会话行品牌底色、确认卡语义色、composer 无页脚行、上下文条按 `SessionStats` 显示 `<1%`（最小 3px 可见）、思考强度菜单无「默认」项且当前档位选中；供应商弹窗验证两组分区、模型手风琴、三列价格/图片输入对齐、正文滚动而页脚固定、重开滚动复位；字号设置 112.5% 下 `text-xs` 实测 12→13.5px，固定 px 字号不再脱钩。
- 已知未覆盖：真实后端的上下文用量只在前端 stream 事件中可见，stub 需注入 `sessionUsage` 才能显示进度条；浅色主题仅做了 stub 走查，未在 Windows 实机复核。

### 第二百零六轮迭代：新对话草稿态可选思考强度（已实施）

依据：用户反馈。在项目内新建对话与顶部「新聊天」出现的草稿输入框中，无法选择
模型的思考强度。根因：思考强度选择器的档位来源只解析**活动会话**的模型
（`target` memo 对草稿态恒返回 `model: null`），档位列表为空；渲染门控
`active && availableLevels.length > 0` 又把整个选择器隐藏。发送路径本身无问题
（`send()` 会把本轮档位传给新建会话的 `SendWithReasoning`，后端
`resolveTurnReasoning` 首轮即生效），纯前端显示层缺陷。

**实现要点**

- 提取 `composerReasoningModel`（`frontend/src/lib/modelLabel.ts`）：统一解析
  「思考档位来源模型」——已保存聊天读目标模型，草稿读 `draftModelFor` 的结果
  （与首条消息发送时后端解析同一模型）；保存过档位表的模型直接命中，旧模型
  （无表）沿用 `SuggestModels` 建议，且仅接受「为该模型 ID 发起的」建议。
- `ChatPage`：`SuggestModels` 兜底跟随草稿模型 ID；渲染门控改为仅
  `availableLevels.length > 0`；草稿态把「跟随会话默认」标签改为「跟随模型默认」
  并隐藏「设为会话默认」菜单项（尚无会话可设）；本轮选择的重置 effect 键改为
  `activeID + composerModelID`，草稿态切换模型同样重置。

**测试证据**

- 前端：`modelLabel.test.ts` 新增 `composerReasoningModel` 4 个用例；先以忠实
  移植旧行为的版本跑出草稿态红色（返回 `null`），修复后转绿——即本次缺陷的
  回归测试缝。`npx tsc --noEmit`、`npm test`（15 个文件 131 例）、
  `npm run build` 全绿。
- 后端未改动；`ChatPage` 组件级测试缝不存在（无 jsdom，package-lock 属共享
  文件不可动），接线部分由类型检查与生产构建覆盖，桩页面可手动走查草稿态。

### 第二百零五轮迭代：`@` 文件上下文与项目级 `/` 技能（已实施）

依据：用户反馈（对话页输入区）。输入框占位文案承诺「输入 @ 添加上下文」，但实际 `@`
只识别消息**开头**的助手/团队/远程 Agent，且不会引用任何文件；`/` 菜单只列出内置
命令与 config.json 里的技能，项目 `.agents/skills` 中的技能既不显示也无法调用。
期望：`@` 可在消息任意位置引用工作区文件作为当轮上下文；`/` 菜单按当前会话工作区
展示技能（含项目/用户 `.agents/skills`），并以「名称 + 描述 + 来源」的列表呈现。

**实现要点**

- **`@` 引用（前端）**：新增 `frontend/src/lib/mentions.ts`，`mentionAtCaret` 按光标
  解析 `@token`（支持消息中部、CJK 紧邻、`@"带 空格 的路径"`）；`buildMentionCandidates`
  统一排序助手（仅行首，后端只路由行首 @）与工作区文件/目录；`applyMentionToken`
  只替换当前 token，目录选择插入 `@dir/` 以便继续下钻。`ChatPage` 的 `@` 弹层改为
  文件/目录/助手统一列表，键盘 ↑↓ / Enter / Tab / Esc 与原有交互一致；占位文案改为
  「输入 @ 引用文件或指定助手，/ 调用技能与指令」。
- **`@` 引用（后端）**：新增 `internal/services/chat_context.go`：`fileMentionPaths`
  解析 `@path` / `@"quoted path"`（拒绝绝对路径、盘符、`..` 与邮箱形态），
  `buildFileMentionContext` 经 `agentkit.SafeWorkspacePath` 读取工作区内的文本文件，
  单文件 48KB / 单轮 96KB / 最多 8 个；二进制与超限文件以「未能载入」清单提示模型
  改用文件工具；内容作为本轮临时指令注入（与 `/skill` 相同的
  `SkillContextStateKey`），不写入历史、下一轮不继承。
- **工作区文件索引**：`ArtifactService.ListContextPaths(sessionID, projectID)` 按
  「会话 > 草稿项目 > 全局工作区」解析目录，复用任务快照的跳过规则（隐藏项、VCS、
  node_modules 等，5000 条上限），前端在切换会话/项目/工作区时刷新。
- **`/` 技能（后端）**：`ChatService.EffectiveSkills(sessionID, projectID)` 返回当前
  上下文的有效技能；`resolveSkillCommand` 改为在
  `config.EffectiveSkills(cfg, workspace, ~/.agents/skills)` 上匹配，因此项目
  `.agents/skills` 无需导入 config.json 即可被 `/name` 调用；HITL 恢复与 Regenerate
  走同一条解析路径。
- **`/` 菜单（前端）**：`slashCandidates` 为每个技能附带来源（项目 / 全局 / 应用内），
  弹层由横向 chips 改为纵向列表（命令名、描述、来源徽标），项目技能一眼可见。
- **桩数据**：`frontend/stub/main.tsx` 增加两个新 RPC 的 mock 与项目/应用技能样例，
  便于浏览器视觉走查。

**测试证据**

- Go：`chatservice_context_test.go` 覆盖「项目 `.agents/skills` 的 `/project-skill`
  注入 SKILL.md」「`@notes.md` / `@"my notes.txt"` 内容进入请求、目录外引用不读取、
  原始文本留在历史、下一轮不继承」；`chat_context_test.go` 覆盖路径解析（邮箱、CJK、
  盘符、`..`、引号）、二进制/缺失文件、工作区跳过规则与项目目录解析。全量
  `go vet ./internal/... .` + `go test ./internal/...` 通过。
- 前端：新增 `mentions.test.ts`（光标解析、引号插入、候选排序、目录下钻）与
  `slashSkills` 来源标签用例；`npx tsc --noEmit`、`npm test`（15 个文件 127 例）、
  `npm run build` 全绿；`wails3 generate bindings` 已同步新 RPC 与 `WorkspaceEntry`。
- 本机未安装 Playwright/Puppeteer，且当前安装未启用应用内浏览器预览，未做自动化 DOM
  走查；已通过纯函数单测 + 类型检查 + 生产构建验证，桩页面补了可手动走查的数据。

### 第二百零四轮迭代：「回到底部」悬浮按钮改为圆形状态指示（已实施）

依据：用户反馈。输入框上方的「回到底部」目前是带文字的胶囊按钮，视觉上偏重；
期望与参考产品一致：空闲时是圆形向下箭头按钮；轮次执行中改为三点呼吸动画；
鼠标移入时恢复为可点击的下箭头。

**实现要点**

- **形态**（`frontend/src/pages/ChatPage.tsx`）：胶囊改为 40×40 圆形按钮，
  水平居中悬浮在消息区底部、距消息视口底 16px；`title` / `aria-label` 仍为
  「回到底部」，运行中标题改为「正在生成，回到底部」。空闲态显示
  `ArrowDown` 图标。
- **定位**：消息区包一层 `relative flex min-h-0 flex-1` 容器，按钮锚定消息
  视口而不是整个对话面板——输入框随输入增高时视口收缩、按钮同步上移，不会
  压住输入区；计划条、排队消息等输入区上方内容同理。
- **运行态**：`running`（当前会话有在跑轮次）时在按钮内叠加三个琥珀色圆点，
  以 160ms / 320ms 错峰呼吸；悬停时圆点淡出、箭头淡入，全程由 CSS
  `group-hover` 完成，不额外维护悬停 React 状态。`prefers-reduced-motion`
  下圆点静止，仍可点击。
- **样式**（`frontend/src/index.css`）：新增 `wb-dot-pulse` 关键帧（缩放 +
  透明度）；运行态按钮边框带淡琥珀色，保留 `hover:bg-accent`、
  `hover:scale-105`、`active:scale-95` 的过渡反馈。

**测试证据**

- 门禁：`npx tsc --noEmit` + `npm run build` 通过；`npm test` 14 个文件 113 例
  全绿。
- 浏览器桩（`npm run stub`）实测：空闲态为 40×40 圆形按钮、`ArrowDown`
  opacity 1 且无圆点；临时令 `IsRunning` 返回 true 后，运行态渲染 3 个
  `.wb-dot-pulse` 圆点、箭头 opacity 0、标题为「正在生成，回到底部」，且样式表
  已生成 `group-hover:opacity-0/100` 规则（该临时改动已还原）。
- 定位实测（1280×800）：空输入框时按钮距输入区顶 16px、完全位于消息视口内；
  把输入框撑到 176px 高后，输入区顶从 639px 上移到 527px，按钮底同步从 623px
  上移到 511px，间距仍为 16px，不与输入框重叠。
- 该交互为纯视觉状态切换，现有 vitest 测试栈只覆盖纯函数、无 React 渲染接缝，
  未新增单测；验证环境为 Windows 11 浏览器桩页面。

### 第二百零三轮迭代：执行过程按整轮结局判定成败（已实施）

依据：用户反馈（Windows 桌面，直聊）。一轮里某个搜索步骤失败后模型换源重试，
并给出了完整的最终答复，但折叠的「执行过程」头部仍显示红色「失败」。期望：
头部徽标反映**这一轮**的结局——产出最终回复即算成功；只有整轮没有产出回复
（以 error 结束，或历史里没有最终回复）才显示失败。单个步骤的失败仍保留在
展开后的工具卡上（红色 X 与失败徽标）。

**实现要点**

- **职责调整**（`frontend/src/lib/chatUtils.ts`）：`groupTurn` 在分组时记录
  轮次级结局 `answered`（是否产出最终回复）与 `errored`（无回复且以 error
  结束；error 事件不落库，只存在于实时流）。`toolGroupStatus` 改为接收整个
  `ToolGroup` 与 `live`，不再因为任意一条 `tool_result` 带 `error` / `isError`
  就把整轮标成失败。
- **判定顺序**：待确认 > 运行中 > 失败 > 完成。失败条件 = `errored`，或已结束
  （`live=false`）却没有最终回复——历史不保存 error 事件，缺失回复是重载后仅存
  的失败信号。实时流中尚未写出回复不算失败，避免回答生成期闪红。
- **不会误伤**：回复之后才到达的告警（如会话 token 预算提示）以 error 事件
  呈现但已有回复，整轮仍为完成；用户消息之前的护栏告警归属于上一轮切片，
  同样不影响本轮判定。
- **组件接口**（`ToolCallGroup.tsx` / `ChatPage.tsx`）：`ToolCallGroup` 改为
  接收整个 `group`（消息、`endTs` 与结局字段），避免调用方漏传轮次结局。
  单卡 `ToolCallCard` 的失败样式不变。

**测试证据**

- `frontend/src/lib/chatUtils.test.ts`：新增「步骤失败但整轮已答复 → 完成」
  （先红后绿：修复前 `expected 'failed' to be 'done'`）、「整轮无回复且以
  error 结束 → 失败」「历史重载无回复 → 失败」「回复后的预算告警不判失败」
  「待确认优先于失败」等用例；原有运行中 / 流式思考 / 旁白用例按新签名迁移。
- 门禁：`npx tsc --noEmit`、`npm test`（14 个文件 113 例）、`npm run build`
  全绿。纯前端分组 / 状态判定改动，未改 Go 与绑定。

### 第二百零二轮迭代：思考过程并入选「执行过程」折叠块（已实施）

依据：issue #58（module:chat，bug）。一轮包含「思考 + 多次工具调用」时，思考
条目按分组规则被当作普通消息，工具事件因此被拆成多段纵向堆叠：展开的思考卡 →
工具卡 → 另一张思考卡，既没有统一的可收起容器，也没有汇总头，最终回复又单独
成块。期望：同一轮的思考条目与连续工具调用归属同一个可整体折叠的执行过程
区块；默认收起，头部给出工具数量、合计耗时与状态；点一次展开即可看每次调用的
参数与结果，最终回复正文仍是独立区块。

**实现要点**

- **分组范围**（`frontend/src/lib/chatUtils.ts`）：`isToolEvent` 改为
  `isProcessEvent`，把 `thinking` 与 `tool_call` / `tool_result` / HITL
  `confirm` 一起视为同一轮的执行过程事件；`plan_todos`、用户消息、助手文本、
  错误与作者切换仍会断开分组，最终回复不会被并进过程块。
- **成组条件**：仍保持「同一作者、连续事件」，至少两次工具调用成组不变；
  新增「思考条目 + 单次工具调用」也成组，否则思考会被留在组外、又退化成分散
  展示。纯思考轮（没有工具调用）不成组，保持原有独立思考卡片的折叠行为。
- **组内渲染**（`frontend/src/components/chat/ToolCallGroup.tsx` +
  `MessageBubble.tsx`）：`ThinkingBlock` 增加 `inline` 模式并导出，组内直接
  展示「思考过程」文本（流式时显示转圈），不再嵌套第二层折叠——外层组头就是
  唯一的展开 / 收起入口。工具卡与确认卡保持原有单卡详情、产物预览与 HITL 操作。
- **摘要语义**：`toolGroupInvocationCount` 与 `toolGroupDurationMs` 只统计
  工具事件，思考条目不计入「执行了 N 个工具」与耗时；`toolGroupStatus` 把仍在
  流式的思考条目视为「运行中」，让模型在工具批次之间继续思考时组保持展开，
  思考结束或轮次结束后回到完成状态。组 id 仍取该轮第一条事件的 id（现在可能
  是思考条目），流式追加不会重置用户的展开 / 收起选择。

**测试证据**

- `frontend/src/lib/chatUtils.test.ts` 新增 5 组用例：思考与多次工具调用合并
  为一个组且回复独立、思考 + 单次调用成组、纯思考轮不成组、作者切换断开思考
  分组、流式思考保持「运行中」而结束的思考不触发自动展开；既有单工具平级
  卡片、`plan_todos` / 文本断组、状态与耗时用例保持通过。
- 门禁：`npx tsc --noEmit` 通过；`npm test` 14 个测试文件、97 个用例全部通过；
  `npm run build` 成功；`./scripts/verify.sh` 全绿（vitest、`go vet`、
  `go test ./internal/... .`、`go test -race ./internal/... .`、`wails3 build`）。
- 验证环境 macOS arm64；纯前端渲染 / 分组改动，平台无关。未在 Windows 实机
  截图复核，折叠交互由纯函数用例、类型检查与构建门禁覆盖。

**已知边界**：助手在两次工具批次之间输出可见文本时仍会断组（与第 200 轮一致）；
思考条目只在所属过程块内展示，不再单独占据组外位置。

### 第二百零一轮迭代：HITL 确认卡「允许完全访问」会话级授权（已实施）

依据：issue #48（module:chat，enhancement）。对话里需要人工确认的工具调用（写文件、`run_command`）目前只能逐次点「允许 / 拒绝」，连续执行 git、构建、读文件这类可信任务时每一步都被打断。期望在确认卡上增加「允许完全访问」：一次点击同时批准当前挂起的调用、并把当前会话切到免确认模式，且与输入框开关共享同一会话级状态。

**实现要点**

- **会话字段**：`config.ChatSession` 新增 `allowAllTools`（持久化在 config.json，缺省即关闭）。新会话默认关闭；切换会话、重启应用后原会话保持授权，其他会话不受影响。
- **权限生效点**：`ChatService.runTurn` 是 Send / Regenerate / EditAndResend / ConfirmToolCall 的共享装配入口，按会话的 `AllowAllTools` 在构建前清空本地 Agent 的 `ConfirmTools`；团队目标经 `Kit.BuildTeamWithWorkspaceReasoningAndAllowAll` 对全部本地成员应用同一策略，`@` 路由到具名 Agent 的轮次随 `turnTargetFor` 复制会话字段而自动生效。远程 A2A 目标本来不在本地 HITL 链路。
- **单次拒绝仍有效**：ADK 的 `functiontool` 不论是否配置 `RequireConfirmation`，都会先检查 `ctx.ToolConfirmation()` 并对 `confirmed=false` 返回拒绝错误；因此授权开启前已挂起的确认仍可被明确拒绝，不会被静默执行。
- **RPC 与审计**：新增 `SetSessionAllowAllTools(sessionID, allowAll, source)` / `SessionAllowAllTools(sessionID)`；切换成功后写入 `session_allow_all_tools_set` 审计记录（`allowAll` + `source`，来源规范化为 `confirm_card` / `composer` / `unknown`），沿用现有 `ToolsAudit` 开关与 0600 JSONL 文件。
- **确认卡**：`ConfirmCard` 在「允许执行 / 拒绝」之外新增「允许完全访问」次级按钮（描边样式，次于主按钮）。点击顺序为「先持久化授权、再批准当前 call」，保证 ADK 恢复轮次构建时已不带确认；失败时回滚卡片到 pending 并尽量撤销授权。
- **输入框开关**：composer 工具栏新增会话级开关，已开启时以琥珀色显示「完全访问已开启」并带关闭图标，点击即恢复逐次确认；与确认卡共用同一 React 状态与后端字段，不出现两套真相。
- **一次性风险提示**：首次开启（无论入口）弹出 `ConfirmDialog`：「请注意数据安全，建议仅在可信的任务中开启」；确认后才生效，接受记录写入 localStorage，之后不再弹。存储不可用时按「未确认」处理，宁可再问一次。
- **绑定文件不改**：`frontend/bindings/**` 属共享文件；新增 RPC 通过 Wails 运行时 `Call.ByName("go-workbuddy/internal/services.ChatService.<Method>")` 调用，生成器写入绑定时可平滑替换为类型化调用。桩传输按 `methodName` 路由并实现两个 RPC，浏览器视觉回归可切换开关。

**测试证据**

- 新增 `internal/services/chatservice_allowall_test.go`（5 例）：开关持久化（重开 config 仍在）、默认关闭、不串会话、审计来源（三种来源）；授权后写文件不再产生 `confirm_request`；从确认卡开启时先授权再批准，后续轮次不再询问；`@` 路由轮次继承会话授权；团队成员写入同样免确认。
- 新增 `frontend/src/lib/allowAll.test.ts`（6 例）：风险确认键的严格匹配、存储异常时 fail-safe、确认写入失败不影响授权、两个入口共用文案与关闭提示。
- `go vet ./internal/...`、`go test -count=1 ./internal/...`、`go test -race -count=1 ./internal/...`；前端 `npx tsc --noEmit`、`npm test`（81 例）、`npm run build`。

### 第二百轮迭代：工具调用整体折叠为「执行过程」区块（已实施）

依据：issue #45（Windows 桌面，直聊 / Agent 多步工具轮）。助手一轮回复里连续执行
多个工具（如 6 次 list/read_workspace_file）时，每次调用各占一张卡片纵向堆叠，
阅读时看不出这段属于同一次思考与执行。期望把连续工具调用收进一个可整体折叠的
区块：默认收起，仅显示工具数量与总耗时，点开后再看每次调用的参数与结果。

实现要点：

- **分组纯函数**：`chatUtils.groupToolMessages` 把同一作者连续的
  `tool_call` / `tool_result` / HITL `confirm` 事件合并为一个
  `tool_group` 渲染项；用户消息、助手文本、错误、`plan_todos` 结果与作者切换
  都会断开分组，保证对话阅读顺序不变。只有一轮内至少两次工具调用才成组，
  单次调用的既有平级卡片保持不变。
- **稳定分组键**：分组 id 取该轮第一条事件的消息 id，流式追加事件不会改变
  React key，用户手动展开 / 收起的状态不会因新 token 重置。
- **摘要与状态**：`toolGroupStatus` 依次判定
  等待确认 > 运行中 > 失败 > 完成；失败沿用 `toolResultFailed`
  （`error` / `isError` / `is_error` 信封字段，抽为纯函数供单卡与分组共用）。
  头部显示折叠箭头、「执行了 N 个工具」、`toolGroupDurationMs` 汇总的已完成
  工具耗时与状态徽标。
- **展开策略**：运行中或存在待确认卡片时默认展开；轮次结束后若无用户操作
  回到默认收起；用户点击后以用户选择为准。展开内容仍是原来的
  `ToolCallCard` / `ConfirmCard`，单卡展开箭头、产物预览与 HITL 操作全部保留。
- **渲染窗口**：消息列表先按执行块分组再套用 200 条渲染上限，分组不会被截断
  窗口切开；省略提示按被隐藏的消息数计数，翻页按钮按条目计数。
- **历史一致**：分组只依赖消息的 kind/author/toolName，历史重载与实时流式走
  同一套函数，渲染结果一致。

测试证据：

- `chatUtils.test.ts` 新增 `groupToolMessages`（合并、运行中并行轮、单次调用
  不成组、文本 / plan_todos / 作者切换断组）、`toolGroupStatus`
  （待确认优先、已回答确认、运行中、三种失败信封、完成）、
  `toolResultFailed`、`toolGroupDurationMs`、`chatItemStartTs/EndTs` 用例。
- `npx tsc --noEmit` 通过；`npm test` 11 个测试文件、75 个用例全部通过；
  `npm run build` 成功。
- `./scripts/verify.sh` 全绿（前端构建与 vitest、`go vet`、`go test ./internal/... .`、
  `go test -race ./internal/... .`、`wails3 build`）。
- 本机未做浏览器桩实机点击验证（定时任务环境无法启动预览服务）；折叠 / 展开
  交互由纯函数用例、TypeScript 类型检查与构建门禁覆盖。

已知边界：首版只合并连续的工具事件，「思考过程」条目待后续思考过程票接入；
助手在两次工具批次之间输出文本时，两段各自成组，不跨文本合并。

### 第一百九十九轮迭代：直聊会话头部隐藏目标徽标（已实施）

依据：issue #44（Windows 桌面，直聊会话）。会话头部固定渲染「直聊」徽标，与
Agent / 团队 / 远程徽标并排出现，用户误以为「直聊」是一种需要选择的模式或实体。
期望直聊会话不再显示徽标，绑定 Agent / 团队 / 远程时仍显示对应实体徽标。

实现要点：

- **文案规则抽为纯函数**：`chatUtils.sessionTargetBadgeLabel(targetType)` 返回
  `Agent` / `团队` / `远程`，直聊（空目标）与非预期值返回 `null`。直聊不再依赖
  三元表达式里的「直聊」兜底文案。
- **头部渲染**：`ChatPage` 头部徽标改为先取标签，`null` 时不渲染；有目标时仍显示
  `标签: 目标名`，Agent / 团队徽标保留点击跳转设置页的行为，远程徽标保持纯展示。
- **测试**：`chatUtils.test.ts` 新增 `sessionTargetBadgeLabel` 用例，覆盖直聊
  （`""`、`undefined`）返回 `null` 与三种绑定目标的标签。

测试证据：

- `npx tsc --noEmit` 通过；
- `npm test`：11 个测试文件、65 个用例全部通过；
- `npm run build` 成功；
- `go vet ./internal/... .`、`go test -count=1 ./internal/...` 全部通过（后端未改）。

### 第一百九十八轮迭代：按轮思考强度与推理模型入口兜底（已实施）

依据：issue #42（Windows x64 桌面，v0.5.21，自定义 / OpenAI 兼容供应商，模型
`codearts/glm-5.3-flash`）。#34 只提供了会话级思考强度 chip，同一会话内无法逐条消息
切换；且入口只在模型显式配置了推理档位表时出现，用户当前模型的档位表为空，输入区
完全没有思考强度入口，被误判为「系统没有这个能力」。

实现要点：

- **按轮覆盖**：新增 `ChatService.SendWithReasoning(sessionID, text, attachments,
  reasoningLevel)`；`Send` 保持原签名并等价于空档位（空档位 = 跟随会话默认，再落到
  模型默认档）。前端输入区 chip 改为「本条消息思考强度」：单选档位只作用于下一条
  消息，发送后自动归零；正在运行的轮次不受影响（档位在发送时解析并构建模型）。
- **优先级**：`runTurn` 统一解析 `单轮显式选择 > 会话默认 > 模型配置默认档`，未知/
  过期档位（切模型后残留）自动落回下一级，不会发送任意字段。会话默认仍由
  `SetSessionReasoningLevel` 持久化，chip 菜单新增「设为会话默认 / 清除会话默认」，
  #34 的能力保留。
- **推理家族兜底**：`config.ModelInfo.ReasoningTable()` 在模型未保存档位表时返回内置
  家族映射（deepseek / kimi / glm / claude / gpt / qwen 等），入口不再隐藏；显式
  `enabled=false` 仍然视为关闭。兜底表只服务于 UI 与显式选择：没有用户选择时不注入
  任何请求字段（`ReasoningConfigured()` 区分「已保存」与「内置兜底」），升级不会悄悄
  改写老模型的请求体。
- **真实下发**：档位仍走 `reasoningExtraBody` 既有链路（OpenAI 兼容
  `reasoning_effort` / `thinking`，Anthropic 自适应 `thinking` + `output_config`，
  Qwen 多字段），团队根节点、直聊、Agent 三条构建路径共用解析结果。
- **历史可还原**：实际生效档位写入用户消息事件的 `Actions.StateDelta`
  （`workbuddy.reasoning_level`，非 `temp:` 前缀所以会入库），`renderEvent` 回填
  `ChatMessage.reasoning`；用户气泡下方显示「思考强度：高」，Markdown / HTML 导出在
  用户标题后注明档位。`Regenerate` / `EditAndResend` / HITL 确认续跑都从事件里读回
  原档位，保持一致。
- **排队消息**：排队项改为 `{text, reasoning}`，排队的下一条消息使用入队时选定的档位，
  不会在等待期间被后续切换污染。
- **绑定与桩**：重新生成 Wails 绑定（新增 `SendWithReasoning`、`ChatMessage.reasoning`、
  `ChatStreamEvent.reasoning`）；浏览器桩补齐 `SendWithReasoning` 方法、流事件回显档位，
  并让 `SuggestModels` 按家族返回兜底档位表。

测试证据：

- `internal/config`：新增兜底表 / 显式关闭 / `MatchLevel` / `DefaultLevelName` 用例。
- `internal/agentkit`：新增「兜底表未选档位不注入、显式选档位才注入」用例。
- `internal/services`：新增按轮档位端到端用例（请求体切换、会话默认不被写回、流事件与
  历史消息带档位、未知档回退、Regenerate 延续档位）与解析优先级单测。
- 质量门禁：`./scripts/verify.sh` 全绿（前端构建、vitest 63 项、`go vet`、
  `go test ./internal/... .`、`go test -race ./internal/... .`、`wails3 build`）。
- 本机未做浏览器桩实机点击验证（定时任务环境无法启动预览服务）；前端逻辑由
  `tsc --noEmit`、vitest 纯函数用例与构建门禁覆盖。

已知边界：远程 A2A 会话没有本地模型，不显示该控件；Gemini 协议仍不合并任意请求字段
（与 #34 一致）；历史消息的档位徽标只对本次改动之后记录的轮次出现。

### 第一百九十七轮迭代：Agent 定义精简为语义化三项 + 高级设置（已实施）

依据：issue #39（Agents 页，enhancement）。新建 Agent 的表单字段多且语义不清，「可用工具」看起来像必须先收敛范围才能用工具；期望「少量必要项 + 自然语言描述」即可定义 Agent，工具默认全量、模型继承全局默认，限制范围是显式动作。与 #38（自建 Agent 默认装配全部内置工具）同源但独立：本票只覆盖 Agents 页定义表单与模型默认继承。

**实现要点**

- **表单分层**：新增/编辑对话框首屏只保留 3 个必填项（名称、描述、系统提示词），每项带一句用途说明与必填标识；模型、内置工具、Temperature/MaxTokens、停止序列、Skills / MCP、安全护栏、Token 预算、超时与工具调用上限、HITL 确认统一收进「高级设置」，默认折叠、可展开，不展开也能直接保存。
- **必填校验**：保存只校验名称/描述/系统提示词非空，不再要求先选供应商与模型；模型留空即继承全局默认，工具留空即全量。
- **默认模型继承**：`config.ResolveAgentModel` 统一解析——空供应商回落「模型配置」中标记默认（否则第一个）的供应商，空模型回落该供应商第一个模型；`kit.buildLLMAgentWithSubAgents` 在装配前解析，`BuildAgentWithModelWorkspaceAndReasoning` 先解析再做会话级覆盖钳制，autoTitle 与统计计费共用同一解析。历史空值配置按继承解释，不静默改写；供应商 ID 显式但已失效时仍报错提示重选。
- **前端一致性**：新增 `lib/agentModel.ts` 的 `resolveAgentModel`，Agents 卡片显示「默认 · 模型名」、ChatPage 的目标模型与模型选择器、图片输入能力判断都按同一规则解析默认供应商/模型。
- **交互文案**：Agent 卡片对空 `builtinTools` 继续显示「内置:全量」；高级区工具说明写明「默认启用全部内置工具；取消勾选才会限制范围」。

**测试证据**

- 新增 `internal/config` 的 `TestResolveAgentModelInheritsDefault`：默认供应商、无默认时取第一个、显式值不改写、显式供应商空模型、失效供应商保持原值、无供应商保持空。
- 新增 `internal/agentkit` 的 `TestBuildAgentInheritsDefaultModel`：仅名称/描述/提示词的 Agent 可构建，会话级模型覆盖按默认供应商钳制，无供应商时给出可操作错误。
- 新增 `internal/services` 的 `TestAgentWithoutExplicitModelRunsOnDefault`：空 provider/model 的 Agent 发送消息使用默认供应商第一个模型。
- 新增 `frontend/src/lib/agentModel.test.ts`（6 例）覆盖默认继承与失效供应商；`npm test` 61 例通过。
- `./scripts/verify.sh` 全绿（前端构建、vitest、go vet、全量测试、race、`wails3 build`）。

### 第一百九十六轮迭代：本地 Agent 终端命令工具 run_command（已实施）

依据：issue #36（Windows x64，v0.5.17，本地自建 Agent + glm-5.3-flash）。用户让 Agent「拉取最新代码」，模型明确回答没有终端 / Shell 执行工具，无法运行 `git pull`、`go build`、`npm install` 等命令；期望本地 Agent 至少能完成拉取代码这类操作。核查确认这不是模型误判：内置工具装配只有时间、工作区文件读写、知识库、长期记忆与任务计划，整个仓库不存在命令执行工具，界面上也没有可开启的入口。

**实现要点**

- `internal/agentkit/shell.go`：新增内置工具 `run_command`，由 Agent 的 `builtinTools` 新增类别 `shell` 挂载（默认不开启，避免升级后静默获得命令执行能力）。命令以任务工作区为 cwd，Windows 走 `cmd.exe /d /c`（先 `chcp 65001` 统一 UTF-8 输出），其他平台走 `/bin/sh -c`；继承用户环境与系统权限。
- 结果契约：返回 `{exitCode, output, truncated, timedOut}`；非零退出码是正常工具结果而不是工具错误，模型可据此调整命令。stdout/stderr 合并，最多回传 64KB，超出部分丢弃但继续排空管道避免子进程阻塞；输出含 64KB 截断标记。
- 超时与取消：默认单条命令 300 秒，模型可传 `timeoutSeconds`（上限 600 秒，低于单轮 600 秒兜底，保证模型能拿到超时结果而不是整轮丢失）。Unix 用独立进程组 + 组级 SIGKILL 清理 `sh` 派生的构建子进程；Windows 用 `taskkill /T /F` 终止整棵进程树，失败时回落到 `Process.Kill`。用户在对话中停止时命令随运行上下文一起取消。
- HITL：`ConfirmTools` 新增 `shell` 类别，开启后每次 `run_command` 都走既有 `adk_request_confirmation` 暂停 / 批准协议；Agents 页勾选「终端命令（run_command）」时默认同步勾上「执行终端命令需人工确认」，用户可显式关闭。
- 前端：Agents 页内置工具行新增终端开关（容器改为 `flex-wrap` 适配窄窗口），下方复用与「写入文件需人工确认」一致的确认开关样式。

**测试证据**

- 新增 `internal/agentkit/shell_test.go`：空工作区/空命令拒绝、工具名契约、命令确实在工作区内执行（`pwd` / `cd`）、非零退出码透传、超 64KB 输出截断且保留前 64KB、300ms 超时被及时终止并标记 `timedOut`。
- `TestBuildAgentWithBuiltinTools` 的内置工具列表加入 `shell`，锁定 Agent 构建路径能装配新工具。
- `go vet ./internal/agentkit/... ./internal/config/...`、`go test -count=1 ./internal/agentkit/... ./internal/config/...` 通过。

### 第一百九十五轮迭代：模型显示名、思考强度配置与会话级档位（已实施）

依据：issue #33 / #34 / #35（Windows x64 桌面，v0.5.12，同批模型设置改造）。模型列表与对话模型选择器只能显示长模型 ID，无法为模型起可读名称；模型级设置缺少「推理档位 → API 参数值」映射，用户无法表达同一供应商下不同推理模型各自的思考档位；对话输入框也没有会话级思考强度入口，只能改全局配置。三张票共用同一套配置与表单，按批次一次实现并在同一版本发布。

**实现要点**

- **配置模型**：`ModelInfo` 新增 `DisplayName`（留空回落 ID，见 `Label()`）、`MaxOutputTokens`、`Reasoning *ReasoningConfig`；`ReasoningConfig` 保存开关、默认档与档位表，`Resolve(level)` 按档解析参数名/参数值，未知档返回 false（运行期回落模型默认，不发送过期覆盖）。`Clone()` 深拷贝档位表，修复模型切片拷贝时 range 到新空切片导致模型丢失的问题。
- **内置预设**：`internal/config/modelpresets.go` 按模型家族（deepseek / kimi / glm / claude / gpt / qwen）给出档位表与默认值，`SuggestModelInfo` 同时把显示名初始化为 ID；预设只作为编辑器默认值，用户可改档位名、参数名、参数值，增删档位。Gemini 协议由 genai SDK 接管请求体，档位配置会保存但运行期不注入（界面已注明适用 OpenAI 兼容 / Anthropic）。
- **请求注入**：`agentkit.reasoningExtraBody` 把解析结果转成请求字段——`Param` 为空时按 JSON 对象合并到顶层（支持 Claude 自适应思考 + `output_config.effort` 多字段、Qwen 多字段），非空时按字段名写入并支持点号路径（如 `thinking.budget_tokens`）。OpenAI 兼容与 Anthropic 请求结构各加 `ExtraBody`，`MarshalJSON` 在类型字段之后合并；`NewModelFromProviderWithReasoning` 在构建 LLM 时解析档位。模型级 `MaxOutputTokens` 在 Agent 未显式设置 `MaxTokens` 时作为兜底（含团队自动协调员）。
- **会话级档位**：`ChatSession` 新增 `ReasoningLevel`；`ChatService.SetSessionReasoningLevel` 走 `mutateSession` 持久化并写 `session_reasoning_set` 审计，不触碰供应商配置；`SetSessionModel` 在切换模型时清空旧档位。`runTurn` 把会话档位传给 Agent / 团队根节点的构建，直聊、Agent、团队三条路径共用同一解析。
- **前端**：新增 `ModelRowEditor`，每行按「模型 ID + 显示名称 → 上下文 + 最大输出 → 图片输入 + 价格 → 推理等级开关 + 档位表」布局；档位行支持档位名（内置常见档位 datalist，可自定义）、参数名、参数值、删除与新增，默认档下拉；「自动补全」按模型家族重新带出预设。`ConfigService.SuggestModels` 暴露内置预设（离线），拉取模型时批量自动带出，手动输入在失焦时补齐；保存前用 `validateModelReasoning` 校验空档位、重复档位、JSON 对象格式与悬空默认档。
- **显示名贯通**：`modelLabel(m) = displayName || id` 用于模型列表徽章、对话模型选择器、输入框模型 chip、Agents / Teams 的模型下拉与卡片摘要；请求仍使用模型 ID，会话绑定与计费归属不变。对话输入区在有效模型具备启用档位表时显示「思考强度」chip，选项为「模型默认（档位）」+ 档位表，按会话记忆；没有档位表的模型不显示入口。
- **绑定与桩**：重新生成 Wails 绑定（`ReasoningConfig` / `ReasoningLevel` / `ChatSession.reasoningLevel` / `ConfigService.SuggestModels` / `ChatService.SetSessionReasoningLevel`）；`frontend/stub` 补齐新方法 ID、种子模型的显示名与档位表，供浏览器视觉回归。

**测试证据**

- 新增 `internal/config/modelpresets_test.go`：家族识别、请求形态、`ActiveReasoning` / `Resolve` 回落、`Clone` 深拷贝、`ModelInfo` JSON 往返。
- 新增 `internal/agentkit/reasoning_test.go`：OpenAI `reasoning_effort` / `thinking` 注入与未知档不注入、Anthropic 自适应 JSON 合并、空 `Param` 非对象报错、点号路径、`MaxOutputTokens` 兜底。
- 新增 `internal/services/chatservice_reasoning_test.go`：会话档位持久化并改变请求体、切换模型清空旧档位、未知会话报错。
- 新增 `frontend/src/lib/modelLabel.test.ts`：显示名回落、档位表启用判定、档位中文标签、档位表保存校验。
- `go vet ./internal/... .`、`go test -count=1 ./internal/... .`、`go test -race -count=1 ./internal/... .` 全绿；`cd frontend && npx tsc --noEmit && npm test && npm run build` 全绿（10 个测试文件、55 个用例）。
- 浏览器桩实机验证：供应商编辑面板展示模型 ID / 显示名 / 上下文 / 最大输出 / 图片输入 / 价格 / 推理档位表；对话输入区出现「思考强度」chip，选择「高」后桩会话写入 `reasoningLevel: "high"`；模型选择器显示「GPT-5 Mini / GPT-5 Max」而非长 ID。

### 第一百九十四轮迭代：供应商编辑面板可滚动、底部操作栏常驻（已实施）

依据：issue #32（Windows x64 桌面，v0.5.12）。供应商编辑面板在模型列表达到 8～9 条后一路撑长，`DialogContent` 没有高度上限也没有滚动容器，底部「保存」「取消」被推到窗口外，既点不到也滚不到，只能 Esc 关闭并丢弃已填内容。期望内容区可纵向滚动、操作栏固定可见、最后一条模型与操作栏不重叠，且窄窗口与键盘 Tab 同样可用。

**实现要点**

- **根因**：`ModelsPage` 的供应商对话框直接使用 shadcn `DialogContent` 默认布局——`grid` + 无 `max-height` 的 `fixed` 居中弹层。表单是普通流式内容，高度只由内容决定，模型行越多弹层越高；超出视口的部分（含 `DialogFooter`）没有任何滚动入口。窗口高度只是决定何时「中招」的阈值，不是根因。
- `frontend/src/lib/providerEditorLayout.ts`（含测试）：新增两个布局 class 常量，锁定「对话框限高 + 只有表单主体滚动」的契约——`PROVIDER_EDITOR_DIALOG_CLASS` 给 `DialogContent` 加 `flex max-h-[85vh] flex-col`（`flex` 经 `cn` 的 tailwind-merge 覆盖默认 `grid`），`PROVIDER_EDITOR_BODY_CLASS` 给表单主体加 `min-h-0 flex-1 overflow-y-auto`。`min-h-0` 是 flex 收缩的关键：缺省 `min-height:auto` 会让主体拒绝缩到内容高度以下，限高后只会裁切而不是滚动。`overflow-y-auto` 刻意放在主体而非对话框上，保证「取消」「保存」始终留在滚动区之外。
- `frontend/src/pages/ModelsPage.tsx`：`DialogContent` 改用对话框常量；名称/协议/Base URL/API Key/模型列表/默认供应商开关所在的表单容器改用主体常量（保留 `grid gap-4 py-2`）；`DialogFooter` 仍是 `DialogContent` 的直接子节点，维持在内容区下方常驻。滚动条只在内容溢出时出现，`py-2` 在最后一条模型与操作栏之间留出间距；Tab 聚焦模型行时浏览器自动把输入滚入可视区，底部按钮不受裁切。
- 该布局与仓库既有可滚动脉冲一致（`ToolCallCard` 的 `flex max-h-[85vh] flex-col` + `min-h-0 flex-1 overflow-auto`），未引入新的 WebView 版本要求。

**测试证据**

- 新增 `frontend/src/lib/providerEditorLayout.test.ts`：#32 回归守卫，断言对话框限高且为纵向 flex、主体独占滚动（对话框本身不得出现 `overflow-y-auto`）、主体具备 `min-h-0 flex-1` 以便在 flex 列内收缩。
- `cd frontend && npx tsc --noEmit && npm test && npm run build`：9 个测试文件、47 个用例全绿，生产构建通过；构建产物 `dist/assets/index-*.css` 确认生成 `max-h-[85vh]`、`min-h-0`、`flex-1`、`overflow-y-auto`、`flex-col` 工具类。
- `go vet ./internal/... .`、`go test -count=1 ./internal/... .` 全绿（后端未改动）。
- 本机预览服务未获授权启动，未做浏览器实机点击验证；布局依据为 flex 收缩语义与构建产物，待测试机安装新版本后按 issue 的期望结果逐条确认（9 条模型滚到底、操作栏常驻、末行不重叠、Tab 可达保存按钮）。

### 第一百九十三轮迭代：新聊天仅在首条消息发送后才创建会话（已实施）

依据：issue #30（Windows x64 桌面，v0.5.12）。点击「新聊天」会立刻调用 `NewSessionWithWorkspace`，左侧会话列表立即多出一条「新对话」，连点几次就堆满空会话且标题被挤成省略号；期望空白对话只存在于主区域，发出首条消息后才落库并出现在列表。

**实现要点**

- **根因**：`ChatPage.createDirectSession` 把「打开一个空白对话」实现成了「立即创建后端会话」——按钮、⌘N 与 `/new` 共用该函数，每次点击都写入一条标题为「新对话」的会话记录并刷新列表。前端其实已具备 composer-first 的延迟创建路径（`send()` 在无 `activeID` 时按首条消息创建会话），但「新聊天」绕过了它。
- `frontend/src/pages/ChatPage.tsx`：`createDirectSession` 改为只进入本地草稿态——清空 `activeID`、消息、草稿输入与编辑/用量状态，不再调用后端；新增 `newChatDraft` 标志，阻止「自动选中最新会话」的副作用把空白草稿顶掉。任一会话被激活（侧栏点击、⌘⇧[/]、首条消息创建、分叉、后台 toast「查看」等）即自动退出草稿态。首条消息仍由既有 `send()` 创建会话，因此「创建会话失败」的提示时机也从点击时顺延到发送时，失败时输入内容保留。
- `frontend/src/lib/sessionDraft.ts`（含测试）：抽出 `shouldOpenLatestSession({ activeID, draft, sessionCount })`，把「首次加载自动打开最新会话」与「用户主动新建草稿」的判定从组件中分离，便于回归测试。
- 项目行「在此项目中新建对话」保持原有的即时创建语义：该入口明确指定了项目归属，不属于本次「新聊天」范围。

**测试证据**

- 新增 `frontend/src/lib/sessionDraft.test.ts`：覆盖首次加载自动打开、草稿态不被顶掉（#30 回归守卫）、已打开会话不被替换、空列表不动作。
- `cd frontend && npx tsc --noEmit && npm test && npm run build`：7 个测试文件、34 个用例全绿，生产构建通过。
- `go vet ./internal/... .`、`go test -count=1 ./internal/...` 全绿（后端未改动）。
- 本机预览服务未获授权启动，未做浏览器实机点击验证；行为由组件状态流转、单测与构建覆盖，待测试机安装新版本后确认点击「新聊天」不再新增列表条目。

### 第一百九十二轮迭代：下线会话迁居（「移动到项目」）功能（已实施）

依据：issue #29（Windows x64 桌面，v0.5.12）。会话行「…」菜单第一项为「移动到项目（首轮前生效）」，悬停展开默认项目与全部注册项目；用户要求连同子菜单、后端迁居 RPC 一并移除，不留残项。会话在创建时已归属项目（项目内新建走 `NewSessionWithProject`），迁居入口只增加噪音与误操作。

**实现要点**

- **根因**：会话的项目归属在创建时确定，但 `ChatService.SetSessionProject` 允许首轮前把既有会话改绑到任意项目（清空任务目录、重置基线并写审计）；前端两个入口（会话行菜单、输入区「任务目录」下拉）都调用它，形成一套与创建路径重复的状态修改面。
- `internal/services/chatservice_sessions.go`：删除 `SetSessionProject` 整个方法（含工作区锁、首轮校验、项目存在性校验、基线重建与 `session_project_set` 审计）。创建路径 `NewSessionWithProject`、项目删除回落、`ForkSession` 保留项目的既有行为不变。
- `internal/services/chatservice_project_test.go`：原首轮锁用例随方法删除；新增 `TestChatServiceHasNoRebindProjectRPC` 以反射守卫断言服务面不再暴露 `SetSessionProject`，防止重新生成绑定后入口复活。
- `frontend/src/pages/ChatPage.tsx`：删除会话行三点菜单的「移动到项目」子菜单及 `switchSessionProject` 调用；输入区「任务目录」下拉移除项目行，只保留「选择其他目录…」与「添加新项目…」（目录绑定与项目创建是独立能力）。移除随之无用的 `DropdownMenuSub*`、`Check` 导入。
- `frontend/src/lib/workspace.ts`（含测试）：锁定文案由「请先移出项目再单独指定工作区」改为「不能单独指定工作区」——迁居下线后「移出项目」已无入口，不再给出死路提示。
- `frontend/stub/main.tsx`：删除 `SetSessionProject` 方法 ID `3134520676` 的桩。`frontend/bindings` 用与 CI 相同的 `wails3 v3.0.0` 重新生成（`SetSessionProject` 绑定消失；`AppInfo` / `ApplyUpdate` 两处注释随源码同步）。

**测试证据**

- `go vet ./internal/... .`、`go test -count=1 ./internal/... .`、`go test -race -count=1 ./internal/... .` 全绿（含新反射守卫用例）。
- `cd frontend && npx tsc --noEmit && npm test && npm run build`：6 个测试文件、30 个用例全绿，生产构建通过。
- 本机预览服务未获授权启动，未做浏览器实机点击验证；菜单结构由源码删除与 `tsc`/构建产物覆盖，待测试机安装新版本后确认三点菜单不再出现「移动到项目」。

### 第一百九十一轮迭代：修复会话行三点菜单弹到窗口左上角（已实施）

依据：issue #28（Windows x64 桌面，v0.5.11）。展开项目分组后点击会话行右侧「…」，菜单固定出现在整个窗口左上角，压住标题栏与「搜索对话」，与按钮完全脱节；任何会话、项目、窗口尺寸均必现，且无日志输出。

**实现要点**

- **根因**：会话行右侧操作区用 `hidden group-hover:flex group-focus-within:flex` 收起，而「…」按钮正是 Radix 下拉菜单的 popper 锚点。菜单打开时 Radix 的模态层会把 `document.body` 设为 `pointer-events: none`，行不再命中 `:hover`；焦点同时移入 portal 里的菜单内容，`group-focus-within` 也随之失效。两个分支都消失后操作区回到 `display: none`，触发按钮没有任何布局盒，floating-ui 量到 0×0 的锚点矩形，于是把弹层放在视口 (0,0)——即窗口左上角。
- `frontend/src/lib/sessionRowActions.ts`：新增 `SESSION_ROW_ACTIONS_CLASS` 常量，在原有 hover / focus-within 分支之外补 `group-has-data-[state=open]:flex`。Radix 给触发按钮写入 `data-state="open"`，`:has()` 是纯 DOM 结构匹配、不受 `display:none` 与 `pointer-events` 影响，因此菜单打开的整段时间操作区都保持 `display:flex`，锚点矩形可正常测量。
- `frontend/src/pages/ChatPage.tsx`：会话行操作区改用该常量并加 `data-slot="session-row-actions"`；其余布局与交互不变（仍只在悬停/聚焦时露出，不长期占位）。
- 该选择器依赖 `:has()`，与本仓库 shadcn 组件已在使用的 `has-data-*` / `group-has-*` 变体同一机制，未引入新的 WebView 版本要求。

**测试证据**

- 新增 `frontend/src/lib/sessionRowActions.test.ts`：断言 class 含 `group-has-data-[state=open]:flex`（#28 回归守卫），并保留 `hidden` / `group-hover:flex` / `group-focus-within:flex`。
- `cd frontend && npx tsc --noEmit && npm test`：6 个文件、30 个用例全绿；`npm run build` 通过。
- 构建产物 `dist/assets/index-*.css` 确认生成 `.group-has-data-\[state\=open\]\:flex:is(:where(.group):has([data-state=open]) *){display:flex}`，特异性 (0,2,0) 高于 `.hidden` 的 (0,1,0) 且出现顺序在后，菜单打开时必然覆盖 `display:none`。
- `go vet ./internal/... .`、`go test -count=1 ./internal/...` 全绿。
- 本机预览服务未获授权启动，未做浏览器实机点击验证；修复依据为 Radix 模态层的 pointer-events / 焦点行为与构建产物选择器，待测试机安装新版本后确认菜单锚定位置。

### 第一百九十轮迭代：修复 Windows 发版门禁的 macOS 路径断言（已实施）

依据：v0.5.10 发版失败（Release run 41607432207）。`gate` 在 windows-latest 上执行 `go test -count=1 ./internal/... .` 时，`TestUpdateTargetForExe/darwin_bundle` 失败：`target = "\\Users\\u\\Applications\\WorkBuddy Agent.app"`，期望 `/Users/u/Applications/WorkBuddy Agent.app`；`publish` 因 gate 失败被跳过，没有产生 release。

**实现要点**

- **根因**：`updateTargetForExe` 的 darwin 分支用宿主机语义的 `filepath.Clean` / `os.PathSeparator` / `filepath.Join` 解析传入的 macOS 路径。macOS 上两者一致，但 Windows 上 `filepath.Clean("/Users/...")` 会把斜杠改写成反斜杠，拼回的目标变成 `\Users\u\...`。生产环境只有 macOS 会走到该分支，所以不影响运行；但该函数按 `goos` 参数设计为可跨平台测试，Windows runner 上的门禁必然红。
- `internal/services/updateleftover.go`：darwin 分支改用斜杠语义的 `path` 包（`path.Clean` / `path.Join`），并保留绝对路径守卫；结果只由 `goos` 决定，与宿主平台无关。
- `.github/workflows/ci.yml`：`windows-build` job 在构建后增加 `Test (windows)` 步骤，与 release gate 使用同一命令，让 Windows 专属失败在 push/PR 阶段暴露，而不是等打标签后才发现。
- `scripts/check-ci-build-coverage.sh`：校验 Windows job 的测试步骤存在，防止以后被删掉。

**测试证据**

- `go test -count=1 -run 'TestUpdateTargetForExe|TestSweepUpdateLeftovers' -v ./internal/services`：全部 PASS。
- `go test -count=1 ./internal/... .` 与 `go test -race -count=1 ./internal/services` 全绿；`go vet ./internal/... .` 通过。
- `GOOS=windows GOARCH=amd64 go test -c ./internal/services` 交叉编译通过（本机没有 Windows 运行环境，Windows 上的执行验证由 v0.5.11 的 release gate 完成）。

### 第一百八十九轮迭代：更新后自动清理桌面上的旧版本残留（已实施）

依据：issue #27（Windows 桌面安装版；反馈者自述 v0.5.8）。更新并重启后，桌面上仍留着上一版本应用的备份副本，右键删除 / 拖入回收站提示「文件正在使用」，只能一直留在桌面；期望新版本启动后不再占用旧版本文件。issue 同时指出与 #26（多开）可能互为因果。

**实现要点**

- **根因（残留必然存在）**：Wails updater 在 Windows 上无法删除仍被映射的可执行文件，只能先把目标改名为 `<exe>.old.<unixnano>` 再放入新文件；执行删除和清扫时 helper 自己仍运行在这个已改名的文件上，删除必然失败。helper 的清扫又只在下一次更新时发生——于是**每次 Windows 更新都会把上一版本副本留在应用旁边**；应用放在桌面时，这个副本就出现在桌面上。
- **根因（长期被占用）**：v0.5.8 及更早没有单实例锁，多开时只有发起更新的那个进程退出，其他旧实例仍映射着旧 exe（改名后即 `.old.*` 文件），Windows 因此持续拒绝删除，直到这些实例全部退出。#26 的单实例修复（v0.5.9）阻止再产生新的多实例，但已留下的残留文件仍需新版本主动清走。
- `internal/services/updateleftover.go`：新增启动清扫。目标路径按 updater 的同一规则推导（Windows/Linux 为 exe 本身，macOS 为 `.app` 包），只匹配 `<target>.old.<unixnano>`（UnixNano 时间戳须晚于 2020，避免误删用户同前缀文件）与 `<target>.bak`。
- 安全边界：**安装目标不存在时一律不清理**（此时备份可能是唯一可恢复的副本）；只删除常规残留文件，目录型 `.bak`（macOS bundle）在目标存在时才 `RemoveAll`；30 秒内的新文件不碰，避免与仍在进行的更新交接竞争。
- `UpdateService.attach` 在单实例锁已获取后启动清扫协程（二次启动进程在 `application.New` 内退出，不会误清）；30 秒重试一次、最多持续 10 分钟——helper 通常在新进程启动后 1 秒内退出，第一次重试即可删除；仍被旧实例占用时留待其退出或下次启动继续。`StopUpdates` 同时停止清扫协程。

**测试证据**

- 新增 `TestUpdateTargetForExe`：Windows 桌面 exe、Linux AppImage、macOS `.app` 包、普通二进制四种目标推导。
- 新增 `TestSweepUpdateLeftovers`：陈旧的 `.old.*` 删除、新鲜残留保留、`.bak` 文件删除、bundle `.bak` 目录删除、目标缺失时全部保留、`old.123` / 非数字后缀 / 其他应用的同名文件不误删、含 glob 元字符的目录名按字面匹配。
- 新增 `TestSweepUpdateLeftoversLoopRetriesUntilUnlocked`（首次因「过新」跳过、第二次重试删除）与 `TestSweepUpdateLeftoversLoopExitsWhenClean`（无残留立即退出）。
- `go vet ./internal/... .`、`go test -count=1 ./internal/... .`、`go test -race -count=1 ./internal/... .` 全绿。
- 本机为 macOS，Windows 的「文件被占用」无法实机复现；修复针对 updater 的平台无关残留命名与清理时机，待测试机再走一次更新并重启后确认桌面残留副本是否自动消失。

### 第一百八十八轮迭代：桌面端单实例限制，二次启动唤回已有窗口（已实施）

依据：issue #26（Windows 桌面安装版；版本字段为空，按 `createdAt`（11-03 08:14 UTC）推断为 v0.5.8）。保持应用运行，再次双击桌面图标或从开始菜单启动，会新开一个独立窗口/进程，重复启动可无限多开；期望第二次启动只激活并聚焦已在运行的窗口。

**实现要点**

- **根因**：`application.New` 没有配置 `SingleInstance`，Wails 不会获取任何单实例锁；每次启动都是完整的新进程，自行创建窗口、托盘、全局快捷键并启动后台服务。
- `main.go`：为应用配置 `application.SingleInstance`，`UniqueID` 使用 `build/config.yml` 的 `productIdentifier`（`com.workbuddy.agent`）且保持跨版本稳定——它同时是运行中实例与二次启动互相发现的锁名，改动会让新旧版本互不可见。第二实例由 Wails 平台内建机制识别（Windows 命名互斥量 + `WM_COPYDATA`，macOS 锁文件 + 分布式通知，Linux D-Bus），把启动信息转发给首个实例后以 `ExitCode` 退出，不再创建窗口或启动服务。
- 回调 `activateMainWindow`：`UnMinimise()` 还原最小化窗口（不用 `Restore()`，避免把用户的最大化/全屏窗口改回普通状态）→ `Show()` 覆盖关闭到托盘的隐藏态 → `Focus()` 置前。窗口句柄保存在 `atomic.Pointer`，二次启动早于窗口创建时安全跳过，不会触发数据竞争。
- 与自动更新兼容：Wails updater helper 先等待旧进程退出、再启动新二进制，更新重启仍能正常拿到单实例锁；若 helper 超时后重启旧二进制，新进程会检测到尚未退出的旧实例并转发激活，而不是并排开出两个窗口。

**测试证据**

- 新增 `TestSingleInstanceOptions`：`SingleInstance` 配置必须存在且 `UniqueID` 固定为产品标识（防止将来改锁名重新引入多开）、第二次启动只触发一次窗口激活、nil 激活器与 nil 窗口不 panic。
- `go vet ./internal/... .`、`go test -count=1 ./internal/... .`、`go test -race -count=1 ./internal/... .` 全绿。
- 本机为 macOS，Windows 无法实机复现；单实例检测与窗口激活使用 Wails 跨平台内建能力，待测试机更新后双击图标确认第二次启动唤回已有窗口。

### 第一百八十七轮迭代：项目展开后侧栏操作按钮不再被横向溢出裁掉（已实施）

依据：issue #25（Windows 11，v0.5.7）。所有项目折叠时，项目行右侧的「+ / 编辑 / ⋯」正常可见；只要展开任意一个项目，所有项目行右侧的按钮都消失，只剩空白。

**实现要点**

- **根因**：侧栏项目/最近列表包在 Radix `ScrollArea` 里，它的内容容器固定为 `display: table; min-width: 100%`；同时列表网格没有显式列宽（`grid` 的自动列按 min-content 取宽）。会话标题与 80 字摘要都是 `truncate`（`white-space: nowrap`），其 min/max-content 等于整行文本宽度。项目折叠时，会话行不渲染，侧栏宽度正常；展开后这些长文本把表格/网格撑到 500+ px，`overflow-hidden` 又把超出侧栏的部分裁掉——所有行等宽，于是所有项目行右对齐的三个按钮一起被裁到可视区之外。
- `frontend/src/pages/ChatPage.tsx`：侧栏列表改为普通块级滚动容器（`min-h-0 flex-1 overflow-x-hidden overflow-y-auto`），不再生成表格包装层；原生滚动条已由 `index.css` 统一为细深色样式，视觉不受影响。
- 列表网格与展开后的会话网格都补上 `grid-cols-1`（`repeat(1, minmax(0, 1fr))`），让列宽受容器约束，`truncate` 正常出现省略号，长标题/摘要不会再撑宽整行。

**测试证据**

- 用 stub 视觉回归夹具（`npm run stub`）注入一条带长摘要的项目会话，以 headless Chromium 量测：修复前侧栏滚动容器 `clientWidth=223`、`scrollWidth=576`，项目行宽 576px，三个按钮位于 522–574px（全部在可视区外）；修复后 `clientWidth=scrollWidth=239`，行宽 223px，按钮位于 169–221px，展开态与折叠态截图均可见「+ / 编辑 / ⋯」。
- `cd frontend && npx tsc --noEmit && npm test && npm run build` 通过。
- 本机为 macOS，Windows 11 无法实机复现；问题为浏览器布局计算，与平台无关，待测试机更新后确认。

### 第一百八十六轮迭代：项目行新建对话恢复与展开态按钮配色（已实施）

依据：issue #24（Windows 11；版本字段为空，按 `createdAt`（11-03 06:20 UTC）推断为 v0.5.6，晚于 v0.5.6 发布）。展开某个项目分组后点击行内「+」显示异常且无法新建项目会话；收起时同一组按钮显示正常、可以新建。

**实现要点**

- **根因（功能）**：目录在注册为项目之前，旧任务会话可能仍以显式工作区（`WorkspaceDir`）独占地占用该目录。`newSession` 对项目会话同样执行了任务互斥的 `workspaceConflict`，于是点击「+」只得到「工作区已被任务「…」使用」的错误，没有创建任何会话；收起/展开本身不改变后端行为，用户观察到的差异来自点到的是哪个项目分组。
- **根因（视觉）**：项目行三个操作按钮的悬停底色是 `hover:bg-background`——深色主题下 `background` 是近黑色。收起行的背景与侧栏同为 `background`，悬停方块看不出来；展开行是 `bg-accent/60` 高亮，悬停时按钮就出现突兀的近黑色圆角方块（issue 截图中的「…」）。
- `internal/services/chatservice_sessions.go`：`projectRootConflict` 与 `workspaceConflict` 仅在创建**任务会话**（`projectID == ""`）时执行。项目会话按设计共享项目根目录，遗留任务会话的独占绑定不再阻塞新建项目对话；任务会话之间仍保持目录互斥。
- `frontend/src/pages/ChatPage.tsx`：侧栏项目行与会话行的操作按钮悬停底色统一改为 `hover:bg-foreground/10`，在展开高亮行和普通行上都呈现一致的淡淡蒙层。

**测试证据**

- 新增 `TestNewSessionWithProjectSharesRootWithLegacyTask`：旧任务会话占用目录后注册项目，项目会话必须创建成功且仍解析到项目根；同一目录上的第二个任务会话仍被「已被任务」拒绝。
- `go vet ./internal/... .`、`go test -count=1 ./internal/...`、`go test -race -count=1 ./internal/...` 全绿。
- `npx tsc --noEmit`、`npm test`（28 用例）、`npm run build` 通过。
- 本机为 macOS，Windows 11 无法实机复现；后端修复为平台无关的工作区校验逻辑，悬停底色为纯样式变更，待测试机更新后确认视觉与新建流程。

### 第一百八十五轮迭代：模型流式响应正确收尾与工具参数 Schema 透传（已实施）

依据：issue #23（Windows 11；版本字段为空，按 `createdAt`（11-03 02:53 UTC）推断为 v0.5.4）。新会话发送「查看本项目是做什么的？」后一直停在回复等待状态、没有任何内容；模型厂家日志为 `ECONNRESET: The socket connection was closed unexpectedly`。v0.5.4 相对前一版对直聊的唯一行为变化是挂载了工作区文件工具（#21），因此首轮通常是工具调用而非文本。

**实现要点**

- **根因：`[DONE]` 不被当成终止信号**。OpenAI 兼容适配器把 `data: [DONE]` 当普通行跳过，继续等 EOF；Anthropic 适配器同样忽略 `message_stop`。提供方网关/代理在最后一个分块后保持 HTTP 响应不关闭时，适配器会一直阻塞：文本增量（若有）不落最终态、工具调用只存在于最终响应而永远不出现，界面停在「正在回复…」，直到单轮 600 秒兜底超时；客户端此时中断连接，正是模型厂家侧看到的 `ECONNRESET`。现在两个适配器在协议终止标记处立即收尾并产出最终响应。
- **流式请求收到非 SSE JSON 不再静默成功**：网关在上游断链时常用 HTTP 200 + 纯 JSON 错误对象回复流式请求；旧逻辑按 SSE 扫描后得到空回答。现在识别 JSON 正文并解析（错误对象按供应商错误上报，完整 completion 按非流式结果返回，多个 tool call 按位置保序，修复了完整响应没有 index 字段时全部合并到 0 的问题）。
- **工具参数 Schema 透传**：ADK 内置工具、Skills 与 MCP 工具把 Schema 放在 `FunctionDeclaration.ParametersJsonSchema`，适配器此前只读旧的 `Parameters` 字段，导致所有工具都以无参数的 `{"type":"object"}` 发出（模型无法正确调用 `read_workspace_file` / skill / MCP 工具）。现在优先取 JSON Schema（`map[string]any` 直接使用，其他类型经 JSON 规范化），旧字段保留兜底。
- **流式静默看门狗**：流打开后超过 2 分钟没有任何数据（无分块、无 keep-alive）即以明确错误中断本轮，而不是等到 10 分钟单轮上限；错误文案提示检查网络/代理。
- 顺带：空响应（空正文、仅噪声行、只有终止标记）与纯噪声正文（HTML 代理拦截页）都给出可读错误，而不是以空回答结束回合。

**测试证据**

- 新增 `TestOpenAICompatStopsAtDone` / `TestAnthropicStopsAtMessageStop`：服务端发出终止标记后保持连接不关闭，适配器必须在 2 秒内完成（旧实现会等 5 秒上下文超时）。
- 新增 `TestOpenAICompatStreamRequestAnsweredWithJSONError` / `TestAnthropicStreamRequestAnsweredWithJSONError`：HTTP 200 + JSON 错误对象必须上报（覆盖 issue 中的 ECONNRESET 形态）。
- 新增 `TestOpenAICompatStreamRequestAnsweredWithJSONCompletion`：纯 JSON 完整响应中的两个 tool call 都必须保留（含参数）。
- 新增 `TestStreamIdleTimeoutAbortsStalledProvider`：首个分块后静默的流在缩短的看门狗时间内中断并给出中文错误。
- 新增 `TestFunctionParametersFromJSONSchema`（OpenAI `parameters` / Anthropic `input_schema` 均携带属性与 required）与 `TestFunctionParametersLegacySchemaStillWorks`。
- 扩充 `TestDirectChatReadsWorkspaceFiles`：直聊首轮请求必须包含 `"properties":{"path"` 与写入工具的 `required`，钉住 issue 的复现场景。
- `go vet ./internal/... .`、`go test -count=1 ./internal/...`、`go test -race -count=1 ./internal/...` 全绿。
- 本机为 macOS，Windows 11 无法实机复现；修复针对的是平台无关的 HTTP/SSE 协议收尾与工具 Schema 透传，待测试机更新后确认。

### 第一百八十四轮迭代：更新重启不再残留旧进程（已实施）

依据：issue #22（Windows 11；版本字段为空，按 `createdAt`（11-03 02:10 UTC）推断为 v0.5.3，早于 v0.5.4 发布）。「设置 → 关于 → 检查更新 → 下载并安装 → 重启完成更新」后新版本启动，但旧版本窗口仍在，任务管理器里同时有两个应用进程。

**实现要点**

- 根因分两层：Wails v3 updater 的 helper 只给父进程 **30 秒**退出窗口（`pkg/updater/helper.go`），超时会放弃替换并重启**旧版**二进制（退出码 17）——用户看到的就是「又启动一个应用、旧的还在」；而 `App.Quit()` 是同步阻塞的，本应用 `OnShutdown` 里的 `MCP.CloseAll()`/`A2A.StopAll()` 会在主线程上等待子进程与流式请求收尾，单个 MCP stdio 子进程关闭最多可花 10 秒级，足以把整个关闭流程拖过 helper 的等待窗口。
- `main.go`：新增 `shutdownBounded`（5 秒上限 + panic 兜底），`OnShutdown` 的 A2A 下线与 MCP 关闭各自独立限时，任何挂死的收尾都不能再占满 Quit 与 helper 之间的预算；超时只记日志并继续退出。
- `internal/services/updateservice.go`：`ApplyUpdate` 不再直接同步调用 `Updater.Restart`，改为 `applyWithWatchdog` 看守——12 秒看门狗之后若 helper 的就绪握手文件（`<tmp>/wails-update-<pid>-*.ready` 内容为 `ready`）已存在，说明 helper 正阻塞等待本进程退出，此时强制 `os.Exit(0)` 让替换与重启完成；`Restart` 返回成功后额外保留 5 秒宽限，进程若仍未退出也强制退出。`Restart` 失败（含 helper 未就绪）时不强制退出，保持应用可重试。
- 就绪文件只认本进程 PID 且内容必须为 `ready`：`error:` 载荷（helper 已放弃）与其他进程的握手都不会触发退出，避免把「更新未完成」变成「应用直接消失」。

**测试证据**

- 新增 `TestHelperAwaitingExit`（无文件 / error 载荷 / ready 载荷 / 其他 PID 四组）。
- 新增 `TestApplyWithWatchdogRestartResult`（成功arm宽限退出、失败保持运行）、`TestApplyWithWatchdogExitsWhenHelperStandsBy`（Quit 永不返回 + helper 就绪 → 触发强制退出）、`TestApplyWithWatchdogSlowHelperFailure`（helper 未就绪且 Restart 失败 → 错误透传、不退出）。
- 新增根包 `TestShutdownBounded`：挂死钩子 25ms 超时返回、正常钩子执行、panic 不向 Quit 传播。
- `go vet ./internal/... .`、`go test -count=1 ./internal/... .`、`go test -race -count=1 ./internal/... .` 全绿。
- 本机为 macOS，Windows 11 的双应用现象无法实机复现；修复针对的是平台无关的关闭预算与 helper 握手契约，待测试机更新到新版本后再走一次「下载安装 → 重启生效」确认。

### 第一百八十三轮迭代：直聊项目会话可读取工作区（已实施）

依据：issue #21（Windows 11；版本字段为空，按 `createdAt` 推断为 v0.5.3；截图中会话时间为 11 月 2 日 20:28，早于 v0.5.2 发布）。两处症状：

- 项目分组展开后看不到「新增会话」按钮。截图为 v0.5.1 的「按钮仅悬停显示」；该问题已在第一百八十一轮（v0.5.2）改为常驻显示，当前代码对已注册项目分组始终渲染加号，无需再改。
- 在项目会话里问「当前项目主要是做什么的？」，助手只回「我来查看当前项目情况。」便停住，没有工具过程与后续回答。根因：直聊的临时 Agent（`directAgentConfig`）没有任何内置工具，系统提示也不包含会话工作区，模型无法读取目录，只能口头承诺后结束。

**实现要点**

- `internal/services/chatservice_render.go`：直聊临时 Agent 挂载 `files` 内置工具（列目录 / 读文件 / 写文件），`ConfirmTools` 同步设为 `files`——读取自动执行，写入仍走 HITL 人工确认；系统提示加入 `{{workspace}}`，要求先列目录、再按需读文件、基于实际内容回答。
- `internal/agentkit/kit.go`：`ListFilesTool` 改用独立的 `listArgs`，`path` 变为可选（省略即工作区根），避免模型第一次「看看项目」的无参调用被 JSON Schema 的 required 拒绝。
- `internal/agentkit/kit.go` 的直聊装配复用既有会话工作区解析：项目会话读取项目根，任务会话读取任务目录，`@` 路由到具名 Agent 时仍用该 Agent 自己的工具配置。

**测试证据**

- 新增 `TestDirectChatReadsWorkspaceFiles`：假 SSE 模型走「列目录 → 读 README → 回答」三轮，断言请求携带工作区工具与 `{{workspace}}` 解析后的路径、工具结果回传给模型、事件流包含两次 `tool_call`/`tool_result` 与最终消息。
- 新增 `TestDirectChatWriteAsksConfirmation`：直聊写文件先产生 `confirm_request`，未批准前文件不落盘。
- `go vet ./internal/... .`、`go test -count=1 ./internal/...`、`go test -race -count=1 ./internal/...` 全绿。
- 本机为 macOS，Windows 11 无法实机复现；改动均为平台无关的 Agent 装配与工具 Schema，待测试机更新后确认。

### 第一百八十二轮迭代：设置通用页目录选择与布局修复（已实施）

依据：issue #20（Windows 11，v0.5.2）——「设置 → 通用」的工作区目录只有输入框，无法弹窗选择；两列网格按行等高，外观、A2A 等高卡片把同行的短卡片撑出大片空白，整体显得松散。

**实现要点**

- `frontend/src/pages/SettingsPage.tsx`：
  - 工作区目录新增「浏览…」按钮，复用 Wails `Dialogs.OpenFile` 原生目录选择器（与对话页「添加项目」同一条调用路径），取消选择经 `isDialogCancelError` 视为“未选择”，不改动表单，仍支持手动输入。
  - 通用页与数据页改为瀑布式两列（`columns-1 lg:columns-2` + `break-inside-avoid`），卡片按内容高度紧密排列，短卡片不再被同行高卡片撑出空白；知识库、关于等单卡片页保持 `max-w-3xl` 单列，避免右半屏空置。
  - 「保存设置」吸底条移出网格容器，滚动时仍贴底显示。
  - 占位符与说明改为平台无关文案，并注明修改后对新建任务生效。
- `frontend/src/lib/workspace.ts` 与 `workspace.test.ts`：新增 `pickedDirectoryValue`，把空串/非字符串返回值统一视为“未选择”，配套回归用例。

**测试证据**

- `npx tsc --noEmit` 干净；`npm test` 28 用例通过（新增 pickedDirectoryValue 2 组）；`npm run build` 通过（产物含 `columns`/`break-inside` 规则）。
- `go vet ./internal/... .`、`go test -count=1 ./internal/...` 全绿。
- 本机为 macOS，Windows 11 的系统目录对话框无法实机复现；选择器与对话页既有路径一致，待测试机更新后确认。
- 本次自动运行未能启动 stub 预览（启动请求被拒绝），未做浏览器走查。

### 第一百八十一轮迭代：修复输入区附件、模型选择与项目新建对话（已实施）

依据：issue #19（Windows 11，chat 输入区多项交互异常）——「+」附件因模型多模态标记缺失被禁用；语音输入在 WebView2 中不可用；模型下拉在 `SelectContent` 内嵌原生按钮导致布局与键盘行为异常；项目与任务工作区各有一个目录入口，取消系统目录选择被当成失败、项目会话再选目录直接报错；左侧项目分组的「新建对话」加号仅悬停时出现。

**实现要点**

- `frontend/src/pages/ChatPage.tsx`：
  - 按期望暂时移除语音输入按钮与识别实现（`speechSupported` / `toggleVoiceInput` / `listening`），保留消息朗读（TTS）。
  - 附件按钮不再因 `multimodal` 标记禁用：未标记时仍可添加图片，并以 toast 提示模型可能无法识别；粘贴与拖拽同样放行；首轮尚无会话时发送不再丢弃已排队图片附件。
  - 会话模型选择由 Radix Select 改为 DropdownMenu 单选组：触发器显示当前生效模型，菜单列出默认项与模型 ID，底部独立「管理模型供应商…」，修复在 `SelectContent` 内嵌原生按钮造成的错乱。
  - 项目与任务目录合并为一个下拉菜单（当前项带勾选）；「选择其他目录…」在项目会话或首轮后禁用并内联显示原因；当前工作区路径改为只读展示，不再提供第二个可点击目录入口。
  - `pickWorkspace` 捕获 Wails 返回的 `cancelled by user` 并视为“未选择”，取消系统目录对话框不再弹出「更新工作区失败」；项目会话改选目录在 UI 层提前拦截。
  - 左侧项目分组的「新对话」加号改为常驻显示，折叠或展开分组都能直接新建。
- `frontend/src/lib/workspace.ts` 与 `workspace.test.ts`：新增对话框取消识别（`isDialogCancelError`）与工作区锁定原因（`workspaceLockReason`）纯函数及回归用例。

**测试证据**

- `npx tsc --noEmit` 干净；`npm test` 26 用例通过（新增 workspace 5 组）；`npm run build` 通过。
- `go vet ./internal/... .`、`go test -count=1 ./internal/...` 全绿。
- 本机为 macOS，Windows 11 的 WebView2 语音与系统目录对话框无法实机复现；修复依据 issue 截图与 Wails 取消错误文案，待测试机更新后确认。
- 本次自动运行未能启动 stub 预览（启动请求被拒绝），未做浏览器走查；改动已通过类型检查、单测与生产构建。

### 第一百八十轮迭代：对话输入区重构为一体化 composer（已实施）

依据：issue #18（Windows 11，chat 模块整体 UI 排版）——顶部会话栏同时承载项目、任务工作区与模型选择，窄窗口下相互挤压；输入框把附件与语音按钮悬浮在文本区，发送按钮在框外，缺少参考设计里「输入框内完成模型选择与发送、框下工具条选择目录」的一体化布局。

**实现要点**

- `frontend/src/pages/ChatPage.tsx`：
  - 顶部会话栏移除项目、任务工作区与模型下拉，只保留标题/目标徽标/会话搜索/重命名，降低窄窗口拥挤。
  - 输入区改为圆角卡片（`rounded-2xl border bg-card`），聚焦、拖拽、语音高亮统一作用在卡片上；文本域无边框并保留自动增高，占位改为「今天帮你做些什么？输入 @ 添加上下文，/ 调用技能与指令」。
  - 卡片底行：左侧「+」附件与语音按钮，右侧本会话模型下拉与圆形发送/停止按钮（编辑态提示「重新发送」）。
  - 卡片下方工具条：项目/目录下拉（默认项目、项目列表、选择其他目录、添加新项目，首轮后锁定）＋当前任务工作区路径按钮＋「Enter 发送 · Shift+Enter 换行」提示。
  - 目录与模型选择继续复用既有 `switchSessionProject` / `changeSessionWorkspace` / `setSessionModel`，首轮锁定与错误提示行为不变。
- 参考图中的分支选择、Worktree 与「允许完全访问」权限切换在当前产品没有对应后端能力，未伪造 UI；本轮聚焦既有控件的排版归位。

**测试证据**

- `npx tsc --noEmit` 干净；`npm test` 21 用例通过；`npm run build` 通过。
- `go vet ./internal/... .`、`go test -count=1 ./internal/...` 全绿。
- 本次自动运行未能启动 stub 做浏览器走查（预览服务启动被拒绝）；改动仅涉及 JSX 结构与样式类名，建议在 Windows 测试机更新后补充实机视觉确认。

### 第一百七十九轮迭代：应用内“反馈问题”入口（已实施）

依据：自动修复流程要求 issue 带版本号，测试机手填易错易漏；应用内已知版本与平台，应在提交入口自动预填。

**实现要点**

- `internal/services/app.go`：`AppInfo` 新增 `platform`（GOOS/GOARCH），供反馈入口预填环境。
- 前端 `lib/issue.ts`：`buildIssueReportURL` 构造带 `field:version`、`field:environment` 的 GitHub issue 表单链接；`lib/issue.test.ts` 覆盖 v 前缀、dev、空值与平台映射。
- `SettingsPage` 关于页新增「反馈问题」按钮，经 `@wailsio/runtime` 的 `Browser.OpenURL` 打开系统浏览器。
- `.github/ISSUE_TEMPLATE/bug_report.yml` 版本字段改为可选：应用内入口自动填入，手动提交可留空，由自动流程按 issue 创建时间推断。

**测试证据**

- `go vet ./internal/services/`、`go test ./internal/services/` 通过（`TestAppInfoReportsInjectedVersion` 覆盖 platform）。
- `npx tsc --noEmit` 干净；vitest 21 用例通过（新增 `issue.test.ts` 4 组）；`npm run build` 通过。
- stub 走查：设置 → 关于出现「反馈问题」按钮与自动填入说明。

### 第一百七十八轮迭代：自动发布与自带检查更新（已实施）

依据：应用只能靠手工打包分发，且版本号在三处手写并已漂移（`main.go`/`build/config.yml` 为 0.4.0，而 `build/windows/info.json`、`wails_tools.nsh`、`nfpm.yaml` 仍是 0.1.0——安装包元数据与 deb 版本都在说谎）。用户侧则完全没有升级路径。

**实现要点**

- **版本号单一来源**：`main.go` 的 `Version` 由 `const` 改为 `var` + 构建期 `-ldflags "-X main.Version=..."`；`Taskfile.yml` 新增 `VERSION`（取 git tag，可被环境变量覆盖），三平台 `BUILD_FLAGS` 注入；新增 `scripts/set-version.sh` 重写 `build/config.yml` 并调用 `wails3 update build-assets` 派生全部安装包元数据；`task version:sync`、`task release:next` 入口。
- **应用内更新**：新增 `internal/services/updateservice.go`，基于 Wails v3 原生 `app.Updater` + endpoint provider 读取 GitHub Releases 上的签名清单（`releases/latest/download/manifest.json`）；RPC 为 `Status/CheckNow/InstallUpdate/ApplyUpdate/Skip/SetAutoCheck/OpenDownloadPage`；`BindUpdates`/`StopUpdates` 用包级函数而非方法，避免「用 App 句柄配置自己」被暴露成前端 RPC。
- **检查策略**：自建 10s 首次 + 24h 循环（**不用** `Config.CheckInterval`，框架的周期循环走 `CheckAndInstall`，会替用户静默安装）；失败只记日志；`Settings.AutoCheckUpdate`（nil=开）+ `Settings.SkippedVersion` 持久化在 `config.json`。
- **安装形态判定**：`detectUpdateMode` 按 `$APPIMAGE` 与「可执行文件所在目录可写」决定 `auto-install` / `notify-only`，`.deb` 与 Windows 系统级安装降级为「提示 + 打开下载页」（updater 无提权路径）。
- **前端**：`components/UpdateCard.tsx` 内联在「设置 → 关于」，监听 10 个 `wails:updater:*` 事件后统一回读服务快照（不镜像状态机），含进度条、更新说明折叠、跳过此版本、自动检查开关。
- **发布流水线**：`.github/workflows/release.yml`——`gate`（Windows 上跑 tsc/vet/test，不跑 race，与构建并行）→ Windows 构建免安装单文件 exe → 单 job 原子发布（`SHA256SUMS` + `wails3 updater manifest` 清单 + `gh release create --latest`），支持 `workflow_dispatch` 重跑与 `dry_run`。**当前仅发 Windows**：测试环境只有 Windows，Linux/macOS 构建配置保留在 git 历史，测试条件具备后按 0.MINOR 补发。
- **文档**：新增 `GLOSSARY.md`、`docs/adr/0001`、`docs/adr/0002`，README 增补发布/更新/已知边界。

**测试证据**

- 新增 `internal/services/updateservice_test.go`（8 组 `detectUpdateMode` 表格用例、公钥解析、Bind 前 `Status` 不 panic、Skip/自动检查持久化与重载）、`internal/config/config_test.go`（自动检查默认开、`false` 经 JSON 往返不丢失）。
- `go vet ./internal/... .`、`go test ./internal/config/ ./internal/services/` 全绿；`npx tsc --noEmit` 干净，前端 vitest 17 用例通过。
- `wails3 generate bindings` 重新生成：7 个服务、92 方法（新增 `updateservice.ts`）。

### 第一百七十七轮迭代：收官验证（07:10 ALL GREEN）

第三次全量验证通过（verify 链：前端 vitest/tsc → go vet → 全量测试 → -race → wails3 build），无回归。

**保活验证记录**：Beat 1（02:05）/-race 全绿；Beat 2（03:08）/-race 全绿；Beat 3（04:08）/-race 全绿 + tsc 干净；Beat 4（05:08）/-race 全绿；Beat 5（06:08）/-race 全绿；Beat 6（07:10）/race + vitest + tsc 全绿、工作树干净。

**本次迭代会话总览**（第 154–177 轮，23 个提交，686911c..HEAD）：

- **能力**：任务变更行级 diff（基线内容快照 + 有界 diff 引擎 + DiffView/复制 Patch/预览内切换）、批量清空归档会话、RevealWorkspaceFile 外部打开。
- **体验**：消息操作解禁、↑ 编辑、Esc 优先级链、composer 自动增高、斜杠键盘导航、排队多条不覆盖、拖拽高亮、切换加载骨架屏、回到底部浮钮、过滤命中高亮、ConfirmDialog 化、按钮收敛、快捷键补全（⌘⇧[⇧]）、渲染截断翻页、空态统一。
- **质量**：流式 delta 合并与索引惰性化（性能）、a11y（live region/listbox/focus-within）、浅色主题走查、macOS 测试可移植性、diff 引擎随机化不变量测试、双轴代码审查修复批。
- **架构**：chatservice.go 2433 行拆六文件、ChatPage 尾部组件迁出、gofmt 全仓清零。
- **验证基线**：`go test -race ./internal/... .` 本机首次可跑且持续全绿（约 225 用例）；wails3 build 产物 macOS 烟雾通过；桩走查覆盖全部新 UI。

### 第一百七十六轮迭代：会话内过滤命中高亮（已实施）

依据：UX 盘点最后一个缓办项——⌘F 过滤只筛掉不匹配的行，命中关键词在行内什么位置仍需目扫。Markdown 内联高亮需 remark 插件链改造，本轮先覆盖明文渲染路径。

实现要点：

- 新增 `highlightText`：大小写不敏感切分文本并包 `<mark>`，空查询原样返回（调用点无需条件化）。
- 应用三处明文路径：用户气泡（主色前景内的 30% 反白 mark）、错误气泡（destructive 25%）、工具卡展开区 JSON pre。助手 Markdown 内联高亮列为已知边界（markdown 语法树注入需 remark 插件，收益/复杂度比暂不划算）。
- MessageBubble/ToolCallCard 以 `filterQ` prop 传入（memo 属性变化在过滤输入时本就整列表重渲，无额外成本）。

测试证据：`tsc --noEmit`、vitest 17/17、`npm run build` 通过。浏览器桩走查：⌘F → 输入 weekly → 4/4 计数与用户气泡内 `<mark>weekly</mark>` 渲染确认。

### 第一百七十五轮迭代：生产构建烟雾（macOS 首验，已实施）

依据：发布就绪检查项「wails3 build 产物启动存活 + 优雅退出」此前只在 Windows 实测过；本仓库迁至 macOS 开发后生产链路（bindings → 前端构建 → Go 编译 → 启动）未验过。

实现要点：无代码变更——`wails3 build`（31MB 二进制）→ 启动存活 5s → SIGTERM 优雅退出，日志无报错。macOS 链接器告警（object file built for newer macOS）为工具链版本提示，不影响产物。

测试证据：`wails3 build` 成功；烟雾存活/退出通过。

### 第一百七十四轮迭代：diff 引擎随机化不变量测试（已实施）

依据：行级 diff 是本轮最大的新后端组件，表驱动用例覆盖了固定场景；补随机化测试钉住算法级不变量（重放构造、行号单调、计数一致），让后续对 diff 路径的任何改动都有回归网。

实现要点：

- `filediff_property_test.go` 两个性质测试：
  - **Small（DP 路径）**：300 次随机生成小文件对（插入/删除/替换/重复行），断言 hunks + 旧文件可重放出新文件（统一 diff 语义：hunk 之间为未变行照抄、hunk 内 context/del 行号连续且与源文本一致、add 行不带 oldNo）。
  - **Chunked（分块路径）**：15 次 4000–8000 行大文件 + 多个随机窗口（最多 ±800 行的删改插）变异，同一不变量在分块递归产物上成立。
- 辅助断言：渲染行数统计与 added/removed 总数一致（未截断时）。
- 第一版不变量实现错误（把 hunk 之间的未变行也要求连续）被测试自己抓出——修正为真实统一 diff 语义后全过，反向印证了断言有效性。

测试证据：`go test ./internal/services -run TestLineDiffProperty -count=1` 全绿；与既有表驱动/生命周期用例共同覆盖 diff 引擎。

### 第一百七十三轮迭代：仓库卫生——gofmt 清零 + 依赖漏洞复扫（已实施）

依据：抽查发现仓库存在 15 个未 gofmt 的历史文件（导入顺序/缩进偏差，CI 未强制 gofmt 故积累）；同时按惯例复扫依赖漏洞。

实现要点：

- `gofmt -w internal/ main.go` 一次性清零（15 文件，纯格式化），`gofmt -l` 复查为 0。
- 依赖复扫：Go 侧本轮零新增依赖（diff 引擎手写未引库）；npm audit 余 6 项（2 low / 4 moderate）全部位于 shadcn CLI 的 devDependency 链（deepmerge 原型污染、minimatch DoS），不进生产产物，与第 136 轮结论一致，保持观察。

测试证据：`go vet ./internal/... .` 干净、`go test ./internal/... . -count=1` 全绿（格式化零行为变更）；前端 `tsc --noEmit`、vitest 17/17、`npm run build` 通过。

### 第一百七十二轮迭代：批量清空已归档会话（已实施）

依据：归档列表只能逐条删除，长期使用后堆积的归档会话没有批量清理出口；删除会话的取消等待/事件孤儿清理/基线清理链路已由 DeleteSession 验证，批量只是入口问题。

实现要点：

- `DeleteSession` 抽出 `deleteSessionInternal`（取消等待 → ADK 会话删除 → 事件孤儿行清理 → 配置移除 → 基线与锁清理 → 审计），新增 `DeleteArchivedSessions() (int, error)` 遍历归档会话逐条复用，返回删除数量，中途失败带会话 ID 报错。
- 侧栏归档组头部（展开态）新增「清空」按钮，走 ConfirmDialog（标题含条数、说明任务文件保留），成功后 toast 报数并刷新列表。
- 桩补 DeleteArchivedSessions 处理器（3028356991）。

测试证据：新增 `chatservice_archive_bulk_test.go`（两条归档+一条活动 → 删 2 留 1、基线文件随之清理、空列表幂等返 0）；`go test ./internal/services` 全绿；绑定再生成后 `tsc --noEmit`、`npm run build` 通过；浏览器桩走查：展开归档组 →「清空」→ 确认 → 归档组整体消失（0 条不渲染），桩处理器命中。

### 第一百七十一轮迭代：会话行操作键盘可达（已实施）

依据：无障碍复查收尾——会话行的归档/置顶/「…」按钮只在行悬停时显现（纯 CSS group-hover），键盘用户 Tab 到行内时按钮仍不可见，操作无法定位。

实现要点：两处行操作容器（会话行/项目行）补 `group-focus-within:flex`——Tab 聚焦进入行内时操作按钮与悬停同等显现，纯 CSS 改动零行为影响。

测试证据：`tsc --noEmit`、`npm run build` 通过；CSS 组合类为 Tailwind 标准变体，无运行时逻辑。

### 第一百六十九轮迭代：预览对话框内嵌「与任务基线对比」（已实施）

依据：行级 diff 此前只在变更列表的展开区可见；用户从产物/全部文件列表点开预览对话框后想看 diff 必须关闭对话框回到变更标签，路径断裂（Codex 的预览与审查是一体的）。

实现要点：

- `DiffView` 从 TaskWorkspacePanel 导出复用；ArtifactPreview 在 `sessionID` 存在且产物为 text/markdown 时，对话框底部操作行提供「与任务基线对比 / 返回预览」切换（aria-pressed），切换内容区为 diff 渲染；全局工作区预览（无 sessionID）与图片/PDF 不提供切换。切换目标文件变化时自动重置回预览态。

测试证据：`tsc --noEmit`、vitest 17/17、`npm run build` 通过（修复一次导出替换误伤注释块与 DiffView 导入缺失，均由编译器定位）。浏览器桩走查：产物标签预览 weekly.md →「与任务基线对比」→ hunk 渲染 →「返回预览」可用。

### 第一百六十八轮迭代：渲染截断分页 + diff 复制 Patch（已实施）

依据：UX 盘点遗留两项：RENDER_LIMIT=200 截断后只有说明文字，无法回看更早消息；行级 diff 视图没有复制出口（贴到评审/issue 里的高频动作）。

实现要点：

- **显示更早**：`renderLimit` 状态替代常量截断（保持 200 初始值），省略横幅内嵌「显示更早 200 条」按钮按步进翻页，切换会话重置；`visibleMessages` 与 DaySeparator 边界逻辑随 limit 生效。
- **复制 Patch**：DiffView 底部统计行右侧补 CopyButton，序列化为标准 unified 文本（`@@` 头 + 空格/+/− 前缀行），与 hunk 渲染同源。

测试证据：`tsc --noEmit`、vitest 17/17、`npm run build` 通过。浏览器桩走查：注入 210 条终态消息后截断横幅出现（渲染 200 条），点击「显示更早 200 条」后全部渲染、横幅消失；diff footer 复制按钮就位。

### 第一百六十七轮迭代：双轴代码审查修复批（已实施）

依据：对第 154–166 轮全部提交（3d8eb08^..HEAD，13 提交 37 文件）执行 Standards/Spec 双轴审查（并行子代理），汇总 1 条硬违规、6 条判断题、2 条实现与声明不符。

修复清单：

- **硬违规（文档地图未同步拆分）**：AGENTS.md 与 README.md 的目录地图仍把 runTurn/HITL/分叉/并行全部记在 chatservice.go 名下，按六文件拆分后的职责逐文件更正（README 原行尾「护栏」归位到 turn 行）。
- **重复代码（RevealWorkspaceFile vs sessionWorkspace）**：提取共享 `sessionWorkspaceOf(cfg, sessionID)`，两处调用统一；顺带消除 `a.S.Store.Get()` 在 range 头与循环体内各调一次的双深拷贝（AGENTS：Get 返回深拷贝）。
- **重复口径（changeStatus vs buildTaskSnapshot）**：提取 `classifyChange(base, existed, size, modTime)` 供快照 diff 与 FileDiff 共用，文件级分类单点维护；顺带删除冗余 `statOK` 参数。
- **死代码**：filediff_test.go 的 `_ = mustSplit` 占位行删除。
- **删除确认丢失 await**：ConfirmDialog 回调里 `Chat.DeleteSession` 改回 await 后再刷新列表（与旧实现语义一致）。
- **语音 updater 不纯（Spec）**：onresult 不再在 setState 更新器内改写闭包变量（StrictMode 重放会双次前滚），改为处理器内以 DOM textarea 值为实时真源同步计算；onend 重置识别段；中间手动编辑导致无法按后缀剥离时自然退化为追加（注释声明该边界）。
- **Shift+↑ 误触编辑（Spec）**：↑ 编辑守卫补 `!e.shiftKey`（Shift+↑ 是文本选择）。

测试证据：`go test ./internal/... . -count=1` 全绿、`go vet` 干净；`tsc --noEmit`、vitest 17/17、`npm run build` 通过；`go test -race ./internal/... .`（本机首次可跑，clang 就绪）全绿——含 filediff/快照/工作区全部新代码的并发回归。审查确认项：Spec 轴逐轮核对无缺失、无 scope creep；排队数组/Esc 链/成本口径/Reveal 校验链/chatservice 拆分零行为搬移均与声明一致。

### 第一百六十六轮迭代：浅色主题走查——本批新 UI 主题适配验证（无代码变更）

依据：第 155–165 轮新增了大量带语义配色的 UI（行级 diff 红绿行、未读点、ConfirmDialog、EmptyState、回到底部浮钮、拖拽高亮），需确认浅色主题下全部可读。

走查结果：浏览器桩切浅色后逐项检查——diff 加/删行背景为 10% 透明度的 emerald/red、文字用 `emerald-700`/`red-700`（浅色变量为深色文字，对比度充足）；确认卡（amber/destructive 边框 + 浅底）、任务计划卡、EmptyState、浮钮均走语义 token，浅色渲染正常。无需修改。

## 视觉测试与体验设计

### 第一百六十五轮迭代：空态统一——EmptyState 共享组件（已实施）

依据：设计一致性盘点：管理页空态各自即兴——Agents/Models/Teams/Skills/MCP 用单行 muted 文本（字号、内边距各不相同），没有图标锚点，与对话页的 Onboarding 卡风格割裂。

实现要点：

- 新增 `components/EmptyState.tsx`：居中圆形图标位 + 标题 + 描述（可选），一处视觉语言。
- 六处空态迁移：Agents（主列表/远程 Agent）、Models（供应商列表）、Teams、Skills、MCP（描述含代码示例改为表达式字符串保留 code 语义）。
- 侧栏空态保持紧凑样式不变（窄栏内 EmptyState 的内边距过大，非本次目标）。

测试证据：`tsc --noEmit`、vitest 17/17、`npm run build` 通过；迁移由编译器保证 JSX 合法性（MCPPage 的跨行空态在脚本替换后由手工修复并通过类型检查）。

## 视觉测试与体验设计

### 第一百六十四轮迭代：外部打开 + 直聊成本口径 + 语音输入覆盖修复（已实施）

依据：UX 盘点三处：不可预览文件（二进制等）在结果区只有「需外部打开」的说明文字却没有打开入口；直聊会话的成本估算固定取默认供应商第一个模型，会话级换模型后（SetSessionModel）估算口径失真；语音输入在录音开始时快照输入框内容，录音期间手动输入会被识别结果覆盖。

实现要点：

- **RevealWorkspaceFile RPC**（AppService）：解析会话有效工作区（项目会话贯通）→ SafeWorkspacePath 防穿越 → 校验文件存在 → 按平台命令在文件管理器中显示（macOS `open -R` 选中、Windows `explorer /select,`、Linux `xdg-open` 打开父目录）。结果区不可预览的文件行从纯禁用改为「显示」按钮（ExternalLink 图标 + aria-label），预览禁用语义不变。
- **成本口径**：`target` 解析镜像后端 `EffectiveModel` 钳制——会话级 `modelOverride` 在解析出的供应商模型列表内则优先（直聊=默认供应商、Agent=其供应商），否则回落原解析；直聊与 Agent 两条路径统一，杜绝「换了模型还按旧模型计价」。
- **语音输入**：识别文本改为「从当前输入末尾替换上次识别段」——onresult 每次从实时输入中剥掉上一轮 interim 文本再追加新文本（空格分隔），手动输入全程保留。

测试证据：新增 `app_reveal_test.go`（revealCommand 三平台参数、未知会话/路径穿越/文件不存在校验）；`go test ./internal/... . -count=1` 全绿、`go vet` 干净；绑定再生成后 `tsc --noEmit`、vitest 17/17、`npm run build` 通过。浏览器桩走查：archive.zip 行出现「显示」按钮，点击命中桩处理器并记录 relPath；成本口径与语音路径为纯前端逻辑，由类型与代码审查覆盖。

## 视觉测试与体验设计

### 第一百六十二轮迭代：ChatPage 组件拆分——消息渲染独立（已实施）

依据：ChatPage.tsx 单文件 2511 行，尾部 320 行是六个只吃 props 的自包含组件，与管理页状态无耦合，可无风险迁出，为后续 Sidebar/Composer 拆分立边界。

实现要点：

- 迁至 `components/chat/`：`MessageBubble.tsx`（MessageBubble + ConfirmCard + DaySeparator，memo 语义不变）、`TodoCards.tsx`（TodoItem 类型 + TodoListCard + PlanStrip）、`Onboarding.tsx`。
- ChatPage 相应导入并裁剪不再使用的 icons/组件导入，2511→2427 行。
- 纯 props 组件搬移，无状态迁移。

测试证据：`tsc --noEmit`、vitest 17/17、`npm run build` 通过；浏览器桩走查用户气泡、任务计划卡、确认卡渲染正常（拆分后回归）。

### 第一百六十三轮迭代：回到底部浮钮 + 工具 JSON 复制 + 附件大图（已实施）

依据：UX 盘点三处高频细节：向上翻历史时来新消息没有任何「回到底部」入口（只能手动拖滚动条）；工具卡展开区是裸 JSON pre 无复制按钮；用户消息的附件缩略图不可点击放大。

实现要点：

- **回到底部浮钮**：消息视口 scroll 监听（passive）维护 `atBottom`（阈值与自动滚动一致 120px），离底且有消息时在聊天列右下渲染「回到底部」圆钮，点击 `scrollIntoView` 回底；容器加 relative 承载。
- **工具 JSON 复制**：ToolCallCard 展开区右上角叠加 CopyButton（序列化内容与 pre 一致，运行中=参数、结束后=结果），半透明悬停显形。
- **附件大图**：用户消息附件缩略图包进按钮（cursor-zoom-in + title），点击在 Dialog 内查看原图（sr-only 标题保证 a11y），memo 组件内局部状态、随气泡卸载。

测试证据：`tsc --noEmit`、vitest 17/17、`npm run build` 通过。浏览器桩走查：注入长对话后滚动离底浮钮出现、点击回底浮钮消失（并修正走查脚本曾误选侧栏视口的问题——消息区以 aria-label 定位）；工具卡展开后复制按钮出现；注入带附件消息，缩略图点击打开大图对话框。


### 第一百六十一轮迭代：chatservice.go 拆分——按职责分文件（已实施）

依据：`chatservice.go` 单文件 2433 行，会话 CRUD、事件渲染、导出、运行引擎、统计、生命周期全部混在一起，新改动定位成本高。UX 盘点同时给出了按自然边界拆分的映射（纯机械搬移、同包内进行、零行为变更）。

实现要点：

- 拆为六个职责文件（包路径、类型、方法集全部不变，仅物理搬移）：
  - `chatservice.go`（393 行）：包头/导入、流事件与会话消息类型、`ChatService` 结构与构造、审计（audit）与杂项助手、运行生命周期（recoverRun/Cancel/IsRunning/registerRun/cancelAndWaitBounded）。
  - `chatservice_sessions.go`（671 行）：会话创建/项目/CRUD/改绑守卫/GetSessionMessages/ForkSession。
  - `chatservice_turn.go`（666 行）：Send/trimLastTurn/Regenerate/EditAndResend、sendTurn/ConfirmToolCall/runTurn/autoTitle、usage 累计。
  - `chatservice_render.go`（290 行）：renderEvent、确认中断识别、目标与 @ 路由解析、renderTodoList/displayNameOf。
  - `chatservice_export.go`（277 行）：ExportSession/ExportAllSessions/ExportSessionHTML 及 HTML 工具。
  - `chatservice_stats.go`（200 行）：RememberSession、SessionStats 类型与方法。
- 搬移脚本按顶层声明块切分（声明行向上吞并注释行），每个新文件先复制全量 import 再按编译错误逐轮裁剪；`gofmt -w` 仅限本轮涉及文件（误触的两个存量测试文件已还原——仓库本就存在未 gofmt 的历史文件，另行处理不属本轮）。

测试证据：`go build ./internal/...` 通过；`go vet ./internal/... .` 干净；`go test ./internal/... . -count=1` 全绿（services 包 8.7s，无行为变化）。`go build ./...` 下 `build/ios` 报 `function main is undeclared` 为模板遗留的 iOS 构建目录问题（iOS 无 main 文件），非本轮引入、与 internal 无关。

### 第一百六十轮迭代：无障碍补齐——live region、listbox 语义与 aria 标注（已实施）

依据：无障碍专项盘点：流式输出对屏幕阅读器完全静默（消息区无 live 语义）；@ 提及弹层无 listbox/option 语义（斜杠建议已在第 157 轮补齐）；侧栏折叠按钮无 `aria-expanded`；过滤输入框、消息输入框、消息列表无 aria 标注。

实现要点：

- **回合状态 live region**：composer 上方新增 `sr-only` 的 `role="status" aria-live="polite"` 区域，只播报阶段切换（生成中 / 本轮出错），不播报每个 token（避免读屏噪音）。
- **mention 弹层**：容器 `role="listbox"` + `aria-label="@ 助手候选"`，候选项 `role="option"` + `aria-selected`（与斜杠建议同款）。
- **标注补齐**：侧栏折叠/展开按钮 `aria-label` + `aria-expanded`；过滤输入框 `aria-label`；消息输入框 `aria-label`；消息列表 ScrollArea `aria-label="会话消息"`。

测试证据：`tsc --noEmit`、vitest 17/17、`npm run build` 通过。浏览器桩走查 DOM 断言：live region 存在、折叠按钮 `aria-expanded=true`、输入 `@写` 后 listbox/option/aria-selected 全部就位、textarea 与消息列表 aria-label 生效。

### 第一百五十九轮迭代：ConfirmDialog 化 + 按钮收敛 + 会话切换快捷键（已实施）

依据：UX 盘点三处一致性问题：删除项目/会话用原生 `window.confirm`（焦点抢占、与应用主题割裂、不可样式化）；会话头部六个图标按钮与会话行悬停六个按钮在窄窗口拥挤、危险操作（删除）与高频操作（复制）并列无层次；快捷键缺会话切换且全局 ⌘ 系处理未防按住重复触发。

实现要点：

- **ConfirmDialog**：新增 `ui/alert-dialog.tsx`（radix AlertDialog 原语，样式与 dialog.tsx 同族）与 `ConfirmDialog` 组件（受控、destructive 变体、Esc/取消语义）；`removeSession` 与 `deleteProject` 改为请求式确认（`confirmRequest` 状态 + `action` 回调），删除改为确认后执行。
- **头部收敛**：复制整段对话/导出 Markdown/导出 HTML/会话统计/存入长期记忆五项收进「更多操作」（MoreHorizontal）下拉菜单，结果区按钮保留在头部。
- **会话行收敛**：归档/置顶保留为单击切换，分叉/重命名/移动到项目（子菜单）/删除收进「…」菜单，删除项 destructive 红色；行悬停控件从 6 个减到 3 个，全部补 aria-label。
- **快捷键**：⌘⇧[ / ⌘⇧]（按 `e.code` 判定，跨键盘布局）切换上一个/下一个会话，经自定义事件转发（与 ⌘N 同款延迟派发，穿越设置视图），在 `sessions` 侧栏顺序上回绕；全局 ⌘ 系处理统一 `e.repeat` 防抖；帮助面板补 ⌘⇧[/]、↑ 编辑、Esc 链三行。

测试证据：`tsc --noEmit`、vitest 17/17、`npm run build` 通过。浏览器桩走查：派发 `wb-next-session` 会话切换生效（GetSessionMessages 重新拉取）；「更多操作」菜单渲染五项；会话行悬停仅剩 归档/置顶/会话操作 三控件；「…」菜单四项含「移动到项目」子菜单；「删除会话…」打开 ConfirmDialog（标题/描述正确），取消关闭且不执行删除；⌘ 系快捷键按住不重复触发（代码路径同 ⌘N，`e.repeat` 早退）。

### 第一百五十八轮迭代：流式渲染性能——delta 合并与搜索索引惰性化（已实施）

依据：UX 盘点确认长会话 + 高速流式的主线程压力：每个 delta token 触发一次 `setMessages`（ChatPage 整体重渲 + 消息列表全量 diff），且以 `messages` 为依赖的 `searchIndex` useMemo 对每条消息做 `JSON.stringify(toolResp/toolArgs)` 每 token 全量重算——200 条会话上每个 token 都是 O(n) 序列化。Markdown 每 token 重解析在 memo 已覆盖的前提下，主要瓶颈是 setState 频率与索引重算。

实现要点：

- **delta 合并**：流式 `delta` 事件不再直接 setState，先累积到 `streamBufRef`（按作者分桶，团队顺序流切换作者时先 flush 旧桶），定时 60ms 统一 flush 到最后一条流式气泡；任何非 delta 事件（message 终态/tool_call/done/error…）在处理前同步 flush 保证轮内顺序；首个 delta 仍立即创建占位气泡（「思考中…」不延迟）。切换会话丢弃缓冲（其完整文本由终态事件/持久化历史承载），后台会话的 delta 本就不进缓冲（会话守卫在先）。
- **searchIndex 惰性化**：索引只在过滤条打开时构建（`filterOpen` 进入依赖并在关闭时返回空 map），流式期间不再做每 token 全量序列化；`filteredMessages` 仅在 `filterQ` 非空（必然处于打开态）时消费索引，语义不变。
- 修正一处结构问题：delta 分支必须在 setMessages 更新器之外处理（更新器内嵌套 setState 会在 StrictMode 下双次执行缓冲）。

测试证据：`tsc --noEmit`、vitest 17/17、`npm run build` 通过。浏览器桩走查：向活动会话同步连发 100 个 delta（缓冲耗时 0ms，无逐 token 渲染），流式气泡仅创建一次；终态 `message` 事件先 flush 后替换，最终文本与 usage 徽标正确、流式光标消失、无重复气泡。

### 第一百五十七轮迭代：composer 手感——自动增高、斜杠键盘导航、排队不覆盖、拖拽高亮（已实施）

依据：UX 盘点的输入区四项缺口：textarea 固定 `rows={2}` 多行输入体验落后；斜杠建议条只能鼠标点击（与 @ 提及弹层的完整键盘导航不一致）；排队输入只存一条、第二条静默覆盖第一条（真实丢消息）；拖文件到 composer 无任何视觉反馈，用户不知道松手会发生什么。

实现要点：

- **自动增高**：`input`/`activeID` 变化时 `height=auto → scrollHeight`，上限 192px（与 `max-h-48` 一致）——超过后 textarea 内部滚动，消息区不再被挤占。
- **排队改数组**：`queued` 从 `Record<string, string[]>`。用户排队追加到队尾；被新忙会话弹回的排队投递插回队首（保序）；`fireQueued` 每次只出队首一条，剩余条目继续等待；横幅渲染每条队列项（各自带「取消」）；两处 truthiness 检查改为 `length > 0`（空数组为真会误触发）。
- **斜杠建议键盘化**：补 `slashIdx`/`slashDismissed` 状态，↑↓ 循环、Enter/Tab 采纳（`onMouseDown` 防失焦）、Esc 关闭且输入变化后自动重开，选中项高亮 + `role="listbox"/"option"`/`aria-selected`；与 @ 弹层互斥优先级保持。
- **拖拽高亮**：composer 拖入文件时 textarea 亮边框 + ring，`dragleave` 经子元素穿透用 `contains(relatedTarget)` 防闪烁，drop 后清除。

测试证据：`tsc --noEmit`、vitest 17/17、`npm run build` 通过。浏览器桩走查：输入 `/` 出 5 个建议，ArrowDown 选中 0→1，Enter 采纳为 `/exporthtml `；Esc 关闭后改输入自动重开；8 行文本 textarea 高 176px（<192 上限，原为固定两行）；排队数组语义由类型与调用点审查覆盖（桩无法模拟运行态）。

### 第一百五十六轮迭代：会话切换加载态 + 消息操作补全（已实施）

依据：双轴代码审查（Standards/Spec 之外的专项 UX 盘点）确认三组高频体验问题：切换会话时上一个会话的消息会一直显示到新历史加载完成（stale flash，正确性级别的「感觉不对」）；复制/朗读/token 徽标被 `isLastAssistant` 门控——翻回历史消息抄一段话是空操作；Esc 只能关 @ 弹层，停止生成、取消编辑都要去够按钮；编辑上一条提问要先滚回原消息悬停。

实现要点：

- **会话切换加载态**：加载历史的 effect 立即 `setMessages([])` 并置 `historyLoading`，异步完成后才渲染新会话内容；渲染期骨架气泡（左右交替 + 「正在加载会话历史…」），卸载/竞态时清理标志。A→B 快速切换的防串数据 guard 保留。
- **消息操作解禁**：助手消息的 复制/朗读/tokens·成本 徽标改为所有已完成消息可见（hover 显现不变），`重新生成` 仍绑定最后一条助手回复（`isLastAssistant && canRegenerate`）。
- **Esc 优先级链**：composer 内 Esc 依次 = 关 @ 弹层（原有）→ 取消编辑（清输入 + 收横幅）→ 停止生成（`Chat.Cancel`）→ 关会话内过滤条；全被占用时不动。
- **↑ 编辑上一条**：composer 为空时按 ↑ 打开最后一条用户消息的编辑（复用 `startEdit`，沿用运行中禁用与编辑横幅）；向上扫描遇到确认卡即止（编辑会错误表示挂起的 HITL 交互），IME 组合中与修饰键组合不触发。

测试证据：`tsc --noEmit`、vitest 17/17、`npm run build` 通过。浏览器桩走查：注入多条助手消息后「较早」气泡的 DOM 中均有 复制/朗读 按钮（原先仅最后一条）；↑ 在空 composer 上触发编辑（textarea 填入最后一条用户消息、「正在编辑最后一条消息…」横幅出现、keydown 被 preventDefault）；Esc 取消编辑（输入清空、横幅消失）；⌘F 打开的过滤条由 Esc 关闭为同一链路的第三级（代码路径同构）。

### 第一百五十五轮迭代：任务变更行级 diff——Codex 式代码审查视图（已实施）

依据：对标 Codex 桌面端的变更审查体验，任务结果区「变更」标签此前只有文件级 新增/修改/删除 徽标，看不到改了哪些行——这也是 README 已知边界里明确记录的最大能力缺口。行级 diff 的前提是「改之前的内容」，而任务基线（`task-snapshots/<sessionID>.json`）只存 size+mtime，因此在基线侧引入内容快照是本轮的架构前提。

实现要点：

- **基线内容快照**：`taskBaselineFile` 新增 `Content` 字段（内容文件名，`omitempty`），快照目录 `task-snapshots/content/<base(sessionID)>/`，文件名为 `sha256(相对路径)`。建立基线时对每个扫描到的普通文件尽力快照：UTF-8 文本（NUL 嗅探 + utf8.Valid，与 diff 时同款 `looksTextual` 判定）、≤256KB、每会话 2000 个文件 / 32MB 预算上限，读失败静默跳过。`taskSnapshotVersion` 1→2——旧 v1 基线按既有「陈旧即重建」路径升级，升级后首个基线自带内容；重建/删除基线同步清空内容目录（`createTaskBaselineLocked` 统一负责）。
- **有界 diff 引擎**（`internal/services/filediff.go`，无第三方依赖）：`lineDiff` 先裁剪公共前缀/后缀；中段 ≤3000 行走 LCS 动态规划（uint16 表 ≤18MB），否则进入分块路径——200 行一块按块内容 LCS 对齐，失配区递归下钻（深度 3 兜底整段替换），保证大文件局部修改仍产生局部 hunk 而非整段替换。`groupHunks` 生成统一 diff hunk（3 行上下文、相邻变更合并），渲染行数 12000 封顶（超出截断标记）。
- **FileDiff RPC**（ArtifactService）：解析会话有效工作区（项目会话贯通）→ `SafeWorkspacePath` 防穿越 → 新增=空侧对比、删除=基线内容对比、修改=双侧对比、与基线一致=空 hunk；二进制/超 2MB/基线未存内容（建基线时为二进制、空文件或超快照上限）返回带 `Unsupported` 原因的结果而非错误，前端内联展示。状态字段与 `buildTaskSnapshot` 同口径，未变更文件状态为空。
- **前端**：变更标签的每行改为 `ChangeRow`——行本体保留预览能力，尾部新增「Diff」开关（aria-expanded），展开内嵌 `DiffView`：hunk 头吸顶、双列行号、加行绿/删行红、底部 ±行数与截断提示，长 diff 内部滚动（max-h-80）。删除的文件现在也能打开（查看被删除的内容），不可预览文件有了新的检视路径。`api.ts` 导出 FileDiff 类型族。
- **视觉桩修复（两个潜伏 bug 互相掩护）**：TaskWorkspaceSnapshot 的桩方法 ID（57605569）自绑定再生成起就不对应任何现役方法（AGENTS「手工计算仅应急」的又一例证），面板一直在错误态，而近几轮走查未触达该面板故未暴露；修正为生成产物标注的 2781372891 后，又暴露第 153 轮引入的 `structuredClone` 深拷贝无法克隆 async 处理器的 Promise——transport 先 `await` 再克隆。桩新增 `FileDiff`（1773899386）夹具，与种子变更列表一一对应（新增/修改/删除各一）。

测试证据：新增 `filediff_test.go` 八组用例——行拆分边界（尾换行/CRLF/空串）、九场景表驱动（相同/插入/删除/追加/前插/空侧/远距双 hunk，断言 hunk 数、±行数、上下文行数与行号-文本对齐不变量）、8000 行文件两处孤立修改经分块路径产出两个局部 hunk（非 4000 行整段替换，回归钉住「分块必须递归下钻」）、渲染上限截断、FileDiff 增删改未变四态端到端（含删除内容逐行还原）、四种 Unsupported（基线未存/二进制/删除详情缺失/超上限）、路径穿越与未知会话拒绝、基线内容快照-重建-清理生命周期。调试中修复分块递归 depth 被 `middleDiff` 重置导致的无限递归（168s 挂死→0.01s）。`go test ./internal/... .` 全绿、`go vet` 干净；绑定再生成后 `tsc --noEmit`、vitest 17/17、生产构建通过；`wails3 build` 产出二进制。浏览器桩走查：变更列表三行 Diff 按钮（aria-expanded 正确）、修改文件 hunk 头 `@@ -3,7 +3,9 @@` 与红绿行、删除文件纯删除行、新增文件纯新增行，截图确认配色与行号布局。顺带清理：`-clean` 再生成删除了无引用的旧版 CLI 遗留 `frontend/bindings/events/index.ts`。

### 第一百五十四轮迭代：macOS 测试可移植性——临时目录符号链接归一化（已实施）

依据：代码库从 Windows 迁到 macOS 开发后，`go test ./internal/services` 有 3 个用例失败：`TestNewSessionWorkspaceIsolatesParallelWrites`、`TestExplicitWorkspaceValidationAndConflict`、`TestSetSessionWorkspaceBeforeFirstTurnOnly`。根因是平台差异而非产品缺陷——macOS 的 `$TMPDIR`（`/var/folders/...`）本身是指向 `/private/var/folders/...` 的符号链接，生产代码 `validateTaskWorkspace` 按设计做 `filepath.EvalSymlinks` 归一化后存储工作区路径，而测试断言仍拿未解析的 `t.TempDir()` 原始路径做字符串比较，Windows 上没有这类符号链接所以从未暴露。

实现要点：

- `workspace_test.go` 新增 `canonicalDir` 辅助函数（与生产同款 `EvalSymlinks + Clean` 归一化，注释说明 macOS TMPDIR 符号链接背景）。
- `newWorkspaceTestChat` 夹具在 MkdirAll 后立即归一化 `workspace`——返回的 `globalWorkspace` 与配置里存的 `Settings.WorkspaceDir` 全部变为解析后路径，自动任务目录前缀断言随之成立。
- 两处需要「存储路径 = 传入路径」相等断言的 `chosen := t.TempDir()`（显式工作区校验、首轮改绑锁）改为 `canonicalDir(t, t.TempDir())`，语义不变：仍然验证传入目录经归一化后原样落库；仅断言错误文案的第三处（660 行附近）保持原样。

测试证据：`go test ./internal/... .` 全绿（services 包 8.7s，3 个失败用例转通过，无新失败）；`go vet` 干净。产品行为零改动——`samePath` 的 `os.SameFile` 与 `validateTaskWorkspace` 归一化本就是平台无关的，本轮只修测试。

### 第一百五十三轮迭代：侧栏重排——新聊天、工作目录自动归类与最近列表（已实施）

依据：用户附 ChatGPT 桌面端侧栏参考截图，要求左侧菜单改为「新聊天 + 项目（可折叠清单）+ 最近」的结构，而非原先「对话标题 + 新对话分裂按钮 + 时间/项目分组切换」的工具栏式布局。第一版仅列出手动注册的项目，用户实测（7 会话、0 注册项目、1 个显式绑定 `D:\projects\go-workbuddy`）后指出「对话的工作目录没有分类」——文件夹必须从会话工作区**自动推导**，而不是只认手动注册的项目。

实现要点：

- **侧栏重排**：顶部改为整宽「新聊天」按钮（SquarePen 图标，secondary 变体）+ 折叠按钮 + 搜索框；「项目」区（标题可折叠，右侧常驻「添加项目」）按工作目录自动归类：注册项目优先，其后为未注册的显式工作区目录（以目录 basename 显示、行 title 带完整路径），其「…」菜单提供「注册为项目」一键转正（`SaveProject("", "", dir)`，转正后自动获得完整管理菜单）；无对话的注册项目也保留空文件夹。点击文件夹手风琴展开其会话（左侧竖线缩进），注册项目行悬停提供「+ 新建对话」「重命名」与「…」菜单（新建 / 重命名 / 更改目录 / 删除项目保留对话与文件）；「最近」区（可折叠）沿用时间分组（置顶 / 今天 / 昨天 / 7 天内 / 更早），已归档尾组与展开入口不变。原「在指定项目中新建对话」下拉由项目行「+」承接，「添加新项目」移入项目区标题。
- **归类规则**：归入文件夹的对话从「最近」移出（与参考应用一致，避免重复）；自动任务目录（全局工作区下）与未绑定目录的对话留在「最近」。切换会话时自动展开其所属文件夹（经 ref 只在会话切换时触发，避免无关刷新重置折叠态）。
- **输入区上下文标签（Codex 式）**：用户新建对话时希望像 Codex 一样在输入框上方直接看到并切换工作上下文。首轮未开始的会话在 composer 上方渲染「📁 项目/目录 ▾」标签：注册项目与显式目录以名称显示（全局工作区下的自动任务目录归为「默认项目」），下拉可切默认项目 / 各注册项目（`SetSessionProject`）/ 选择其他目录（`SetSessionWorkspace`，目录冲突时报错文案已带解法）/ 添加新项目；首轮开始后标签隐藏（顶栏保留锁定的工作区路径与项目徽章）。
- **行为微调**：会话行抽取为 `renderSessionRow`（文件夹手风琴、最近、搜索三处复用，悬停操作完全一致）；有搜索词时整栏切换为扁平「搜索结果（N）」列表（不走分组，含文件夹内对话）；移除「时间/项目」分组切换与 `sidebarGrouping` 前端读取（Go 配置字段保留以兼容旧配置）。
- **占用报错自解释**：用户实测撞上「工作区已被任务使用」改绑报错后不知如何解除。`workspaceConflict` / `workspaceConflictForUnarchive` 的文案改为附带解法（归档或删除占用任务即可释放目录），桩同步；`workspaceConflict` 本就跳过已归档会话，归档即释放占用，无需改行为。

测试证据：`npx tsc --noEmit`、`npm run build` 通过，vitest 17/17（分组纯函数与用例未动）；浏览器桩走查截图与 DOM 断言：注册项目（含空项目）、目录 basename 文件夹、手风琴展开显示各自会话与悬停操作、文件夹内对话从「最近」消失、临时目录菜单含「注册为项目」、搜索「proj」得扁平「搜索结果（1）」、「最近」折叠后列表隐藏再展开恢复、归档入口仍在。桩新增「临时目录：未注册工作区」夹具覆盖自动归类路径。走查补遗：桩缺 Archive/Pin/DeleteSession 处理器且 transport 直接返回种子数据的共享引用——原地变更 + 同引用 setState 使 React 判定无变化，造成「归档后行状态变了但列表不重排」的桩侧假象；已补齐三个处理器并让 transport 对返回值 `structuredClone` 模拟真实 IPC 序列化，随后完整实测「归档 → 最近消失 → 归档列表 +1 → 归档行删除（confirm）→ 彻底消失」全链路。真实应用经 Wails IPC 每次返回反序列化副本，不受该桩缺陷影响。报错文案变更后 `go test ./internal/services` 全绿、`go vet` 干净。上下文标签实测：新聊天后标签显示「默认项目」，下拉含全部菜单项，选 go-workbuddy 后标签、顶栏项目徽章与工作区路径（D:/projects/go-workbuddy）三处联动，侧栏归入该项目文件夹；打开已有消息的会话标签隐藏。

### 第一百五十二轮迭代：会话项目分类——项目实体、新建选项目与项目管理（已实施）

依据：用户指出对话框缺少项目分类，选择目录应有「添加」入口，且新对话时应可选择项目（附参考截图：侧栏项目清单与输入区项目徽标）。诊断确认这是功能缺口而非运行时缺陷：代码中不存在项目实体，侧栏「项目分组」只是前端按会话工作区根目录的隐式推导（`groupSessionsByProject`），而唯一建会话入口写死 `NewSessionWithWorkspace("","","","")`，所有会话必然落入自动任务目录与「默认项目」；选目录仅存在于「首轮前改绑任务工作区」一条路径。关键约束：`workspaceConflict` 规定一个显式目录只能绑定一个活动会话，因此「多会话共享一个项目目录」无法靠把目录塞进每个会话的 `WorkspaceDir` 实现，必须引入独立的项目实体。

实现要点：

- **项目实体**：`config.Project {ID, Name, RootDir, CreatedAt}` 挂到 `Config.Projects`，`ChatSession` 新增 `ProjectID`（`Clone` 深拷贝，导出/导入直接携带，无敏感字段）。会话三态：`ProjectID` 非空 = 项目会话（共享项目根目录、不占任务目录）；仅 `WorkspaceDir` = 独立任务目录（现状）；两者皆空 = 自动任务目录（现状）。旧数据零迁移。
- **工作区解析**：`ChatSession.EffectiveWorkspaceFor(cfg)` 取代四处调用点（runTurn、ForkSession 基线、`SessionWorkspace` RPC、ArtifactService 预览），优先级 显式任务目录 > 项目根目录 > 全局工作区；项目会话 `WorkspaceDir` 保持为空，项目改名/换根即时传播。
- **项目 RPC（ChatService）**：`ListProjects` / `SaveProject`（创建默认名取目录末段；`validateTaskWorkspace` 校验可写；`samePath` 查重）/ `DeleteProject`（运行中会话拒绝删除；其会话回落默认项目并把任务基线重置到新生效目录，避免快照把项目文件误报为批量删除）/ `NewSessionWithProject`（不创建 `tasks/<id>`、基线建在项目根）/ `SetSessionProject`（首轮前限定，取会话级工作区锁，加入项目即清空任务目录绑定并重置基线；基线失败回滚）。`ForkSession` 复制 `ProjectID`，分支保持项目会话。
- **目录占用互斥**：`projectRootConflict` 拒绝把项目根目录绑定为显式任务目录（创建与改绑两处），提示「请在项目中新建对话」；项目会话本身不允许再单独指定工作区。
- **前端**：`groupSessionsByProject(sessions, projects, globalWorkspace)` 改为真实项目分组（`proj:<id>` 键，projectId 或工作区根匹配皆入组；未注册目录保留临时文件夹分组；默认项目与已归档尾组不变）。新对话按钮改为分裂式：主按钮保持默认直聊，下拉可选「默认对话 / 各项目 / 添加新项目…」；侧栏标题行新增「添加项目」（目录选择器）；项目组头部提供「+ 新建对话」与「…」菜单（重命名 / 更改目录 / 删除项目保留对话与文件）；会话头部新增项目徽标（首轮前点击可切换项目，首轮后锁定并提示）；会话行悬停新增「移动到项目」（首轮前生效）。项目重命名使用独立对话框。
- **视觉桩**：种子数据补 `projects` 与绑定项目会话；新 RPC 按生成绑定的方法 ID（ListProjects 2203540805 / SaveProject 2762009531 / DeleteProject 911459121 / NewSessionWithProject 3614387014 / SetSessionProject 3134520676）补桩，`SessionWorkspace` 桩支持项目根解析。

测试证据：红→绿——先写 `internal/services/chatservice_project_test.go` 六个用例（SaveProject 校验与查重、NewSessionWithProject 不建任务目录且工作区解析到项目根、SetSessionProject 首轮锁与工作区移交、DeleteProject 回落与基线重置、项目根拒作任务目录、Fork 保留项目）与 `TestProjectsRoundTripAndCloneIndependent`，红（编译失败）后实现转绿；`go test ./internal/... .` 全绿、`go vet` 干净；`go test -race` 本机无 C 工具链（CGO）无法执行，由 CI ubuntu 任务覆盖；vitest 17/17（分组纯函数用例重写为项目感知）、`tsc --noEmit`、`npm run build` 通过；`wails3 build`（含绑定 `-clean` 再生成）端到端成功产出 `bin/go-workbuddy.exe`。生成绑定确认方法 ID 以生成产物为准（手算 FNV 与生成器不一致，印证 AGENTS「手工计算仅应急」的告诫）。

### 第一百五十一轮迭代：模块重命名 changeme → go-workbuddy（已实施）

依据：用户指出 `go.mod` 模块名仍是 Wails 模板默认的 `changeme`，与项目名 go-workbuddy 不符，要求整体审核并执行重命名。审计确认模块名贯穿 go.mod、47 个 Go 文件 import、前端生成绑定目录、`api.ts` 手写 import、视觉桩硬编码方法 ID、`.gitignore`/`AGENTS.md`/`README.md`；另发现第二处模板遗留 `go-wails-react-tpl`（Taskfile `APP_NAME` 与 macOS/iOS/Windows/Linux 打包资产）。

实现要点：

- `go.mod` 模块名改为 `go-workbuddy`，47 个 Go 文件 import 同步替换（含 `debug/mcp_probe`）。
- 重新生成绑定：产物目录由 `frontend/bindings/changeme/` 变为 `frontend/bindings/go-workbuddy/`，全部方法 ID 随模块路径变化；`api.ts` 五处 import 同步。
- 视觉桩 `frontend/stub/main.tsx` 的 17 个硬编码方法 ID 全部换算为新 ID；顺带修复陈旧的 AppInfo 键（旧值 2350821288 不对应任何现存方法，属潜伏死代码，现已指向真实的 `appservice.AppInfo`）。
- 方法 ID 公式确认为 FNV-1a32(`包路径.类型名.方法名`)（wails v3.0.0 `internal/hash/fnv.go` + `internal/generator/collect/service.go`），`AGENTS.md` 同步模块名。
- Taskfile `APP_NAME` 改为 `go-workbuddy`；macOS/iOS plist、Windows NSIS/MSIX、Linux desktop/nfpm 打包资产中的模板名全部替换；`docs/iteration-log.md` 历史轮次记录保留原名不动。

测试证据：rg 全仓 `changeme` 零残留（含生成绑定）；`go vet` + `go test ./internal/... .` 全绿；vitest 16/16、`tsc --noEmit`、`npm run build` 通过；`wails3 build` 端到端成功并产出 `bin/go-workbuddy.exe`。`go test -race` 本机无 C 工具链（CGO）无法执行，由 CI ubuntu 任务覆盖。

### 第一百五十轮迭代：对话优先外壳与设置中心（已实施）

依据：用户提出对齐 ChatGPT / Claude / Codex 桌面端的导航模型——最左列直接是对话列表、对话按目录分组、全部配置收进设置。设计固化为 `docs/specs/chat-first-shell.md` 并拆为六张票据（设置中心骨架、对话优先外壳、侧栏折叠、时间分组、项目分组、快捷入口）全部实施。关键设计澄清：目录分组的键是工作区根而非会话任务目录——每个会话都自动创建独立任务目录，逐目录分组会碎片化。

实现要点：

- **设置中心**：设置页重构为「左标签 + 右内容」九标签（通用 / 模型供应商 / Agents / Agent 团队 / Skills / MCP 服务 / 知识库 / 数据与存储 / 关于），五个管理页组件化迁入对应标签；⌘, 打开、Esc 与返回按钮回对话；关于标签显示版本号。
- **对话优先外壳**：删除主导航栏，视图收敛为「对话 / 设置」；主题切换、帮助、设置入口迁入对话列底部；空状态与 Onboarding 直达设置对应标签；⌘N / ⌘K / ⌘F 语义不变。
- **对话列表分组**：新增 `groupSessionsByTime`（置顶 → 今天 → 昨天 → 7 天内 → 更早 → 已归档，归档默认折叠可展开）与 `groupSessionsByProject`（显式工作区按目录成组并保留原路径大小写、自动任务目录与直聊归「默认项目」、组间按最新成员排序）两个纯函数；列表按组渲染，搜索过滤与分组正交；项目组悬停显示完整路径。
- **折叠与偏好持久化**：设置新增 `sidebarGrouping`（time / project，默认 time，非法值回退）与 `sidebarCollapsed`（默认 false）；⌘B 或按钮折叠 / 展开对话列，折叠后头部出现展开按钮。
- **快捷入口**：@ 候选浮层底部「管理助手与团队…」、模型选择器下拉底部「管理模型供应商…」、会话头部徽标点击直达设置对应标签（直聊徽标保持纯展示）；帮助面板补 ⌘, 与 ⌘B。
- **视觉桩**：新增跨天 / 置顶 / 归档 / 独立工作区会话夹具，日期相对加载日计算，保证分组桶始终有成员。

测试证据：分组纯函数 vitest 表驱动四用例（固定「当前时间」断言桶序与成员、7 天临界、无效时间回落更早、大小写与反斜杠路径归并、组间最新排序）；`internal/config` Go 测试覆盖新字段默认值、持久化与非法分组回退；浏览器桩走查两列外壳、时间 / 项目分组（截图）、归档折叠、侧栏折叠 / 展开三态下长对话 composer 几何（708 ≤ 720，消息区可滚）、设置九标签与管理页嵌入、@ 浮层入口截图与模型选择器入口 DOM 断言；走查中发现并修复「分组切换仅持久化未更新 UI 状态」缺陷。工作区另有并行会话进行中的 MCPPage / config / kit 改动（第一百四十九轮），未纳入本轮提交。

### 第一百四十九轮迭代：MCP 启用开关——默认开启、随启动自连并注入所有会话（已实施）

依据：用户反馈直聊里让助手用 Tavily 查天气，助手回答「当前会话尚未加载 Tavily MCP，无法热加载」（附截图）；并提出 MCP 应「只有开启和关闭」——默认在软件打开时就开启并注入会话，开关控件改用 Switch。代码定位三处断点：`directAgentConfig` 构建的直聊临时助手不带任何 `MCPServerIDs`，直聊会话零 MCP；`MCPServerConfig.AutoStart` 字段自创建起无后端读取、无 UI 控件，纯死配置，启动时也不连接；MCP 页只有「连接/断开」按钮，没有启用开关。

实现要点：

- **语义重定义**：以 `Disabled bool`（`disabled,omitempty`）替代死字段 `AutoStart`——省略即启用，存量配置零迁移自动变为「默认开启」，正好符合用户预期；启用开关是权威控制。
- **注入所有会话**：`kit.buildLLMAgent` 的 MCP 挂载改为「启用的服务全量注入（直聊/Agent/团队统一生效）∪ Agent 显式挂载的补充（可单独启用某停用服务）」；显式挂载已停用服务仍生效（逃逸口），悬空 ID 依旧报错，启用但损坏的服务仍走「报错不静默降级」契约。直聊经 `BuildAgentWithModelAndWorkspace` 走同一装配，无需改 `directAgentConfig`。
- **启动自连**：`main.go` 增加后台 goroutine 顺序预连接所有启用服务并预热工具列表，失败只记日志不阻塞启动；Manager 锁内建连，与页面「连接」共用同一状态缓存。
- **MCP 页 Switch 化**：卡片操作区以「启用」Switch 为主控（开启即保存并立即连接，关闭即保存并自动断开），移除「断开」按钮，「连接」保留为启用状态下的手动重试；停用服务显示「已停用」徽标；编辑对话框顶部新增启用开关（说明：随应用启动自动连接并注入所有会话）；新增服务默认启用。

测试证据：新增 `TestBuildAgentInjectsEnabledMCPServers`（修复前红：启用但损坏的 MCP 不注入、Build 不报错；修复后启用→报错注入、停用→构建通过、显式挂载停用服务→按需启用）并回归 `TestBuildAgentReportsMCPFailures` 保持通过；`go test ./internal/...`、`go vet ./internal/... .` 全绿；绑定再生成（`autoStart`→`disabled`）后 `tsc --noEmit`、vitest 16 用例、生产构建通过。`-race` 因本机缺 gcc 无法运行（环境限制）；启动自连 goroutine 只读 `Store.Get()` 快照且 Manager 自带锁。

### 第一百四十八轮迭代：MCP stdio 命令尾随空白导致连接失败修复（已实施）

依据：用户反馈 MCP 页添加 npx 型 stdio 服务（`npx -y tavily-mcp`）点连接即报 `failed to list MCP tools: failed to init MCP session: exec: "npx ": executable file not found in %PATH%`，而同机其他 Agent 客户端配置同一服务正常。错误引号内的尾随空格暴露根因：命令输入框的值原样入库（前端仅对「参数」逐行 trim，command、后端 `SaveMCPServer` 与 `buildTransport` 全链路无规范化），`exec.Command("npx ")` 的 `LookPath("npx ")` 永远匹配不到 `npx.cmd`——与 PATH 无关，其他客户端的 JSON 配置不引入尾随空白故不受影响。

实现要点：

- **运行时兜底**：`buildTransport` stdio 分支对 Command `TrimSpace`、Args 逐项 trim 并丢弃空白项；streamable-http 的 URL 同步 trim——已入库的脏配置（如现存 `"npx "`）无需重新保存即可恢复连接。
- **保存侧止血**：`SaveMCPServer` 落库前规范化 Name / Command / URL / Args，UI 保存与配置导入两条路径统一；规范化在 changed 判定之前执行，脏配置改存后能正确触发旧连接失效。
- **空 Args 保持 nil**：`Store.Get()` 深拷贝会把空切片还原为 nil，若规范化产出空非 nil 切片，JSON 比较（`[]` vs `null`）每次都判 changed，未变更保存也会误断缓存连接——规范化后空集回落 nil，与新测试共同钉住该语义。前端无需改动。

测试证据：新增 `TestBuildTransportTrimsStdioCommand`（修复前红，报错 `exec: "cmd ": executable file not found in %PATH%` 与用户症状同形；修复后命令解析成功且 Args 保持 `["-y","tavily-mcp"]`）与 `TestSaveMCPServerNormalizesWhitespace`（落库值全部去空白）；回归中抓出空 Args 误判 changed 问题并以 nil 语义修复，`TestSaveMCPServerInvalidatesStaleConnection` 保持通过；`go test ./internal/...` 与 `go vet ./internal/... .` 全绿。

### 第一百四十七轮迭代：聊天页垂直溢出修复——输入框被顶出窗口（已实施）

依据：用户反馈直聊会话「UI 有很大的问题」并附 maximized 截图：长回复底部发送按钮被窗口边缘裁切；实机复现（814px 高窗口）更严重——两轮长对话后整个 composer 被推出窗口外完全不可见，消息区也无法滚动，历史消息低于折叠线的部分永远读不到。桩页几何测量定位根因：shadcn `ScrollArea` Root 自基线起缺失默认的 `overflow-hidden`，在 `h-screen` 列布局中作为 `flex-1` 子项时自动最小尺寸（`min-height: auto`）等于内容高度，flexbox 无法把它收缩到可用空间以内——实测消息区 Root 高 921px（视口仅 720px）、`scrollHeight == clientHeight`（自身不滚），整列被内容撑高后由 App 根的 `overflow-hidden` 裁掉底部。

实现要点：

- **`scroll-area.tsx` Root 恢复 `overflow-hidden`**（shadcn 默认值）：非 visible 的 overflow 使 flex 子项自动最小尺寸归零，`flex-1` 重新生效——消息区收缩到剩余空间、radix Viewport 正确接管内部滚动；会话列表与任务结果区的 ScrollArea 同步受益。
- **composer 防挤占**：输入区容器加 `shrink-0`，textarea 加 `max-h-48 overflow-y-auto`——长文本粘贴时输入框封顶 12rem 并内部滚动，消息区让位而不是把 composer 顶出窗口。
- **视觉桩补直聊场景**：stub 种子新增空目标直聊会话（长 Markdown 回复，与用户实况一致），作为本轮回归的走查夹具。

测试证据：桩页（720px 视口）修复前后对比——修复前消息区 Root `overflow: visible`、高 921px、composer 顶部 748px（窗口外）；修复后 Root `overflow: hidden`、高 578px、composer 底部 708px（窗口内），Viewport `scrollHeight 921 > clientHeight 578` 可滚且能滚回顶部看到日期分隔与首条消息；2100 字符粘贴 textarea 稳定在 192px 并内部滚动。新增 `scroll-area.test.tsx` SSR 断言 Root 必须携带 `overflow-hidden`（防止再次回归）；`vitest` 12 用例、`tsc --noEmit`、生产构建全部通过。

### 第一百四十六轮迭代：直聊会话与 @ 助手路由，滚动条与设置页修复（已实施）

依据：用户反馈对齐 Codex / Claude Code / WorkBuddy 的对话模型——对话不应要求先创建 Agent，助手应通过 `@` 按需唤起；同时指出设置页布局失衡与全站滚动条观感问题。代码审查确认三处根因：新对话对话框在无 Agent 时死锁（必须先建 Agent）；全站无 `::-webkit-scrollbar` 样式，WebView2 深色主题渲染 15px 亮色原生滚动条且与聊天区 radix ScrollArea 风格割裂；设置页为唯一未居中的 `max-w-xl` 窄列（实测 1217px 视口中右侧空置 640px）。另发现既有缺陷：非 ASCII Agent 名经 `sanitizeAgentName` 变为 `agent_<hash>`，聊天气泡与流事件直接显示该哈希。

实现要点：

- **直聊会话**：`NewSessionWithWorkspace` 允许空目标（仅 `""`，非法类型仍拒绝）；`runTurn` 新增直聊分支——不落库地构造「默认供应商默认模型 + 通用提示词」的临时 Agent，会话级模型覆盖与工作区机制照常生效。无供应商时发送报错引导至模型配置页。
- **@ 消息级路由**：`resolveMention` 解析消息开头 `@名称`（Agent/团队/远程，最长名匹配，须跟随空格边界），`turnTargetFor` 仅将该轮构建目标替换为被 @ 者，会话绑定不变；@ 提及保留在消息原文中，Regenerate / EditAndResend / HITL 恢复（从最后一条用户消息回溯解析）都按同一规则重放路由；未知名回落直聊。
- **作者名反查**：新增 `displayAuthor` 将 ADK 事件作者（净化后标识符）映射回配置显示名，应用于流事件与历史渲染（导出经同一渲染受益），中文 Agent 名在气泡中正确显示。
- **前端直聊流**：「新对话」按钮与 ⌘N 直接创建空目标会话（删除旧选择对话框）；无会话时输入框可用，首条消息惰性建会话（`freshInput` 草稿）；composer 输入 `@` 弹出助手/团队/远程候选浮层（键盘上下选择、Enter/Tab 采纳、Esc 关闭）；头部徽标「直聊」、模型选择器列默认供应商全部模型、空态与 Onboarding 改为「配供应商 → 直接开聊（@ 指定助手）」两步。
- **滚动条统一**：全局 10px 细滚动条（thumb 用前景色 25% 混合、圆角、hover 提亮、track 透明）+ `scrollbar-width: thin`，设置/Agents/团队/Skills/MCP/模型页、弹窗与代码块与聊天区视觉一致。
- **设置页布局**：`max-w-5xl` 居中 + `lg:grid-cols-2` 两列卡片网格（窄窗口回落单列），保存按钮改为吸底模糊操作栏（跨双列）。
- **顺带修复**：`Input`/`Textarea` 改 `forwardRef`，修复 ChatPage 传 ref 无效导致的 ⌘K 聚焦失效（React "Function components cannot be given refs" 告警消失）。

测试证据：后端新增 `chatservice_direct_test.go` 四用例（直聊会话目标不变且作者为「助手」、@路由至具名 Agent 且 Regenerate 保持路由、未知名回落直聊、无供应商报错引导），`go test ./internal/...` 与 `go vet ./internal/... .` 全绿；前端 `tsc --noEmit`、vitest 11 用例、生产构建通过；浏览器桩（1440×900）走查确认直聊会话创建、直聊徽标与默认模型选择器、`@写` 弹出候选且 Enter 采纳为 `@写手助手 `、设置页两列居中布局与吸底保存、细滚动条生效。

### 第一百四十五轮迭代：Wails v3 正式版与 Windows 启动告警修复（已实施）

依据：Wails v3.0.0 正式发布后统一升级 Go SDK、CLI 与前端运行时；Windows `wails3 dev` 启动仍出现 `uname` / `tail` 缺失提示，定位为根 Taskfile 无条件加载 Android 全局 shell 变量所致，而非 Windows 构建需要这些工具。

实现要点：

- Wails Go SDK、`@wailsio/runtime` 与本机 CLI 统一为 `v3.0.0`；README 安装命令同步更新，绑定重新生成。
- Android `HOST_ARCH` / `ANDROID_ABI` 改用 Task 内置 `ARCH`，消除 include 阶段的 `uname` 求值；`avdmanager` 查找移入模拟器启动脚本，仅在实际需要时执行 `tail`。

测试证据：`wails3 task --list-all` 无 Unix 工具缺失警告；Android `arm64` / `amd64` dry-run 保留正确 ABI 与交叉编译参数；`wails3 dev` 实测完成 Go 编译、Vite 启动、WebView2 创建和前端连接，启动日志无 `uname` / `tail` 警告。升级后 `go test ./internal/...`、`go vet`、前端 `tsc`、vitest 11 用例、生产构建及 `wails3 build` 均通过。未实际构建或运行 Android（本机无 Android SDK）。

### 第一百四十四轮迭代：任务级工作区与产物验收工作台（已实施）

依据：MCP 调研 WorkBuddy 官方「结果查看 / 快速开始」后确认其核心闭环是任务级工作目录 + 右侧产物验收；结合本会话规格 issue #10 与垂直切片 #11–#15 实施（GitHub issue 编号已作废：原文与结论存档于 `docs/issues-archive/`，编号对照见该目录 README）。

实现要点：

- **任务级工作区**：会话持久化 `WorkspaceDir`；新对话默认创建独立 `<全局工作区>/tasks/<sessionID>`，也可通过原生目录选择器绑定已有目录。首轮前可改绑、首轮后锁定；目录需可读写且不能被其他未归档任务重复占用。旧会话继续回落全局工作区。
- **运行时统一**：单 Agent、团队、文件工具、`{{workspace}}` 提示词变量和工具卡预览统一使用会话工作区；知识库、Skills 与导出保持应用级目录。工具卡中的 `search_knowledge` 引用继续走全局预览。
- **右侧结果区**：聊天页新增可收起的任务结果面板，提供「全部文件 / 产物 / 变更」；支持路径搜索、图片/PDF/Markdown/文本预览、未知二进制提示、空/错/截断状态、手动刷新和会话切换防串数据。桌面为第三栏，窄屏为覆盖式面板。
- **产物与变更**：任务创建时原子写入版本化快照基线；结果区计算新增、修改、删除及计数，旧会话首次查询懒建基线，重启后继续累计。HITL 批准写入、工具结果、回合完成与外部 MCP 文件变化都可经重新扫描发现。
- **安全与边界**：扫描只统计普通文件，拒绝目录穿越与符号链接逃逸，跳过隐藏文件、版本库元数据、依赖缓存和虚拟环境；5000 文件扫描上限、200 条展示上限。删除会话清理基线但保留文件；归档不影响结果访问；分叉共享有效工作区但建立独立基线。
- **双轴审查修复**：公开 `NewSession` 也默认创建任务目录；发送与改绑用会话级锁线性化；显式目录强制绝对路径；前端工作区统一经后端 `SessionWorkspace` 解析；快速切会话不再残留旧快照；补齐变更类型筛选、ARIA 标签键盘导航、普通文件判定、错误 toast 与浏览器桩空/错/截断/慢响应状态。
- **复审补强**：归档目录被新任务复用后，解除归档会重新校验占用（同分叉谱系豁免）；目录同一性用 `os.SameFile` 覆盖 macOS 大小写变体；“全部”及标签页变更数使用截断前总数；无扩展名 UTF-8 文件恢复文本预览；ARIA 仅对现存面板建立 `aria-controls`；浏览器桩覆盖目录选择、取消、冲突与失败；新增并行写入与并行快照的 race 场景。

测试证据：后端新增任务隔离、目录校验、首轮锁定、发送/改绑竞态、快照状态/截断、旧会话懒基线、HITL 产物、分叉/删除/归档与并发快照测试；`go test ./internal/... .`、`go vet` 与临时 MinGW 下 `CGO_ENABLED=1 go test -race ./internal/... .` 全绿；前端 `tsc`、vitest 11 用例与生产构建通过；浏览器桩走查确认任务路径、文件搜索/预览、产物/变更计数与筛选、键盘标签、空/错/截断/慢响应与切会话防串数据；`wails3 build` 生产构建成功。

### 第一百四十三轮迭代：代码审查修复批次（已实施）

依据：对本仓库后端核心包与前端聊天主链路的全面代码审查（Windows 实测复现 5 个测试失败 + OAuth 授权截断），产出规格 issue #1 与票据 #2–#9，本批次逐票实施（GitHub issue 编号已作废：原文与结论存档于 `docs/issues-archive/`，编号对照见该目录 README）。

实现要点：

- **票 #2（Windows OAuth）**：浏览器启动命令提取为纯函数；Windows 由 `cmd /c start` 改为 rundll32 FileProtocolHandler——cmd 会把授权 URL 中第一个 `&` 当命令分隔符，授权流程必然失败。
- **票 #3（全局代理）**：空配置由禁用代理改为委托 `ProxyFromEnvironment`；已配置代理时 127.0.0.1 / localhost / ::1 直连（本地 Ollama / 本地 MCP / A2A 回环流量不再进代理）。
- **票 #4（测试可移植性）**：测试内 gorm / 会话库 / 知识索引 / 旋转日志句柄统一 Cleanup 关闭；POSIX 权限断言在 Windows 跳过。`go test ./internal/... .` 在 Windows 由 5 失败转全绿。
- **票 #5（会话一致性）**：运行句柄携带完成信号，Regenerate / EditAndResend / DeleteSession「取消并等待（10s 兜底）」；顺带修复存量 bug——ADK 会话 Delete 只删 sessions 行，DeleteSession 现显式清理事件孤儿行（可被全文搜索永久命中）。
- **票 #6（检索一致性）**：knowledge 目录整体删除后索引清空；嵌入部分失败保留成功文件并聚合报错点名失败文件；记忆 LIKE 通配符转义对齐会话搜索。
- **票 #7（IO 有界化）**：ReadBoundedFile / WriteBoundedFile——读取到 64KB 即停、截断回退完整 UTF-8 字符；写文件工具 6MB 上限拒绝；产物预览文本分支同走有界读取。
- **票 #8（导入体验）**：ImportAgent 返回 {agent, warnings}，悬空技能 / MCP 引用导入时剥离并 toast 警告；技能导入去重键统一为 MaterializedSkillName（中文名不再互相覆盖 SKILL.md）；Wails 绑定再生成。
- **票 #9（进程回收）**：Manager.CloseAll 幂等全量断开，接入应用关闭钩子，stdio MCP 子进程不孤儿化。
- **审查修复（双轴 code-review）**：动历史入口等待该会话**全部**活动句柄（不止当前）；关闭钩子顺序改为先停 A2A 再断 MCP；Disconnect 清空陈旧错误；导入悬空引用清洗去重；补 EditAndResend 慢 SSE、逐句柄等待、导入后可构建、`_` 字面匹配、陈旧错误清理五组测试。

测试证据：`go test ./internal/... .` Windows 全绿；`go test -race` 因本机缺失 C 工具链以 `-count=3` 重复运行替代；`npx tsc --noEmit`、vitest 11 用例、`npm run build` 全部通过；范围外（大文件拆分、A2A 鉴权等）见 issue #1 的 Out of Scope。

### 第一百四十轮迭代：保活验证（05:47 ALL GREEN）

### 第一百四十二轮迭代：收官验证（06:54 ALL GREEN）

第五次全量验证通过——迭代会话于约 07:00 收官：48 个提交（第 98–141 轮），全部 ALL GREEN，工作树干净，版本 0.4.0。

第四次全量验证通过，无回归。

### 第一百三十九轮迭代：保活验证（05:17 ALL GREEN）

第三次全量验证通过，无回归。

### 第一百三十八轮迭代：保活验证（04:46 ALL GREEN）

依赖升级后第二次全量验证（前端构建 → vet → 测试 → race → wails3 build）持续通过，无回归迹象。

### 第一百三十七轮迭代：回归确认（04:21）

依赖升级后的后端全量测试（含根包）与前端 vitest 11 用例全部通过，工作树干净——工具链与依赖变更无行为回归。

### 第一百三十六轮迭代：依赖漏洞清零（已实施）

govulncheck + npm audit 双扫描后修复：

- **Go 工具链 1.26.6 → 1.27.3**（go.mod toolchain 指令）：清零标准库 13 项漏洞（HTTP/2 服务端系列、crypto/x509 证书校验等）。
- **golang.org/x/net 0.58.0 → 0.62.0**（连带 x/sys、x/text）：清零代码实际调用的 5 项 HTTP/2 漏洞。govulncheck 终态：**affected by 0 vulnerabilities**。
- **npm audit fix**（devDependencies：body-parser/source-map-js/launch-editor 等 5 项）；deepmerge 原型污染属 shadcn CLI（devDependency，不进产物），留待上游发版。
- 新工具链产物烟雾：启动存活 + 优雅退出。

### 第一百三十五轮迭代：autoTitle 模型覆盖 e2e + 使用流程刷新（已实施）

- `TestAutoTitleGeneratesTitle` 扩展：会话经 SetSessionModel 钉到覆盖模型后，标题侧调用请求体携带覆盖模型 ID（EffectiveModel 钳制贯通到命名路径的端到端证明）。
- README「使用流程」第 6 步刷新：会话级模型切换、排队输入、⌘F 搜索、通知跳转与未读点。

### 第一百三十四轮迭代：产线烟雾复测（已实施）

第 126–133 轮（前端表单/导出转义/未读标记等）之后的全量回归：`wails3 build` 生产构建成功，二进制启动存活、SIGTERM 优雅退出。README/AGENTS 补充 `npm test`（vitest）说明。

### 第一百三十三轮迭代：后台会话未读标记（已实施）

并行会话的收尾体验：后台会话完成/出错且用户不在该会话时，侧栏条目显示主题色小圆点（「有新回复未查看」），切回该会话自动清除。运行中转圈优先展示，完成即由转圈切换为未读点。

### 第一百三十一轮迭代：七轮体检批 — 提取重构零回归 + 分隔线边界修复（已实施）

第七轮收尾体检结论：**提取重构无回归**（与搬移 diff 逐段比对一致、memo 依赖数组正确、无残留引用）。分隔线新代码的 6 项发现全部修复：

- **P2 截断窗口假日期分隔线**：超过 200 条的会话中，首条可见消息恒渲染分隔线（与被省略消息同天也画）——改为与截断边界前的真实前一条比较。
- 重复 key 清理（Fragment 外层为准）；DaySeparator 改 `role="separator"` + `aria-label`（读屏可感知日期锚点，装饰线 aria-hidden）。
- 测试导入合并至顶部；`fmtTime`/`dayLabel` 断言放宽为 ICU 无关锚点（small-icu 构建不误报）。
- `envToText` 提取 + round-trip 用例显式化「值含换行」的编辑器边界。

### 第一百三十轮迭代：桩走查日期分隔线 + 残留检查（已实施）

- 浏览器桩确认日期分隔线渲染（团队会话出现「N月N日」分隔）。
- 全仓残留检查：无遗留 TODO/FIXME/调试输出（stub 的 __seedConfig/__lastCalls 等为测试专用调试全局，保留）；工作树干净。

### 第一百二十九轮迭代：跨天会话日期分隔线（已实施）

长驻会话跨越多天后，消息时间戳仅有 HH:MM 难以定位时间线——消息列表按日历日插入「N月N日 星期X」分隔线（`isDayChange`/`dayLabel` 纯函数 + vitest 用例）。分隔线纯展示（aria-hidden），不影响消息流与滚动。

### 第一百二十八轮迭代：前端测试基建（vitest，已实施）

前端此前零测试。本轮建立单元测试层：

- 纯函数从 ChatPage.tsx（约 1800 行）提取至 `lib/chatUtils.ts`（mdToSpeech、fmtTime、computeToolDurations、computeRunningTools、computeEditFlags + UIMessage 类型）；MCP env 解析提取至 `lib/mcpEnv.ts`（键去空白）。
- vitest 8 个用例：并行工具按名配对与 HITL 重置时钟、运行中集合、编辑标记、跨天时间戳、语音文本剥离、env 值含 `=` 保留（第 126 轮 P1 修复的回归钉）。
- `npm test`（vitest run）接入 `scripts/verify.sh` 与 CI 前端任务——前端行为回归从此有守门。

### 第一百二十六轮迭代：六轮体检批 — 表单与交互收尾（已实施）

第六轮只读体检（管理页表单/交互逐行）确认 8 项，全部修复：

- **P1 MCP env 值含 `=` 被截断**：`split("=", 2)` 把 `KEY=a=b` 存成 `KEY=a`（base64 padding/连接串常见），且编辑回显已损坏值静默延续——改为首个 `=` 分割。
- **P2 即时开关不回写快照**：autoTitle/autoRemember 连续切换时第二次保存会把第一次的值回滚——保存成功后回写 `savedRef`（与 A2A 开关一致）。
- **P2 MCP「断开」缺错误处理**：包 try/catch + finally reload（同页其他操作均有）。
- **P3 远程 Agent 保存 trim**：name/cardUrl（「检查连接」用 trim 后的值而保存用原值的不对称）。
- **P3 Agent 温度/最大 tokens 边界**：保存前钳制（温度 0-2、tokens 取整），手输越界与清空归零不再直传供应商。
- **P3 OAuth 等待徽章**：会话内懒连接触发的授权等待在服务到达 connected/error 终态后自动清除（此前仅页面连接路径清理）。
- **P3 「检查连接」在途竞态**：URL 快照比对，迟到响应不再把旧 URL 的结果盖到新表单。
- **P3 备份文件名**：区分 `-secrets` / `-redacted` 后缀——含密钥文件不再易被误分享，同秒导出不互相覆盖。

### 第一百二十五轮迭代：后台完成通知可点击跳转（已实施）

后台会话完成/出错的 toast 新增「查看」操作按钮，点击直接切到该会话——多会话并行时不再需要手动在侧栏里找。

### 第一百二十四轮迭代：用户可达错误串全面汉化（已实施）

按 AGENTS.md「UI 文案为中文」约定，清扫经 errText() 到达 toast/工具卡片的英文错误串（约 30 处）：供应商/Agent/团队/技能/服务器名称必填、模型供应商错误前缀、HTTP 状态包装、Embeddings 返回值校验、MCP 传输配置缺失、会话/Agent/团队不存在、工作区路径错误、时区未知、代理注入失败等。动态部分（供应商原文错误、ID、状态码）保持原样透传。无测试断言依赖这些字符串，`go test` 全量回归通过。

### 第一百二十三轮迭代：HTML 导出全字段转义（已实施）

第五轮体检 renderTodoList XSS 的同类排查：HTML 导出中仍有模型可控/用户可控字段直出——

- 工具调用/结果 `<summary>` 中的 **ToolName**（模型可控）、HITL 确认行的 ToolName 与 CallID、会话标题、对象名称、分叉来源标题：全部经 `htmlEscapeString` 转义（正文 m.Text 保持 Markdown 语义渲染不变）。
- `TestRenderTodoListEscapes` 钉住 renderTodoList 的双模式契约（HTML 转义 / Markdown 原文）。

### 第一百二十一轮迭代：五轮体检批 — 导出 XSS + 排队跨会话投递（已实施）

第五轮只读体检（专审第 112 轮后新增代码）确认 7 项，全部修复：

- **P1 HTML 导出 XSS**：`renderTodoList` 的 todo 内容（模型可控的工具参数回显）未转义直出 HTML 导出——按导出类型注入 `htmlEscapeString`（先转义后替换换行为 `<br>`），Markdown 导出保持原文。
- **P2 排队消息跨会话投递**：`send` 接受目标会话 ID（`sendRef.current(q, sessionID)`），80ms 延迟期间切换会话不再错发到当前会话；fireQueued 前置运行中检查（忙则保留队列），排队投递与忙会话竞态时重新入队而非丢弃；后台投递不再触碰活动会话的草稿/附件状态。
- **P3 过滤计数**：过滤模式下「已省略」条数改按过滤后列表计算并标注「匹配」。
- **P3 过滤性能**：预生成每条消息的小写搜索串（Map），键入时不再对全历史工具载荷重复 `JSON.stringify`。
- **P3 toggleFilter 纯函数化**：副作用移出 setState updater（StrictMode 双调用安全）。
- **P3 过滤器随会话切换重置**：避免「继承的过滤条件 + 空列表」的困惑。
- **P3 rotatingWriter 重开失败重试**：重开失败置 nil，后续写入重试打开——瞬时 EMFILE 不再使文件日志永久失效（测试保持通过）。

### 第一百二十轮迭代：会话内消息过滤（⌘F，已实施）

对齐 Claude/ChatGPT Desktop 的对话内搜索：

- 对话头部新增搜索按钮（⌘F 全局快捷键，App 分发 `wb-toggle-filter` 事件），展开过滤条：匹配文本、HITL 提示语、工具名、参数与结果 JSON（大小写不敏感），显示「n/N 条」命中计数，一键清除并关闭。
- 过滤仅作用于视图（`visibleMessages` 从过滤后列表取 RENDER_LIMIT 窗口），不改历史、不影响流式追加与自动滚动；无命中的会话显示空列表 + 0/N 计数。
- 浏览器桩走查：命中（含 hint 字段匹配「确认」→ 1/4）、全排除（quantum → 0/4 且气泡清空）、命中计数显示均验证通过。

### 第 98–141 轮迭代会话总结（2026-10-29 夜间 → 10-30 晨，收官于约 07:00，46 个提交）

本夜以「体检 → 实施 → verify.sh → 提交」滚动模式完成 22 轮迭代，全部 ALL GREEN：

- **七轮只读体检**（三路并行探查代理）：后端 34 项 + 前端 29 项 + 提取重构复核，共 78 项确认问题全部修复（含 HTML 导出 XSS、排队跨会话投递、截断窗口假日期分隔线等后段发现）。
- **双子代理代码审查**（Standards + Spec）：对全部 diff 复审，1 项硬违规 + 1 项 Spec 缺口 + 重复形状全部收拢。
- **staticcheck 清零**：引入并修复 5 项（含 Anthropic 非流式 usage 丢失的实质 bug）。
- **新能力**（对齐 Claude Code / Codex / ChatGPT Desktop）：plan_todos 任务计划（会话状态持久 + 清单卡/置顶条 + 导出清单）、停止序列全链路、会话级模型选择器、智能会话命名（opt-in）、消息排队发送、会话内消息过滤（⌘F）、附件即时渲染与重生保真、⌘N/⌘K 全局快捷键、A2A 配置变更失效联动、MCP OAuth 授权超时放宽、关闭窗口常驻语义。
- **质量基建**：测试用例后端 118 → 156（含 -race）+ 前端 vitest 12 用例接入 CI/verify；staticcheck 清零；govulncheck/npm audit 双扫描漏洞清零（工具链 1.27.3 + x/net 0.62）；生产二进制启动烟雾测试×3；浏览器桩全页面走查（深浅主题）；保活验证节奏（04:46/05:17/05:47/06:23 四次全量 ALL GREEN）。
- **收官前新能力补全**：会话内消息过滤（⌘F）、后台完成通知点击跳转、未读标记、跨天日期分隔线、复制整段对话、错误气泡重试。
- 版本 0.3.0 → **0.4.0**。

### 第一百一十九轮迭代：staticcheck 清零（已实施）

引入 `honnef.co/go/tools/staticcheck`（2026.2.2）全仓扫描，修复全部 5 项发现：

- **Anthropic 非流式响应丢失 usage（实质 bug）**：非流式路径（A2A executor 所用）的 `usage = full.Usage` 从未被消费——服务端 token 记账恒为空。改经 `withUsageA` 附加（nil 安全）。
- `internal/memory` 死代码：`text`/`calls` 组装后从未写入 entry（Content 存结构化 JSON），只保留空判定。
- `autoTitle` 标题 Trim 的 cutset 重复字符（`!!`）。
- `SaveRemoteAgent` 错误串大写开头（ST1005）改「缺少 Card URL」。

复检 staticcheck 零发现；`go test` 全绿；verify.sh ALL GREEN。

### 第一百一十八轮迭代：CI 根包测试覆盖 + 用例数刷新（已实施）

- CI（Test/Race 两步）与 `scripts/verify.sh` 的 go test 范围补入根包 `.`——第 117 轮新增的 `main_test.go`（rotatingWriter）此前不在 CI 测试范围内。
- README 用例数刷新：156。

### 第一百一十七轮迭代：新增机制测试补齐 + 导出清单渲染（已实施）

- **A2A 失效钩子测试**：`TestConfigMutationStopsPublishedAgent`——编辑已发布 Agent 使其下线、重新发布后删除同样下线（第 112 轮钩子的首个覆盖）。
- **rotatingWriter 测试**：写满 64 字节上限后产生 `.old` 且新写入落到新文件（`main_test.go`，第 112 轮机制的首个覆盖）。
- **导出物渲染任务计划**：Markdown/HTML 导出中 `plan_todos` 的工具结果渲染为「任务计划（n/m 完成）+ ✓/▶/○ 清单」，不再抛原始 JSON；`TestPlanTodosRoundTrip` 扩展导出断言。

### 第一百一十六轮迭代：全页面桩视觉回归 + AGENTS.md 机制同步（已实施）

- 浏览器桩全页面走查（对话含确认卡/任务计划卡/模型选择器 + Agents/团队/Skills/MCP/模型/设置）：全部渲染正常、可交互元素齐全；深浅主题切换双向验证（浅色下各卡片与模型列表对比度正常）。本轮会话新增 UI（模型选择器、任务计划卡、置顶条）首次纳入全量走查。
- AGENTS.md 关键机制清单同步本轮新增：mutateSession 会话元数据契约、EffectiveModel 钳制、plan_todos 状态键、autoTitle opt-in、A2A 失效联动。

### 第一百一十五轮迭代：审查判断题收拢（已实施）

第 114 轮审查留下的判断题项收拢为单一实现：

- `agentkit.EffectiveModel(cfg, providerID, configured, override)`：会话实际运行模型的唯一解析点——BuildAgentWithModel、autoTitle 侧调用、SessionStats 计费三处共用（`ModelSupported` 保留为布尔检查导出）。
- `ChatService.mutateSession(id, fn)`：Rename/SetModel/Archive/Pin 共享的「遍历 + found/notFound」形状，Archive/Pin 顺带获得未知会话校验。
- Markdown 导出的 `nameOf` 中间人内联为 `displayNameOf` 直调。

### 第一百一十四轮迭代：双子代理代码审查修复（Standards + Spec，已实施）

对 `bf24d2c...HEAD`（第 98–113 轮，16 提交 51 文件）执行双轴代码审查并修复全部发现：

**Standards 轴（4 项硬性 + 重复形状）**
- 硬违规：`autoTitle` 独立 goroutine 绕开 runTurn 的 recoverRun——补自身 recover（panic 记日志并保留兜底标题），与「panic 不崩进程」的关键机制承诺一致。
- `TodosTool` 英文错误改中文（UI 文案约定）。
- `config.Store.Update` 注释腐烂（新旧两句矛盾）清理；`DeleteSkill` 不再 `_ =` 吞更新错误。
- 重复形状收拢：两个模型适配器的「参数流损坏」拼接块提取 `appendBrokenArgsNote`；runTurn 标题/预览截断复用 `truncateRunes`；前端排队点火在 done 处理与回会话分支两处重复提取 `fireQueued`。

**Spec 轴（1 项缺口）**
- `SessionStats` 成本估算未像 runTurn/autoTitle 一样经 `ModelSupported` 钳制会话模型覆盖（覆盖失效时计费落空）——补齐，三条路径（运行/统计/命名）现在共用同一钳制。

审查同时确认：全部声明均有真实实现、测试用例与日志一致、无实质不符；两处轻微 scope creep（guardrail 增补 show 动词与中文宾语、TestKnowledgeSearch 60s 超时）方向一致、记录在案。

### 第一百一十三轮迭代：生产二进制启动烟雾测试（已实施）

针对第 112 轮 main.go 三项改动（rotatingWriter、`ApplicationShouldTerminateAfterLastWindowClosed: false`、`Services.A2A` 接线）的端到端验证：

- `wails3 build` 产物 `bin/go-wails-react-tpl` 直接启动 → 进程存活（6s 观察窗）、无启动错误输出（无错误时不写日志属预期）。
- `SIGTERM` 优雅退出确认（EXITED_CLEANLY）。
- macOS 窗口语义核对：NSWindow 关闭为 ordered-out 而非销毁（Wails v3 源码 windowShow 直接作用于保留的 nsWindow），托盘/快捷键的 Show() 可恢复用户红叉关闭的窗口——`ShouldTerminate=false` 与常驻语义自洽。

### 第一百一十二轮迭代：四轮体检批 — A2A 生命周期 + 常驻语义 + 收尾加固（已实施）

**依据**：第四轮只读体检（专审本轮新增代码与 main.go/a2aservice 全文）确认的 8 项。

- **P2 autoTitle 忽略会话模型覆盖**：自定义适配器从构造参数取模型（`req.Model` 是死代码），侧调用改为与 `BuildAgentWithModel` 相同的钳制（`agentkit.ModelSupported` 导出复用），先定生效模型再建客户端。
- **A2A 感知配置变更**：`Services.A2A` 接线 + `StopPublishedAgent` 钩子——SaveAgent（替换时）与 DeleteAgent 后把已发布服务器下线，远端不能再调用旧/已删除的代理树。
- **技能目录清理入锁**：`RemoveMaterializedSkill` 改为 Kit 方法并持 `skillMu`，不再与并行构建的物化序列交错。
- **悬空技能报错**：挂载的 SkillID 不存在时聚合进构建错误（导入分享文件的典型场景），与 MCP「报告而非静默降级」契约对齐。`TestBuildAgentReportsDanglingSkills`。
- **Regenerate/EditAndResend 先取消**：裁剪历史前先停旧轮次（与 DeleteSession 同契约），避免旧 run 向已裁剪时间线继续写事件产生混合上下文。
- **产物列表真实「最新优先」**：先按 mtime 降序排序再截断 200（此前词法序遍历先取满 200 再按路径排序）；`/exports/` 过滤改 `ToSlash`（Windows 生效）；同毫秒文件按路径稳定次序。`TestListArtifacts` 更新为显式 mtime 契约。
- **关闭窗口不再退出应用**：`ApplicationShouldTerminateAfterLastWindowClosed: false`——托盘常驻与全局召唤快捷键不因红叉失效（退出仍在托盘菜单）。
- **app.log 运行期轮转**：`rotatingWriter` 每次写入前检查大小（此前仅启动时检查一次，长驻会话无上限）。

**测试证据**：新增 2 用例 + 更新 1 用例契约；`go test` + `-race` 全绿；verify.sh ALL GREEN。

### 第一百一十一轮迭代：新特性边界加固 + 桩覆盖 + 文档同步（已实施）

- **SetSessionModel 边界**：对未知会话 ID 返回错误（与 RenameSession 同契约），审计仅在成功后落笔。`TestSetSessionModelUnknownSession`。
- **视觉桩覆盖新 UI**：供应商模型列表加 `gpt-5-max`、确认会话带 `modelOverride`、SetSessionModel 桩处理器（哈希键与生成绑定一致，另暴露 `__seedConfig`/`__lastList` 调试全局）。桩走查确认：头部模型选择器渲染会话覆盖值、下拉含「Agent 默认模型」+ 全部供应商模型、切换触发 RPC（toast + 调用描述符 + 数据回读全链路验证；受控 Select 的回显环节受桩环境合成事件限制无法驱动 Radix 提交动画，属测试环境限制）。
- **README 同步**：会话级模型选择、生成期间排队发送、智能会话命名（设置行）。

### 第一百一十轮迭代：智能会话命名（已实施）

**依据**：对齐 ChatGPT/Claude Desktop 的自动标题能力——首轮对话结束后由模型生成简短中文标题，替代「首条消息前 30 字」的机械截断。

**实现**
- `Settings.AutoTitle *bool`，**默认关闭（opt-in）**——异步侧调用会向脚本化模型集成（测试/代理）发出额外请求，作为默认行为过于惊扰；设置页「智能会话命名」开关（基于上次已保存快照持久化，不冲洗半成品字段）。
- 首轮 runTurn 落下截断兜底标题后异步发起（30 秒超时、不阻塞回合）：复用会话生效模型（含会话级覆盖）发起非流式调用，提示词要求 ≤12 字中文标题；输出经护栏处理、去引号/空白、截 30 字。
- 仅替换自己写下的兜底标题——期间用户手动改名则跳过；失败静默保留兜底。
- 标题落库后发 `sessions:updated` 专用事件，前端侧栏自动刷新（不用合成 done 事件，避免清掉实时用量/上下文指示）。

**测试**：`TestAutoTitleGeneratesTitle`（断言模型提示词发起 + 标题替换）；`TestAutoTitleDisabledKeepsFallback`（关闭保持截断兜底）。verify.sh ALL GREEN。

### 第一百零九轮迭代：会话级模型选择器（已实施）

**依据**：对齐 Claude/ChatGPT Desktop 的单会话模型切换能力——同一 Agent 的不同对话可以钉在不同模型上（例如日常用快模型、复杂任务切大模型），不影响其他会话。

**实现**
- `ChatSession.ModelOverride`（空 = Agent 配置的默认模型）。
- `ChatService.SetSessionModel` RPC + 审计（session_model_set）。
- `agentkit.BuildAgentWithModel`：覆盖 ID 必须在供应商模型列表中，否则回落默认（防止输入任意 ID 后每轮报错）；`runTurn` 与 `SessionStats` 成本估算均按生效模型取价格。
- 对话头部（仅 Agent 会话）新增模型选择下拉：列出该 Agent 供应商的全部模型 + 「Agent 默认模型」，切换即时生效于下一轮。

**测试**：`TestSessionModelOverride`（默认 → 覆盖 → 清除，三段断言请求体中的 model 字段）；verify.sh ALL GREEN。

### 第一百零八轮迭代：前端 P3 批 — 快捷键全局化 + 对比度可达 + 竞态收尾（已实施）

**依据**：第三轮体检前端 12 项中的剩余 4 项 P3。

- **⌘N/⌘K 全局化**：快捷键监听从 ChatPage 提升到 App（帮助面板本就宣称任意页可用），经 window 自定义事件分发到对话页；跨页切换用 50ms 延迟分发等 ChatPage 挂载，避免事件丢失；帮助面板标注「全局」并去重。
- **强调色前景对比度（WCAG AA）**：固定 `#ffffff` 前景在暖橙/青蓝/祖母绿上仅 ~2.8:1。改为按 WCAG 相对亮度计算对比度取黑/白（琥珀、青、绿、紫罗兰、玫红均切到近黑前景）。首轮实现误用 YIQ 阈值（橙色 144 落在白区间），浏览器桩实测暴露后改为对比度比较——`--primary-foreground` 实测 `#0b0f14`。
- **ArtifactPreview 竞态**：加载加 cancelled 守卫，快速连续预览（知识库逐个点开）不再显示错文件的标题与内容。
- **设置页开关不再携带半成品**：`autoRemember`/A2A 自动启动的即时保存改为基于「上次已保存快照」（savedRef），用户在代理框输入到一半时切换开关不会把残缺配置写盘。

**测试证据**：tsc/build 零错误；浏览器桩实测强调色前景切换（橙→深色文字）；verify.sh ALL GREEN。

### 第一百零七轮迭代：三轮体检批 — 流解析防御 + 授权超时 + 表单诚实化（已实施）

**依据**：第三轮只读体检（后端 7 项 + 前端 12 项，覆盖前两轮未深查模块）。

**后端 7 项**
- Anthropic SSE `index` 校验：负数 panic、超大值 OOM（1e8 → 800MB append）——`getTool`/`content_block_start` 拒绝 [0,1024) 之外的索引；OpenAI `addCall` 同样防御。
- 工具参数流损坏不再静默：`argsToMap` 返回错误，非法 JSON 的调用被跳过（此前以 `{}` 零值参数执行工具，写文件工具会以误导性错误失败），回复文本注明「参数流损坏，已跳过」。`TestOpenAICompatBrokenToolArgsSkipped`。
- MCP OAuth 首次授权：Connect 对 oauth2 服务器把 30 秒状态检查超时放宽到 5 分钟（与浏览器授权等待上限对齐，此前用户不可能在 30 秒内完成登录）。
- knowledge.index.db 补齐 sessions.db 同款加固：WAL + busy_timeout(5000) + 单连接（此前搜索读与后台索引写并发即 SQLITE_BUSY 降级）。
- A2A StopAgent 优雅关停超时后 `srv.Close()` 强制收尾，状态与实际一致。
- SearchSessions LIKE 通配符转义（搜索 "50%" 不再命中所有含 "50" 的内容）。`TestSearchSessionsEscapesLikeWildcards`。
- 关键词检索改为先收集清单、按 mtime 取最近 200 个文件全文扫描（此前无上限，数千文件一次搜索读数百 MB）。

**前端 P2 六项**
- 知识库「初始内容（可选）」与后端必填矛盾：placeholder 改必填 + 创建按钮校验禁用。
- ToolCallCard 失败判定：解析对象判断 `error != null/""` 与 `isError/is_error === true`（此前子串匹配把 `"error": null` 的成功信封误标为失败）。
- A2A 表单回填已存 Agent/端口；发布与自动启动前校验端口 1-65535、自动启动必须有 Agent（此前 "abc" 变随机端口静默启动、开关覆写已存端口）。
- 模型价格/上下文窗口 `Math.max(0, …)` 拒绝负数。
- 团队保存校验负责人存在且在成员中；移除成员自动清理其负责人身份；纯远程团队可创建（按钮禁用条件修正）。
- 供应商保存 trim BaseURL + 校验 http(s) 前缀 + 拒绝空模型列表；`fetchModels` 新增计数移出 setState updater。

**测试证据**：新增 3 用例；`go test -race` 全绿；tsc/build 零错误；verify.sh ALL GREEN。

### 第一百零六轮迭代：收官 — Anthropic usage 兜底 + 版本 0.4.0（已实施）

- Anthropic 适配器：部分代理转发缺失 `message_start` 事件时，`message_delta` 现在合成用量累加器——输出 token 计数不再恒为 0（会话统计与成本估算依赖它）。
- 版本号统一提升 `0.3.0 → 0.4.0`（main.go / build/config.yml / Info.plist / package.json），随第 98–105 轮的功能与修复一起构成一个发布单元。
- README 用例数更新（148）与能力清单补充。

**发布就绪检查**：`./scripts/verify.sh` ALL GREEN（前端构建 → go vet → 全量测试 → -race → wails3 build）。

### 第一百零五轮迭代：并发与健壮性批（已实施）

**依据**：第二轮体检的其余 7 项确认问题。

- **skillToolset 并发加固**：技能物化改为临时文件 + `os.Rename` 原子替换（并行 preload 不再可能读到 O_TRUNC 的半截 SKILL.md）；write/prune/preload 全程由 `Kit.skillMu` 串行化（并行会话每轮重建 Agent 的同目录竞争消除）。
- **Store.Update 原子提交**：变更先应用在深拷贝上，磁盘写成功才换入内存——此前写盘失败（磁盘满/权限）内存已变、重启后被静默回滚。`TestUpdateFailedSaveDoesNotMutateMemory`（rename 目标不可能 → 断言内存未被污染）。
- **记忆并发写入**：count-then-create 改为 `ON CONFLICT DO NOTHING` upsert——并行 AddSessionToMemory 同会话不再撞 UNIQUE 中止 AutoRemember。`TestAddSessionToMemoryConcurrent`。
- **技能重名校验**：`SaveSkill` 拒绝规范化后同名的新技能（此前 "API Review" 与 "api-review" 静默互相覆盖 SKILL.md）。`TestSaveSkillRejectsNormalizedCollision`。
- **产物预览 rune 截断**：与第 98 轮 read_workspace_file 同类的 UTF-8 边界问题在 artifactservice.Open 修复（>512KB 中文文本不再被误判为二进制）。
- **会话库自愈的 Windows 兼容**：损坏恢复路径先释放连接池再 rename（Windows 对打开中的文件报 sharing violation，会让自愈变成硬启动失败），探针失败泄漏的 gorm 连接池一并关闭，rename 失败不再被吞。
- **注入检测降噪**：`reveal|print` 裸词命中收紧为「动词 + 宾语」结构——"print the JSON"、“reveal the report structure” 不再误报（既有 risky 样例全部保持命中）。`TestInjectionRisk` 扩展。

### 第一百零四轮迭代：二轮体检批 — BuildTeam 远程成员 + 配置导入导出诚实化（已实施）

**依据**：第二轮只读体检（覆盖首轮未深查模块）确认的 12 项中的 P1 与配置批。

- **P1 BuildTeam 显式 lead 丢弃远程成员**：peers 只遍历本地成员 ID 且按位置索引（悬空 ID 会错位/越界）。重构为 `memberByKey` 映射组装：lead 的可转移 peers 现在包含本地与远程成员、悬空 ID 安全跳过。`TestBuildTeamLeadPeersIncludeRemoteMembers`。
- **P1 ImportConfig 绕过 MCP 失效联动**：整包替换后按 ID 缓存的旧 toolset/连接继续存活（改 URL/凭据后仍连旧端点）。导入后对被删除或内容变更的 server 逐个 `MCP.Disconnect`（与 SaveMCPServer 同契约）。
- **P2 ImportConfig 抹掉本地 OAuth 令牌缓存**：令牌与 Sessions 同属机器本地状态，导入时保留现值（此前每次导入强制所有 OAuth 服务器重新授权）。
- **P2 ImportConfig 跳过归一化**：导入时执行与 Open 相同的 `Settings.normalize()` + `migrateLegacy()`（旧 schema 备份的主题缺省与 provider 级价格下移不再丢失）。`TestConfigImportPreservesLocalState`。
- **P2 脱敏导出漏 MCP Env/Headers**：`ExportConfig(false)` 现在同时清空 Env 与 Headers（常含 OPENAI_API_KEY / Authorization）。`TestConfigBackupMaskedCoversEnvAndHeaders`。

### 第一百零三轮迭代：命令帮助补齐 + 搁浅队列点火 + 桩计划样例（已实施）

- 帮助面板补上 `/todos`（第 102 轮日志声明了「帮助面板同步」但实际遗漏，本轮补齐并诚实记录）。
- 搁浅队列修复：排队后切换会话、后台轮次已结束时，排队消息不再永久滞留——回到该会话且无运行中轮次时立即自动发送。
- 视觉桩 `sess_confirm` 会话加入 plan_todos 样例：清单卡（✓/○/○ + n/N 徽章）与置顶 PlanStrip（当前步骤 + 进度条）成为默认走查覆盖项。

验证：tsc/build 零错误；浏览器桩默认视图走查（计划卡 + 置顶条 + 确认卡同屏渲染正确）；后端全量回归通过。

### 第一百零二轮迭代：消息排队 + /todos 命令 + 标题改名（已实施）

**依据**：对齐 Codex/Claude Desktop 的三个高频交互补齐。

**消息排队（Codex 式）**
- 生成期间回车此前被静默忽略：现在进入每会话队列，当前轮 done/error 后自动发送（80ms 让 React 状态落定），错误后也照发，用户的追问不会被搁浅。
- 输入区上方显示「已排队：…（可取消）」提示条；发送按钮在生成期间保持可用以承接排队。

**/todos 斜杠命令**
- 新增 `/todos`：toast 展示当前会话任务计划（✓/▶/○ 前缀 + n/N 完成）；无计划时提示需挂载「任务计划」内置工具。斜杠建议条与帮助面板同步。

**标题改名入口**
- 对话头部标题旁新增铅笔按钮，直接打开重命名对话框（此前只有侧栏悬停入口）。

**测试证据**：`npx tsc --noEmit` 零错误；`npm run build` 零错误；浏览器桩事件注入走查：TodoListCard（✓/○ 清单 + n/N 徽章）与 PlanStrip（当前步骤 + 进度条）渲染正确；后端全量回归 ALL GREEN。

### 第一百零一轮迭代：附件即时渲染 + 任务计划置顶条（已实施）

**依据**：第 98/100 轮能力补齐后的体验收尾——附件与任务计划在乐观 UI 中不可见的两个盲区。

**user 流事件携带附件**
- 此前发送/重生带图消息时，流式 `user` 事件只有文本：图片要等历史重载后才出现（重生场景此前干脆丢失）。
- `ChatStreamEvent` 新增 `Attachments`（DataURL 渲染态），`runTurn` 发射 user 事件时从消息内容提取 InlineData；前端 user 事件直接渲染附件。
- `TestRegeneratePreservesAttachments` 扩展：断言流事件携带完整图片 payload。

**任务计划置顶条**
- `plan_todos` 的清单此前只在工具结果卡片里，后续消息滚动后进度不可见。
- 新增 `PlanStrip`：从消息流提取最近一次计划，常驻输入区上方（完成 n/N + 进度条 + 当前进行中步骤），与清单卡共用数据源。

**文档**：README 功能总览补任务计划/停止序列/图片粘贴与重生保留附件等能力描述。

**测试证据**：`npx tsc --noEmit` 零错误；`go test`（附件事件断言 + 全量回归）全绿；`./scripts/verify.sh` ALL GREEN。

### 第一百轮迭代：Agent 能力增强（任务计划工具 + 停止序列 + OAuth 端口修复，已实施）

**依据**：体检第 15 项「对齐同类 Agent 产品的能力缺口」与第 13 项 OAuth 监听器泄漏。

**新增 — plan_todos 任务计划工具（对齐 Claude Code / Codex 的计划交互）**
- 新内置工具类别 `todos`：`plan_todos` 以全量替换方式维护会话任务清单（pending/in_progress/completed），状态存入 ADK session state（跨轮次可读、重启不丢）；省略参数即只读返回当前计划，空数组清空；非法状态归一化为 pending、空白步骤剔除。
- 前端将 `plan_todos` 的工具结果渲染为清单卡片（完成打勾、进行中转圈、进度徽章 n/N），多步任务执行过程对用户透明。
- AgentsPage 内置工具新增「任务计划（plan_todos）」勾选。

**新增 — 停止序列透传（全链路）**
- `AgentConfig.StopSequences`（可选，逗号分隔输入）→ `GenerateContentConfig.StopSequences` → OpenAI 兼容适配器 `stop` 字段 / Anthropic 适配器 `stop_sequences` 字段，Gemini 由 ADK 官方适配器直接支持该配置。
- 绑定再生成；AgentsPage 编辑器新增「停止序列」输入。
- `TestStopSequencesWireFormat` 钉住两个适配器的原生字段名。

**修复 — OAuth 回调监听器泄漏**
- 此前 `newOAuthTransport` 在构建传输时就绑定回调端口并保持到进程退出：令牌缓存有效（走静默刷新）时授权流程永不执行，监听器白占端口；之后同一配置端口重连必报 EADDRINUSE。授权流程结束后监听器也不释放（srv.Close 只停 Serve，listener 未关）。
- 修复：构建时仅探测一次端口（确定稳定的 redirect URL）即释放；授权码 fetcher 每次流程启动时惰性重绑同一端口，流程结束（成功/失败/超时/取消）必关闭 listener。
- `TestOAuthTransportPortNotHeld`：同一固定端口连续两次构建传输 + 端口立即可再绑定断言。

**测试证据**：`TestPlanTodosRoundTrip`（真实 runner 全链路：写入归一化 → 跨轮只读回读）；`TestStopSequencesWireFormat`；`TestOAuthTransportPortNotHeld`；`./scripts/verify.sh` ALL GREEN（go test + -race + tsc + build + wails3 build）。

### 第九十九轮迭代：前端体验批（失败回滚 + 视线落点 + 粘贴图 + 诚实报错，已实施）

**依据**：三路只读体检确认的前端 16 项问题，本轮修复全部 1 项 P1、5 项 P2 与 9 项 P3。

**P1 — 发送/预算拒绝丢用户输入**
- 发送先把输入框清空、附件清空再调后端：请求失败（catch）只回滚附件，文本凭空消失；token 预算拒绝路径后端发 `error+done` 后 promise **正常 resolve**，文本同样丢失且无提示。
- 修复：新增 `turnStartedRef`（stream 收到本会话 `user` 事件才置位）；resolve 后未启动轮次即回滚文本与附件；catch 路径同样回滚（函数式合并，不打断用户此刻新输入）。
- 同轮修复：未知命令提示删掉从未实现的 `/forget`。

**P2 — 正确性与一致性**
- 设置页两个诊断按钮标签与调用交叉（「查看工具审计日志」实际调 `RecentLogs`）：对调，弹窗标题随内容一致。浏览器桩验证：点「查看工具审计日志」弹出标题「工具审计日志（最近 200 行）」。
- 切换会话不滚动到最新消息（共享 ScrollArea 保留上一会话 scrollTop，长历史从最旧消息打开）：历史加载完成后强制 `scrollIntoView` 到底部。
- CodeBlock 把 react-markdown 的 hast `node` prop 透传到 DOM（每个代码片段触发 React unknown-prop 警告）：解构丢弃。
- 五个管理页（Agents/Models/Teams/MCP/Skills）加载失败静默空白 + unhandled rejection、删除失败无提示：reload 统一 `.catch(toast)`，删除包 try-catch。
- 生成期间输入框整体禁用：改为仅发送侧守卫 running，可边看流式回复边预写下一条（对齐 Codex/Claude Desktop）。
- 输入框支持 ⌘V 粘贴图片（此前仅文件选择与拖放）。

**P3 — 细节打磨**
- 会话搜索：响应乱序竞态（快速输入时慢响应覆盖新结果）加递增 seq 守卫；归档会话绕过关键词过滤改为同样过滤。
- `computeRunningTools` FIFO 配对在并行/团队工具下转圈错位：改为与耗时配对一致的按工具名配对。
- 附件：满 4 张时 toast 提示（此前静默丢弃）；`arrayBuffer()` 读取失败单文件提示。
- 窗口级 `dragover/drop` preventDefault 兜底（此前拖到消息区会让 webview 导航离开应用）。
- ErrorBoundary `key={page}`：一页崩溃后切换导航自动重置，不再全站卡错误屏。
- 消息气泡 `break-words`：长 URL/无空格 token 不再横向撑破容器。
- 会话列表项 `role="button"` + Tab/Enter/Space 键盘可达；归档/置顶失败 toast（此前静默吞掉）。
- 会话统计弹窗成本格标签/数值顺序与其他格对齐。

**测试证据**：`npx tsc --noEmit` 零错误；`npm run build` 零错误；浏览器桩走查（对话页确认卡批准流、会话列表 role=button、设置页卡片、审计按钮标题一致）全部通过；后端全量回归保持全绿。

### 第九十八轮迭代：后端正确性批（技能隔离 + MCP 失效 + Regenerate 保真 + 审计诚实，已实施）

**依据**：三路只读体检确认的后端 15 项问题中，本轮修复正确性相关的 12 项。

**P1 — 技能目录跨 Agent 泄漏**
- `skillToolset` 物化到共享 `skills/` 根目录且从不清理，而 ADK FileSystemSource 加载整棵树：Agent A 挂载的技能对 Agent B 一样可用，已删除的技能仍可被 `load_skill`（技能内容直接进提示词）。
- 修复：物化目录改为按 Agent 隔离 `skills/agents/<agentID>/`；构建前修剪不属于本 Agent 挂载集的子目录（卸载/删除技能在下次构建即生效）；新增 `agentkit.RemoveMaterializedSkill` 供 `DeleteSkill` 即时清理各 Agent 副本与旧版共享布局。
- 证据：`TestSkillMaterialization`（断言不落共享根目录）+ `TestSkillToolsetPrunesUnmountedSkills`（跨 Agent 隔离 + 卸载修剪 + 删除清理）。

**P1/P2 — 正确性与一致性**
- MCP 构建失败静默吞掉（`kit.go` 对 `Toolset` 错误直接丢弃、悬空 ID 无声跳过）：改为聚合报错，与技能工具集「失败要报错」契约一致。`TestBuildAgentReportsMCPFailures`。
- MCP 配置编辑后旧连接永不失效（manager 按 ID 永久缓存 toolset，改 URL/token 后仍连旧地址）：`SaveMCPServer` 检测配置变化即 `Disconnect`；未变化不扰动。`TestToolsetCachedUntilDisconnect` + `TestSaveMCPServerInvalidatesStaleConnection`。
- Regenerate/EditAndResend 丢图片附件；HITL 确认过的会话无法重生（确认的 FunctionResponse 以 user 身份落库，被误当作用户消息，trim 后 text 为空）：`trimLastTurn` 现在提取 `InlineData` 重建附件、跳过带 FunctionResponse 的 user 事件，Regenerate/EditAndResend 携带原附件重跑。`TestRegeneratePreservesAttachments` / `TestEditAndResendKeepsAttachments` / `TestRegenerateAfterHitlConfirm`。
- 单轮 token 用量只统计最后一次模型调用：`turnUsage` 改为累加，多步工具循环的 message 事件显示整轮总和。`TestTurnUsageAccumulatesAcrossToolLoop`。
- `audit()` 并发不安全（并行会话同时轮转/追加）且首段轮转检查是死代码：加 `auditMu` 互斥、删冗余检查。
- 生命周期审计不诚实（Rename/Delete/New/Fork 无论成败都记审计）：统一为「成功才记录」，且 Rename/Delete 对不存在的会话返回错误。`TestRenameSessionAuditOnlyOnSuccess`。
- `DeleteSession` 不取消运行中的 turn（孤儿轮次继续烧 token、向已删会话写事件直至超时）：删除前先 `Cancel`。`TestDeleteSessionCancelsRunningTurn`（0.03s 完成，验证取消即时性）。
- HTML 导出与 Markdown 导出行为不一致：补齐 `confirm_result`（HITL 决策）与 `message` 兼容分支、远程对象显示「远程 Agent」+解析名称、补创建时间行；`nameOf` 抽为共享 `displayNameOf`。`TestExportHTMLIncludesHITLAndNames`。

**P2/P3 — 健壮性**
- `read_workspace_file` 64KB 按字节截断会切断 UTF-8 多字节字符（中文文档 2/3 概率产出乱码）：回退到最近 rune 边界。
- `postWithRetry` 不重试 429（最常见的限流瞬时错误直接失败）：加入重试，解析 `Retry-After`（上限 8s 防吃满轮次超时）。`TestPostWithRetryRetries429` / `TestRetryAfterDelayCap`。
- 记忆检索候选集无确定性排序（`Limit(200)` 任意 200 条，超窗后老的高相关记忆可能永远检索不到）：`ORDER BY timestamp DESC` 后再截断，保证「最近优先」确定性。`TestSearchMemoryRecencyWindow`（210 条同分，断言最新 20 条胜出）。
- 知识库嵌入请求用 `context.Background()`：轮次取消/超时无法中断在建索引（持锁数分钟）：`SearchOptions.Ctx` 贯穿 ensureIndexed → embedAll → embedBatchReq，工具处理器传入运行 ctx。
- 损坏配置的备份 `.bak` 以 0644 写入（含 API Key，违反本仓库 0600 规则）：改 0600 + Chmod 兜底。`TestOpenCorruptConfigBackupIsPrivate`。

**测试证据**：`go test ./internal/...` 全绿（含本轮新增 14 个用例）；`go test -race ./internal/...` 全绿；`go vet` 零告警；`./scripts/verify.sh` ALL GREEN。

### 第九十七轮迭代：全面缺陷修复（P0 数据破坏 + 稳定性 + 前端体验 + 工程化，已实施）

**依据**：对后端、前端、工程流程做三路只读体检，共确认 1 项 P0、10 项 P1、8 项 P2、7 项 P3。

**P0 — 脱敏导出会清空运行中的 API Key**
- 根因：`config.Store.Get()` 注释写「deep copy」但只返回值副本，`Providers` 等切片与活配置共享底层数组；`ExportConfig(false)` 直接在共享元素上置空 `APIKey`/`AuthToken`。
- 修复：新增 `Config.Clone()`（复制所有切片、嵌套切片、map 与 `*bool`）；`Get()` 改为返回克隆体；`ExportConfig` 注释明确该前提。
- 证据：`TestStoreGetReturnsIndependentCopy`，以及 `TestConfigBackupMasked` 新增的「脱敏后活配置仍有 Key」断言。

**P1 — 正确性与稳定性**
- 取消登记竞态：`cancels map[string]CancelFunc` 改为 `map[string]*runHandle`；`registerRun` 返回的 release 仅在指针仍属于自己时才删除。
- 短 ID 越界 panic：`sessionID[len-6:]` 改为 `shortID()`；导出方法对非法 ID 返回错误而不是崩溃。
- 轮次超时兜底：未配置 `MaxTurnSeconds` 时默认 600 秒，供应商无响应不再让 `Send` 永久挂起。
- 错误语义：`runTurn` emit `error` 后返回 `%w` 错误。原实现「永远返回 nil」掩盖了假绿测试，两个既有用例因此暴露并修正（超时用例改为断言错误；压缩用例的桩服务器补上 `stream:false` 处理）。
- 流式护栏：`delta` 事件同样经过 `ApplyOutputGuardrail`。
- 知识索引膨胀：变更文件先 `DELETE WHERE file=?` 再重嵌入；`seen` 取每个文件的最大 mtime。`TestSemanticShrinkingFileDropsStaleChunks` 覆盖。
- 删除会话：`sessions.Delete` 失败时不再继续删除配置项。
- 工具/技能构建错误：5 处 `t, _ := functiontool.New(...)` 与技能目录写盘错误改为向上返回。
- 路径边界：`safeWorkspacePath` 增加 `EvalSymlinks` 双侧校验；尚不存在的写目标通过最深的已存在祖先校验。
- MCP 生命周期：`Toolset` 改为锁内创建（消除 check-then-act 重复连接）；`trackedTransport` 记录连接，`Disconnect` 真正关闭会话与 stdio 子进程。
- 其余：HTML 导出标题转义；Agent/远程 Agent 导出文件名走 `sanitizeExportName`；A2A `Serve` 失败时移除失效条目；`MCPService.Connect` 未知 ID 返回错误。
- 运行期 panic 恢复：recover 抽为 `recoverRun`，panic 转为 `error` + `done` 事件，界面不会卡在运行态。

**P2 — 前端体验与性能**
- 后台会话完成事件此前不可达（done/error 处理位于 `sessionId === activeID` 过滤之后），已前置：后台会话会清 spinner、刷新列表并弹提示。
- 会话切换竞态：历史加载加 `cancelled` 守卫。
- 渲染性能：`MessageBubble`/`ToolCallCard` 改 `memo`，回调 `useCallback` 稳定化，`canEdit` 的 O(n²) 扫描改为 `computeEditFlags` 预计算。
- 工具耗时：原先读取从未赋值的 `m.durationMs`，改用父组件传入的配对结果。
- 滚动：仅在读者贴底时自动滚动，并按 `requestAnimationFrame` 合并。
- 空态/错误态：设置页加载失败显示重试；Skills 空态；Chat 有供应商但无会话的引导；新增 `ErrorBoundary`。
- 首屏体积：除 Chat 外全部页面 `lazy` + `manualChunks` 拆 vendor，单 chunk 626KB → 首屏约 377KB（gzip 约 117KB）。
- `autoSpeak` 改为 ref 读取，消除流订阅中的过期闭包。
- 依赖清理：移除未使用的 `clsx`/`tailwind-merge`/`shadcn`；sonner 直接读 `<html class="dark">` 而非依赖无 Provider 的 `next-themes`；`@wailsio/runtime` 锁定 `3.0.0-beta.20`；补 `engines.node >= 20.19`。

**P3 — 工程化**
- 新增 `.github/workflows/ci.yml`：后端 `vet + test + -race`，前端 `tsc + build`；先构建前端以满足 `//go:embed all:frontend/dist`。
- `scripts/verify.sh`：先构建前端再 vet；`-race` 覆盖 `./internal/...`；不再吞掉 `wails3 build` 的错误输出。
- 版本号统一为 `0.3.0`（`main.go` / `build/config.yml` / `Info.plist` / `package.json`）。
- README 由 881 行降至约 200 行，迭代记录迁至本文件；修正 Go/Node 版本要求与日志轮转等不准确描述。

**测试证据**：`go test ./internal/...` 全绿；`go test -race ./internal/...` 全绿；`npx tsc --noEmit` 通过；`npm run build` 分包成功。

本项目的 UI 通过「浏览器 + 数据桩」方式做视觉回归：前端以 `wails3 dev` 提供的页面 + 注入的 IPC mock 数据桩，在真实浏览器中逐页走查截图（对话/Agents/团队/Skills/MCP/模型/设置、深浅主题、空态、对话框）。

第一轮视觉测试发现并已修复的缺陷：

| 缺陷 | 修复 |
| --- | --- |
| 助手消息 Markdown 原样显示（`##`/`**`/代码围栏） | 引入 react-markdown + remark-gfm，新增 `MessageMarkdown` 组件：标题/列表/表格/引用全量渲染 |
| 代码块无高亮容器、无法复制 | 代码块渲染为带语言标签和「复制」按钮的面板（对齐 Codex 体验） |
| 绑定调用失败时 toast 只显示 "Error" | `errText()` 统一透传 Wails RuntimeError.message / cause 到所有提示 |
| 页面数据加载失败静默空白 | 会话/配置加载失败均有可见 toast |
| 无消息复制操作 | 用户/最后一条助手消息悬停显示复制按钮 |
| 硬编码深色主题 | 侧栏底部主题切换（浅/深），localStorage 持久化 |
| 首次运行无引导 | 无供应商时对话页显示三步引导卡片（可点击跳转） |
| 会话多时难以查找 | 会话列表新增搜索框；标题完整提示（title） |
| 输入框无快捷键 | ⌘N 新对话、⌘K 聚焦输入框（占位符内提示） |

### 参考 Codex / Claude Desktop / WorkBuddy 的设计对齐

已实施：Markdown + 代码块复制、消息级复制、深浅主题、首次运行引导、会话搜索、快捷键、工具调用折叠面板、流式打字指示器。

### 第九十二轮迭代：MCP 授权等待状态徽章（已实施）

- MCP 服务器卡片在 OAuth 授权等待期间显示「等待浏览器授权…」琥珀徽章，连接完成后自动清除
- 与 mcp:auth-pending 事件/toast 提示配套，消除授权期间的静默等待感

验证：tsc/build 零错误 + 全量回归。

### 第九十三轮迭代：审计查看器落地（第五十二轮声明闭环，已实施）

- 诊断卡片新增「查看工具审计日志」：读取 tools-audit.jsonl 尾部 200 行弹窗展示（与第五十二轮声明对齐，此前该声明未落地——本轮补齐并诚实记录）
- `RecentToolAudit` RPC 复用统一 readTail 尾部语义；应用日志/审计日志弹窗标题区分

测试：审计尾部语义（缺失占位 / 尾 N 行）e2e + 全量回归。

### 第九十四轮迭代：归档事件纳入审计（已实施）

- ArchiveSession 的归档/恢复两个方向均写入审计 JSONL（session_archived / session_unarchived），与生命周期审计保持一致

测试：审计含归档/恢复两阶段断言 e2e + 全量回归。

### 第八十九轮迭代：关键词路径文件引用（已实施）

- 关键词检索结果同样输出「文件: …」引用行（与语义路径一致），未配置 Embedding 时预览体验不打折

验证：双路径检索回归 + 构建零错误。

### 第八十七轮迭代：Markdown 导出补 HITL 确认记录（已实施）

- Markdown 导出此前缺失 HITL 确认轨迹（仅 HTML 有）：补齐确认请求（🛡️ + 工具名/参数）与决策（批准/拒绝）行，与 HTML 导出一致

测试：HITL 全流程导出断言（确认请求 + 批准决策均入档）e2e + 全量回归。

### 第八十五轮迭代：供应商测试延迟显示（已实施）

- TestProvider 返回耗时（毫秒），模型配置页测试成功提示附「· NNNms」，连通质量一目了然

验证：tsc/build 零错误 + 全量回归。

### 第八十四轮迭代：批量导出全部会话（已实施）

- 诊断卡片新增「批量导出全部会话」：逐会话写入工作区 exports/（Markdown），返回导出数量

测试：批量导出计数 e2e + 全量回归。

### 第八十三轮迭代：导入 Agent 后引导补全（已实施）

- 导入分享的 Agent 后自动打开编辑器，引导用户选择供应商与模型（分享文件不含密钥与运行环境）

验证：tsc/build 零错误。

### 第八十二轮迭代：快捷键与命令帮助面板（已实施）

- 侧栏底部 ？ 按钮弹出「快捷键与命令」清单（⌘N/⌘K/Enter/Shift+Enter 与斜杠命令说明），提升可发现性

验证：tsc/build 零错误 + 全量回归。

### 第八十轮迭代：会话归档（已实施）

- 会话悬停新增归档按钮：归档后从主列表隐藏，底部「已归档会话（N）」开关可展开查看并恢复
- 后端 `ChatSession.Archived` + `ArchiveSession` RPC；归档会话数据完整保留，随时恢复

验证：tsc/build 零错误 + 全量回归。

### 第七十九轮迭代：知识文件列表嵌套支持（已实施）

- 设置页知识文件列表同步支持嵌套目录（相对路径 `docs/sub/deep.md` 形式展示），与索引/检索路径键一致；删除同样走遍历防护

测试：嵌套文件列于清单 e2e 扩展 + 全量回归。

### 第九十六轮补充：视觉桩哈希对齐（已实施）

- 视觉桩 handler 键与生成绑定的权威 methodID 全面对齐（此前部分键为错误 FNV 变体导致 ListKnowledgeFiles/StorageStats/MemoryStats 不渲染）
- 设置页走查全部通过：知识文件列表/检索调试器/索引统计/存储占用/长期记忆卡片均正常渲染

### 第九十五轮迭代：斜杠建议条视觉验证（已实施）

- 浏览器桩走查：输入 `/` 实时渲染 4 条命令建议（点击填入），确认卡片交互、时间戳、审计开关等既有验证项全部保持通过
- 至此第五十~五十七轮 UI 改动全部具备「构建 + e2e + 视觉」三重验证

验证：tsc/build 零错误 + 视觉走查通过。

### 第七十八轮迭代：知识库嵌套目录支持（已实施）

- 关键词与语义两条检索路径均改为 `filepath.WalkDir` 遍历：`knowledge/子目录/文件` 同样可索引可检索
- 向量索引行以相对路径为键，嵌套文件的分块引用与预览路径一致

测试：嵌套文件关键词命中 + 语义索引含相对路径引用 e2e + 全量回归。

### 第七十六轮迭代：会话生命周期审计（已实施）

- 会话创建 / 重命名 / 分叉 / 删除均写入审计 JSONL（phase=session_*），与工具调用审计同文件同开关

测试：生命周期四阶段记录断言 e2e + 全量回归。

### 第七十五轮迭代：导出标注分叉链路（已实施）

- Markdown / HTML 导出头部在分叉会话中追加「分叉自: <原会话标题>」，导出物可追溯分支来源

测试：分叉后导出断言「分叉自」存在 e2e + 全量回归。

### 第七十四轮迭代：A2A Card 暴露 Agent 技能（已实施）

- 发布的 Agent Card 通过 ADK `BuildAgentSkills` 携带技能清单（源自 Agent 挂载的工具/Skills），远程客户端可感知能力面

验证：loopback e2e 回归 + 打包零错误。

### 第七十三轮迭代：并发嵌入加速（已实施）

- 知识库索引的嵌入调用改为 4 worker 并行（网络密集型；单文件多批次分片调度），大语料索引耗时近似降为 1/4
- DB 写入保持顺序（单连接约束不变），嵌入失败即中止并回退提示

测试：全部语义/索引/回退 e2e 在并发路径下通过 + `-race` + 打包零错误。

### 第七十二轮迭代：优雅关停与代理覆盖测试（已实施）

- `OnShutdown` 钩子：应用退出时 A2A 服务全部下线（StopAll），避免残留监听
- 代理覆盖契约补测：gemini SDK / a2a-go 客户端的兜底 `&http.Client{}` 亦经 DefaultTransport 受全局代理管辖（回环转发验证）

测试：空客户端代理穿透 e2e + 全量 `-race` + 打包零错误。

### 第七十一轮迭代：HTML 工具折叠与知识文件排序（已实施）

- HTML 导出：工具调用/结果改为 `<details>` 折叠块（默认收起，点开看参数/结果 JSON），内容经 HTML 转义防注入
- 知识库文件列表按修改时间倒序（最新优先）

测试：HTML 含 details/summary 断言 + 文件排序 e2e + 全量回归。

### 第七十轮迭代：会话列表最后消息预览（已实施）

- `ChatSession.Preview`：每轮完成后记录最新助手回复的尾部（80 字符），侧栏会话条目第二行显示，一眼掌握各会话进展

测试：预览写入与随轮次刷新 e2e + 全量回归。

### 第六十九轮迭代：审计日志轮转（已实施）

- 审计文件达到 10MB 时自动轮转为 `.old`，新记录写入新文件，防止无限增长；轮转判据为「追加后大小」提前判断

测试：预填充至临界 + 工具调用触发轮转 e2e（.old 生成 + 新文件继续写入）+ 全量回归。

### 第六十八轮迭代：HTML 导出统计表（已实施）

- HTML 导出尾部追加「用量统计」：用户消息/助手轮次/工具调用/总 tokens，以及按日 tokens 与成本估算表（复用 SessionStats 聚合与模型价格）

测试：HTML 导出断言扩展（用量统计表存在）+ 全量回归。

### 第六十七轮迭代：知识库文件点击预览（已实施）

- 设置页知识文件列表：点击文件名弹出 ArtifactPreview（Markdown/文本/图片等渲染），复用既有产物预览管道（工作区边界校验、大小上限）

验证：tsc/build 零错误 + 全量回归。

### 第六十六轮迭代：HITL 确认决策纳入审计（已实施）

- ConfirmToolCall 的批准/拒绝决策写入工具审计 JSONL（phase=confirm，含 callID 与 decision），合规链路闭环：调用 → 等待 → 决策 → 结果全留痕

测试：审计记录含 confirm 决策断言 e2e + 全量回归。

### 第六十五轮迭代：托盘左键快速切换窗口（已实施）

- 托盘图标接入 `AttachWindow` smart defaults：**左键**直接显示/隐藏主窗口，**右键**弹出菜单（显示隐藏/退出）
- 窗口位置跟随托盘偏移（WindowOffset 8），唤起即聚焦

验证：`./scripts/verify.sh` ALL GREEN + 打包启动烟雾。

### 第六十四轮迭代：一键验证脚本（已实施）

- `scripts/verify.sh`：整合 go vet / 全量测试 / `-race` / 前端 tsc+构建 / `wails3 build` 的单一质量门，`VERIFY: ALL GREEN` 即通过
- AGENTS.md 已引用，供后续任意迭代快速自检

### 第六十三轮迭代：周期性视觉回归（已实施）

浏览器视觉回归走查（截图 + DOM 断言）确认第五十~五十七轮全部前端改动：消息时间戳、审计开关、确认卡片交互（批准后状态切换 + 工具完成 + 模型续答）、设置页新卡片渲染与深色主题一致性。

测试：归档/取消标志持久化 e2e + 全量回归 + 打包零错误。

### 第六十二轮迭代：自动存入长期记忆（已实施）

- 设置新增「自动存入长期记忆」开关：每轮对话完成后 best-effort 将会话写入记忆（含工具调用摘要），失败静默不影响对话
- 与既有 🧠 手动存入并存；记忆计数实时可查（第六十轮卡片）

测试：manual/auto 双子测试（手动 RememberSession 与设置驱动自动摄取两种路径均产生记忆条目）+ 全量回归。

### 第六十轮迭代：长期记忆管理 UI（已实施）

- 设置页新增「长期记忆」卡片：显示已存储条目数，「清空记忆」带确认对话框（复用第七轮 Count/Clear 后端）
- 补齐记忆能力的可观测与生命周期管理

测试：记忆写入 → 统计 → 清空 → 统计归零 e2e + 全量回归。

### 第五十九轮迭代：HTML 导出深色与打印适配（已实施）

- HTML 导出模板新增 `prefers-color-scheme: dark` 深色样式与 `@media print` 打印优化，跟随系统外观或直接打印分发

验证：HTML 导出 e2e 回归 + 构建零错误。

### 第五十八轮迭代：A2A 服务多 Agent 并发发布（已实施）

- A2AService 重构为多服务器模型：可同时发布多个本地 Agent（各自独立端口与 Agent Card，`Status` 返回全部运行条目）
- Start 按 Agent 幂等（重复发布返回现有状态）；`StopAgent` 单独下线某个 Agent；`StopAll` 兜底
- 设置页 A2A 卡片改为运行列表（逐项停止）；发布按钮常驻，支持多 Agent 同时对外服务

测试：发布/幂等/停止清空 e2e 更新 + 全量 `-race` + 打包零错误。

### 第五十七轮迭代：输入框斜杠命令（已实施）

- 输入框支持本地斜杠命令：`/export`（导出 Markdown）、`/exporthtml`（导出 HTML）、`/stats`（会话统计）、`/new`（新建对话）
- 未知命令提示可用列表；不触发模型调用

验证：tsc/build 零错误 + 全量回归。

### 第五十六轮迭代：知识库文件管理（已实施）

- 设置页语义检索卡片列出 `knowledge/` 目录文件（名称 / 大小 / 修改时间），支持删除（确认对话框 + 目录穿越防护）
- 删除文件后向量索引自动同步移除该文件分块（既有 mtime 感知机制）

测试：列表 / 缺目录空态 / 遍历拒绝 / 删除生效 e2e + 全量回归。

### 第五十五轮迭代：远程 Agent 分享导出/导入（已实施）

### 第五十四轮迭代：知识库检索调试器（已实施）

- 设置页语义检索卡片新增「测试检索」：输入检索词即时执行底层 searchHybrid（含增量索引与混合评分），结果原文回显，调参不再需要进对话试探
- 覆盖双路径：Embedding 已配置走语义混合，未配置走关键词回退——两者输出均可预览

测试：底层 TestKnowledgeSearch 直通 e2e（复用语义/关键词双路径）+ 全量回归。

### 第五十三轮迭代：会话内容搜索（已实施）

- 侧栏搜索升级：输入关键词后按 250ms 防抖调用后端 `SearchSessions`，匹配**标题或历史消息内容**（对 events 负载的 LIKE 探测，CJK 原生匹配）
- 空查询回落到完整列表（置顶优先 + 最近活跃排序）；不匹配则空态

测试：内容命中 / 空查询全量 / 不匹配为空 e2e + 全量回归。

### 第五十二轮迭代：会话导出为自包含 HTML（已实施）

- 对话顶栏新增「导出为自包含 HTML」（FileCode2 图标）：完整会话经 gomarkdown 渲染为带内联样式的单文件 HTML（标题/二级标题/引用块/表格/代码样式），可直接在浏览器阅读或分享
- 与既有 Markdown 导出并存（HTML 走相同事件渲染管道，含工具调用与 HITL 确认记录）

测试：HTML 导出 e2e（DOCTYPE/标题/渲染正文/文档闭合）+ 全量 `-race`。

### 第五十一轮迭代：存储占用显示与会话库压缩（已实施）

- 诊断卡片显示会话库 / 知识索引的磁盘占用；「压缩会话库」按钮执行 VACUUM，回收修剪/重新生成历史后产生的空页
- `AppService.StorageStats` / `VacuumSessions` RPC，数据库位置经 SetStoragePaths 注入

测试：真实文件尺寸统计、缺失文件 -1 语义、VACUUM 后数据库仍可用 e2e + 全量回归。

### 第五十轮迭代：工具调用耗时显示（已实施）

- 每个工具结果卡片头部显示调用耗时（如 `1.2s`）：前端按工具名 FIFO 配对 call/result 时间戳计算，流式与历史回放均适用
- 配对逻辑同时校正 runningTools 判定（同名并发调用按序配对）

验证：tsc/build 零错误 + 全量回归。

### 第四十九轮迭代：团队成员时间线（已实施）

旧路线图「团队运行时间线」落地：

- `SessionStats.Authors`：按对话参与者（团队成员/远程 Agent）聚合轮次、工具调用与 tokens（排除用户自身）
- 统计弹层在多参与者会话中显示「成员时间线」表（成员 / 轮次 / 工具调用 / tokens），单人会话自动隐藏

测试：按作者聚合断言（含 sanitize 后名称与轮次计数）+ 全量回归。

### 第四十八轮迭代：消息时间戳悬停显示（已实施）

- 用户气泡悬停显示发送时间（本地 HH:MM）；助手回复作者旁常显时间
- 历史消息透传存储时间戳，流式消息在到达时本地打点

验证：tsc/build 零错误 + 全量回归。

### 第四十七轮迭代：知识库索引重建（已实施）

- 设置页语义检索卡片新增「重建索引」：清空本地向量缓存，下次检索自动从工作区文件重新嵌入（换模型/换文件批量调整后的管理闭环）
- ConfigService.ResetKnowledgeIndex → KnowledgeIndex.Reset（全表清空 + 互斥保护）

测试：重置清空 + 自动重建 e2e + 全量回归。

### 第四十六轮迭代：代理覆盖确认与保存校验（已实施）

- **覆盖确认**：核验 gemini SDK（`&http.Client{}`）、a2a-go JSONRPC 客户端（同）与 MCP streamable（`http.DefaultClient`）均回落到 `http.DefaultTransport` —— 全局代理设置对所有出站流量生效，无需逐客户端接线
- **保存即校验**：SaveSettings 校验代理地址（scheme http/https + host），格式错误立即报错，不再等到重启后

测试：合法/非法代理保存 e2e + 全量回归。

### 第四十五轮迭代：模型请求瞬态重试（已实施）

- OpenAI 兼容与 Anthropic 适配器共享 `postWithRetry`：连接失败与 502/503/504 网关错误按固定退避（0.5s/1.5s）自动重试最多 2 次
- **仅在未消费任何响应字节时重试**——流式输出绝不重复；4xx（鉴权/参数错误）不重试、立即报错
- 已知边界补充：重试针对瞬态网关故障，模型返回的业务错误不重试

测试：503×2 后恢复 e2e（恰 3 次请求）+ 既有 401 语义回归 + 全量 `-race`。

### 第四十四轮迭代：MCP OAuth 授权等待提示（已实施）

- Manager 新增 `SetAuthPendingHook`：OAuth 授权码流程等待用户在浏览器操作时，向 Wails 事件总线发 `mcp:auth-pending`（携带服务器名与授权地址）
- MCP 页监听该事件并 toast 提示「请在浏览器中完成 MCP 授权…完成后连接自动继续」，消除等待期间的静默困惑
- 钩子经 `buildTransport` 贯穿至官方 OAuth handler 的 Fetcher（打开浏览器前触发）

测试：OAuth 流程 e2e 扩展（钩子收到 /authorize 地址断言）+ 全量回归。

### 第四十三轮迭代：代理覆盖契约固化（已实施）

- 契约测试钉住：无显式 HTTPClient 的 MCP streamable 传输（`http.DefaultClient` → `http.DefaultTransport`）必须经过全局代理——回环转发桩验证流量真实穿过代理服务器
- 顺带确认 gemini SDK / 模型适配器 / 向量客户端同路径覆盖；代理桩需直连转发（避免自环）

测试：代理覆盖 e2e（流量真实经过代理服务器）+ 回归。

### 第四十二轮迭代：全量代码审查（已实施）

双子代理（Standards + Spec）并行审查 35 个提交：修复 tool_result 双发 bug、预算豁免确认应答、错误中文化、semantic.go 清理、README 章节排序（22/29 节补齐）。

### 第四十一轮迭代：长会话自动上下文压缩（已实施）

依据 ADK `session/compaction` 官方能力（滑动窗口 + 尾部保留）：

- 设置页「长会话上下文压缩」：**滑动窗口间隔**（每 N 轮把更早对话压缩为 LLM 摘要，携带 1 轮重叠保证连续性）与 **Token 阈值触发**（提示超阈值时压缩其余、保留尾部）
- 摘要用会话根 Agent 自己的模型（ADK 默认行为）；远程 A2A 会话无本地模型、自动跳过
- 两者可同时启用；均为 0 关闭（零破坏）

测试：压缩 e2e（开启后出现额外摘要模型调用 + 回答完整；关闭后恰好一次调用）+ 全量回归。

### 第四十轮迭代：危险操作确认（已实施）

删除会话 / Agent / 团队 / 远程 Agent / 供应商 / MCP 服务器 / Skill 一律弹出确认对话框，防止误删不可恢复的数据。

验证：tsc/build 零错误 + 全量回归。

### 第三十九轮迭代：知识库检索结果可预览（已实施）

- search_knowledge 结果附带 `refs`（工作区相对文件路径，去重排序）；ToolCallCard 路径识别扩展 `refs` 键，检索结果可直接点开「预览」查看原文（Markdown/文本渲染）

测试：语义/关键词检索回归 + 前端 tsc/build 零错误。

### 第三十八轮迭代：应用内日志查看器（已实施）

- 设置页「诊断」卡片：一键查看最近 200 行应用日志（logs/app.log 尾部），便于故障自查与反馈
- 日志缺失时给出友好占位；尾部窗口上限 300 行防大文件读取

测试：缺失占位 / 尾部窗口语义 e2e + 全量回归。

### 第三十七轮迭代：HTTP 代理支持（已实施）

- 设置页新增「网络」卡片：全局出站代理（`http://host:port`），应用于模型请求、知识库向量、MCP 远程连接与 A2A（共享 http.DefaultTransport，启动时一次性注入）
- 保存并重启后生效；非法地址仅记日志、回退直连；留空恢复直连

测试：代理注入/回环命中（请求经代理服务器转发）/重置直连/非法地址拒绝 单测 + 全量回归。

### 第三十六轮迭代：系统提示词模板变量（已实施）

- 系统提示词支持 `{{date}}` / `{{time}}` / `{{datetime}}` / `{{workspace}}` 四个变量，每次构建 Agent 时解析（应用长驻也能保持日期新鲜）
- 未匹配的 `{{...}}` 原样保留，不破坏既有提示词

测试：变量解析/残留检查/纯文本不变 单测 + 全量回归。

### 第三十五轮迭代：工具调用审计日志（已实施）

- **JSONL 审计追踪**：设置开启后，每次工具调用/结果（含会话 ID、作者、工具名、参数/结果负载、错误字段）以 JSON 行追加到 `logs/tools-audit.jsonl`（0640）
- 写入失败静默忽略——审计绝不阻断对话；开关默认关闭，按需启用
- 设置页「外观」卡片内新增开关

测试：审计 e2e（call/result 记录完整、会话 ID 关联）+ 全量 `-race` + 打包零错误。

### 第三十四轮迭代：会话 Token 预算护栏（已实施）

第十九轮运行护栏的累计维度补充：

- **MaxSessionTokens**（Agent 级，0=不限制）：会话累计 tokens 超出预算后，当前轮正常完成并提示，**新用户消息被拒绝**（ConfirmToolCall 等待中的确认不受影响，仍可应答）
- Agent 编辑器新增预算输入框

测试：跨预算轮完成 + 警告、后续轮拒发（模型零调用）e2e + 全量回归。

### 第三十三轮迭代：A2A 服务开机自启（已实施）

第十六轮 A2A 服务端「重启需手动启动」的补全：

- 设置新增 `a2aAutoStart` / `a2aAgentId` / `a2aPort`；应用启动时若启用则自动发布所选 Agent（端口 0 自动分配），失败仅记日志不阻塞启动
- 设置页 A2A 卡片新增「开机自动启动」开关，保存当前卡片选择的 Agent 与端口

测试：设置持久化往返 e2e + 全量回归 + 打包零错误。

### 第三十二轮迭代：会话导出统计头（已实施）

导出的 Markdown 头部新增统计行（用户消息 / 回复轮次 / 工具调用 / 总 tokens），来自既存 SessionStats 聚合。

测试：导出内容包含统计头 e2e + 全量回归。

### 第三十一轮迭代：AGENTS.md 工程指南（已实施）

为本仓库沉淀面向编码代理的工程地图：常用命令、目录地图、关键机制（HITL 协议、会话库并发约束、A2A 双向栈、绑定哈希、假模型服务器要求）、测试与迭代记录约定、已知边界 —— 降低后续任何自动化/人工迭代的上下文成本。

### 第三十轮迭代：知识库索引状态可观测（已实施）

设置页语义检索卡片显示索引状态：已索引文件数 / 分块数 / 嵌入模型，便于确认向量检索是否就绪。

测试：索引统计单测（文件/分块/模型计数 + nil 索引零值）+ 全量回归 + 打包零错误。

### 第二十九轮迭代：发布就绪检查与总览刷新（已实施）

- README 功能总览/目录结构对齐全部新能力；新增「发布就绪检查」章节（后端全量 + -race、tsc/build、wails3 打包、启动烟雾、视觉回归覆盖）
- 顺带修复四十一轮累积的章节顺序与笔误

### 第二十八轮迭代：后台会话完成提示（已实施）

多会话并行（第十二轮）的收尾体验：切换到其他会话时，后台会话完成/出错会以 toast 提示「『会话标题』已完成回复 / 本轮出错」，避免用户错过并行任务结果。

验证：tsc/build 零错误 + 全量回归。

### 第二十七轮迭代：Agent 单个导出/导入（分享，已实施）

- **导出**：Agent 卡片一键导出为版本化分享文件（`exports/agents/<名称>.agent.json`），不含任何密钥
- **导入**：顶栏「导入 Agent」读取分享文件，自动分配全新 ID；名称冲突自动加（2）（3）… 后缀；版本校验与解析错误友好提示
- 供应商引用（providerId）随文件携带，导入后需在模型配置中确认对应供应商存在

测试：导出/导入往返（新 ID、冲突后缀递增、未知 Agent/坏 JSON/版本拒绝）e2e + 全量回归。

### 第二十六轮迭代：Skills 工作区批量导入（已实施）

- **扫描导入**：Skills 页「从工作区导入」一键扫描 `<工作区>/skills/*/SKILL.md`（agentskills.io 布局），解析 YAML frontmatter 的 name/description；无 frontmatter 时回退一级标题命名
- **去重幂等**：按名称（大小写不敏感）跳过已存在技能，重复导入零副作用
- 未配置工作区/目录缺失均给出明确提示

测试：导入 e2e（frontmatter 解析/标题回退/大小写去重/非 SKILL 文件忽略/幂等重导入）+ 全量回归。

### 第二十五轮迭代：上下文高水位提醒（已实施）

第二十三轮指示器的行为延伸：

- 上下文占用 ≥80% 时：顶栏出现红色警示徽章；消息区顶部出现可关闭的琥珀色提醒条，附「导出 Markdown」与「知道了」快捷操作
- 切换会话自动重置关闭状态

验证：tsc/build 零错误 + 全量回归。

### 第二十四轮迭代：会话置顶（已实施）

- `ChatSession.Pinned` + `PinSession` RPC：侧栏悬停置顶/取消；置顶会话固定在列表最前（其余按最近活跃排序——顺带把列表从"创建序"升级为"活跃序"）
- 置顶状态以图钉图标常显

测试：置顶排序/取消还原 e2e + 全量回归。

### 第二十三轮迭代：上下文窗口使用指示器（已实施）

利用模型级元数据（contextWindow，见「模型接口拉取」轮）：

- 对话顶栏新增**上下文窗口占用进度条**：已用 tokens / 上下文窗口，百分比着色（<50% 绿、50-80% 琥珀、>80% 红），悬停显示具体数值
- 与 tokens/成本徽章并列展示；未配置窗口大小或无用量时不显示

验证：视觉回归桩注入 done 用量事件（200k/400k）→ 50% 琥珀进度条正确渲染 + tsc/build 零错误。

### 第二十二轮迭代：会话统计按日成本汇总（已实施）

- **SessionStats 扩展**：`daily[]` 按本地日历日聚合输入/输出/总 tokens，并用会话目标 Agent **当前模型价格**（¥/1M）估算每日成本；跨模型历史按现价近似（已在 UI 注明）
- 统计弹层新增「按日明细」表（日期 / 输入 / 输出 / 估算成本）

测试：日聚合 + 成本计算 e2e（1M 输入 / 0.5M 输出 → 精确 ¥6.00 校验，误差 <0.0001）+ 全量回归 + 打包零错误。

### 第二十一轮迭代：MCP OAuth 令牌持久化（已实施）

第十八轮 OAuth 的补全——**重启后免重复授权**：

- **令牌缓存**：`Config.MCPTokens`（config.json 0600）按服务器缓存访问/刷新令牌与 token endpoint；OAuth handler 通过官方 `NewTokenSource`/`InitialTokenSource` 钩子读写缓存
- **静默刷新**：缓存含刷新令牌 + token endpoint 时，过期令牌自动静默刷新（无需浏览器）；浏览器授权仅在首次或刷新令牌彻底失效时触发
- **导出安全**：令牌缓存永不导出（含密钥导出也不含——属机器本地秘密，丢失可重新授权恢复）

测试：重启模拟 e2e（首次完整授权 → 断言缓存写入含 TokenURL → 新 transport 免浏览器直连成功，浏览器被调用即测试失败）+ 脱敏导出零泄漏扩展 + 全量 `-race`。

### 第二十轮迭代：分叉关系可视化（已实施）

第十五轮会话分叉的 UX 补全：

- `ChatSession.ForkedFrom` 记录分叉来源会话；侧栏分叉会话标题前显示 GitFork 图标，悬停提示「分叉自『原会话标题』」
- 分叉链路可追溯（多级分叉依次回溯）

测试：分叉元数据断言扩展（ForkedFrom 正确指向原会话）+ 全量回归。

### 第十九轮迭代：Agent 运行护栏（超时与工具调用上限，已实施）

针对 Agent 失控循环/挂起的安全护栏（ADK RunConfig 无此能力，应用层实现）：

- **单轮最长运行秒数**（`maxTurnSeconds`）：超时自动中止本轮并明确提示（区别于手动取消）
- **单轮工具调用上限**（`maxToolCalls`）：超出后停止本轮、保留已产生的结果与统计，防止死循环烧 token
- 两者默认 0 = 不限制（零破坏）；Agent 编辑器新增数值输入；Teams 暂按成员各自配置不叠加（负责人级后续迭代）

测试：上限触发（第 3 次调用被拦 + done 收尾）、超时中止（1s 上限 vs 3s 挂起模型）、零值保持旧行为 ×3 + 全量 `-race` + 打包构建零错误。

### 第十八轮迭代：MCP 远程服务器认证（Bearer / API Key / OAuth 2.1，已实施）

路线图「MCP OAuth」落地，依据 MCP 官方授权规范与 go-sdk v1.7 官方 `auth.AuthorizationCodeHandler`（授权码 + PKCE）：

- **三种认证方式**（Streamable HTTP 服务器，按服务器配置选择）：
  - **Bearer Token**：静态令牌，自动加 `Authorization: Bearer …`
  - **API Key**：自定义请求头（默认 `X-API-Key`）
  - **OAuth 2.1**：预注册 Client ID；连接时按 MCP 规范完成发现（WWW-Authenticate → 受保护资源元数据 → 授权服务器元数据）→ 打开系统浏览器完成授权 → 本地回环地址接收回调（授权码 + PKCE S256）→ 令牌交换，401/403 自动触发授权并重试
- **顺带修复**：配置里的自定义 `headers` 字段此前从未被应用到传输层，现已生效
- **安全**：脱敏导出同时剥离 MCP 认证令牌；回调仅绑定本机回环地址；授权等待 5 分钟超时

测试：进程内假授权服务器 + 受保护真实 go-sdk MCP 服务器的全流程 e2e ×4（Bearer / API Key / OAuth 2.1 完整流 / 无凭据被拒）+ 全量 `-race` + 打包构建零错误。

### 第十七轮迭代：浏览器视觉回归基建（已实施）

为本次会话累计的前端改动建立**可复用的浏览器视觉回归**（`frontend/stub/`，`npm run stub`）：

- **原理**：Wails v3 runtime 官方 `setTransport` 注入点替换 IPC 传输层为数据桩；真实 React UI 在普通浏览器中完整运行，`window.__emit` 可注入 `chat:stream` 事件流
- **验证走查（截图 + DOM）**：工具执行确认卡片（pending → 已批准 → 工具完成 → 模型续答全流程交互）、Agents 页远程 Agent 管理区、团队页混合拓扑（🌐 远程成员）、设置页语义检索/备份迁移/A2A 服务三张新卡片、深色主题渲染
- 修复过程中确认了调用描述符结构（`methodID`/`args` 槽位），桩与真实 IPC 语义一致

### 第十六轮迭代：A2A 服务端模式（已实施）

与第十轮（作为客户端连接远程 Agent）对称——本应用现在也能**对外提供 A2A 服务**：

- **发布本地 Agent**：设置页「A2A 服务」选择 Agent 与端口一键启动（端口 0 自动分配；仅监听本机回环地址）；启动后任意 A2A 客户端可发现 Agent Card 并以 JSON-RPC 调用
- **官方服务端栈**：与 ADK launcher 同一配方（`a2asrv.NewStaticAgentCardHandler + adka2a.NewExecutor`），远程调用各自获得服务端侧会话，经同一 ADK runner 执行
- **运行态管理**：启动/停止/状态查询 RPC；重复启动幂等；停止后端口释放；服务为运行态（重启应用后需手动重新启动，避免意外常驻暴露）

测试：loopback e2e（发布 → 卡片发现 → 本应用远程客户端全栈回环对话 → 幂等启动 → 停止状态清理）+ 全量 `-race` + 打包构建零错误。

### 第十五轮迭代：会话分叉（已实施）

路线图「消息分支树」的务实落地（ADK Branch 字段定位是子代理可见性隔离，非用户会话分支，故采用「分叉即新会话」模型）：

- **ForkSession**：一键把整段会话的 ADK 事件完整复制为独立新会话（标题自动加「（分支）」），原时间线永不变动；分叉后可随意 重新生成/编辑重发/人工确认，两边互不干扰
- **实现稳健性**：事件复制用 `pragma_table_info` 动态列名构建 SQL，ADK 事件表结构演进不受影响；事件主键含 session_id，跨会话复制天然无冲突
- **入口**：侧栏会话悬停「分叉」按钮（GitFork 图标），分叉后直接跳转到新分支

测试：分叉 e2e（历史一致复制、元数据正确、分叉内回复只落在分支、原会话时间线零变动、未知会话报错）+ 全量 `-race` + 打包构建零错误。

### 第十四轮迭代：系统托盘与全局快捷键（已实施）

路线图「系统托盘与全局快捷键：类 WorkBuddy 的常驻入口」落地，依据 Wails v3 官方 API（`SystemTrayManager` / `GlobalShortcutManager`）与官方示例：

- **菜单栏托盘**：常驻 🧭 图标（应用图标嵌入二进制）；左键菜单提供「显示/隐藏 WorkBuddy」「退出」；tooltip 提示应用名
- **全局快捷键**：`⌘⇧Space` 全局唤起/隐藏主窗口（无论应用是否前台）；注册冲突（被其他应用占用）仅记录日志、不阻塞启动
- 关闭窗口后从托盘可再次唤起，配合 `ApplicationShouldTerminateAfterLastWindowClosed` 保持单窗口常驻模式

验证：生产构建零错误；启动烟雾确认托盘/快捷键注册无报错、进程健康；全量测试回归。

### 第十三轮迭代：远程 Agent 进团队（已实施）

第十轮 A2A 的自然延伸——远程 Agent 现在可以作为**团队成员**参与协作：

- **混合团队**：团队编辑器新增「远程成员（A2A）」多选区；协调员可把任务 transfer 给远程成员，远程回复经 A2A 流回本地会话历史
- **负责人始终本地**：远程成员仅作为子代理参与（ADK agent transfer 路由），负责人/协调员由本地 Agent 担任
- 删除远程 Agent 时自动从所有团队摘除；拓扑图以 🌐 标注远程成员
- 导出 `SanitizeAgentName`：子代理规范名可被预测（协调员提示词内列出，模型照抄调用）

测试：混合团队全链路 e2e（自动协调员 fake 模型发起 `transfer_to_agent` → A2A 远程成员执行 → 回复落入本地 SQLite 历史）+ 全量 `-race` + 打包构建零错误。

### 第十二轮迭代：多会话并行（已实施）

路线图「多窗口/多会话并行」落地：

- **运行状态按会话隔离**：前端全局 `running` 布尔改为按会话的运行表——任一会话生成中不再阻塞其他会话发送/重新生成/确认操作；侧栏为运行中的会话显示旋转指示器；切换会话时通过 IsRunning 校准
- **并行会话远程图标**：侧栏区分 Agent / 团队 / 远程 A2A 会话图标
- **关键修复（并发数据库）**：并行写入暴露出会话库 SQLITE_BUSY——WAL 的延迟事务快照冲突会绕过 busy_timeout；会话库改为单连接串行化（桌面场景足够），主库与测试库同步
- 测试：双会话并发 e2e（独立回复、无串扰、完成后无残留运行态）+ 全量 `-race`

### 第十一轮迭代：配置备份与迁移（已实施）

路线图「配置导入导出与云备份」的本地部分落地：

- **导出**：设置页「备份与迁移」一键导出为版本化 JSON（`version` 字段向前兼容），写入工作区 `exports/`；**含密钥备份**（0600 权限，可直接恢复）与**脱敏备份**（剥离全部 API Key，可安全分享）两种
- **导入**：选择备份文件 → 明确提示覆盖范围（供应商/Agent/远程 Agent/团队/Skills/MCP/设置被覆盖，**会话历史保留**）→ 解析校验（版本拒绝、缺字段拒绝、10MB 上限）
- 会话历史（sessions.db）与记忆库永不参与导入覆盖

测试：往返恢复（密钥/Agent/团队/远程/Skills 全量）、脱敏导出零泄漏、非法 JSON/版本/缺字段拒绝、导入后会话保留 ×4 + 全量回归。

### 第十轮迭代：A2A 远程 Agent 互操作（已实施）

调研路线图「A2A 协议互操作（连接远程 Agent）」落地，依据 ADK Go v2.4.0 官方 `agent/remoteagent/v2` + 官方 `a2aproject/a2a-go v2`（Agent-to-Agent 协议标准）：

- **远程 Agent 接入**：Agents 页新增「远程 Agent（A2A 协议）」管理——填写对方 A2A 服务地址即可连接；「检查连接」自动发现 Agent Card（`/.well-known/agent-card.json`），展示名称/版本/描述/流式能力/技能清单
- **直接对话**：新建对话支持「远程 Agent」类型；消息经 JSON-RPC 跨进程往返，远程回复照常流式渲染并入 SQLite 会话历史
- **官方栈实现**：客户端用 ADK `remoteagent.NewA2A`（每次调用重新解析卡片，远端更新无需重启）；测试服务端用 `a2asrv.NewHandler + adka2a.NewExecutor`（与 ADK launcher 同一配方）托管真实 llmagent
- 卡片校验含 `SupportedInterfaces`（JSONRPC 绑定）与来源一致性校验，配置错误会得到明确报错

测试：进程内 A2A 全链路 e2e（本地 ChatService → ADK 远程 Agent → 官方 A2A 服务端 → 托管 llmagent → 假模型 → 双侧 SQLite）+ 卡片发现校验 + 全量回归与 `-race`；打包构建零错误、启动烟雾通过。

### 第九轮迭代：知识库语义检索（已实施）

调研路线图「语义化检索（嵌入向量）替代关键词 RAG-lite」落地，依据 OpenAI Embeddings API 权威规范（`POST {base}/embeddings`、`data[].embedding`、余弦相似度推荐）：

- **混合检索**：设置页配置 Embedding 供应商（OpenAI 兼容协议）+ 模型后，`search_knowledge` 升级为「向量语义 75% + 关键词 25%」混合评分；未配置时保持纯关键词行为（零破坏）
- **本地向量索引**：`knowledge.index.db`（SQLite，与会话库同目录）按文件增量构建（mtime 感知，仅重新嵌入变更文件）；删除文件自动清理；**更换 Embedding 模型自动全量重建**（不同模型的向量不可比）
- **切块策略**：700 字符滑动窗口 + 100 重叠，优先在换行处断开，保证片段可读
- **工程鲁棒性**：Embedding 服务故障优雅回退关键词并附提示；协议契约测试固定请求/响应形状（乱序 `index` 恢复、Bearer 鉴权、批量上限）
- 检索结果直接返回语义片段与「语义相关度 %」，比关键词行摘要信息量更高

测试：agentkit 单测/e2e ×5（排序命中与缓存、增量索引、模型切换重建、双路回退、切块边界）+ 协议契约 + 全链路 e2e（ChatService → search_knowledge → 语义检索结果进入模型上下文）+ 全量 `-race` 通过。

### 第八轮迭代：工具执行 HITL 确认（已实施）

调研路线图「工具执行 HITL 确认」落地，依据 ADK Go v2.4.0 官方实现（`tool/tool.go` confirmationTool 包装、`tool/toolconfirmation` 协议、`cmd/launcher/console/hitl.go` 官方客户端模式）：

- **工具级确认声明**：Agent 新增「写入文件需人工确认」开关（`confirmTools`，默认关）；开启后 `write_workspace_file` 经 ADK `functiontool.Config.RequireConfirmation` 包装，模型每次调用前先暂停请求批准（列表/读取不受影响）
- **确认卡片**：运行暂停时对话流实时出现琥珀色确认卡片（原工具名 + 参数 JSON + 提示语），「允许执行 / 拒绝」一键应答；批准后工具真正执行、模型看到执行结果继续生成，拒绝后模型收到拒绝结果并自行调整
- **恢复引擎统一**：`ChatService` 抽出共享 `runTurn`（Send 与 ConfirmToolCall 共用）；新 API `ConfirmToolCall(sessionID, callID, approved)` 按 ADK 协议回发 `adk_request_confirmation` FunctionResponse，重复应答/未知 ID 会被拒绝
- **重启可恢复**：确认请求随事件持久化（`LongRunningToolIDs`），重启后加载历史时未应答的确认卡片仍可交互应答
- 对模型透明：ADK 在恢复后把确认往返折叠为「原调用 + 结果」，模型只感知工具成功或被拒

测试：e2e ×4（批准全链路——文件真正写入 + 模型续答；拒绝——工作区不被触碰 + 模型收到 error；重启后仍可应答并完成写入；参数防护与默认关闭回归）+ 全量 `-race` 通过；修复上一轮遗留的 `wails3 build` 前端严格模式报错（ModelsPage 空值处理）；打包烟雾通过。

### 第七轮迭代：长期记忆（已实施）

调研路线图第一项落地：

- **持久化记忆服务**：`internal/memory` 实现 ADK `memory.Service`（SQLite `memories` 表，与会话库同库）；`AddSessionToMemory` 按 event 幂等摄取（跳过 partial/thought，工具调用记录为摘要），`SearchMemory` 关键词相关度检索，按用户隔离，附带 Count/Clear
- **模型可自动召回**：runner 接入 MemoryService；Agent 勾选「长期记忆检索」后挂载 ADK `loadmemorytool`，模型可跨会话检索用户历史
- **一键存入**：对话顶栏 🧠 按钮把当前整段对话存入长期记忆（toast 显示累计条数）
- 测试：摄取幂等/检索命中/用户隔离/清空 ×4 + RememberSession e2e（部署事实跨会话可召回、无记忆服务时干净报错）

### 第六轮迭代：行业调研对齐（已实施）

基于对 2026 agent 开发趋势的调研（Google ADK 生态：[Memory](https://adk.dev/sessions/memory/) / [A2A 协议](https://a2a-protocol.org/latest/) / [HITL](https://adk.dev/graphs/human-input/)；分层护栏最佳实践：[输入验证→模型→输出过滤](https://www.kalviumlabs.ai/blog/guardrails-for-llm-applications/)；[Agent 可观测性](https://www.langchain.com/resources/agent-observability)），结合项目差距落地三项：

| 功能 | 实现 | 对应趋势 |
| --- | --- | --- |
| 知识库检索（RAG-lite） | `search_knowledge` 内置工具：`workspace/knowledge/` 下 md/txt/csv/json 全文关键词检索，相关度评分 + 片段摘要，Agent 编辑器勾选挂载 | RAG/知识库 |
| 分层护栏 | 输入+输出双向密钥脱敏（sk-/AKIA/ghp_/xox/AIza/sk-ant/私钥块/通用 hex secret 8 类模式）与提示注入启发式检测告警；`EnableGuardrails` 每 Agent 开关（默认开） | Guardrails 分层防御 |
| 会话统计（Observability-lite） | `SessionStats` 聚合：消息数/助手轮次/工具调用与失败/token 用量/估算成本，顶栏统计弹层展示 | 可观测性 |

新增测试：脱敏 8 类模式、注入正负样本、知识库检索/空库/无关文件过滤、统计聚合、护栏脱敏 e2e（历史不留原始密钥）。

路线图（调研后更新）：长期记忆（ADK memory.Service 或 Mem0 集成）、A2A 协议互操作（连接远程 Agent）、工具执行 HITL 确认（ADK toolconfirmation）、语义化检索（嵌入向量）替代关键词 RAG-lite。

### 第五轮迭代（已实施）

| 功能 | 实现 |
| --- | --- |
| 图片附件（视觉模型输入） | 全栈：`Send` 支持 ≤4 张图片附件（单张 6MB，png/jpeg/webp/gif）；OpenAI 兼容适配器转为 content 数组 `image_url`（data URL），Anthropic 转为 base64 image block；前端支持 📎 选择与拖拽投放、缩略图 chips 可移除；用户消息气泡渲染附件缩略图，历史回放同步显示 |
| 团队拓扑可视化 | 团队卡片内渲染层级图：协调员/负责人在顶部，成员以连线展开；自动协调模式与指定负责人模式分别展示 |

多轮测试：适配器图片转换单测 ×2（OpenAI image_url / Anthropic image block）、附件全链路 e2e（模型收到图片 payload、历史回放 data URL、非法类型拒绝）、32 并发/畸形 SSE/取消安全回归、`-race` 通过；浏览器视觉回归（附件缩略图渲染、📎/🎤 按钮、团队拓扑层级图）；打包烟雾通过。

### 第四轮迭代（已实施）

| 功能 | 实现 |
| --- | --- |
| 编辑消息重发 | 用户消息悬停「编辑」→ 输入框进入编辑模式（横幅提示、重发按钮、可取消），发送调用 `EditAndResend` 裁剪最后一轮并重跑新文本 |
| 成本估算 | 供应商可配置输入/输出价格（¥/1M tokens），对话中按单条回复与会话累计 token 自动折算成本（未配置价格则不显示） |
| 运行中可继续输入 | 修复测试中发现的交互问题：回复生成时输入框不再禁用，可提前输入下一条（占位符提示），发送按钮单独禁用 |
| 稳定性测试 | 32 并发配置写入无丢失、畸形 SSE（坏 JSON/超长行/空 choices）不中断且用量不丢、慢响应取消后无残留运行态；services 包 `-race` 通过 |

后续路线图（按优先级）：消息分支树、附件上传（ADK Artifact）、团队拓扑可视化、MCP OAuth、系统托盘、多会话并行、自动更新、配置导入导出。

### 第三轮迭代（已实施）

| 功能 | 实现 |
| --- | --- |
| 语音输入 | 对话输入框内置麦克风按钮（Web Speech API，`SpeechRecognition`，中文 `zh-CN`，支持中间结果实时上屏）；不支持的环境自动禁用并提示 |
| 语音朗读（TTS） | 助手回复悬停「朗读」按钮（`speechSynthesis`，自动剥离 Markdown 标记）；设置页「自动朗读回复」开关，开启后每轮回复完成自动朗读 |
| 工具调用可视化 | `ToolCallCard` 组件：调用中（旋转指示）/ 完成（绿色对勾）/ 失败（红色叉）三态、参数摘要 chips、可展开完整 JSON、运行时实时刷新 |
| 产物预览 | `ArtifactService`（Go）安全读取工作区文件（目录穿越防护、12MB 二进制 / 512KB 文本上限、UTF-8 校验）；工具参数/结果中出现的文件路径自动出现「预览」按钮，`ArtifactPreview` 对话框支持图片、PDF（内嵌渲染）、Markdown（完整渲染）与文本/代码 |
| 夜间模式增强 | 主题模式三选：浅色 / 深色（夜间）/ 跟随系统（监听 OS 外观变化实时切换）；侧栏快捷切换按钮同步持久化 |
| 自定义主题 | 6 种强调色（默认蓝/紫罗兰/祖母绿/暖橙/玫红/青蓝）实时生效（CSS 变量覆盖 primary/ring/sidebar）；正文字号三档（小/中/大）；外观全部持久化到配置文件并在启动时恢复 |

多轮测试记录：① 后端单测全量（含 ArtifactService 4 项新测试：图片 base64、Markdown 读取、目录穿越/二进制拒绝、列表过滤）；② 浏览器视觉回归（工具卡片三态、图片/Markdown 预览、强调色/浅色/字号实时切换、保存、语音按钮存在性）；③ 生产构建打包 + 启动烟雾测试（进程存活 + 配置 schema 自动迁移落盘）。

### 第二轮迭代（已实施）

| 功能 | 实现 |
| --- | --- |
| Token 用量统计 | 两个自研适配器（OpenAI 兼容 `stream_options.include_usage` / Anthropic `message_start+message_delta`）提取用量到 ADK `UsageMetadata`；`ChatStreamEvent` 携带单轮 `usage` 与会话累计 `sessionUsage`；前端在助手消息下方与对话顶栏展示 |
| 重新生成 | `ChatService.Regenerate`：定位最后一条用户消息，通过共享 gorm 连接裁剪其后所有 ADK 事件，再原样重跑；前端在最后一条回复上提供「重新生成」按钮 |
| 会话导出 | `ChatService.ExportSession` 把整段对话导出为 Markdown（含工具调用记录）到工作区 `exports/`；对话顶栏下载按钮一键导出 |
| Agent 模板库 | 内置翻译助手/周报助手/代码审查员/会议纪要 4 套模板，「从模板创建…」一键预填 |

后续路线图（按优先级）：

1. **消息操作**：编辑后重发、分支对话
2. **成本统计**：在 token 用量基础上按模型单价折算成本，按日汇总
3. **对话内附件**：图片/文件拖拽，走 ADK Artifact 服务
4. **团队可视化**：Agent 拓扑图展示委派路径，团队运行时间线
5. **MCP OAuth**：远程 MCP 服务器的授权流（ADK `auth.CredentialProvider`）
6. **系统托盘与全局快捷键**：类 WorkBuddy 的常驻入口与划词唤起
7. **多窗口/多会话并行**：同时运行多个会话的并行流式输出
8. **自动更新与版本通道**：Sparkle/Wails 更新器集成
9. **配置导入导出与云备份**

本次持续迭代会话至此完成第八至第八十九轮共 80+ 轮迭代记录。
