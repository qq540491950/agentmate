# Sandbox Hardening B1 停机报告

- 工作树：`D:\project\agentmate-sandbox-hardening`
- 分支：`codex/sandbox-hardening`
- HEAD：`799183847a73df2dbf0fa7625f0135036b8376c4`
- 基线：`acf66cb58cc5ef39edeac602cfb20d7afd76fb02`
- 远端状态：未 push，未创建 PR

## 提交

1. `4f0754d3cd6aaaac9a54753b1991e5ef1e2d234f` — `fix(sandbox): MCP 工具接入审批门`
   - `Kit` 在 MCP 注入点按会话 `SandboxMode` 生成短生命周期确认包装。
   - `mcpmgr.Manager` 仍按 server ID 缓存原始、无策略 toolset。
   - `request-approval` 与 `auto-approve` 均要求确认，`danger-full-access` 不要求。
   - 确认继续复用 `confirm_request` / `ConfirmToolCall`，原始 MCP 参数透传。
2. `799183847a73df2dbf0fa7625f0135036b8376c4` — `fix(sandbox): 非 Windows workspace-write 改为 fail closed`
   - 非 Windows 的 `workspace-write` 返回 `sandbox.ErrUnavailable`，不再回退 `runDirect`。
   - `danger-full-access` 仍走 direct，并保留 `configureDirectProcess`。
   - 新增 `AppService.SandboxStatus()`；未改前端、绑定或 UI。
   - README 安全段补充非 Windows 平台边界。

## 测试结果

- `go test ./internal/agentkit ./internal/mcpmgr ./internal/services -count=1 -timeout 5m`：通过。
- 新增 fake MCP streamable HTTP 用例覆盖 `request-approval` / `auto-approve` 产生确认、`danger-full-access` 不确认、MCP 参数原样透传、同服务两会话策略隔离。
- `go test ./internal/sandbox ./internal/services -count=1 -timeout 5m`：通过。
- `go test ./internal/... -count=1 -timeout 10m`：通过。
- `GOOS=linux go test -c -o <临时目录>/agentmate-sandbox-linux.test ./internal/sandbox`：编译通过；Windows 本机不执行 Linux 二进制。
- `go vet ./internal/...`：通过。
- `git diff --check`：通过。

## 已知边界

- 本批只补 MCP 审批，不接 stdio MCP 进程的 OS 沙箱。
- 非 Windows 暂无 OS 沙箱实现；`workspace-write` fail closed，只有显式 `danger-full-access` 才直接运行。
- 未增加生产环境变量后门；需要直跑的测试显式构造 `ModeDangerFullAccess`。
- 未修改 `docs/iteration-log.md`、`frontend/bindings/**`、`go.mod` / `go.sum`、`frontend/package-lock.json`、版本号或构建产物。
- 任务给定工作树路径 `..\agentmate-wt-sandbox-hardening` 不存在；复用仓库中已绑定 `codex/sandbox-hardening` 的现有 worktree `..\agentmate-sandbox-hardening`。

## 下一批前置符号

- `agentkit.mcpToolsetForApproval(ts tool.Toolset, sandboxMode string) tool.Toolset`（`internal/agentkit/kit.go`，包内 helper）。
- `(*services.AppService).SandboxStatus() services.SandboxStatusInfo`；JSON 字段为 `available` / `backend` / `error` / `profileSid`。
- `sandbox.ErrUnavailable` 与 `sandbox.BackendStatus()`；非 Windows `workspace-write` 已可据 `errors.Is` 识别不可用。
