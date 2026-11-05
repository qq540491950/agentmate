### 第 ? 轮迭代：HITL 确认卡「允许完全访问」会话级授权（已实施）

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