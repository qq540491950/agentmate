# Issue 提交模板与多 Issue 处理约定

本文件用于测试机反馈问题。GitHub 提交页提供两种表单：

- [`bug_report.yml`](../.github/ISSUE_TEMPLATE/bug_report.yml)：Bug 报告；
- [`feature_request.yml`](../.github/ISSUE_TEMPLATE/feature_request.yml)：功能需求
  （enhancement）。

下面保留一份 Bug 的 Markdown 版本，便于在别处复制或离线填写。

**推荐从应用内“设置 → 关于 → 反馈问题”进入**：会自动带上版本与平台信息，
不需要手动填写版本号。直接在 GitHub 提交时版本可以留空，自动流程会按 issue
创建时间推断当时最新的发布版本。

自动处理的执行流程见
[`docs/issue-fixer-prompt.md`](issue-fixer-prompt.md)。

## 标题格式

```text
[模块] 简短现象
```

模块可用：`chat`、`agents`、`teams`、`skills`、`mcp`、`models`、`settings`、
`workspace`、`update`、`other`。

示例：

- `[chat] 发送消息后界面一直停在“运行中”`
- `[workspace] 任务结果区不显示新增文件`
- `[mcp] OAuth 授权完成后服务器仍是断开状态`
- `[update] 检查更新提示版本解析失败`

## 填写内容

| 字段 | 说明 |
|---|---|
| **版本 / commit** | 可选。应用内“反馈问题”入口会自动填入；留空则由自动流程按 issue 创建时间推断当时最新的发布版本 |
| **测试环境** | Windows 版本与架构，以及相关的模型、MCP、Agent、工作区配置 |
| **复现步骤** | 从打开应用开始的最小步骤，尽量稳定复现 |
| **期望结果** | 正确应该发生什么 |
| **实际结果** | 实际发生了什么，包括错误提示原文 |
| **复现频率** | 必现 / 经常 / 偶现 / 只出现一次 |

强烈建议补充：错误前后各 30～50 行日志、截图、前置条件、最近升级或配置改动。

## Markdown 可复制模板

```markdown
## 版本
（可留空；应用内“反馈问题”入口会自动填入）

## 测试环境
Windows 11 23H2 x64
模型：OpenAI 兼容 / gpt-4o
MCP：filesystem
工作区：D:\workbuddy-test

## 复现步骤
1. 打开应用，新建会话
2. 发送“列出当前目录文件”
3. 在工具确认弹窗点“允许”

## 期望结果
工具执行完成后返回文件列表，运行状态结束。

## 实际结果
界面一直显示“运行中”，超过 2 分钟不结束。

## 复现频率
必现（3/3）

## 日志 / 截图
（粘贴错误日志或拖入截图；提交前请脱敏）

## 前置条件
（是否只在特定会话、工作区、模型或 MCP 下出现；最近是否升级或改过配置）
```

## 功能需求（enhancement）

功能需求使用 `feature_request.yml`，或手动给 issue 加 `enhancement` 标签。自动
流程也会实现功能需求，但需要足够明确；缺少关键信息时会先评论询问并标
`needs-info`。

必填：

| 字段 | 说明 |
|---|---|
| **目标 / 背景** | 希望解决什么问题，为什么需要 |
| **期望行为 / 验收标准** | 满足哪些条件算完成，尽量具体 |
| **影响范围** | 涉及页面、模块、Agent、MCP、设置等 |

建议补充：可选方案、交互草图、边界条件、是否可拆成多个 issue。

```markdown
## 目标
希望任务结果区支持按文件类型筛选。

## 期望行为 / 验收标准
1. 结果区顶部出现“类型”筛选器；
2. 选择 Markdown 后只显示 .md 文件；
3. 清空筛选恢复全部文件。

## 影响范围
任务结果区（TaskWorkspacePanel）与相关前端组件。

## 可选方案 / 备注
也可以做成搜索框语法，但首版先做下拉筛选。
```

## 多个 Issue 一起提交时怎么处理

### 1. 流水线并发，不再是单票 FIFO

自动流程每 15 分钟做一次短动作，多个 issue 可以同时在途：

- 不同 module 可以同时在途；编码动作按轮次串行，CI 与集成并行；
- 同一 module 同时最多一个；
- 全局最多 3 个自动 PR 在途；
- 每个修复在独立 `git worktree` 完成并开 PR，由 Integrator 串行合入
  `main`，不会互相覆盖；
