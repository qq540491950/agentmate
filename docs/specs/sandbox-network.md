# ADR：沙箱子进程网络策略

- 状态：Proposed
- 范围：Windows AppContainer 沙箱内由 `run_command` 启动的子进程及其后代
- 本批决策：只注入显式代理环境；域名 allowlist、私网防护和系统级防火墙规则均不实现

## 背景

当前本地工具沙箱只有两个粗粒度网络档位：

- `off`：AppContainer 令牌不启用网络 capability，子进程不能直接建立出站连接。
- `on`：令牌启用 `internetClient`（SID `S-1-15-3-1`），子进程可以访问网络。

这不是域名 allowlist，也不是私网隔离。`internetClient` 是 AppContainer 的能力开关，WFP 只看到包 SID、用户、地址、端口等条件，不会校验 HTTP Host、URL 路径或调用意图。Windows 的地址分类还可能使该 capability 覆盖本地“私网”地址；即使某台机器上表现为只允许公网，也不能把它当作可移植的私网边界。

本批新增的 `Settings.HTTPProxy` 注入只是兼容性措施：

- 未配置代理时，沙箱环境不包含 `HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY`，也不继承父进程的同名变量。
- 配置代理时，仅沙箱子进程收到三个变量；`NO_PROXY` 固定保留本机回环。
- 完全访问（`danger-full-access`）继续继承父环境，不读取沙箱子进程的代理覆盖。
- 环境变量不会阻止进程忽略代理、清空变量或直接使用 socket。它不能替代网络策略引擎。

## 威胁模型

### 资产

- 任务工作区中的源代码、文档、客户数据和凭据。
- 应用配置、长期记忆、知识库索引与技能目录。
- 本机回环服务、局域网设备、云元数据端点和企业内网资源。
- 用户对“允许联网”这一动作的安全预期。

### 攻击入口

- 提示注入诱导模型执行下载、上传、回连或扫描命令。
- 依赖安装脚本、构建脚本和 `curl | powershell` 等供应链路径。
- MCP 服务或自定义技能返回恶意指令。
- 子进程自身被利用后尝试横向移动或窃取数据。

### 安全目标

1. 默认关闭网络；只有用户显式启用时才允许出站。
2. 启用 allowlist 后，仅允许域名与端口白名单；拒绝私网、回环、链路本地、云元数据和组播地址。
3. 代理故障、DNS 失败和策略异常一律 fail closed，不回退直连。
4. 子进程及其后代不能通过忽略环境变量绕过策略。
5. 完全访问模式保持现有语义，不伪装成受限模式。

### 非目标

- 防御拥有本机管理员权限的恶意进程。
- 拦截内核驱动、其他用户进程或应用自身的网络请求。
- 在代理中解密 TLS 或检查 HTTPS 请求体。
- 本批实现 DNS 过滤、企业代理自动发现或 PAC 脚本。

## 关键事实

### `internetClient` 不是 allowlist

`internetClient` 只说明 AppContainer 令牌拥有“客户端网络”能力。它没有域名、URL、HTTP 方法、请求体或“只允许代理”的概念。只要 capability 存在，任何现有 WFP 放行规则都可能适用于该进程。因此：

- 不能把“允许联网（不限制目标）”改名为“允许访问白名单”。
- 不能在启用 `internetClient` 的同时，仅靠设置 `HTTP_PROXY` 声称已经限制目标。
- 真正的 allowlist 必须位于能够解析主机名和协议语义的 L7 代理，或由代理加 WFP 组成强制边界。

### AppContainer loopback 默认隔离

AppContainer 到 loopback 的连接默认被网络隔离阻止；需要给对应 AppContainer 添加 loopback exemption。该豁免按 AppContainer 标识维护，是系统级状态，不是某个端口或某个进程的一次性授权。Windows 文档提供的 `CheckNetIsolation.exe LoopbackExempt` 面向调试/部署场景，通常需要管理员权限；上线前必须验证目标 Windows 版本的持久化、卸载清理和多用户行为。相同 package SID 的 AppContainer 之间的 loopback 例外也不能被当成“只能访问本地代理”。

这意味着“本地 CONNECT 代理 + loopback 豁免”能打通代理，但豁免可能同时扩大子进程对其它本机 loopback 服务的可达范围。若无法把可达范围收窄到代理端点，只能把该方案标为 best-effort，不能标为硬 allowlist 边界。

### WFP 的强项与盲区

Windows Filtering Platform 能按 AppContainer 包 SID、用户、地址、端口、协议和方向做内核级过滤，适合阻断直连、限制到代理端点以及拒绝私网地址段。但：

- WFP 不解析 HTTP Host 或 URL，单独使用无法实现域名 allowlist。
- 用户态 WFP 管理和按 SID 动态加规则通常需要管理员权限；BFE 对象默认不对普通用户开放。
- 规则是系统状态，必须处理崩溃残留、卸载清理、多实例互斥和企业策略冲突。
- 只能按 IP 允许时，CDN 共享地址和 DNS 变化会迫使规则扩大；先解析域名再添加 IP 规则还会遭遇 DNS rebinding。

