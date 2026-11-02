# #5 04: 会话历史一致性（动历史前等待旧轮退出）

> 本文件是 GitHub issue 原文存档。该 issue 已于 2026-11-02 从仓库删除（编号作废），此处的正文与评论为唯一留存副本。

| 字段 | 值 |
| --- | --- |
| Issue | #5 |
| 归档时状态 | open |
| 标签 | `ready-for-agent` |
| 作者 | qq540491950 |
| 创建时间 | 2026-10-31 16:44 +08:00 |
| 关闭时间 | — |
| 票据类型 | 子票（母票 #1） |
| 实施轮次 | 第一百四十三轮迭代：代码审查修复批次（已实施） |

## 归档结论

已实现：运行句柄携带完成信号，Regenerate / EditAndResend / DeleteSession 走「取消并等待（10s 兜底）」；同时修复 ADK Delete 只删 sessions 行留下的事件孤儿行。

实施提交：

- `ea874ed` fix(chat): 动历史前等待旧轮落定 + 删除会话清理孤儿事件行
- `9ac074e` fix(chat,mcp): 代码审查修复 — 全句柄等待 / 关闭顺序 / 状态清理

补充说明：代码位置：`internal/services/chatservice.go` 的 `cancelAndWaitBounded`；9ac074e 由「等待当前句柄」改为等待该会话全部活动句柄。

迭代日志对照：[docs/iteration-log.md](../docs/iteration-log.md#L447-L463)

## 原文

### Parent
#1

### What to build

并行会话用户在旧一轮还在流式输出时点「重新生成」「编辑重发」或删除会话：旧一轮先被取消并完全落定（事件写入结束），然后才裁剪/删除历史；不再出现已删历史被旧轮重新写入的复活现象。取消等待带超时兜底，UI 不会因此卡死。

### Acceptance criteria

- [ ] 运行句柄携带完成信号，动历史的三个入口统一「取消并等待」语义
- [ ] 慢 SSE 假模型下的回归测试：运行中触发 Regenerate / DeleteSession，最终事件流完整、无复活写入
- [ ] `go test -race` 通过


## 评论

（无评论）
