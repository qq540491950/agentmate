### 第 ? 轮迭代：发现 .agents/skills 项目 / 全局技能并接入 Agent 挂载（已实施）

依据：issue #52（Skills 页，enhancement，Windows x64 / v0.5.25）。可复用技能按 agentskills.io 规范放在 `.agents/skills/<name>/SKILL.md`，同一约定被 Codex、Claude Code、OpenCode 等工具读取；应用此前只认 config.json 内的 skill 与手动「从工作区导入」的 `<工作区>/skills/`，导致同一份 skill 在别的工具开箱可用，在本应用必须手工复制或重复导入。

**实现要点**

- **只读发现层**：`internal/config/skills_discovery.go` 固定两个发现根——项目级 `<会话有效工作区>/.agents/skills`、用户级 `~/.agents/skills`（Windows 为 `%USERPROFILE%\.agents\skills`）。只扫描一层 `<root>/<name>/SKILL.md`，文件名必须是大写 `SKILL.md`，根目录散落文件与嵌套目录一律忽略；目录不存在或不可读时静默为空，不报错、不弹 toast。
- **frontmatter 校验**：按 agentskills.io 解析 `name` / `description`（复用并上移原导入解析器，新增 `|` / `>` 块标量支持）。`name` 必填、匹配 `^[a-z0-9]+(-[a-z0-9]+)*$` 且不超过 64 字符，`description` 必填；单条非法只跳过该条，不影响同目录其他 skill。小写 `skill.md` 在大小写不敏感文件系统上也不会被误认。
- **稳定身份**：发现项 ID 为 `fs:project:<name>` / `fs:global:<name>`，Agent 的 `SkillIDs` 直接保存该 ID，重启后仍能解析；外部目录失效时不会把 ID 写回 config.json。发现项只在 `GetConfig` 返回的副本中合并，配置导出 / 备份导入保持原行为。
- **冲突优先级**：`EffectiveSkills` 按「项目 > 全局 > 应用内」去重（键为物化目录名）。被遮蔽的应用内 skill 若仍被 Agent 挂载，构建时回落到同名发现项物化，不再报「挂载的技能不存在」；发现项随工作区切换消失时静默跳过，缺失的应用内 skill 仍保持硬错误（不静默降级）。
- **物化与挂载**：`Kit.skillToolset` 改用有效技能集解析挂载，发现项按既有路径物化到 `skills/agents/<agentKey>/<name>/SKILL.md`，剪枝逻辑不变；项目级根使用会话有效工作区，因此会话改绑工作区后下一轮构建即刷新；全局根在 `NewKit` 时解析、测试可覆盖。
- **前端**：`lib/skills.ts` 从 ID 前缀派生来源（项目 / 全局 / 应用内，无需新增绑定字段）；Skills 页每张卡片标注来源徽章，发现项显示「只读」并隐藏编辑 / 删除；Agent 编辑的 Skills 选择器通过同一份 `GetConfig` 自动列出发现项。
- **只读保护**：`SaveSkill` / `DeleteSkill` 收到发现项 ID 时返回明确错误，提示直接修改对应目录的 `SKILL.md`。

**测试证据**

- 新增 `internal/config/skills_discovery_test.go`：单层扫描与两处根、项目/全局顺序、大小写文件名与散落文件忽略、非法 name / 空 description 单条跳过、缺失根静默、冲突优先级（项目 > 全局 > 应用内，含规范化同名遮蔽）、块标量 description、名称规范化。
- 新增 `internal/services/configservice_discovered_skills_test.go`：`GetConfig` 合并项目 + 全局发现项且不写入 config.json；`DeleteSkill` / `SaveSkill` 拒绝发现项。
- 新增 `internal/agentkit/kit_discovered_skills_test.go`：发现项按 ID 物化到 per-agent 目录；应用内 skill 被同名发现项遮蔽时构建不失败并物化胜出定义；全局根物化；发现项消失静默跳过而缺失应用内 skill 仍报错。
- 现有 `TestSkillMaterialization` 改为替换默认 seeds，避免构造出保存路径本不允许的同名冲突。
- `./scripts/verify.sh` 全绿（前端构建、vitest 85 例、`go vet`、全量 `go test`、`go test -race`、`wails3 build`）；新增 `frontend/src/lib/skills.test.ts` 覆盖来源解析与只读判定。