#!/usr/bin/env bash
# release-notes.sh — 由 git tag 驱动，生成发布说明与建议版本号。
#
#   scripts/release-notes.sh --next            建议下一个版本号 + 说明
#   scripts/release-notes.sh <prev> <tag>      生成 <tag> 的说明（CI 发布步骤）
#   scripts/release-notes.sh <tag>             生成 <tag> 的说明（prev = 上一个 tag）
#
# 有附注 tag 消息时优先使用它（内容由你决定），否则使用范围内的约定式提交
# 主题，按类型分组并本地化为中文。这里刻意不使用 iteration-log.md：
# 它是粒度远细于用户发布说明的开发日志。
set -euo pipefail
cd "$(dirname "$0")/.."

classify() {
	# 将 <subject> 分类为 "feat" | "fix" | "other"
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
	# 使用 tformat（而不是 format）也会终止最后一条提交：`while read` 会丢弃
	# 末尾没有换行的最后一行，曾导致每条生成的发布说明都静默丢失范围内最旧的提交。
	local log_args=(--no-merges --pretty=tformat:%s)
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

# tag_message 返回完整的附注 tag 消息（主题和正文）：
# `%(contents:subject)` 会静默丢弃第一行之后的所有内容，而那正是人们实际
# 手写的多段说明。
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
		# 尚无 tag：从应用当前声明的版本（build/config.yml）递增，绝不从 0.0.0
		# 开始——首个 tag 若低于已发布版本，会成为所有客户端都忽略的降级。
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
	done < <(git log "$range" --no-merges --pretty=tformat:'%s')
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

TAG_ARG="${2:-${1:-}}"
if [ -z "$TAG_ARG" ]; then
	echo "release-notes: no tag given" >&2
	exit 2
fi
# 两个参数 =（上一个 tag，本 tag），发布任务即如此调用。一个参数 = 仅本 tag，
# 上一个 tag 自动推导。两条路径都必须给 PREV 赋值：脚本以 `set -u` 运行，
# 未设置的 PREV 会直接报错，而不是当作空字符串。
if [ "$#" -ge 2 ]; then
	PREV="${1:-}"
else
	PREV="$(prev_tag_of "$TAG_ARG")"
fi
# Git ref 保留 "v" 前缀；展示和 semver 比较不保留。这里只解析一次 ref，
# 让 `v0.5.0` 和 `0.5.0` 都能作为参数使用。
if git rev-parse --verify --quiet "refs/tags/${TAG_ARG}" >/dev/null; then
	TAG_REF="$TAG_ARG"
elif git rev-parse --verify --quiet "refs/tags/v${TAG_ARG}" >/dev/null; then
	TAG_REF="v${TAG_ARG}"
else
	echo "release-notes: tag ${TAG_ARG} not found locally (fetch tags first)" >&2
	exit 2
fi
TAG="${TAG_REF#v}"
if [ -n "$PREV" ] && ! git rev-parse --verify --quiet "refs/tags/${PREV}" >/dev/null; then
	PREV="v${PREV}"
fi

echo "## AgentMate v${TAG}"
echo
msg="$(tag_message "$TAG_REF" 2>/dev/null || true)"
if printf '%s' "$msg" | grep -Eq '^v?[0-9]+\.[0-9]+\.[0-9]+$'; then
	msg="" # a bare-version tag message carries no information
fi
if [ -n "$msg" ]; then
	printf '%s\n\n' "$msg"
else
	if [ -n "$PREV" ]; then
		range_notes "${PREV}..${TAG_REF}"
	else
		# 首次发布：没有上一个 tag，因此「截至 TAG 的全部内容」会倾倒整个开发
		# 历史。只汇总近期工作并明确说明，而不是假装本次发布包含全部内容。
		echo "（首次发布：未指定 tag 说明，以下仅列出最近 40 条提交）"
		echo
		range_notes "$TAG_REF" 40
	fi
fi
echo "---"
echo
echo "校验：下载 \`SHA256SUMS\` 后执行 \`sha256sum -c SHA256SUMS\`。"
echo "macOS 未签名说明：首次打开请右键 →「打开」；Linux 的 .deb 安装版请手动下载升级（AppImage 版可自动更新）。"
