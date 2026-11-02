# #13 03: 右侧任务文件浏览与预览

> 本文件是 GitHub issue 原文存档。该 issue 已于 2026-11-02 从仓库删除（编号作废），此处的正文与评论为唯一留存副本。

| 字段 | 值 |
| --- | --- |
| Issue | #13 |
| 归档时状态 | closed |
| 标签 | `ready-for-agent` |
| 作者 | qq540491950 |
| 创建时间 | 2026-10-31 19:54 +08:00 |
| 关闭时间 | 2026-10-31 21:22 +08:00 |
| 票据类型 | 子票（母票 #10） |
| 实施轮次 | 第一百四十四轮迭代：任务级工作区与产物验收工作台（已实施） |

## 归档结论

已实现并已于 2026-10-31 21:21 关闭（关闭评论：`Implemented in commits 3f96664..761decf`）。

实施提交：

- `decf861` feat(chat): 右侧任务文件浏览与预览

补充说明：本票验收复选框在建单时未勾选，实施结论见关闭评论与迭代日志第 144 轮。

迭代日志对照：[docs/iteration-log.md](../docs/iteration-log.md#L431-L445)

## 原文

### Parent

#10

### What to build

聊天页增加可收起的右侧任务文件面板，用户能浏览当前任务工作区中的文件、按路径搜索并预览受支持的图片、PDF、Markdown 与文本。面板在桌面显示为第三栏，在窄屏显示为覆盖式面板，并在切换会话、任务完成或手动刷新后显示正确状态。

### Acceptance criteria

- [ ] Artifact 服务提供会话级文件列表和会话级预览；扫描拒绝穿越与符号链接逃逸。
- [ ] 扫描跳过隐藏文件、版本库元数据、依赖缓存和虚拟环境，并返回截断标志。
- [ ] 聊天页结果区支持展开/收起、「全部文件」、路径搜索、预览、空态、加载态、错误态和截断提示。
- [ ] 桌面三栏与窄屏覆盖式布局可用，开关和标签满足键盘与 ARIA 要求。
- [ ] 快速切换会话时迟到响应不会覆盖当前面板；运行完成与手动刷新会更新文件列表。
- [ ] 浏览器桩与后端服务契约测试覆盖上述行为。


## 评论

**qq540491950** · 2026-10-31 21:21 +08:00

Implemented in commits 3f96664..761decf. Final verification: go test ./internal/... ., go vet, CGO MinGW go test -race, tsc, vitest 11 tests, frontend production build, wails3 build all passed. Final Standards/Spec review found no remaining blocking findings.
