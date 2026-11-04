# Issue 自动处理流水线设计

本文件说明自动修复侧的目标架构。可执行步骤见
[`issue-fixer-prompt.md`](issue-fixer-prompt.md)；提交侧契约见
[`.agents/skills/github-bug-relay/SKILL.md`](../.agents/skills/github-bug-relay/SKILL.md)。

## 为什么不再用「一轮修一个 issue」

旧流程把「一个 issue 的完整生命周期」放进「一次定时运行 + 一个 `main`
工作区」：

- 一次运行从认领、修复、提交、等 CI 到发版，最长可占 30 分钟以上；
- 工作区脏、本地分叉、CI 基础设施失败都会让整条队列停下；
- 人工会话和定时任务共用 `main`，互相踩；
- `agent:blocked` 是终态，没有自动重试；
- 发版按「当前所有 pending」关闭，并发后会误关新 issue。

目标是把长事务拆成可并行、可重试、可串行集成的短动作。

## 角色

| 角色 | 职责 | 边界 |
|---|---|---|
| Dispatcher | 快照、自愈、认领、WIP 与 module 互斥、决定本轮动作 | 不写业务代码、不等 CI、不发版 |
| Worker | 在独立 worktree 修复/实现、跑质量门禁、推分支、开 PR | 不推 `main`、不打 tag、不发版 |
| Integrator | 按优先级合入一个 PR、处理 rebase、维护共享文件 | 不修业务 bug、不发版 |
| Release | 快照 pending、打一个 tag、等 Release、按祖先关系关票 | 不修代码、不重复打 tag |

单轮运行只做一个动作，**绝不在进程内等待 CI**（发版动作例外：必须等待
Release run 完成）。CI 由 GitHub Actions 在 PR 上并行跑，下一轮再检查结果。

当前实现由同一个控制循环按动作顺序执行四个角色；Worker 已经在独立
worktree 中工作，后续可以把编码动作拆成独立会话以提高并发。

## 状态机

| 状态 | 载体 | 说明 |
|---|---|---|
| 待处理 | 无 `agent:*` 标签 | Dispatcher 可认领 |
| 已认领 | `agent:processing` + claim 评论 | 编码阶段；lease 过期可回收 |
| PR 在途 | `agent:processing` + PR 标签 `agent-fix` | PR 本身就是活动凭证 |
| 可合入 | PR 标签 `agent:ready-to-merge` | checks 绿且分支包含最新 `main` |
| 待发版 | `agent:pending-release` | 已合入 `main`，等待批次 |
| 已发布 | issue closed + `agent:fixed` | 修复提交是 Release tag 的祖先 |
| 待补充 | `needs-info` | 作者补充评论后自动回队 |
| 失败 | `agent:blocked` + `agent:retryable` / `agent:permanent` | 可重试的按退避回队 |

### Claim 评论

认领时在 issue 下发一条带机器可读元数据的评论：

```html
<!-- agent-claim run=<run-id> worker=<worker-id> branch=<branch> leaseUntil=<epoch> -->
已认领：本次运行将按流水线处理，编码完成后会开 PR。
```

- `run` 由每轮运行的开始时间生成，例如 `20261104T142500Z`；
- `branch` 形如 `auto/issue-39-agents-form`；
- `leaseUntil` 是 epoch 秒；Worker 在阶段边界用
  `scripts/issue-lease.sh heartbeat` 续租；
- 进入 PR 阶段后释放 lease，PR 成为活动凭证；Dispatcher 看到 open PR
  就不重复认领。

### 失败分类

| 类型 | 判定 | 处理 |
|---|---|---|
| infra | 限流、runner、安装器、网络、GitHub API 故障 | 标 `agent:blocked` + `agent:retryable`，30 分钟后自动回队；main CI 转绿也可提前回队 |
| code | 本地测试、编译、类型检查失败 | 同一轮内只修一次；仍失败直接标 `agent:permanent`。只有确认 flaky 或基础设施故障才用 `agent:retryable` |
| conflict | PR 与最新 `main` 冲突 | 自动同步 `main` 最多 2 次；仍冲突转 `agent:permanent` |
| info | 复现信息或验收标准不足 | `needs-info`，等作者补充，不自动重试 |
| release | 发版失败 | 只影响本批快照 issue，标 `agent:retryable`，不影响新 pending |

