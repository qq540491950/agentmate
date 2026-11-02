#!/usr/bin/env bash
# release-notes.sh — release notes + suggested version, driven by the git tag.
#
#   scripts/release-notes.sh --next            suggest the next version + notes
#   scripts/release-notes.sh <prev> <tag>      notes for <tag> (CI publish step)
#   scripts/release-notes.sh <tag>             notes for <tag> (prev = previous tag)
#
# Notes come from the annotated tag message when it has one (write whatever you
# want there), otherwise from the conventional-commit subjects in the range,
# grouped and localised in Chinese. iteration-log.md is deliberately NOT used:
# it is a development log at a much finer granularity than a user-facing release.
set -euo pipefail
cd "$(dirname "$0")/.."

classify() {
	# classify <subject> -> "feat" | "fix" | "other"
	case "$1" in
	feat*:*) echo feat ;;
	fix*:* | perf*:*) echo fix ;;
	*) echo other ;;
	esac
}

strip_prefix() {
	# "feat(chat): 主题 — 要点" -> "主题 — 要点"
	printf '%s' "$1" | sed -E 's/^[a-z]+(\([^)]*\))?!?:[[:space:]]*//'
}

range_notes() {
	local range="$1" limit="${2:-}" feat=() fix=() other=() s kind
	local log_args=(--no-merges --pretty=format:%s)
	if [ -n "$limit" ]; then
		log_args=(-n "$limit" "${log_args[@]}")
	fi
	while IFS= read -r s; do
		[ -n "$s" ] || continue
		case "$s" in
		"Merge "* | "chore(release)":*) continue ;;
		esac
		kind="$(classify "$s")"
		case "$kind" in
		feat) feat+=("- $(strip_prefix "$s")") ;;
		fix) fix+=("- $(strip_prefix "$s")") ;;
		other) other+=("- $(strip_prefix "$s")") ;;
		esac
	done < <(git log "$range" "${log_args[@]}")

	if [ ${#feat[@]} -gt 0 ]; then
		printf '### 新增\n\n'
		printf '%s\n' "${feat[@]}"
		printf '\n'
	fi
	if [ ${#fix[@]} -gt 0 ]; then
		printf '### 修复与优化\n\n'
		printf '%s\n' "${fix[@]}"
		printf '\n'
	fi
	if [ ${#other[@]} -gt 0 ]; then
		printf '### 其他\n\n'
		printf '%s\n' "${other[@]}"
		printf '\n'
	fi
}

# tag_message returns the whole annotated-tag message (subject AND body):
# `%(contents:subject)` silently drops everything after the first line, which
# is exactly the multi-paragraph hand-written notes people actually write.
tag_message() {
	git for-each-ref --format='%(contents)' "refs/tags/$1" 2>/dev/null |
		sed -e :a -e '/^\n*$/{$d;N;ba' -e '}' || true
}

prev_tag_of() {
	git describe --tags --abbrev=0 "$1^" 2>/dev/null || echo ""
}

if [ "${1:-}" = "--next" ]; then
	last="$(git describe --tags --abbrev=0 2>/dev/null || echo "")"
	if [ -n "$last" ]; then
		range="$last..HEAD"
		base="${last#v}"
	else
		range="HEAD"
		# No tags yet: bump from the version the app currently claims to be
		# (build/config.yml), never from 0.0.0 — a first tag below the already
		# shipped version would be a downgrade every client ignores.
		base="$(sed -nE 's/^  version: "([^"]*)".*/\1/p' build/config.yml | head -1)"
		[ -n "$base" ] || base="0.0.0"
	fi
	major="${base%%.*}"
	rest="${base#*.}"
	minor="${rest%%.*}"
	patch="${rest#*.}"
	patch="${patch%%[!0-9]*}"
	has_feat=0
	has_fix=0
	while IFS= read -r s; do
		case "$s" in
		feat*:*) has_feat=1 ;;
		fix*:* | perf*:*) has_fix=1 ;;
		esac
	done < <(git log "$range" --no-merges --pretty=format:'%s')
	if [ "$has_feat" = 1 ]; then
		minor=$((minor + 1))
		patch=0
	elif [ "$has_fix" = 1 ]; then
		patch=$((patch + 1))
	else
		patch=$((patch + 1))
	fi
	echo "suggested version: v${major}.${minor}.${patch}   (from ${last:-<no tags>})"
	echo
	echo "--- notes preview ---"
	range_notes "$range"
	echo "--- when it looks right ---"
	echo "git tag -a v${major}.${minor}.${patch} -m \"<optional hand-written notes>\" && git push origin v${major}.${minor}.${patch}"
	exit 0
fi

TAG="${2:-${1:-}}"
PREV="${1:-}"
[ "$#" -ge 2 ] || PREV="$(prev_tag_of "$TAG")"
TAG="${TAG#v}"
if [ -z "$TAG" ]; then
	echo "release-notes: no tag given" >&2
	exit 2
fi

echo "## WorkBuddy Agent v${TAG}"
echo
msg="$(tag_message "$TAG" 2>/dev/null || true)"
if printf '%s' "$msg" | grep -Eq '^v?[0-9]+\.[0-9]+\.[0-9]+$'; then
	msg="" # a bare-version tag message carries no information
fi
if [ -n "$msg" ]; then
	printf '%s\n\n' "$msg"
else
	if [ -n "$PREV" ]; then
		range_notes "${PREV}..${TAG}"
	else
		# First release: there is no previous tag, so "everything up to TAG"
		# would dump the entire development history. Summarise recent work and
		# say so, instead of pretending this release contains all of it.
		echo "（首次发布：未指定 tag 说明，以下仅列出最近 40 条提交）"
		echo
		range_notes "$TAG" 40
	fi
fi
echo "---"
echo
echo "校验：下载 \`SHA256SUMS\` 后执行 \`sha256sum -c SHA256SUMS\`。"
echo "macOS 未签名说明：首次打开请右键 →「打开」；Linux 的 .deb 安装版请手动下载升级（AppImage 版可自动更新）。"
