# Issue 存档（已从 GitHub 删除）

本目录存放 2026-11-02 从 `qq540491950/go-workbuddy` 删除的 15 个 GitHub issue 的原文副本。

## 背景

这 15 个 issue 不是用户反馈，而是**规格驱动工作流的产物**：一次代码审查产出「规格母票 + 子票」，母票写 `Problem Statement / Solution / User Stories / Implementation Decisions / Testing Decisions / Out of Scope`，子票写 `What to build` + `Acceptance criteria`，统一打 `ready-for-agent` 标签，实施完即关闭。

归档时（2026-11-02）的状态是：15 个 issue 中 10 个 open、5 个 closed，而复核代码与迭代日志后确认**这 10 个 open 全部对应已完成的工作**——A 批子票实现后漏关（且验收复选框未勾），B 批子票已关闭但母票漏关。由于已无任何待办语义，经确认后：先把原文与结论归档到本目录并提交进 git，再从 GitHub 删除，让 `ready-for-agent` 标签重新拥有「真的等待实施」的含义。

`docs/iteration-log.md` 与历史提交信息中仍会出现 `#1`、`#2–#9`、`#10` 等编号，它们现在指向本目录的对应文件，可用下表换算。GitHub 上的这些编号已作废，旧链接会是 404。

## 编号对照与结论

| Issue | 标题 | 归档时状态 | 类型 | 结论 | 存档文件 |
| --- | --- | --- | --- | --- | --- |
| #1 | [Spec] 加固与一致性修复方案：平台正确性 / 会话一致性 / 资源安全 / 导入体验 | open | 母票（Spec） | 子票 #2–#9 全部实现并在代码中复核通过；本票本身无需再实施。 | [issue-001-spec-hardening-consistency.md](issue-001-spec-hardening-consistency.md) |
| #2 | 01: Windows OAuth 授权修复 | open | 子票 | 已实现：浏览器启动命令提取为纯函数，Windows 走 `rundll32 url.dll,FileProtocolHandler`，不再经 cmd 解析（cmd 会把授权 URL 里第一个 `&` 当命令分隔符）。 | [issue-002-01-windows-oauth.md](issue-002-01-windows-oauth.md) |
| #3 | 02: 全局代理正确性（环境变量回落 + 回环直连） | open | 子票 | 已实现：空配置委托 `http.ProxyFromEnvironment`；已配置代理时 127.0.0.1 / localhost / ::1 直连。 | [issue-003-02-global-proxy.md](issue-003-02-global-proxy.md) |
| #4 | 03: 测试可移植性（句柄清理 + 平台条件断言） | open | 子票 | 已实现：测试内 gorm / 会话库 / 知识索引 / 旋转日志句柄统一注册 Cleanup；POSIX 权限断言在 Windows 跳过。Windows 下 `go test ./internal/... .` 由 5 失败转全绿。 | [issue-004-03-test-portability.md](issue-004-03-test-portability.md) |
| #5 | 04: 会话历史一致性（动历史前等待旧轮退出） | open | 子票 | 已实现：运行句柄携带完成信号，Regenerate / EditAndResend / DeleteSession 走「取消并等待（10s 兜底）」；同时修复 ADK Delete 只删 sessions 行留下的事件孤儿行。 | [issue-005-04-session-history-consistency.md](issue-005-04-session-history-consistency.md) |
| #6 | 05: 检索一致性（知识索引清空 / 部分失败保留 + 记忆 LIKE 转义） | open | 子票 | 已实现：knowledge 目录整体删除后索引清空；嵌入部分失败保留成功文件并聚合报错点名失败文件；记忆 LIKE 通配符转义与会话搜索统一（`ESCAPE '\'`）。 | [issue-006-05-search-consistency.md](issue-006-05-search-consistency.md) |
| #7 | 06: 工作区 IO 有界化（读截断 + 写上限） | open | 子票 | 已实现：`ReadBoundedFile` / `WriteBoundedFile`——读取到上限即停并在完整 UTF-8 字符处回退，写文件 6MB 上限拒绝，产物预览文本分支同走有界读取。 | [issue-007-06-workspace-io-bounds.md](issue-007-06-workspace-io-bounds.md) |
| #8 | 07: 导入体验（Agent 悬空引用清洗 + 技能重名统一） | open | 子票 | 已实现：`ImportAgent` 返回 `{agent, warnings}`，悬空技能 / MCP 引用导入时剥离并在前端 toast 警告；技能导入去重键与物化目录名统一为 `MaterializedSkillName`。 | [issue-008-07-import-experience.md](issue-008-07-import-experience.md) |
| #9 | 08: MCP stdio 进程回收 | open | 子票 | 已实现：`Manager.CloseAll` 幂等全量断开（逐个关闭受控传输并清理状态），接入应用关闭钩子，stdio MCP 子进程不再孤儿化。 | [issue-009-08-mcp-stdio-reclaim.md](issue-009-08-mcp-stdio-reclaim.md) |
| #10 | [Spec] 任务级工作区与右侧产物验收工作台 | open | 母票（Spec） | 子票 #11–#15 全部实现并已于 2026-10-31 关闭（关闭评论记录 `3f96664..761decf`）；本票仅漏关。 | [issue-010-spec-task-workspace-artifacts.md](issue-010-spec-task-workspace-artifacts.md) |
| #11 | 01: 独立任务目录与工作区绑定 | closed | 子票 | 已实现并已于 2026-10-31 21:21 关闭（关闭评论：`Implemented in commits 3f96664..761decf`）。 | [issue-011-01-task-workspace-isolation.md](issue-011-01-task-workspace-isolation.md) |
| #12 | 02: 新建任务目录选择与首轮前改绑 | closed | 子票 | 已实现并已于 2026-10-31 21:21 关闭（关闭评论：`Implemented in commits 3f96664..761decf`）。 | [issue-012-02-task-dir-selection.md](issue-012-02-task-dir-selection.md) |
| #13 | 03: 右侧任务文件浏览与预览 | closed | 子票 | 已实现并已于 2026-10-31 21:21 关闭（关闭评论：`Implemented in commits 3f96664..761decf`）。 | [issue-013-03-artifact-browser-preview.md](issue-013-03-artifact-browser-preview.md) |
| #14 | 04: 产物与文件级变更验收 | closed | 子票 | 已实现并已于 2026-10-31 21:21 关闭（关闭评论：`Implemented in commits 3f96664..761decf`）。 | [issue-014-04-artifact-change-review.md](issue-014-04-artifact-change-review.md) |
| #15 | 05: 任务生命周期与并发一致性 | closed | 子票 | 已实现并已于 2026-10-31 21:21 关闭（关闭评论：`Implemented in commits 3f96664..761decf`）。 | [issue-015-05-task-lifecycle-concurrency.md](issue-015-05-task-lifecycle-concurrency.md) |

