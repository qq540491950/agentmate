### 第 ? 轮迭代：斜杠菜单列出并使用 Skill（已实施）

依据：issue #53（module:chat，enhancement，ready-for-agent）。输入框的 `/` 菜单原先只有 5 个内置命令，已配置的 Skill 既不出现在候选里，手输 `/<skill>` 也会落到「未知命令」分支、消息不发送，Skill 因此无法用斜杠引用。验收要求：菜单候选 = 内置命令 + 可用 Skill、大小写不敏感前缀过滤、键盘交互与 @mention 一致、发送时把该 Skill 的 `SKILL.md` 正文注入本轮、带参数时其余文本作为用户输入、未知命令提示与菜单一致、内置命令与既有流程不回归。

**实现要点**

- **候选合并**（`frontend/src/lib/slashSkills.ts`）：`skillCommandName` 镜像后端 `agentkit.MaterializedSkillName`（小写 + 连字符），`slashCandidates` 以内置命令打底、追加 Skill 并去重（同名时内置命令优先），`filterSlashCommands` 保留原有「整段输入仍是一个命令 token」与大小写不敏感前缀匹配的语义。`ChatPage` 从既有配置加载里取 `skills`，菜单渲染与键盘选择（↑↓ / Enter / Tab / Esc）复用原逻辑。
- **发送分发**：内置 5 个命令仍在本地执行；命中 Skill 的 `/<name>` 走普通发送（`isSkillCommand`），未知 token 仍弹提示且不发送，提示列出与菜单完全一致的内置命令，存在 Skill 时追加「输入 / 选择 N 个 Skill」。
- **正文注入**（`internal/services/chatservice_render.go` + `internal/agentkit/kit.go`）：`resolveSkillCommand` 解析首个 token（复用 materialized 名称归一化，`/Weekly_Report` 可命中 `Weekly Report`），`RenderSkillMD` 用与落盘相同的方式渲染 frontmatter + 正文；`buildSkillTurnContext` 生成注入块，包含「命令后的文本才是实际任务」与实际参数。
- **只作用于本轮**：注入块通过 ADK 的 `temp:` 会话状态（`agentkit.SkillContextStateKey`）+ `runner.WithStateDelta` 传给 `skillAwareInstruction`（InstructionProvider 在每次模型调用时追加到系统指令）。临时状态在该轮生效但不会写回会话，因此下一轮不会继承；用户消息保持原文 `/<skill> 参数`，历史、导出与「重新生成」都按原样重放并重新注入。HITL 恢复轮从最后一条用户消息重新解析 Skill，确认往返后技能不丢失。远程 A2A 目标没有本地 InstructionProvider，改为把注入块随消息发送。
- **状态合并**：思考等级与 Skill 上下文共用同一个 `WithStateDelta`（后一个 RunOption 会覆盖前一个），避免两种注入互相吞掉。

**测试证据**

- 新增 `internal/services/chatservice_skill_test.go`：假 SSE 模型捕获请求体，断言 `/slash-test-skill 本周进展` 首轮请求含 SKILL.md 正文与实际任务参数、用户消息保持原命令文本、历史不含注入正文、下一轮普通消息不继承技能指令；`resolveSkillCommand` 覆盖大小写 / 下划线 / 连字符归一、参数切分、内置命令与未匹配 token。
- 新增 `frontend/src/lib/slashSkills.test.ts`（4 组 10 例）：名称归一化、候选合并与去重、大小写不敏感前缀过滤、发送路径的技能识别。
- 本地门禁：`go vet ./internal/... .`、`go test -count=1 ./internal/... .`、`go test -race -count=1 ./internal/...`；前端 `npx tsc --noEmit`、`npm test`（88 例）、`npm run build`。验证环境 macOS arm64；Windows 交互未实机验证，纯前端/后端逻辑与平台无关。
- 已知范围：issue #52 的项目 / 全局 `.agents/skills` 发现尚未合入，本票按 issue 说明先覆盖应用内配置的 Skill；发现能力合入后在候选来源处接入同一套 `slashCandidates`。