# #4 03: 测试可移植性（句柄清理 + 平台条件断言）

> 本文件是 GitHub issue 原文存档。该 issue 已于 2026-11-02 从仓库删除（编号作废），此处的正文与评论为唯一留存副本。

| 字段 | 值 |
| --- | --- |
| Issue | #4 |
| 归档时状态 | open |
| 标签 | `ready-for-agent` |
| 作者 | qq540491950 |
| 创建时间 | 2026-10-31 16:44 +08:00 |
| 关闭时间 | — |
| 票据类型 | 子票（母票 #1） |
| 实施轮次 | 第一百四十三轮迭代：代码审查修复批次（已实施） |

## 归档结论

已实现：测试内 gorm / 会话库 / 知识索引 / 旋转日志句柄统一注册 Cleanup；POSIX 权限断言在 Windows 跳过。Windows 下 `go test ./internal/... .` 由 5 失败转全绿。

实施提交：

- `3ae7a4a` test: 测试可移植性 — DB 句柄清理 + 平台条件权限断言
- `f4e170f` fix(test): 去掉两处依赖宿主机环境的用例，让 CI/gate 可信

补充说明：后续 f4e170f 又移除两处依赖宿主机环境的用例，进一步稳定 CI。

迭代日志对照：[docs/iteration-log.md](../docs/iteration-log.md#L447-L463)

## 原文

### Parent
#1

### What to build

开发者在 Windows 与 macOS 上运行同一验证脚本都得到全量测试通过：测试自身打开的数据库连接在测试结束时全部关闭（不再出现临时目录清理失败），文件权限断言只在支持 POSIX 权限语义的平台执行。

### Acceptance criteria

- [ ] 所有打开 gorm / session 库的测试路径注册了关闭清理（Cleanup）
- [ ] 权限断言在 Windows 上跳过并说明原因，在 POSIX 平台继续执行
- [ ] `go test ./internal/... .` 在 Windows 上全绿（此前 5 个失败清零）


## 评论

（无评论）
