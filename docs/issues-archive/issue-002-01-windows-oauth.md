# #2 01: Windows OAuth 授权修复

> 本文件是 GitHub issue 原文存档。该 issue 已于 2026-11-02 从仓库删除（编号作废），此处的正文与评论为唯一留存副本。

| 字段 | 值 |
| --- | --- |
| Issue | #2 |
| 归档时状态 | open |
| 标签 | `ready-for-agent` |
| 作者 | qq540491950 |
| 创建时间 | 2026-10-31 16:44 +08:00 |
| 关闭时间 | — |
| 票据类型 | 子票（母票 #1） |
| 实施轮次 | 第一百四十三轮迭代：代码审查修复批次（已实施） |

## 归档结论

已实现：浏览器启动命令提取为纯函数，Windows 走 `rundll32 url.dll,FileProtocolHandler`，不再经 cmd 解析（cmd 会把授权 URL 里第一个 `&` 当命令分隔符）。

实施提交：

- `a93fdf9` fix(mcp): Windows OAuth 授权 URL 经 rundll32 完整打开

补充说明：代码位置：`internal/mcpmgr/auth.go`（36–38 行附近的平台分支）；测试覆盖三平台纯函数与 URLOpener 注入。

迭代日志对照：[docs/iteration-log.md](../docs/iteration-log.md#L447-L463)

## 原文

### Parent
#1

### What to build

Windows 用户挂载 OAuth 类型的 streamable-http MCP 服务器后，点击连接时授权 URL 被完整地交给系统默认浏览器打开（不再经过 cmd 二次解析），浏览器能正常打开带 query 参数的授权页并完成回调。macOS / Linux 行为保持不变。

### Acceptance criteria

- [ ] 浏览器启动命令由可独立测试的纯函数按平台构造；Windows 分支不经过 cmd 解析 URL
- [ ] 纯函数测试覆盖三个平台：URL 原样作为参数、Windows 命令不包含 cmd
- [ ] 现有 URLOpener 注入测试全部通过
- [ ] 在 Windows 上对带 `&` 的授权 URL 手动验证浏览器打开完整地址


## 评论

（无评论）
