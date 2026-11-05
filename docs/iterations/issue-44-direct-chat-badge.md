### 第 ? 轮迭代：直聊会话头部隐藏目标徽标（已实施）

依据：issue #44（Windows 桌面，直聊会话）。会话头部固定渲染「直聊」徽标，与
Agent / 团队 / 远程徽标并排出现，用户误以为「直聊」是一种需要选择的模式或实体。
期望直聊会话不再显示徽标，绑定 Agent / 团队 / 远程时仍显示对应实体徽标。

实现要点：

- **文案规则抽为纯函数**：`chatUtils.sessionTargetBadgeLabel(targetType)` 返回
  `Agent` / `团队` / `远程`，直聊（空目标）与非预期值返回 `null`。直聊不再依赖
  三元表达式里的「直聊」兜底文案。
- **头部渲染**：`ChatPage` 头部徽标改为先取标签，`null` 时不渲染；有目标时仍显示
  `标签: 目标名`，Agent / 团队徽标保留点击跳转设置页的行为，远程徽标保持纯展示。
- **测试**：`chatUtils.test.ts` 新增 `sessionTargetBadgeLabel` 用例，覆盖直聊
  （`""`、`undefined`）返回 `null` 与三种绑定目标的标签。

测试证据：

- `npx tsc --noEmit` 通过；
- `npm test`：11 个测试文件、65 个用例全部通过；
- `npm run build` 成功；
- `go vet ./internal/... .`、`go test -count=1 ./internal/...` 全部通过（后端未改）。