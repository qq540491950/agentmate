---
name: github-bug-relay
description: 手动调用的提票工具：把用户报告的缺陷整理成 GitHub issue，交接给远端定时修复 agent，并在修复后回报。只提票，不在本地复现、验证或改代码，也不产生任何文件。仅用于本仓库 qq540491950/go-workbuddy。
disable-model-invocation: true
---

# 提票 → 交接 → 收回

**手动触发**：只有用户显式点名调用本 skill（`/github-bug-relay` 或「用 github-bug-relay 提这个 bug」）时才执行。用户只是提到 bug、抱怨功能坏了、或者要反馈问题时，不要自行启用本 skill，也不要主动建议启用。

本仓库的缺陷闭环分两侧，你只做**提交侧**：

- **提交侧（本 skill）**：把口述的问题变成一张远端能自动抓取的 issue。
- **修复侧**：[`docs/issue-fixer-prompt.md`](../../../docs/issue-fixer-prompt.md) —— 流水线模式：Dispatcher 每轮只做一个短动作（认领 / 集成 / 修复 / 开新票），Worker 在独立 worktree 修复并开 PR，Integrator 串行合入；Release 是 GitHub Actions 的发布列车，main CI 绿后自动打 patch tag、发布并评论关闭。设计与状态机见 [`docs/issue-pipeline.md`](../../../docs/issue-pipeline.md)。

## 铁律

- **只提票**。不在本机复现问题、不验证 bug 是否真实存在、不启动应用、不跑测试或构建、不改代码——这些全是远端修复侧的活。
- **不产生任何文件**。不写临时正文、不写报告、不写脚本、不改仓库里的任何文件。全文只走 `--body` 内联或 stdin。
- 允许的动作只有三类：读仓库里已有的文档、`gh` 的只读查询、`gh issue create` 本身。
- 信息不足就列问题让用户补，不要自己去查证。

## 前提

- `gh auth status` 的登录账号必须是 `qq540491950`。修复侧**只抓作者为该账号的 open issue**，换账号提的票永远不会被处理——最容易踩的坑，先验再提。
- 执行者要能跑 shell（`gh`）。没有 shell 的执行者不适用本 skill。
- 字段定义与多票约定以 [`docs/issue-template.md`](../../../docs/issue-template.md) 为唯一事实源，本 skill 不复制。

## 提问预算

缺料就问，但问题要计数。两类问题的意义完全不同，混在一起数就会误判：

| 类型 | 例 | 是否计入「这条描述不成票」 |
|---|---|---|
| 模板缺口 | 版本？环境？必现还是偶现？ | 不计——常规收料 |
| 语义澄清 | 你具体点了哪个按钮？你期望它变成什么？这俩是不是同一个问题？ | **计入** |

- 每轮 ≤3 个问题，总量 **≤2 轮**。第 3 轮还在追细节，停手。
- 能自己查的不问：版本靠 `gh release list` 得出，不要再弹窗问一遍。
- 优先给选项而不是开放题：版本给候选 tag，频率给四档，模块给白名单。有交互提问工具就用（DSH 的 `ask_user_question`、Claude Code 的 `AskUserQuestion`），没有就正文列编号问题让用户一行回。
- 「未知」是合法答案。写进票里就算收料完成，不要追问第二遍。
- **语义澄清累计 ≥2 个仍答不出来 → 停止提票**。回一句「需要一段最小复现步骤（谁 → 点了什么 → 看到什么）」，等用户补完再来。宁可这次不提，也不要提一张注定被 `needs-info` 打回的票。

## 拆票判据

命中任一条就拆成多张票：

- 期望结果不止一条（「应该显示 X，且不应该崩」= 两个修复）
- 跨两个以上模块（`[chat]` 的票里冒出 workspace / MCP 的行为）
- 同一现象在不同路径各自出现，且不能确定同根因
- 修完要在两处不相关的地方分别验证

反过来：**同一根因的多个症状写进一张票**，注明「同一根因」。根因不明就不要合并，按独立票走。

拆分后的关联：

- 子票正文首行写 `parent #<母票号>`；母票正文列 `sub-issues: #a, #b`。
- 优先级继承：母票带 `[P0]`，子票也带 `[P0]`，并加 `priority:p0` 标签。
- 拆完默认**批量发版**，不再询问逐票还是批量；P0 由发布列车按 15 分钟窗口优先发版。

## 1. 收料

按 issue-template.md 的字段表从上到下收：版本/commit、测试环境、复现步骤、期望结果、实际结果、复现频率、日志/截图、前置条件。

- 版本自己查，别问「你是什么版本」当第一个问题：`gh release list --repo qq540491950/go-workbuddy --limit 20 --json tagName,publishedAt`，取最新 `v*` 给用户确认。查不到就留空——修复侧会按 issue 创建时间推断当时最新版本，不必卡住。
- 日志由用户粘贴。告诉用户去 `%APPDATA%\workbuddy-agent\logs\app.log` 取错误前后 30～50 行，你自己不去翻本机文件。
- 缺的字段按「提问预算」问：≤3 个/轮、总量 ≤2 轮、能给选项就给选项、未知合法。
- 问完语义仍不清（见提问预算末条）→ 停，不要提半成品。

完成判据：每个必填字段有值或写明「未知」，且没触发「语义澄清 ≥2 仍答不出」。

