# Sandbox Hardening B5 报告：按次申请脱离沙箱

- 工作树：`D:\project\agentmate-sandbox-hardening`
- 分支：`codex/sandbox-hardening`
- 批次起点 HEAD：`47a304c`
- 提交：`feat(sandbox): 支持按次申请脱离沙箱`
  （提交哈希与最终 HEAD 见交付回复）
- 远端状态：未 push，未创建 PR

> 指定的 `..\agentmate-wt-sandbox-hardening` 不存在；复用已绑定
> `codex/sandbox-hardening` 的 `..\agentmate-sandbox-hardening`。

## 前置检查

- 批次 2 提交 `5bf28e9`（`refactor(sandbox): 审批策略与沙箱 profile 解耦`）存在。
- 批次 4 提交 `adf99ed`（`feat(sandbox): 新增只读沙箱`）存在。
- `config.Settings` / `config.ChatSession` 已有 `ApprovalPolicy`、`SandboxProfile`。
- `sandbox.ModeReadOnly` 已存在；`Kit.sandboxPolicy` 按 `SandboxProfile` 选择模式。

## 实现要点

- `shellArgs` 新增 `escalate` 与 `justification`：前者只在沙箱拒绝后按次申请；
  后者向用户解释为什么必须越界。工具描述明确要求先尝试沙箱内执行，禁止预判性
  使用。
- `ShellToolWithPolicyAndMode` 新增 `allowEscalation`：
  - `never`：handler 返回明确错误，命令不执行；
  - `request-approval` / `auto-approve`：`escalate=true` 强制走 HITL；
  - `danger-full-access`：忽略 `escalate`，直接走现有 direct 分支，不追加确认。
- 批准后仅把本次调用的 `sandbox.Policy` 副本切到
  `sandbox.ModeDangerFullAccess`，仍调用 `sandbox.Run` 的 danger 分支，
  `configureDirectProcess` 保持生效；会话的 `SandboxProfile` 不变。
- 拒绝时 handler 不会执行；模型收到 ADK 的 rejected 结果。
- 确认卡通过 `confirmRequestHint` 显示“脱离沙箱执行终端命令。理由：…”，普通
  `run_command` 显示“执行终端命令。”；历史重放使用同一文案。
- 审计 `call` 记录增加 `escalated=true`、`command`、`justification`；`confirm`
  记录增加 `decision`（approved / rejected）及同样字段。
- 第一版不做自动 on-failure 检测：由模型看到沙箱拒绝后主动重试 `escalate`。

## 测试结果

- `go test ./internal/agentkit ./internal/services -count=1`：通过
  （agentkit 6.360s，services 27.400s）。
- `go test -race ./internal/agentkit ./internal/services -count=1`：通过
  （agentkit 13.454s，services 40.784s）。
- `go test ./internal/... . -count=1`：通过（services 34.025s，cmd 包 1.138s）。
- `go vet ./internal/... .`：通过；`git diff --check`：通过。
- 覆盖点：
  - `never` 下 `escalate` 返回错误且工作区内文件未创建，证明命令未执行。
  - `auto-approve` 下 `escalate` 产生确认；批准后工作区外写入成功，拒绝后
    原文件不变；确认文案含“脱离沙箱”与 justification。
  - `request-approval` 下普通命令与 `escalate` 都弹确认，卡片文案可区分。
  - `danger-full-access` 下 `escalate` 不产生额外确认并直接执行。
  - 审计 call / confirm 记录均含 `escalated=true`、`justification`、命令；
    confirm 另含 `decision`。
  - 批准越界后 `SessionExecutionPolicy` 仍为 `workspace-write`。

## 已知边界

- 未修改 `config.go`、`sandbox_windows.go`、`frontend/**`、
  `chatservice_sessions.go`。
- 未修改 `docs/iteration-log.md`、`frontend/bindings/**`、`go.mod` /
  `go.sum`、`frontend/package-lock.json`；未新增依赖。
- 未做自动失败检测与自动重试；越界必须由模型显式发起、用户逐次批准。
- 非 Windows 平台仍无 OS 沙箱后端；沙箱内命令会 fail closed，模型可显式申请
  按次越界并由用户决定。

## 下一批前置符号

- `agentkit.shellArgs.Escalate`、`agentkit.shellArgs.Justification`
- `agentkit.ShellToolWithPolicyAndMode(..., allowEscalation bool)`
- `agentkit.shellExecution.policyFor(args shellArgs) (sandbox.Policy, error)`
- 审计字段：`escalated`、`justification`、`command`、`decision`
- `services.confirmRequestHint`（确认卡边界文案）
- `services.escalationAuditFields` / `services.auditToolCallArgs`
