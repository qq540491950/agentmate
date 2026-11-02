# #11 01: 独立任务目录与工作区绑定

> 本文件是 GitHub issue 原文存档。该 issue 已于 2026-11-02 从仓库删除（编号作废），此处的正文与评论为唯一留存副本。

| 字段 | 值 |
| --- | --- |
| Issue | #11 |
| 归档时状态 | closed |
| 标签 | `ready-for-agent` |
| 作者 | qq540491950 |
| 创建时间 | 2026-10-31 19:54 +08:00 |
| 关闭时间 | 2026-10-31 21:21 +08:00 |
| 票据类型 | 子票（母票 #10） |
| 实施轮次 | 第一百四十四轮迭代：任务级工作区与产物验收工作台（已实施） |

## 归档结论

已实现并已于 2026-10-31 21:21 关闭（关闭评论：`Implemented in commits 3f96664..761decf`）。

实施提交：

- `3f96664` feat(chat): 任务级工作区隔离与运行时绑定

补充说明：本票验收复选框在建单时未勾选，实施结论见关闭评论与迭代日志第 144 轮。

迭代日志对照：[docs/iteration-log.md](../docs/iteration-log.md#L431-L445)

## 原文

### Parent

#10

### What to build

新建会话默认获得唯一任务工作目录，并让单 Agent、Agent 团队、提示词工作区变量、文件工具和工具卡预览都使用该会话的工作区。旧会话继续回落到全局工作区；知识库、Skills 物化和会话导出保持应用级目录。用户能在会话标题栏看到当前工作区，并能在两个并行任务中安全写入同名文件而不互相覆盖。

### Acceptance criteria

- [ ] 会话元数据持久化任务工作区；新会话默认创建唯一目录，旧会话空值回落到全局工作区。
- [ ] 单 Agent 与团队执行时，内置文件工具和 `{{workspace}}` 变量使用会话工作区。
- [ ] 工具卡中的任务文件预览按会话工作区解析；`search_knowledge` 引用继续使用应用级预览。
- [ ] 两个并行任务写入同名相对路径时，文件落在各自目录，快照/预览不串会话。
- [ ] 会话标题栏显示工作区入口与中文错误状态。
- [ ] Wails 绑定重新生成，服务契约测试与并发回归通过。


## 评论

**qq540491950** · 2026-10-31 21:21 +08:00

Implemented in commits 3f96664..761decf. Final verification: go test ./internal/... ., go vet, CGO MinGW go test -race, tsc, vitest 11 tests, frontend production build, wails3 build all passed. Final Standards/Spec review found no remaining blocking findings.