## 2. 定性 + 标题

- 标题 `[模块] 简短现象`，模块取 issue-template.md 的白名单，都不像就用 `other`。
- 阻塞测试、启动即崩溃：标题前置 `[P0] `。
- 功能建议：打 `enhancement` 而不是 `bug`，正文写明「自动流程只修 bug，需人工评估」。
- 按「拆票判据」确认这一条装得下同一个问题；同一根因的多症状合并，并注明「同一根因」。

完成判据：类别唯一、标题合规。

### 路由标签契约

提票时同时应用以下标签，供修复侧 Dispatcher 做 module 互斥与优先级排序：

| 标签 | 何时加 | 说明 |
|---|---|---|
| `module:<模块>` | 总是 | 模块取标题里的白名单值；不是标题的替代品，只是机器可读路由 |
| `priority:p0` | 标题带 `[P0]` 或正文写明“阻塞测试” | 修复侧优先处理，发版可走最短窗口 |
| `ready-for-agent` | enhancement 且验收标准明确 | 需求不清时不加，让修复侧先走 `needs-info` |

不要添加任何 `agent:*` 标签：`agent:processing`、`agent:pending-release`、
`agent:blocked` 等由修复侧维护。

## 3. 脱敏

仓库是公开的。删掉或替换：API Key、Token、cookie、`ghp_`/`gho_`/`sk-` 样式串、`Bearer` 头、账号邮箱、`C:\Users\<name>` 等个人路径、内网地址、会话里的私有内容。

完成判据：正文重搜一遍，密钥样式与个人路径零命中；路径统一替换为 `<path>`。

## 4. 查重

```bash
gh issue list --repo qq540491950/go-workbuddy --state open --limit 100 \
  --json number,title,labels --search "<现象关键词>"
```

同现象已有 open 票 → 在原票评论里补日志和复现信息，不新建。

完成判据：确认无同现象 open 票，或材料已补在原票上。

## 5. 装配正文

小节顺序与 `bug_report.yml` 字段一致。正文在 shell 变量里拼好直接传，不落盘：

```markdown
## 版本
（填 `gh release list` 查到的最新 tag，或留空）

## 测试环境
Windows 11 23H2 x64；模型：…；MCP：…；工作区：<path>

## 复现步骤
1. …

## 期望结果
…

## 实际结果
…（错误提示原文照抄）

## 日志 / 截图
（用户粘贴的、已脱敏的 30～50 行）

## 复现频率
必现（3/3）

## 前置条件
…
```

完成判据：每个表单小节都有内容，无空壳小节。

## 6. 提票

```bash
# 模块取标题里的白名单值；P0 追加 priority:p0；enhancement 验收明确时追加 ready-for-agent
LABELS="bug,module:<模块>"
# LABELS="$LABELS,priority:p0"
# LABELS="$LABELS,ready-for-agent"
gh issue create --repo qq540491950/go-workbuddy \
  --title "[模块] 简短现象" \
  --label "$LABELS" \
  --body "<上面拼好的正文>"
```

正文长、shell 转义麻烦时，用 stdin 传（`--body-file -`），仍然不落盘。

提完复核：

```bash
gh issue view <N> --repo qq540491950/go-workbuddy \
  --json number,author,state,labels,url
```

`module:<模块>` 必须存在；`priority:p0` 只在 P0 票上出现；enhancement 只有在
验收标准明确时才带 `ready-for-agent`。标签写错会直接影响修复侧的 module
互斥与排序。

完成判据：author 为 `qq540491950`、state 为 OPEN、没有任何
`agent:*` 标签；编号与链接已报给用户。

## 7. 交接与收回

远端每 15 分钟检查一次，先告诉用户「预计 15 分钟内开始处理」。

按需轮询（间隔 5～10 分钟，别空转刷屏）：

```bash
gh issue view <N> --repo qq540491950/go-workbuddy --json state,labels,comments
```

标签翻译成人话：

- `agent:processing` —— 已认领，编码或 PR 阶段，不用管。
- `agent:pending-release` —— 已合入 main，等待下一批 patch 版本。
- `needs-info` —— 按「提问预算」让用户补版本/日志/步骤，补完评论在原票，不重开新票。
- `agent:blocked` + `agent:retryable` —— 可自动重试的失败，30 分钟后会回队，不用人工改标签。
- `agent:blocked` + `agent:permanent` —— 需要人工判断，从评论抄出失败摘要给用户。
- `agent:fixed` —— 报版本号 + 更新方法（重启应用，或「设置 → 关于 → 检查更新」），并提示「如仍未解决，请重开并附新日志和复现步骤」。

完成判据：票处于终态之一，且用户知道下一步动作。

## 边界

- 一次一张票；该不该拆看「拆票判据」，拆出来的票互相引用。不动 `agent:*` 标签，不自己关票（远端关）。
- 不 force push、不删 tag、不改 release 资产。
- 不在本机跑 `go test`、不构建、不启动应用验证——复现与质量门禁都是远端的事。
- 多票默认批量发版：发布列车按 30 分钟窗口合并成一个 patch tag，P0 走最短 15 分钟窗口；main CI 绿后自动发版。不再逐票询问发版方式。
- 修复侧本地是 macOS、测试机是 Windows，Windows 特有问题远端只能靠日志 + 代码分析；票里把日志和复现步骤给足，它的修复结论才站得住。
