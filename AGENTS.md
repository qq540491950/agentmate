# AGENTS.md — AgentMate 工程指南

面向在本仓库工作的编码代理。功能总览、架构与完整边界见 [README.md](README.md)，迭代记录见 [docs/iteration-log.md](docs/iteration-log.md)，术语见 [GLOSSARY.md](GLOSSARY.md)。

本地 AI Agent 桌面应用：**Google ADK Go v2**（运行时）+ **Wails v3**（桌面壳）+ **React/TS + shadcn/ui**（前端）。

## 常用命令

一键全量验证（测试 + race + 前端 + 打包）：`./scripts/verify.sh`。

```bash
go test ./internal/...                     # 后端全量（提交前必须全绿）
go test -race ./internal/...               # 改并发/数据库后必跑
go vet ./internal/... .
cd frontend && npx tsc --noEmit && npm test && npm run build
```

```bash
wails3 generate bindings -f '-tags production' -clean=true -ts -i   # 须在仓库根目录
wails3 build        # 绑定生成 + 前端构建 + Go 编译
wails3 package      # 打包 macOS .app
cd frontend && npm run stub   # 视觉回归桩（:9246/stub/index.html）
```

- 绑定由构建自动重建（`-clean=true`），**勿手改**。仅在仓库根目录生成；子目录执行会产 `bindings/{time,gorm.io,…}` 垃圾目录（可删），但 `bindings/github.com/`（wails-typed-events）勿删。依赖包绑定已被 `.gitignore` 排除。
- 方法 ID = FNV-1a32(`agentmate/internal/services.<Service>.<Method>`)，生成器自动处理。

## 目录地图

