# Sandbox Hardening 最终集成报告

- 工作树：`D:\project\agentmate-sandbox-hardening`
- 分支：`codex/sandbox-hardening`
- 基线：`acf66cb`（`main`）
- 集成提交：本文件所在的 `chore(sandbox): 集成验证与迭代记录`
- 交付：推送分支并创建 PR

## 依据

沙箱加固把原先混在 `sandboxMode` 里的「是否询问」与「OS 边界」拆成两个独立维度：
`approvalPolicy`（请求批准 / 仅风险确认 / 从不询问）与 `sandboxProfile`
（工作区可写 / 只读 / 完全访问）。这样既能表达 Codex 风格的交叉组合，也能在
没有交互界面的执行路径上明确选择非交互策略，而不是让默认审批把工具调用挂起。

本批次同时补齐了 MCP 审批门、非 Windows fail closed、只读 ACL、按次脱离沙箱、
`edit_workspace_file` 与审批路径预检，并把网络代理行为写成 ADR。工作树与分支
直接复用仓库中已有的 `agentmate-sandbox-hardening`；任务文本中的
`agentmate-wt-sandbox-hardening` 在本机不存在。

## 提交清单

| 哈希 | 主题 |
| --- | --- |
| `4f0754d` | `fix(sandbox): MCP 工具接入审批门` |
| `7991838` | `fix(sandbox): 非 Windows workspace-write 改为 fail closed` |
| `7b7b90f` | `docs(sandbox): 记录 B1 停机状态` |
| `5bf28e9` | `refactor(sandbox): 审批策略与沙箱 profile 解耦` |
| `36cb491` | `feat(ui): 审批与沙箱改为两轴选择` |
| `adf99ed` | `feat(sandbox): 新增只读沙箱` |
| `47a304c` | `feat(ui): 沙箱状态、只读选项与网络文案` |
| `3b2b33b` | `feat(sandbox): 支持按次申请脱离沙箱` |
| `32db9bc` | `feat(tools): 新增 edit_workspace_file 与 diff 证据` |
| `06dafee` | `fix(chat): 审批卡展示风险原因与路径预检` |
| `494c689` | `docs(sandbox): 网络策略与代理边界` |
| `47ed3a4` | `docs(tools): 统一工具执行层设计` |
| 本提交 | `chore(sandbox): 集成验证与迭代记录` |

## 每批实现要点

### B1：MCP 审批门与非 Windows fail closed

- MCP toolset 在注入 Agent 时按会话审批策略包一层短生命周期确认；`mcpmgr` 仍按
  server ID 缓存无策略 toolset，避免同服务在不同会话间串策略。
- 非 Windows 的 `workspace-write` 返回 `sandbox.ErrUnavailable`，不再回退到
  `runDirect`；只有显式 `danger-full-access` 才直接执行。
- 新增 `AppService.SandboxStatus()` 供设置页展示平台后端状态。

### B2：审批策略与沙箱 profile 解耦

- `Settings` / `ChatSession` 持久化 `approvalPolicy` 与 `sandboxProfile`；
  `sandboxMode` 保留为旧配置和旧前端的兼容投影。
- 迁移顺序为新字段优先，缺失轴回退 legacy `sandboxMode`；旧 JSON 缺少新键时
  不会被 `Default()` 预填值误判为显式选择。
- `AgentConfig.ApplyExecutionPolicy` 按审批策略决定静态确认集合，`sandboxPolicy`
  只按沙箱 profile 选择 OS 边界；`runTurn`、直聊、Agent 与团队本地成员共用
  同一对维度。
- 新增 `SetSessionExecutionPolicy` / `SessionExecutionPolicy`；旧
  `SetSessionSandboxMode` 保留并映射到新 pair。

### B3：两轴 UI

- 设置页把「新会话默认执行策略」拆成审批与沙箱两个单选组；composer 使用两个
  紧凑选择器并分别持久化。
- 旧会话按新字段 → legacy `sandboxMode` → 全局默认的顺序解析，不显示未知模式。
- 完全访问沙箱仍先弹风险确认；`从不询问 + 工作区可写`、
  `请求批准 + 完全访问` 等交叉组合有明确提示。

### B4：只读沙箱

- 新增 `sandbox.ModeReadOnly` 与 `config.SandboxProfileReadOnly`；Windows
  后端只给工作区读取/执行 ACL，不给 Modify，也不给工作区贴低完整性标签。
- 同一工作区从可写切到只读时先撤销旧 AppContainer ACE，避免残留 Modify 授权。
- `write_workspace_file` / `edit_workspace_file` 在只读模式下直接返回
  `read_only`；非 Windows 的只读与工作区可写一样 fail closed。
- 设置页显示后端状态与「允许联网（不限制目标）」文案，明确没有 domain
  allowlist，也不隔离 localhost / 私网。

### B5：按次申请脱离沙箱

- `run_command` 增加 `escalate` 与 `justification`；模型必须先在沙箱内尝试，
  被拒后才能按次申请。
- 请求批准 / 仅风险确认强制逐次 HITL；批准只把本次调用的 policy 副本切到
  danger，不改变会话沙箱档位。`never` 拒绝越界申请，完全访问忽略该参数。
