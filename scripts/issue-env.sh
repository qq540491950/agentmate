#!/usr/bin/env bash
# 供 issue 自动修复运行手册在每个 Bash 调用开头 source。
# 用法：source /Users/liruitao/Desktop/go-project/agentmate/scripts/issue-env.sh
#
# 提供 REPO / ROOT / STATE / LEASE / WT_ROOT / MAIN_WT / RUN。
# RUN 每轮固定：首次 source 时生成并写入 $STATE/run-id；后续调用复用。
# 新一轮开始前由运行手册显式重置（见 issue-fixer-prompt.md 第 0 节）。
export REPO="${ISSUE_REPO:-qq540491950/agentmate}"
export ROOT="${ISSUE_REPO_ROOT:-/Users/liruitao/Desktop/go-project/agentmate}"
export STATE="${ISSUE_STATE_DIR:-$HOME/.claude/scheduled-tasks/issue-fixer}"
export LEASE="$STATE/leases"
export WT_ROOT="${ISSUE_WORKTREE_ROOT:-$STATE/worktrees}"
export MAIN_WT="$WT_ROOT/main"
# 让子脚本使用同一份仓库、状态目录与租约目录
export ISSUE_REPO="$REPO"
export ISSUE_STATE_DIR="$STATE"
export ISSUE_LEASE_DIR="$LEASE"

mkdir -p "$STATE" "$LEASE" "$WT_ROOT"
chmod 700 "$STATE" "$LEASE" "$WT_ROOT" 2>/dev/null || true

if [ -f "$STATE/run-id" ]; then
	RUN="$(cat "$STATE/run-id" 2>/dev/null || true)"
fi
if [ -z "${RUN:-}" ]; then
	RUN="$(date -u +%Y%m%dT%H%M%SZ)"
	printf '%s\n' "$RUN" >"$STATE/run-id"
	chmod 600 "$STATE/run-id"
fi
export RUN