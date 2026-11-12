# ADR：统一工具执行层

- 状态：Proposed
- 范围：内置工具、MCP 工具、技能工具与会话状态工具
- 本批约束：只定义接口、注册声明、迁移顺序和测试策略；不迁移任何工具，不引入网络 allowlist 或系统级防火墙

## 背景

当前本地工具的执行边界分散在多个入口：

- `run_command` 调用 `sandbox.Run`，但命令预检、审批模板和审计仍由 shell 工具自己拼装。
- `write_workspace_file` / `edit_workspace_file` 通过 `Policy.CheckWrite`，读工具只需要工作区路径校验。
- MCP 工具通过 `WithConfirmation` 加审批，但 stdio MCP 进程还没有接入同一个 OS 沙箱。
- `plan_todos` 写 ADK session state；memory、knowledge、skills 各自处理持久化或读取，没有统一的副作用声明。
- 审计字段由调用点决定，工具名、参数、拒绝码和结果之间缺少稳定关联。

这会导致三个问题：新增工具容易绕过某个边界；审批提示会与真正执行的策略不一致；测试只能逐个工具复制，无法证明“所有有副作用的工具都被覆盖”。

## 目标

1. 每个工具在执行前经过同一条 Guard 管线，执行后产生同一种审计事件。
2. 工具在注册时声明自己的副作用、需要的检查、确认策略和审计字段；运行时验证声明与行为一致。
3. 拒绝、确认、执行和审计使用稳定代码和顺序，前端只消费结构化结果。
4. 迁移期间保持现有工具行为、错误码和 HITL 语义不变。
5. 用表驱动测试覆盖策略矩阵，而不是为每个工具写一套重复流程。

## 非目标

- 不重写 ADK 工具接口，不引入第三方插件运行时。
- 不在本批迁移工具，不改变 `run_command`、文件工具或 MCP 的现有行为。
- 不实现域名 allowlist、私网防护或 WFP 规则。
- 不把正则命令启发式升级为完整 shell 解析器。
- 不给 MCP 进程补 OS 沙箱；这需要独立的生命周期与进程边界设计。
- 不把审计做成远端服务或不可篡改账本。

## 统一执行流程

```
ToolCall
  -> Registry.Resolve
  -> Guard.CheckWrite / Guard.CheckCommand
  -> Guard.Confirmation
  -> Executor (filesystem / sandbox / MCP / session state)
  -> Guard.Audit
  -> ToolResult
```

任何一步返回拒绝都必须停止后续步骤；审计仍然记录被拒绝的尝试。确认只能在预检通过后发生，避免用户为一个必然失败的调用做决定。

## Guard 接口

Guard 是会话级对象，由当前 `ApprovalPolicy`、`SandboxProfile`、`SandboxNetwork`、工作区和工具注册声明共同构建。它不直接操作 UI；确认和审计通过请求/事件结构与运行时交互。

```go
type Guard interface {
    CheckWrite(ctx context.Context, req WriteRequest) Decision
    CheckCommand(ctx context.Context, req CommandRequest) Decision
    Confirmation(ctx context.Context, req ConfirmationRequest) (ConfirmationDecision, error)
    Audit(ctx context.Context, event AuditEvent) error
}
```

### `Decision`

```go
type Decision struct {
    Allowed   bool
    Code      string
    Reason    string
    Risk      RiskLevel
    NeedHuman bool
}
```

- `Code` 面向工具结果和测试，保持稳定，例如 `outside_workspace`、`protected_path`、`read_only`、`network_blocked`。
- `Reason` 面向用户，使用中文，但不得包含密码、token、完整命令参数或路径之外的敏感内容。
- `Allowed=false` 优先于 `NeedHuman`；拒绝时不得弹确认卡。
- `Risk` 用于确认提示和 UI 展示，不是权限来源。

### `CheckWrite`

检查工具准备写入的文件或持久化资源：

- 路径类工具必须经过 `safeWorkspacePath` 和 `Policy.CheckWrite`；只读模式下直接返回 `read_only`。
- 写入范围、受保护路径、符号链接解析和原子写入策略由 Guard 统一执行。
- 需要整体替换文件时，Guard 还应校验大小上限和目标权限，但不得执行实际写入。
- `memory`、`todos` 等非工作区资源在迁移阶段使用明确的资源类型，不允许把任意路径伪装成“内部资源”绕过检查。

### `CheckCommand`

检查将要启动 shell 或外部进程的调用：

