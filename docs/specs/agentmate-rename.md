# Spec：项目更名 AgentMate（软件名 / 打包名 / 数据目录 / 仓库 / Logo 全量贯通）

> 状态：待实施（本地规格）。来源：2026-11-07 更名与 logo 设计讨论；用户已确认三项决策——仓库策略 A（GitHub 改名保留历史）、数据目录全新开始（不迁移）、持久状态键直接改名。按用户要求落盘仓库本地，不发布 issue tracker；执行范围仅限本地修改，远端改名与推送由用户单独指令触发。

## Problem Statement

项目从仿 WorkBuddy 起家，如今需要建立独立身份：软件名、安装包名、单实例锁名、Go module、应用数据目录、GitHub 仓库、应用与托盘图标全部沿用借来的 WorkBuddy 品牌与模板默认图标。用户对外交流、搜索资料时身份混淆；应用缺少自己的名字与脸，品牌资产（已定稿的火花 A logo）没有接入产品。

## Solution

- **软件名**：全端统一为 AgentMate（窗口标题、托盘 tooltip、欢迎页、关于页、侧栏底部）。
- **代码身份**：Go module 改为 `agentmate`，全部 import 与方法 ID 命名空间随动，绑定由构建自动重新生成，前端手写 import 与视觉桩同步。
- **打包身份**：二进制名 `agentmate`、productIdentifier `com.agentmate.app`，安装器 / plist / manifest / desktop 等打包资产同步。
- **数据身份**：应用数据目录改为 `agentmate`，全新开始不迁移；默认工作区改为 `AgentMateWorkspace`；持久状态键与本地存储键改为 `agentmate.*`。
- **仓库身份**：GitHub 仓库改名 `agentmate`，保留全部 issue 与 release 历史，旧链接自动重定向；更新渠道常量与应用内反馈入口同步指向新仓库。
- **视觉身份**：应用图标与托盘图标替换为已定稿的火花 A logo（Teal 主色 + Orange 火花）。

## User Stories

1. As a 个人用户, I want to 启动应用看到 AgentMate 的窗口标题与欢迎页, so that 应用有属于自己的身份。
2. As a 个人用户, I want to 系统托盘显示新火花 A 图标与 AgentMate 提示文字, so that 常驻入口一眼可辨。
3. As a 个人用户, I want to 在设置 → 关于看到 AgentMate 名称与版本, so that 确认运行的是更名后的版本。
4. As a 全新开始的用户, I want to 在全新的数据目录中重新配置供应商与 MCP, so that 旧数据不被误读、配置从干净状态开始。
5. As a 全新开始的用户, I want to 默认工作区落在 Documents\AgentMateWorkspace, so that 首次使用即符合新品牌。
6. As a 首次启动的用户, I want to 走一遍 onboarding, so that 关键配置在干净状态下完成。
7. As a 个人用户, I want to 任务栏与开始菜单显示 AgentMate, so that 系统里不再出现 WorkBuddy。
8. As a 个人用户, I want to 应用内「反馈问题」入口跳转到新仓库, so that 反馈与项目实际地址一致。
9. As a 开发者, I want to Go module 与全部 import 统一为 agentmate, so that 代码库命名自洽。
10. As a 开发者, I want to 绑定由构建重新生成且方法 ID 命名空间随 module 变化, so that IPC 全链路无需手工对齐。
11. As a 开发者, I want to 前端手写 import 与视觉桩方法 ID 同步更新, so that 类型化调用与浏览器视觉回归都正常。
12. As a 开发者, I want to wails3 build 产出 agentmate 可执行文件, so that 产物名与新身份一致。
13. As a 开发者, I want to 全仓搜索旧名零残留（历史迭代记录除外）, so that 更名彻底、未来维护不踩旧名。
14. As a 开发者, I want to 单实例锁名更新为 com.agentmate.app, so that 新身份版本独立持有实例锁。
15. As a 发布流程, I want to release 资产与更新 manifest 使用新命名, so that 自更新资产与安装包名称一致。
16. As a 旧版本自更新用户, I want to 仓库改名后经 GitHub 重定向拉到新 manifest, so that 存量版本完成最后一次桥接升级。
17. As a 维护者, I want to README 与工程文档中的仓库地址指向新仓库, so that 人工操作不被旧地址误导。
18. As a 个人用户, I want to 导出的配置备份标识为 agentmate, so that 备份与新版本匹配。
19. As a 个人用户, I want to 语义检索在新数据目录重建索引后正常工作, so that 全新开始后知识能力不缺失。
20. As a 维护者, I want to logo 设计源保留在仓库内, so that 后续品牌维护有据可依。
21. As a 维护者, I want to 工程指南 / 术语表 / README 标题同步更新, so that 文档与新身份一致。
22. As a 维护者, I want to 本地工作目录最后手动改名为 agentmate, so that 文件夹与项目身份一致且不打断进行中的工作。

