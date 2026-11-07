#!/usr/bin/env bash
# 批量发版的快照与关闭逻辑。
#
#   issue-release.sh snapshot
#   issue-release.sh verify <tag>
#   issue-release.sh close <tag> [--dry-run]
#   issue-release.sh fail <reason> [--dry-run]
#   issue-release.sh clear
#
# 快照只包含快照时刻的 agent:pending-release issue，并记录每张票最近一条
# 「fix-commit: <sha>」评论。发版期间新加入的 pending 不会进入本批。
# close 只关闭 fix-commit 是 tag 祖先的 issue；找不到 fix-commit 的票保持
# pending，等下一轮补标记或人工处理。
# fail 只处理快照内的票：保留 agent:pending-release，加 agent:blocked 与
# agent:retryable，让发布列车按冷却时间自动重试同一 tag。
set -euo pipefail

REPO="${ISSUE_REPO:-qq540491950/agentmate}"
STATE_DIR="${ISSUE_STATE_DIR:-$HOME/.claude/scheduled-tasks/issue-fixer}"
SNAPSHOT="$STATE_DIR/release-snapshot.tsv"

die() {
	echo "issue-release: $*" >&2
	exit 1
}

usage() {
	sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//'
}

fix_commit_for() {
	local issue body
	issue="$1"
	body="$(gh issue view "$issue" --repo "$REPO" --json comments \
		--jq '.comments[].body' 2>/dev/null || true)"
	printf '%s\n' "$body" |
		grep -Eo 'fix-commit: [0-9a-f]{7,40}' |
		tail -n 1 |
		awk '{print $2}' || true
}

cmd="${1:-}"
case "$cmd" in
snapshot)
	mkdir -p "$STATE_DIR"
	chmod 700 "$STATE_DIR"
	: >"$SNAPSHOT"
	tmp="$(mktemp "$STATE_DIR/release-snapshot.XXXXXX")"
	rows="$(gh issue list --repo "$REPO" --state open --author qq540491950 \
		--label agent:pending-release --limit 100 \
		--json number,title,createdAt \
		--jq 'sort_by(.createdAt) | .[] | [.number, .title] | @tsv')" ||
		die "gh issue list 失败"
	while IFS=$'\t' read -r number title; do
		[ -n "$number" ] || continue
		sha="$(fix_commit_for "$number")"
		printf '%s\t%s\t%s\n' "$number" "$sha" "$title" >>"$tmp"
		if [ -n "$sha" ]; then
			echo "snapshot issue=$number fix-commit=$sha title=$title"
		else
			echo "snapshot issue=$number fix-commit=MISSING title=$title"
		fi
	done <<<"$rows"
	mv "$tmp" "$SNAPSHOT"
	chmod 600 "$SNAPSHOT"
	echo "snapshot-file=$SNAPSHOT"
	;;
verify | close)
	tag="${2:-}"
	[ -n "$tag" ] || die "$cmd 需要 tag"
	dry_run=no
	[ "$cmd" = "verify" ] && dry_run=yes
	[ "${3:-}" = "--dry-run" ] && dry_run=yes
	[ -f "$SNAPSHOT" ] || die "没有快照，先运行 snapshot"
	git fetch origin main --quiet
	git fetch --tags origin --quiet
	git rev-parse --verify "refs/tags/$tag" >/dev/null 2>&1 ||
		die "tag $tag 不存在"
	ok=0
	skip=0
	while IFS= read -r line; do
		number="${line%%$'\t'*}"
		rest="${line#*$'\t'}"
		sha="${rest%%$'\t'*}"
		title="${rest#*$'\t'}"
		[ -n "$number" ] || continue
		if [ -z "$sha" ]; then
			echo "skip issue=$number reason=no-fix-commit"
			skip=$((skip + 1))
			continue
		fi
		if ! git cat-file -e "$sha^{commit}" 2>/dev/null; then
			echo "skip issue=$number reason=unknown-commit sha=$sha"
			skip=$((skip + 1))
			continue
		fi
		if ! git merge-base --is-ancestor "$sha" "refs/tags/$tag"; then
			echo "skip issue=$number reason=not-in-tag sha=$sha"
			skip=$((skip + 1))
			continue
		fi
		echo "close issue=$number fix-commit=$sha title=$title"
		ok=$((ok + 1))
		if [ "$dry_run" = "yes" ] || [ "$cmd" = "verify" ]; then
			continue
		fi
		body="$(printf '版本 `%s` 已发布，本批修复/功能已包含在该版本中。\n\n- 修复提交：`%s`\n- 更新方法：重启应用，或进入「设置 → 关于 → 检查更新」。\n\n如仍未解决，请重开并补充新的日志和复现步骤。' "$tag" "$sha")"
		gh issue comment "$number" --repo "$REPO" --body "$body"
		gh issue close "$number" --repo "$REPO" --reason completed || true
		gh issue edit "$number" --repo "$REPO" \
			--add-label agent:fixed \
			--remove-label agent:pending-release \
			--remove-label agent:blocked \
			--remove-label agent:retryable || true
	done <"$SNAPSHOT"
	echo "summary close=$ok skip=$skip tag=$tag dry-run=$dry_run"
	;;
fail)
	reason="${2:-未知原因}"
	dry_run=no
	[ "${3:-}" = "--dry-run" ] && dry_run=yes
	[ -f "$SNAPSHOT" ] || die "没有快照，先运行 snapshot"
	count=0
	while IFS= read -r line; do
		number="${line%%$'\t'*}"
		rest="${line#*$'\t'}"
		sha="${rest%%$'\t'*}"
		title="${rest#*$'\t'}"
		[ -n "$number" ] || continue
		if [ -z "$sha" ]; then
			echo "skip issue=$number reason=no-fix-commit"
			continue
		fi
		echo "fail issue=$number fix-commit=$sha title=$title"
		count=$((count + 1))
		[ "$dry_run" = "yes" ] && continue
		body="$(printf '本批发版失败：%s。\n\n修复提交 `%s` 仍在 main，已标记为可重试并保留在待发版队列；发布列车稍后会自动重试同一 tag，不需要人工改标签。' "$reason" "${sha:-unknown}")"
		gh issue comment "$number" --repo "$REPO" --body "$body"
		gh issue edit "$number" --repo "$REPO" \
			--add-label agent:blocked,agent:retryable || true
	done <"$SNAPSHOT"
	echo "summary failed=$count dry-run=$dry_run"
	;;
clear)
	rm -f "$SNAPSHOT"
	echo "cleared $SNAPSHOT"
	;;
*)
	usage
	exit 1
	;;
esac