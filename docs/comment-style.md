# 代码注释规范

本项目代码注释统一使用简体中文。注释解释“为什么”和“不能做什么”，不逐行复述代码。

## 基本规则

- 只翻译自然语言注释；标识符、字符串、日志、错误文案、路径、URL、issue 编号保持原样。
- 保留 `must / must not / never / only / always` 的强制程度，分别使用“必须 / 不得 / 绝不 / 仅 / 始终”。
- 中文与英文、数字之间加空格；代码标识符用反引号。
- Go 文档注释保留 `Package <包名>` 或导出标识符前缀，便于 `go doc` 和 lint 识别。
- `//go:build`、`//go:embed`、`//nolint`、`// eslint-disable`、`// @ts-*` 等指令逐字保留，不翻译、不移动。
- JSDoc 保留 `/** ... */`；JSX 子节点注释保留 `{/* ... */}`。
- `frontend/bindings/**`、`build/**` 等生成或平台模板文件不翻译。

## 术语

| 英文 | 中文 |
|---|---|
| session | 会话 |
| turn | 回合 |
| workspace | 工作区 |
| skill | 技能（`SKILL.md` 等标识符除外） |
| staging directory | 暂存目录 |
| tool call | 工具调用 |
| approval | 审批 |
| sandbox | 沙箱 |
| guardrail | 护栏 |
| compaction | 压缩 |
| artifact | 产物 |
| snapshot | 快照 |
| stream | 流式 |
| prompt | 提示词 |
| provider | 供应商 |
| fallback | 回退 |
| legacy | 遗留 |
| builtin | 内置 |
| race | 竞态 |
| idempotent | 幂等 |
| best-effort | 尽力而为 |

`ADK`、`Wails`、`MCP`、`A2A`、`HITL`、`JSON`、`RPC`、`API`、`UI`、`SSE`、`OAuth`、`SQLite`、`Agent` 保持原样。

## 示例

```go
// skillMu 串行化技能物化：同一 Agent 的并行构建（并行会话每轮都会重建）
// 不得在共享暂存目录上交错执行写入、清理与预加载序列。
```

```ts
/** markRunning 按会话跟踪进行中的回合，使多个会话可以并行流式输出而互不阻塞。 */
```

提交前用 `gofmt`、`tsc --noEmit` 和测试确认只改动了注释。
