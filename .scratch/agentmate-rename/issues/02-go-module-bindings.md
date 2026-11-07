# 02: Go module 更名与绑定再生成

**What to build:** 代码身份一次性切换为 agentmate：module 声明与全部 import 统一更名，绑定由构建自动重生成（第三方 wails-typed-events 绑定目录保留），前端手写 import 与浏览器视觉桩的硬编码方法 ID 同步；端到端 IPC 与浏览器视觉回归全部正常。

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] 全仓 module 引用统一为 agentmate，旧名零残留（历史迭代记录白名单除外）
- [ ] 绑定重生成成功且产物目录随新 module，第三方绑定目录完好
- [ ] 后端 vet + 全量测试全绿；前端三件套全绿；视觉桩方法 ID 命中
