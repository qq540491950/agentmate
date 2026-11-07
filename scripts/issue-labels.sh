#!/usr/bin/env bash
# 创建/更新 issue 流水线使用的路由标签。可重复执行。
# 需要 gh 已登录并有仓库写权限。
set -euo pipefail

REPO="${ISSUE_REPO:-qq540491950/agentmate}"

while IFS='|' read -r name color desc; do
	[ -n "$name" ] || continue
	gh label create "$name" --repo "$REPO" --color "$color" \
		--description "$desc" --force >/dev/null
	echo "label $name"
done <<'EOF'
module:chat|1d76db|自动修复路由：chat 模块
module:agents|0e8a16|自动修复路由：agents 模块
module:teams|5319e7|自动修复路由：teams 模块
module:skills|fbca04|自动修复路由：skills 模块
module:mcp|006b75|自动修复路由：mcp 模块
module:models|b60205|自动修复路由：models 模块
module:settings|d93f0b|自动修复路由：settings 模块
module:workspace|c2e0c6|自动修复路由：workspace 模块
module:update|bfd4f2|自动修复路由：update 模块
module:other|ededed|自动修复路由：other 模块
priority:p0|b60205|阻塞性问题，优先处理
agent:retryable|fbca04|失败可自动重试，30 分钟后回队
agent:permanent|b60205|失败需要人工，不再自动重试
agent:ready-to-merge|0e8a16|PR 检查全绿且可合入
agent-fix|1d76db|自动修复流水线创建的 PR
EOF