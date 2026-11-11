# Sandbox Hardening B3 前端报告：审批与沙箱两轴选择

- 工作树：`D:\project\agentmate-sandbox-hardening`
- 分支：`codex/sandbox-hardening`
- 提交前 HEAD：`5bf28e9c5ed9e76d7e76e5bd21e9aebbce9414fe`
- 提交：`feat(ui): 审批与沙箱改为两轴选择`
- 远端状态：未 push，未创建 PR

> 本次实现与本文档在同一提交中落盘，因此提交哈希由交付回复中的
> `git rev-parse HEAD` 给出。

## 实现

- `settingsModel.ts` 新增独立的 `ApprovalPolicy`、`SandboxProfile` 与
  `ExecutionPolicy` 类型，以及对应的 normalize / legacy 迁移 / pair 解析函数。
  `normalizeSandboxMode` 只保留给旧 `sandboxMode` 迁移；新 UI 不再消费三档组合值。
- `sandboxMode.ts` 拆出 `APPROVAL_OPTIONS`（请求批准 / 仅风险确认 / 从不询问）
  与 `SANDBOX_PROFILE_OPTIONS`（工作区可写 / 完全访问）；旧
  `SANDBOX_MODE_OPTIONS` 保留为 deprecated，不再用于新 UI。
- 设置页把「新会话默认执行策略」拆为两个 `role="radiogroup"` 选择组，沿用
  `choiceCardClass`、`aria-checked` 与键盘方向键切换。完全访问仍先弹风险确认；
  默认完全访问提示联网由沙箱隐含开启。联网开关在完全访问下只显示隐含开启，
  不改写持久化的 `sandboxNetwork` 偏好。
- ChatPage 的 composer 改为审批、沙箱两个紧凑选择器；草稿分别保存两轴，首条消息
  创建会话后调用 `SetSessionExecutionPolicy`，已有会话切换则写会话级 pair。
- 读取旧会话时先按 `approvalPolicy` / `sandboxProfile` 解析；任一为空时由 legacy
  `sandboxMode` 推导缺失轴；全部为空时回退当前全局默认，不展示“未知模式”。
- 新增交叉组合提示：`从不询问 + 工作区可写` 说明仍受沙箱约束；
  `请求批准 + 完全访问` 说明逐步询问但已无沙箱。
- `api.ts` 通过 `Call.ByName("agentmate/internal/services.ChatService.SetSessionExecutionPolicy", ...)`
  接入批次 2 RPC；未修改或提交 `frontend/bindings/**`。生成绑定尚未包含新字段时，
  `Settings` / `ChatSession` 用可选字段扩展保持运行时类型兼容。

## 测试结果

- `npx tsc --noEmit`：通过。
- `npm test`：36 个测试文件、268 个用例全绿。
- `npm run build`：通过（Vite production build）。
- `git diff --check`：通过。
- 新增/更新覆盖：
  - 两轴各自 normalize，以及三档 legacy `sandboxMode` 的 1:1 pair 迁移。
  - 显式新字段优先、缺失轴回退 legacy、旧会话全部为空时使用全局 fallback。
  - 3 + 2 选项元数据与 composer 短标签。
  - 完全访问的风险确认判定。
  - `never + workspace-write` 与 `request-approval + danger-full-access` 两条交叉提示。

## 已知边界

- 本批不改 Go 文件、`docs/iteration-log.md`、`frontend/bindings/**`、
  `go.mod` / `go.sum`、`frontend/package-lock.json`、版本号或构建产物。
- `read-only` 沙箱档位仍留给后续批次；当前只展示 `workspace-write` /
  `danger-full-access`。
- 生成绑定尚未包含两轴字段与 `SetSessionExecutionPolicy`，因此 API 层使用
  兼容交叉类型与 `Call.ByName`；绑定重建后可直接收敛为生成类型。
- 指定的 `..\agentmate-wt-sandbox-hardening` 不存在；复用已绑定
  `codex/sandbox-hardening` 的 `..\agentmate-sandbox-hardening`。

## 下一批前置符号

- `frontend/src/lib/settingsModel.ts`:
  - `ApprovalPolicy`、`SandboxProfile`、`ExecutionPolicy`
  - `DEFAULT_EXECUTION_POLICY`
  - `normalizeApprovalPolicy`、`normalizeSandboxProfile`
  - `executionPolicyFromLegacySandboxMode`、`resolveExecutionPolicy`
- `frontend/src/lib/sandboxMode.ts`:
  - `APPROVAL_OPTIONS`、`SANDBOX_PROFILE_OPTIONS`
  - `approvalPolicyOption`、`sandboxProfileOption`
  - `executionPolicyHint`、`sandboxNeedsRiskConfirmation`
- `frontend/src/lib/api.ts`:
  - `Settings.approvalPolicy` / `Settings.sandboxProfile`
  - `ChatSession.approvalPolicy` / `ChatSession.sandboxProfile`
  - `setSessionExecutionPolicy(sessionID, approvalPolicy, sandboxProfile)`
- 界面状态：
  - SettingsPage 已有「新会话默认审批」「新会话默认沙箱」两个选择组。
  - ChatPage 已有草稿两轴状态、会话级切换与两个 composer 选择器。