## 两批规格

- **A 批（issue #1 + #2–#9，2026-10-31 16:33–17:18）**：加固与一致性修复——Windows OAuth、全局代理、测试可移植性、会话历史一致性、检索一致性、工作区 IO 有界化、导入体验、MCP stdio 进程回收。实现记录见迭代日志第一百四十三轮（`docs/iteration-log.md#L447-L463`）。
- **B 批（issue #10 + #11–#15，2026-10-31 18:24–19:54）**：任务级工作区与右侧产物验收工作台。子票关闭评论记录实现区间 `3f96664..761decf`，实现记录见迭代日志第一百四十四轮（`docs/iteration-log.md#L431-L445`）。

## 延后项在哪里

两张母票的 `Out of Scope` 段列出了刻意延后的方向（chatservice/聊天页拆分、A2A 鉴权、索引加锁与检索性能、会话元数据迁移 SQLite、行级 diff 之外的三方合并/回滚、Office 与网页预览、文件系统 watcher、任务分享与云同步等）。这些条目**没有**单独开票跟踪，现状与边界以正文为准：

- A 批：[issue-001-spec-hardening-consistency.md](issue-001-spec-hardening-consistency.md) 的 `Out of Scope` 段
- B 批：[issue-010-spec-task-workspace-artifacts.md](issue-010-spec-task-workspace-artifacts.md) 的 `Out of Scope` 段

## 其他同类规格

`docs/specs/chat-first-shell.md`（对话优先外壳与设置中心）是同一工作流的另一份规格，但它从未提成 issue，因此不在本目录的存档范围内。

## 恢复方式

GitHub 删除 issue 后编号作废、普通用户无法恢复。如需沿用某个编号的规格，请从对应存档文件的「原文」段取正文，新建 issue 时会得到新编号。

## 操作记录

- 2026-11-02：抓取 15 个 issue 的正文、评论、标签与时间戳 → 生成本目录 → 提交归档 → 从 GitHub 删除 15 个 issue（含 5 个已关闭的）。
