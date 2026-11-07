# 03: 数据目录与持久键全新切换

**What to build:** 数据身份全新开始：应用数据目录改为 agentmate（零迁移逻辑，旧目录原样保留且新版本永不读取），默认工作区改为 Documents\AgentMateWorkspace（仅未配置时生效），ADK 会话状态键与前端本地存储风险确认键改为 agentmate.*；首次启动即全新 onboarding。

**Blocked by:** 02

**Status:** ready-for-agent

- [ ] 新数据目录创建于 agentmate，旧 workbuddy-agent 目录不被读取也不被删除
- [ ] 未配置工作区时默认落点为 AgentMateWorkspace
- [ ] 状态键与本地存储键均为 agentmate.*；后端全量测试全绿
