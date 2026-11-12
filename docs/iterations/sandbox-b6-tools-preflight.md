# Sandbox Hardening B6 报告：编辑工具与审批预检

- 工作树：`D:\project\agentmate-sandbox-hardening`
- 分支：`codex/sandbox-hardening`
- 批次起点 HEAD：`32db9bc`（A 提交）
- 提交：
  - A `32db9bc` `feat(tools): 新增 edit_workspace_file 与 diff 证据`
  - B `fix(chat): 审批卡展示风险原因与路径预检`
    （B 提交哈希与最终 HEAD 见交付回复）
- 远端状态：未 push，未创建 PR

> 指定的 `..\agentmate-wt-sandbox-hardening` 不存在；复用已绑定
> `codex/sandbox-hardening` 的 `..\agentmate-sandbox-hardening`。

## 前置检查

- 批次 2 提交 `5bf28e9`（`refactor(sandbox): 审批策略与沙箱 profile 解耦`）存在。
- 批次 5 提交 `3b2b33b`（`feat(sandbox): 支持按次申请脱离沙箱`）存在。
- `config.Settings` / `config.ChatSession` / `config.AgentConfig` 已有
  `ApprovalPolicy`、`SandboxProfile`。
- `agentkit.shellArgs` 已有 `Escalate`、`Justification`。

## A. `edit_workspace_file` 与 diff

- 参数：`path`、`oldText`、`newText`、`replaceAll`（默认 false）。
- 处理链复用 `safeWorkspacePath` → `Policy.CheckWrite` → `maxWriteBytes`：
  - `oldText` 为空直接拒绝；
  - 0 次匹配返回“未找到匹配内容”；
  - 多次匹配且 `replaceAll=false` 返回“匹配到 N 处，请提供更精确上下文或
    replaceAll=true”，不写文件；
  - 越界、受保护路径、只读模式沿用文件工具既有错误码。
- 写入使用同目录 `os.CreateTemp` + `Chmod(原权限)` + `Sync` + `Rename`：
  原子替换，保留原文件权限；未触碰的 CRLF 原字节保持不变。
- 返回 `ok`、`bytes`、`addedLines`、`removedLines`、`diff`。diff 为包内
  逐行 LCS 实现（大中间段退化为删除/新增块），输出受 200 行 / 32KB 限制，
  超出追加“... [diff 已截断]”。
- `workspaceFileTools` 注册顺序为 list / read / write / edit；工具描述明确
  “修改已有文件优先用本工具，新建或整体重写用 write_workspace_file”。
- 前端 `toolStepDisplay` 将 `edit_workspace_file` 显示为“修改 + path”。

## B. 审批原因与路径预检

- 新增只读 `agentkit.PreviewGuard`：
  - `NewPreviewGuard(workspace, profile, network, protectedDirs...)` 通过
    `SessionPolicy` 构建与工具完全相同的策略；
  - `NewPreviewGuardWithPolicy` 复用已构建的工具策略；
  - `CheckWritePath` 只做 `safeWorkspacePath` + `Policy.CheckWrite`，
    `CheckCommand` 只做 `Policy.CheckCommand`，均不写磁盘；
  - `Kit.sandboxPolicy` 改为调用 `SessionPolicy`，消除 chatservice 复制策略。
- `write_workspace_file`、`edit_workspace_file`、`run_command` 改为动态
  `RequireConfirmationProvider`：
  - 路径/命令/越界/escalation 预检失败返回 false，不弹确认，由 handler
    直接返回错误；
  - 预检通过且当前审批策略需要确认时返回 true；
  - `request-approval` 对所有可执行调用返回 true；
  - `never` 不弹确认；`auto-approve` 保留风险命令与 `escalate` 的动态确认。
- `runTurn` 生成 `confirm_request` 时由后端计算 `Hint`，前端只渲染
  `ChatStreamEvent.Hint`：
  - `run_command`：`sandbox.AssessCommand` 的风险原因，未命中给
    “该命令需要确认。”；`escalate` 保留脱离沙箱边界与 justification；
  - 文件工具：用会话 `PreviewGuard` 预检路径，区分“工作区外 / 受保护路径 /
    只读模式 / 预检失败”；
  - 非内置工具：“外部工具可能有副作用。”；
  - ADK 的英文通用提示与空 hint 视为无应用提示，不覆盖自定义 hint。
- `ConfirmCard` 始终显示 hint（缺失时显示“该工具调用需要你的确认。”），
  不再只依赖 `toolArgs`；脱离沙箱、工作区外、受保护路径、只读模式与预检失败
  使用 `warning-soft` 底纹 + `TriangleAlert` 图标 + `warning-text` 文字。

## 测试结果

- `go test ./internal/agentkit ./internal/services -count=1`：通过
  （agentkit 6.890s，services 30.027s）。
- `go test -race ./internal/agentkit ./internal/services -count=1`：通过
  （agentkit 9.620s，services 42.701s）。
- `go test ./internal/... -count=1`：通过（services 35.900s）。
- `go vet ./internal/... .`：通过；`git diff --check`：通过。
- `cd frontend && npx tsc --noEmit`：0 错；
  `npm test`：37 个文件 / 273 个用例全绿；
  `npm run build`：成功。
- 覆盖点：
  - A：唯一匹配、0 匹配、多匹配（拒绝 + replaceAll）、越界、protected、
    symlink、只读、CRLF 原字节、权限保留、200 行 diff 截断、工具注册与描述。
  - B：风险命令 hint、工作区外/受保护路径不弹确认、正常写入仍弹确认、
    MCP 外部工具提示、PreviewGuard 路径/命令只读预检、动态 provider
    的预检失败与正常确认分支、ConfirmCard 风险语义与缺失 hint 兜底。

## 已知边界

- 未修改 `config.go`、`sandbox_windows.go`、`chatservice_sessions.go`。
- 未修改 `docs/iteration-log.md`、`frontend/bindings/**`、`go.mod` /
  `go.sum`、`frontend/package-lock.json`；未新增依赖。
- 历史消息重放仍走 `chatservice_render.go` 的既有 `confirmRequestHint`
  （本批边界不允许修改该文件）；新建的路径/外部工具原因在实时
  `confirm_request` 流中生效。
- `edit_workspace_file` 以文本精确替换为边界，不做语义编辑；大文件 diff
  仅截断展示，文件完整写入。

## 下一批前置符号

- `agentkit.workspaceFileTools` 已注册 `edit_workspace_file`
  （`EditFileTool` / `EditFileToolWithPolicy`）。
- `services.confirmRequestReason` 保证实时 `confirm_request.Hint` 非空。
- `agentkit.PreviewGuard`、`NewPreviewGuard`、`NewPreviewGuardWithPolicy`、
  `CheckWritePath`、`CheckCommand` 与 `agentkit.SessionPolicy` 可复用。
- `write_workspace_file` / `edit_workspace_file` / `run_command` 均设置
  `RequireConfirmationProvider`。
- `ChatStreamEvent.Hint` 仍是审批卡唯一文案来源；前端 `ConfirmCard`
  已按 warning 语义展示边界风险。
