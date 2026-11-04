#!/usr/bin/env bash
# Issue 自动修复的本地租约。用于防止中断的运行长期占用队列。
#
#   issue-lease.sh claim <issue> <run-id>
#   issue-lease.sh heartbeat <issue> <run-id>
#   issue-lease.sh release <issue> <run-id>
#   issue-lease.sh status
#   issue-lease.sh expire [--dry-run]
#
# 租约目录默认在 ~/.claude/scheduled-tasks/issue-fixer/leases，
# 可用 ISSUE_LEASE_DIR 覆盖；TTL 默认 1800 秒，可用
# ISSUE_LEASE_TTL_SECONDS 覆盖。
set -euo pipefail

LEASE_ROOT="${ISSUE_LEASE_DIR:-$HOME/.claude/scheduled-tasks/issue-fixer/leases}"
TTL="${ISSUE_LEASE_TTL_SECONDS:-1800}"

die() {
	echo "issue-lease: $*" >&2
	exit 1
}

usage() {
	sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
}

meta_file() {
	printf '%s/issue-%s/meta' "$LEASE_ROOT" "$1"
}

read_meta() {
	local file key
	file="$(meta_file "$1")"
	key="$2"
	[ -f "$file" ] || return 0
	awk -F= -v k="$key" '$1 == k { print $2; exit }' "$file" 2>/dev/null || true
}

write_meta() {
	local issue run started
	issue="$1"
	run="$2"
	started="$3"
	cat >"$(meta_file "$issue")" <<EOF
run=$run
started=$started
heartbeat=$(date +%s)
EOF
	chmod 600 "$(meta_file "$issue")"
}

lease_age() {
	local issue heartbeat now
	issue="$1"
	heartbeat="$(read_meta "$issue" heartbeat)"
	[ -n "$heartbeat" ] || heartbeat="$(read_meta "$issue" started)"
	[ -n "$heartbeat" ] || heartbeat=0
	now="$(date +%s)"
	printf '%s' "$((now - heartbeat))"
}

validate_issue() {
	[[ "${1:-}" =~ ^[0-9]+$ ]] || die "issue 必须是数字"
}

cmd="${1:-}"
case "$cmd" in
claim)
	issue="${2:-}"
	run="${3:-}"
	validate_issue "$issue"
	[ -n "$run" ] || die "claim 需要 run-id"
	mkdir -p "$LEASE_ROOT"
	chmod 700 "$LEASE_ROOT"
	lease="$LEASE_ROOT/issue-$issue"
	if mkdir "$lease" 2>/dev/null; then
		chmod 700 "$lease"
		write_meta "$issue" "$run" "$(date +%s)"
		echo "claimed issue=$issue run=$run"
		exit 0
	fi
	current="$(read_meta "$issue" run)"
	age="$(lease_age "$issue")"
	if [ "$age" -ge "$TTL" ]; then
		rm -rf "$lease"
		if mkdir "$lease" 2>/dev/null; then
			chmod 700 "$lease"
			write_meta "$issue" "$run" "$(date +%s)"
			echo "claimed-expired issue=$issue run=$run previous=${current:-unknown} age=${age}s"
			exit 0
		fi
	fi
	echo "busy issue=$issue run=${current:-unknown} age=${age}s ttl=${TTL}s"
	exit 1
	;;
heartbeat)
	issue="${2:-}"
	run="${3:-}"
	validate_issue "$issue"
	[ -n "$run" ] || die "heartbeat 需要 run-id"
	current="$(read_meta "$issue" run)"
	[ "$current" = "$run" ] || die "issue=$issue 不属于 run=$run（当前 ${current:-无}）"
	started="$(read_meta "$issue" started)"
	write_meta "$issue" "$run" "${started:-$(date +%s)}"
	echo "heartbeat issue=$issue run=$run age=0s"
	;;
release)
	issue="${2:-}"
	run="${3:-}"
	validate_issue "$issue"
	current="$(read_meta "$issue" run)"
	if [ -n "$run" ] && [ "$current" != "$run" ]; then
		die "拒绝释放：issue=$issue 当前 run=${current:-无}，请求 run=$run"
	fi
	rm -rf "$LEASE_ROOT/issue-$issue"
	echo "released issue=$issue"
	;;
status)
	[ -d "$LEASE_ROOT" ] || exit 0
	for lease in "$LEASE_ROOT"/issue-*; do
		[ -d "$lease" ] || continue
		issue="${lease##*/issue-}"
		run="$(read_meta "$issue" run)"
		age="$(lease_age "$issue")"
		if [ "$age" -ge "$TTL" ]; then
			expired=yes
		else
			expired=no
		fi
		echo "issue=$issue run=${run:-unknown} age=${age}s ttl=${TTL}s expired=$expired"
	done
	;;
expire)
	mode="${2:-}"
	[ -d "$LEASE_ROOT" ] || exit 0
	for lease in "$LEASE_ROOT"/issue-*; do
		[ -d "$lease" ] || continue
		issue="${lease##*/issue-}"
		age="$(lease_age "$issue")"
		[ "$age" -ge "$TTL" ] || continue
		run="$(read_meta "$issue" run)"
		if [ "$mode" = "--dry-run" ]; then
			echo "expired issue=$issue run=${run:-unknown} age=${age}s"
		else
			rm -rf "$lease"
			echo "expired-removed issue=$issue run=${run:-unknown} age=${age}s"
		fi
	done
	;;
*)
	usage
	exit 1
	;;
esac