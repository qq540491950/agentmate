# AgentMate

基于 **Google ADK Go v2**（`google.golang.org/adk/v2`）+ **Wails v3** + **React/TypeScript + shadcn/ui** 构建的本地 AI Agent 桌面应用。

支持：可配置模型供应商、自定义 Agent、Agent 团队（ADK 子代理委派）、Skills（agentskills.io SKILL.md 规范）、MCP 工具服务器、流式对话、会话持久化（SQLite）。

![tech](https://img.shields.io/badge/ADK-Go%20v2-blue) ![tech](https://img.shields.io/badge/Wails-v3-green) ![tech](https://img.shields.io/badge/ui-shadcn%2Fui-black)

## 功能总览

| 模块 | 说明 |
| --- | --- |
| 模型配置 | 支持 OpenAI 兼容协议（OpenAI / DeepSeek / Kimi / Qwen / Ollama / vLLM…）、Anthropic（Claude）、Google Gemini 三种协议；可配 Base URL、API Key、模型列表（多模态/上下文窗口/价格元数据，可从 /models 拉取）、默认供应商，并一键测试连通性 |
| Agents | 自定义名称、描述、系统提示词、Temperature / MaxTokens / 停止序列，挂载 Skills、MCP 工具与内置工具（时间查询、工作区文件读写、知识库检索、长期记忆、任务计划 plan_todos、终端命令 run_command）；安全护栏（密钥脱敏/注入检测）；运行护栏（工具调用上限、供应商请求自动重试）；写入文件与执行终端命令的 HITL 人工确认；单个导出/导入分享 |
| 远程 Agent | A2A 协议（官方 a2a-go）：连接远程 Agent 直接对话（Agent Card 自动发现 + 连通性检查），也可作为团队成员参与协作；本应用亦可作为 A2A 服务端对外发布本地 Agent |
| Agent 团队 | 多 Agent 协作：自动协调员模式（系统合成协调员分派任务）或指定负责人模式；基于 ADK 的 agent 转移机制（transfer）实现委派；支持远程 A2A 成员 |
| Skills | 可复用技能指令包，保存为标准 SKILL.md（YAML frontmatter + Markdown），由 ADK Skill Toolset 以渐进式披露方式供模型按需加载；支持从工作区 skills/ 目录批量导入；自动发现项目/用户 `.agents/skills`（只读），`/` 菜单按当前会话工作区列出并可调用 |
| MCP 服务 | 通过 stdio（本地命令）或 Streamable HTTP（远程）连接 MCP 服务器，发现并列出工具，挂载到 Agent；远程服务器支持 Bearer / API Key / OAuth 2.1（授权码 + PKCE，令牌持久化静默刷新）认证 |
| 知识库 | 工作区 knowledge/ 目录检索：可配置 Embedding 供应商升级为「向量语义 + 关键词」混合检索（本地增量向量索引，SQLite） |
| 对话 | 流式输出（SSE）、`@` 引用工作区文件/目录作为当轮上下文并可直接指定助手、任务级工作区（自动独立目录或首轮前选择文件夹）、右侧结果区（产物/全部文件/变更/**行级 diff 与复制 Patch**/预览/**基线对比切换**）、工具调用/结果实时展示、团队对话中显示各成员输出、随时停止、工具执行人工确认（HITL）、会话置顶/分叉/并行运行、后台完成提示、上下文窗口占用指示与高水位提醒、Token 用量与成本（含按日明细）、任务计划清单卡与置顶进度条、会话级模型选择、生成期间排队发送（**多条排队不覆盖**）、会话内消息过滤（⌘F）、图片粘贴/拖放/重生成保留附件、Markdown 导出、会话历史持久化（ADK Database Session Service + 纯 Go SQLite 驱动）、**↑ 编辑上一条 / Esc 停止 / ⌘⇧[⇧] 切换会话 / 长会话「显示更早」翻页 / 切换加载骨架屏** |
| 桌面集成 | 系统托盘（显示/隐藏/退出）、全局快捷键（⌘⇧Space 唤起）、Nord / 石墨 / 纸墨 / 高对比主题、深浅/跟随系统、字号、语音输入与朗读 |
| 设置与迁移 | 默认/全局工作区目录、外观、语义检索 Embedding 配置、智能会话命名（默认开启、可指定标题模型）、A2A 服务启停、配置备份导出/导入（版本化、可脱敏）、Agent 分享 |

## 架构

```
┌────────────────────────── macOS / Windows / Linux ──────────────────────────┐
│  Wails v3 桌面壳                                                            │
│  ┌────────────── React + TS + shadcn/ui (Tailwind v4) ───────────────────┐  │
│  │  对话 / Agents / 团队 / Skills / MCP / 模型配置 / 设置                  │  │
│  └───────────────▲───────────────────────────────────────────────────────┘  │
│                  │ 绑定调用 (wails3 generate bindings) + 事件流 (chat:stream) │
│  ┌───────────────┴──────────────── Go 后端 ──────────────────────────────┐  │
│  │  services: ConfigService / ChatService / MCPService / AppService      │  │
│  │  agentkit: model.LLM 适配器(OpenAI 兼容 / Anthropic / Gemini)          │  │
│  │            llmagent 构建、内置 FunctionTool、Skill Toolset             │  │
│  │  mcpmgr:   MCP 服务器生命周期与状态（ADK mcptoolset + go-sdk）          │  │
│  │  config:   JSON 配置存储；ADK session/database + SQLite 会话持久化      │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────────┘
```

关键点：

- **模型可配置**：ADK 的 `model.LLM` 是接口，本项目自行实现了 OpenAI Chat Completions（流式 + 函数调用）与 Anthropic Messages（流式 + tool use）两个适配器，Gemini 使用 ADK 官方 `model/gemini`，因此任意兼容供应商都能接入。
- **Agent 团队**：团队成员是标准 `llmagent`，由 ADK 注入 `transfer_to_agent` 能力实现相互委派；自动协调员模式额外合成一位协调员作为根节点。
- **Skills**：保存时物化为 `<配置目录>/skills/<name>/SKILL.md`，构建 Agent 时通过 `skilltoolset` 挂载。
- **会话持久化**：ADK 事件（含工具调用）完整存入 SQLite（`sessions.db`），重启后可回看历史。

## 快速开始

### 环境要求

- Go 1.26+（以 `go.mod` 为准）
- Node.js ≥ 20.19 + npm（Vite 8 与 `@vitejs/plugin-react` 要求）
- [Wails v3 CLI](https://v3.wails.io/getting-started/installation/)，与 Go SDK 保持同一版本：

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0
```

升级 Go 工具链后要重新执行上面的安装命令：`wails3` 内置的 `go/types`
必须与当前 Go 版本一致，否则 `wails3 dev/build` 会把新版标准库的语法或
build tag 误报成 warning。

macOS 需要 Xcode Command Line Tools；Linux 需要 GTK / WebKit 开发包。

### 开发运行

```bash
wails3 dev
```

### 构建与打包

```bash
# 生产构建（生成绑定 → 构建前端 → 编译 Go）
wails3 build          # 产物: bin/agentmate

# 打包 macOS .app
wails3 package        # 产物: bin/agentmate.app（重命名为 AgentMate.app）
```

### 自测

```bash
go test ./internal/... .
go vet ./internal/... .
cd frontend && npm test        # vitest（前端纯函数单测）
```

测试覆盖：

- OpenAI 兼容适配器：SSE 流式文本、函数调用往返（含 `tool` 消息回传）、HTTP 错误
- Anthropic 适配器：SSE 流式文本 + `input_json_delta` 工具参数解析
- 配置存储：默认种子数据、持久化往返、损坏文件自愈
- 工作区路径安全：防目录穿越
- 端到端：ChatService → ADK Runner → llmagent → 假 SSE 模型服务器 → 内置时间工具调用循环 → SQLite 会话持久化

## 使用

应用按「模型 → Agent → 会话」组织：先在「模型配置」接入供应商，再在「Agents」定义 Agent 或团队，之后即可在「对话」中选择并执行任务。

| 模块 | 用途 |
| --- | --- |
| 模型配置 | 接入 OpenAI 兼容 / Anthropic / Gemini，拉取模型列表并测试连通性 |
| Agents | 配置提示词与模型参数，挂载 Skills、MCP 与内置工具；多个 Agent 可组队 |
| 对话 | 选择 Agent 或团队与任务目录，流式执行并查看产物、文件变更与行级 diff |
| 扩展 | Skills、MCP、知识库、A2A 发布、配置备份与迁移，按需在对应页面启用 |

## 视觉测试与体验设计

UI 通过「浏览器 + 数据桩」做视觉回归：前端以 `wails3 dev` 提供的页面 + 注入的 IPC mock 数据桩，在真实浏览器中逐页走查（对话/Agents/团队/Skills/MCP/模型/设置、Nord/石墨/纸墨/高对比主题、空态、对话框、行级 diff 与快捷键交互）。

UI 实现约束与 Nord 令牌见 [docs/design-system.md](docs/design-system.md)；代理必须同时遵守 [frontend/AGENTS.md](frontend/AGENTS.md)。

逐轮的迭代依据、实现要点与测试证据记录在 [docs/iteration-log.md](docs/iteration-log.md)。

## 质量与验证

```bash
./scripts/verify.sh      # 前端构建 → go vet → 测试 → -race → wails3 build
```

CI（`.github/workflows/ci.yml`）在每次 push / PR 上跑后端 `vet + test + -race` 与前端 `tsc + build`。注意 workflow 会先构建前端：`main.go` 用 `//go:embed all:frontend/dist` 嵌入产物，干净检出时该目录不存在。

测试覆盖（`go test ./internal/... .`，约 220 个用例，`-race` 全绿）：

- 两个模型适配器：SSE 流式文本、函数调用往返（含 `tool` 消息回传）、HTTP 错误与重试
- 配置存储：深拷贝隔离、默认种子、持久化往返、损坏文件自愈、脱敏导出不污染运行态
- 端到端：ChatService → ADK Runner → llmagent → 假 SSE 模型服务器 → 内置工具调用循环 → SQLite 持久化
- 稳定性：并发起停与取消、跨轮次取消登记、知识索引增量与过期行清理、路径穿越与符号链接逃逸、技能物化隔离与原子写、配置更新原子提交、记忆并发去重、任务计划跨轮回读
- 能力：plan_todos 任务计划（清单卡 + 置顶进度条）、停止序列透传、429/Retry-After 重试、消息排队、附件即时渲染与重生保留

## 发布就绪检查（第 29/42/63/177 轮多轮收官验证，持续维护）

- 后端全量测试（agentkit / config / mcpmgr / memory / services，约 225 用例）+ `-race` 全部通过（macOS / Windows 双平台）
- 前端 `tsc` 严格类型检查 + 生产构建零错误
- `wails3 build` 生产打包成功，启动烟雾通过（进程存活、窗口创建、托盘/快捷键注册无报错、干净退出）
- 浏览器视觉回归（`frontend/stub/`）覆盖确认卡片交互、远程 Agent 管理、团队拓扑、设置页全部新卡片与深色主题
- 双子代理代码审查（Standards + Spec）已执行并修复全部硬性发现
- 一键复检：`./scripts/verify.sh` → `VERIFY: ALL GREEN`

## 生产使用

### 打包与发布
```bash
wails3 build && wails3 package   # 本地产物 bin/<APP_NAME>.app（版本为 dev）
```

正式发布只走 GitHub Actions：推一个 `v*` tag 即自动构建并发布。

```bash
task release:next          # 依据 conventional commits 建议下一个版本号 + 预览发布说明
git tag -a v0.5.0 -m "..." # tag 消息会被当作发布说明（留空则自动按提交归类）
git push origin v0.5.0     # 触发 .github/workflows/release.yml
```

自动修复流水线合入的修复由同一条**发布列车**自动发版：main CI 绿后立即
评估，按 30 分钟窗口打 patch tag（`priority:p0` 为 15 分钟），发布成功后
自动关闭本批 issue。无需本地会话在线，也不占用 15 分钟定时轮。

- **当前发布范围：仅 Windows 64 位**。维护者的测试环境只有 Windows，未经验证的 Linux/macOS 产物配合自动更新等于给用户装一个坏版本；这两个平台的构建配置（AppImage + deb、universal `.app` + dmg）保留在 git 历史中，测试条件具备后按 `0.MINOR` 规则补发
- **版本号唯一来源是 git tag**：CI 用 `-ldflags "-X main.Version=X.Y.Z"` 注入二进制，并用 `scripts/set-version.sh` 重写 `build/config.yml` 后由 `wails3 update build-assets` 派生 exe 元数据。无 tag 的构建版本为 `dev`
- 构建使用 `-tags production -trimpath -ldflags "-w -s"`（Windows 追加 `-H windowsgui`）
- 产物（每次发布原子出齐，缺一不发）：`AgentMate-windows-amd64.exe`（**免安装，下载即用**）、`SHA256SUMS`、`manifest.json`
- 不发安装包、也不发 zip：那一个 exe 既是用户下载的东西，也是自更新替换的目标，所以把它放在可写目录（`D:\Apps`、下载目录、U 盘）时自更新直接可用
- 详见 `docs/adr/0001-git-tag-as-version-source.md` 与 `docs/adr/0002-in-app-updater-and-dual-release-artifacts.md`

#### 版本号规则（0.x 阶段）

首个发布版本为 **v0.5.0**（承接此前 0.4.0 的内部版本号，保证任何已装的 0.4.x 都能被判定为「可升级」）。1.0.0 之前只发 `0.MINOR.PATCH`：

| 变更内容 | 递增位 | 例子 |
|---|---|---|
| 新功能、用户可感知的行为变化、配置/数据结构调整、破坏性变更 | **MINOR**（+1，PATCH 归零） | `0.5.0 → 0.6.0` |
| 仅修复、性能、文案、依赖升级、无新能力 | **PATCH**（+1） | `0.6.0 → 0.6.1` |

- `task release:next` 会按上面的规则读 conventional commits 给出建议值（`feat:` → minor，`fix:`/`perf:` → patch），可人工上调
- 0.x 期间破坏性变更**不另设标记**，一律用 MINOR 表示并在发布说明首行写清「不兼容：…」
- 一批工作合成一次发布，不要每个 commit 发版（自动检查 24h 一次，发得太密等于频繁打扰用户）
- **已发布的 tag 不移动、不重复发**；构建坏掉/产物缺失时用 `workflow_dispatch` 重跑同一 tag（`--clobber` 覆盖资产），不递增版本号
- 不发预发布（`releases/latest` 会跳过 prerelease，等于用户看不到）；需要灰度就先发一个 PATCH
- 进入 1.0.0 的条件：配置/数据 schema 稳定、对外 A2A 协议稳定，且升级链路已连续跑通若干版本

### 应用内更新
- 「设置 → 关于」内联更新卡片：检查 / 下载并安装 / 重启完成更新 / 跳过此版本 / 兼容「打开下载页」
- 启动后约 10 秒静默检查一次，之后每 24 小时一次；**只提示，不自动安装**，可在同处关闭
- 更新通道：GitHub Releases 上的 `manifest.json`（`releases/latest/download/manifest.json`），经 Wails 原生 `app.Updater` + endpoint provider 消费
- 能否自动安装由**安装形态**决定：Windows 独立 exe 在可写目录时可自替换；放在只读位置则只提示下载（updater 无提权路径）。macOS `.app` 与 Linux `.AppImage` 同理，但这两个平台当前未发布产物
- 清单签名：`updatePublicKeyB64`（`internal/services/updateservice.go`）钉公钥后**强制**校验签名；私钥放 CI secret `UPDATER_SIGNING_KEY`。未钉公钥时退化为仅校验清单摘要

### 稳定性与自愈
- 所有回合默认不限制时长，长任务不会被整轮墙钟时间误杀；模型流持续无数据由空闲看门狗中断，供应商网络/429/502/503/504 自动重试 5 次
- 运行期 panic 被 recover 并转为界面错误事件，应用不崩溃
- 会话数据库损坏时启动自动备份（`sessions.db.corrupt-<时间戳>`）并重建，保证可启动
- 全部后端路径有单测覆盖，`internal/...` 通过 `-race`

### 安全
- `config.json`（含 API Key）权限 0600；API Key 不出本机（仅直连所配置的供应商）
- 模型输出经 react-markdown 渲染，默认不执行原始 HTML；链接新窗口打开
- 产物预览严格限制在工作区目录内（目录穿越防护、大小上限）
- 每个任务有独立工作区；首轮开始后目录锁定，删除会话不会删除任务文件

### 运维与诊断
- 日志：`~/Library/Application Support/agentmate/logs/app.log`（5MB 自动轮转）
- 故障排查：先看 app.log；会话库异常时检查 `sessions.db.corrupt-*` 备份
- 数据备份：直接复制 `~/Library/Application Support/agentmate/`（配置 + 会话 + Skills）

### 已知边界
- 语音识别依赖 Web Speech API（macOS WKWebView 可用性随系统版本变化，不支持时按钮自动禁用）
- 长会话渲染上限 200 条（完整历史已持久化，可导出 Markdown 查看）
- MCP OAuth 令牌缓存在本机 config.json（0600），不随任何导出携带；丢失后重新授权即可
- 团队负责人必须为本地 Agent（远程 A2A 成员仅作为可转移的子代理参与）
- HTTP 代理设置在启动时注入，修改后需重启应用生效
- 结果区提供文件级变更、行级 diff（基线内容快照对比，二进制/超大文件自动降级为说明文案）与现有格式预览，不提供 Office 在线预览或网页运行预览
- `@` 文件引用只读取当前会话工作区内的文本文件：单文件 48KB、单轮 96KB、最多 8 个；二进制、越界路径与缺失文件不会注入，改为提示模型使用文件工具
- 项目/用户 `.agents/skills` 为只读发现，编辑需直接修改对应目录的 SKILL.md；应用内保存/删除会被拒绝
- **产物未做付费签名**：Windows 会触发 SmartScreen 提示；macOS 未公证，首次打开需右键→「打开」，且自更新替换后的 `.app` 可能被 Gatekeeper 二次拦截（updater 原样搬运字节，不重新签名）
- **`.deb` 安装版与「放在只读目录的 Windows exe」不支持自动安装更新**（updater 无提权路径），只提示并跳转下载
- Windows 不提供安装程序：没有卸载项、开始菜单快捷方式与文件关联；把 exe 放进可写目录（如 `D:\Apps`）即可获得自更新能力
- **不支持降级**：自动更新只升不降；新版本写过 config/sessions 后手动回退旧版，旧程序对未知字段不做守卫（`backupSchemaVersion` 只用于备份导入）

## 数据存储

| 内容 | 位置（macOS） |
| --- | --- |
| 配置（供应商/Agent/团队/Skills/MCP/会话元数据） | `~/Library/Application Support/agentmate/config.json` |
| Skills 物化目录 | `~/Library/Application Support/agentmate/skills/` |
| ADK 会话历史（SQLite） | `~/Library/Application Support/agentmate/sessions.db` |
| 任务快照基线（按会话 ID，原子 JSON） | `~/Library/Application Support/agentmate/task-snapshots/` |
| 自动创建的任务工作区 | `<全局工作区>/tasks/<sessionID>/` |

## 目录结构

```
main.go                    # 应用入口：服务注册、窗口、托盘+全局快捷键、SQLite 会话库、
                           # 知识索引、代理注入、A2A 自启动
internal/
  config/                  # 配置模型与 JSON 存储（0600；含 MCP OAuth 令牌缓存、默认 Skills 种子）
  agentkit/                # ADK 集成核心
    llm.go                 #   model.LLM 适配器（OpenAI 兼容 / Anthropic）
    kit.go                 #   Agent/团队构建、内置工具装配、提示词模板变量、SanitizeAgentName
    semantic.go            #   知识库向量索引（SQLite 增量）+ OpenAI 兼容 /embeddings 客户端
    knowledge.go           #   search_knowledge 工具（语义+关键词混合检索）
    remote.go              #   A2A 远程 Agent（官方 a2a-go）+ Agent Card 解析
    guardrail.go           #   密钥脱敏 / 提示注入检测
  mcpmgr/                  # MCP 服务器生命周期（连接状态、工具发现）
    auth.go                #   Bearer / API Key / OAuth 2.1 授权码+PKCE 传输层
  memory/                  # 长期记忆（ADK memory.Service，SQLite）
  services/                # Wails 服务（前端可调用 RPC）
    chatservice.go           #   共享类型/构造/审计/运行生命周期
    chatservice_sessions.go  #   会话 CRUD、项目、改绑守卫、分叉
    chatservice_turn.go      #   Send/ConfirmToolCall/runTurn/autoTitle（护栏应用点）
    chatservice_render.go    #   事件渲染、目标与 @ 路由解析
    chatservice_export.go    #   Markdown/HTML 导出
    chatservice_stats.go     #   长期记忆、会话统计
    task_workspace.go      #   任务目录校验、会话级改绑锁与工作区 RPC
    workspace_snapshot.go  #   任务快照基线、产物与文件级变更
    artifactservice.go     #   会话级文件列表/预览与安全扫描
    a2aservice.go          #   A2A 服务端（对外发布本地 Agent）
    app.go                 #   AppInfo、配置备份导出/导入、Agent 分享、日志诊断
frontend/
  src/pages/               # 对话 / Agents(含远程) / 团队 / Skills / MCP / 模型 / 设置
  src/components/ui/       # shadcn/ui 组件
  src/components/ToolCallCard.tsx  # 工具卡片 + 产物预览 + 路径提取
  src/components/TaskWorkspacePanel.tsx # 右侧结果区：产物/全部文件/变更
  stub/                    # 浏览器视觉回归桩（setTransport IPC 数据桩，npm run stub）
  bindings/                # wails3 生成的 TS 绑定（构建时自动再生成，勿手改）
build/                     # 各平台打包资源（Info.plist / 图标 / Taskfile）
AGENTS.md                  # 面向编码代理的工程指南（命令/机制/约定）
```
