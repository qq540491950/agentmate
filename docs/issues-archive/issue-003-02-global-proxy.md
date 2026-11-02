# #3 02: 全局代理正确性（环境变量回落 + 回环直连）

> 本文件是 GitHub issue 原文存档。该 issue 已于 2026-11-02 从仓库删除（编号作废），此处的正文与评论为唯一留存副本。

| 字段 | 值 |
| --- | --- |
| Issue | #3 |
| 归档时状态 | open |
| 标签 | `ready-for-agent` |
| 作者 | qq540491950 |
| 创建时间 | 2026-10-31 16:44 +08:00 |
| 关闭时间 | — |
| 票据类型 | 子票（母票 #1） |
| 实施轮次 | 第一百四十三轮迭代：代码审查修复批次（已实施） |

## 归档结论

已实现：空配置委托 `http.ProxyFromEnvironment`；已配置代理时 127.0.0.1 / localhost / ::1 直连。

实施提交：

- `69e413d` fix(agentkit): 全局代理空配置回落环境变量 + 回环直连

补充说明：代码位置：`internal/agentkit/proxy.go`（`isLoopbackHost`）；契约测试 `internal/agentkit/proxy_test.go`（回环直连 + httptest 代理缝）。

迭代日志对照：[docs/iteration-log.md](../docs/iteration-log.md#L447-L463)

## 原文

### Parent
#1

### What to build

用户不配置应用内代理时，出站请求遵循 HTTP_PROXY / HTTPS_PROXY / NO_PROXY 环境变量；无论是否配置代理，发往 127.0.0.1 / localhost / ::1 的请求永远直连。已配置应用内代理时，模型、嵌入、MCP、A2A 流量统一走代理的行为不变。

### Acceptance criteria

- [ ] 空代理配置后默认传输层的代理函数等价于 ProxyFromEnvironment（可用环境变量验证）
- [ ] 配置代理后：公网地址经代理、回环地址直连（httptest 代理缝验证）
- [ ] 现有代理契约测试全部通过；「仅启动时应用、改配置需重启」契约不变


## 评论

（无评论）
