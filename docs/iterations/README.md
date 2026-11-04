# 迭代记录片段

自动修复流水线中的 enhancement Worker 不直接修改
`docs/iteration-log.md`，而是把本轮记录写到本目录：

```text
docs/iterations/issue-<N>-<slug>.md
```

文件内容与 `iteration-log.md` 中的单轮记录一致，以
`### 第 N 轮迭代：<主题>（已实施）` 开头。Worker 不知道全局轮次时，先用
`第 ? 轮`，由 Integrator 在合并时替换为实际轮次。

Integrator 合并 PR 后：

1. 读取片段；
2. 把轮次占位符替换为 `iteration-log.md` 中最大轮次 +1；
3. 插入 `docs/iteration-log.md` 的「视觉测试与体验设计」章节顶部；
4. 删除片段文件。

这样多个 enhancement 并行时不会同时改同一个大文件。人工直接提交的迭代
仍然可以直接追加到 `iteration-log.md`。