WFP 因而适合作为“禁止绕过代理”和“拒绝私网”的硬底座，而不是域名策略本身。

## 方案比较

### 方案 A：本地 CONNECT 代理 + AppContainer loopback 例外

沙箱不启用 `internetClient`，只允许通过 loopback 访问应用内的本地代理。代理负责 HTTP 绝对 URI 和 HTTPS `CONNECT host:port`，维护域名/端口 allowlist，并在解析 DNS 后拒绝私网地址。上游连接由代理建立，沙箱本身只能访问代理。

优点：

- 唯一能在不实施 TLS 中间人的情况下按域名和端口做 allowlist 的方案。
- 拒绝逻辑集中，便于稳定错误码、审计和单元测试。
- 可处理 HTTP 重定向、CONNECT、DNS rebinding 和私网地址校验。
- 不需要给沙箱开放 `internetClient`，代理可独立于子进程生命周期运行。

缺点与风险：

- 需要 AppContainer loopback exemption，可能是管理员操作和系统级持久配置。
- 豁免粒度若无法限定到代理端口，子进程可能访问其它本机服务。
- 只支持遵守代理协议或环境变量的客户端；不遵守的进程会被直连阻断，这是预期的 fail closed，但会产生兼容性问题。
- 代理必须防 DNS rebinding、IPv4-mapped IPv6、重定向逃逸、代理认证泄漏和本地恶意客户端滥用。
- HTTPS `CONNECT` 只能约束目标主机与端口，无法看到路径和内容。

### 方案 B：Windows Filtering Platform

按 AppContainer SID 和网络条件建立允许/阻断过滤器。典型形态是默认阻断出站，仅允许代理地址和端口；也可直接阻断 RFC1918、链路本地、回环和云元数据地址段。

优点：

- 内核级强制执行，子进程无法通过换语言、换客户端或忽略环境变量绕过。
- 对私网、地址族、端口和代理直连的限制可以非常明确。
- 可对残留连接和 UDP/QUIC 做统一处理。

缺点与风险：

- 无法表达域名 allowlist；单独使用只能回答“能否访问这个 IP:port”。
- 动态规则通常需要管理员权限，增加安装、升级和运维成本。
- 规则生命周期、清理、冲突、日志和测试都明显更复杂。
- 每台机器的网卡、VPN、企业防火墙和 Windows 版本差异会放大兼容性问题。

### 方案 C：暂不实现

保留当前 `off` / `on` 两档。`on` 依靠 `internetClient` 提供粗粒度全局联网，代理环境仅用于兼容用户的 HTTP 代理设置。

优点：

- 改动最小，不引入管理员权限或系统级状态。
- 对本地 Ollama、内网服务和开发工具的兼容性最好。

缺点：

- 没有域名 allowlist、私网防护或“只走代理”保证。
- 提示注入后的任意下载、上传和内网访问仍由用户显式打开“允许联网”承担风险。
- 不能满足“只允许访问若干个已知域名”的产品承诺。

### 对比

| 维度 | A：本地 CONNECT 代理 | B：WFP | C：暂不实现 |
| --- | --- | --- | --- |
| 域名 allowlist | 可以 | 不可以，只能按 IP/端口 | 不可以 |
| 私网/元数据阻断 | 代理解析后可以，但需防 rebinding | 可以，内核级 | 不可以 |
| 防止绕过代理 | 依赖普通网络 capability 关闭；loopback 豁免可能仍过宽 | 可以限定到代理端点 | 不可以 |
| 是否需要管理员 | loopback 豁免通常需要；若再配合 WFP 则明确需要 | 通常需要 | 不需要 |
| 兼容性 | 需要客户端遵守代理；不遵守时 fail closed | 对任何客户端生效，但规则易误伤 | 最好 |
| 系统残留 | 豁免和代理进程需要清理 | 规则、sublayer、provider 需要清理 | 无 |
| 适用结论 | 适合作为 L7 策略主路径 | 适合作为 L3/L4 强制底座 | 仅可作为当前过渡 |

## 推荐

**推荐方向是方案 A，并将最小范围的 WFP 强制作为达到“安全 allowlist”承诺的前置条件。**

分阶段落地：

1. **本批（已完成）**：仅注入显式代理环境，保持 `off` / `on` 语义，不修改 UI，不宣称 allowlist。
2. **下一阶段 MVP**：实现本地 CONNECT 代理、域名/端口匹配、DNS rebinding 防护和私网地址拒绝；仅在 loopback 豁免可受控时开放。
3. **强制阶段**：若 loopback 豁免不能按端口收窄，则用 WFP 只允许沙箱访问代理端点并阻断其它出站，再移除 allowlist 模式的 `internetClient`。
4. **验收阶段**：只有在“忽略代理环境、直接 curl、原始 socket、UDP/QUIC、私网地址、重定向、DNS rebinding”全部失败后，才可在 UI 和文档中使用“域名 allowlist”表述。

