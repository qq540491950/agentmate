#!/usr/bin/env bash
# release-train.sh — 发布列车的决策器。
#
#   scripts/release-train.sh plan
#
# 由 .github/workflows/release.yml 的 plan job 调用。只读 git / GitHub 状态，
# 不创建 tag、不发版；真正的 tag、Release 与 issue 关闭在 publish job 中完成。
#
# 输入环境变量：
#   EVENT_NAME                    push | workflow_run | schedule | workflow_dispatch
#   REF_NAME                      push tag 的 ref 名
#   INPUT_TAG                     workflow_dispatch 的 tag 输入
#   WR_CONCLUSION                 workflow_run.conclusion
#   WR_EVENT                      workflow_run.event
#   WR_HEAD_BRANCH                workflow_run.head_branch
#   WR_HEAD_SHA                   workflow_run.head_sha
#   REPO                          owner/name（默认 GITHUB_REPOSITORY）
#   ISSUE_STATE_DIR               快照目录（默认 $RUNNER_TEMP/issue-fixer）
#   RELEASE_INTERVAL_MINUTES      常规合并窗口，默认 30
#   RELEASE_P0_INTERVAL_MINUTES   P0 合并窗口，默认 15
#   RELEASE_RETRY_COOLDOWN_MINUTES 未发布 tag 的重试冷却，默认 30
#   RELEASE_MAX_RETRIES           未发布 tag 的最大失败次数，默认 3
#   GITHUB_OUTPUT                 GitHub Actions 输出文件（存在则写入）
#
# 输出：should_release=true|false、tag、sha、reason（stdout 与 GITHUB_OUTPUT）。
set -euo pipefail
cd "$(dirname "$0")/.."

repo="${REPO:-${GITHUB_REPOSITORY:-}}"
event="${EVENT_NAME:-}"
state="${ISSUE_STATE_DIR:-${RUNNER_TEMP:-/tmp}/issue-fixer}"

interval="${RELEASE_INTERVAL_MINUTES:-30}"
p0_interval="${RELEASE_P0_INTERVAL_MINUTES:-15}"
retry_cooldown="${RELEASE_RETRY_COOLDOWN_MINUTES:-30}"
max_retries="${RELEASE_MAX_RETRIES:-3}"

die() {
	echo "release-train: $*" >&2
	exit 2
}

need_int() {
	printf '%s' "$1" | grep -Eq '^[0-9]+$' || die "非法数值：$1"
}

need_int "$interval"
need_int "$p0_interval"
need_int "$retry_cooldown"
need_int "$max_retries"
[ -n "$repo" ] || die "REPO 未设置"

mkdir -p "$state"

emit() {
	if [ -n "${GITHUB_OUTPUT:-}" ]; then
		printf '%s=%s\n' "$1" "$2" >>"$GITHUB_OUTPUT"
	fi
	printf '%s=%s\n' "$1" "$2"
}

stop() { # stop <reason>
	emit should_release false
	emit tag ""
	emit sha ""
	emit reason "$1"
	echo "release-train: 跳过（$1）"
	exit 0
}

go() { # go <tag> <sha> <reason>
	emit should_release true
	emit tag "$1"
	emit sha "$2"
	emit reason "$3"
	echo "release-train: 发版 tag=$1 sha=$2 reason=$3"
	exit 0
}

parse_epoch() { # parse_epoch <ISO8601>：GNU date 与 BSD date 都支持
	local input="$1" epoch
	if epoch="$(date -u -d "$input" +%s 2>/dev/null)"; then
		printf '%s' "$epoch"
		return 0
	fi
	if epoch="$(TZ=UTC date -j -f "%Y-%m-%dT%H:%M:%SZ" "$input" +%s 2>/dev/null)"; then
		printf '%s' "$epoch"
		return 0
	fi
	return 1
}

snapshot() {
	ISSUE_STATE_DIR="$state" ISSUE_REPO="$repo" \
		bash scripts/issue-release.sh snapshot >/dev/null ||
		stop snapshot-failed
}

git fetch origin main --tags --quiet || stop fetch-failed
main_sha="$(git rev-parse origin/main)"

# 人工触发：push tag 或 workflow_dispatch 指定已有 tag，直接发布，不做窗口判断。
case "$event" in
push)
	tag="${REF_NAME:-}"
	[ -n "$tag" ] || stop missing-tag
	git rev-parse --verify "refs/tags/$tag^{commit}" >/dev/null 2>&1 || stop tag-missing
	go "$tag" "$(git rev-parse "refs/tags/$tag^{commit}")" manual-tag
	;;
