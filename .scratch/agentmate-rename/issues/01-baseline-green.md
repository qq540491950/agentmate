# 01: 基线整备

**What to build:** 工作区恢复「可安全重构」状态：另一条工作线的未提交改动（UI 刷新）单独提交或 stash，与更名提交彻底隔离；在干净 main 基线上后端全量测试与前端三件套全部通过，作为更名零行为重构的对照基线。

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] 工作区无未提交改动（UI 刷新 WIP 已独立提交或 stash）
- [ ] `go vet ./internal/... .` 与 `go test ./internal/... .` 全绿
- [ ] 前端类型检查、vitest、构建三件套全部通过
