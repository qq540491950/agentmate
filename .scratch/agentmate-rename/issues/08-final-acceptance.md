# 08: 全仓验收与人工冒烟

**What to build:** 更名完成的最终证明：旧名全仓零残留（白名单仅历史迭代记录）、全量自动验证通过、人工冒烟覆盖常驻与数据能力；本地阶段到此完成，远端改名与推送按既定约定等用户单独指令。

**Blocked by:** 03, 04, 05, 06, 07

**Status:** ready-for-agent

- [ ] `rg -in workbuddy` 零残留（docs/iterations 白名单）
- [ ] 后端全量 + 前端三件套 + `wails3 build` 全绿
- [ ] 人工冒烟：托盘图标与提示、二次启动单实例转发、全新目录 onboarding、语义检索重建