- 一个 issue 的 CI 等待或失败不会让整条队列停下。

其余 issue 保持 open 排队，不会丢失。

### 2. 紧急问题可以插队

默认按 `priority:p0` → 创建时间排序。如果是阻塞性、会导致无法继续测试的
问题：

- 在标题前加 `[P0]`，例如 `[P0] [chat] 应用启动即崩溃`；
- 或在 issue 里明确写“阻塞测试”；
- 提交时加 `priority:p0` 标签（`github-bug-relay` 会自动加）。

P0 会被 Dispatcher 优先选取，发版可走最短 15 分钟窗口。

### 3. 重复 Issue

后提交的重复问题会被标记 `duplicate`，并评论引用先提交的 issue，不重复修复。
先提交的那个修完后，重复 issue 一起关闭。

### 4. 同一根因的多个 Issue

如果多个 issue 实际是同一根因（例如同一个接口导致多个页面报错），会合并为
一个修复提交，提交信息同时引用多个编号（`refs #12, refs #13`），发版成功后
一起评论并关闭。

无法确定是否同一根因时，默认按独立问题逐个处理，避免误合并。

### 5. 批量发版节奏

当前默认**每 60 分钟最多打一个 patch tag**。修复合入 main 后先标
`agent:pending-release`，等待下一个发版窗口；同一窗口内的所有修复合并成一个
patch 版本（例如 `v0.5.1`）。

发版开始时会对当时的 pending 队列做快照：

- 只关闭修复提交是该 tag 祖先的 issue；
- 发版期间新合入的 pending 留到下一批，不会被误关；
- 测试机最多约 1 小时后可以更新；
- 没有待发版 issue 的窗口不会产生空 Release。
- `priority:p0` 可以走最短 15 分钟窗口。

### 6. 失败不阻塞队列

- 无法复现或信息不足：标 `needs-info`，评论请求补充信息；作者补充后自动
  回队，不阻塞其他 issue。
- 修复或 CI 失败：标 `agent:blocked` + `agent:retryable` 或
  `agent:permanent`。可重试的 30 分钟后自动回队；永久失败等人工处理，但
  不会阻塞其他 issue。
- 发版失败：只影响本批快照里的 issue，标 `agent:retryable`，新 pending
  不受影响；不会连续打 tag。

### 7. 提交建议

- **一个 issue 只描述一个问题**，不要把多个无关问题塞进同一个 issue。
- 如果几个症状确实来自同一根因，可以合并描述，并注明“同一根因”。
- 先提阻塞性问题，再提体验问题；每个 issue 尽量独立可复现。

## 自动流程使用的标签

| 标签 | 含义 |
|---|---|
| `module:<模块>` | 路由标签，决定 module 互斥与并行车道 |
| `priority:p0` | 阻塞性问题，优先处理并走最短发版窗口 |
| `ready-for-agent` | enhancement 验收标准明确，可以进入实现队列 |
| `agent:processing` | 已认领，编码或 PR 阶段 |
| `agent:pending-release` | 已合入 main，等待批量发版 |
| `agent:fixed` | 已发布并关闭 |
| `needs-info` | 信息不足，等待补充 |
| `agent:blocked` | 修复、CI 或发版失败 |
| `agent:retryable` | 失败可自动重试，30 分钟后回队 |
| `agent:permanent` | 失败需要人工，不再自动重试 |
| `bug` | 由 Bug 表单自动添加 |
| `enhancement` | 功能需求；验收明确时才进入实现 |
| `duplicate` | 重复 issue，引用先提交的编号 |

`agent-fix`、`agent:ready-to-merge` 是自动修复 PR 的标签，不出现在 issue 上。

请不要手动修改 `agent:*`、`module:*`、`priority:*` 标签，否则可能导致重复
处理或漏处理。

## 注意事项

1. 仓库是公开的，日志中如有 API Key、Token、账号、个人路径，请先脱敏。
2. 功能需求请标 `enhancement` 或使用功能需求表单；自动流程也会实现，需求不明确
   时会先评论询问。
3. 关闭后如果仍然复现，直接重开并补充新的日志和步骤；带 `agent:fixed` 的
   issue 重开会重新进入队列。
4. 大日志只截取错误前后关键片段，不要把整个日志文件直接贴入 issue。