- 复用 `Policy.CheckCommand` 的受保护路径预检，并调用 `sandbox.AssessCommand` 标记风险。
- 校验工作目录位于允许的根内，越界、只读目录或不可用后端返回稳定拒绝码。
- `escalate` 只能在已有沙箱拒绝后申请，且必须有 `justification`；审批模式为 never 时拒绝。
- 命令文本只用于风险提示和日志；真正的安全边界仍是 OS 沙箱或后续网络策略。

### `Confirmation`

返回是否需要人工确认、确认卡的结构化 hint 和可执行范围，而不是直接弹窗。

- `request-approval`：所有有副作用或外部性的调用需要确认。
- `auto-approve`：风险命令、escalate、越界或未知 MCP 工具需要确认。
- `never`：不弹确认；但拒绝条件仍由预检决定，不能因为“免确认”而绕过沙箱。
- 文件工具被预检拒绝时不生成确认请求。
- 确认请求必须带 `CallID`、工具名、稳定风险码、脱敏参数摘要和建议动作；前端只渲染这些字段。

### `Audit`

每次工具调用产生一条结构化事件：

```go
type AuditEvent struct {
    SessionID string
    TurnID    string
    CallID    string
    Tool      string
    Effect    Effect
    Decision  string
    Code      string
    Risk      RiskLevel
    StartedAt time.Time
    Duration  time.Duration
    Result    string
}
```

- 审计参数按工具声明的字段采集；命令保留完整文本，但凭据、代理认证信息和环境变量必须脱敏。
- 被拒绝、取消、超时、非零退出和成功都要记录，不能只记录成功。
- 审计写入失败不回滚已经执行的副作用，但必须让调用结果带 `audit_error` 警告并写入应用日志；安全决策不得依赖审计是否成功。

## 工具注册声明

工具不能在 handler 内偷偷调用文件系统、shell 或网络。注册时必须提供完整声明：

```go
type ToolSpec struct {
    Name         string
    Version      string
    Description  string
    Effects      EffectSet
    Guard        GuardSpec
    Confirmation ConfirmationSpec
    Audit        AuditSpec
}

type EffectSet struct {
    WorkspaceRead  bool
    WorkspaceWrite bool
    Command        bool
    Network        bool
    StateWrite     bool
    External       bool
}

type GuardSpec struct {
    CheckWrite   bool
    CheckCommand bool
    Confirmation bool
    Audit        bool
}
```

注册表在启动时执行断言：

- `WorkspaceWrite` 必须声明 `CheckWrite`；`Command` 必须声明 `CheckCommand`；任何有副作用的工具都必须声明 `Audit`。
- `External` 或 `Network` 工具默认声明 `Confirmation`，除非有明确的、经过安全评审的豁免。
- 工具名唯一，schema 可序列化，描述非空；禁止未声明就直接引入 `os/exec` 或裸 socket。
- 运行时只能从注册表取得 executor，handler 不得自行构造 Guard 或绕过包装器。
- 注册表拒绝时应用 fail closed：该工具不暴露给模型，并记录启动错误。

## 迁移矩阵

| 工具/类别 | 当前副作用 | Guard 检查 | 确认 | 审计 | 目标阶段 |
| --- | --- | --- | --- | --- | --- |
| `list_workspace_files` / `read_workspace_file` | 工作区读取 | 工作区路径校验；无 CheckWrite | 否 | 路径、字节数、结果 | 1 |
| `write_workspace_file` / `edit_workspace_file` | 工作区写入 | `CheckWrite`；大小、保护路径、只读 | 按审批策略；预检失败不确认 | 路径、字节数、diff 统计、结果 | 1 |
| `run_command` | 执行 shell/外部进程 | `CheckCommand` + OS 沙箱 + escalation | 风险/hint 按审批策略 | 脱敏命令、cwd、退出码、耗时 | 1 |
| MCP 工具 | 未知外部副作用 | 启动命令声明；调用参数审计；暂不做 OS 沙箱 | 默认始终确认（当前已如此） | server、tool、参数摘要、结果码 | 2 |
| `plan_todos` | 写 ADK session state | 状态命名空间与大小检查 | 否 | 变更前后数量、结果 | 3 |
| `memory` | 写长期记忆数据库 | 内部资源写检查；大小/配额 | 否（由用户显式操作触发） | 操作类型、条目数、结果 | 3 |
| `search_knowledge` | 知识库读取/检索 | 只读工作区与索引路径校验 | 否 | 查询摘要、命中数、降级模式 | 3 |
| 技能发现/读取 | 只读用户/项目技能 | 路径与只读校验 | 否 | 根目录、技能数、结果 | 3 |

