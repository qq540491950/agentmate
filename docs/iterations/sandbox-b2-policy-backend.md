# Sandbox Hardening B2 后端报告：审批策略与沙箱 profile 解耦

- 工作树：`D:\project\agentmate-sandbox-hardening`
- 分支：`codex/sandbox-hardening`
- 提交前 HEAD：`7b7b90ffd9658b02018ef1c24eee5e66a666c0a5`
- 提交：`refactor(sandbox): 审批策略与沙箱 profile 解耦`
- 远端状态：未 push，未创建 PR

> 本次实现与本文档在同一提交中落盘，因此提交哈希由交付回复中的
> `git rev-parse HEAD` 给出。

## 实现

- `Settings` / `ChatSession` 新增持久化字段 `approvalPolicy` /
  `sandboxProfile`；保留 `sandboxMode` 作为旧配置与旧前端的组合 profile。
- `AgentConfig` / `TeamConfig` 新增 runtime-only `ApprovalPolicy` /
  `SandboxProfile`（`json:"-"`），运行时以新字段为准，旧字段可继续输入。
- 解析顺序为新字段优先；空字段从 legacy `sandboxMode` 映射：
  - `request-approval` -> `request-approval` + `workspace-write`
  - `auto-approve` -> `auto-approve` + `workspace-write`
  - `danger-full-access` -> `never` + `danger-full-access`
  - 空 -> `request-approval` + `workspace-write`
- 加载旧 JSON 时先检查 `approvalPolicy` / `sandboxProfile` 键是否真实存在，
  避免 `Default()` 预填值掩盖 legacy 迁移。`Store.Update` 对旧前端只改
  `sandboxMode` 的写入做兼容桥接；显式新字段变更优先。
- `AgentConfig.ApplyExecutionPolicy`：`request-approval` 强制 files + shell，
  `auto-approve` 保留 Agent 显式配置，`never` 清空确认。交叉组合
  `request-approval + danger-full-access` 与 `never + workspace-write` 均合法。
- `kit.sandboxPolicy` 只按 `SandboxProfile` 选择 OS 边界；
  `ShellToolWithPolicyAndMode` 只按 `ApprovalPolicy` 决定静态/动态确认。
  MCP 审批门在非 `never` 时继续要求确认，不受 danger 沙箱影响。
- `runTurn`、直聊、Agent 与团队本地成员均同时传审批与沙箱两个维度。
- `NewSession` 快照新 pair 并继续写 legacy `sandboxMode`；`ForkSession`
  继承源会话 pair 与持久化网络偏好。
- danger 不再改写已持久化的 `SandboxNetwork`：
  `EffectiveSandboxNetwork` 在 danger 时返回 `on`，切回 workspace-write
  后恢复原值；fork 不会把 danger 的临时 `on` 固化成新会话偏好。
- 新增 `SetSessionExecutionPolicy` / `SessionExecutionPolicy`；保留
  `SetSessionSandboxMode`，把三档旧值映射到 pair。新审计事件为
  `session_execution_policy_set`，同时记录 `approval`、`sandbox`、`source`；
  旧 RPC 保留 `session_sandbox_mode_set` 并附带拆维字段。

## 测试结果

- `go test ./internal/config ./internal/agentkit ./internal/services -count=1`：
  通过（config 0.200s，agentkit 6.136s，services 27.856s）。
- `go test -race ./internal/config ./internal/agentkit ./internal/services -count=1`：
  通过（config 1.252s，agentkit 7.653s，services 33.705s）。
- `go test ./internal/... -count=1`：通过。
- `go vet ./internal/...`：通过。
- `git diff --check`：通过。
- 新增/更新覆盖：
  - 旧值五档迁移矩阵、新字段优先、旧前端 legacy-only 写入、danger 网络回切。
  - settings 修改不污染已有会话；fork 继承拆维 pair 与网络偏好。
  - `never + workspace-write`：文件与命令均无确认，工作区外写入仍被拦截。
  - `request-approval + danger-full-access`：先弹确认，批准后工作区外写入成功。
  - `auto-approve + workspace-write`：保留安全命令直跑、风险命令确认。
  - MCP 审批门覆盖 request/auto/never 及两个交叉组合。

## 已知边界

- 本批不改前端、`frontend/bindings/**`、`docs/iteration-log.md`、
  `go.mod` / `go.sum`、`frontend/package-lock.json`、版本号或构建产物。
- 旧前端仍只消费 `sandboxMode`；拆维 UI 与 bindings 由后续批次接入。
- `read-only` sandbox profile 留给批次 4，目前只接受 `workspace-write` /
  `danger-full-access`。
- 未改变 stdio MCP 进程不接入 OS 沙箱的既有边界。
- 指定的 `..\agentmate-wt-sandbox-hardening` 不存在；复用已绑定
  `codex/sandbox-hardening` 的 `..\agentmate-sandbox-hardening`。

## 下一批前置符号

- `config.Settings.ApprovalPolicy`、`config.Settings.SandboxProfile`
- `config.ChatSession.ApprovalPolicy`、`config.ChatSession.SandboxProfile`
- `config.AgentConfig.ApplyExecutionPolicy(approvalPolicy, sandboxProfile string)`
- `(config.Settings).EffectiveExecutionPolicy() (approvalPolicy, sandboxProfile string)`
- `(config.ChatSession).EffectiveExecutionPolicy(cfg config.Config) (approvalPolicy, sandboxProfile string)`
- `services.SessionExecutionPolicy{ApprovalPolicy, SandboxProfile}`
- `(*services.ChatService).SetSessionExecutionPolicy(sessionID, approvalPolicy, sandboxProfile, source string) error`
- `(*services.ChatService).SessionExecutionPolicy(sessionID string) (*services.SessionExecutionPolicy, error)`
- 兼容入口仍在：`(*services.ChatService).SetSessionSandboxMode(sessionID, mode, source string) error`