完整目录见 [README.md](README.md#目录结构)。

```
main.go                   # 装配：服务、窗口、托盘/快捷键、SQLite、知识索引
internal/agentkit/        # ADK 集成：LLM 适配器、Agent/团队、语义检索、A2A 客户端、护栏
internal/mcpmgr/          # MCP 生命周期与认证（Bearer / API Key / OAuth 2.1）
internal/memory/          # 长期记忆（ADK memory.Service，SQLite）
internal/services/        # Wails RPC；chatservice_* 为对话引擎，task_workspace + workspace_snapshot + artifactservice 为任务工作区
frontend/src/pages/       # Chat / Agents / Teams / Skills / MCP / Models / Settings
frontend/src/components/TaskWorkspacePanel.tsx  # 右侧任务结果区
frontend/stub/            # 浏览器视觉回归桩（setTransport + window.__emit）
frontend/bindings/        # wails3 生成绑定（勿手改）
```

## 关键机制（改动前必读）

- **对话引擎**：`ChatService.runTurn` 是 Send / Regenerate / EditAndResend / ConfirmToolCall 的共用引擎；改 `ChatStreamEvent` 时同步改前端 `ChatPage.tsx` 的 switch 与历史渲染 `renderEvent`。
- **HITL 确认**：ADK 以 `adk_request_confirmation` FunctionCall（带 `LongRunningToolIDs`）暂停；应答为 `{ID, Name, Response: {"confirmed": bool}}` FunctionResponse；ADK 恢复后对模型隐藏该往返。
- **会话库并发**：`sessions.db` 用 WAL + busy_timeout(5000) + **单连接**（`SetMaxOpenConns(1)`）；不要移除单连接，否则并行会话写库触发 `SQLITE_BUSY`。
- **全局代理**：`agentkit.ApplyGlobalProxy` 启动时改写 `http.DefaultTransport.Proxy`；新增出站客户端必须走 DefaultTransport，或在代理测试中钉住覆盖。
- **A2A 双向**：客户端 `agentkit.BuildRemoteAgent`（Agent Card 每次调用重解析）；服务端 `a2aservice.go`（测试同配方：`a2asrv.NewHandler + adka2a(v2).NewExecutor`）。Card 必须含 `SupportedInterfaces`（JSONRPC），否则报 "no supported interfaces"。`SaveAgent`（替换）/`DeleteAgent` 下线已发布服务器；OAuth 回调端口惰性重绑（授权结束必释放）。
- **假模型服务器**：OpenAI 兼容桩必须同时处理 `"stream": false` 的普通 JSON 与 SSE（A2A executor 走非流式）。
- **知识库索引**：`knowledge.index.db` 独立于 sessions.db；按 mtime 增量；重嵌入前先删旧行（否则缩小文件留过期分块）；换 Embedding 模型全量重建；故障回退关键词。
- **配置读取**：`Store.Get()` 返回深拷贝，可随意改；`Settings()` 用于全局设置热路径。`ExportConfig(false)` 剥离供应商 API Key 与 MCP AuthToken；`MCPTokens` 任何导出都不带。
- **取消登记**：每轮在 `runs[sessionID]` 登记 `*runHandle`，释放时只在指针仍属于自己时删除。
- **会话元数据**：Rename / SetModel / Archive / Pin 统一走 `mutateSession`；`DeleteSession` 与 `Regenerate/EditAndResend` 先 `Cancel` 再动历史。
- **会话级模型覆盖**：`ChatSession.ModelOverride` 经 `agentkit.EffectiveModel` 统一钳制；运行与统计计费共用。标题提炼默认走全局主模型，与会话覆盖解耦。
- **@ 文件上下文与技能发现**：`@path` / `@"quoted path"` 由 `chat_context.go` 从会话的有效工作区读取，按轮注入（单文件 48KB / 单轮 96KB / ≤8 个，越界与二进制不读）；`/skill` 在 `config.EffectiveSkills`（项目 > 用户 `~/.agents/skills` > 应用）上匹配，`ChatService.EffectiveSkills` 与 `ArtifactService.ListContextPaths` 供前端按会话/草稿项目解析菜单。
- **任务级工作区**：`NewSession` 与 `NewSessionWithWorkspace` 均创建任务目录；`EffectiveWorkspace` 是文件工具、`{{workspace}}` 与预览的唯一后端解析入口；首轮后锁定，活动任务不能重复占用同一显式目录；发送/确认/改绑共用会话级 workspace 锁。知识库/Skills/导出仍用全局工作区。
- **任务产物快照**：基线存应用数据目录 `task-snapshots/<sessionID>.json`（版本化、原子写，不进 sessions.db）；按存在性/大小/mtime 算新增、修改、删除；跳过隐藏/VCS/依赖目录与符号链接（5000 文件 / 200 条上限）；删会话清基线但保留任务文件，分叉共享工作区但独立基线。
- **plan_todos**：状态存 ADK session state 键 `agentmate.todos`，全量替换（省略参数=只读，空数组=清空）；前端从 tool_result 渲染清单卡与置顶条。
- **autoTitle**：默认开启（`Settings.AutoTitle` 显式 false 才关闭）；首轮后异步 30s 侧调用，默认用全局主模型，可用 `Settings.TitleProviderID/TitleModel` 指定；仅替换首轮兜底标题，goroutine 自带 recover。历史会话可经 `ChatService.RefineSessionTitle` 手动重提炼。
- **单轮超时**：未配置 `MaxTurnSeconds` 时默认 600 秒；`runTurn` 的 recover 把 panic 转为 `error` + `done`。
- **桌面壳**：单实例锁名 = `build/config.yml` 的 `productIdentifier`（不可改），二次启动转发激活后退出。Windows 更新残留 `<exe>.old.*` / `.bak` 由 `updateleftover.go` 在锁后清扫（30 秒内新文件不碰，重试至 10 分钟）。

## Issue 自动修复流水线

自动修复不直接改 `main`（只由 Integrator 推进）：Dispatcher / Worker / Integrator 在本地定时任务运行，Worker 一律在 `git worktree` + 分支 + PR 中工作，主 checkout 不参与。Release 是 GitHub Actions 发布列车（`.github/workflows/release.yml`），本地轮次不打 tag、不等 CI。流程见 [docs/issue-pipeline.md](docs/issue-pipeline.md)，执行步骤见 [docs/issue-fixer-prompt.md](docs/issue-fixer-prompt.md)。

- 全局 WIP ≤ 3，同一 module 同时 ≤ 1；单轮只做一个短动作，不在进程内等 CI。
- 共享文件只由 Integrator 改：`docs/iteration-log.md`、`frontend/bindings/**`、`go.mod` / `go.sum`、`frontend/package-lock.json`、版本号与构建产物。
- 流水线的迭代记录写 `docs/iterations/issue-<N>-<slug>.md` 片段，由 Integrator 插入；人工提交可直接追加。
- 运行手册自身只通过独立的工具链 issue/PR 修改。
- 辅助脚本：`scripts/issue-*.sh`、`scripts/release-train.sh`。

## 约定

- 测试与被测代码同包同目录（`*_test.go`）；e2e 用 `httptest` 假服务器（fake SSE 模型 / 假授权服务器 / 内存 A2A），不依赖外网。
- UI 文案中文；错误经 `errText()` 透传到 toast。
- 每轮功能迭代在 `docs/iteration-log.md` 顶层「视觉测试与体验设计」章节顶部追加记录（含依据、实现要点、测试证据）；README 只保留概览与「质量与验证」。
- 提交信息用约定式前缀 + scope（如 `feat(chat): 主题 — 要点`），正文列实现与测试清单。

## 已知边界

- 长会话渲染上限 200 条（历史完整持久化，可导出）。
- MCP OAuth 令牌缓存在 config.json（0600），脱敏导出不带。
- 团队负责人必须为本地 Agent（远程 A2A 成员仅作为可转移子代理）。
- 结果区支持行级 diff（基线内容快照 ≤256KB/文件、32MB/会话），不支持 Office 在线预览或网页运行预览。

更多边界见 [README.md](README.md#已知边界)。
