### 第 ? 轮迭代：工具调用整体折叠为「执行过程」区块（已实施）

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