workflow_dispatch)
	tag="${INPUT_TAG:-}"
	[ -n "$tag" ] || stop missing-input-tag
	git fetch origin "refs/tags/$tag:refs/tags/$tag" --quiet 2>/dev/null || true
	git rev-parse --verify "refs/tags/$tag^{commit}" >/dev/null 2>&1 || stop tag-missing
	go "$tag" "$(git rev-parse "refs/tags/$tag^{commit}")" manual-dispatch
	;;
workflow_run)
	[ "${WR_CONCLUSION:-}" = "success" ] || stop ci-not-success
	[ "${WR_EVENT:-}" = "push" ] || stop ci-not-push
	[ "${WR_HEAD_BRANCH:-}" = "main" ] || stop ci-not-main
	target_sha="${WR_HEAD_SHA:-}"
	[ -n "$target_sha" ] || stop missing-ci-sha
	;;
schedule)
	target_sha="$main_sha"
	;;
*)
	stop "unsupported-event:${event:-<empty>}"
	;;
esac

git merge-base --is-ancestor "$target_sha" "$main_sha" 2>/dev/null ||
	stop target-not-on-main

latest_tag="$(git tag --list 'v[0-9]*' --sort=-v:refname | head -n 1)"

# 最高 tag 已存在但 Release 缺失 / 资产不全：重试同一 tag，避免浪费版本号。
if [ -n "$latest_tag" ]; then
	assets=""
	if ! assets="$(gh release view "$latest_tag" --repo "$repo" --json assets \
		--jq '[.assets[].name] | join(",")' 2>/dev/null)"; then
		assets=""
	fi
	if ! printf '%s' "$assets" | grep -q 'manifest.json'; then
		tag_sha="$(git rev-parse "refs/tags/$latest_tag^{commit}" 2>/dev/null || true)"
		[ -n "$tag_sha" ] || stop retry-tag-unreadable
		git merge-base --is-ancestor "$tag_sha" "$main_sha" 2>/dev/null ||
			stop retry-tag-not-on-main
		since="$(git for-each-ref --format='%(creatordate:short)' "refs/tags/$latest_tag")"
		[ -n "$since" ] || since="$(date -u +%Y-%m-%d)"
		if ! attempts="$(gh run list --repo "$repo" --workflow Release \
			--created ">=${since}" --limit 30 --json conclusion,createdAt 2>/dev/null)"; then
			stop release-runs-failed
		fi
		[ -n "$attempts" ] || attempts='[]'
		fails="$(printf '%s' "$attempts" |
			jq '[.[] | select(.conclusion == "failure")] | length' 2>/dev/null || echo 0)"
		if [ "$fails" -ge "$max_retries" ]; then
			stop retry-exhausted
		fi
		last_attempt="$(printf '%s' "$attempts" |
			jq -r '[.[] | select(.conclusion != null)] | sort_by(.createdAt) | last | (.createdAt // empty)' 2>/dev/null || true)"
		if [ -n "$last_attempt" ] && last_epoch="$(parse_epoch "$last_attempt")"; then
			if [ $(( $(date +%s) - last_epoch )) -lt $((retry_cooldown * 60)) ]; then
				stop retry-cooldown
			fi
		fi
		snapshot
		go "$latest_tag" "$tag_sha" retry-unpublished-tag
	fi
fi

# 构建失败发生在 tag 创建之前时，没有“未发布 tag”可供重试分支识别：用最近
# 已完成的 Release run 做通用冷却与连续失败上限，避免每 15 分钟重复烧构建。
if ! recent_completed="$(gh run list --repo "$repo" --workflow Release \
	--status completed --limit 10 --json conclusion,createdAt 2>/dev/null)"; then
	stop release-runs-failed
fi
last_conclusion="$(printf '%s' "$recent_completed" |
	jq -r '.[0].conclusion // ""' 2>/dev/null || true)"
last_created="$(printf '%s' "$recent_completed" |
	jq -r '.[0].createdAt // ""' 2>/dev/null || true)"
consecutive_fails=0
while IFS= read -r conclusion; do
	[ "$conclusion" = "failure" ] || break
	consecutive_fails=$((consecutive_fails + 1))
done < <(printf '%s' "$recent_completed" | jq -r '.[].conclusion // ""')
if [ "$consecutive_fails" -ge "$max_retries" ]; then
	stop retry-exhausted