若产品不能接受管理员权限或系统级 WFP 规则，则只能采用方案 C 的诚实边界：继续显示“允许联网（不限制目标）”，并把代理环境作为兼容设置，而不是安全控制。

## allowlist 与私网防护设计

### 规则匹配

- 仅接受规范化后的 ASCII 小写主机名；IDN 先转 punycode，去掉末尾点。
- 默认只允许显式列出的精确主机；通配符只允许 `*.example.com` 形式，并要求至少一级标签，禁止 `*`、`*.com` 和 IP 字面量规则。
- 默认只允许 443；HTTP 80 必须单独声明并显示警告。
- `CONNECT` 校验目标主机和端口；普通 HTTP 校验绝对 URI 的主机、端口和 scheme。
- 拒绝 `userinfo@host`、反斜杠、控制字符、畸形端口和非 HTTP/HTTPS scheme。

### 防私网与 DNS rebinding

代理必须自己解析 DNS，并在实际 dial 时使用已校验的地址，而不是“先校验域名解析结果，再让系统重新解析”。每个解析结果都要检查：

- IPv4：`0.0.0.0/8`、`127.0.0.0/8`、RFC1918、`100.64.0.0/10`、`169.254.0.0/16`、组播和保留段。
- IPv6：`::1`、`fc00::/7`、`fe80::/10`、组播、未指定地址及 IPv4-mapped 地址。
- 域名解析到多个地址时全部校验，任一私网结果即拒绝。
- 目标端口必须属于 allowlist；默认拒绝全部非 TCP 流量。
- 代理不自动跟随重定向；客户端的下一次请求仍经过代理并重新匹配。

### 代理自身

- 只监听 `127.0.0.1`，使用随机端口和每次运行随机凭据，避免被同机其它进程直接当作开放代理。
- 代理凭据不能写入普通日志；审计只记录脱敏后的主机、端口、结果和拒绝码。
- 代理异常、监听失败、DNS 失败和校验失败均返回拒绝，不允许回退直连。
- 代理进程必须随应用退出而停止；启动时清理上次异常退出的监听状态。

### 当前边界

本批没有域名 allowlist、私网拦截、UDP 过滤、loopback exemption 或 WFP 规则。配置的代理变量只对遵守代理协议的客户端有效，沙箱开启网络时仍可能直连任意可达地址。

## 测试计划

### 单元测试

- 主机名规范化、端口限制、通配符层级、IDN、IP 字面量和畸形 URL。
- IPv4/IPv6 私网、链路本地、回环、元数据、组播、IPv4-mapped 地址表。
- DNS 返回混合公网/私网地址时拒绝；dial 只使用已校验地址。
- `CONNECT`、HTTP 绝对 URI、重定向和代理认证矩阵。
- 代理配置为空时无代理变量；配置后仅沙箱环境可见；父环境代理不泄漏；完全访问继承父环境。

### 集成测试

- 本地假代理 + 假上游服务器：允许域名成功，未列域名 403，私网目标 403。
- 沙箱 `curl` 通过代理成功；清空 `HTTP_PROXY` 后直连被系统边界拒绝。
- 代理停止时命令失败，不产生直连回退。
- loopback 豁免添加/移除的幂等性与崩溃清理。

### 端到端与安全回归

- Windows AppContainer 中验证：允许、拒绝、私网、DNS rebinding、IPv6、UDP/QUIC、子进程继承和代理重启。
- 检查 `NO_PROXY` 不会把公网误判为直连；回环服务不被意外放行。
- 审计中不出现代理凭据、完整 URL 查询串或响应体。
- 完全访问模式不经过沙箱代理覆盖，保持现有父环境行为。

## 本批决策与后果

- 保留 `off` / `on` 与当前 UI 文案；不新增域名 allowlist 配置。
- 只实现后端代理环境注入；不修改 `sandbox_windows.go` 的网络 capability。
- 不引入系统级防火墙、管理员安装步骤或 loopback exemption。
- 后续实现若不能阻断绕过代理的直连，必须继续使用“允许联网（不限制目标）”，不得提前使用“域名白名单”宣传。

## 参考资料

- [AppContainer isolation — Microsoft Learn](https://learn.microsoft.com/en-us/windows/win32/secauthz/appcontainer-isolation)
- [Troubleshooting UWP App Connectivity Issues in Windows Firewall — Microsoft Learn](https://learn.microsoft.com/en-us/windows/security/operating-system-security/network-security/windows-firewall/troubleshooting-uwp-firewall)
- [Understanding Network Access in Windows AppContainers — Project Zero](https://projectzero.google/2021/08/understanding-network-access-windows-app.html)
- [Windows Filtering Platform — Microsoft Learn](https://learn.microsoft.com/en-us/windows/win32/fwp/windows-filtering-platform-start-page)
