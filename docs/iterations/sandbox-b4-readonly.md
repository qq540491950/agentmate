# Sandbox Hardening B4 报告：只读沙箱后端与 UI 收尾

- 工作树：`D:\project\agentmate-sandbox-hardening`
- 分支：`codex/sandbox-hardening`
- 批次起点 HEAD：`36cb49178a4671405d100a9bb3ffc4d562bb7f2a`
- 后端提交：`adf99ed45e6ae3d660e55982080ef4e5075c5f9b`
  （`feat(sandbox): 新增只读沙箱`）
- 前端提交：`feat(ui): 沙箱状态、只读选项与网络文案`
- 最终 HEAD：见交付回复中的 `git rev-parse HEAD`
- 远端状态：未 push，未创建 PR

> 指定的 `..\agentmate-wt-sandbox-hardening` 不存在；复用已绑定
> `codex/sandbox-hardening` 的 `..\agentmate-sandbox-hardening`。

## 后端实现

- `sandbox.ModeReadOnly` 加入模式枚举与 `Policy.validate`；只读策略下
  `CheckWrite` 对所有目标返回 `read_only`，先于 protected/outside 判定。
- `sandbox.Run` 在只读模式下只要求 `Request.Dir` 位于工作区（`Within`），
  不再用 `CheckWrite` 拒绝合法的只读工作目录；越界目录仍以
  `outside_workspace` 拒绝。
- Windows 后端对只读工作区只执行
  `icacls /grant *SID:(OI)(CI)RX`，不授予 Modify，也不给工作区贴低完整性
  标签；同一工作区从可写切到只读时先撤销旧 AppContainer ACE，避免残留
  Modify 授权。临时 HOME 仍授予 Modify + 低完整性，受保护路径 deny ACE
  继续应用，AppContainer 网络 capability 逻辑未改。
- 非 Windows 的 `read-only` 与 `workspace-write` 一样 fail closed，返回
  `ErrUnavailable`，不会回退到 direct exec。
- `config.SandboxProfileReadOnly` 加入归一化；`Kit.sandboxPolicy` 映射到
  `sandbox.ModeReadOnly`；`SetSessionExecutionPolicy` 接受只读 profile。
- `write_workspace_file` 在只读策略下直接返回 `read_only`，并关闭该工具的
  HITL 确认，避免无意义的中断；`list_workspace_files` /
  `read_workspace_file` 不受影响。
- README 安全段补充只读 ACL、临时 HOME、受保护路径与审批独立性的边界。

## 前端实现

- `SandboxProfile` 增加 `read-only`；设置页与 composer 的三档选项为
  工作区可写 / 只读 / 完全访问，均使用 `Eye` / `FolderLock` /
  `TriangleAlert` 图标。
- 提取 `PolicyChoiceGroup` 组件，保留单选语义、`aria-checked`、方向键 /
  Home / End 导航与 Check 选中标识；选中面继续使用 `choice-selected`，
  未选中面使用 `control`，无布局 border/ring。
- 设置页通过 `getSandboxStatus`（`Call.ByName` 调用
  `AppService.SandboxStatus`，不手改生成绑定）读取平台后端：
  `available=true` 显示后端名称；`available=false` 使用 `warning-soft`
  显示“当前平台未实现 OS 沙箱，请求批准/帮我批准会拒绝执行终端命令；
  只有完全访问可运行”。
- 网络项改名为“允许联网（不限制目标）”，明确没有 domain allowlist、
  不隔离 localhost / 私网；`danger-full-access` 显示“联网由完全访问隐含
  开启”，开关只读且有隐含开启状态，不改写持久化的 `sandboxNetwork`。
- composer 的审批与沙箱选择器共享新的只读选项；选中只读时展示
  “只读沙箱：终端只能读，不能写工作区；审批策略仍独立，文件写入会被拒绝”。
- 浏览器桩补了 `AppService.SandboxStatus` 的可用后端样例，便于视觉走查。

## 测试结果

- 后端：`go test ./internal/sandbox ./internal/agentkit ./internal/services -count=1`
  通过（sandbox 1.623s、agentkit 7.508s、services 29.040s）。
- 后端全量：`go test ./internal/... -count=1` 通过（services 33.495s）。
- 静态检查：`go vet ./internal/... .` 通过。
- 前端：`cd frontend && npx tsc --noEmit` 通过。
- 前端测试：`npm test` 通过，37 个测试文件 / 271 个用例。
- 前端构建：`npm run build` 通过（Vite production build）。
- `git diff --check` 通过。

覆盖点：

- Windows 只读工作区：读取成功、工作区内写入失败、工作区外写入失败、
  `git status` 可运行；同一工作区先可写后切只读时旧 Modify ACE 被撤销。
- `CheckWrite` 的只读/受保护/越界路径全部返回 `read_only`；只读 `Run` 的
  越界 Dir 被拒绝，工作区内 Dir 可启动。
- 文件工具 read/list 成功、write 失败且不产生 HITL；会话执行策略可持久化
  只读档位。
- 前端只读 normalization、三个 sandbox 选项、只读单选态 `aria-checked`、
  `SandboxStatus.available=false` 精确警告、`never + danger-full-access`
  的联网隐含开启与无 allowlist/私网防护文案。

> Windows `git status` 用例将本机 Git for Windows 的 `git.exe` 与运行 DLL
> 复制进测试工作区后执行，因为本机 Git 位于 `D:`，未向 AppContainer 暴露
> 读取权限；命令本身走真实 `sandbox.Run` 与只读 ACL。

## 已知边界

- 本批未改 `docs/iteration-log.md`、`frontend/bindings/**`、
  `go.mod` / `go.sum`、`frontend/package-lock.json`、版本号或构建产物。
- 只读仅约束 OS 文件写入与文件工具写入；审批策略仍独立决定是否询问。
- 非 Windows 平台仍无 OS 沙箱后端，只读与工作区可写均 fail closed。
- 不包含 escalation 能力；解释器 / 提权路径仍属后续批次。

## 下一批前置符号

- `sandbox.ModeReadOnly`
- `config.SandboxProfileReadOnly`
- `Policy.CheckWrite` 的稳定错误码 `read_only`
- `(*services.ChatService).SetSessionExecutionPolicy` 接受只读 profile
- `getSandboxStatus()` / `SandboxStatusInfo`（`frontend/src/lib/api.ts`）
- `SandboxProfile` 包含 `read-only`，`SANDBOX_PROFILE_OPTIONS` 含“只读”
- `sandboxStatusSummary()`、`sandboxNetworkCopy()`、`executionPolicyHint()`