fi
if [ "$last_conclusion" = "failure" ] && [ -n "$last_created" ] &&
	last_epoch="$(parse_epoch "$last_created")" &&
	[ $(( $(date +%s) - last_epoch )) -lt $((retry_cooldown * 60)) ]; then
	stop retry-cooldown
fi

# schedule 兜底触发时，当前 main HEAD 的 CI 必须已经成功；workflow_run 触发
# 已经保证过这一点，不再重复查询。
if [ "$event" = "schedule" ]; then
	runs="$(gh run list --repo "$repo" --workflow CI --branch main --limit 30 \
		--json headSha,status,conclusion 2>/dev/null || true)"
	[ -n "$runs" ] || stop ci-list-failed
	exact="$(printf '%s' "$runs" | jq -r --arg sha "$target_sha" \
		'map(select(.headSha == $sha)) | .[0] | if . == null then "" else "\(.status)\t\(.conclusion // "")" end')"
	if [ -n "$exact" ]; then
		status="${exact%%$'\t'*}"
		conclusion="${exact##*$'\t'}"
		[ "$status" = "completed" ] && [ "$conclusion" = "success" ] ||
			stop ci-not-green
	else
		# 纯文档 HEAD 可能没有对应 run：回退到最近一个 headSha 是 HEAD 祖先
		# 的 main run，并要求它成功。
		ok=0
		while IFS=$'\t' read -r sha status conclusion; do
			[ -n "$sha" ] || continue
			[ "$status" = "completed" ] || continue
			if git merge-base --is-ancestor "$sha" "$target_sha" 2>/dev/null; then
				[ "$conclusion" = "success" ] && ok=1
				break
			fi
		done < <(printf '%s' "$runs" |
			jq -r '.[] | [.headSha, .status, (.conclusion // "none")] | @tsv')
		[ "$ok" = "1" ] || stop ci-not-green
	fi
fi

pending_json="$(gh issue list --repo "$repo" --state open \
	--author "${repo%%/*}" --label agent:pending-release --limit 100 \
	--json number,labels 2>/dev/null || true)"
[ -n "$pending_json" ] || stop pending-list-failed
pending_count="$(printf '%s' "$pending_json" | jq 'length' 2>/dev/null || echo 0)"
[ "$pending_count" -gt 0 ] || stop no-pending

snapshot
snapshot_file="$state/release-snapshot.tsv"
[ -f "$snapshot_file" ] || stop snapshot-missing

ready_count=0
while IFS= read -r line; do
	number="${line%%$'\t'*}"
	rest="${line#*$'\t'}"
	sha="${rest%%$'\t'*}"
	[ -n "$number" ] || continue
	[ -n "$sha" ] || continue
	if git merge-base --is-ancestor "$sha" "$target_sha" 2>/dev/null; then
		ready_count=$((ready_count + 1))
	fi
done <"$snapshot_file"
[ "$ready_count" -gt 0 ] || stop no-fix-commit

# 任一待发版票是 P0，就走最短窗口。P0 是否缺少 fix-commit 不影响窗口判断：
# 即使本批无法关闭它，也应尽快把其他修复发出去。
p0=0
while IFS=$'\t' read -r number labels; do
	[ -n "$number" ] || continue
	case ",$labels," in
	*,priority:p0,*)
		p0=1
		break
		;;
	esac
done < <(printf '%s' "$pending_json" |
	jq -r '.[] | [.number, ([.labels[].name] | join(","))] | @tsv')

latest_pub=""
if ! latest_pub="$(gh release list --repo "$repo" --limit 1 \
	--json publishedAt --jq '.[0].publishedAt // ""' 2>/dev/null)"; then
	stop release-list-failed
fi
if [ -n "$latest_pub" ]; then
	last_epoch="$(parse_epoch "$latest_pub")" || stop bad-release-date
	window="$interval"
	if [ "$p0" = "1" ]; then
		window="$p0_interval"
	fi
	if [ $(( $(date +%s) - last_epoch )) -lt $((window * 60)) ]; then
		stop not-due
	fi
fi

if [ -n "$latest_tag" ] &&
	[ "$(git rev-list --count "$latest_tag".."$target_sha")" -eq 0 ]; then
	stop no-new-commits
fi

next_tag="$(bash scripts/next-patch-tag.sh)" || stop next-tag-failed
go "$next_tag" "$target_sha" auto