### 阶段 1：文件与 shell

不改变现有行为，只把已有逻辑收进 Guard：

- `write_workspace_file` 和 `edit_workspace_file` 复用 `Policy.CheckWrite`，保持错误码。
- `run_command` 复用 `sandbox.Run`、风险检测和动态确认 provider。
- 先以适配器包装旧实现，确认输出、确认 hint 和审计字段与现状一致。

### 阶段 2：MCP 外部工具

- 为每个 server/tool 注册外部副作用声明，默认 `External=true`、`Confirmation=true`。
- Guard 不能假装已经隔离 MCP 进程；审计必须明确 `sandboxed=false`。
- stdio MCP 的启动命令、环境变量和工作目录要进入声明；后续单独接 OS 沙箱。

### 阶段 3：状态类与只读工具

- `plan_todos`、memory、knowledge、skills 接入统一 Audit 和资源级检查。
- 明确“内存/session state 写入”不等于工作区文件写入，但同样不能绕过配额和命名空间边界。
- 只读工具不允许演化成隐式写入工具；新增写能力必须修改声明和测试。

### 阶段 4：强制注册表

- 移除旧工具构造函数入口，只保留通过注册表构建的路径。
- 添加静态检查，禁止工具包直接调用 `os/exec`、`os.WriteFile`、裸 `net.Dial` 等绕过 Guard 的 API。
- 注册表覆盖率达到 100% 后，才允许对外宣称“所有工具经过统一执行层”。

## 测试策略

### 表驱动策略矩阵

用一张表覆盖：

```text
sandbox profile × network × path/command × approval policy × tool kind
  -> allowed/denied code
  -> confirmation required?
  -> audit event fields
```

至少包含：

- 只读、工作区可写、完全访问。
- 网络关闭、网络开启。
- 工作区内、工作区外、受保护路径、符号链接、只读目录。
- 安全命令、风险命令、escalate、无 justification、never。
- 文件读、文件写、shell、MCP、todos、memory、knowledge、skills。
- 预检拒绝不弹确认；确认后执行；取消、超时、非零退出。

### 注册表契约测试

- 每个工具都有 `ToolSpec`，声明的 Effects 与测试期望一致。
- 声明 `WorkspaceWrite` 却没有 `CheckWrite` 时注册失败。
- 声明 `Command` 却没有 `CheckCommand` 时注册失败。
- 有副作用却没有 `Audit` 时注册失败。
- 未声明的工具名、重复工具名和不可序列化 schema 被拒绝。
- 静态扫描工具包，发现 `os/exec`、`os.WriteFile`、裸 `net.Dial` 等直接调用时失败，除非该文件是受审查的 adapter。

### 验证顺序

1. Guard 单元测试与决策表。
2. 工具级 fake executor 测试，不依赖真实网络或用户交互。
3. `sandbox.Run`、文件政策与 session state 的集成测试。
4. Windows AppContainer 端到端测试。
5. 全量 `go test ./internal/...` 与 `go vet ./internal/... .`。

## 兼容与回滚

- 阶段 1 的 adapter 保留旧实现作为一侧对照，行为差异先用测试锁定再删旧路径。
- 稳定错误码和确认 hint 不得因重构改变；若必须改变，先升级前端契约。
- 审计仅追加字段，不改已有字段含义。
- 任一阶段发现工具行为差异，回退 adapter 而不是放宽 Guard。

## 本批不做的内容

- 不迁移 `files`、`shell`、MCP、`todos`、memory、knowledge 或 skills 的真实执行路径。
- 不新增 domain allowlist、网络代理 enforcement 或系统级 WFP 规则。
- 不改变 `sandbox_windows.go` 的 AppContainer capability。
- 不实现第三方插件、MCP 进程沙箱或远程审计。
- 不把风险命令启发式包装成“完整命令白名单”。

## 参考入口

- `internal/sandbox/sandbox.go`：`Policy.CheckWrite`、`Policy.CheckCommand`、`Run`。
- `internal/agentkit/kit.go`：`SessionPolicy`、`PreviewGuard`、文件工具注册。
- `internal/agentkit/shell.go`：`run_command` 的策略与 escalation。
- `internal/services/chatservice_turn.go`：确认事件与工具结果渲染。
- `internal/services/chatservice_render.go`：审批提示的现有文案来源。
