#!/usr/bin/env bash
# 打印 issue 自动修复队列快照，供每轮调度决策使用。只读，不修改任何状态。
set -euo pipefail
cd "$(dirname "$0")/.."

REPO="${ISSUE_REPO:-qq540491950/go-workbuddy}"
AUTHOR="${ISSUE_AUTHOR:-qq540491950}"

echo "== NOW =="
date -u '+%Y-%m-%dT%H:%M:%SZ epoch=%s'

echo
echo "== OPEN ISSUES (author=$AUTHOR) =="
gh issue list --repo "$REPO" --state open --author "$AUTHOR" --limit 100 \
	--json number,title,labels,createdAt,updatedAt \
	--jq 'sort_by(.createdAt) | .[] |
		[.number, ([.labels[].name] | join(",")), .createdAt, .updatedAt, .title] |
		@tsv'

echo
echo "== OPEN AUTO PRS =="
gh pr list --repo "$REPO" --state open --label agent-fix --limit 100 \
	--json number,title,headRefName,labels,mergeable,mergeStateStatus,updatedAt,statusCheckRollup \
	--jq '.[] |
		[.number, .headRefName, (.mergeable // "-"), (.mergeStateStatus // "-"),
		 ([.labels[].name] | join(",")),
		 (([.statusCheckRollup[]? | (.conclusion // .state // "unknown")] |
			group_by(.) | map("\(.[0]):\(length)") | join(","))),
		 .title] | @tsv'

echo
echo "== PENDING RELEASE =="
gh issue list --repo "$REPO" --state open --author "$AUTHOR" \
	--label agent:pending-release --limit 100 \
	--json number,title,createdAt \
	--jq 'sort_by(.createdAt) | .[] | [.number, .createdAt, .title] | @tsv'

echo
echo "== LATEST RELEASE =="
gh release list --repo "$REPO" --limit 1 --json tagName,publishedAt \
	--jq '.[] | [.tagName, .publishedAt] | @tsv'

echo
echo "== MAIN CI =="
gh run list --repo "$REPO" --branch main --limit 3 \
	--json databaseId,headSha,status,conclusion,createdAt \
	--jq '.[] | [.databaseId, .headSha[0:7], .status, (.conclusion // "-"), .createdAt] | @tsv'

echo
echo "== LEASES =="
bash scripts/issue-lease.sh status || true