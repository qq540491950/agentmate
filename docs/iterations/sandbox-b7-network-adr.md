# Sandbox Hardening B7 报告：代理环境与网络 / 工具执行 ADR

- 工作树：`D:\project\agentmate-sandbox-hardening`
- 分支：`codex/sandbox-hardening`
- 批次起点 HEAD：`06dafeedd66d5b843da4022a890886ea280574c5`
- A 提交：`494c6892d33d4e3b3ebbd88a619005a3bfa68239`
  （`docs(sandbox): 网络策略与代理边界`）
- B 提交：本提交（`docs(tools): 统一工具执行层设计`；完整哈希见交付回复）
- 最终 HEAD：B 提交（见交付回复）
- 远端状态：未 push，未创建 PR

> 指定的 `..\agentmate-wt-sandbox-hardening` 不存在；复用已绑定
> `codex/sandbox-hardening` 的 `..\agentmate-sandbox-hardening`。开始前
> `git status --short` 为空。

## 前置检查

- 批次 4 提交 `adf99ed`（`feat(sandbox): 新增只读沙箱`）存在且是 HEAD 祖先。
- `sandbox.ModeReadOnly` 已存在并映射到 `config.SandboxProfileReadOnly`。
- `config.Settings.SandboxProfile` / `config.ChatSession.SandboxProfile` 已存在。
- 本批未改 `SettingsPage`、`ChatPage`、README、绑定、依赖或版本号。

## A. 沙箱代理环境

- `sandbox.Policy` 新增 `ProxyURL` 和 `WithProxy`；字段只供沙箱后端构造子进程环境，
  `danger-full-access` 的 `runDirect` 不读取它。
- `agentkit.Kit.sandboxPolicy` 从 `Store.Settings().HTTPProxy` 读取唯一代理配置；
  未配置或仅空白时不生成代理变量。
- 非空时代理环境为：
  - `HTTP_PROXY=<Settings.HTTPProxy>`
  - `HTTPS_PROXY=<Settings.HTTPProxy>`
  - `NO_PROXY=localhost,127.0.0.1,::1`
- 沙箱仍从白名单构造环境，默认不继承父进程的 `HTTP_PROXY` / `HTTPS_PROXY` /
  `NO_PROXY`。完全访问仍使用 `os.Environ()`，不消费沙箱代理字段。
- 没有新增 config 字段，没有改 `sandbox_windows.go` 的网络 capability。
- 代理环境只是客户端兼容设置，不是网络 allowlist enforcement；边界写入
  `docs/specs/sandbox-network.md`。

覆盖测试：

- 未配置代理时父进程代理变量不进入沙箱环境。
- 配置后沙箱子进程可见三个代理变量，且 `NO_PROXY` 只含本机回环。
- `danger-full-access` 继续继承父进程 `HTTP_PROXY`，不会采用 `Policy.ProxyURL`。
- `Kit.sandboxPolicy` 在空配置和更新后的 `Settings.HTTPProxy` 之间正确切换。

## B. 文档

- `docs/specs/sandbox-network.md`：
  - 比较本地 CONNECT 代理 + AppContainer loopback 例外、WFP、暂不实现。
  - 覆盖威胁模型、allowlist 匹配、私网/回环/元数据防护、DNS rebinding、
    代理认证、故障 fail closed 与测试计划。
  - 明确 `internetClient` 不是 allowlist；明确本批不引入 loopback exemption、
    WFP 或域名白名单。
  - 推荐本地 CONNECT 代理为主路径；若 loopback 例外无法按端点收窄，则必须以
    最小 WFP 强制作为宣称“安全 allowlist”的前置条件。
- `docs/specs/tool-runtime.md`：
  - 定义统一 `Guard` 的 `CheckWrite` / `CheckCommand` / `Confirmation` /
    `Audit` 接口及 `Decision` / `ConfirmationDecision` / `AuditEvent`。
  - 定义工具注册声明 `ToolSpec`、副作用 `EffectSet`、Guard 覆盖断言和稳定
    拒绝码。
  - 给出 files / shell / MCP / todos / memory / knowledge / skills 的迁移矩阵。
  - 给出阶段 1–4 的 registry 收口顺序、表驱动测试策略、静态绕过检查与明确不做项。
  - 本批不迁移任何工具，不实现网络 allowlist，不引入系统级防火墙规则。

## 测试结果

- `go test ./internal/sandbox -count=1`：通过（6.974s）。
- `go test ./internal/sandbox ./internal/services -count=1`：通过
  （sandbox 1.758s，services 35.565s）。
- `go test ./internal/... -count=1`：通过（agentkit 7.187s、config 0.465s、
  mcpmgr 0.273s、memory 4.595s、sandbox 1.768s、services 35.526s）。
- `go vet ./internal/... .`：通过。
- 定向代理用例：`TestProxyEnvironmentOnlyWhenConfigured`、
  `TestDangerFullAccessKeepsParentProxyEnvironment`、`TestWindowsProxyEnvironment`
  全部 PASS（AppContainer 真实子进程）。
- `git diff --check`：通过。

## 已知边界

- `HTTP_PROXY` / `HTTPS_PROXY` 环境对不读取代理配置的进程无效；沙箱开启网络时仍可
  直连可达目标。
- 未实现域名 allowlist、私网拦截、UDP/QUIC 过滤、loopback exemption 或 WFP。
- 完全访问模式不获得应用内代理注入；它继续按原行为继承父环境。
- 未改 README / 设置页 / Chat 页 / bindings / `go.mod` / `go.sum` /
  `frontend/package-lock.json`，未新增依赖。

## 下一批前置

- `docs/specs/sandbox-network.md` 与 `docs/specs/tool-runtime.md` 存在。
- 沙箱代理环境测试通过：未配置无变量、配置后子进程可见、direct 行为不变。
- `sandbox.Policy.ProxyURL` / `WithProxy` 与 `Kit.sandboxPolicy` 的代理接线可复用。
- 若进入网络 allowlist 批次，先确认 loopback exemption 的系统权限、粒度与清理；
  未能限制到代理端点时不得提前宣称“域名白名单”。
