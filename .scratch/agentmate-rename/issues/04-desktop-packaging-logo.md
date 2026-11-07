# 04: 桌面身份、打包资产与 Logo 接入

**What to build:** 桌面可执行身份整体换新：产品名 AgentMate、单实例锁名 com.agentmate.app、二进制名 agentmate；打包资产（plist / manifest / 安装器 / desktop）与更新器目标名测试期望同步；火花 A logo 替换应用图标与 favicon，托盘随二进制内嵌图标联动。完整构建产出带新身份与新图标的 agentmate.exe。

**Blocked by:** 02

**Status:** ready-for-agent

- [ ] 桌面身份配置与全部打包资产字面值一致、无旧名
- [ ] 应用图标与 favicon 为火花 A，托盘显示新图标
- [ ] `wails3 build` 产出 agentmate.exe；更新器测试期望更新后后端全绿
