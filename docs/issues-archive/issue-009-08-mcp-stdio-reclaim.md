# #9 08: MCP stdio 进程回收

> 本文件是 GitHub issue 原文存档。该 issue 已于 2026-11-02 从仓库删除（编号作废），此处的正文与评论为唯一留存副本。

| 字段 | 值 |
| --- | --- |
| Issue | #9 |
| 归档时状态 | open |
| 标签 | `ready-for-agent` |
| 作者 | qq540491950 |
| 创建时间 | 2026-10-31 16:44 +08:00 |
| 关闭时间 | — |
| 票据类型 | 子票（母票 #1） |
| 实施轮次 | 第一百四十三轮迭代：代码审查修复批次（已实施） |

## 归档结论

已实现：`Manager.CloseAll` 幂等全量断开（逐个关闭受控传输并清理状态），接入应用关闭钩子，stdio MCP 子进程不再孤儿化。

实施提交：

- `0792912` fix(mcp): CloseAll 全量断开 + 应用关闭钩子接线

补充说明：代码位置：`internal/mcpmgr/manager.go`（`CloseAll`）、`main.go`（关闭钩子：先停 A2A 再断 MCP）。

迭代日志对照：[docs/iteration-log.md](../docs/iteration-log.md#L447-L463)

## 原文

### Parent
#1

### What to build

挂载了 stdio MCP 服务器的用户退出应用时，管理器在关闭钩子中被全量断开：受控传输逐个关闭、状态清理，重复调用安全；系统不再残留孤儿子进程。

### Acceptance criteria

- [ ] 管理器提供幂等的全量断开入口，清空受控连接与状态
- [ ] 应用关闭钩子在停止 A2A 之后调用该入口
- [ ] 现有 MCP 管理器测试全部通过


## 评论

（无评论）
