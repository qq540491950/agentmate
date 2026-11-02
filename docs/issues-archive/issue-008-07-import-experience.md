# #8 07: 导入体验（Agent 悬空引用清洗 + 技能重名统一）

> 本文件是 GitHub issue 原文存档。该 issue 已于 2026-11-02 从仓库删除（编号作废），此处的正文与评论为唯一留存副本。

| 字段 | 值 |
| --- | --- |
| Issue | #8 |
| 归档时状态 | open |
| 标签 | `ready-for-agent` |
| 作者 | qq540491950 |
| 创建时间 | 2026-10-31 16:44 +08:00 |
| 关闭时间 | — |
| 票据类型 | 子票（母票 #1） |
| 实施轮次 | 第一百四十三轮迭代：代码审查修复批次（已实施） |

## 归档结论

已实现：`ImportAgent` 返回 `{agent, warnings}`，悬空技能 / MCP 引用导入时剥离并在前端 toast 警告；技能导入去重键与物化目录名统一为 `MaterializedSkillName`。

实施提交：

- `d08e84c` feat(services): Agent 导入清洗悬空引用 + 技能导入重名统一

补充说明：代码位置：`internal/services/app.go`（`stripMissingRefs` / `ImportAgentResult.Warnings`）、`frontend/src/pages/AgentsPage.tsx`（警告 toast）；Wails 绑定已再生成。

迭代日志对照：[docs/iteration-log.md](../docs/iteration-log.md#L447-L463)

## 原文

### Parent
#1

### What to build

用户从分享文件导入 Agent 时，指向本机不存在的技能 / MCP 服务器的引用被自动剥离，导入结果附带被剥离项的警告并在界面 toast 展示，导入的 Agent 开箱即用。从工作区导入技能时，去重与物化目录名使用同一套规范化规则：两个规范化后同名的技能导入时报冲突并跳过，不再互相覆盖。

### Acceptance criteria

- [ ] 导入含悬空引用的分享文档：引用被剥离、警告列表返回、导入的 Agent 可正常构建
- [ ] 导入返回结构新增警告字段，Wails 绑定再生成，前端展示 toast
- [ ] 技能导入去重键与物化目录名统一；规范化同名导入被跳过
- [ ] 前端类型检查与构建通过


## 评论

（无评论）
