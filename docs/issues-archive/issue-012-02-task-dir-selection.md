# #12 02: 新建任务目录选择与首轮前改绑

> 本文件是 GitHub issue 原文存档。该 issue 已于 2026-11-02 从仓库删除（编号作废），此处的正文与评论为唯一留存副本。

| 字段 | 值 |
| --- | --- |
| Issue | #12 |
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

- `41a8aaa` feat(chat): 新建任务目录选择与首轮前改绑

补充说明：本票验收复选框在建单时未勾选，实施结论见关闭评论与迭代日志第 144 轮。

迭代日志对照：[docs/iteration-log.md](../docs/iteration-log.md#L431-L445)

## 原文

### Parent

#10

### What to build

用户可以在新建任务时选择已有文件夹，也可以恢复为自动创建的独立目录；首轮执行前可以改绑工作区，首轮开始后工作区锁定。系统会校验目录存在、可读写且没有被另一个未归档任务占用；失败时不留下半创建会话，也绝不删除用户选择的目录。

### Acceptance criteria

- [ ] 新对话对话框提供自动目录、原生目录选择、路径展示和重置操作。
- [ ] 服务端验证绝对路径、目录存在、可读写和活动任务冲突，并返回中文错误。
- [ ] 首轮事件产生前允许改绑；首轮产生后拒绝更改。
- [ ] 自动目录创建或基线初始化失败时清理会话元数据；用户目录永不被删除。
- [ ] 归档旧任务后，其目录可被新任务复用。
- [ ] 浏览器桩覆盖选择、重置、成功、冲突和失败保表单状态。


## 评论

**qq540491950** · 2026-10-31 21:21 +08:00

Implemented in commits 3f96664..761decf. Final verification: go test ./internal/... ., go vet, CGO MinGW go test -race, tsc, vitest 11 tests, frontend production build, wails3 build all passed. Final Standards/Spec review found no remaining blocking findings.