- 确认卡展示脱离沙箱的原因；审计 `call` / `confirm` 记录
  `escalated`、`command`、`justification` 与 `decision`。

### B6：编辑工具与审批预检

- 新增 `edit_workspace_file`：精确替换 `oldText`，默认要求唯一匹配，
  `replaceAll` 可全量替换；同目录临时文件 + rename 原子落盘并保留权限。
- 返回 `addedLines` / `removedLines` 与有界 unified diff（200 行 / 32KB）。
- `PreviewGuard` 与工具共用同一份 `SessionPolicy`；路径/命令预检失败不弹确认，
  直接由 handler 返回错误。
- 实时确认卡的 `Hint` 由后端生成：风险命令原因、工作区外 / 受保护路径 /
  只读模式 / 预检失败 / 外部工具副作用。

### B7：网络与统一执行层 ADR

- 沙箱环境只从白名单构造，并可按 `Settings.HTTPProxy` 注入
  `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY=localhost,127.0.0.1,::1`；
  `danger-full-access` 仍继承父环境，不消费该字段。
- `docs/specs/sandbox-network.md` 明确 `internetClient` 不是 allowlist，
  记录本地 CONNECT 代理、WFP 与 fail closed 方案；当前不实现域名白名单。
- `docs/specs/tool-runtime.md` 定义统一 Guard、ToolSpec、副作用集合与迁移矩阵；
  本批只落文档，不迁移工具。

### 最终集成

- A2A 发布代理没有交互确认通道。新默认的 `request-approval` 会让它的文件/命令
  工具一直等待 HITL，因此 A2A 构建显式固定为
  `never + workspace-write + network off`，并清空运行时副本上的额外确认开关；
  持久化的 Agent 配置不变。
- README 与 AgentsPage 的文案改为当前两轴术语，补上只读档与非 Windows
  fail closed 的准确描述。
- `wails3 build` 会重建 `frontend/bindings/**`；本分支按共享文件约束不提交
  这些生成物，前端通过 `Call.ByName` 与可选字段交叉类型调用新 RPC。

## 测试证据

以下命令均在 `D:\project\agentmate-sandbox-hardening` 执行：

- `go test ./internal/...`：通过（`services` 34.650s，其余包缓存命中）。
- `go test -race ./internal/...`：通过（`services` 38.030s）。
- `go vet ./internal/... .`：通过，无输出。
- `go test .`：通过；`go test -race .`：通过（1.210s）。
- `cd frontend && npx tsc --noEmit`：通过，0 类型错误。
- `cd frontend && npm test`：37 个测试文件 / 273 个用例全绿。
- `cd frontend && npm run build`：通过（Vite production build，2333 modules）。
- `wails3 build`：通过（673 packages / 7 services / 101 methods 绑定生成，
  Windows 原生产物构建成功）；生成后的 `frontend/bindings/**` 已还原。
- `git diff --check`：通过；`git ls-files --eol` 确认所有改动文本文件为
  `i/lf w/lf`，无 CRLF-only diff。
- `docs/iteration-log.md`、`frontend/bindings/**`、`go.mod` / `go.sum`、
  `frontend/package-lock.json`、版本号与构建产物均未进入改动。
- `./scripts/verify.sh` 无法在本机 WSL bash 运行：该 bash 环境没有 `go` 与
  `wails3`。其前端构建/测试、Go vet、全量测试、race 与 `wails3 build`
  已用 PowerShell 等价执行并通过。

## 仍存在的边界

- **非 Windows**：没有 OS 沙箱后端；`workspace-write` 与 `read-only` 都返回
  “当前平台未实现 OS 沙箱”，不会回退到全权限。只有显式
  `danger-full-access` 才直接运行。
- **stdio MCP**：MCP 进程尚未接入 Windows AppContainer / Job Object；启用第三方
  stdio MCP 仍需信任其启动命令。MCP 工具在请求批准 / 仅风险确认下逐次确认，
  只有从不询问会关闭确认门。
- **网络 allowlist**：当前 `on/off` 只控制 AppContainer 网络 capability；
  `HTTP_PROXY` 是客户端兼容设置，不是强制代理。没有域名 allowlist、私网 /
  回环 / 元数据地址拦截、DNS rebinding 防护或 UDP/QUIC 过滤。
- **A2A 非交互策略**：发布代理固定 `never + workspace-write + network off`，
  没有远程 HITL 通道；这是有意的取舍，不是完整的远程审批方案。
- **生成绑定**：提交中的绑定快照仍不包含两轴字段、`SetSessionExecutionPolicy`
  与 `SandboxStatus`；下一次由 Integrator 统一重建绑定后可收敛为生成类型。
- **历史审批卡**：实时确认卡会显示风险命令原因与路径预检；历史重放对普通
  `run_command` 仍只显示通用文案，脱离沙箱文案已对齐。
- **平台验证**：Windows AppContainer / ACL 用例已在本机真实子进程验证；
  macOS / Linux 仅做编译级检查，没有运行时沙箱实现可测。