## 并发与冲突控制

### 物理隔离

每个 Worker 从最新 `origin/main` 建独立 worktree：

```bash
git worktree add -b "auto/issue-<N>-<slug>" "$WT" origin/main
```

主 checkout 不参与自动修复。人工未提交的改动、本地领先/落后的 `main`
不再影响自动流程。

### 逻辑隔离

- 全局 WIP 上限：**3 个在途 PR**（`agent-fix` 标签的 open PR）。
- 同一 module 同时最多 1 个在途 issue/PR。
- module 取 issue 的 `module:*` 标签；没有标签时解析标题 `[module]`；都没有
  按 `other` 处理。
- 不同 module 可同时在途；编码动作按轮次串行，CI 与集成并行。相同
  module 按 P0 → `createdAt` 排序。

### 集成串行

- 同一时间只允许一个 Integrator 合并；使用本地 `integration.lock`。
- 合并前必须满足：
  1. PR 的 checks 全部 completed/success；
  2. PR 分支包含最新 `origin/main`（不满足则先 `gh pr update-branch`，
     本轮不合并）；
  3. `mergeable == MERGEABLE`。
- 合并使用 squash，保留 `refs #N` 与 `fix-commit` 评论。
- 推荐开启 `main` 分支保护与 required checks（`backend`、`frontend`、
  `windows-build`），保留管理员 bypass。没有保护时，Integrator 的本地锁 +
  「分支包含最新 main」检查是最后防线。

### 共享文件归 Integrator

Worker 不得修改以下文件，除非 issue 本身就是工具链问题：

- `docs/iteration-log.md`（改为按 issue 写 `docs/iterations/issue-<N>-<slug>.md`
  片段，Integrator 合并时统一插入顶部）；
- `frontend/bindings/**`（Integrator 合并 Go 改动后统一
  `wails3 generate bindings -clean=true`）；
- `go.mod` / `go.sum`；
- `frontend/package-lock.json`；
- 版本号与构建产物。

运行手册本身也只通过独立的工具链 issue/PR 修改，禁止在修产品 bug 的同一轮
「顺手」改流程。

## 批量发版

Release 是独立动作，不再是每轮修复的尾巴。发版在独立的 detached
`main` worktree 中进行，不使用主 checkout，因此人工未提交改动不会阻塞发版。

1. `scripts/issue-release.sh snapshot` 读取 `agent:pending-release` issue，
   从评论里的 `fix-commit: <sha>` 标记生成快照；
2. 只发快照里的 issue，发版期间新加入的 pending 留到下一批；
3. `scripts/issue-release.sh close <tag>` 只关闭 `fix-commit` 是 tag 祖先的
   issue；
4. 发版失败时 `scripts/issue-release.sh fail <原因>` 只处理快照内的 issue；
5. 常规窗口 60 分钟；`priority:p0` 可最快 15 分钟发一次。

## 提交侧契约

`github-bug-relay` 只提票，但提供机器可读路由信息：

- 提票时加 `module:<chat|agents|models|...>`；
- P0 加 `priority:p0`；
- enhancement 在验收标准明确时加 `ready-for-agent`，否则留 `needs-info`；
  Dispatcher 对没有标签的手工票也会先评估，标准明确则补标签后继续；
- 父子票：父票是 umbrella，Dispatcher 不直接编码；子票独立处理，全部子票
  `agent:fixed` 后再关闭父票；
- 默认批量发版，不再逐票询问发版方式。

## 部署

首次启用或在新仓库部署时，先运行一次：

```bash
bash scripts/issue-labels.sh
```

它会创建/更新 `module:*`、`priority:p0`、`agent:retryable`、
`agent:permanent`、`agent:ready-to-merge`、`agent-fix` 标签。

## 观测

每轮结束在运行摘要里输出：本轮动作、在途 PR 数、被阻塞 issue 数、最近一次
发版时间。若要进一步排障，可运行 `scripts/issue-queue.sh` 查看完整队列快照。