## Implementation Decisions

- **命名映射**：软件名 AgentMate；Go module `agentmate`（保持当前裸模块名风格）；二进制与打包名 `agentmate`；productIdentifier `com.agentmate.app`；数据目录 `%APPDATA%\agentmate`；默认工作区 `Documents\AgentMateWorkspace`；GitHub 仓库 `agentmate`。
- **仓库策略 A（已确认）**：GitHub 改名保留 71 个 issue、33 个 release、CI 历史与自动重定向；新建仓库方案仅作备选记录，不采用。执行顺序为：本地原子提交（含更新渠道常量）→ GitHub 改名 → 更新本地 remote URL → push。常量先改齐、push 放在改名之后，避免「CI 在旧仓库跑新常量」的窗口期。
- **数据全新开始（已确认）**：不实现任何迁移逻辑；旧数据目录原样保留、新版本永不读取、不自动删除；状态键与本地存储键直接改名，无需改写存量数据。
- **绑定再生成**：绑定由构建 `-clean` 自动重建，产物目录名随 module 变化；wails-typed-events 类第三方绑定目录必须保留；方法 ID 以生成产物为准，不做手工计算。
- **打包资产同步**：更新桌面身份配置后，可选「自动重生成打包资产」或「手工同步」（上一轮模块重命名已有手工同步先例）；两条路径的结果必须一致。
- **图标接入**：托盘图标来自二进制内嵌的应用图标，替换应用图标即联动托盘；多尺寸 ICO 与浅色 / 深色 / 单色 SVG 变体保留在仓库设计目录作为品牌源。
- **本地人工执行**：本变更由维护者本地人工执行；提交切分为三个原子提交（module 与绑定 → 打包资产与桌面身份 → 品牌文案、logo 与文档常量）。
- **本地目录改名收尾（用户手动执行）**：工作目录改名安排在全部工单完成并验证之后由维护者手动切换；module 为裸名不依赖目录路径，改名前仅需更新视觉桩路径夹具与工程文档中的本地路径记载，改名后跑一次构建冒烟即可。
- **硬前置**：执行前必须处理工作区内既有的未提交改动（UI 刷新 WIP），避免混入更名提交。

## Testing Decisions

- 更名是零行为变化的重构：好的验证 = 现有测试**原样全绿**，唯一允许更新的期望值是更新器目标名断言（其期望值本身就是被更名的对象，随品牌改为 AgentMate）。
- Seam 1（后端）：全量测试 + vet 干净，覆盖全部服务与配置模块。
- Seam 2（前端）：vitest + 类型检查 + 构建三件套，覆盖手写 import、视觉桩与品牌文案。
- Seam 3（构建端到端）：绑定再生成 + 完整构建产出 agentmate 可执行文件（先例：上一轮模块重命名即按同一组合验证）。
- 全仓文本验收：旧名搜索零残留，白名单仅历史迭代记录。
- 人工冒烟：全新数据目录首次启动走 onboarding、托盘图标与提示、二次启动单实例转发、知识索引重建后语义检索。

## Out of Scope

- 不改任何功能行为与视觉设计（品牌文案与图标除外）。
- 不迁移旧数据，不自动删除旧数据目录（全新开始是既定决策；旧数据保留由用户自行处置）。
- 不更新历史迭代记录中的旧名（保留史实）。
- 平台验证以 Windows 为主，其他平台打包资产仅同步字面值（与仓库现状一致）。
- 远端仓库改名与 push 不在本 spec 的本地执行范围，由用户单独指令触发。

## Further Notes

- 本 spec 按用户要求落盘仓库本地（未发布 issue tracker）；如需发布 issue，按 enhancement 提交即可。
- 仓库改名后旧名释放，存在被他人占用的理论风险（当前 0 star 个人项目，风险可忽略）；GitHub 改名可逆，但改回需旧名未被占用。
- 工程指南中「productIdentifier 不可改」的约束指日常跨版本稳定性，本次更名是计划内的一次性切换，后果（新旧版本互不识别实例锁）已接受。
- 工程文档中的维护者本地路径需在远端阶段由用户手工更新。
- 更名版本发布后，需用存量旧版本实测一次「重定向渠道 → 新 manifest → 桥接升级」链路。
